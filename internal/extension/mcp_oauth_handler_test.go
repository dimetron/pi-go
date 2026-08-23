package extension

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"golang.org/x/oauth2"
)

func TestMCPOAuthCallbackHandler(t *testing.T) {
	t.Run("provider error renders failure page and fails the flow", func(t *testing.T) {
		ch := make(chan mcpOAuthCallbackResult, 1)
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/callback?error=access_denied&error_description=User+said+no", nil)
		mcpOAuthCallbackHandler(ch)(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", w.Code)
		}
		body := w.Body.String()
		if !strings.Contains(body, "Authentication failed") || !strings.Contains(body, "User said no") {
			t.Errorf("expected failure page with description, got %q", body)
		}
		select {
		case r := <-ch:
			if r.res != nil || r.err == nil || !strings.Contains(r.err.Error(), "User said no") {
				t.Errorf("expected provider error delivered, got %+v", r)
			}
		default:
			t.Fatal("provider error must be delivered to the waiting fetcher")
		}
	})

	t.Run("provider error without description falls back to code", func(t *testing.T) {
		ch := make(chan mcpOAuthCallbackResult, 1)
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/callback?error=invalid_scope", nil)
		mcpOAuthCallbackHandler(ch)(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", w.Code)
		}
		if !strings.Contains(w.Body.String(), "invalid_scope") {
			t.Error("expected error code used as description")
		}
		if r := <-ch; r.err == nil || !strings.Contains(r.err.Error(), "invalid_scope") {
			t.Errorf("expected error code in delivered error, got %+v", r)
		}
	})

	t.Run("missing code renders failure page and fails the flow", func(t *testing.T) {
		ch := make(chan mcpOAuthCallbackResult, 1)
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/callback?state=s1", nil)
		mcpOAuthCallbackHandler(ch)(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", w.Code)
		}
		if !strings.Contains(w.Body.String(), "No code received") {
			t.Error("expected 'No code received' page")
		}
		if r := <-ch; r.res != nil || r.err == nil {
			t.Errorf("expected missing-code error delivered, got %+v", r)
		}
	})

	t.Run("success delivers result once and ignores duplicates", func(t *testing.T) {
		ch := make(chan mcpOAuthCallbackResult, 1)
		h := mcpOAuthCallbackHandler(ch)

		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/callback?code=abc&state=s1&iss=https://as.example.com", nil)
		h(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want 200", w.Code)
		}
		if !strings.Contains(w.Body.String(), "Authentication successful") {
			t.Error("expected success page")
		}
		select {
		case r := <-ch:
			if r.err != nil || r.res == nil || r.res.Code != "abc" || r.res.State != "s1" || r.res.Iss != "https://as.example.com" {
				t.Errorf("unexpected result %+v", r)
			}
		default:
			t.Fatal("expected result on resultChan")
		}

		// Fill the channel again so a second redirect hits the "already
		// pending" branch; it must still render success and not block.
		ch <- mcpOAuthCallbackResult{res: &auth.AuthorizationResult{Code: "first"}}
		w2 := httptest.NewRecorder()
		done := make(chan struct{})
		go func() {
			h(w2, httptest.NewRequest(http.MethodGet, "/callback?code=dup", nil))
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("duplicate callback blocked")
		}
		if w2.Code != http.StatusOK {
			t.Errorf("duplicate status = %d, want 200", w2.Code)
		}
		if r := <-ch; r.res == nil || r.res.Code != "first" {
			t.Errorf("duplicate must not replace the pending result, got %+v", r)
		}
	})
}

func TestMCPOAuthCodeFetcher(t *testing.T) {
	allowInteractiveOAuth(t)

	t.Run("returns callback result and opens browser", func(t *testing.T) {
		ch := make(chan mcpOAuthCallbackResult, 1)
		var opened string
		fetch := mcpOAuthCodeFetcher("srv", ch, func(u string) error {
			opened = u
			// The browser redirect lands on the callback after the URL opens.
			ch <- mcpOAuthCallbackResult{res: &auth.AuthorizationResult{Code: "code-1", State: "st"}}
			return nil
		})

		res, err := fetch(context.Background(), &auth.AuthorizationArgs{URL: "https://as.example.com/authorize?x=1"})
		if err != nil {
			t.Fatal(err)
		}
		if res.Code != "code-1" || res.State != "st" {
			t.Errorf("unexpected result %+v", res)
		}
		if opened != "https://as.example.com/authorize?x=1" {
			t.Errorf("browser opened with %q", opened)
		}
	})

	t.Run("stale outcome is dropped before the flow starts", func(t *testing.T) {
		ch := make(chan mcpOAuthCallbackResult, 1)
		ch <- mcpOAuthCallbackResult{res: &auth.AuthorizationResult{Code: "stale"}}
		fetch := mcpOAuthCodeFetcher("srv", ch, func(string) error {
			// Simulate the browser redirect arriving during the flow.
			ch <- mcpOAuthCallbackResult{res: &auth.AuthorizationResult{Code: "fresh", State: "s"}}
			return nil
		})
		res, err := fetch(context.Background(), &auth.AuthorizationArgs{URL: "https://as.example.com/authorize"})
		if err != nil {
			t.Fatal(err)
		}
		if res.Code != "fresh" {
			t.Errorf("expected fresh result, got %+v", res)
		}
	})

	t.Run("provider error is returned", func(t *testing.T) {
		ch := make(chan mcpOAuthCallbackResult, 1)
		fetch := mcpOAuthCodeFetcher("srv", ch, func(string) error {
			ch <- mcpOAuthCallbackResult{err: errors.New("OAuth error: access_denied")}
			return nil
		})
		res, err := fetch(context.Background(), &auth.AuthorizationArgs{URL: "https://as.example.com/authorize"})
		if err == nil || !strings.Contains(err.Error(), "access_denied") {
			t.Fatalf("expected provider error, got res=%v err=%v", res, err)
		}
	})

	t.Run("browser failure is non-fatal and context cancel aborts", func(t *testing.T) {
		ch := make(chan mcpOAuthCallbackResult, 1)
		fetch := mcpOAuthCodeFetcher("srv", ch, func(string) error { return errors.New("no browser") })

		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		res, err := fetch(ctx, &auth.AuthorizationArgs{URL: "https://as.example.com/authorize"})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got res=%v err=%v", res, err)
		}
	})
}

