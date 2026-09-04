package cx

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// sseResponse writes a minimal Codex responses stream.
func sseResponse(w http.ResponseWriter, events ...string) {
	w.Header().Set("Content-Type", "text/event-stream")
	for _, e := range events {
		_, _ = w.Write([]byte("event: x\ndata: " + e + "\n\n"))
	}
}

func usePrimeServer(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(h)
	t.Cleanup(server.Close)
	oldResponses, oldModels, oldClient := directResponsesEndpoint, directModelsEndpoint, primeHTTPClient
	directResponsesEndpoint = server.URL + "/responses"
	directModelsEndpoint = server.URL + "/models"
	primeHTTPClient = server.Client()
	t.Cleanup(func() {
		directResponsesEndpoint, directModelsEndpoint, primeHTTPClient = oldResponses, oldModels, oldClient
	})
	return server
}

func TestPrimeWeeklyWindowUsesCodexResponsesEndpoint(t *testing.T) {
	p := makeTestPaths(t)
	a := Account{ID: "a", Name: "primary", AccountID: "acct"}
	writeTestAccount(t, p, a)
	// A cached catalog keeps the common path down to a single request.
	writeModelsCache(t, filepath.Join(p.accountDir(a.ID), "models_cache.json"), "0.151.0",
		`{"slug":"gpt-5.4","visibility":"list","priority":16,"supported_in_api":true}`,
		`{"slug":"gpt-5.4-mini","visibility":"list","priority":23,"supported_in_api":true}`)

	var body map[string]any
	var gotPath string
	requests := 0
	usePrimeServer(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		gotPath = r.URL.Path
		if got := r.Header.Get("Authorization"); got != "Bearer access" {
			t.Errorf("Authorization=%q", got)
		}
		if got := r.Header.Get("ChatGPT-Account-Id"); got != "acct" {
			t.Errorf("ChatGPT-Account-Id=%q", got)
		}
		if got := r.Header.Get("OpenAI-Beta"); got != "responses=experimental" {
			t.Errorf("OpenAI-Beta=%q", got)
		}
		if got := r.Header.Get("originator"); got != "codex_cli_rs" {
			t.Errorf("originator=%q", got)
		}
		if r.Header.Get("session_id") == "" {
			t.Error("session_id header is empty")
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		sseResponse(w, `{"type":"response.created"}`, `{"type":"response.completed"}`)
	})

	if err := primeWeeklyWindow(p, a); err != nil {
		t.Fatal(err)
	}
	if requests != 1 {
		t.Fatalf("requests=%d; a cached catalog should need no model lookup", requests)
	}
	if gotPath != "/responses" {
		t.Fatalf("path=%q", gotPath)
	}
	if body["model"] != "gpt-5.4-mini" {
		t.Fatalf("model=%v; want the cheapest listed model", body["model"])
	}
	if body["stream"] != true || body["store"] != false {
		t.Fatalf("body=%v; the turn must stream and must not be stored", body)
	}
}

func TestPrimeWeeklyWindowNeedsNoCodexExecutable(t *testing.T) {
	p := makeTestPaths(t)
	a := Account{ID: "a", Name: "primary", AccountID: "acct"}
	writeTestAccount(t, p, a)
	writeModelsCache(t, filepath.Join(p.CodexHome, "models_cache.json"), "0.151.0",
		`{"slug":"gpt-5.4-mini","visibility":"list","priority":23,"supported_in_api":true}`)
	usePrimeServer(t, func(w http.ResponseWriter, r *http.Request) {
		sseResponse(w, `{"type":"response.created"}`, `{"type":"response.completed"}`)
	})
	t.Setenv("PATH", t.TempDir())

	if err := primeWeeklyWindow(p, a); err != nil {
		t.Fatal(err)
	}
}

func TestPrimeWeeklyWindowFetchesModelCatalogWhenNoCacheExists(t *testing.T) {
	p := makeTestPaths(t)
	a := Account{ID: "a", Name: "primary", AccountID: "acct"}
	writeTestAccount(t, p, a)

	var clientVersion string
	var model any
	usePrimeServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/models":
			clientVersion = r.URL.Query().Get("client_version")
			_, _ = w.Write([]byte(`{"models":[{"slug":"gpt-9-mini","visibility":"list","priority":20,"supported_in_api":true}]}`))
		case "/responses":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			model = body["model"]
			sseResponse(w, `{"type":"response.created"}`, `{"type":"response.completed"}`)
		}
	})

	if err := primeWeeklyWindow(p, a); err != nil {
		t.Fatal(err)
	}
	if clientVersion != defaultCodexClientVersion {
		t.Fatalf("client_version=%q", clientVersion)
	}
	if model != "gpt-9-mini" {
		t.Fatalf("model=%v", model)
	}
}

