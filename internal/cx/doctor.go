package cx

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func doctor(p paths) error {
	ok := true
	fmt.Println(bold("cx doctor"))
	if path, err := exec.LookPath("codex"); err != nil {
		fmt.Printf("%s codex: not found\n", red("✗"))
		ok = false
	} else {
		out, _ := exec.Command(path, "--version").CombinedOutput()
		fmt.Printf("%s codex: %s %s\n", green("✓"), path, dim(strings.TrimSpace(string(out))))
	}
	fmt.Printf("%s shared home: %s\n", green("✓"), p.CodexHome)
	if err := ensureFileStoreConfig(p.sharedConfigPath(), true); err != nil {
		fmt.Printf("%s credential store: %v\n", red("✗"), err)
		ok = false
	} else {
		fmt.Printf("%s credential store: file\n", green("✓"))
	}
	as, err := listAccounts(p)
	if err != nil {
		fmt.Printf("%s accounts: %v\n", red("✗"), err)
		ok = false
	} else {
		fmt.Printf("%s accounts: %d\n", green("✓"), len(as))
		for _, a := range as {
			id, e := parseAuth(p.accountAuth(a.ID))
			if e != nil || id.AccountID != a.AccountID {
				fmt.Printf("  %s %s: credential mismatch/corrupt\n", red("✗"), a.Name)
				ok = false
			} else {
				fmt.Printf("  %s %s\n", green("✓"), a.Name)
			}
		}
	}
	live := p.sharedAuthPath()
	if info, err := os.Lstat(live); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			target, _ := os.Readlink(live)
			if !filepath.IsAbs(target) {
				target = filepath.Join(filepath.Dir(live), target)
			}
			root, _ := filepath.Abs(p.AccountsRoot)
			target, _ = filepath.Abs(target)
			if strings.HasPrefix(target, root+string(os.PathSeparator)) {
				fmt.Printf("%s active auth symlink: managed\n", green("✓"))
			} else {
				fmt.Printf("%s active auth symlink: unmanaged\n", red("✗"))
				ok = false
			}
		} else {
			fmt.Printf("%s active auth: regular file (not managed)\n", yellow("!"))
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		fmt.Printf("%s active auth: %v\n", red("✗"), err)
		ok = false
	}

	if runtime, available := codexRuntimeSummary(); available {
		fmt.Printf("%s runtime home: %s\n", green("✓"), emptyDash(runtime.CodexHome))
		st, _ := loadState(p)
		var expected authIdentity
		for _, a := range as {
			if a.ID == st.ActiveID {
				expected, _ = parseAuth(p.accountAuth(a.ID))
				break
			}
		}
		if expected.Email != "" && !runtimeMatchesSelection(runtime, expected) {
			fmt.Printf("%s runtime account: %s · expected %s\n", red("✗"), runtimeAccountLabel(runtime), expected.Email)
			ok = false
		} else {
			fmt.Printf("%s runtime account: %s\n", green("✓"), runtimeAccountLabel(runtime))
		}
	}

	if !ok {
		return errors.New("doctor found problems")
	}
	return nil
}
