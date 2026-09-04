package cx

import (
	"errors"
	"fmt"
	"strings"
)

var errAllAccountsExhausted = errors.New("all accounts are exhausted")

func handleAuto(p paths) error {
	accounts, err := listAccounts(p)
	if err != nil {
		return err
	}
	if len(accounts) == 0 {
		return errors.New("no accounts; add one with: cx add NAME")
	}

	st, err := loadState(p)
	if err != nil {
		return err
	}
	active := false
	for _, account := range accounts {
		if account.ID == st.ActiveID {
			active = true
			break
		}
	}
	if !active {
		return errors.New("no active account; select one with: cx use NAME")
	}

	account, keep, err := selectAutoAccount(fetchAllUsage(p, accounts), st.ActiveID)
	if err != nil {
		return err
	}
	if keep {
		fmt.Printf("current account still has quota · keeping %s\n", account.Name)
		return nil
	}
	if err := switchAccount(p, account); err != nil {
		return err
	}

	fmt.Printf("%s switched to %s", green("✓"), account.Name)
	if account.Email != "" {
		fmt.Printf(" (%s)", account.Email)
	}
	fmt.Println()
	return nil
}

func selectAutoAccount(results []UsageResult, activeID string) (Account, bool, error) {
	active := -1
	for i := range results {
		if results[i].Account.ID == activeID {
			active = i
			break
		}
	}
	if active < 0 {
		return Account{}, false, errors.New("no active account; select one with: cx use NAME")
	}

	weekly, fiveHour, err := autoQuotaRemaining(results[active])
	if err != nil {
		return Account{}, false, fmt.Errorf("quota unavailable for %s: %w", results[active].Account.Name, err)
	}
	if weekly > 0 && fiveHour > 0 {
		return results[active].Account, true, nil
	}

	best := -1
	var bestWeekly, bestFiveHour float64
	for i := range results {
		if i == active {
			continue
		}
		weekly, fiveHour, err := autoQuotaRemaining(results[i])
		if err != nil {
			return Account{}, false, fmt.Errorf("quota unavailable for %s: %w", results[i].Account.Name, err)
		}
		if weekly <= 0 || fiveHour <= 0 {
			continue
		}
		if best < 0 || weekly < bestWeekly ||
			(weekly == bestWeekly && fiveHour < bestFiveHour) ||
			(weekly == bestWeekly && fiveHour == bestFiveHour && accountBefore(results[i].Account, results[best].Account)) {
			best, bestWeekly, bestFiveHour = i, weekly, fiveHour
		}
	}
	if best < 0 {
		return Account{}, false, errAllAccountsExhausted
	}
	return results[best].Account, false, nil
}

func autoQuotaRemaining(result UsageResult) (weekly, fiveHour float64, err error) {
	if result.Err != "" {
		return 0, 0, errors.New(result.Err)
	}
	if !result.Usage.Fresh || result.Usage.WindowMinutes <= 0 {
		return 0, 0, errors.New("weekly quota is unavailable")
	}
	if result.FiveHour == nil || !result.FiveHour.Fresh || result.FiveHour.WindowMinutes <= 0 {
		return 0, 0, errors.New("5-hour quota is unavailable")
	}
	return clamp(100-result.Usage.UsedPercent, 0, 100), clamp(100-result.FiveHour.UsedPercent, 0, 100), nil
}

func accountBefore(a, b Account) bool {
	if !a.CreatedAt.Equal(b.CreatedAt) {
		return a.CreatedAt.Before(b.CreatedAt)
	}
	aName, bName := strings.ToLower(a.Name), strings.ToLower(b.Name)
	if aName != bName {
		return aName < bName
	}
	return a.ID < b.ID
}
