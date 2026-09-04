package cx

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// primaryAccountName is the name given to a pre-cx Codex login that the
// installer imports as the very first managed account.
const primaryAccountName = "primary"

type bootstrapStatus int

const (
	// bootstrapAdopted means an existing bare Codex login became "primary".
	bootstrapAdopted bootstrapStatus = iota
	// bootstrapAlreadyManaged means cx already owns at least one account.
	bootstrapAlreadyManaged
	// bootstrapNoLogin means the system has no ChatGPT login worth importing.
	bootstrapNoLogin
)

// bootstrapPrimaryAccount imports a pre-cx ChatGPT login from
// CODEX_HOME/auth.json as the first managed account, named "primary". It runs
// at install time and is a no-op when cx already manages accounts or when the
// system has no ChatGPT login: cx never invents an account in that case, the
// user adds their first one under any name with cx add.
func bootstrapPrimaryAccount(p paths) (Account, bootstrapStatus, error) {
	accounts, err := listAccounts(p)
	if err != nil {
		return Account{}, bootstrapNoLogin, err
	}
	if len(accounts) != 0 {
		return Account{}, bootstrapAlreadyManaged, nil
	}

	live := p.sharedAuthPath()
	info, err := os.Lstat(live)
	if errors.Is(err, os.ErrNotExist) {
		return Account{}, bootstrapNoLogin, nil
	}
	if err != nil {
		return Account{}, bootstrapNoLogin, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return Account{}, bootstrapNoLogin, nil
	}

	ident, err := parseAuth(live)
	if err != nil {
		// cx manages ChatGPT credentials only. Leave API-key/unknown auth alone;
		// the normal first-switch backup path will preserve it unchanged.
		return Account{}, bootstrapNoLogin, nil
	}

	id, err := newID()
	if err != nil {
		return Account{}, bootstrapNoLogin, err
	}
	if err := os.MkdirAll(p.accountDir(id), 0700); err != nil {
		return Account{}, bootstrapNoLogin, err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(p.accountDir(id))
		}
	}()
	if err := ensureFileStoreConfig(filepath.Join(p.accountDir(id), "config.toml"), false); err != nil {
		return Account{}, bootstrapNoLogin, err
	}
	if err := installAuth(live, p.accountAuth(id)); err != nil {
		return Account{}, bootstrapNoLogin, err
	}

	now := time.Now()
	a := Account{
		ID:        id,
		Name:      primaryAccountName,
		AccountID: ident.AccountID,
		Email:     ident.Email,
		Plan:      ident.Plan,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := writeJSON(p.accountMeta(id), a); err != nil {
		return Account{}, bootstrapNoLogin, err
	}
	if err := switchAccount(p, a); err != nil {
		return Account{}, bootstrapNoLogin, fmt.Errorf("manage existing Codex login: %w", err)
	}
	cleanup = false
	return a, bootstrapAdopted, nil
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
