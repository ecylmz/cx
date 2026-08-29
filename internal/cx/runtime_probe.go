package cx

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	codexRuntimeProbeTimeout  = 8 * time.Second
	codexDaemonRestartTimeout = 20 * time.Second
)

type codexRuntimeAccount struct {
	CodexHome string
	Type      string
	Email     string
	Plan      string
}

func probeCodexRuntimeAccount() (codexRuntimeAccount, error) {
	return probeCodexAccount("app-server")
}

// probeCodexDaemonAccount talks to the shared local app-server daemon through
// Codex's own stdio proxy. This is the same daemon that Codex 0.151+ TUI may
// auto-connect to, so it can have a different in-memory auth snapshot from a
// freshly spawned `codex app-server` process after cx changes auth.json.
func probeCodexDaemonAccount() (codexRuntimeAccount, error) {
	return probeCodexAccount("app-server", "proxy")
}

func probeCodexAccount(args ...string) (codexRuntimeAccount, error) {
	path, err := exec.LookPath("codex")
	if err != nil {
		return codexRuntimeAccount{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), codexRuntimeProbeTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, path, args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return codexRuntimeAccount{}, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return codexRuntimeAccount{}, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return codexRuntimeAccount{}, err
	}
	defer func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()

	enc := json.NewEncoder(stdin)
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 1024), 4*1024*1024)

	if err := enc.Encode(map[string]any{
		"id":     91,
		"method": "initialize",
		"params": map[string]any{
			"clientInfo": map[string]any{"name": "cx", "title": "cx", "version": Version},
		},
	}); err != nil {
		return codexRuntimeAccount{}, err
	}
	initEnv, err := readResponse(scanner, 91)
	if err != nil {
		return codexRuntimeAccount{}, runtimeProbeError(strings.Join(args, " ")+" initialize", err, stderr.String())
	}
	var initResult struct {
		CodexHome string `json:"codexHome"`
	}
	if err := json.Unmarshal(initEnv.Result, &initResult); err != nil {
		return codexRuntimeAccount{}, fmt.Errorf("decode Codex initialize result: %w", err)
	}
	if err := enc.Encode(map[string]any{"method": "initialized", "params": map[string]any{}}); err != nil {
		return codexRuntimeAccount{}, err
	}
	if err := enc.Encode(map[string]any{
		"id":     92,
		"method": "account/read",
		"params": map[string]any{"refreshToken": false},
	}); err != nil {
		return codexRuntimeAccount{}, err
	}
	accountEnv, err := readResponse(scanner, 92)
	if err != nil {
		return codexRuntimeAccount{}, runtimeProbeError(strings.Join(args, " ")+" account/read", err, stderr.String())
	}
	var accountResult struct {
		Account *struct {
			Type     string  `json:"type"`
			Email    *string `json:"email"`
			PlanType string  `json:"planType"`
		} `json:"account"`
	}
	if err := json.Unmarshal(accountEnv.Result, &accountResult); err != nil {
		return codexRuntimeAccount{}, fmt.Errorf("decode Codex account/read result: %w", err)
	}
	out := codexRuntimeAccount{CodexHome: strings.TrimSpace(initResult.CodexHome)}
	if accountResult.Account != nil {
		out.Type = accountResult.Account.Type
		out.Plan = accountResult.Account.PlanType
		if accountResult.Account.Email != nil {
			out.Email = strings.TrimSpace(*accountResult.Account.Email)
		}
	}
	return out, nil
}

func runtimeProbeError(stage string, err error, stderr string) error {
	stderr = strings.TrimSpace(stderr)
	if len(stderr) > 300 {
		stderr = stderr[len(stderr)-300:]
	}
	if stderr != "" {
		return fmt.Errorf("Codex runtime %s: %w (%s)", stage, err, stderr)
	}
	return fmt.Errorf("Codex runtime %s: %w", stage, err)
}

func samePath(a, b string) bool {
	aa, errA := filepath.Abs(a)
	bb, errB := filepath.Abs(b)
	if errA != nil || errB != nil {
		return filepath.Clean(a) == filepath.Clean(b)
	}
	return filepath.Clean(aa) == filepath.Clean(bb)
}

func runtimeMatchesSelection(runtime codexRuntimeAccount, ident authIdentity) bool {
	if runtime.Type != "chatgpt" {
		return false
	}
	if ident.Email == "" {
		return true
	}
	return runtime.Email != "" && strings.EqualFold(runtime.Email, ident.Email)
}

func runtimeAccountLabel(runtime codexRuntimeAccount) string {
	if runtime.Email != "" {
		return runtime.Email
	}
	if runtime.Type != "" {
		return runtime.Type
	}
	return "no account"
}

