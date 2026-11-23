package cx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode"
)

const agyDefaultID = "agy-default"

func handleAgy(p paths, args []string) error {
	if len(args) == 0 || slices.Contains([]string{"help", "-h", "--help"}, args[0]) {
		fmt.Print(`Usage:
  cx agy init [NAME]                add the existing agy login (default: primary)
  cx agy add NAME                   sign in to an isolated Google account
  cx agy use [NAME] [--resume]       select an Antigravity account
  cx agy status [NAME] [--json]     show Antigravity quotas
  cx agy rename OLD NEW             rename an Antigravity account
  cx agy remove NAME                remove an Antigravity account
  cx agy run [ARGS...]               launch the selected Antigravity account
`)
		return nil
	}
	switch args[0] {
	case "init", "add":
		existing := args[0] == "init"
		if len(args) > 2 || (!existing && len(args) != 2) {
			return errors.New("usage: cx agy init [NAME] or cx agy add NAME")
		}
		name := "primary"
		if len(args) == 2 {
			name = args[1]
		}
		a, err := addAgyAccount(p, name, existing)
		if err != nil {
			return err
		}
		fmt.Printf("%s added %s [%s]\n", green("✓"), a.Name, a.Provider.label())
	case "use":
		which := providerAgy
		return useAccount(p, args[1:], &which)
	case "status":
		os.Exit(handleProviderStatus(p, args[1:], providerAgy))
	case "rename":
		if len(args) != 3 {
			return errors.New("usage: cx agy rename OLD NEW")
		}
		return renameAccountFor(p, args[1], args[2], providerAgy)
	case "remove":
		if len(args) != 2 {
			return errors.New("usage: cx agy remove NAME")
		}
		return removeAccountFor(p, args[1], providerAgy)
	case "run":
		which := providerAgy
		return runAgent(p, args[1:], &which)
	default:
		return fmt.Errorf("unknown Antigravity command %q; run cx agy help", args[0])
	}
	return nil
}

func agyCommand(ctx context.Context, p paths, a Account, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "agy", args...)
	if a.ID == agyDefaultID {
		return cmd
	}
	home := filepath.Join(p.accountDir(a.ID), "home")
	// shortcut: agy selects its private file store in SSH mode, use a native profile flag when one is available.
	cmd.Env = append(cmd.Environ(),
		"HOME="+home,
		"XDG_CONFIG_HOME="+filepath.Join(home, ".config"),
		"XDG_DATA_HOME="+filepath.Join(home, ".local", "share"),
		"XDG_CACHE_HOME="+filepath.Join(home, ".cache"),
		"SSH_CONNECTION=127.0.0.1 1 127.0.0.1 22",
		"AGY_ADC_AUTH=",
	)
	return cmd
}

func checkAgyVersion(ctx context.Context) error {
	out, err := exec.CommandContext(ctx, "agy", "--version").Output()
	if err != nil {
		return fmt.Errorf("check agy version: %w", err)
	}
	var major, minor, patch int
	n, _ := fmt.Sscanf(strings.TrimSpace(string(out)), "%d.%d.%d", &major, &minor, &patch)
	if n != 3 || major < 1 || (major == 1 && (minor < 5 || (minor == 5 && patch < 14))) {
		return errors.New("Antigravity profiles require agy 1.5.14 or later")
	}
	return nil
}

func readAgyUsage(p paths, a Account, login bool) ([]quotaGroup, error) {
	ctx, cancel := context.WithTimeout(context.Background(), usageTimeout)
	err := checkAgyVersion(ctx)
	cancel()
	if err != nil {
		return nil, err
	}
	ctx = context.Background()
	if !login {
		ctx, cancel = context.WithTimeout(ctx, usageTimeout)
		defer cancel()
	}
	cmd := agyCommand(ctx, p, a, "-p", "/usage", "--output-format", "json", "--log-file", os.DevNull)
	if login {
		cmd.Stdin, cmd.Stderr = os.Stdin, os.Stderr
	} else {
		cmd.Env = append(cmd.Environ(), "ANTIGRAVITY_REMOTE_OOB_AUTH=true", "BROWSER=/bin/false", "AGY_CLI_DISABLE_AUTO_UPDATE=true")
	}
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("agy quota request: %w", ctx.Err())
		}
		return nil, fmt.Errorf("agy /usage failed: %w", err)
	}
	return parseAgyUsage(out, time.Now())
}

