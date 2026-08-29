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

const codexRuntimeProbeTimeout = 8 * time.Second

type codexRuntimeAccount struct {
	CodexHome string
	Type      string
	Email     string
	Plan      string
}

func probeCodexRuntimeAccount() (codexRuntimeAccount, error) {
	path, err := exec.LookPath("codex")
	if err != nil {
		return codexRuntimeAccount{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), codexRuntimeProbeTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, path, "app-server")
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
		return codexRuntimeAccount{}, runtimeProbeError("initialize", err, stderr.String())
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
		return codexRuntimeAccount{}, runtimeProbeError("account/read", err, stderr.String())
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

// reconcileCodexRuntimeSelection verifies the account through the same Codex
// executable a user launches. This catches wrappers or pre-existing installs
// that resolve a different CODEX_HOME than cx initially assumed. Probe support
// is best-effort for older Codex versions, but a successfully observed account
// mismatch is treated as an error instead of falsely reporting a switch.
func reconcileCodexRuntimeSelection(p paths, a Account, ident authIdentity) error {
	if _, err := exec.LookPath("codex"); err != nil {
		return nil
	}

	runtime, err := probeCodexRuntimeAccount()
	if err != nil {
		return nil
	}

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
	if runtimeMatchesSelection(runtime, ident) {
		return nil
	}

	// Re-assert file storage in the runtime-resolved home once. This repairs
	// stale keyring/auto-store setups when the runtime home was already known.
	if err := configureCodexHomeSelection(p, effectiveHome, a); err != nil {
		return err
	}
	runtime, err = probeCodexRuntimeAccount()
	if err != nil {
		return fmt.Errorf("Codex account verification failed after activating %s: %w", a.Name, err)
	}
	if runtimeMatchesSelection(runtime, ident) {
		return nil
	}

	return fmt.Errorf(
		"Codex still resolves %s after switching to %s (%s); effective CODEX_HOME=%s; a higher-priority Codex auth/config source may be overriding cx",
		runtimeAccountLabel(runtime),
		a.Name,
		ident.Email,
		effectiveHome,
	)
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
