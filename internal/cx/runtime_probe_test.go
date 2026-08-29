package cx

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func installRuntimeProbeCodex(t *testing.T, script string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell helper")
	}
	bin := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(bin, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(bin, "codex")
	if err := os.WriteFile(path, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return path
}

func TestProbeCodexRuntimeAccount(t *testing.T) {
	runtimeHome := filepath.Join(t.TempDir(), "runtime-codex")
	t.Setenv("CX_RUNTIME_HOME", runtimeHome)
	installRuntimeProbeCodex(t, `#!/bin/sh
if [ "$1" != "app-server" ]; then exit 2; fi
IFS= read -r init
echo "{\"id\":91,\"result\":{\"codexHome\":\"$CX_RUNTIME_HOME\"}}"
IFS= read -r initialized
IFS= read -r request
echo '{"id":92,"result":{"account":{"type":"chatgpt","email":"x@example.com","planType":"plus"},"requiresOpenaiAuth":true}}'
while :; do sleep 1; done
`)

	got, err := probeCodexRuntimeAccount()
	if err != nil {
		t.Fatal(err)
	}
	if got.CodexHome != runtimeHome || got.Type != "chatgpt" || got.Email != "x@example.com" || got.Plan != "plus" {
		t.Fatalf("runtime=%+v", got)
	}
}

func TestSwitchAccountRepairsDifferentRuntimeHome(t *testing.T) {
	p := makeTestPaths(t)
	runtimeHome := filepath.Join(t.TempDir(), "runtime-codex")
	t.Setenv("CX_RUNTIME_HOME", runtimeHome)
	installRuntimeProbeCodex(t, `#!/bin/sh
if [ "$1" != "app-server" ]; then exit 2; fi
IFS= read -r init
echo "{\"id\":91,\"result\":{\"codexHome\":\"$CX_RUNTIME_HOME\"}}"
IFS= read -r initialized
IFS= read -r request
if [ -L "$CX_RUNTIME_HOME/auth.json" ]; then
  email='x@example.com'
else
  email='old@example.com'
fi
echo "{\"id\":92,\"result\":{\"account\":{\"type\":\"chatgpt\",\"email\":\"$email\",\"planType\":\"plus\"},\"requiresOpenaiAuth\":true}}"
while :; do sleep 1; done
`)

	a := Account{ID: "a", Name: "foo", AccountID: "acct", Email: "x@example.com", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	writeTestAccount(t, p, a)
	if err := switchAccount(p, a); err != nil {
		t.Fatal(err)
	}

	config, err := os.ReadFile(filepath.Join(runtimeHome, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(config), `cli_auth_credentials_store = "file"`) {
		t.Fatalf("runtime config=%q", config)
	}
	live := filepath.Join(runtimeHome, "auth.json")
	info, err := os.Lstat(live)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("runtime auth is not a symlink")
	}
	target, err := os.Readlink(live)
	if err != nil {
		t.Fatal(err)
	}
	if target != p.accountAuth(a.ID) {
		t.Fatalf("runtime auth target=%q want=%q", target, p.accountAuth(a.ID))
	}
	st, err := loadState(p)
	if err != nil || st.ActiveID != a.ID {
		t.Fatalf("state=%+v err=%v", st, err)
	}
}

func TestSwitchAccountReportsRuntimeMismatch(t *testing.T) {
	p := makeTestPaths(t)
	t.Setenv("CX_RUNTIME_HOME", p.CodexHome)
	installRuntimeProbeCodex(t, `#!/bin/sh
if [ "$1" != "app-server" ]; then exit 2; fi
IFS= read -r init
echo "{\"id\":91,\"result\":{\"codexHome\":\"$CX_RUNTIME_HOME\"}}"
IFS= read -r initialized
IFS= read -r request
echo '{"id":92,"result":{"account":{"type":"chatgpt","email":"old@example.com","planType":"plus"},"requiresOpenaiAuth":true}}'
while :; do sleep 1; done
`)

	a := Account{ID: "a", Name: "foo", AccountID: "acct", Email: "x@example.com", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	writeTestAccount(t, p, a)
	err := switchAccount(p, a)
	if err == nil || !strings.Contains(err.Error(), "Codex still resolves old@example.com") {
		t.Fatalf("expected verified runtime mismatch, got %v", err)
	}
	st, stateErr := loadState(p)
	if stateErr != nil {
		t.Fatal(stateErr)
	}
	if st.ActiveID != "" {
		t.Fatalf("failed switch must not update cx state: %+v", st)
	}
}

func TestSwitchAccountRestartsStaleSharedDaemon(t *testing.T) {
	p := makeTestPaths(t)
	marker := filepath.Join(t.TempDir(), "daemon-email")
	count := filepath.Join(t.TempDir(), "daemon-restarts")
	if err := os.WriteFile(marker, []byte("old@example.com\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CX_DAEMON_EMAIL", marker)
	t.Setenv("CX_DAEMON_RESTARTS", count)
	installRuntimeProbeCodex(t, `#!/bin/sh
if [ "$1" != "app-server" ]; then exit 2; fi
if [ "$2" = "daemon" ] && [ "$3" = "restart" ]; then
  echo 'x@example.com' > "$CX_DAEMON_EMAIL"
  n=0
  [ -f "$CX_DAEMON_RESTARTS" ] && n=$(cat "$CX_DAEMON_RESTARTS")
  echo $((n + 1)) > "$CX_DAEMON_RESTARTS"
  echo '{"status":"restarted"}'
  exit 0
fi
if [ "$2" = "proxy" ]; then
  email=$(cat "$CX_DAEMON_EMAIL")
else
  email='x@example.com'
fi
IFS= read -r init
echo "{\"id\":91,\"result\":{\"codexHome\":\"$CODEX_HOME\"}}"
IFS= read -r initialized
IFS= read -r request
echo "{\"id\":92,\"result\":{\"account\":{\"type\":\"chatgpt\",\"email\":\"$email\",\"planType\":\"plus\"},\"requiresOpenaiAuth\":true}}"
while :; do sleep 1; done
`)

	a := Account{ID: "a", Name: "backup", AccountID: "acct", Email: "x@example.com", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	writeTestAccount(t, p, a)
	if err := switchAccount(p, a); err != nil {
		t.Fatal(err)
	}

	b, err := os.ReadFile(count)
	if err != nil || strings.TrimSpace(string(b)) != "1" {
		t.Fatalf("daemon restart count=%q err=%v", b, err)
	}
	daemon, err := probeCodexDaemonAccount()
	if err != nil {
		t.Fatal(err)
	}
	if daemon.Email != "x@example.com" {
		t.Fatalf("daemon account=%+v", daemon)
	}
}

func TestSwitchAccountDoesNotRestartMatchingSharedDaemon(t *testing.T) {
	p := makeTestPaths(t)
	count := filepath.Join(t.TempDir(), "daemon-restarts")
	t.Setenv("CX_DAEMON_RESTARTS", count)
	installRuntimeProbeCodex(t, `#!/bin/sh
if [ "$1" != "app-server" ]; then exit 2; fi
if [ "$2" = "daemon" ] && [ "$3" = "restart" ]; then
  echo 1 > "$CX_DAEMON_RESTARTS"
  exit 0
fi
IFS= read -r init
echo "{\"id\":91,\"result\":{\"codexHome\":\"$CODEX_HOME\"}}"
IFS= read -r initialized
IFS= read -r request
echo '{"id":92,"result":{"account":{"type":"chatgpt","email":"x@example.com","planType":"plus"},"requiresOpenaiAuth":true}}'
while :; do sleep 1; done
`)

	a := Account{ID: "a", Name: "backup", AccountID: "acct", Email: "x@example.com", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	writeTestAccount(t, p, a)
	if err := switchAccount(p, a); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(count); !os.IsNotExist(err) {
		t.Fatalf("matching daemon should not restart: %v", err)
	}
}

func TestSwitchAccountFailsWhenStaleSharedDaemonCannotRestart(t *testing.T) {
	p := makeTestPaths(t)
	installRuntimeProbeCodex(t, `#!/bin/sh
if [ "$1" != "app-server" ]; then exit 2; fi
if [ "$2" = "daemon" ] && [ "$3" = "restart" ]; then
  echo 'app server is running but is not managed by codex app-server daemon' >&2
  exit 1
fi
if [ "$2" = "proxy" ]; then
  email='old@example.com'
else
  email='x@example.com'
fi
IFS= read -r init
echo "{\"id\":91,\"result\":{\"codexHome\":\"$CODEX_HOME\"}}"
IFS= read -r initialized
IFS= read -r request
echo "{\"id\":92,\"result\":{\"account\":{\"type\":\"chatgpt\",\"email\":\"$email\",\"planType\":\"plus\"},\"requiresOpenaiAuth\":true}}"
while :; do sleep 1; done
`)

	a := Account{ID: "a", Name: "backup", AccountID: "acct", Email: "x@example.com", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	writeTestAccount(t, p, a)
	err := switchAccount(p, a)
	if err == nil || !strings.Contains(err.Error(), "shared daemon still uses old@example.com") {
		t.Fatalf("expected daemon restart failure, got %v", err)
	}
	st, stateErr := loadState(p)
	if stateErr != nil {
		t.Fatal(stateErr)
	}
	if st.ActiveID != "" {
		t.Fatalf("failed daemon reconciliation must not update cx state: %+v", st)
	}
}