func TestPrimeWeeklyWindowRetriesWhenCachedModelIsGone(t *testing.T) {
	p := makeTestPaths(t)
	a := Account{ID: "a", Name: "primary", AccountID: "acct"}
	writeTestAccount(t, p, a)
	writeModelsCache(t, filepath.Join(p.accountDir(a.ID), "models_cache.json"), "0.140.0",
		`{"slug":"gpt-retired-mini","visibility":"list","priority":23,"supported_in_api":true}`)

	var models []any
	usePrimeServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/models" {
			_, _ = w.Write([]byte(`{"models":[{"slug":"gpt-current-mini","visibility":"list","priority":23,"supported_in_api":true}]}`))
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		models = append(models, body["model"])
		if body["model"] == "gpt-retired-mini" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"detail":"The 'gpt-retired-mini' model is not supported when using Codex with a ChatGPT account."}`))
			return
		}
		sseResponse(w, `{"type":"response.created"}`, `{"type":"response.completed"}`)
	})

	if err := primeWeeklyWindow(p, a); err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 || models[0] != "gpt-retired-mini" || models[1] != "gpt-current-mini" {
		t.Fatalf("models=%v; want a retry with the freshly fetched slug", models)
	}
}

func TestPrimeWeeklyWindowReportsExhaustedQuotaAsSkipped(t *testing.T) {
	cases := []struct {
		name    string
		handler http.HandlerFunc
	}{
		{"http 429", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"detail":"You've hit your usage limit. Upgrade to Pro."}`))
		}},
		{"stream failure", func(w http.ResponseWriter, r *http.Request) {
			sseResponse(w, `{"type":"response.created"}`,
				`{"type":"response.failed","response":{"status":"failed","error":{"code":"usage_limit_reached","message":"You've hit your usage limit."}}}`)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := makeTestPaths(t)
			a := Account{ID: "a", Name: "primary", AccountID: "acct"}
			writeTestAccount(t, p, a)
			writeModelsCache(t, filepath.Join(p.CodexHome, "models_cache.json"), "0.151.0",
				`{"slug":"gpt-5.4-mini","visibility":"list","priority":23,"supported_in_api":true}`)
			usePrimeServer(t, tc.handler)

			err := primeWeeklyWindow(p, a)
			primeErr, skipped := classifyPrimeFailure(err)
			if primeErr != "" {
				t.Fatalf("an exhausted account must not surface a red failure: %q", primeErr)
			}
			if skipped == "" {
				t.Fatalf("expected a quiet skip note, got err=%v", err)
			}
		})
	}
}

func TestClassifyPrimeFailureKeepsErrorsToOneLine(t *testing.T) {
	p := makeTestPaths(t)
	a := Account{ID: "a", Name: "primary", AccountID: "acct"}
	writeTestAccount(t, p, a)
	writeModelsCache(t, filepath.Join(p.CodexHome, "models_cache.json"), "0.151.0",
		`{"slug":"gpt-5.4-mini","visibility":"list","priority":23,"supported_in_api":true}`)
	usePrimeServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("ERROR: line one.\nERROR: line two.\nERROR: line three.\n"))
	})

	primeErr, skipped := classifyPrimeFailure(primeWeeklyWindow(p, a))
	if skipped != "" {
		t.Fatalf("a 500 is a real failure, not a skip: %q", skipped)
	}
	if primeErr == "" {
		t.Fatal("expected a failure message")
	}
	if strings.ContainsAny(primeErr, "\n\r") {
		// Status output and the dashboard both allot one row per note.
		t.Fatalf("prime error spans multiple lines: %q", primeErr)
	}
	if n := len([]rune(primeErr)); n > 160 {
		t.Fatalf("prime error is %d runes long: %q", n, primeErr)
	}
}

func TestChooseCodexModelPrefersCheapUsableModels(t *testing.T) {
	models := []codexModel{
		{Slug: "gpt-reserve", Visibility: "hide", Priority: 3, SupportedInAPI: true},
		{Slug: "gpt-5.6-sol", Visibility: "list", Priority: 6, SupportedInAPI: true},
		{Slug: "gpt-5.4-mini", Visibility: "list", Priority: 23, SupportedInAPI: true},
	}
	if got := chooseCodexModel(models); got != "gpt-5.4-mini" {
		t.Fatalf("model=%q", got)
	}

	noMini := []codexModel{
		{Slug: "gpt-hidden", Visibility: "hide", Priority: 99, SupportedInAPI: true},
		{Slug: "gpt-no-api", Visibility: "list", Priority: 90, SupportedInAPI: false},
		{Slug: "gpt-5.6-sol", Visibility: "list", Priority: 6, SupportedInAPI: true},
		{Slug: "gpt-5.4", Visibility: "list", Priority: 16, SupportedInAPI: true},
	}
	if got := chooseCodexModel(noMini); got != "gpt-5.4" {
		t.Fatalf("model=%q; want the least prominent listed model", got)
	}
	if got := chooseCodexModel(nil); got != "" {
		t.Fatalf("model=%q; an empty catalog must not invent a slug", got)
	}
}

func TestPrimeTimeoutIsBoundedForInteractiveUse(t *testing.T) {
	if primeTimeout > 30*time.Second {
		t.Fatalf("primeTimeout=%s; interactive dashboard can block too long", primeTimeout)
	}
}

func writeModelsCache(t *testing.T, path, clientVersion string, models ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	payload := `{"client_version":"` + clientVersion + `","models":[` + strings.Join(models, ",") + `]}`
	if err := os.WriteFile(path, []byte(payload), 0600); err != nil {
		t.Fatal(err)
	}
}
