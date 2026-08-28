package cx

import "sync"

type usageUpdate struct {
	Index  int
	Result UsageResult
	Final  bool
}

func fetchUsageUpdatesWithPriming(p paths, accounts []Account) <-chan usageUpdate {
	updates := make(chan usageUpdate, len(accounts)*2)
	usageSem := make(chan struct{}, usageConcurrency)
	primeSem := make(chan struct{}, primeConcurrency)
	var wg sync.WaitGroup

	for i, a := range accounts {
		wg.Add(1)
		go func(i int, a Account) {
			defer wg.Done()

			usageSem <- struct{}{}
			rs := fetchAllUsage(p, []Account{a})
			<-usageSem
			r := rs[0]
			if r.Err == "" {
				annotateWindowState(p, &r)
				annotateFiveHourWindowState(p, &r)
			}

			if r.Err != "" || r.Usage.WindowStarted {
				updates <- usageUpdate{Index: i, Result: r, Final: true}
				return
			}

			r.Err = refreshingError("starting weekly quota window…")
			updates <- usageUpdate{Index: i, Result: r, Final: false}
			r.Err = ""

			primeSem <- struct{}{}
			err := primeWeeklyWindow(p, r.Account)
			<-primeSem
			if err != nil {
				r.PrimeErr = err.Error()
				updates <- usageUpdate{Index: i, Result: r, Final: true}
				return
			}

			usageSem <- struct{}{}
			fiveHour, weekly, err := fetchUsagePair(p, r.Account)
			<-usageSem
			if err != nil {
				r.PrimeErr = "window-start turn succeeded, but quota refresh failed: " + err.Error()
				updates <- usageUpdate{Index: i, Result: r, Final: true}
				return
			}

			weekly.WindowStarted = true
			if fiveHour != nil {
				fiveHour.WindowStarted = true
			}
			r.Usage = weekly
			r.FiveHour = fiveHour
			r.Primed = true

			account := r.Account
			if weekly.ResetsAt > 0 {
				if updated, rememberErr := rememberWeeklyReset(p, account, weekly.ResetsAt); rememberErr == nil {
					account = updated
				}
			}
			if fiveHour != nil && fiveHour.ResetsAt > 0 {
				if updated, rememberErr := rememberFiveHourReset(p, account, fiveHour.ResetsAt); rememberErr == nil {
					account = updated
				}
			}
			r.Account = account
			updates <- usageUpdate{Index: i, Result: r, Final: true}
		}(i, a)
	}

	go func() {
		wg.Wait()
		close(updates)
	}()
	return updates
}
