package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestCodexLoginE2E_BrowserPKCE is the primary e2e test for codex login.
// It mocks the full PKCE browser flow:
//  1. TLS preflight probe → OK
//  2. Open browser → auth URL with PKCE challenge, scopes, audience
//  3. User authenticates → redirect to localhost callback with code
//  4. Exchange code + verifier for token
//  5. Extract API key → save to ~/.pi-go/.env
func TestCodexLoginE2E_BrowserPKCE(t *testing.T) {
	var capturedAuthParams url.Values

	// Mock token endpoint.
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		_ = r.ParseForm()

		// Verify token exchange parameters.
		if r.FormValue("grant_type") != "authorization_code" {
			t.Errorf("wrong grant_type: %q", r.FormValue("grant_type"))
		}
		if r.FormValue("code") != "codex-auth-code-from-browser" {
			t.Errorf("wrong code: %q", r.FormValue("code"))
		}
		if r.FormValue("code_verifier") == "" {
			t.Error("missing code_verifier in token exchange")
		}
		if r.FormValue("client_id") != "pi-go-cli" {
			t.Errorf("wrong client_id: %q", r.FormValue("client_id"))
		}
		if r.FormValue("redirect_uri") == "" {
			t.Error("missing redirect_uri in token exchange")
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(TokenResponse{
			AccessToken:  "codex-browser-token-xyz789",
			TokenType:    "bearer",
			ExpiresIn:    3600,
			RefreshToken: "codex-refresh-token",
			IDToken:      "codex-id-token",
		})
	}))
	defer tokenSrv.Close()

	// Mock auth endpoint — simulates OpenAI login page redirecting back.
	authSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuthParams = r.URL.Query()

		redirectURI := r.URL.Query().Get("redirect_uri")
		state := r.URL.Query().Get("state")

		// Simulate: user logs in on OpenAI, browser redirects to callback.
		http.Redirect(w, r,
			redirectURI+"?code=codex-auth-code-from-browser&state="+state,
			http.StatusFound)
	}))
	defer authSrv.Close()

	// Build codex provider pointing to mock servers.
	prov := Provider{
		Name:     "codex",
		EnvVar:   "OPENAI_API_KEY",
		AuthURL:  authSrv.URL + "/authorize",
		TokenURL: tokenSrv.URL + "/oauth/token",
		ClientID: "pi-go-cli",
		Scopes:   []string{"openid", "profile", "email", "offline_access"},
		ExtraParams: map[string]string{
			"audience": "https://api.openai.com/v1",
		},
		TLSPreflight: true,
		TokenToKey: func(tok *TokenResponse) string {
			if tok.APIKey != "" {
				return tok.APIKey
			}
			return tok.AccessToken
		},
		KeyPageURL: "https://platform.openai.com/api-keys",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// --- Run PKCE flow with simulated browser ---
	result, err := PKCEFlow(ctx, prov, func(authURL string) error {
		// Simulate browser: GET auth URL → follow redirect to localhost callback.
		client := &http.Client{
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
		resp, err := client.Get(authURL)
		if err != nil {
			return err
		}
		defer resp.Body.Close()

		if resp.StatusCode == http.StatusFound {
			loc := resp.Header.Get("Location")
			_, err = http.Get(loc)
			return err
		}
		return fmt.Errorf("expected redirect, got %d", resp.StatusCode)
	})

	if err != nil {
		t.Fatalf("PKCEFlow() error: %v", err)
	}
	if result.Err != nil {
		t.Fatalf("PKCEFlow result error: %v", result.Err)
	}

	// --- Verify auth URL parameters ---
	if capturedAuthParams.Get("response_type") != "code" {
		t.Error("expected response_type=code")
	}
	if capturedAuthParams.Get("client_id") != "pi-go-cli" {
		t.Errorf("expected client_id=pi-go-cli, got %q", capturedAuthParams.Get("client_id"))
	}
	if capturedAuthParams.Get("code_challenge") == "" {
		t.Error("missing PKCE code_challenge")
	}
	if capturedAuthParams.Get("code_challenge_method") != "S256" {
		t.Error("expected code_challenge_method=S256")
	}
	scopes := capturedAuthParams.Get("scope")
	for _, expected := range []string{"openid", "profile", "email", "offline_access"} {
		if !strings.Contains(scopes, expected) {
			t.Errorf("expected scope %q in %q", expected, scopes)
		}
	}
	if capturedAuthParams.Get("audience") != "https://api.openai.com/v1" {
		t.Errorf("expected audience extra param, got %q", capturedAuthParams.Get("audience"))
	}
	if capturedAuthParams.Get("state") == "" {
		t.Error("missing state parameter")
	}
	if !strings.HasPrefix(capturedAuthParams.Get("redirect_uri"), "http://127.0.0.1:") {
		t.Errorf("expected localhost redirect_uri, got %q", capturedAuthParams.Get("redirect_uri"))
	}

	// --- Verify result ---
	if result.APIKey != "codex-browser-token-xyz789" {
		t.Errorf("expected 'codex-browser-token-xyz789', got %q", result.APIKey)
	}
	if result.Provider != "codex" {
		t.Errorf("expected provider 'codex', got %q", result.Provider)
	}
	if result.EnvVar != "OPENAI_API_KEY" {
		t.Errorf("expected env var 'OPENAI_API_KEY', got %q", result.EnvVar)
	}

	// --- Save key and verify ---
	tmpDir := t.TempDir()
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	if err := SaveKey(result.EnvVar, result.APIKey); err != nil {
		t.Fatalf("SaveKey() error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(tmpDir, ".pi-go", ".env"))
	if err != nil {
		t.Fatalf("error reading .env: %v", err)
	}
	if !strings.Contains(string(data), "OPENAI_API_KEY=codex-browser-token-xyz789") {
		t.Errorf("expected key in .env, got: %s", data)
	}

	if os.Getenv("OPENAI_API_KEY") != "codex-browser-token-xyz789" {
		t.Error("expected OPENAI_API_KEY set in environment")
	}
	os.Unsetenv("OPENAI_API_KEY")
}

// TestCodexLoginE2E_BrowserPKCE_OAuthError verifies handling when OpenAI
// returns an error in the callback (user denies, session expired, etc).
func TestCodexLoginE2E_BrowserPKCE_OAuthError(t *testing.T) {
	authSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirectURI := r.URL.Query().Get("redirect_uri")
		// Simulate: OpenAI returns error in callback.
		http.Redirect(w, r,
			redirectURI+"?error=access_denied&error_description=User+cancelled+login",
			http.StatusFound)
	}))
	defer authSrv.Close()

	prov := Provider{
		Name:     "codex",
		EnvVar:   "OPENAI_API_KEY",
		AuthURL:  authSrv.URL,
		TokenURL: "http://unused",
		ClientID: "pi-go-cli",
		Scopes:   []string{"openid"},
		TokenToKey: func(tok *TokenResponse) string {
			return tok.AccessToken
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := PKCEFlow(ctx, prov, func(authURL string) error {
		client := &http.Client{
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
		resp, err := client.Get(authURL)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusFound {
			loc := resp.Header.Get("Location")
			_, err = http.Get(loc)
			return err
		}
		return nil
	})

	if err != nil {
		t.Fatalf("PKCEFlow() error: %v", err)
	}
	if result.Err == nil {
		t.Fatal("expected OAuth error in result")
	}
	if !strings.Contains(result.Err.Error(), "User cancelled login") {
		t.Errorf("expected 'User cancelled login' in error, got: %v", result.Err)
	}
}

// TestCodexLoginE2E_BrowserPKCE_TokenExchangeFails verifies handling when
// the token exchange returns an error (invalid code, expired, etc).
func TestCodexLoginE2E_BrowserPKCE_TokenExchangeFails(t *testing.T) {
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{
			"error":             "invalid_grant",
			"error_description": "Authorization code has expired",
		})
	}))
	defer tokenSrv.Close()

	authSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirectURI := r.URL.Query().Get("redirect_uri")
		state := r.URL.Query().Get("state")
		http.Redirect(w, r, redirectURI+"?code=expired-code&state="+state, http.StatusFound)
	}))
	defer authSrv.Close()

	prov := Provider{
		Name:     "codex",
		EnvVar:   "OPENAI_API_KEY",
		AuthURL:  authSrv.URL,
		TokenURL: tokenSrv.URL,
		ClientID: "pi-go-cli",
		Scopes:   []string{"openid"},
		TokenToKey: func(tok *TokenResponse) string {
			return tok.AccessToken
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := PKCEFlow(ctx, prov, func(authURL string) error {
		client := &http.Client{
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
		resp, err := client.Get(authURL)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusFound {
			loc := resp.Header.Get("Location")
			_, err = http.Get(loc)
			return err
		}
		return nil
	})

	if err != nil {
		t.Fatalf("PKCEFlow() error: %v", err)
	}
	if result.Err == nil {
		t.Fatal("expected token exchange error")
	}
	if !strings.Contains(result.Err.Error(), "token exchange") {
		t.Errorf("expected 'token exchange' in error, got: %v", result.Err)
	}
}

// TestCodexLoginE2E_BrowserPKCE_Timeout verifies the flow handles
// browser timeout (user never completes login).
func TestCodexLoginE2E_BrowserPKCE_Timeout(t *testing.T) {
	prov := Provider{
		Name:     "codex",
		EnvVar:   "OPENAI_API_KEY",
		AuthURL:  "http://127.0.0.1:1/authorize", // unreachable
		TokenURL: "http://127.0.0.1:1/token",
		ClientID: "pi-go-cli",
		Scopes:   []string{"openid"},
		TokenToKey: func(tok *TokenResponse) string {
			return tok.AccessToken
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	result, err := PKCEFlow(ctx, prov, func(authURL string) error {
		// Don't actually open anything — simulate user never completing login.
		return nil
	})

	if err != nil {
		t.Fatalf("PKCEFlow() error: %v", err)
	}
	if result.Err == nil {
		t.Fatal("expected timeout error")
	}
}

// TestCodexLoginE2E_TLSPreflight verifies TLS preflight runs against a mock.
func TestCodexLoginE2E_TLSPreflight(t *testing.T) {
	// Test with real endpoint — structure validation.
	result := RunTLSPreflight(2000)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	// Can't guarantee network in CI, but verify result has correct shape.
	if !result.OK && result.Kind == "" {
		t.Error("failed result should have a kind (tls-cert or network)")
	}
}

// TestCodexLoginE2E_403HTML verifies graceful handling when
// an endpoint returns a 403 with HTML (Cloudflare block).
func TestCodexLoginE2E_403HTML(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`<!DOCTYPE html><html><head><title>403 Forbidden</title></head>
<body><h1>Just a moment...</h1><p>Enable JavaScript and cookies to continue</p>
<script>/* challenge code */</script></body></html>`))
	}))
	defer srv.Close()

	prov := Provider{
		Name:      "codex",
		DeviceURL: srv.URL + "/device/code",
		ClientID:  "pi-go-cli",
		Scopes:    []string{"openai.public"},
		ExtraParams: map[string]string{
			"audience": "https://api.openai.com/v1",
		},
	}

	_, err := DeviceFlow(context.Background(), prov)
	if err == nil {
		t.Fatal("expected error for 403 HTML response")
	}

	errMsg := err.Error()
	if strings.Contains(errMsg, "<html>") || strings.Contains(errMsg, "<script>") {
		t.Errorf("error message should not contain raw HTML, got: %s", errMsg)
	}
	if !strings.Contains(errMsg, "403") {
		t.Errorf("expected 403 in error message, got: %s", errMsg)
	}
	if !strings.Contains(errMsg, "HTML error page") {
		t.Errorf("expected sanitized HTML note, got: %s", errMsg)
	}
}

