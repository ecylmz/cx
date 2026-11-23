package cx

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode"
)

const agyDefaultID = "agy-default"

var errNoAgyCredential = errors.New("no saved agy credential")

func agyCommand(ctx context.Context, p paths, a Account, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "agy", args...)
	if a.ID == agyDefaultID {
		if _, err := os.Lstat(agyCredentialPath(p, a)); errors.Is(err, os.ErrNotExist) {
			return cmd
		}
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

type agyUsage struct {
	groups           []quotaGroup
	tier             string
	hasFiveHourQuota bool
}

func readAgyUsage(p paths, a Account, login bool) (agyUsage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), usageTimeout)
	err := checkAgyVersion(ctx)
	cancel()
	if err != nil {
		return agyUsage{}, err
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
			return agyUsage{}, fmt.Errorf("agy quota request: %w", ctx.Err())
		}
		return agyUsage{}, fmt.Errorf("agy /usage failed: %w", err)
	}
	return parseAgyUsage(out, time.Now())
}

func parseAgyUsage(out []byte, now time.Time) (agyUsage, error) {
	var report struct {
		NumTurns int    `json:"num_turns"`
		Tier     string `json:"plan_tier"`
		Command  struct {
			Data struct {
				Tier   string `json:"plan_tier"`
				Groups []struct {
					Name    string `json:"name"`
					Buckets []struct {
						Window    string   `json:"window"`
						Remaining *float64 `json:"remaining_fraction"`
						ResetTime string   `json:"reset_time"`
						Disabled  bool     `json:"disabled"`
					} `json:"buckets"`
				} `json:"groups"`
			} `json:"data"`
		} `json:"command"`
	}
	if err := json.Unmarshal(out, &report); err != nil || report.NumTurns != 0 {
		return agyUsage{}, errors.New("invalid agy quota response")
	}
	usage := agyUsage{tier: cmp.Or(report.Tier, report.Command.Data.Tier)}
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
				if !bucket.Disabled && u.ResetsAt > now.Unix() {
					usage.hasFiveHourQuota = true
				}
				u.WindowMinutes = 5 * 60
				if quota.FiveHour == nil || u.UsedPercent > quota.FiveHour.UsedPercent {
					quota.FiveHour = &u
				}
			}
		}
		if complete && quota.Name != "" && (quota.Weekly.WindowMinutes > 0 || quota.FiveHour != nil) {
			usage.groups = append(usage.groups, quota)
		}
	}
	if len(usage.groups) == 0 {
		return agyUsage{}, errors.New("agy returned no quota windows")
	}
	return usage, nil
}

func fetchAgyUsage(p paths, a Account) UsageResult {
	if a.Email == "" {
		a.Email = agyAccountEmail(p, a)
	}
	usage, err := readAgyUsage(p, a, false)
	r := UsageResult{Account: a, QuotaGroups: usage.groups}
	if err != nil {
		r.Err = err.Error()
	}
	return r
}

func addAgyAccount(p paths, name string, sub subscription, existing bool) (Account, error) {
	if err := validateName(name); err != nil {
		return Account{}, err
	}
	name = strings.TrimSpace(name)
	checkAccount := func(email string) error {
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
			if email != "" {
				savedEmail := a.Email
				if savedEmail == "" {
					savedEmail = agyAccountEmail(p, a)
				}
				if strings.EqualFold(savedEmail, email) {
					return fmt.Errorf("Antigravity account is already registered as %s", a.Name)
				}
			}
		}
		return nil
	}
	if err := checkAccount(""); err != nil {
		return Account{}, err
	}
	a := Account{ID: agyDefaultID, Subscription: cmp.Or(sub, agyPro), Name: name}
	if !existing {
		if !isTerminal() {
			return Account{}, errors.New("run cx add NAME --provider agy in an interactive terminal")
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
	usage, err := readAgyUsage(p, a, !existing)
	if err != nil {
		return Account{}, err
	}
	if err := verifySubscription(sub, usage.tier); err != nil {
		return Account{}, err
	}
	if sub != "" && usage.tier == "" && usage.hasFiveHourQuota && (sub.tier() == "free" || sub.tier() == "plus") {
		return Account{}, fmt.Errorf("subscription mismatch: expected %s, but Antigravity reported an active five-hour quota", sub.label())
	}
	lock, err := lockState(p)
	if err != nil {
		return Account{}, err
	}
	defer lock.Close()
	if existing {
		credential, err := (agyKeyring{platform: runtime.GOOS}).read()
		if err != nil {
			return Account{}, err
		}
		if agyCredentialEmail(credential) == "" {
			return Account{}, errors.New("no supported Antigravity OAuth login in the keyring")
		}
		if err := atomicWrite(agyCredentialPath(p, a), credential, 0o600); err != nil {
			return Account{}, err
		}
	}
	credential, email, err := readAgyCredential(p, a)
	if err != nil {
		return Account{}, err
	}
	if err := checkAccount(email); err != nil {
		return Account{}, err
	}
	if !existing {
		if err := atomicWrite(agyCredentialPath(p, a), credential, 0o600); err != nil {
			return Account{}, err
		}
	}
	st, err := loadState(p)
	if err != nil {
		return Account{}, err
	}
	a.CreatedAt = time.Now()
	a.UpdatedAt = a.CreatedAt
	a.Email = email
	activate := !existing || st.activeAccountID(providerAgy) == ""
	if activate {
		a.LastUsedAt = &a.CreatedAt
	}
	if err := writeJSON(p.accountMeta(a.ID), a); err != nil {
		return Account{}, err
	}
	if activate {
		if !existing {
			st.DefaultAccountID = a.ID
		}
		st.setActiveAccount(a)
		save := func() error { return saveState(p, st) }
		if existing {
			err = save()
		} else {
			err = switchAgyAccount(p, a, save)
		}
		if err != nil {
			return Account{}, err
		}
	}
	saved = true
	return a, nil
}

func agyCredentialPath(p paths, a Account) string {
	return filepath.Join(p.accountDir(a.ID), "home", ".gemini", "antigravity-cli", "antigravity-oauth-token")
}

func agyAccountEmail(p paths, a Account) string {
	_, email, _ := readAgyCredential(p, a)
	return email
}

func readAgyCredential(p paths, a Account) ([]byte, string, error) {
	path := agyCredentialPath(p, a)
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, "", fmt.Errorf("%w for %s", errNoAgyCredential, a.Name)
	}
	if err != nil {
		return nil, "", err
	}
	if !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return nil, "", fmt.Errorf("invalid saved agy credential for %s", a.Name)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	data = bytes.TrimSpace(data)
	email := agyCredentialEmail(data)
	if email == "" || a.Email != "" && !strings.EqualFold(a.Email, email) {
		return nil, "", fmt.Errorf("invalid saved agy credential for %s", a.Name)
	}
	return data, email, nil
}

func agyCredentialEmail(data []byte) string {
	// shortcut: use the file store's email when agy exposes no identity command.
	var profile struct {
		Email string `json:"id_token_email"`
		Token struct {
			RefreshToken string `json:"refresh_token"`
		} `json:"token"`
	}
	if err := json.Unmarshal(data, &profile); err != nil || profile.Token.RefreshToken == "" {
		return ""
	}
	email := strings.TrimSpace(profile.Email)
	address, err := mail.ParseAddress(email)
	if err != nil || address.Address != email || strings.ContainsFunc(email, unicode.IsControl) {
		return ""
	}
	return email
}

func launchAgy(args []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), usageTimeout)
	defer cancel()
	if err := checkAgyVersion(ctx); err != nil {
		return err
	}
	return execAgent(exec.Command("agy", args...))
}
