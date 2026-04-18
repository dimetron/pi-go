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

// TestAnthropicLoginE2E_ManualCodeFlow is an e2e test for the Anthropic
// manual-code OAuth flow. It tests the complete flow:
//  1. StartManualCodeFlow builds the auth URL with PKCE challenge
//  2. User pastes the final redirect URL (simulated here)
//  3. Token exchange with JSON body + state
//  4. API key creation via createAPIKey endpoint
//  5. Save key to ~/.pi-go/.env
func TestAnthropicLoginE2E_ManualCodeFlow(t *testing.T) {
	var capturedTokenReq map[string]string
	var capturedAPIKeyReq bool

	// Mock API key creation endpoint.
	apiKeySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAPIKeyReq = true
		if auth := r.Header.Get("Authorization"); !strings.HasPrefix(auth, "Bearer ") {
			t.Errorf("expected Bearer auth, got %q", auth)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"raw_key":"sk-ant-api-test-123"}`))
	}))
	defer apiKeySrv.Close()

	// Mock token endpoint.
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		capturedTokenReq = body

		if body["grant_type"] != "authorization_code" {
			t.Errorf("wrong grant_type: %q", body["grant_type"])
		}
		if body["code"] != "anthropic-auth-code" {
			t.Errorf("wrong code: %q", body["code"])
		}
		if body["code_verifier"] == "" {
			t.Error("missing code_verifier")
		}
		if body["client_id"] != "9d1c250a-e61b-44d9-88ed-5944d1962f5e" {
			t.Errorf("wrong client_id: %q", body["client_id"])
		}
		if body["state"] == "" {
			t.Error("missing state in token request")
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(TokenResponse{
			AccessToken: "anthropic-oauth-token-xyz",
			TokenType:   "bearer",
			ExpiresIn:   3600,
		})
	}))
	defer tokenSrv.Close()

	// Mock auth endpoint (would normally redirect, but we simulate user pasting).
	authSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()

		// Simulate: user pastes the redirect URL with code and state.
		// The mock server returns the redirect URL that the user would paste.
		redirectURI := q.Get("redirect_uri")
		state := q.Get("state")

		w.Header().Set("Content-Type", "application/json")
		// In real flow, user would get redirected to redirect_uri?code=...&state=...
		// For manual code, we simulate the user pasting the full redirect URL.
		redirectURL := fmt.Sprintf("%s?code=anthropic-auth-code&state=%s", redirectURI, state)
		json.NewEncoder(w).Encode(map[string]string{
			"redirect_url": redirectURL,
		})
	}))
	defer authSrv.Close()

	// Build Anthropic provider pointing to mock servers.
	prov := Provider{
		Name:      "anthropic",
		EnvVar:    "ANTHROPIC_API_KEY",
		AuthURL:   authSrv.URL + "/authorize",
		TokenURL:  tokenSrv.URL + "/oauth/token",
		APIKeyURL: apiKeySrv.URL + "/create_api_key",
		ClientID:  "9d1c250a-e61b-44d9-88ed-5944d1962f5e",
		Scopes: []string{
			"user:profile",
			"user:inference",
			"org:create_api_key",
			"user:sessions:claude_code",
			"user:mcp_servers",
			"user:file_upload",
		},
		ExtraParams:       map[string]string{"code": "true"},
		ManualCode:        true,
		ManualRedirectURI: "https://platform.claude.com/oauth/code/callback",
		TokenJSONBody:     true,
		TokenToKey: func(tok *TokenResponse) string {
			if tok.RawKey != "" {
				return tok.RawKey
			}
			return tok.AccessToken
		},
	}

	// --- Start manual code flow ---
	sess, err := StartManualCodeFlow(prov)
	if err != nil {
		t.Fatalf("StartManualCodeFlow error: %v", err)
	}

	// Verify auth URL parameters.
	authURL, err := url.Parse(sess.AuthURL)
	if err != nil {
		t.Fatalf("parse auth URL: %v", err)
	}
	q := authURL.Query()
	if q.Get("response_type") != "code" {
		t.Error("expected response_type=code")
	}
	if q.Get("client_id") != "9d1c250a-e61b-44d9-88ed-5944d1962f5e" {
		t.Errorf("wrong client_id: %q", q.Get("client_id"))
	}
	if q.Get("code_challenge") == "" {
		t.Error("missing PKCE code_challenge")
	}
	if q.Get("code_challenge_method") != "S256" {
		t.Error("expected code_challenge_method=S256")
	}
	if q.Get("code") != "true" {
		t.Errorf("expected code=true extra param, got %q", q.Get("code"))
	}
	if q.Get("state") == "" {
		t.Error("missing state parameter")
	}
	if q.Get("redirect_uri") != "https://platform.claude.com/oauth/code/callback" {
		t.Errorf("wrong redirect_uri: %q", q.Get("redirect_uri"))
	}
	if !strings.Contains(q.Get("scope"), "user:file_upload") {
		t.Errorf("expected user:file_upload in scope, got %q", q.Get("scope"))
	}

	// --- Simulate user pasting the redirect URL ---
	// In real flow, user would copy the redirect URL from browser.
	// We construct one that matches what the mock server would redirect to.
	pastedURL := fmt.Sprintf("https://platform.claude.com/oauth/code/callback?code=anthropic-auth-code&state=%s", sess.State)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// --- Complete manual code flow ---
	result, err := CompleteManualCodeFlow(ctx, sess, pastedURL)
	if err != nil {
		t.Fatalf("CompleteManualCodeFlow error: %v", err)
	}
	if result.Err != nil {
		t.Fatalf("result error: %v", result.Err)
	}

	// --- Verify token exchange ---
	if capturedTokenReq == nil {
		t.Fatal("token endpoint was not called")
	}
	if capturedTokenReq["redirect_uri"] != "https://platform.claude.com/oauth/code/callback" {
		t.Errorf("wrong redirect_uri in token exchange: %q", capturedTokenReq["redirect_uri"])
	}

	// --- Verify API key creation ---
	if !capturedAPIKeyReq {
		t.Fatal("API key creation endpoint was not called")
	}

	// --- Verify result ---
	if result.APIKey != "sk-ant-api-test-123" {
		t.Errorf("expected 'sk-ant-api-test-123', got %q", result.APIKey)
	}
	if result.Provider != "anthropic" {
		t.Errorf("expected provider 'anthropic', got %q", result.Provider)
	}
	if result.EnvVar != "ANTHROPIC_API_KEY" {
		t.Errorf("expected env var 'ANTHROPIC_API_KEY', got %q", result.EnvVar)
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
	if !strings.Contains(string(data), "ANTHROPIC_API_KEY=sk-ant-api-test-123") {
		t.Errorf("expected key in .env, got: %s", data)
	}

	if os.Getenv("ANTHROPIC_API_KEY") != "sk-ant-api-test-123" {
		t.Error("expected ANTHROPIC_API_KEY set in environment")
	}
}

// TestAnthropicLoginE2E_ManualCodeFlow_StateMismatch verifies error handling
// when the state in the pasted URL doesn't match.
func TestAnthropicLoginE2E_ManualCodeFlow_StateMismatch(t *testing.T) {
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("token endpoint should not be called on state mismatch")
	}))
	defer tokenSrv.Close()

	prov := Provider{
		Name:              "anthropic",
		EnvVar:            "ANTHROPIC_API_KEY",
		TokenURL:          tokenSrv.URL,
		ClientID:          "test-client",
		ManualCode:        true,
		ManualRedirectURI: "https://platform.claude.com/oauth/code/callback",
		TokenJSONBody:     true,
		TokenToKey:        func(tok *TokenResponse) string { return tok.AccessToken },
	}

	sess, err := StartManualCodeFlow(prov)
	if err != nil {
		t.Fatalf("StartManualCodeFlow error: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Paste URL with wrong state.
	pastedURL := "https://platform.claude.com/oauth/code/callback?code=auth-code&state=wrong-state"
	_, err = CompleteManualCodeFlow(ctx, sess, pastedURL)
	if err == nil {
		t.Fatal("expected error on state mismatch")
	}
	if !strings.Contains(err.Error(), "state mismatch") {
		t.Errorf("expected 'state mismatch' in error, got: %v", err)
	}
}

// TestAnthropicLoginE2E_ManualCodeFlow_TokenExchangeFails verifies error
// handling when the token exchange fails.
func TestAnthropicLoginE2E_ManualCodeFlow_TokenExchangeFails(t *testing.T) {
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "invalid_grant", http.StatusBadRequest)
	}))
	defer tokenSrv.Close()

	prov := Provider{
		Name:              "anthropic",
		EnvVar:            "ANTHROPIC_API_KEY",
		TokenURL:          tokenSrv.URL,
		ClientID:          "test-client",
		ManualCode:        true,
		ManualRedirectURI: "https://platform.claude.com/oauth/code/callback",
		TokenJSONBody:     true,
		TokenToKey:        func(tok *TokenResponse) string { return tok.AccessToken },
	}

	sess, err := StartManualCodeFlow(prov)
	if err != nil {
		t.Fatalf("StartManualCodeFlow error: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pastedURL := fmt.Sprintf("https://platform.claude.com/oauth/code/callback?code=expired-code&state=%s", sess.State)
	result, err := CompleteManualCodeFlow(ctx, sess, pastedURL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Err == nil {
		t.Fatal("expected error in result")
	}
	if !strings.Contains(result.Err.Error(), "token exchange") {
		t.Errorf("expected 'token exchange' in error, got: %v", result.Err)
	}
}

// TestAnthropicProvider_ConfigVerification verifies the Anthropic provider
// configuration matches the expected values from the working example.
func TestAnthropicProvider_ConfigVerification(t *testing.T) {
	prov, ok := FindProvider("anthropic")
	if !ok {
		t.Fatal("anthropic provider not found")
	}

	// Verify AuthURL uses the correct path.
	if prov.AuthURL != "https://claude.com/cai/oauth/authorize" {
		t.Errorf("wrong AuthURL: %q", prov.AuthURL)
	}

	// Verify TokenURL.
	if prov.TokenURL != "https://platform.claude.com/v1/oauth/token" {
		t.Errorf("wrong TokenURL: %q", prov.TokenURL)
	}

	// Verify ClientID matches the working example.
	if prov.ClientID != "9d1c250a-e61b-44d9-88ed-5944d1962f5e" {
		t.Errorf("wrong ClientID: %q", prov.ClientID)
	}

	// Verify ManualRedirectURI.
	if prov.ManualRedirectURI != "https://platform.claude.com/oauth/code/callback" {
		t.Errorf("wrong ManualRedirectURI: %q", prov.ManualRedirectURI)
	}

	// Verify APIKeyURL.
	if prov.APIKeyURL != "https://console.anthropic.com/api/oauth/claude_cli/create_api_key" {
		t.Errorf("wrong APIKeyURL: %q", prov.APIKeyURL)
	}

	// Verify scopes match the working example.
	expectedScopes := []string{
		"user:profile",
		"user:inference",
		"org:create_api_key",
		"user:sessions:claude_code",
		"user:mcp_servers",
		"user:file_upload",
	}
	if len(prov.Scopes) != len(expectedScopes) {
		t.Errorf("wrong number of scopes: got %d, want %d", len(prov.Scopes), len(expectedScopes))
	}
	for i, scope := range expectedScopes {
		found := false
		for _, s := range prov.Scopes {
			if s == scope {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing scope %q at index %d", scope, i)
		}
	}

	// Verify ExtraParams includes code=true.
	if prov.ExtraParams["code"] != "true" {
		t.Errorf("expected code=true in ExtraParams, got %q", prov.ExtraParams["code"])
	}

	// Verify flow type flags.
	if !prov.ManualCode {
		t.Error("expected ManualCode=true")
	}
	if !prov.TokenJSONBody {
		t.Error("expected TokenJSONBody=true")
	}
}
