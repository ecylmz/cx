package cx

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// adoptExistingCodexAuth turns a pre-cx ChatGPT login in CODEX_HOME/auth.json
// into the first managed cx account before a new account is added. This keeps
// the already-active Codex identity explicit and makes the first real switch
// use the same managed-account path as every later switch.
func adoptExistingCodexAuth(p paths, reservedName string) (Account, bool, error) {
	accounts, err := listAccounts(p)
	if err != nil {
		return Account{}, false, err
	}
	if len(accounts) != 0 {
		return Account{}, false, nil
	}

	live := p.sharedAuthPath()
	info, err := os.Lstat(live)
	if errors.Is(err, os.ErrNotExist) {
		return Account{}, false, nil
	}
	if err != nil {
		return Account{}, false, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return Account{}, false, nil
	}

	ident, err := parseAuth(live)
	if err != nil {
		// cx manages ChatGPT credentials only. Leave API-key/unknown auth alone;
		// the normal first-switch backup path will preserve it unchanged.
		return Account{}, false, nil
	}

	id, err := newID()
	if err != nil {
		return Account{}, false, err
	}
	name := "current"
	if strings.EqualFold(strings.TrimSpace(reservedName), name) {
		name = "existing"
	}

	if err := os.MkdirAll(p.accountDir(id), 0700); err != nil {
		return Account{}, false, err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(p.accountDir(id))
		}
	}()
	if err := ensureFileStoreConfig(filepath.Join(p.accountDir(id), "config.toml"), false); err != nil {
		return Account{}, false, err
	}
	if err := installAuth(live, p.accountAuth(id)); err != nil {
		return Account{}, false, err
	}

	now := time.Now()
	a := Account{
		ID:        id,
		Name:      name,
		AccountID: ident.AccountID,
		Email:     ident.Email,
		Plan:      ident.Plan,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := writeJSON(p.accountMeta(id), a); err != nil {
		return Account{}, false, err
	}
	if err := switchAccount(p, a); err != nil {
		return Account{}, false, fmt.Errorf("manage existing Codex login: %w", err)
	}
	cleanup = false
	fmt.Printf("%s adopted existing Codex login as %s", green("✓"), a.Name)
	if a.Email != "" {
		fmt.Printf(" (%s)", a.Email)
	}
	fmt.Println()
	return a, true, nil
}

func sameStoredIdentity(existing Account, ident authIdentity) bool {
	if existing.AccountID != ident.AccountID {
		return false
	}
	if existing.Email == "" || ident.Email == "" {
		return true
	}
	return strings.EqualFold(existing.Email, ident.Email)
}
