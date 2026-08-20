package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const githubRepo = "ecylmz/cx"

type githubRelease struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	} `json:"assets"`
}

func handleUpdate(args []string) error {
	force := false
	for _, arg := range args {
		switch arg {
		case "--force":
			force = true
		default:
			return errors.New("usage: cx update [--force]")
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	token := githubToken()
	rel, err := fetchLatestRelease(ctx, token)
	if err != nil {
		return err
	}
	latest := strings.TrimPrefix(rel.TagName, "v")
	current := strings.TrimPrefix(version, "v")
	if !force && current != "" && current != "dev" && latest == current {
		fmt.Printf("%s cx %s is already current\n", green("✓"), current)
		return nil
	}

	assetName, err := platformAsset(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return err
	}
	var assetID, sumsID int64
	for _, asset := range rel.Assets {
		switch asset.Name {
		case assetName:
			assetID = asset.ID
		case "SHA256SUMS":
			sumsID = asset.ID
		}
	}
	if assetID == 0 {
		return fmt.Errorf("release %s does not contain %s", rel.TagName, assetName)
	}
	if sumsID == 0 {
		return fmt.Errorf("release %s does not contain SHA256SUMS", rel.TagName)
	}

	payload, err := downloadReleaseAsset(ctx, token, assetID)
	if err != nil {
		return err
	}
	if len(payload) < 1024 {
		return errors.New("downloaded release asset is unexpectedly small")
	}
	sums, err := downloadReleaseAsset(ctx, token, sumsID)
	if err != nil {
		return fmt.Errorf("download SHA256SUMS: %w", err)
	}
	if err := verifySHA256(assetName, payload, sums); err != nil {
		return err
	}

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve current executable: %w", err)
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return fmt.Errorf("resolve executable symlink: %w", err)
	}
	if err := atomicReplaceExecutable(exe, payload); err != nil {
		return err
	}
	fmt.Printf("%s updated cx %s → %s\n", green("✓"), current, latest)
	return nil
}

func platformAsset(goos, goarch string) (string, error) {
	var osName string
	switch goos {
	case "darwin":
		osName = "darwin"
	case "linux":
		osName = "linux"
	default:
		return "", fmt.Errorf("unsupported OS: %s", goos)
	}
	var archName string
	switch goarch {
	case "arm64":
		archName = "arm64"
	case "amd64":
		archName = "amd64"
	default:
		return "", fmt.Errorf("unsupported architecture: %s", goarch)
	}
	return "cx-" + osName + "-" + archName, nil
}

func githubToken() string {
	if token := strings.TrimSpace(os.Getenv("GH_TOKEN")); token != "" {
		return token
	}
	if token := strings.TrimSpace(os.Getenv("GITHUB_TOKEN")); token != "" {
		return token
	}
	if _, err := exec.LookPath("gh"); err == nil {
		cmd := exec.Command("gh", "auth", "token")
		if out, err := cmd.Output(); err == nil {
			return strings.TrimSpace(string(out))
		}
	}
	return ""
}

func fetchLatestRelease(ctx context.Context, token string) (githubRelease, error) {
	var rel githubRelease
	url := "https://api.github.com/repos/" + githubRepo + "/releases/latest"
	body, status, err := githubGET(ctx, url, token, "application/vnd.github+json")
	if err != nil {
		return rel, err
	}
	if status == http.StatusNotFound && token == "" {
		return rel, errors.New("latest release is not accessible; cx is private, so run `gh auth login` or set GH_TOKEN")
	}
	if status != http.StatusOK {
		return rel, fmt.Errorf("GitHub latest-release request failed: HTTP %d", status)
	}
	if err := json.Unmarshal(body, &rel); err != nil {
		return rel, fmt.Errorf("decode latest release: %w", err)
	}
	if rel.TagName == "" {
		return rel, errors.New("latest GitHub release has no tag")
	}
	return rel, nil
}

func downloadReleaseAsset(ctx context.Context, token string, assetID int64) ([]byte, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/assets/%d", githubRepo, assetID)
	body, status, err := githubGET(ctx, url, token, "application/octet-stream")
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		if status == http.StatusNotFound && token == "" {
			return nil, errors.New("release asset is private; run `gh auth login` or set GH_TOKEN")
		}
		return nil, fmt.Errorf("GitHub release download failed: HTTP %d", status)
	}
	return body, nil
}

func githubGET(ctx context.Context, url, token, accept string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "cx/"+version)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	client := &http.Client{Timeout: 40 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 128<<20))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return body, resp.StatusCode, nil
}

func verifySHA256(assetName string, payload, sums []byte) error {
	want := ""
	for _, line := range strings.Split(string(sums), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && strings.TrimPrefix(fields[len(fields)-1], "*") == assetName {
			want = strings.ToLower(fields[0])
			break
		}
	}
	if want == "" {
		return fmt.Errorf("SHA256SUMS has no entry for %s", assetName)
	}
	got := fmt.Sprintf("%x", sha256.Sum256(payload))
	if got != want {
		return fmt.Errorf("checksum mismatch for %s", assetName)
	}
	return nil
}

func atomicReplaceExecutable(target string, payload []byte) error {
	dir := filepath.Dir(target)
	st, err := os.Stat(target)
	if err != nil {
		return fmt.Errorf("stat current executable: %w", err)
	}
	f, err := os.CreateTemp(dir, ".cx-update-*")
	if err != nil {
		return fmt.Errorf("cannot write beside %s: %w", target, err)
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err := f.Write(payload); err != nil {
		f.Close()
		return fmt.Errorf("write update: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("sync update: %w", err)
	}
	if err := f.Close(); err != nil {
		return err
	}
	mode := st.Mode().Perm()
	if mode&0111 == 0 {
		mode = 0755
	}
	if err := os.Chmod(tmp, mode); err != nil {
		return fmt.Errorf("chmod update: %w", err)
	}
	if runtime.GOOS == "darwin" {
		cmd := exec.Command("xattr", "-d", "com.apple.quarantine", tmp)
		_ = cmd.Run()
		if _, err := exec.LookPath("codesign"); err == nil {
			cmd = exec.Command("codesign", "--force", "--sign", "-", tmp)
			_ = cmd.Run()
		}
	}
	if err := os.Rename(tmp, target); err != nil {
		return fmt.Errorf("replace %s: %w", target, err)
	}
	return nil
}
