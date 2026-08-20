package main

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

type UsageResult struct {
	Account Account
	Usage   WeeklyUsage
	Err     string
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
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for i, a := range accounts {
		wg.Add(1)
		go func(i int, a Account) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			u, err := fetchUsage(p, a)
			out[i] = UsageResult{Account: a, Usage: u}
			if err != nil {
				out[i].Err = err.Error()
			}
		}(i, a)
	}
	wg.Wait()
	return out
}

func fetchUsage(p paths, a Account) (WeeklyUsage, error) {
	if _, err := exec.LookPath("codex"); err != nil {
		return WeeklyUsage{}, errors.New("codex executable not found")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "codex", "app-server")
	cmd.Env = append(os.Environ(), "CODEX_HOME="+p.accountDir(a.ID))
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return WeeklyUsage{}, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return WeeklyUsage{}, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return WeeklyUsage{}, err
	}
	defer func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() }()

	enc := json.NewEncoder(stdin)
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 1024), 4*1024*1024)
	if err := enc.Encode(map[string]any{"id": 1, "method": "initialize", "params": map[string]any{"clientInfo": map[string]any{"name": "cx", "title": "cx", "version": version}}}); err != nil {
		return WeeklyUsage{}, err
	}
	if _, err := readResponse(scanner, 1); err != nil {
		return WeeklyUsage{}, fmt.Errorf("app-server initialize: %w", err)
	}
	if err := enc.Encode(map[string]any{"method": "initialized", "params": map[string]any{}}); err != nil {
		return WeeklyUsage{}, err
	}
	if err := enc.Encode(map[string]any{"id": 2, "method": "account/rateLimits/read", "params": map[string]any{}}); err != nil {
		return WeeklyUsage{}, err
	}
	env, err := readResponse(scanner, 2)
	if err != nil {
		msg := stderr.String()
		if len(msg) > 240 {
			msg = msg[len(msg)-240:]
		}
		if msg != "" {
			return WeeklyUsage{}, fmt.Errorf("rate limit read: %w (%s)", err, msg)
		}
		return WeeklyUsage{}, fmt.Errorf("rate limit read: %w", err)
	}
	var rr rateResponse
	if err := json.Unmarshal(env.Result, &rr); err != nil {
		return WeeklyUsage{}, fmt.Errorf("decode rate limits: %w", err)
	}
	w, err := selectWeeklyWindow(rr)
	if err != nil {
		return WeeklyUsage{}, err
	}
	mins := int64(0)
	if w.WindowDurationMins != nil {
		mins = *w.WindowDurationMins
	}
	reset := int64(0)
	if w.ResetsAt != nil {
		reset = *w.ResetsAt
	}
	return WeeklyUsage{UsedPercent: w.UsedPercent, WindowMinutes: mins, ResetsAt: reset, FetchedAt: time.Now(), Fresh: true}, nil
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
