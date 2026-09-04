package cx

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	primeTimeout = 25 * time.Second
	// defaultCodexClientVersion is sent only to the Codex model catalog, which
	// rejects a non-semver client_version. It is used when no local Codex model
	// cache records the installed CLI version.
	defaultCodexClientVersion = "0.151.0"
	// defaultPrimeModel is the last resort when neither the local model cache
	// nor the catalog endpoint answers.
	defaultPrimeModel = "gpt-5.4-mini"
)

var (
	directResponsesEndpoint = "https://chatgpt.com/backend-api/codex/responses"
	directModelsEndpoint    = "https://chatgpt.com/backend-api/codex/models"
	primeHTTPClient         = &http.Client{}
)

// errQuotaExhausted marks a window start that the account cannot perform right
// now because it has no quota left. It is an expected state, not a failure.
var errQuotaExhausted = errors.New("usage limit reached")

type primeHTTPError struct {
	status int
	body   string
}

func (e *primeHTTPError) Error() string {
	if e.body == "" {
		return fmt.Sprintf("responses endpoint returned HTTP %d", e.status)
	}
	return fmt.Sprintf("responses endpoint returned HTTP %d: %s", e.status, e.body)
}

// modelRejected reports whether the backend refused the model slug itself, in
// which case a different slug is worth trying.
func (e *primeHTTPError) modelRejected() bool {
	return e.status == http.StatusBadRequest && strings.Contains(strings.ToLower(e.body), "model")
}

// primeWeeklyWindow intentionally performs one real Codex turn. It is used only
// when the server reports an unused rolling window. The turn goes straight to
// the Codex responses endpoint with the account's own credential: no codex
// process, no sandbox, no session or rollout files, and a request body small
// enough that the turn costs a handful of tokens.
func primeWeeklyWindow(p paths, a Account) error {
	token, accountID, err := directUsageCredentials(p, a)
	if err != nil {
		return fmt.Errorf("window start: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), primeTimeout)
	defer cancel()

	models, clientVersion := localCodexModels(p, a)
	model := chooseCodexModel(models)
	if model == "" {
		if fetched, ferr := fetchCodexModels(ctx, token, accountID, clientVersion); ferr == nil {
			model = chooseCodexModel(fetched)
		}
	}
	if model == "" {
		model = defaultPrimeModel
	}

	err = startQuotaWindow(ctx, token, accountID, model)
	var httpErr *primeHTTPError
	if errors.As(err, &httpErr) && httpErr.modelRejected() {
		// The cached catalog is stale. Ask the backend which models this
		// account may use and retry once.
		if fetched, ferr := fetchCodexModels(ctx, token, accountID, clientVersion); ferr == nil {
			if alt := chooseCodexModel(fetched); alt != "" && alt != model {
				err = startQuotaWindow(ctx, token, accountID, alt)
			}
		}
	}
	return err
}

// startQuotaWindow sends one minimal streaming turn and waits until the backend
// confirms it started, which is what puts the rolling window in motion.
func startQuotaWindow(ctx context.Context, token, accountID, model string) error {
	body, err := json.Marshal(map[string]any{
		"model":        model,
		"instructions": "Reply with OK.",
		"input": []any{map[string]any{
			"type":    "message",
			"role":    "user",
			"content": []any{map[string]any{"type": "input_text", "text": "ok"}},
		}},
		"tools":  []any{},
		"store":  false,
		"stream": true,
	})
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, directResponsesEndpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("ChatGPT-Account-Id", accountID)
	req.Header.Set("OpenAI-Beta", "responses=experimental")
	req.Header.Set("originator", "codex_cli_rs")
	req.Header.Set("session_id", newSessionID())
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("User-Agent", "codex-cli")

	resp, err := primeHTTPClient.Do(req)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return errors.New("window start timed out")
		}
		return fmt.Errorf("window start request: %w", err)
	}
	defer resp.Body.Close()

	if !httpSuccess(resp.StatusCode) {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
		msg := errorMessageFromBody(raw)
		if quotaExhausted(resp.StatusCode, "", msg) {
			return errQuotaExhausted
		}
		return &primeHTTPError{status: resp.StatusCode, body: msg}
	}
	return readPrimeStream(ctx, resp.Body)
}