func TestMCPOAuthNewTokenSource_PersistsAndWraps(t *testing.T) {
	setTestHome(t)
	const server, url = "nts", "https://n.example.com/mcp"

	cfg := &oauth2.Config{ClientID: "cid", Endpoint: oauth2.Endpoint{TokenURL: "https://as.example.com/token"}}
	tok := &oauth2.Token{AccessToken: "at", RefreshToken: "rt", TokenType: "Bearer", Expiry: time.Now().Add(time.Hour)}

	ts, err := mcpOAuthNewTokenSource(server, url)(context.Background(), cfg, tok)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := ts.(*persistingTokenSource); !ok {
		t.Fatalf("expected *persistingTokenSource, got %T", ts)
	}
	got, err := ts.Token()
	if err != nil {
		t.Fatal(err)
	}
	if got.AccessToken != "at" {
		t.Errorf("unexpected token %+v", got)
	}

	// The token must be cached for the next session with its refresh config.
	cached := loadMCPOAuthTokenSource(server, url)
	if cached == nil {
		t.Fatal("expected token to be cached on disk")
	}
	if _, ok := cached.(*persistingTokenSource); !ok {
		t.Errorf("expected cached entry to carry refresh config, got %T", cached)
	}
}

func TestMCPOAuthNewTokenSource_CacheFailureIsNonFatal(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	cfg := &oauth2.Config{}
	tok := &oauth2.Token{AccessToken: "at", Expiry: time.Now().Add(time.Hour)}
	ts, err := mcpOAuthNewTokenSource("x", "https://x")(context.Background(), cfg, tok)
	if err != nil {
		t.Fatalf("cache failure must not fail token source creation: %v", err)
	}
	got, err := ts.Token()
	if err != nil || got.AccessToken != "at" {
		t.Fatalf("expected in-memory token to work, got %+v err=%v", got, err)
	}
}

func TestNewMCPOAuthHandler_UsesCachedToken(t *testing.T) {
	setTestHome(t)
	const server, url = "cached-srv", "https://c.example.com/mcp"

	cfg := &oauth2.Config{ClientID: "cid", Endpoint: oauth2.Endpoint{TokenURL: "https://as.example.com/token"}}
	tok := &oauth2.Token{AccessToken: "cached-at", RefreshToken: "rt", Expiry: time.Now().Add(time.Hour)}
	if err := saveMCPOAuthToken(server, url, cfg, tok); err != nil {
		t.Fatal(err)
	}

	h, err := newMCPOAuthHandler(server, url)
	if err != nil {
		t.Fatal(err)
	}
	ach, ok := h.(*auth.AuthorizationCodeHandler)
	if !ok {
		t.Fatalf("expected *auth.AuthorizationCodeHandler, got %T", h)
	}
	ts, err := ach.TokenSource(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if ts == nil {
		t.Fatal("expected cached token source to be injected as InitialTokenSource")
	}
	got, err := ts.Token()
	if err != nil {
		t.Fatal(err)
	}
	if got.AccessToken != "cached-at" {
		t.Errorf("expected cached access token, got %+v", got)
	}
}

func TestNewMCPOAuthHandler_NoCacheStartsUnauthorized(t *testing.T) {
	setTestHome(t)
	h, err := newMCPOAuthHandler("fresh-srv", "https://f.example.com/mcp")
	if err != nil {
		t.Fatal(err)
	}
	ach, ok := h.(*auth.AuthorizationCodeHandler)
	if !ok {
		t.Fatalf("expected *auth.AuthorizationCodeHandler, got %T", h)
	}
	ts, err := ach.TokenSource(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if ts != nil {
		t.Errorf("expected no token source before authorization, got %T", ts)
	}
}

// The SDK calls the fetcher itself on any 401/403, so it is the last place
// that can stop a browser opening in a print, JSON, RPC, socket or ACP run.
// A cached token is still worth presenting there, which is why the handler is
// installed at all — but the flow that needs a human must fail immediately
// rather than block the run for the browser timeout.
func TestMCPOAuthCodeFetcherRefusesWhenNonInteractive(t *testing.T) {
	opened := false
	fetcher := mcpOAuthCodeFetcher("srv", make(chan mcpOAuthCallbackResult, 1), func(string) error {
		opened = true
		return nil
	})

	res, err := fetcher(context.Background(), &auth.AuthorizationArgs{URL: "https://as.example.com/authorize"})
	if err == nil {
		t.Fatalf("got res=%v, want a refusal", res)
	}
	if opened {
		t.Error("opened a browser with no user present")
	}
	if !strings.Contains(err.Error(), "no user to grant it") {
		t.Errorf("error %q does not explain why", err)
	}
}
