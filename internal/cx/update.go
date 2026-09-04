package cx

import (
	"bytes"
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

const (
	githubRepo       = "ecylmz/cx"
	maxReleaseAsset  = int64(128 << 20)
	progressBarWidth = 22
)

var githubAPIBase = "https://api.github.com"
var githubHTTPClient = &http.Client{Timeout: 40 * time.Second}

type githubRelease struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	} `json:"assets"`
}

type updateDisplay struct {
	out         io.Writer
	interactive bool
}

type assetProgress func(downloaded, total int64, elapsed time.Duration, done bool)

func newUpdateDisplay() updateDisplay {
	st, _ := os.Stdout.Stat()
	return updateDisplay{
		out:         os.Stdout,
		interactive: st != nil && (st.Mode()&os.ModeCharDevice) != 0,
	}
}

func (d updateDisplay) withSpinner(label string, fn func() error) error {
	if !d.interactive {
		return fn()
	}
	frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(80 * time.Millisecond)
		defer ticker.Stop()
		i := 0
		for {
			fmt.Fprintf(d.out, "\r\x1b[2K%s %s", dim(frames[i%len(frames)]), label)
			i++
			select {
			case <-done:
				return
			case <-ticker.C:
			}
		}
	}()
	err := fn()
	close(done)
	<-stopped
	fmt.Fprint(d.out, "\r\x1b[2K")
	return err
}

func (d updateDisplay) success(message string) {
	fmt.Fprintf(d.out, "%s %s\n", green("✓"), message)
}

func (d updateDisplay) downloadAsset(ctx context.Context, token string, assetID int64, name string) ([]byte, error) {
	if !d.interactive {
		return downloadReleaseAsset(ctx, token, assetID)
	}
	fmt.Fprintf(d.out, "%s downloading %s\n", cyan("↓"), name)
	var lastDraw time.Time
	payload, err := downloadReleaseAssetWithProgress(ctx, token, assetID, func(downloaded, total int64, elapsed time.Duration, done bool) {
		now := time.Now()
		if !done && !lastDraw.IsZero() && now.Sub(lastDraw) < 80*time.Millisecond {
			return
		}
		lastDraw = now
		fmt.Fprintf(d.out, "\r\x1b[2K%s", downloadProgressLine(downloaded, total, elapsed))
	})
	if err != nil {
		fmt.Fprint(d.out, "\r\x1b[2K")
		return nil, err
	}
	fmt.Fprintln(d.out)
	return payload, nil
}

func downloadProgressLine(downloaded, total int64, elapsed time.Duration) string {
	var speed float64
	if elapsed > 0 {
		speed = float64(downloaded) / elapsed.Seconds()
	}
	if total <= 0 {
		return fmt.Sprintf("  %s downloaded  %s/s", formatBytes(downloaded), formatBytes(int64(speed)))
	}
	fraction := min(max(float64(downloaded)/float64(total), 0), 1)
	filled := int(fraction*progressBarWidth + 0.5)
	bar := cyan(strings.Repeat("█", filled)) + dim(strings.Repeat("░", progressBarWidth-filled))
	pct := int(fraction*100 + 0.5)
	return fmt.Sprintf("  %s  %3d%%  %s / %s  %s/s", bar, pct, formatBytes(downloaded), formatBytes(total), formatBytes(int64(speed)))
}

