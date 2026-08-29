package cx

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

var Version = "dev"

type loginCommandArgs struct {
	Name          string
	ExpectedEmail string
}

func parseLoginCommandArgs(args []string, requireName bool, usage string) (loginCommandArgs, error) {
	var out loginCommandArgs
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--expect":
			if out.ExpectedEmail != "" || i+1 >= len(args) {
				return loginCommandArgs{}, errors.New("usage: " + usage)
			}
			i++
			out.ExpectedEmail = strings.TrimSpace(args[i])
			if out.ExpectedEmail == "" {
				return loginCommandArgs{}, errors.New("usage: " + usage)
			}
		case strings.HasPrefix(a, "--expect="):
			if out.ExpectedEmail != "" {
				return loginCommandArgs{}, errors.New("usage: " + usage)
			}
			out.ExpectedEmail = strings.TrimSpace(strings.TrimPrefix(a, "--expect="))
			if out.ExpectedEmail == "" {
				return loginCommandArgs{}, errors.New("usage: " + usage)
			}
		case strings.HasPrefix(a, "-"):
			return loginCommandArgs{}, errors.New("usage: " + usage)
		case out.Name == "":
			out.Name = a
		default:
			return loginCommandArgs{}, errors.New("usage: " + usage)
		}
	}
	if requireName && out.Name == "" {
		return loginCommandArgs{}, errors.New("usage: " + usage)
	}
	return out, nil
}

func Main() {
	p, err := resolvePaths()
	if err != nil {
		fatal(err)
	}
	if err := p.ensure(); err != nil {
		fatal(err)
	}

	args := os.Args[1:]
	if len(args) == 0 {
		if err := runDashboard(p); err != nil {
			fatal(err)
		}
		return
	}

	switch args[0] {
	case "help", "-h", "--help":
		printHelp()
	case "version", "--version":
		fmt.Printf("cx %s\n", Version)
	case "shell-init":
		if len(args) != 1 {
			fatal(errors.New("usage: cx shell-init"))
		}
		printShellInit()
	case "add":
		opts, err := parseLoginCommandArgs(args[1:], false, "cx add [NAME] [--expect EMAIL]")
		if err != nil {
			fatal(err)
		}
		a, err := addAccount(p, opts.Name, opts.ExpectedEmail)
		if err != nil {
			fatal(err)
		}
		fmt.Printf("%s added %s", green("✓"), a.Name)
		if a.Email != "" {
			fmt.Printf(" (%s)", a.Email)
		}
		fmt.Println()
	case "relogin":
		opts, err := parseLoginCommandArgs(args[1:], true, "cx relogin NAME [--expect EMAIL]")
		if err != nil {
			fatal(err)
		}
		a, err := reloginAccount(p, opts.Name, opts.ExpectedEmail)
		if err != nil {
			fatal(err)
		}
		fmt.Printf("%s refreshed credentials for %s (%s)\n", green("✓"), a.Name, emptyDash(a.Email))
	case "use":
		handleUse(p, args[1:])
	case "status":
		code := handleStatus(p, args[1:])
		os.Exit(code)
	case "list":
		handleList(p)
	case "current":
		handleCurrent(p)
	case "rename":
		if len(args) != 3 {
			fatal(errors.New("usage: cx rename OLD NEW"))
		}
		if err := renameAccount(p, args[1], args[2]); err != nil {
			fatal(err)
		}
		fmt.Printf("%s renamed %s → %s\n", green("✓"), args[1], args[2])
	case "remove", "rm":
		if len(args) != 2 {
			fatal(errors.New("usage: cx remove NAME"))
		}
		if err := removeAccount(p, args[1]); err != nil {
			fatal(err)
		}
		fmt.Printf("%s removed %s\n", green("✓"), args[1])
	case "doctor":
		if err := doctor(p); err != nil {
			os.Exit(1)
		}
	case "update":
		if err := handleUpdate(args[1:]); err != nil {
			fatal(err)
		}
	default:
		fatal(fmt.Errorf("unknown command %q; run cx help", args[0]))
	}
}

