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

func TestBootstrapAdoptsExistingCodexLoginAsPrimary(t *testing.T) {
	p := makeTestPaths(t)
	email := "emre.can.ylmz@gmail.com"
	if err := os.WriteFile(p.sharedAuthPath(), testAuthBytesFor(t, "acct-old", email), 0600); err != nil {
		t.Fatal(err)
	}

	a, status, err := bootstrapPrimaryAccount(p)
	if err != nil {
		t.Fatal(err)
	}
	if status != bootstrapAdopted {
		t.Fatalf("status=%v", status)
	}
	if a.Name != primaryAccountName || !strings.EqualFold(a.Email, email) || a.AccountID != "acct-old" {
		t.Fatalf("adopted=%+v", a)
	}

	accounts, err := listAccounts(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 1 || accounts[0].Name != primaryAccountName {
		t.Fatalf("accounts=%+v", accounts)
	}

	st, err := loadState(p)
	if err != nil {
		t.Fatal(err)
	}
	if st.ActiveID != a.ID {
		t.Fatalf("adopted account must become active: state=%+v account=%+v", st, a)
	}
	info, err := os.Lstat(p.sharedAuthPath())
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("active auth not a managed symlink: %v %v", info, err)
	}
	live, err := parseAuth(p.sharedAuthPath())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(live.Email, email) {
		t.Fatalf("live auth changed during adoption: %+v", live)
	}
	legacy, err := parseAuth(p.sharedAuthPath() + ".cx-backup")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(legacy.Email, email) {
		t.Fatalf("legacy backup=%+v", legacy)
	}
}

func TestBootstrapAddsNothingWithoutExistingCodexLogin(t *testing.T) {
	p := makeTestPaths(t)

	_, status, err := bootstrapPrimaryAccount(p)
	if err != nil {
		t.Fatal(err)
	}
	if status != bootstrapNoLogin {
		t.Fatalf("status=%v", status)
	}
	accounts, err := listAccounts(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 0 {
		t.Fatalf("bootstrap invented accounts on an unauthorized system: %+v", accounts)
	}
	if st, _ := loadState(p); st.ActiveID != "" {
		t.Fatalf("state=%+v", st)
	}
	if _, err := os.Lstat(p.sharedAuthPath()); !os.IsNotExist(err) {
		t.Fatalf("bootstrap must not create %s: %v", p.sharedAuthPath(), err)
	}
}

func TestBootstrapIgnoresNonChatGPTAuth(t *testing.T) {
	p := makeTestPaths(t)
	raw := []byte(`{"OPENAI_API_KEY":"sk-test"}`)
	if err := os.WriteFile(p.sharedAuthPath(), raw, 0600); err != nil {
		t.Fatal(err)
	}

	_, status, err := bootstrapPrimaryAccount(p)
	if err != nil {
		t.Fatal(err)
	}
	if status != bootstrapNoLogin {
		t.Fatalf("status=%v", status)
	}
	accounts, err := listAccounts(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 0 {
		t.Fatalf("API-key auth must not be adopted: %+v", accounts)
	}
	got, err := os.ReadFile(p.sharedAuthPath())
	if err != nil || string(got) != string(raw) {
		t.Fatalf("existing auth.json was modified: %s %v", got, err)
	}
}

func TestBootstrapIsIdempotentAndKeepsUserChosenNames(t *testing.T) {
	p := makeTestPaths(t)
	if err := os.WriteFile(p.sharedAuthPath(), testAuthBytesFor(t, "acct-old", "a@example.com"), 0600); err != nil {
		t.Fatal(err)
	}
	first, status, err := bootstrapPrimaryAccount(p)
	if err != nil || status != bootstrapAdopted {
		t.Fatalf("first bootstrap: %+v %v %v", first, status, err)
	}
	if err := renameAccount(p, primaryAccountName, "work"); err != nil {
		t.Fatal(err)
	}

	_, status, err = bootstrapPrimaryAccount(p)
	if err != nil {
		t.Fatal(err)
	}
	if status != bootstrapAlreadyManaged {
		t.Fatalf("rerunning the installer must not re-import: status=%v", status)
	}
	accounts, err := listAccounts(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 1 || accounts[0].Name != "work" {
		t.Fatalf("accounts=%+v", accounts)
	}
}

func TestAddKeepsFirstAccountNameAndDoesNotImportExistingLogin(t *testing.T) {
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
	if len(accounts) != 1 || accounts[0].Name != "backup1" {
		t.Fatalf("cx add must manage only what the user asked for: %+v", accounts)
	}

	st, err := loadState(p)
	if err != nil {
		t.Fatal(err)
	}
	if st.ActiveID != added.ID {
		t.Fatalf("first added account should become active: state=%+v added=%+v", st, added)
	}
	live, err := parseAuth(p.sharedAuthPath())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(live.Email, newEmail) {
		t.Fatalf("live auth=%+v", live)
	}
	legacy, err := parseAuth(p.sharedAuthPath() + ".cx-backup")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(legacy.Email, oldEmail) {
		t.Fatalf("unmanaged login must be preserved as a backup: %+v", legacy)
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
