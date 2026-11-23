package cx

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"syscall"
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
		if len(args) > 2 {
			fatal(errors.New("usage: cx shell-init [bash|zsh|fish]"))
		}
		var shell string
		if len(args) == 2 {
			shell = args[1]
		} else {
			shell = detectShell()
		}
		if err := printShellInit(shell); err != nil {
			fatal(err)
		}
	case "init":
		if len(args) != 1 {
			fatal(errors.New("usage: cx init"))
		}
		handleInit(p)
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
	case "agy":
		if err := handleAgy(p, args[1:]); err != nil {
			fatal(err)
		}
	case "run":
		if err := runAgent(p, args[1:], nil); err != nil {
			fatal(err)
		}
	case "auto":
		if len(args) != 1 {
			fatal(errors.New("usage: cx auto"))
		}
		if err := handleAuto(p); err != nil {
			fatal(err)
		}
	case "status":
		code := handleStatus(p, args[1:])
		os.Exit(code)
	case "list":
		handleList(p)
	case "current":
		handleCurrent(p)
	case "rename":
		if len(args) != 3 {
			fatal(errors.New("usage: cx rename [PROVIDER/]OLD NEW"))
		}
		which, name, err := parseAccountSelector(args[1], providerCodex)
		if err != nil {
			fatal(err)
		}
		if err := renameAccountFor(p, name, args[2], which); err != nil {
			fatal(err)
		}
		fmt.Printf("%s renamed %s → %s\n", green("✓"), args[1], args[2])
	case "remove", "rm":
		if len(args) != 2 {
			fatal(errors.New("usage: cx remove [PROVIDER/]NAME"))
		}
		which, name, err := parseAccountSelector(args[1], providerCodex)
		if err != nil {
			fatal(err)
		}
		if err := removeAccountFor(p, name, which); err != nil {
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

func handleInit(p paths) {
	a, status, err := bootstrapPrimaryAccount(p)
	if err != nil {
		fatal(err)
	}
	switch status {
	case bootstrapAdopted:
		fmt.Printf("%s adopted existing Codex login as %s", green("\u2713"), a.Name)
		if a.Email != "" {
			fmt.Printf(" (%s)", a.Email)
		}
		fmt.Println()
		fmt.Println(dim("add more accounts with: cx add NAME --expect EMAIL"))
	case bootstrapAlreadyManaged:
		fmt.Println(dim("cx already manages accounts; nothing to import"))
	default:
		fmt.Println(dim("no existing Codex login found; add your first account with: cx add NAME"))
	}
}

func handleUse(p paths, args []string) {
	if err := useAccount(p, args, nil); err != nil {
		fatal(err)
	}
}

func parseAccountSelector(selector string, fallback provider) (provider, string, error) {
	prefix, name, ok := strings.Cut(selector, "/")
	if !ok {
		return fallback, selector, nil
	}
	switch prefix {
	case "codex":
		return providerCodex, name, nil
	case "agy":
		return providerAgy, name, nil
	default:
		return fallback, "", fmt.Errorf("unknown provider %q", prefix)
	}
}

func useAccount(p paths, args []string, selectedProvider *provider) error {
	resume := false
	name := ""
	for _, a := range args {
		switch {
		case a == "--resume":
			resume = true
		case name == "":
			name = a
		default:
			return errors.New("usage: cx use [PROVIDER/][NAME] [--resume]")
		}
	}
	fallback := providerCodex
	if selectedProvider != nil {
		fallback = *selectedProvider
	}
	which, selector, err := parseAccountSelector(name, fallback)
	if err != nil {
		return err
	}
	var acct Account
	if strings.Contains(name, "/") {
		selectedProvider = &which
	}
	if selector == "" {
		acct, err = pickAccount(p, selectedProvider)
	} else {
		acct, err = findAccountFor(p, selector, which)
	}
	if err != nil {
		return err
	}
	if err := switchAccount(p, acct); err != nil {
		return err
	}
	fmt.Printf("%s switched to %s", green("✓"), acct.Name)
	if acct.Provider != providerCodex {
		fmt.Printf(" [%s]", acct.Provider.label())
	}
	if acct.Email != "" {
		fmt.Printf(" (%s)", acct.Email)
	}
	fmt.Println()
	if resume {
		if acct.Provider == providerAgy {
			return launchAgy(p, acct, []string{"--continue"})
		}
		cmd := exec.Command("codex", "-c", `cli_auth_credentials_store="file"`, "resume", "--last")
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		return cmd.Run()
	}
	return nil
}

func handleStatus(p paths, args []string) int {
	return handleProviderStatus(p, args, providerCodex)
}

func handleProviderStatus(p paths, args []string, which provider) int {
	usage := "cx status [PROVIDER/][NAME] [--json]"
	if which == providerAgy {
		usage = "cx agy status [NAME] [--json]"
	}
	jsonOut := false
	name := ""
	for _, a := range args {
		switch {
		case a == "--json":
			jsonOut = true
		case name == "":
			name = a
		default:
			fatal(errors.New("usage: " + usage))
		}
	}
	var accounts []Account
	var err error
	if name == "" && which == providerCodex {
		accounts, err = listAllAccounts(p)
	} else {
		which, name, err = parseAccountSelector(name, which)
		if err != nil {
			fatal(err)
		}
		accounts, err = listAccountsFor(p, which)
	}
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
		payload := statusJSON(p, results)
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(payload)
	} else {
		printStatus(p, results)
	}
	if failed > 0 {
		return 1
	}
	return 0
}

