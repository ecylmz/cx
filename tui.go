package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

func runDashboard(p paths) error {
	accounts, err := listAccounts(p)
	if err != nil {
		return err
	}
	if len(accounts) == 0 {
		return errors.New("no accounts; add one with: cx add NAME")
	}
	sel := 0
	for {
		results := fetchAllUsageWithPriming(p, accounts)
		cache, _ := loadCache(p)
		for i := range results {
			if results[i].Err != "" {
				if old, ok := cache[results[i].Account.ID]; ok {
					old.Fresh = false
					old.Err = results[i].Err
					results[i].Usage = old
				}
			}
		}
		saveFreshCache(p, results)
		idx, action, err := dashboardLoop(p, accounts, results, sel)
		if err != nil {
			return err
		}
		sel = idx
		switch action {
		case "quit":
			return nil
		case "refresh":
			continue
		case "switch":
			if err := switchAccount(p, accounts[sel]); err != nil {
				return err
			}
			accounts, _ = listAccounts(p)
			sel = 0
			continue
		}
	}
}

func dashboardLoop(p paths, accounts []Account, results []UsageResult, sel int) (int, string, error) {
	if !isTerminal() {
		printStatus(p, accounts, results)
		return sel, "quit", nil
	}
	old, err := rawMode()
	if err != nil {
		return sel, "", err
	}
	defer restoreMode(old)
	for {
		drawDashboard(p, accounts, results, sel)
		key, err := readKey()
		if err != nil {
			return sel, "", err
		}
		switch key {
		case "up", "k":
			if sel > 0 {
				sel--
			}
		case "down", "j":
			if sel < len(accounts)-1 {
				sel++
			}
		case "enter":
			return sel, "switch", nil
		case "r":
			return sel, "refresh", nil
		case "q", "esc":
			return sel, "quit", nil
		}
	}
}

func drawDashboard(p paths, accounts []Account, results []UsageResult, sel int) {
	fmt.Print("\x1b[2J\x1b[H")
	host, _ := os.Hostname()
	fmt.Printf(" %s  %s\n\n", bold("cx"), dim(host))
	st, _ := loadState(p)
	for i, r := range results {
		cursor := "  "
		if i == sel {
			cursor = cyan("› ")
		}
		active := dim("○")
		if r.Account.ID == st.ActiveID {
			active = green("●")
		}
		fmt.Printf("%s%s %-18s", cursor, active, bold(r.Account.Name))
		if r.Account.Plan != "" {
			fmt.Printf(" %-10s", r.Account.Plan)
		}
		if r.Account.Email != "" {
			fmt.Printf(" %s", dim(r.Account.Email))
		}
		fmt.Println()
		if r.Err == "" {
			fmt.Println("   " + usageLine(r.Usage)[2:])
			if r.PrimeErr != "" {
				fmt.Printf("   %s %s\n", red("window start failed"), r.PrimeErr)
			} else if r.Primed {
				fmt.Println("   " + dim("weekly window started just now"))
			}
		} else if !r.Usage.FetchedAt.IsZero() {
			fmt.Println("   " + usageLine(r.Usage)[2:])
			fmt.Printf("   %s · cached %s ago\n", yellow("stale"), shortDuration(time.Since(r.Usage.FetchedAt)))
		} else {
			fmt.Printf("   %s %s\n", red("unavailable"), r.Err)
		}
		fmt.Println()
	}
	fmt.Println(dim(" ↑/↓ or j/k select   enter switch   r refresh   q quit"))
	fmt.Println(dim(" live quota read on every refresh"))
}

func pickAccount(p paths) (Account, error) {
	accounts, err := listAccounts(p)
	if err != nil {
		return Account{}, err
	}
	if len(accounts) == 0 {
		return Account{}, errors.New("no accounts")
	}
	if !isTerminal() {
		return Account{}, errors.New("interactive selection requires a terminal")
	}
	old, err := rawMode()
	if err != nil {
		return Account{}, err
	}
	defer restoreMode(old)
	sel := 0
	for {
		fmt.Print("\x1b[2J\x1b[H")
		fmt.Println(bold(" Switch Codex account"))
		fmt.Println()
		for i, a := range accounts {
			c := "  "
			if i == sel {
				c = cyan("› ")
			}
			fmt.Printf("%s%-18s %s\n", c, a.Name, dim(emptyDash(a.Email)))
		}
		fmt.Println()
		fmt.Println(dim(" ↑/↓ or j/k · enter switch · esc cancel"))
		k, e := readKey()
		if e != nil {
			return Account{}, e
		}
		switch k {
		case "up", "k":
			if sel > 0 {
				sel--
			}
		case "down", "j":
			if sel < len(accounts)-1 {
				sel++
			}
		case "enter":
			return accounts[sel], nil
		case "esc", "q":
			return Account{}, errors.New("cancelled")
		}
	}
}

func isTerminal() bool { s, _ := os.Stdin.Stat(); return s != nil && (s.Mode()&os.ModeCharDevice) != 0 }
func rawMode() (string, error) {
	get := exec.Command("stty", "-g")
	get.Stdin = os.Stdin
	b, err := get.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(b))
		if msg != "" {
			return "", fmt.Errorf("read terminal mode: %w: %s", err, msg)
		}
		return "", fmt.Errorf("read terminal mode: %w", err)
	}
	old := strings.TrimSpace(string(b))
	cmd := exec.Command("stty", "raw", "-echo")
	cmd.Stdin = os.Stdin
	b, err = cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(b))
		if msg != "" {
			return "", fmt.Errorf("enable terminal raw mode: %w: %s", err, msg)
		}
		return "", fmt.Errorf("enable terminal raw mode: %w", err)
	}
	fmt.Print("\x1b[?25l")
	return old, nil
}
func restoreMode(old string) {
	if old == "" {
		return
	}
	cmd := exec.Command("stty", old)
	cmd.Stdin = os.Stdin
	_ = cmd.Run()
	fmt.Print("\x1b[0m\x1b[?25h")
}
func readKey() (string, error) {
	r := bufio.NewReader(os.Stdin)
	b, err := r.ReadByte()
	if err != nil {
		return "", err
	}
	switch b {
	case '\r', '\n':
		return "enter", nil
	case 27:
		b2, _ := r.ReadByte()
		if b2 != '[' {
			return "esc", nil
		}
		b3, _ := r.ReadByte()
		if b3 == 'A' {
			return "up", nil
		}
		if b3 == 'B' {
			return "down", nil
		}
		return "esc", nil
	default:
		return string([]byte{b}), nil
	}
}