func parseAgyUsage(out []byte, now time.Time) ([]quotaGroup, error) {
	var report struct {
		NumTurns int `json:"num_turns"`
		Command  struct {
			Data struct {
				Groups []struct {
					Name    string `json:"name"`
					Buckets []struct {
						Window    string   `json:"window"`
						Remaining *float64 `json:"remaining_fraction"`
						ResetTime string   `json:"reset_time"`
					} `json:"buckets"`
				} `json:"groups"`
			} `json:"data"`
		} `json:"command"`
	}
	if err := json.Unmarshal(out, &report); err != nil || report.NumTurns != 0 {
		return nil, errors.New("invalid agy quota response")
	}
	var groups []quotaGroup
	for _, group := range report.Command.Data.Groups {
		name := strings.Map(func(r rune) rune {
			if unicode.IsControl(r) {
				return -1
			}
			return r
		}, group.Name)
		quota := quotaGroup{Name: strings.TrimSpace(name)}
		complete := true
		for _, bucket := range group.Buckets {
			if bucket.Window != "weekly" && bucket.Window != "5h" {
				continue
			}
			if bucket.Remaining == nil || *bucket.Remaining < 0 || *bucket.Remaining > 1 {
				complete = false
				break
			}
			u := WeeklyUsage{
				UsedPercent: (1 - *bucket.Remaining) * 100,
				FetchedAt:   now, Fresh: true, WindowStarted: true,
			}
			if reset, err := time.Parse(time.RFC3339, bucket.ResetTime); err == nil {
				u.ResetsAt = reset.Unix()
			}
			switch bucket.Window {
			case "weekly":
				u.WindowMinutes = 7 * 24 * 60
				if quota.Weekly.WindowMinutes == 0 || u.UsedPercent > quota.Weekly.UsedPercent {
					quota.Weekly = u
				}
			case "5h":
				u.WindowMinutes = 5 * 60
				if quota.FiveHour == nil || u.UsedPercent > quota.FiveHour.UsedPercent {
					quota.FiveHour = &u
				}
			}
		}
		if complete && quota.Name != "" && (quota.Weekly.WindowMinutes > 0 || quota.FiveHour != nil) {
			groups = append(groups, quota)
		}
	}
	if len(groups) == 0 {
		return nil, errors.New("agy returned no quota windows")
	}
	return groups, nil
}

func fetchAgyUsage(p paths, a Account) UsageResult {
	groups, err := readAgyUsage(p, a, false)
	r := UsageResult{Account: a, QuotaGroups: groups}
	if err != nil {
		r.Err = err.Error()
	}
	return r
}

func addAgyAccount(p paths, name string, existing bool) (Account, error) {
	if err := validateName(name); err != nil {
		return Account{}, err
	}
	name = strings.TrimSpace(name)
	checkName := func() error {
		accounts, err := listAccountsFor(p, providerAgy)
		if err != nil {
			return err
		}
		for _, a := range accounts {
			if existing && a.ID == agyDefaultID {
				return errors.New("cx already manages the default agy login")
			}
			if strings.EqualFold(a.Name, name) {
				return fmt.Errorf("account name already exists: %s", name)
			}
		}
		return nil
	}
	if err := checkName(); err != nil {
		return Account{}, err
	}
	a := Account{ID: agyDefaultID, Provider: providerAgy, Name: name}
	if !existing {
		if !isTerminal() {
			return Account{}, errors.New("run cx agy add in an interactive terminal")
		}
		id, err := newID()
		if err != nil {
			return Account{}, err
		}
		a.ID = id
	}
	dir := p.accountDir(a.ID)
	if err := os.Mkdir(dir, 0o700); err != nil {
		return Account{}, err
	}
	saved := false
	defer func() {
		if !saved {
			_ = os.RemoveAll(dir)
		}
	}()
	if !existing {
		fmt.Println("Sign in with the Google account for " + name + ".")
	}
	if _, err := readAgyUsage(p, a, !existing); err != nil {
		return Account{}, err
	}
	if !existing {
		if err := requireAgyCredential(p, a); err != nil {
			return Account{}, err
		}
		if err := os.Chmod(agyCredentialPath(p, a), 0o600); err != nil {
			return Account{}, err
		}
	}
	lock, err := lockState(p)
	if err != nil {
		return Account{}, err
	}
	defer lock.Close()
	if err := checkName(); err != nil {
		return Account{}, err
	}
	st, err := loadState(p)
	if err != nil {
		return Account{}, err
	}
	a.CreatedAt = time.Now()
	a.UpdatedAt = a.CreatedAt
	first := st.selectedAccountID(providerAgy) == ""
	if first {
		a.LastUsedAt = &a.CreatedAt
	}
	if err := writeJSON(p.accountMeta(a.ID), a); err != nil {
		return Account{}, err
	}
	if first {
		if st.defaultAccountID() == "" {
			st.DefaultProvider = providerAgy
		}
		st.selectAccount(a)
		if err := saveState(p, st); err != nil {
			return Account{}, err
		}
	}
	saved = true
	return a, nil
}

func agyCredentialPath(p paths, a Account) string {
	return filepath.Join(p.accountDir(a.ID), "home", ".gemini", "antigravity-cli", "antigravity-oauth-token")
}

func requireAgyCredential(p paths, a Account) error {
	if a.ID == agyDefaultID {
		return nil
	}
	info, err := os.Lstat(agyCredentialPath(p, a))
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		return fmt.Errorf("no saved agy credential for %s", a.Name)
	}
	return nil
}

func launchAgy(p paths, a Account, args []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), usageTimeout)
	defer cancel()
	if err := checkAgyVersion(ctx); err != nil {
		return err
	}
	return execAgent(agyCommand(context.Background(), p, a, args...))
}