func handleList(p paths) {
	accounts, err := listAllAccounts(p)
	if err != nil {
		fatal(err)
	}
	st, _ := loadState(p)
	for _, a := range accounts {
		marker := " "
		if a.ID == st.defaultAccountID() {
			marker = "*"
		}
		if a.Provider != providerCodex {
			fmt.Printf("%s %-16s [%s]\n", marker, a.Name, a.Provider.label())
			continue
		}
		fmt.Printf("%s %-16s %-28s %s\n", marker, a.Name, emptyDash(a.Email), emptyDash(a.Plan))
	}
}

func handleCurrent(p paths) {
	accounts, err := listAllAccounts(p)
	if err != nil {
		fatal(err)
	}
	st, _ := loadState(p)
	for _, a := range accounts {
		if a.ID == st.defaultAccountID() {
			if a.Provider != providerCodex {
				fmt.Printf("%s  [%s]\n", a.Name, a.Provider.label())
				return
			}
			fmt.Printf("%s  %s  %s\n", a.Name, emptyDash(a.Email), emptyDash(a.Plan))
			return
		}
	}
	fmt.Println("none")
}

func runAgent(p paths, args []string, selectedProvider *provider) error {
	st, err := loadState(p)
	if err != nil {
		return err
	}
	which := st.DefaultProvider
	if selectedProvider != nil {
		which = *selectedProvider
	}
	accounts, err := listAccountsFor(p, which)
	if err != nil {
		return err
	}
	i := slices.IndexFunc(accounts, func(a Account) bool { return a.ID == st.selectedAccountID(which) })
	if i < 0 {
		return errors.New("no selected account; run cx use NAME or cx agy use NAME")
	}
	switch which {
	case providerCodex:
		return execAgent(exec.Command("codex", append([]string{"-c", `cli_auth_credentials_store="file"`}, args...)...))
	case providerAgy:
		return launchAgy(p, accounts[i], args)
	default:
		return fmt.Errorf("unknown provider %q", which)
	}
}

func execAgent(cmd *exec.Cmd) error {
	if cmd.Err != nil {
		return cmd.Err
	}
	return syscall.Exec(cmd.Path, cmd.Args, cmd.Environ())
}

func printHelp() {
	fmt.Print(`cx — Codex and Antigravity account switcher

Usage:
  cx                                 live dashboard; starts unused Codex windows
  cx status [PROVIDER/][NAME] [--json]
                                     show live quotas for all accounts or a selection
  cx add [NAME] [--expect EMAIL]     add an account with Codex device auth
  cx init                            import an existing Codex login as "primary"
  cx relogin NAME [--expect EMAIL]   refresh a Codex account's credentials
  cx use [PROVIDER/][NAME] [--resume]
                                     activate an account and set its provider as default
  cx auto                            select an account, starting with the default provider
  cx run [ARGS...]                   launch the default provider with its active account
  cx agy help                        show Antigravity account commands
  cx current                         show the account for cx run
  cx list                            list accounts without network access
  cx rename [PROVIDER/]OLD NEW        rename an account
  cx remove|rm [PROVIDER/]NAME        remove an account and its credential
  cx doctor                          check the Codex CLI, credential store, and accounts
  cx shell-init [bash|zsh|fish]       print the optional Codex shell wrapper
  cx update [--force]                install the latest GitHub release
  cx version                         print the cx version

Notes:
  A plain account name targets Codex. Use agy/NAME to target Antigravity.
  Omit NAME with 'cx use' to open the picker. Use 'cx use agy/' to show
  Antigravity accounts, or 'cx use codex/' to show Codex accounts.

  In the dashboard, Enter activates an account for its provider. With multiple
  providers, d sets the provider under the cursor as default. The footer
  shows this shortcut on a folded group. Press ? for all shortcuts.

  'cx shell-init' checks its parent process, then $SHELL, for bash, zsh, or fish.
  If neither is recognized, it prints Bash/Zsh syntax. Pass a shell name to
  select the output explicitly.

  Shell integration is optional. Add the matching line to your startup file
  only if a bare 'codex' keeps serving the previous account after 'cx use',
  which happens when it reattaches to a running app-server daemon.

  Bash/Zsh: eval "$(cx shell-init)"
  Fish:     cx shell-init | source

  Use --expect EMAIL with 'cx add' or 'cx relogin' to reject a device-auth
  login to the wrong ChatGPT account before its credential is saved.
`)
}

func fatal(err error) {
	msg := strings.TrimSpace(err.Error())
	fmt.Fprintln(os.Stderr, red("error:"), msg)
	os.Exit(1)
}
