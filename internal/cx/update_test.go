package cx

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPlatformAsset(t *testing.T) {
	tests := []struct {
		os, arch, want string
	}{
		{"darwin", "arm64", "cx-darwin-arm64"},
		{"darwin", "amd64", "cx-darwin-amd64"},
		{"linux", "amd64", "cx-linux-amd64"},
		{"linux", "arm64", "cx-linux-arm64"},
	}
	for _, tt := range tests {
		got, err := platformAsset(tt.os, tt.arch)
		if err != nil || got != tt.want {
			t.Fatalf("platformAsset(%q,%q) = %q, %v; want %q", tt.os, tt.arch, got, err, tt.want)
		}
	}
}

func TestPlatformAssetRejectsUnsupported(t *testing.T) {
	if _, err := platformAsset("windows", "amd64"); err == nil {
		t.Fatal("expected unsupported OS error")
	}
	if _, err := platformAsset("linux", "386"); err == nil {
		t.Fatal("expected unsupported arch error")
	}
}

func TestVerifySHA256(t *testing.T) {
	payload := []byte("hello")
	sums := []byte("2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824  cx-darwin-arm64\n")
	if err := verifySHA256("cx-darwin-arm64", payload, sums); err != nil {
		t.Fatal(err)
	}
	if err := verifySHA256("cx-darwin-arm64", []byte("changed"), sums); err == nil {
		t.Fatal("expected checksum mismatch")
	}
}

func TestGitHubTokenIsOptional(t *testing.T) {
	t.Setenv("GH_TOKEN", " gh-token ")
	t.Setenv("GITHUB_TOKEN", "other")
	if got := githubToken(); got != "gh-token" {
		t.Fatalf("token=%q", got)
	}
	t.Setenv("GH_TOKEN", "")
	if got := githubToken(); got != "other" {
		t.Fatalf("token=%q", got)
	}
	t.Setenv("GITHUB_TOKEN", "")
	if got := githubToken(); got != "" {
		t.Fatalf("anonymous token=%q", got)
	}
}

func TestGitHubReleaseHTTPFlowIsAnonymous(t *testing.T) {
	oldBase := githubAPIBase
	oldClient := githubHTTPClient
	defer func() { githubAPIBase = oldBase; githubHTTPClient = oldClient }()

	mux := http.NewServeMux()
	mux.HandleFunc("/repos/ecylmz/cx/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Errorf("unexpected authorization=%q", r.Header.Get("Authorization"))
		}
		if !strings.Contains(r.Header.Get("User-Agent"), "cx/") {
			t.Errorf("user-agent=%q", r.Header.Get("User-Agent"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"tag_name":"v9.9.9","assets":[{"id":7,"name":"cx-linux-amd64"},{"id":8,"name":"SHA256SUMS"}]}`)
	})
	mux.HandleFunc("/repos/ecylmz/cx/releases/assets/7", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Errorf("unexpected authorization=%q", r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte("payload"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	githubAPIBase = srv.URL
	githubHTTPClient = srv.Client()

	ctx := context.Background()
	rel, err := fetchLatestRelease(ctx, "")
	if err != nil || rel.TagName != "v9.9.9" || len(rel.Assets) != 2 {
		t.Fatalf("release=%+v err=%v", rel, err)
	}
	b, err := downloadReleaseAsset(ctx, "", 7)
	if err != nil || string(b) != "payload" {
		t.Fatalf("asset=%q err=%v", b, err)
	}
}

func TestGitHubLatestReleaseNotFound(t *testing.T) {
	oldBase := githubAPIBase
	oldClient := githubHTTPClient
	defer func() { githubAPIBase = oldBase; githubHTTPClient = oldClient }()

	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	githubAPIBase = srv.URL
	githubHTTPClient = srv.Client()
	if _, err := fetchLatestRelease(context.Background(), ""); err == nil || !strings.Contains(err.Error(), "no GitHub release found") {
		t.Fatalf("expected not-found error, got %v", err)
	}
}

func TestGitHubGETAndAtomicReplaceExecutable(t *testing.T) {
	oldClient := githubHTTPClient
	defer func() { githubHTTPClient = oldClient }()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != "application/test" || r.Header.Get("Authorization") != "Bearer t" {
			t.Errorf("headers=%v", r.Header)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()
	githubHTTPClient = srv.Client()
	body, status, err := githubGET(context.Background(), srv.URL, "t", "application/test")
	if err != nil || status != http.StatusCreated || string(body) != "ok" {
		t.Fatalf("body=%q status=%d err=%v", body, status, err)
	}

	target := filepath.Join(t.TempDir(), "cx")
	if err := os.WriteFile(target, []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := atomicReplaceExecutable(target, []byte("new-binary")); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(target)
	if err != nil || string(got) != "new-binary" {
		t.Fatalf("target=%q err=%v", got, err)
	}
	st, _ := os.Stat(target)
	if st.Mode().Perm()&0111 == 0 {
		t.Fatalf("updated executable is not executable: %o", st.Mode().Perm())
	}
}

func TestHandleUpdateRejectsArgsAndStopsWhenCurrent(t *testing.T) {
	if err := handleUpdate([]string{"--bad"}); err == nil {
		t.Fatal("expected usage error")
	}
	oldBase := githubAPIBase
	oldClient := githubHTTPClient
	oldVersion := Version
	defer func() { githubAPIBase = oldBase; githubHTTPClient = oldClient; Version = oldVersion }()
	Version = "1.2.3"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"tag_name":"v1.2.3","assets":[]}`)
	}))
	defer srv.Close()
	githubAPIBase = srv.URL
	githubHTTPClient = srv.Client()
	out := captureStdout(t, func() {
		if err := handleUpdate(nil); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "already current") {
		t.Fatalf("out=%q", out)
	}
}

