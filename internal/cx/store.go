package cx

import (
	"cmp"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

type Account struct {
	ID              string     `json:"id"`
	Name            string     `json:"name"`
	AccountID       string     `json:"account_id"`
	Email           string     `json:"email,omitempty"`
	Plan            string     `json:"plan,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	LastUsedAt      *time.Time `json:"last_used_at,omitempty"`
	FiveHourResetAt int64      `json:"five_hour_reset_at,omitempty"`
	WeeklyResetAt   int64      `json:"weekly_reset_at,omitempty"`
}

type State struct {
	ActiveID string `json:"active_id,omitempty"`
}

type WeeklyUsage struct {
	UsedPercent   float64   `json:"used_percent"`
	WindowMinutes int64     `json:"window_minutes,omitempty"`
	ResetsAt      int64     `json:"resets_at,omitempty"`
	FetchedAt     time.Time `json:"fetched_at"`
	Fresh         bool      `json:"fresh"`
	WindowStarted bool      `json:"window_started"`
	Err           string    `json:"error,omitempty"`
}

type cachedStatus struct {
	Accounts map[string]WeeklyUsage `json:"accounts"`
}

func newID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func atomicWrite(path string, data []byte, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".cx-tmp-")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return atomicWrite(path, b, 0600)
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

func listAccounts(p paths) ([]Account, error) {
	ents, err := os.ReadDir(p.AccountsRoot)
	if err != nil {
		return nil, err
	}
	out := make([]Account, 0, len(ents))
	for _, e := range ents {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		var a Account
		if err := readJSON(p.accountMeta(e.Name()), &a); err != nil {
			continue
		}
		out = append(out, a)
	}
	// Most recently used first, then never-used accounts by name.
	slices.SortFunc(out, func(x, y Account) int {
		switch {
		case x.LastUsedAt != nil && y.LastUsedAt != nil:
			return y.LastUsedAt.Compare(*x.LastUsedAt)
		case x.LastUsedAt != nil:
			return -1
		case y.LastUsedAt != nil:
			return 1
		default:
			return cmp.Compare(strings.ToLower(x.Name), strings.ToLower(y.Name))
		}
	})
	return out, nil
}

func findIn(accounts []Account, selector string) (Account, error) {
	s := strings.ToLower(selector)
	var matches []Account
	for _, a := range accounts {
		if strings.ToLower(a.Name) == s || strings.ToLower(a.ID) == s || (a.Email != "" && strings.ToLower(a.Email) == s) {
			return a, nil
		}
		if strings.HasPrefix(strings.ToLower(a.Name), s) || strings.HasPrefix(strings.ToLower(a.ID), s) {
			matches = append(matches, a)
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) == 0 {
		return Account{}, fmt.Errorf("account not found: %s", selector)
	}
	return Account{}, fmt.Errorf("ambiguous account selector: %s", selector)
}

func findAccount(p paths, selector string) (Account, error) {
	as, err := listAccounts(p)
	if err != nil {
		return Account{}, err
	}
	return findIn(as, selector)
}

func loadState(p paths) (State, error) {
	var s State
	err := readJSON(p.statePath(), &s)
	if errors.Is(err, os.ErrNotExist) {
		return State{}, nil
	}
	return s, err
}
func saveState(p paths, s State) error { return writeJSON(p.statePath(), s) }

func loadCache(p paths) (map[string]WeeklyUsage, error) {
	var c cachedStatus
	err := readJSON(p.cachePath(), &c)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]WeeklyUsage{}, nil
	}
	if err != nil {
		return nil, err
	}
	if c.Accounts == nil {
		c.Accounts = map[string]WeeklyUsage{}
	}
	return c.Accounts, nil
}
func saveFreshCache(p paths, rs []UsageResult) {
	c, _ := loadCache(p)
	if c == nil {
		c = map[string]WeeklyUsage{}
	}
	for _, r := range rs {
		if r.Err == "" {
			c[r.Account.ID] = r.Usage
		}
	}
	_ = writeJSON(p.cachePath(), cachedStatus{Accounts: c})
}

func validateName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("account name cannot be empty")
	}
	if strings.ContainsFunc(name, func(r rune) bool {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return false
		case r == '-', r == '_', r == '.':
			return false
		default:
			return true
		}
	}) {
		return errors.New("account name may contain only letters, digits, dot, dash, underscore")
	}
	return nil
}

func renameAccount(p paths, selector, newName string) error {
	if err := validateName(newName); err != nil {
		return err
	}
	newName = strings.TrimSpace(newName)
	as, err := listAccounts(p)
	if err != nil {
		return err
	}
	target, err := findIn(as, selector)
	if err != nil {
		return err
	}
	if slices.ContainsFunc(as, func(a Account) bool {
		return a.ID != target.ID && strings.EqualFold(a.Name, newName)
	}) {
		return fmt.Errorf("account name already exists: %s", newName)
	}
	target.Name = newName
	target.UpdatedAt = time.Now()
	return writeJSON(p.accountMeta(target.ID), target)
}

func removeAccount(p paths, selector string) error {
	a, err := findAccount(p, selector)
	if err != nil {
		return err
	}
	st, _ := loadState(p)
	if st.ActiveID == a.ID {
		return errors.New("cannot remove the active account; switch to another account first")
	}
	return os.RemoveAll(p.accountDir(a.ID))
}