// TestCodexProviderConfig verifies the codex provider is configured for PKCE.
func TestCodexProviderConfig(t *testing.T) {
	p, ok := FindProvider("codex")
	if !ok {
		t.Fatal("codex provider not found")
	}
	if p.EnvVar != "OPENAI_API_KEY" {
		t.Errorf("expected OPENAI_API_KEY, got %q", p.EnvVar)
	}
	if p.UseDeviceFlow {
		t.Error("codex should NOT use device flow (uses PKCE browser flow)")
	}
	if p.DeviceURL != "" {
		t.Error("codex should not have DeviceURL (PKCE only)")
	}
	if p.AuthURL == "" {
		t.Error("codex must have AuthURL for PKCE")
	}
	if p.TokenURL == "" {
		t.Error("codex must have TokenURL for PKCE")
	}
	if p.TLSPreflight != true {
		t.Error("codex should have TLS preflight enabled")
	}
	// Verify scopes include OpenID Connect scopes.
	scopes := strings.Join(p.Scopes, " ")
	for _, expected := range []string{"openid", "profile", "email"} {
		if !strings.Contains(scopes, expected) {
			t.Errorf("expected scope %q in codex scopes", expected)
		}
	}
	// Verify audience extra param.
	if p.ExtraParams["audience"] != "https://api.openai.com/v1" {
		t.Errorf("expected audience extra param, got %v", p.ExtraParams)
	}
}