func restartCodexDaemon() error {
	path, err := exec.LookPath("codex")
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), codexDaemonRestartTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "app-server", "daemon", "restart")
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return fmt.Errorf("Codex shared daemon restart timed out after %s", codexDaemonRestartTimeout)
	}
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if len(msg) > 400 {
			msg = msg[len(msg)-400:]
		}
		if msg != "" {
			return fmt.Errorf("restart Codex shared daemon: %w (%s)", err, msg)
		}
		return fmt.Errorf("restart Codex shared daemon: %w", err)
	}
	return nil
}

// reconcileCodexDaemonSelection handles Codex 0.151+'s shared local app-server
// daemon. The TUI may auto-connect to that daemon, whose AuthManager keeps the
// account that was active when the daemon started. A fresh `codex app-server`
// probe therefore is not sufficient to verify what the TUI will use.
//
// If no daemon is reachable (or the installed Codex predates the proxy
// command), this is a no-op. If a daemon is reachable and stale, restart the
// managed daemon so it reloads the newly selected auth.json, then verify the
// account through the daemon proxy itself.
func reconcileCodexDaemonSelection(a Account, ident authIdentity) error {
	daemon, err := probeCodexDaemonAccount()
	if err != nil {
		return nil
	}
	if runtimeMatchesSelection(daemon, ident) {
		return nil
	}

	previous := runtimeAccountLabel(daemon)
	if err := restartCodexDaemon(); err != nil {
		return fmt.Errorf(
			"Codex shared daemon still uses %s after switching to %s (%s): %w",
			previous,
			a.Name,
			ident.Email,
			err,
		)
	}

	daemon, err = probeCodexDaemonAccount()
	if err != nil {
		return fmt.Errorf("Codex shared daemon restarted for %s but could not be re-verified: %w", a.Name, err)
	}
	if runtimeMatchesSelection(daemon, ident) {
		return nil
	}
	return fmt.Errorf(
		"Codex shared daemon still resolves %s after restart; expected %s (%s)",
		runtimeAccountLabel(daemon),
		a.Name,
		ident.Email,
	)
}

// reconcileCodexRuntimeSelection verifies both auth paths used by modern Codex:
// a fresh app-server process and, when present, the shared local daemon that the
// interactive TUI can automatically reuse. This prevents cx from reporting a
// successful switch while a stale daemon still serves the previous account.
func reconcileCodexRuntimeSelection(p paths, a Account, ident authIdentity) error {
	if _, err := exec.LookPath("codex"); err != nil {
		return nil
	}

	runtime, err := probeCodexRuntimeAccount()
	if err == nil {
		effectiveHome := p.CodexHome
		if runtime.CodexHome != "" {
			effectiveHome = runtime.CodexHome
		}
		if !samePath(effectiveHome, p.CodexHome) {
			if err := configureCodexHomeSelection(p, effectiveHome, a); err != nil {
				return err
			}
			runtime, err = probeCodexRuntimeAccount()
			if err != nil {
				return fmt.Errorf("Codex runtime changed home to %s but could not be re-verified: %w", effectiveHome, err)
			}
		}
		if !runtimeMatchesSelection(runtime, ident) {
			// Re-assert file storage in the runtime-resolved home once. This repairs
			// stale keyring/auto-store setups when the runtime home was already known.
			if err := configureCodexHomeSelection(p, effectiveHome, a); err != nil {
				return err
			}
			runtime, err = probeCodexRuntimeAccount()
			if err != nil {
				return fmt.Errorf("Codex account verification failed after activating %s: %w", a.Name, err)
			}
			if !runtimeMatchesSelection(runtime, ident) {
				return fmt.Errorf(
					"Codex still resolves %s after switching to %s (%s); effective CODEX_HOME=%s; a higher-priority Codex auth/config source may be overriding cx",
					runtimeAccountLabel(runtime),
					a.Name,
					ident.Email,
					effectiveHome,
				)
			}
		}
	}

	return reconcileCodexDaemonSelection(a, ident)
}

func codexRuntimeSummary() (codexRuntimeAccount, bool) {
	if _, err := exec.LookPath("codex"); err != nil {
		return codexRuntimeAccount{}, false
	}
	runtime, err := probeCodexRuntimeAccount()
	if err != nil {
		return codexRuntimeAccount{}, false
	}
	return runtime, true
}

func codexDaemonSummary() (codexRuntimeAccount, bool) {
	if _, err := exec.LookPath("codex"); err != nil {
		return codexRuntimeAccount{}, false
	}
	runtime, err := probeCodexDaemonAccount()
	if err != nil {
		return codexRuntimeAccount{}, false
	}
	return runtime, true
}
