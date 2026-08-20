package cx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const directUsageTimeout = 4 * time.Second

var (
	directUsageEndpoint   = "https://chatgpt.com/backend-api/wham/usage"
	directUsageHTTPClient = &http.Client{}
)

type directUsagePayload struct {
	RateLimit            *directRateLimit            `json:"rate_limit"`
	AdditionalRateLimits []directAdditionalRateLimit `json:"additional_rate_limits"`
}

type directAdditionalRateLimit struct {
	RateLimit *directRateLimit `json:"rate_limit"`
}

type directRateLimit struct {
	PrimaryWindow   *directRateWindow `json:"primary_window"`
	SecondaryWindow *directRateWindow `json:"secondary_window"`
}

type directRateWindow struct {
	UsedPercent        float64 `json:"used_percent"`
	LimitWindowSeconds int64   `json:"limit_window_seconds"`
	ResetAt            int64   `json:"reset_at"`
}

func fetchUsageDirect(p paths, a Account) (WeeklyUsage, error) {
	token, accountID, err := directUsageCredentials(p, a)
	if err != nil {
		return WeeklyUsage{}, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), directUsageTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, directUsageEndpoint, nil)
	if err != nil {
		return WeeklyUsage{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("ChatGPT-Account-Id", accountID)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("User-Agent", "codex-cli")

	resp, err := directUsageHTTPClient.Do(req)
	if err != nil {
		return WeeklyUsage{}, fmt.Errorf("usage request: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return WeeklyUsage{}, fmt.Errorf("read usage response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(body))
		if len(msg) > 240 {
			msg = msg[:240]
		}
		if msg != "" {
			return WeeklyUsage{}, fmt.Errorf("usage endpoint returned HTTP %d: %s", resp.StatusCode, msg)
		}
		return WeeklyUsage{}, fmt.Errorf("usage endpoint returned HTTP %d", resp.StatusCode)
	}

	var payload directUsagePayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return WeeklyUsage{}, fmt.Errorf("decode usage response: %w", err)
	}
	window, err := selectDirectWeeklyWindow(payload)
	if err != nil {
		return WeeklyUsage{}, err
	}
	mins := int64(0)
	if window.LimitWindowSeconds > 0 {
		mins = (window.LimitWindowSeconds + 59) / 60
	}
	return WeeklyUsage{
		UsedPercent:   window.UsedPercent,
		WindowMinutes: mins,
		ResetsAt:      window.ResetAt,
		FetchedAt:     time.Now(),
		Fresh:         true,
	}, nil
}

func directUsageCredentials(p paths, a Account) (string, string, error) {
	var auth authShape
	if err := readJSON(p.accountAuth(a.ID), &auth); err != nil {
		return "", "", fmt.Errorf("read auth: %w", err)
	}
	if auth.Tokens == nil || strings.TrimSpace(auth.Tokens.AccessToken) == "" {
		return "", "", errors.New("auth.json does not contain a ChatGPT access token")
	}
	accountID := strings.TrimSpace(auth.Tokens.AccountID)
	if accountID == "" {
		accountID = strings.TrimSpace(a.AccountID)
	}
	if accountID == "" {
		identity, err := parseAuth(p.accountAuth(a.ID))
		if err != nil {
			return "", "", fmt.Errorf("determine account id: %w", err)
		}
		accountID = identity.AccountID
	}
	if accountID == "" {
		return "", "", errors.New("could not determine ChatGPT account_id")
	}
	return strings.TrimSpace(auth.Tokens.AccessToken), accountID, nil
}

func selectDirectWeeklyWindow(payload directUsagePayload) (*directRateWindow, error) {
	var windows []*directRateWindow
	add := func(limit *directRateLimit) {
		if limit == nil {
			return
		}
		if limit.PrimaryWindow != nil {
			windows = append(windows, limit.PrimaryWindow)
		}
		if limit.SecondaryWindow != nil {
			windows = append(windows, limit.SecondaryWindow)
		}
	}
	add(payload.RateLimit)
	for i := range payload.AdditionalRateLimits {
		add(payload.AdditionalRateLimits[i].RateLimit)
	}
	if len(windows) == 0 {
		return nil, errors.New("usage endpoint returned no rate-limit window")
	}
	best := windows[0]
	for _, w := range windows[1:] {
		if w.LimitWindowSeconds > best.LimitWindowSeconds {
			best = w
		}
	}
	return best, nil
}
