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
	"syscall"
	"time"
)

type provider string

const (
	providerCodex provider = ""
	providerAgy   provider = "agy"
)

func (p provider) label() string {
	switch p {
	case providerCodex:
		return "Codex"
	case providerAgy:
		return "Antigravity"
	default:
		return string(p)
	}
}

type subscription string

const (
	codexPlus subscription = "codex/plus"
	agyPro    subscription = "agy/pro"
)

func newSubscription(p provider, tier string) subscription {
	tier = canonicalTier(p, strings.ToLower(strings.TrimSpace(tier)))
	if p == providerCodex {
		return subscription("codex/" + cmp.Or(tier, "plus"))
	}
	return subscription(string(p) + "/" + cmp.Or(tier, "pro"))
}

func (s subscription) provider() provider {
	product, _, _ := strings.Cut(string(s), "/")
	if product == "codex" {
		return providerCodex
	}
	return provider(product)
}

func (s subscription) tier() string {
	_, tier, _ := strings.Cut(string(cmp.Or(s, codexPlus)), "/")
	return tier
}

func (s subscription) tierLabel() string {
	tier := s.tier()
	for _, option := range subscriptionTiers[s.provider()] {
		if option.id == tier {
			return option.label
		}
	}
	if tier == "" {
		return ""
	}
	return strings.ToUpper(tier[:1]) + tier[1:]
}

func (s subscription) label() string {
	return s.provider().label() + " " + s.tierLabel()
}

type Account struct {
	ID              string       `json:"id"`
	Subscription    subscription `json:"subscription,omitempty"`
	Name            string       `json:"name"`
	AccountID       string       `json:"account_id"`
	Email           string       `json:"email,omitempty"`
	CreatedAt       time.Time    `json:"created_at"`
	UpdatedAt       time.Time    `json:"updated_at"`
	LastUsedAt      *time.Time   `json:"last_used_at,omitempty"`
	FiveHourResetAt int64        `json:"five_hour_reset_at,omitempty"`
	WeeklyResetAt   int64        `json:"weekly_reset_at,omitempty"`
}

func (a Account) subscription() subscription {
	return cmp.Or(a.Subscription, codexPlus)
}

func (a Account) provider() provider {
	return a.subscription().provider()
}

func (a *Account) UnmarshalJSON(data []byte) error {
	type account Account
	var stored struct {
		*account
		Provider provider `json:"provider"`
		Tier     string   `json:"plan"`
	}
	stored.account = (*account)(a)
	if err := json.Unmarshal(data, &stored); err != nil {
		return err
	}
	if a.Subscription == "" {
		a.Subscription = newSubscription(stored.Provider, stored.Tier)
	}
	product, tier, ok := strings.Cut(string(a.Subscription), "/")
	if !ok || validateName(product) != nil || validateName(tier) != nil {
		return errors.New("invalid account subscription")
	}
	return nil
}

type State struct {
	CodexAccountID   string              `json:"active_id,omitempty"`
	DefaultAccountID string              `json:"default_account_id,omitempty"`
	ActiveAccounts   map[provider]string `json:"provider_accounts,omitempty"`
}

func (s State) defaultAccountID() string {
	return cmp.Or(s.DefaultAccountID, s.CodexAccountID)
}

func (s State) activeAccountID(p provider) string {
	if p == providerCodex {
		return s.CodexAccountID
	}
	return s.ActiveAccounts[p]
}