func formatBytes(n int64) string {
	n = max(n, 0)
	const (
		kib = int64(1 << 10)
		mib = int64(1 << 20)
		gib = int64(1 << 30)
	)
	switch {
	case n >= gib:
		return fmt.Sprintf("%.1f GiB", float64(n)/float64(gib))
	case n >= mib:
		return fmt.Sprintf("%.1f MiB", float64(n)/float64(mib))
	case n >= kib:
		return fmt.Sprintf("%.1f KiB", float64(n)/float64(kib))
	default:
		return fmt.Sprintf("%d B", n)
	}
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
	display := newUpdateDisplay()
	token := githubToken()

	var rel githubRelease
	if err := display.withSpinner("checking latest release", func() error {
		var err error
		rel, err = fetchLatestRelease(ctx, token)
		return err
	}); err != nil {
		return err
	}
	latest := strings.TrimPrefix(rel.TagName, "v")
	current := strings.TrimPrefix(Version, "v")
	if !force && current != "" && current != "dev" && latest == current {
		display.success(fmt.Sprintf("cx %s is already current", current))
		return nil
	}
	if display.interactive {
		display.success("latest release " + latest)
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

	payload, err := display.downloadAsset(ctx, token, assetID, assetName)
	if err != nil {
		return err
	}
	if len(payload) < 1024 {
		return errors.New("downloaded release asset is unexpectedly small")
	}

	var sums []byte
	if err := display.withSpinner("verifying checksum", func() error {
		var err error
		sums, err = downloadReleaseAsset(ctx, token, sumsID)
		if err != nil {
			return fmt.Errorf("download SHA256SUMS: %w", err)
		}
		return verifySHA256(assetName, payload, sums)
	}); err != nil {
		return err
	}
	if display.interactive {
		display.success("checksum verified")
	}

	if err := display.withSpinner("installing update", func() error {
		exe, err := os.Executable()
		if err != nil {
			return fmt.Errorf("resolve current executable: %w", err)
		}
		exe, err = filepath.EvalSymlinks(exe)
		if err != nil {
			return fmt.Errorf("resolve executable symlink: %w", err)
		}
		return atomicReplaceExecutable(exe, payload)
	}); err != nil {
		return err
	}
	display.success(fmt.Sprintf("updated cx %s → %s", current, latest))
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
	return ""
}

func fetchLatestRelease(ctx context.Context, token string) (githubRelease, error) {
	var rel githubRelease
	url := githubAPIBase + "/repos/" + githubRepo + "/releases/latest"
	body, status, err := githubGET(ctx, url, token, "application/vnd.github+json")
	if err != nil {
		return rel, err
	}
	if status == http.StatusNotFound {
		return rel, errors.New("no GitHub release found")
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
	return downloadReleaseAssetWithProgress(ctx, token, assetID, nil)
}

func downloadReleaseAssetWithProgress(ctx context.Context, token string, assetID int64, progress assetProgress) ([]byte, error) {
	url := fmt.Sprintf("%s/repos/%s/releases/assets/%d", githubAPIBase, githubRepo, assetID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/octet-stream")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "cx/"+Version)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := githubHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, errors.New("release asset not found")
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		msg := strings.TrimSpace(string(body))
		if msg != "" {
			return nil, fmt.Errorf("GitHub release download failed: HTTP %d: %s", resp.StatusCode, msg)
		}
		return nil, fmt.Errorf("GitHub release download failed: HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > maxReleaseAsset {
		return nil, fmt.Errorf("release asset is too large: %s", formatBytes(resp.ContentLength))
	}

	var buf bytes.Buffer
	if resp.ContentLength > 0 {
		buf.Grow(int(resp.ContentLength))
	}
	chunk := make([]byte, 64<<10)
	var downloaded int64
	started := time.Now()
	for {
		n, readErr := resp.Body.Read(chunk)
		if n > 0 {
			downloaded += int64(n)
			if downloaded > maxReleaseAsset {
				return nil, fmt.Errorf("release asset exceeds %s", formatBytes(maxReleaseAsset))
			}
			_, _ = buf.Write(chunk[:n])
			if progress != nil {
				progress(downloaded, resp.ContentLength, time.Since(started), false)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return nil, readErr
		}
	}
	if progress != nil {
		progress(downloaded, resp.ContentLength, time.Since(started), true)
	}
	return buf.Bytes(), nil
}

func githubGET(ctx context.Context, url, token, accept string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "cx/"+Version)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := githubHTTPClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxReleaseAsset))
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