func TestFormatBytesAndProgressLine(t *testing.T) {
	oldColor := useColor
	defer func() { useColor = oldColor }()
	useColor = false
	if got := formatBytes(1536); got != "1.5 KiB" {
		t.Fatalf("formatBytes=%q", got)
	}
	if got := formatBytes(3 << 20); got != "3.0 MiB" {
		t.Fatalf("formatBytes=%q", got)
	}
	line := downloadProgressLine(1<<20, 2<<20, time.Second)
	for _, want := range []string{"50%", "1.0 MiB / 2.0 MiB", "1.0 MiB/s"} {
		if !strings.Contains(line, want) {
			t.Fatalf("progress line missing %q: %q", want, line)
		}
	}
	unknown := downloadProgressLine(2048, -1, time.Second)
	if !strings.Contains(unknown, "2.0 KiB downloaded") {
		t.Fatalf("unknown progress=%q", unknown)
	}
}

func TestDownloadReleaseAssetProgressReportsContentLength(t *testing.T) {
	oldBase := githubAPIBase
	oldClient := githubHTTPClient
	defer func() { githubAPIBase = oldBase; githubHTTPClient = oldClient }()

	payload := strings.Repeat("x", 8192)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(payload)))
		_, _ = io.WriteString(w, payload)
	}))
	defer srv.Close()
	githubAPIBase = srv.URL
	githubHTTPClient = srv.Client()

	var gotDownloaded, gotTotal int64
	var gotDone bool
	b, err := downloadReleaseAssetWithProgress(context.Background(), "", 7, func(downloaded, total int64, elapsed time.Duration, done bool) {
		gotDownloaded, gotTotal, gotDone = downloaded, total, done
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != payload {
		t.Fatal("payload mismatch")
	}
	if gotDownloaded != int64(len(payload)) || gotTotal != int64(len(payload)) || !gotDone {
		t.Fatalf("progress downloaded=%d total=%d done=%v", gotDownloaded, gotTotal, gotDone)
	}
}

func TestUpdateDisplayNonInteractiveStaysQuiet(t *testing.T) {
	var out strings.Builder
	d := updateDisplay{out: &out, interactive: false}
	if err := d.withSpinner("checking", func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if out.Len() != 0 {
		t.Fatalf("non-interactive spinner wrote %q", out.String())
	}
}
