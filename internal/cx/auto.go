package cx

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
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
	active := slices.IndexFunc(accounts, func(a Account) bool { return a.ID == st.ActiveID })
	if active < 0 {
		return errors.New("no active account; select one with: cx use NAME")
	}

	activeResult := fetchAutoUsage(p, accounts[active])
	weekly, fiveHour, err := autoQuotaRemaining(activeResult)
	if err != nil {
		return fmt.Errorf("quota unavailable for %s: %w", activeResult.Account.Name, err)
	}
	if weekly > 0 && fiveHour > 0 {
		fmt.Printf("current account still has quota · keeping %s\n", activeResult.Account.Name)
		return nil
	}

	candidates := slices.Concat(accounts[:active], accounts[active+1:])
	results := append([]UsageResult{activeResult}, fetchAllAutoUsage(p, candidates)...)
	account, _, err := selectAutoAccount(results, st.ActiveID)
	if err != nil {
		return err
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

func fetchAutoUsage(p paths, account Account) UsageResult {
	fiveHour, weekly, err := fetchUsagePair(p, &account)
	result := UsageResult{Account: account, Usage: weekly, FiveHour: fiveHour}
	if err != nil {
		result.Err = err.Error()
	}
	return result
}

func fetchAllAutoUsage(p paths, accounts []Account) []UsageResult {
	results := make([]UsageResult, len(accounts))
	sem := make(chan struct{}, usageConcurrency)
	var wg sync.WaitGroup
	for i := range accounts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			results[i] = fetchAutoUsage(p, accounts[i])
		}()
	}
	wg.Wait()
	return results
}

func selectAutoAccount(results []UsageResult, activeID string) (Account, bool, error) {
	active := slices.IndexFunc(results, func(r UsageResult) bool { return r.Account.ID == activeID })
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
	var unreadable []string
	for i := range results {
		if i == active {
			continue
		}
		weekly, fiveHour, err := autoQuotaRemaining(results[i])
		if err != nil {
			// A candidate cx cannot read is not a verdict on the ones it can.
			// A stale token, one slow response among several concurrent ones, or
			// an account the backend reports no Codex window for used to veto
			// every usable account on the list — at the single moment that list
			// matters, when the active account has just run out.
			unreadable = append(unreadable, fmt.Sprintf("%s: %v", results[i].Account.Name, err))
			continue
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
		// Skipping an unreadable candidate must not make it disappear: with
		// nothing usable left, why cx could not read it is the whole answer,
		// and "exhausted" would send the operator to wait for a reset that is
		// not what stands in the way.
		if len(unreadable) > 0 {
			return Account{}, false, fmt.Errorf("no usable account; quota unavailable for %s", strings.Join(unreadable, ", "))
		}
		return Account{}, false, errAllAccountsExhausted
	}
	return results[best].Account, false, nil
}

func autoQuotaRemaining(result UsageResult) (weekly, fiveHour float64, err error) {
	if result.Err != "" {
		return 0, 0, errors.New(result.Err)
	}
	weekly, fiveHour = 100, 100
	if result.Usage == (WeeklyUsage{}) && result.FiveHour == nil {
		return 0, 0, errors.New("no quota windows available")
	}
	if result.Usage != (WeeklyUsage{}) {
		if !result.Usage.Fresh || result.Usage.WindowMinutes <= 0 {
			return 0, 0, errors.New("weekly quota is unavailable")
		}
		weekly = clamp(100-result.Usage.UsedPercent, 0, 100)
	}
	if result.FiveHour != nil {
		if !result.FiveHour.Fresh || result.FiveHour.WindowMinutes <= 0 {
			return 0, 0, errors.New("5-hour quota is unavailable")
		}
		fiveHour = clamp(100-result.FiveHour.UsedPercent, 0, 100)
	}
	return weekly, fiveHour, nil
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
