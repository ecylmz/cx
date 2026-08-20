package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

var version = "dev"

func main() {
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
		fmt.Printf("cx %s\n", version)
	case "add":
		name := ""
		if len(args) > 1 {
			name = args[1]
		}
		a, err := addAccount(p, name)
		if err != nil {
			fatal(err)
		}
		fmt.Printf("%s added %s", green("✓"), a.Name)
		if a.Email != "" {
			fmt.Printf(" (%s)", a.Email)
		}
		fmt.Println()
	case "relogin":
		if len(args) != 2 {
			fatal(errors.New("usage: cx relogin NAME"))
		}
		a, err := reloginAccount(p, args[1])
		if err != nil {
			fatal(err)
		}
		fmt.Printf("%s refreshed credentials for %s\n", green("✓"), a.Name)
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
	fmt.Printf("%s switched to %s\n", green("✓"), acct.Name)
	if resume {
		cmd := exec.Command("codex", "resume", "--last")
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
  cx                         live weekly quota dashboard; starts unused window
  cx status [NAME] [--json] live weekly quota status; starts unused window
  cx add [NAME]             add account with Codex device auth
  cx relogin NAME           replace one account's credential safely
  cx use [NAME] [--resume]  switch account; interactive if NAME omitted
  cx current                show active account
  cx list                   list accounts without network access
  cx rename OLD NEW
  cx remove NAME
  cx doctor
  cx update [--force]        install latest GitHub release
  cx version

Dashboard keys: ↑/↓ or j/k select · enter switch · r refresh · q quit
`)
}

func fatal(err error) {
	msg := strings.TrimSpace(err.Error())
	fmt.Fprintln(os.Stderr, red("error:"), msg)
	os.Exit(1)
}
