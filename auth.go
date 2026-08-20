package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type authIdentity struct{ AccountID, Email, Plan string }

type authShape struct {
	Tokens *struct {
		IDToken      string `json:"id_token"`
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		AccountID    string `json:"account_id"`
	} `json:"tokens"`
}

func parseAuth(path string) (authIdentity, error) {
	var a authShape
	if err := readJSON(path, &a); err != nil {
		return authIdentity{}, err
	}
	if a.Tokens == nil || a.Tokens.AccessToken == "" {
		return authIdentity{}, errors.New("auth.json does not contain ChatGPT tokens")
	}
	id := authIdentity{AccountID: a.Tokens.AccountID}
	if a.Tokens.IDToken != "" {
		claims, _ := jwtClaims(a.Tokens.IDToken)
		if v, _ := claims["email"].(string); v != "" {
			id.Email = v
		}
		if ns, ok := claims["https://api.openai.com/auth"].(map[string]any); ok {
			if id.AccountID == "" {
				if v, _ := ns["chatgpt_account_id"].(string); v != "" {
					id.AccountID = v
				}
			}
			if v, _ := ns["chatgpt_plan_type"].(string); v != "" {
				id.Plan = v
			}
		}
	}
	if id.AccountID == "" {
		return authIdentity{}, errors.New("could not determine ChatGPT account_id")
	}
	return id, nil
}

func jwtClaims(token string) (map[string]any, error) {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return nil, errors.New("invalid JWT")
	}
	b, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return m, nil
}

var storeLine = regexp.MustCompile(`(?m)^\s*cli_auth_credentials_store\s*=.*$`)

func ensureFileStoreConfig(path string, backup bool) error {
	b, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	original := string(b)
	desired := `cli_auth_credentials_store = "file"`
	updated := original
	if storeLine.MatchString(updated) {
		updated = storeLine.ReplaceAllString(updated, desired)
	} else {
		if updated != "" && !strings.HasSuffix(updated, "\n") {
			updated += "\n"
		}
		updated = desired + "\n" + updated
	}
	if updated == original {
		return nil
	}
	if backup && original != "" {
		bak := path + ".cx-backup"
		if _, err := os.Stat(bak); errors.Is(err, os.ErrNotExist) {
			_ = atomicWrite(bak, []byte(original), 0600)
		}
	}
	return atomicWrite(path, []byte(updated), 0600)
}

func installAuth(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return fmt.Errorf("invalid auth JSON: %w", err)
	}
	return atomicWrite(dst, b, 0600)
}

func switchAccount(p paths, a Account) error {
	if err := ensureFileStoreConfig(p.sharedConfigPath(), true); err != nil {
		return fmt.Errorf("configure Codex file credential store: %w", err)
	}
	ident, err := parseAuth(p.accountAuth(a.ID))
	if err != nil {
		return fmt.Errorf("invalid credential for %s: %w", a.Name, err)
	}
	if ident.AccountID != a.AccountID {
		return fmt.Errorf("identity mismatch for %s; expected %s, found %s", a.Name, a.AccountID, ident.AccountID)
	}

	live := p.sharedAuthPath()
	info, err := os.Lstat(live)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			target, _ := os.Readlink(live)
			abs := target
			if !filepath.IsAbs(abs) {
				abs = filepath.Join(filepath.Dir(live), target)
			}
			abs, _ = filepath.Abs(abs)
			root, _ := filepath.Abs(p.AccountsRoot)
			if !strings.HasPrefix(abs, root+string(os.PathSeparator)) {
				return errors.New("~/.codex/auth.json is a symlink not managed by cx; refusing to replace it")
			}
		} else {
			bak := live + ".cx-backup"
			if _, berr := os.Lstat(bak); berr == nil {
				return errors.New("unmanaged ~/.codex/auth.json detected and a cx backup already exists; refusing to overwrite it")
			}
			if err := os.Rename(live, bak); err != nil {
				return fmt.Errorf("backup existing auth.json: %w", err)
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	tmp := live + fmt.Sprintf(".cx-%d", time.Now().UnixNano())
	_ = os.Remove(tmp)
	if err := os.Symlink(p.accountAuth(a.ID), tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, live); err != nil {
		_ = os.Remove(tmp)
		return err
	}

	now := time.Now()
	a.LastUsedAt = &now
	a.UpdatedAt = now
	if err := writeJSON(p.accountMeta(a.ID), a); err != nil {
		return err
	}
	return saveState(p, State{ActiveID: a.ID})
}
