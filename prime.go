package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// primeWeeklyWindow intentionally performs one real Codex turn. It is used
// only when the server reports an unused rolling weekly window. The turn is
// ephemeral, runs in an empty temporary directory with a read-only sandbox,
// and asks for a minimal response so it starts the window with minimal usage.
func primeWeeklyWindow(p paths, a Account) error {
	if _, err := exec.LookPath("codex"); err != nil {
		return errors.New("codex executable not found")
	}
	work, err := os.MkdirTemp("", "cx-prime-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	args := []string{
		"exec",
		"--ephemeral",
		"--ignore-user-config",
		"--ignore-rules",
		"--skip-git-repo-check",
		"--sandbox", "read-only",
		"--color", "never",
		"Reply exactly with OK and nothing else.",
	}
	cmd := exec.CommandContext(ctx, "codex", args...)
	cmd.Dir = work
	cmd.Env = append(os.Environ(), "CODEX_HOME="+p.accountDir(a.ID))
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return errors.New("weekly window start timed out")
		}
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		if len(msg) > 400 {
			msg = msg[len(msg)-400:]
		}
		if msg != "" {
			return fmt.Errorf("weekly window start failed: %w: %s", err, msg)
		}
		return fmt.Errorf("weekly window start failed: %w", err)
	}
	return nil
}