func (s *State) setActiveAccount(a Account) {
	id := s.defaultAccountID()
	if id == "" || id == s.activeAccountID(a.provider()) {
		s.DefaultAccountID = a.ID
	}
	if a.provider() == providerCodex {
		s.CodexAccountID = a.ID
		return
	}
	if s.ActiveAccounts == nil {
		s.ActiveAccounts = make(map[provider]string)
	}
	s.ActiveAccounts[a.provider()] = a.ID
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
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
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
	return atomicWrite(path, b, 0o600)
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

func listAccounts(p paths) ([]Account, error) {
	return listAccountsFor(p, providerCodex)
}

func listAccountsFor(p paths, which provider) ([]Account, error) {
	return listScopedAccounts(p, &which)
}

func listAllAccounts(p paths) ([]Account, error) {
	return listScopedAccounts(p, nil)
}

func listScopedAccounts(p paths, which *provider) ([]Account, error) {
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
		if err := readJSON(p.accountMeta(e.Name()), &a); err != nil || a.ID != e.Name() {
			continue
		}
		if which != nil && a.provider() != *which {
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
	if selector == "" {
		return Account{}, errors.New("account name required")
	}
	s := strings.ToLower(selector)
	var exact, prefixes []Account
	for _, a := range accounts {
		name, id := strings.ToLower(a.Name), strings.ToLower(a.ID)
		switch {
		case name == s || id == s || strings.ToLower(a.Email) == s:
			exact = append(exact, a)
		case strings.HasPrefix(name, s) || strings.HasPrefix(id, s):
			prefixes = append(prefixes, a)
		}
	}
	matches := exact
	if len(exact) == 0 {
		matches = prefixes
	}
	switch len(matches) {
	case 0:
		return Account{}, fmt.Errorf("account not found: %s", selector)
	case 1:
		return matches[0], nil
	}
	if slices.ContainsFunc(matches[1:], func(a Account) bool { return a.provider() != matches[0].provider() }) {
		return Account{}, fmt.Errorf("ambiguous account selector %q across providers. Use --provider codex or --provider agy", selector)
	}
	return Account{}, fmt.Errorf("ambiguous account selector: %s", selector)
}

func findAccount(p paths, selector string, which *provider) (Account, error) {
	as, err := listScopedAccounts(p, which)
	if err != nil {
		return Account{}, err
	}
	return findIn(as, selector)
}

func accountsByTier(accounts []Account, tier string) []Account {
	if tier == "" {
		return accounts
	}
	return slices.DeleteFunc(accounts, func(a Account) bool { return a.subscription().tier() != canonicalTier(a.provider(), tier) })
}

func loadState(p paths) (State, error) {
	var s State
	stored := struct {
		*State
		ActiveProvider provider `json:"active_provider"`
	}{State: &s}
	err := readJSON(p.statePath(), &stored)
	if errors.Is(err, os.ErrNotExist) {
		return State{}, nil
	}
	if err == nil && s.DefaultAccountID == "" {
		s.DefaultAccountID = s.activeAccountID(stored.ActiveProvider)
	}
	return s, err
}
func saveState(p paths, s State) error { return writeJSON(p.statePath(), s) }

func lockState(p paths) (*os.File, error) {
	lock, err := os.OpenFile(p.statePath()+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		lock.Close()
		return nil, err
	}
	return lock, nil
}

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
		if r.Account.provider() == providerCodex && r.Err == "" {
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

func renameAccount(p paths, selector, newName string, which *provider) error {
	if err := validateName(newName); err != nil {
		return err
	}
	newName = strings.TrimSpace(newName)
	lock, err := lockState(p)
	if err != nil {
		return err
	}
	defer lock.Close()
	as, err := listScopedAccounts(p, which)
	if err != nil {
		return err
	}
	target, err := findIn(as, selector)
	if err != nil {
		return err
	}
	if slices.ContainsFunc(as, func(a Account) bool {
		return a.provider() == target.provider() && a.ID != target.ID && strings.EqualFold(a.Name, newName)
	}) {
		return fmt.Errorf("account name already exists: %s", newName)
	}
	target.Name = newName
	target.UpdatedAt = time.Now()
	return writeJSON(p.accountMeta(target.ID), target)
}

func removeAccount(p paths, selector string, which *provider) error {
	lock, err := lockState(p)
	if err != nil {
		return err
	}
	defer lock.Close()
	a, err := findAccount(p, selector, which)
	if err != nil {
		return err
	}
	st, err := loadState(p)
	if err != nil {
		return err
	}
	if st.activeAccountID(a.provider()) == a.ID {
		return errors.New("cannot remove the active account; switch to another account of the same provider first")
	}
	return os.RemoveAll(p.accountDir(a.ID))
}
