package cx

import (
	"cmp"
	"encoding/json"
	"fmt"
	"slices"
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
	body, err := backendGet(p, a, directResetCreditsEndpoint, "banked reset")
	if err != nil {
		return nil, err
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
	slices.SortStableFunc(out, func(x, y BankedReset) int {
		tx, xok := bankedExpiry(x.ExpiresAt)
		ty, yok := bankedExpiry(y.ExpiresAt)
		switch {
		case xok && yok:
			return tx.Compare(ty)
		case xok:
			return -1
		case yok:
			return 1
		default:
			return cmp.Compare(x.ID, y.ID)
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