type primeAPIError struct {
	Type    string `json:"type"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type primeEvent struct {
	Type     string         `json:"type"`
	Error    *primeAPIError `json:"error"`
	Response *struct {
		Status string         `json:"status"`
		Error  *primeAPIError `json:"error"`
	} `json:"response"`
}

// readPrimeStream consumes the SSE stream until the turn ends. Once the backend
// has emitted response.created the window is running, so a truncated stream
// after that point still counts as a successful start.
func readPrimeStream(ctx context.Context, body io.Reader) error {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 8192), 1<<20)
	started := false
	for scanner.Scan() {
		data, ok := strings.CutPrefix(scanner.Text(), "data:")
		if !ok {
			continue
		}
		data = strings.TrimSpace(data)
		if data == "" || data == "[DONE]" {
			continue
		}
		var ev primeEvent
		if json.Unmarshal([]byte(data), &ev) != nil {
			continue
		}
		switch ev.Type {
		case "response.created", "response.in_progress":
			started = true
		case "response.completed", "response.incomplete":
			return nil
		case "response.failed", "error":
			apiErr := ev.Error
			if apiErr == nil && ev.Response != nil {
				apiErr = ev.Response.Error
			}
			if apiErr == nil {
				if started {
					return nil
				}
				return errors.New("window start turn failed")
			}
			if quotaExhausted(0, apiErr.Code, apiErr.Message) || quotaExhausted(0, apiErr.Type, "") {
				return errQuotaExhausted
			}
			if started {
				return nil
			}
			return fmt.Errorf("window start turn failed: %s", singleLine(apiErr.Message))
		}
	}
	if err := scanner.Err(); err != nil && !started {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return errors.New("window start timed out")
		}
		return fmt.Errorf("read window start stream: %w", err)
	}
	if !started {
		return errors.New("window start turn produced no response")
	}
	return nil
}

// quotaExhausted reports whether a backend refusal means the account has no
// quota left, rather than something the user should see as a failure.
func quotaExhausted(status int, code, message string) bool {
	if status == http.StatusTooManyRequests {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(code)) {
	case "usage_limit_reached", "usage_not_included", "rate_limit_exceeded":
		return true
	}
	lower := strings.ToLower(message)
	return strings.Contains(lower, "usage limit") || strings.Contains(lower, "rate limit")
}

// errorMessageFromBody pulls the human-readable part out of the several error
// envelopes the backend uses, falling back to the raw body.
func errorMessageFromBody(raw []byte) string {
	var envelope struct {
		Detail any            `json:"detail"`
		Error  *primeAPIError `json:"error"`
	}
	if json.Unmarshal(raw, &envelope) == nil {
		if envelope.Error != nil && envelope.Error.Message != "" {
			return singleLine(envelope.Error.Message)
		}
		if s, ok := envelope.Detail.(string); ok && s != "" {
			return singleLine(s)
		}
	}
	return singleLine(string(raw))
}

// singleLine collapses a message to one bounded line. Status and dashboard
// output allot exactly one row per note, so an embedded newline would break the
// layout as well as flood the screen.
func singleLine(s string) string {
	out := strings.Join(strings.Fields(s), " ")
	const limit = 160
	runes := []rune(out)
	if len(runes) <= limit {
		return out
	}
	return string(runes[:limit-1]) + "…"
}

type codexModel struct {
	Slug           string `json:"slug"`
	Visibility     string `json:"visibility"`
	Priority       int    `json:"priority"`
	SupportedInAPI bool   `json:"supported_in_api"`
}

type codexModelsPayload struct {
	ClientVersion string       `json:"client_version"`
	Models        []codexModel `json:"models"`
}

// chooseCodexModel picks the cheapest model the account can actually use: a
// mini variant when the catalog offers one, otherwise the least prominent
// listed model, which the backend orders by ascending priority.
func chooseCodexModel(models []codexModel) string {
	var fallback codexModel
	for _, m := range models {
		if m.Slug == "" || !m.SupportedInAPI || !strings.EqualFold(m.Visibility, "list") {
			continue
		}
		if strings.Contains(strings.ToLower(m.Slug), "mini") {
			return m.Slug
		}
		if fallback.Slug == "" || m.Priority > fallback.Priority {
			fallback = m
		}
	}
	return fallback.Slug
}

// localCodexModels reads the model catalog the Codex CLI already cached, so the
// common path needs no extra request. It returns the recorded client version
// too, because the catalog endpoint requires one.
func localCodexModels(p paths, a Account) ([]codexModel, string) {
	candidates := []string{
		filepath.Join(p.accountDir(a.ID), "models_cache.json"),
		filepath.Join(p.CodexHome, "models_cache.json"),
	}
	for _, path := range candidates {
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var payload codexModelsPayload
		if json.Unmarshal(raw, &payload) != nil {
			continue
		}
		version := strings.TrimSpace(payload.ClientVersion)
		if version == "" {
			version = defaultCodexClientVersion
		}
		if len(payload.Models) > 0 {
			return payload.Models, version
		}
	}
	return nil, defaultCodexClientVersion
}

func fetchCodexModels(ctx context.Context, token, accountID, clientVersion string) ([]codexModel, error) {
	if strings.TrimSpace(clientVersion) == "" {
		clientVersion = defaultCodexClientVersion
	}
	endpoint := directModelsEndpoint + "?client_version=" + url.QueryEscape(clientVersion)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("ChatGPT-Account-Id", accountID)
	req.Header.Set("originator", "codex_cli_rs")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "codex-cli")

	resp, err := primeHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("model catalog request: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if !httpSuccess(resp.StatusCode) {
		return nil, fmt.Errorf("model catalog returned HTTP %d", resp.StatusCode)
	}
	var payload codexModelsPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("decode model catalog: %w", err)
	}
	return payload.Models, nil
}

// newSessionID returns the per-turn identifier the responses endpoint expects.
// A random UUID is enough: cx never resumes a primed turn.
func newSessionID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%032x", time.Now().UnixNano())
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
