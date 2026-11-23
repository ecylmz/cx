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

type accountCommandArgs struct {
	names         []string
	provider      *provider
	tier          string
	option        bool
	expectedEmail string
}

func parseAccountCommandArgs(args []string, option, usage string, allowSubscription bool) (accountCommandArgs, error) {
	var out accountCommandArgs
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			out.names = append(out.names, args[i+1:]...)
			break
		}
		flag, value, inline := strings.Cut(arg, "=")
		switch {
		case flag == "--provider", flag == "--expect" && option == "--expect", flag == "--subscription" && allowSubscription:
			if !inline {
				if i+1 == len(args) || strings.HasPrefix(args[i+1], "--") {
					return accountCommandArgs{}, errors.New("usage: " + usage)
				}
				i++
				value = args[i]
			}
			switch flag {
			case "--expect":
				value = strings.TrimSpace(value)
				if out.expectedEmail != "" || value == "" {
					return accountCommandArgs{}, errors.New("usage: " + usage)
				}
				out.expectedEmail = value
			case "--subscription":
				value = strings.ToLower(strings.TrimSpace(value))
				if out.tier != "" || validateName(value) != nil {
					return accountCommandArgs{}, errors.New("usage: " + usage)
				}
				out.tier = value
			case "--provider":
				if out.provider != nil {
					return accountCommandArgs{}, errors.New("usage: " + usage)
				}
				var which provider
				switch value {
				case "codex":
					which = providerCodex
				case "agy":
					which = providerAgy
				default:
					return accountCommandArgs{}, fmt.Errorf("unknown provider %q (expected codex or agy)", value)
				}
				out.provider = &which
			}
		case option != "" && arg == option:
			out.option = true
		case strings.HasPrefix(arg, "--"):
			return accountCommandArgs{}, errors.New("usage: " + usage)
		default:
			out.names = append(out.names, arg)
		}
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
	case "init", "add":
		if err := registerAccount(p, args[0], args[1:]); err != nil {
			fatal(err)
		}
	case "relogin":
		const usage = "cx relogin NAME [--provider codex] [--expect EMAIL]"
		opts, err := parseAccountCommandArgs(args[1:], "--expect", usage, false)
		if err != nil {
			fatal(err)
		}
		if len(opts.names) != 1 || opts.names[0] == "" {
			fatal(errors.New("usage: " + usage))
		}
		if opts.provider != nil && *opts.provider != providerCodex {
			fatal(errors.New("relogin supports only Codex accounts"))
		}
		a, err := reloginAccount(p, opts.names[0], opts.expectedEmail)
		if err != nil {
			fatal(err)
		}
		fmt.Printf("%s refreshed credentials for %s (%s)\n", green("✓"), a.Name, emptyDash(a.Email))
	case "use":
		if err := useAccount(p, args[1:]); err != nil {
			fatal(err)
		}
	case "run":
		if err := runAgent(p, args[1:]); err != nil {
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
		const usage = "cx rename OLD NEW [--provider PROVIDER]"
		opts, err := parseAccountCommandArgs(args[1:], "", usage, false)
		if err != nil {
			fatal(err)
		}
		if len(opts.names) != 2 {
			fatal(errors.New("usage: " + usage))
		}
		if err := renameAccount(p, opts.names[0], opts.names[1], opts.provider); err != nil {
			fatal(err)
		}
		fmt.Printf("%s renamed %s → %s\n", green("✓"), opts.names[0], opts.names[1])
	case "remove", "rm":
		const usage = "cx remove NAME [--provider PROVIDER]"
		opts, err := parseAccountCommandArgs(args[1:], "", usage, false)
		if err != nil {
			fatal(err)
		}
		if len(opts.names) != 1 {
			fatal(errors.New("usage: " + usage))
		}
		if err := removeAccount(p, opts.names[0], opts.provider); err != nil {
			fatal(err)
		}
		fmt.Printf("%s removed %s\n", green("✓"), opts.names[0])
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

func registerAccount(p paths, command string, args []string) error {
	initializing := command == "init"
	usage, option := "cx add [NAME] [--provider PROVIDER] [--subscription TIER] [--expect EMAIL]", "--expect"
	if initializing {
		usage, option = "cx init [NAME] [--provider PROVIDER] [--subscription TIER]", ""
	}
	opts, err := parseAccountCommandArgs(args, option, usage, true)
	if err != nil {
		return err
	}
	if len(opts.names) > 1 {
		return errors.New("usage: " + usage)
	}
	name := ""
	if len(opts.names) == 1 {
		name = opts.names[0]
	}
	which := providerCodex
	if opts.provider != nil {
		which = *opts.provider
	}
	sub, err := availableSubscription(which, opts.tier)
	if err != nil {
		return err
	}
	if opts.tier == "" {
		sub = ""
	}
	if which == providerAgy {
		if opts.expectedEmail != "" {
			return errors.New("--expect supports only Codex accounts")
		}
		if len(opts.names) == 0 {
			if !initializing {
				return errors.New("usage: cx add NAME --provider agy")
			}
			name = primaryAccountName
		}
		a, err := addAgyAccount(p, name, sub, initializing)
		if err != nil {
			return err
		}
		fmt.Printf("%s added %s [%s]", green("✓"), a.Name, a.subscription().label())
		if !initializing {
			fmt.Print(" and switched to it")
		}
		fmt.Println()
		return nil
	}
	if initializing {
		if len(opts.names) != 0 {
			return errors.New("usage: cx init [--provider codex] [--subscription TIER]")
		}
		handleInit(p, sub)
		return nil
	}
	a, err := addAccount(p, name, opts.expectedEmail, sub)
	if err != nil {
		return err
	}
	fmt.Printf("%s added %s", green("✓"), a.Name)
	if a.Email != "" {
		fmt.Printf(" (%s)", a.Email)
	}
	fmt.Println()
	return nil
}

func handleInit(p paths, sub subscription) {
	a, status, err := bootstrapPrimaryAccount(p, sub)
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

func useAccount(p paths, args []string) error {
	const usage = "cx use [NAME] [--provider PROVIDER] [--subscription TIER] [--resume]"
	opts, err := parseAccountCommandArgs(args, "--resume", usage, true)
	if err != nil {
		return err
	}
	if len(opts.names) > 1 {
		return errors.New("usage: " + usage)
	}
	var acct Account
	if len(opts.names) == 0 {
		acct, err = pickAccount(p, opts.provider, opts.tier)
	} else {
		accounts, listErr := listScopedAccounts(p, opts.provider)
		if listErr != nil {
			return listErr
		}
		acct, err = findIn(accountsByTier(accounts, opts.tier), opts.names[0])
	}
	if err != nil {
		return err
	}
	if err := switchAccount(p, acct); err != nil {
		return err
	}
	fmt.Printf("%s switched to %s", green("✓"), acct.Name)
	if acct.provider() != providerCodex {
		fmt.Printf(" [%s]", acct.subscription().label())
	}
	if acct.Email != "" {
		fmt.Printf(" (%s)", acct.Email)
	}
	fmt.Println()
	if opts.option {
		if acct.provider() == providerAgy {
			return launchAgy([]string{"--continue"})
		}
		cmd := exec.Command("codex", "-c", `cli_auth_credentials_store="file"`, "resume", "--last")
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		return cmd.Run()
	}
	return nil
}

func handleStatus(p paths, args []string) int {
	const usage = "cx status [NAME] [--provider PROVIDER] [--subscription TIER] [--json]"
	opts, err := parseAccountCommandArgs(args, "--json", usage, true)
	if err != nil {
		fatal(err)
	}
	if len(opts.names) > 1 {
		fatal(errors.New("usage: " + usage))
	}
	accounts, err := listScopedAccounts(p, opts.provider)
	if err != nil {
		fatal(err)
	}
	accounts = accountsByTier(accounts, opts.tier)
	if len(opts.names) == 1 {
		a, err := findIn(accounts, opts.names[0])
		if err != nil {
			fatal(err)
		}
		accounts = []Account{a}
	}
	if len(accounts) == 0 {
		if opts.provider != nil {
			fatal(fmt.Errorf("no %s accounts", opts.provider.label()))
		}
		fatal(errors.New("no accounts. Run cx add NAME or cx add NAME --provider agy"))
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

	if opts.option {
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
		fmt.Printf("%s %-16s %-28s [%s]\n", marker, a.Name, emptyDash(a.Email), a.subscription().label())
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
			fmt.Printf("%s  %s  [%s]\n", a.Name, emptyDash(a.Email), a.subscription().label())
			return
		}
	}
	fmt.Println("none")
}

func runAgent(p paths, args []string) error {
	var selectedProvider *provider
	if len(args) > 0 {
		flag, _, inline := strings.Cut(args[0], "=")
		if flag == "--provider" {
			n := 1
			if !inline && len(args) > 1 {
				n = 2
			}
			opts, err := parseAccountCommandArgs(args[:n], "", "cx run [--provider PROVIDER] [ARGS...]", false)
			if err != nil {
				return err
			}
			selectedProvider, args = opts.provider, args[n:]
		}
	}
	if len(args) > 0 && args[0] == "--" {
		args = args[1:]
	}
	st, err := loadState(p)
	if err != nil {
		return err
	}
	id := st.defaultAccountID()
	if selectedProvider != nil {
		id = st.activeAccountID(*selectedProvider)
	}
	if id == "" {
		return errors.New("no active account. Run cx use NAME to select one")
	}
	accounts, err := listScopedAccounts(p, selectedProvider)
	if err != nil {
		return err
	}
	i := slices.IndexFunc(accounts, func(a Account) bool { return a.ID == id })
	if i < 0 {
		return errors.New("active account was not found. Run cx use NAME to select one")
	}
	account := accounts[i]
	if err := activateAccount(p, account, false); err != nil {
		return err
	}
	if account.provider() == providerAgy {
		return launchAgy(args)
	}
	return execAgent(exec.Command("codex", append([]string{"-c", `cli_auth_credentials_store="file"`}, args...)...))
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
  cx status [NAME] [--provider PROVIDER] [--subscription TIER] [--json]
                                     show live quotas for all accounts or a selection
  cx add [NAME] [--provider codex] [--subscription TIER] [--expect EMAIL]
  cx add NAME --provider agy [--subscription TIER]
                                     sign in and add the account
  cx init [--provider codex] [--subscription TIER]
  cx init [NAME] --provider agy [--subscription TIER]
                                     register an existing login as "primary" by default
  cx relogin NAME [--provider codex] [--expect EMAIL]
                                     refresh a Codex account's credentials
  cx use [NAME] [--provider PROVIDER] [--subscription TIER] [--resume]
                                     activate an account and set its subscription as default
  cx auto                            select an account, starting with the default subscription
  cx run [--provider PROVIDER] [ARGS...]
                                     launch the provider CLI with the active account
  cx current                         show the default account and its subscription
  cx list                            list accounts without network access
  cx rename OLD NEW [--provider PROVIDER]
                                     rename an account
  cx remove|rm NAME [--provider PROVIDER]
                                     remove an account and its credential
  cx doctor                          check the Codex CLI, credential store, and accounts
  cx shell-init [bash|zsh|fish]       print the optional Codex shell wrapper
  cx update [--force]                install the latest GitHub release
  cx version                         print the cx version

Providers:
  Use codex for Codex and agy for Antigravity. The init and add commands use
  Codex by default. Antigravity requires agy 1.5.14 or later.
  The default tier is Plus for Codex and Pro for Antigravity.
  Run 'cx init --provider agy' to register an existing Antigravity login.
  Run 'cx add NAME --provider agy' in a terminal to add a Google account.
  After you sign in, cx activates the account and makes its subscription the default.

  If an account name matches more than one provider, specify --provider codex
  or --provider agy. Omit NAME with 'cx use' to open the picker. Add
  --provider to limit the picker or the status list to one provider.

  'cx run' launches the account in the default subscription. To use a different
  provider, put --provider before the arguments for that provider's CLI.

Notes:
  In the dashboard, Enter activates an account and s opens its subscription
  menu. Tab moves to the next subscription. Press d to set it as default
  and ? for all shortcuts.

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
