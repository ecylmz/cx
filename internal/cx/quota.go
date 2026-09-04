package cx

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"time"
)

const (
	usageConcurrency = 8
	primeConcurrency = 4
	usageTimeout     = 6 * time.Second
)

type UsageResult struct {
	Account      Account
	Usage        WeeklyUsage
	FiveHour     *WeeklyUsage
	Err          string
	PrimeErr     string
	PrimeSkipped string
	Primed       bool
	BankedResets []BankedReset
	BankedErr    string
	BankedLoaded bool
}

type rpcEnvelope struct {
	ID     any             `json:"id,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type rateWindow struct {
	UsedPercent        float64 `json:"usedPercent"`
	WindowDurationMins *int64  `json:"windowDurationMins"`
	ResetsAt           *int64  `json:"resetsAt"`
}
type rateSnapshot struct {
	Primary   *rateWindow `json:"primary"`
	Secondary *rateWindow `json:"secondary"`
}
type rateResponse struct {
	RateLimits          rateSnapshot            `json:"rateLimits"`
	RateLimitsByLimitID map[string]rateSnapshot `json:"rateLimitsByLimitId"`
}

func fetchAllUsage(p paths, accounts []Account) []UsageResult {
	out := make([]UsageResult, len(accounts))
	sem := make(chan struct{}, usageConcurrency)
	var wg sync.WaitGroup
	for i, a := range accounts {
		wg.Add(1)
		go func(i int, a Account) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			var fiveHour *WeeklyUsage
			var weekly WeeklyUsage
			var usageErr error
			var banked []BankedReset
			var bankedErr error
			var reads sync.WaitGroup
			reads.Add(2)
			go func() {
				defer reads.Done()
				fiveHour, weekly, usageErr = fetchUsagePair(p, a)
			}()
			go func() {
				defer reads.Done()
				banked, bankedErr = fetchBankedResetsDirect(p, a)
			}()
			reads.Wait()

			if bankedErr != nil && usageErr == nil {
				if retry, err := fetchBankedResetsDirect(p, a); err == nil {
					banked = retry
					bankedErr = nil
				} else {
					bankedErr = err
				}
			}

			r := UsageResult{
				Account:      a,
				Usage:        weekly,
				FiveHour:     fiveHour,
				BankedResets: banked,
				BankedLoaded: true,
			}
			if usageErr != nil {
				r.Err = usageErr.Error()
			}
			if bankedErr != nil {
				r.BankedErr = bankedErr.Error()
			}
			out[i] = r
		}(i, a)
	}
	wg.Wait()
	return out
}

func fetchAllUsageWithPriming(p paths, accounts []Account) []UsageResult {
	results := fetchAllUsage(p, accounts)
	for i := range results {
		if results[i].Err == "" {
			annotateWindowState(p, &results[i])
			annotateFiveHourWindowState(p, &results[i])
		}
	}

	sem := make(chan struct{}, primeConcurrency)
	var wg sync.WaitGroup
	for i := range results {
		if results[i].Err != "" || results[i].Usage.WindowStarted {
			continue
		}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if err := primeWeeklyWindow(p, results[i].Account); err != nil {
				results[i].PrimeErr, results[i].PrimeSkipped = classifyPrimeFailure(err)
				return
			}
			fiveHour, weekly, err := fetchUsagePair(p, results[i].Account)
			if err != nil {
				results[i].PrimeErr = singleLine("window-start turn succeeded, but quota refresh failed: " + err.Error())
				return
			}
			weekly.WindowStarted = true
			if fiveHour != nil {
				fiveHour.WindowStarted = true
			}
			results[i].Usage = weekly
			results[i].FiveHour = fiveHour
			results[i].Primed = true

			account := results[i].Account
			if weekly.ResetsAt > 0 {
				if updated, err := rememberWeeklyReset(p, account, weekly.ResetsAt); err == nil {
					account = updated
				}
			}
			if fiveHour != nil && fiveHour.ResetsAt > 0 {
				if updated, err := rememberFiveHourReset(p, account, fiveHour.ResetsAt); err == nil {
					account = updated
				}
			}
			results[i].Account = account
		}(i)
	}
	wg.Wait()
	return results
}

// classifyPrimeFailure splits a window-start failure into a real error and the
// expected "there is no quota left to spend" case. An exhausted account cannot
// start a window until it resets, so reporting that in red every refresh is
// noise, not information.
func classifyPrimeFailure(err error) (primeErr, skipped string) {
	if err == nil {
		return "", ""
	}
	if errors.Is(err, errQuotaExhausted) {
		return "", "usage limit reached · starts after reset"
	}
	return singleLine(err.Error()), ""
}

func annotateFiveHourWindowState(p paths, r *UsageResult) {
	u := r.FiveHour
	if u == nil {
		return
	}
	now := time.Now()
	if u.UsedPercent > 0 {
		u.WindowStarted = true
		if u.ResetsAt > 0 {
			if a, err := rememberFiveHourReset(p, r.Account, u.ResetsAt); err == nil {
				r.Account = a
			}
		}
		return
	}
	if u.WindowMinutes <= 0 {
		u.WindowStarted = true
		return
	}
	if resetMatchesKnown(r.Account.FiveHourResetAt, u.ResetsAt, now) {
		u.WindowStarted = true
		return
	}
	if fiveHourMatchesWeeklyStart(r.Account, r.Usage, *u, now) {
		u.WindowStarted = true
		if u.ResetsAt > 0 {
			if a, err := rememberFiveHourReset(p, r.Account, u.ResetsAt); err == nil {
				r.Account = a
			}
		}
		return
	}
	u.WindowStarted = !looksLikeUnstartedWindow(*u, now)
	if u.WindowStarted && u.ResetsAt > 0 {
		if a, err := rememberFiveHourReset(p, r.Account, u.ResetsAt); err == nil {
			r.Account = a
		}
	}
}

func fiveHourMatchesWeeklyStart(a Account, weekly WeeklyUsage, fiveHour WeeklyUsage, now time.Time) bool {
	weeklyReset := a.WeeklyResetAt
	if weeklyReset <= now.Unix() || fiveHour.ResetsAt <= 0 || fiveHour.WindowMinutes <= 0 {
		return false
	}
	weeklyMinutes := weekly.WindowMinutes
	if weeklyMinutes <= 0 {
		weeklyMinutes = 7 * 24 * 60
	}
	weeklyStart := time.Unix(weeklyReset, 0).Add(-time.Duration(weeklyMinutes) * time.Minute)
	fiveHourStart := time.Unix(fiveHour.ResetsAt, 0).Add(-time.Duration(fiveHour.WindowMinutes) * time.Minute)
	delta := weeklyStart.Sub(fiveHourStart)
	if delta < 0 {
		delta = -delta
	}
	return delta <= 2*time.Minute
}

func annotateWindowState(p paths, r *UsageResult) {
	now := time.Now()
	u := &r.Usage
	if u.UsedPercent > 0 {
		u.WindowStarted = true
		if u.ResetsAt > 0 {
			if a, err := rememberWeeklyReset(p, r.Account, u.ResetsAt); err == nil {
				r.Account = a
			}
		}
		return
	}
	if u.WindowMinutes <= 0 {
		u.WindowStarted = true
		return
	}
	if resetMatchesKnown(r.Account.WeeklyResetAt, u.ResetsAt, now) {
		u.WindowStarted = true
		return
	}
	u.WindowStarted = !looksLikeUnstartedWindow(*u, now)
	if u.WindowStarted && u.ResetsAt > 0 {
		if a, err := rememberWeeklyReset(p, r.Account, u.ResetsAt); err == nil {
			r.Account = a
		}
	}
}

func resetMatchesKnown(knownReset, currentReset int64, now time.Time) bool {
	if knownReset <= now.Unix() || currentReset <= 0 {
		return false
	}
	delta := knownReset - currentReset
	if delta < 0 {
		delta = -delta
	}
	return delta <= 120
}

func rememberWeeklyReset(p paths, a Account, resetAt int64) (Account, error) {
	if resetAt <= 0 || a.WeeklyResetAt == resetAt {
		return a, nil
	}
	a.WeeklyResetAt = resetAt
	a.UpdatedAt = time.Now()
	if err := writeJSON(p.accountMeta(a.ID), a); err != nil {
		return a, err
	}
	return a, nil
}

func rememberFiveHourReset(p paths, a Account, resetAt int64) (Account, error) {
	if resetAt <= 0 || a.FiveHourResetAt == resetAt {
		return a, nil
	}
	a.FiveHourResetAt = resetAt
	a.UpdatedAt = time.Now()
	if err := writeJSON(p.accountMeta(a.ID), a); err != nil {
		return a, err
	}
	return a, nil
}

func fetchUsage(p paths, a Account) (WeeklyUsage, error) {
	_, weekly, err := fetchUsagePair(p, a)
	return weekly, err
}

func fetchUsagePair(p paths, a Account) (*WeeklyUsage, WeeklyUsage, error) {
	fiveHour, weekly, directErr := fetchUsagePairDirect(p, a)
	if directErr == nil {
		return fiveHour, weekly, nil
	}
	fiveHour, weekly, appErr := fetchUsagePairViaAppServer(p, a)
	if appErr == nil {
		return fiveHour, weekly, nil
	}
	return nil, WeeklyUsage{}, fmt.Errorf("direct usage read: %v; app-server fallback: %w", directErr, appErr)
}

func fetchUsageViaAppServer(p paths, a Account) (WeeklyUsage, error) {
	_, weekly, err := fetchUsagePairViaAppServer(p, a)
	return weekly, err
}

func fetchUsagePairViaAppServer(p paths, a Account) (*WeeklyUsage, WeeklyUsage, error) {
	if _, err := exec.LookPath("codex"); err != nil {
		return nil, WeeklyUsage{}, errors.New("codex executable not found")
	}
	ctx, cancel := context.WithTimeout(context.Background(), usageTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "codex", "app-server")
	cmd.Env = append(os.Environ(), "CODEX_HOME="+p.accountDir(a.ID))
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, WeeklyUsage{}, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, WeeklyUsage{}, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, WeeklyUsage{}, err
	}
	defer func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() }()

	enc := json.NewEncoder(stdin)
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 1024), 4*1024*1024)
	if err := enc.Encode(map[string]any{"id": 1, "method": "initialize", "params": map[string]any{"clientInfo": map[string]any{"name": "cx", "title": "cx", "version": Version}}}); err != nil {
		return nil, WeeklyUsage{}, err
	}
	if _, err := readResponse(scanner, 1); err != nil {
		return nil, WeeklyUsage{}, fmt.Errorf("app-server initialize: %w", err)
	}
	if err := enc.Encode(map[string]any{"method": "initialized", "params": map[string]any{}}); err != nil {
		return nil, WeeklyUsage{}, err
	}
	if err := enc.Encode(map[string]any{"id": 2, "method": "account/rateLimits/read", "params": map[string]any{}}); err != nil {
		return nil, WeeklyUsage{}, err
	}
	env, err := readResponse(scanner, 2)
	if err != nil {
		msg := stderr.String()
		if len(msg) > 240 {
			msg = msg[len(msg)-240:]
		}
		if msg != "" {
			return nil, WeeklyUsage{}, fmt.Errorf("rate limit read: %w (%s)", err, msg)
		}
		return nil, WeeklyUsage{}, fmt.Errorf("rate limit read: %w", err)
	}
	var rr rateResponse
	if err := json.Unmarshal(env.Result, &rr); err != nil {
		return nil, WeeklyUsage{}, fmt.Errorf("decode rate limits: %w", err)
	}

	fiveHourWindow, weeklyWindow, err := selectCodexRateWindows(rr)
	if err != nil {
		return nil, WeeklyUsage{}, err
	}
	fetchedAt := time.Now()
	weekly := appWindowUsage(weeklyWindow, fetchedAt)
	var fiveHour *WeeklyUsage
	if fiveHourWindow != nil {
		u := appWindowUsage(fiveHourWindow, fetchedAt)
		fiveHour = &u
	}
	return fiveHour, weekly, nil
}

func appWindowUsage(w *rateWindow, fetchedAt time.Time) WeeklyUsage {
	if w == nil {
		return WeeklyUsage{}
	}
	mins := int64(0)
	if w.WindowDurationMins != nil {
		mins = *w.WindowDurationMins
	}
	reset := int64(0)
	if w.ResetsAt != nil {
		reset = *w.ResetsAt
	}
	return WeeklyUsage{
		UsedPercent:   w.UsedPercent,
		WindowMinutes: mins,
		ResetsAt:      reset,
		FetchedAt:     fetchedAt,
		Fresh:         true,
	}
}

func selectCodexRateWindows(rr rateResponse) (*rateWindow, *rateWindow, error) {
	snapshot := rr.RateLimits
	if c, ok := rr.RateLimitsByLimitID["codex"]; ok {
		snapshot = c
	}
	windows := make([]*rateWindow, 0, 2)
	if snapshot.Primary != nil {
		windows = append(windows, snapshot.Primary)
	}
	if snapshot.Secondary != nil {
		windows = append(windows, snapshot.Secondary)
	}
	if len(windows) == 0 {
		return nil, nil, errors.New("server returned no Codex rate-limit window")
	}

	var fiveHour, weekly *rateWindow
	for _, w := range windows {
		if w.WindowDurationMins == nil {
			continue
		}
		switch *w.WindowDurationMins {
		case 5 * 60:
			fiveHour = w
		case 7 * 24 * 60:
			weekly = w
		}
	}

	if weekly == nil {
		candidate := windows[0]
		for _, w := range windows[1:] {
			if windowMinutes(w) > windowMinutes(candidate) {
				candidate = w
			}
		}
		if windowMinutes(candidate) >= 24*60 {
			weekly = candidate
		}
	}
	if fiveHour == nil && len(windows) > 1 {
		candidate := windows[0]
		for _, w := range windows[1:] {
			if windowMinutes(w) < windowMinutes(candidate) {
				candidate = w
			}
		}
		if candidate != weekly {
			fiveHour = candidate
		}
	} else if fiveHour == nil && len(windows) == 1 && windowMinutes(windows[0]) < 24*60 {
		fiveHour = windows[0]
	}
	return fiveHour, weekly, nil
}

func windowMinutes(w *rateWindow) int64 {
	if w == nil || w.WindowDurationMins == nil {
		return 0
	}
	return *w.WindowDurationMins
}

func readResponse(scanner *bufio.Scanner, id int) (rpcEnvelope, error) {
	for scanner.Scan() {
		var e rpcEnvelope
		if json.Unmarshal(scanner.Bytes(), &e) != nil {
			continue
		}
		if !sameID(e.ID, id) {
			continue
		}
		if e.Error != nil {
			return e, fmt.Errorf("%d: %s", e.Error.Code, e.Error.Message)
		}
		return e, nil
	}
	if err := scanner.Err(); err != nil {
		return rpcEnvelope{}, err
	}
	return rpcEnvelope{}, io.EOF
}

func sameID(v any, want int) bool {
	switch x := v.(type) {
	case float64:
		return int(x) == want
	case json.Number:
		n, _ := x.Int64()
		return int(n) == want
	case string:
		n, _ := strconv.Atoi(x)
		return n == want
	}
	return false
}

func selectWeeklyWindow(rr rateResponse) (*rateWindow, error) {
	var ws []*rateWindow
	add := func(s rateSnapshot) {
		if s.Primary != nil {
			ws = append(ws, s.Primary)
		}
		if s.Secondary != nil {
			ws = append(ws, s.Secondary)
		}
	}
	add(rr.RateLimits)
	if c, ok := rr.RateLimitsByLimitID["codex"]; ok {
		add(c)
	}
	if len(ws) == 0 {
		return nil, errors.New("server returned no rate-limit window")
	}
	best := ws[0]
	score := func(w *rateWindow) int64 {
		if w.WindowDurationMins == nil {
			return 0
		}
		return *w.WindowDurationMins
	}
	for _, w := range ws[1:] {
		if score(w) > score(best) {
			best = w
		}
	}
	return best, nil
}
