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
	accounts, err := listAllAccounts(p)
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
	activeID := st.defaultAccountID()
	active := slices.IndexFunc(accounts, func(a Account) bool { return a.ID == activeID })
	if active < 0 {
		return errors.New("no active account; select one with: cx use NAME")
	}

	activeResult := fetchAccountQuota(p, accounts[active])
	weekly, fiveHour, err := autoQuotaRemaining(activeResult)
	if err == nil && weekly > 0 && fiveHour > 0 {
		fmt.Printf("current account still has quota · keeping %s\n", activeResult.Account.Name)
		return nil
	}

	var same, other []Account
	for i, a := range accounts {
		if i == active {
			continue
		}
		if a.subscription() == accounts[active].subscription() {
			same = append(same, a)
		} else {
			other = append(other, a)
		}
	}
	results := append([]UsageResult{activeResult}, fetchAllAutoUsage(p, same)...)
	account, _, err := selectAutoAccount(results, activeID)
	if err != nil && len(other) > 0 {
		results = append(results, fetchAllAutoUsage(p, other)...)
		account, _, err = selectAutoAccount(results, activeID)
	}
	if err != nil {
		return err
	}
	if err := switchAccount(p, account); err != nil {
		return err
	}

	fmt.Printf("%s switched to %s", green("✓"), account.Name)
	if account.subscription() != accounts[active].subscription() {
		fmt.Printf(" [%s]", account.subscription().label())
	}
	if account.Email != "" {
		fmt.Printf(" (%s)", account.Email)
	}
	fmt.Println()
	return nil
}

func fetchAccountQuota(p paths, account Account) UsageResult {
	switch account.provider() {
	case providerAgy:
		return fetchAgyUsage(p, account)
	case providerCodex:
		fiveHour, weekly, err := fetchUsagePair(p, &account)
		result := UsageResult{Account: account, Usage: weekly, FiveHour: fiveHour}
		if err != nil {
			result.Err = err.Error()
		}
		return result
	default:
		return UsageResult{Account: account, Err: fmt.Sprintf("unknown provider %q", account.provider())}
	}
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
			results[i] = fetchAccountQuota(p, accounts[i])
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
	if err == nil && weekly > 0 && fiveHour > 0 {
		return results[active].Account, true, nil
	}

	best := -1
	var bestWeekly, bestFiveHour float64
	var unreadable []string
	for i := range results {
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
	if len(result.QuotaGroups) > 0 {
		for _, group := range result.QuotaGroups {
			w, f, err := quotaRemaining(group.Weekly, group.FiveHour)
			if err == nil && w > 0 && f > 0 && (w > weekly || (w == weekly && f > fiveHour)) {
				weekly, fiveHour = w, f
			}
		}
		return weekly, fiveHour, nil
	}
	return quotaRemaining(result.Usage, result.FiveHour)
}

func quotaRemaining(usage WeeklyUsage, five *WeeklyUsage) (weekly, fiveHour float64, err error) {
	weekly, fiveHour = 100, 100
	if usage == (WeeklyUsage{}) && five == nil {
		return 0, 0, errors.New("no quota windows available")
	}
	if usage != (WeeklyUsage{}) {
		if !usage.Fresh || usage.WindowMinutes <= 0 {
			return 0, 0, errors.New("weekly quota is unavailable")
		}
		weekly = clamp(100-usage.UsedPercent, 0, 100)
	}
	if five != nil {
		if !five.Fresh || five.WindowMinutes <= 0 {
			return 0, 0, errors.New("5-hour quota is unavailable")
		}
		fiveHour = clamp(100-five.UsedPercent, 0, 100)
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
