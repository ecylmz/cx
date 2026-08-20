package cx

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

func TestGitHubTokenPrefersEnvironment(t *testing.T) {
	t.Setenv("GH_TOKEN", " gh-token ")
	t.Setenv("GITHUB_TOKEN", "other")
	if got := githubToken(); got != "gh-token" {
		t.Fatalf("token=%q", got)
	}
	t.Setenv("GH_TOKEN", "")
	if got := githubToken(); got != "other" {
		t.Fatalf("token=%q", got)
	}
}

func TestGitHubReleaseHTTPFlow(t *testing.T) {
	oldBase := githubAPIBase
	oldClient := githubHTTPClient
	defer func() { githubAPIBase = oldBase; githubHTTPClient = oldClient }()

	mux := http.NewServeMux()
	mux.HandleFunc("/repos/ecylmz/cx/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("authorization=%q", r.Header.Get("Authorization"))
		}
		if !strings.Contains(r.Header.Get("User-Agent"), "cx/") {
			t.Errorf("user-agent=%q", r.Header.Get("User-Agent"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"tag_name":"v9.9.9","assets":[{"id":7,"name":"cx-linux-amd64"},{"id":8,"name":"SHA256SUMS"}]}`)
	})
	mux.HandleFunc("/repos/ecylmz/cx/releases/assets/7", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("payload"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	githubAPIBase = srv.URL
	githubHTTPClient = srv.Client()

	ctx := context.Background()
	rel, err := fetchLatestRelease(ctx, "secret")
	if err != nil || rel.TagName != "v9.9.9" || len(rel.Assets) != 2 {
		t.Fatalf("release=%+v err=%v", rel, err)
	}
	b, err := downloadReleaseAsset(ctx, "secret", 7)
	if err != nil || string(b) != "payload" {
		t.Fatalf("asset=%q err=%v", b, err)
	}
}

func TestGitHubLatestReleasePrivateErrorAndBadJSON(t *testing.T) {
	oldBase := githubAPIBase
	oldClient := githubHTTPClient
	defer func() { githubAPIBase = oldBase; githubHTTPClient = oldClient }()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/ecylmz/cx/releases/latest" {
			if r.URL.Query().Get("bad") == "1" {
				_, _ = io.WriteString(w, "{")
				return
			}
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	githubAPIBase = srv.URL
	githubHTTPClient = srv.Client()
	if _, err := fetchLatestRelease(context.Background(), ""); err == nil || !strings.Contains(err.Error(), "private") {
		t.Fatalf("expected private error, got %v", err)
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