func handleUse(p paths, args []string) {
	resume := false
	name := ""
	for _, a := range args {
		if a == "--resume" {
			resume = true
		} else if name == "" {
			name = a
		} else {
			fatal(errors.New("usage: cx use [NAME] [--resume]"))
		}
	}
	var acct Account
	var err error
	if name == "" {
		acct, err = pickAccount(p)
	} else {
		acct, err = findAccount(p, name)
	}
	if err != nil {
		fatal(err)
	}
	if err := switchAccount(p, acct); err != nil {
		fatal(err)
	}
	fmt.Printf("%s switched to %s", green("✓"), acct.Name)
	if acct.Email != "" {
		fmt.Printf(" (%s)", acct.Email)
	}
	fmt.Println()
	if resume {
		cmd := exec.Command("codex", "-c", `cli_auth_credentials_store="file"`, "resume", "--last")
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			fatal(err)
		}
	}
}

func handleStatus(p paths, args []string) int {
	jsonOut := false
	name := ""
	for _, a := range args {
		if a == "--json" {
			jsonOut = true
		} else if name == "" {
			name = a
		} else {
			fatal(errors.New("usage: cx status [NAME] [--json]"))
		}
	}
	accounts, err := listAccounts(p)
	if err != nil {
		fatal(err)
	}
	if name != "" {
		a, err := findIn(accounts, name)
		if err != nil {
			fatal(err)
		}
		accounts = []Account{a}
	}
	if len(accounts) == 0 {
		fatal(errors.New("no accounts; add one with: cx add NAME"))
	}

	results := fetchAllUsageWithPriming(p, accounts)
	cache, _ := loadCache(p)
	failed := 0
	for i := range results {
		if results[i].Err != "" {
			failed++
			if old, ok := cache[results[i].Account.ID]; ok {
				old.Fresh = false
				old.Err = results[i].Err
				results[i].Usage = old
			}
		}
		if results[i].PrimeErr != "" {
			failed++
		}
	}
	saveFreshCache(p, results)

	if jsonOut {
		payload := statusJSON(p, accounts, results)
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(payload)
	} else {
		printStatus(p, accounts, results)
	}
	if failed > 0 {
		return 1
	}
	return 0
}

func handleList(p paths) {
	accounts, err := listAccounts(p)
	if err != nil {
		fatal(err)
	}
	st, _ := loadState(p)
	for _, a := range accounts {
		marker := " "
		if a.ID == st.ActiveID {
			marker = "*"
		}
		fmt.Printf("%s %-16s %-28s %s\n", marker, a.Name, emptyDash(a.Email), emptyDash(a.Plan))
	}
}

func handleCurrent(p paths) {
	accounts, err := listAccounts(p)
	if err != nil {
		fatal(err)
	}
	st, _ := loadState(p)
	for _, a := range accounts {
		if a.ID == st.ActiveID {
			fmt.Printf("%s  %s  %s\n", a.Name, emptyDash(a.Email), emptyDash(a.Plan))
			return
		}
	}
	fmt.Println("none")
}

func printHelp() {
	fmt.Print(`cx — minimal Codex account switcher

Usage:
  cx                                  live quota dashboard; starts unused windows
  cx status [NAME] [--json]           live quota status; starts unused windows
  cx add [NAME] [--expect EMAIL]      add account with Codex device auth
  cx relogin NAME [--expect EMAIL]    refresh one account's credential safely
  cx use [NAME] [--resume]            switch account; interactive if NAME omitted
  cx current                          show active account
  cx list                             list accounts without network access
  cx rename OLD NEW
  cx remove NAME
  cx doctor
  cx shell-init                       shell wrapper for deterministic Codex auth
  cx update [--force]                 install latest GitHub release
  cx version

Add 'eval "$(cx shell-init)"' to your shell startup file. It keeps bare Codex launches on the selected auth.json instead of reusing a stale local app-server account.

Use --expect EMAIL when adding or re-authorizing an account to reject a browser/device-auth login to the wrong ChatGPT account before its credential is saved.

Dashboard keys: ↑/↓ or j/k select · enter switch · r refresh · q quit
`)
}

func fatal(err error) {
	msg := strings.TrimSpace(err.Error())
	fmt.Fprintln(os.Stderr, red("error:"), msg)
	os.Exit(1)
}
