package cx

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func runDeviceAuth(home string) error {
	if _, err := exec.LookPath("codex"); err != nil {
		return errors.New("codex executable not found in PATH")
	}
	if err := os.MkdirAll(home, 0700); err != nil {
		return err
	}
	if err := ensureFileStoreConfig(filepath.Join(home, "config.toml"), false); err != nil {
		return err
	}
	cmd := exec.Command("codex", "login", "--device-auth")
	cmd.Env = append(os.Environ(), "CODEX_HOME="+home)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

func verifyExpectedEmail(ident authIdentity, expectedEmail string) error {
	expectedEmail = strings.TrimSpace(expectedEmail)
	if expectedEmail == "" {
		return nil
	}
	if ident.Email == "" {
		return fmt.Errorf("authenticated account did not expose an email; expected %s; credential was not saved", expectedEmail)
	}
	if !strings.EqualFold(ident.Email, expectedEmail) {
		return fmt.Errorf("authenticated as %s, expected %s; credential was not saved", ident.Email, expectedEmail)
	}
	return nil
}

func addAccount(p paths, requestedName, expectedEmail string) (Account, error) {
	if requestedName != "" {
		if err := validateName(requestedName); err != nil {
			return Account{}, err
		}
	}
	id, err := newID()
	if err != nil {
		return Account{}, err
	}
	stage := filepath.Join(p.DataRoot, ".login-"+id)
	defer os.RemoveAll(stage)

	fmt.Println(dim("Starting Codex device authorization in an isolated credential home..."))
	if expectedEmail != "" {
		fmt.Printf("%s expected account: %s\n", dim("→"), strings.TrimSpace(expectedEmail))
	}
	if err := runDeviceAuth(stage); err != nil {
		return Account{}, fmt.Errorf("device auth failed: %w", err)
	}
	ident, err := parseAuth(filepath.Join(stage, "auth.json"))
	if err != nil {
		return Account{}, err
	}
	if err := verifyExpectedEmail(ident, expectedEmail); err != nil {
		return Account{}, err
	}

	accounts, err := listAccounts(p)
	if err != nil {
		return Account{}, err
	}
	for _, existing := range accounts {
		if existing.AccountID == ident.AccountID {
			if err := installAuth(filepath.Join(stage, "auth.json"), p.accountAuth(existing.ID)); err != nil {
				return Account{}, err
			}
			existing.Email, existing.Plan, existing.UpdatedAt = ident.Email, ident.Plan, time.Now()
			if err := writeJSON(p.accountMeta(existing.ID), existing); err != nil {
				return Account{}, err
			}
			return existing, nil
		}
	}

	name := strings.TrimSpace(requestedName)
	if name == "" {
		name = "account"
		if ident.Email != "" {
			if i := strings.IndexByte(ident.Email, '@'); i > 0 {
				name = ident.Email[:i]
			}
		}
		base := name
		n := 2
		for {
			collision := false
			for _, a := range accounts {
				if strings.EqualFold(a.Name, name) {
					collision = true
					break
				}
			}
			if !collision {
				break
			}
			name = fmt.Sprintf("%s-%d", base, n)
			n++
		}
	}
	if err := validateName(name); err != nil {
		return Account{}, err
	}
	for _, a := range accounts {
		if strings.EqualFold(a.Name, name) {
			return Account{}, fmt.Errorf("account name already exists: %s", name)
		}
	}

	finalDir := p.accountDir(id)
	if err := os.MkdirAll(finalDir, 0700); err != nil {
		return Account{}, err
	}
	if err := ensureFileStoreConfig(filepath.Join(finalDir, "config.toml"), false); err != nil {
		return Account{}, err
	}
	if err := installAuth(filepath.Join(stage, "auth.json"), p.accountAuth(id)); err != nil {
		return Account{}, err
	}
	now := time.Now()
	a := Account{ID: id, Name: name, AccountID: ident.AccountID, Email: ident.Email, Plan: ident.Plan, CreatedAt: now, UpdatedAt: now}
	if err := writeJSON(p.accountMeta(id), a); err != nil {
		return Account{}, err
	}

	st, _ := loadState(p)
	if st.ActiveID == "" {
		if err := switchAccount(p, a); err != nil {
			return Account{}, err
		}
	}
	return a, nil
}

func reloginAccount(p paths, selector, expectedEmail string) (Account, error) {
	a, err := findAccount(p, selector)
	if err != nil {
		return Account{}, err
	}
	id, err := newID()
	if err != nil {
		return Account{}, err
	}
	stage := filepath.Join(p.DataRoot, ".relogin-"+id)
	defer os.RemoveAll(stage)
	fmt.Printf("Re-authorizing %s (%s)\n", a.Name, emptyDash(a.Email))
	if expectedEmail != "" {
		fmt.Printf("%s expected account: %s\n", dim("→"), strings.TrimSpace(expectedEmail))
	}
	if err := runDeviceAuth(stage); err != nil {
		return Account{}, fmt.Errorf("device auth failed: %w", err)
	}
	ident, err := parseAuth(filepath.Join(stage, "auth.json"))
	if err != nil {
		return Account{}, err
	}
	if err := verifyExpectedEmail(ident, expectedEmail); err != nil {
		return Account{}, err
	}
	if ident.AccountID != a.AccountID {
		return Account{}, fmt.Errorf("account mismatch: expected %s (%s), received %s; credential was not saved", a.Name, a.AccountID, ident.AccountID)
	}
	if err := installAuth(filepath.Join(stage, "auth.json"), p.accountAuth(a.ID)); err != nil {
		return Account{}, err
	}
	a.Email, a.Plan, a.UpdatedAt = ident.Email, ident.Plan, time.Now()
	if err := writeJSON(p.accountMeta(a.ID), a); err != nil {
		return Account{}, err
	}
	return a, nil
}
