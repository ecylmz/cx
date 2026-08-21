package cx

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

var directResetCreditsEndpoint = "https://chatgpt.com/backend-api/wham/rate-limit-reset-credits"

type BankedReset struct {
	ID          string `json:"id"`
	ResetType   string `json:"reset_type,omitempty"`
	Status      string `json:"status"`
	GrantedAt   string `json:"granted_at,omitempty"`
	ExpiresAt   string `json:"expires_at,omitempty"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
}

type directResetCreditsPayload struct {
	Credits        []directResetCredit `json:"credits"`
	AvailableCount int                 `json:"available_count"`
}

type directResetCredit struct {
	ID          string  `json:"id"`
	ResetType   string  `json:"reset_type"`
	Status      string  `json:"status"`
	GrantedAt   string  `json:"granted_at"`
	ExpiresAt   *string `json:"expires_at"`
	Title       *string `json:"title"`
	Description *string `json:"description"`
}

func fetchBankedResetsDirect(p paths, a Account) ([]BankedReset, error) {
	token, accountID, err := directUsageCredentials(p, a)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), directUsageTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, directResetCreditsEndpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("ChatGPT-Account-Id", accountID)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("User-Agent", "codex-cli")

	resp, err := directUsageHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("banked reset request: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read banked reset response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(body))
		if len(msg) > 240 {
			msg = msg[:240]
		}
		if msg != "" {
			return nil, fmt.Errorf("banked reset endpoint returned HTTP %d: %s", resp.StatusCode, msg)
		}
		return nil, fmt.Errorf("banked reset endpoint returned HTTP %d", resp.StatusCode)
	}

	var payload directResetCreditsPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode banked reset response: %w", err)
	}
	out := make([]BankedReset, 0, len(payload.Credits))
	for _, c := range payload.Credits {
		if !strings.EqualFold(strings.TrimSpace(c.Status), "available") {
			continue
		}
		out = append(out, BankedReset{
			ID:          c.ID,
			ResetType:   c.ResetType,
			Status:      c.Status,
			GrantedAt:   c.GrantedAt,
			ExpiresAt:   stringValue(c.ExpiresAt),
			Title:       stringValue(c.Title),
			Description: stringValue(c.Description),
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		ti, iok := bankedExpiry(out[i].ExpiresAt)
		tj, jok := bankedExpiry(out[j].ExpiresAt)
		switch {
		case iok && jok:
			return ti.Before(tj)
		case iok:
			return true
		case jok:
			return false
		default:
			return out[i].ID < out[j].ID
		}
	})
	return out, nil
}

func stringValue(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

func bankedExpiry(raw string) (time.Time, bool) {
	if strings.TrimSpace(raw) == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}
