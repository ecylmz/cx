package cx

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testAuthBytesFor(t *testing.T, accountID, email string) []byte {
	t.Helper()
	payload := map[string]any{
		"email": email,
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": accountID,
			"chatgpt_plan_type":  "plus",
		},
	}
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	tok := "x." + base64.RawURLEncoding.EncodeToString(b) + ".y"
	raw, err := json.Marshal(map[string]any{
		"tokens": map[string]any{
			"id_token":      tok,
			"access_token":  "access-" + email,
			"refresh_token": "refresh-" + email,
			"account_id":    accountID,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestFirstAddAdoptsExistingCodexLoginBeforeAddingDifferentAccount(t *testing.T) {
	p := makeTestPaths(t)

	oldEmail := "emre.can.ylmz@gmail.com"
	newEmail := "atakumtech@gmail.com"
	if err := os.WriteFile(p.sharedAuthPath(), testAuthBytesFor(t, "acct-old", oldEmail), 0600); err != nil {
		t.Fatal(err)
	}

	newAuth := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(newAuth, testAuthBytesFor(t, "acct-new", newEmail), 0600); err != nil {
		t.Fatal(err)
	}
	installFakeLoginCodex(t, newAuth)

	added, err := addAccount(p, "backup1", newEmail)
	if err != nil {
		t.Fatal(err)
	}
	if added.Name != "backup1" || !strings.EqualFold(added.Email, newEmail) {
		t.Fatalf("added=%+v", added)
	}

	accounts, err := listAccounts(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 2 {
		t.Fatalf("accounts=%+v", accounts)
	}
	var existing, backup Account
	for _, a := range accounts {
		switch a.Name {
		case "emre.can.ylmz":
			existing = a
		case "backup1":
			backup = a
		}
	}
	if existing.ID == "" || !strings.EqualFold(existing.Email, oldEmail) {
		t.Fatalf("existing login was not adopted: %+v", existing)
	}
	if backup.ID == "" || !strings.EqualFold(backup.Email, newEmail) {
		t.Fatalf("new login was not stored separately: %+v", backup)
	}

	st, err := loadState(p)
	if err != nil {
		t.Fatal(err)
	}
	if st.ActiveID != existing.ID {
		t.Fatalf("first add should preserve the pre-existing active login: state=%+v existing=%+v", st, existing)
	}
	live, err := parseAuth(p.sharedAuthPath())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(live.Email, oldEmail) {
		t.Fatalf("live auth changed during add: %+v", live)
	}

	legacy, err := parseAuth(p.sharedAuthPath() + ".cx-backup")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(legacy.Email, oldEmail) {
		t.Fatalf("legacy backup=%+v", legacy)
	}

	if err := switchAccount(p, backup); err != nil {
		t.Fatal(err)
	}
	live, err = parseAuth(p.sharedAuthPath())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(live.Email, newEmail) {
		t.Fatalf("switch did not activate backup1: %+v", live)
	}
	st, _ = loadState(p)
	if st.ActiveID != backup.ID {
		t.Fatalf("state after switch=%+v", st)
	}
}

func TestSameStoredIdentityDoesNotCollapseDifferentUsersInSameWorkspace(t *testing.T) {
	existing := Account{AccountID: "workspace", Email: "one@example.com"}
	if sameStoredIdentity(existing, authIdentity{AccountID: "workspace", Email: "two@example.com"}) {
		t.Fatal("different users in one workspace must remain separate cx accounts")
	}
	if !sameStoredIdentity(existing, authIdentity{AccountID: "workspace", Email: "ONE@example.com"}) {
		t.Fatal("same user/workspace should be detected as duplicate")
	}
}
