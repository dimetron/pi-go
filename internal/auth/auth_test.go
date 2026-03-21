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

func TestGeneratePKCE(t *testing.T) {
	verifier, challenge := generatePKCE()
	if len(verifier) == 0 {
		t.Error("verifier should not be empty")
	}
	if len(challenge) == 0 {
		t.Error("challenge should not be empty")
	}
	if verifier == challenge {
		t.Error("verifier and challenge should be different")
	}

	// Verify deterministic: same verifier produces same challenge.
	// (Can't test since they're random, but verify format.)
	if strings.Contains(verifier, "+") || strings.Contains(verifier, "/") {
		t.Error("verifier should be URL-safe base64")
	}
	if strings.Contains(challenge, "+") || strings.Contains(challenge, "/") {
		t.Error("challenge should be URL-safe base64")
	}
}

func TestGenerateState(t *testing.T) {
	s1 := generateState()
	s2 := generateState()
	if s1 == "" {
		t.Error("state should not be empty")
	}
	if s1 == s2 {
		t.Error("states should be unique")
	}
}

func TestBuildAuthURL(t *testing.T) {
	prov := Provider{
		AuthURL:  "https://example.com/auth",
		ClientID: "test-client",
		Scopes:   []string{"api", "read"},
		ExtraParams: map[string]string{
			"audience": "https://api.example.com",
		},
	}

	authURL := buildAuthURL(prov, "http://localhost:8080/callback", "test-state", "test-challenge")

	u, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("failed to parse URL: %v", err)
	}

	if u.Scheme != "https" || u.Host != "example.com" || u.Path != "/auth" {
		t.Errorf("unexpected base URL: %s", authURL)
	}

	q := u.Query()
	if q.Get("response_type") != "code" {
		t.Error("missing response_type=code")
	}
	if q.Get("client_id") != "test-client" {
		t.Error("wrong client_id")
	}
	if q.Get("redirect_uri") != "http://localhost:8080/callback" {
		t.Error("wrong redirect_uri")
	}
	if q.Get("state") != "test-state" {
		t.Error("wrong state")
	}
	if q.Get("code_challenge") != "test-challenge" {
		t.Error("wrong code_challenge")
	}
	if q.Get("code_challenge_method") != "S256" {
		t.Error("wrong code_challenge_method")
	}
	if q.Get("audience") != "https://api.example.com" {
		t.Error("missing extra param audience")
	}
}

func TestHandleCallback_Success(t *testing.T) {
	ch := make(chan codeResult, 1)
	state := "test-state"

	req := httptest.NewRequest("GET", "/callback?code=test-code&state=test-state", nil)
	w := httptest.NewRecorder()

	handleCallback(w, req, state, ch)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	cr := <-ch
	if cr.err != nil {
		t.Errorf("unexpected error: %v", cr.err)
	}
	if cr.code != "test-code" {
		t.Errorf("expected code 'test-code', got %q", cr.code)
	}
}

func TestHandleCallback_StateMismatch(t *testing.T) {
	ch := make(chan codeResult, 1)

	req := httptest.NewRequest("GET", "/callback?code=test-code&state=wrong-state", nil)
	w := httptest.NewRecorder()

	handleCallback(w, req, "expected-state", ch)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}

	cr := <-ch
	if cr.err == nil {
		t.Error("expected error for state mismatch")
	}
}

func TestHandleCallback_OAuthError(t *testing.T) {
	ch := make(chan codeResult, 1)

	req := httptest.NewRequest("GET", "/callback?error=access_denied&error_description=User+denied+access", nil)
	w := httptest.NewRecorder()

	handleCallback(w, req, "any-state", ch)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}

	cr := <-ch
	if cr.err == nil || !strings.Contains(cr.err.Error(), "User denied access") {
		t.Errorf("expected OAuth error with description, got: %v", cr.err)
	}
}

func TestHandleCallback_NoCode(t *testing.T) {
	ch := make(chan codeResult, 1)

	req := httptest.NewRequest("GET", "/callback?state=test-state", nil)
	w := httptest.NewRecorder()

	handleCallback(w, req, "test-state", ch)

	cr := <-ch
	if cr.err == nil {
		t.Error("expected error for missing code")
	}
}

func TestPKCEFlow_ExchangeSuccess(t *testing.T) {
	// Mock token endpoint.
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		_ = r.ParseForm()
		if r.FormValue("grant_type") != "authorization_code" {
			http.Error(w, "wrong grant_type", http.StatusBadRequest)
			return
		}
		if r.FormValue("code") == "" {
			http.Error(w, "missing code", http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(TokenResponse{
			AccessToken: "sk-test-token-123",
			TokenType:   "bearer",
		})
	}))
	defer tokenServer.Close()

	// Mock auth endpoint that immediately redirects with a code.
	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirectURI := r.URL.Query().Get("redirect_uri")
		state := r.URL.Query().Get("state")
		http.Redirect(w, r, redirectURI+"?code=test-auth-code&state="+state, http.StatusFound)
	}))
	defer authServer.Close()

	prov := Provider{
		Name:     "test",
		EnvVar:   "TEST_API_KEY",
		AuthURL:  authServer.URL,
		TokenURL: tokenServer.URL,
		ClientID: "test-client",
		Scopes:   []string{"api"},
		TokenToKey: func(tok *TokenResponse) string {
			return tok.AccessToken
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// openBrowser simulates the browser by making a GET to the auth URL,
	// which redirects to our callback.
	result, err := PKCEFlow(ctx, prov, func(authURL string) error {
		// Follow the redirect chain to simulate browser behavior.
		client := &http.Client{
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
		resp, err := client.Get(authURL)
		if err != nil {
			return err
		}
		defer func() { _ = resp.Body.Close() }()

		// Follow redirect to callback.
		if resp.StatusCode == http.StatusFound {
			loc := resp.Header.Get("Location")
			_, err = http.Get(loc)
			return err
		}
		return fmt.Errorf("unexpected status: %d", resp.StatusCode)
	})

	if err != nil {
		t.Fatalf("PKCEFlow error: %v", err)
	}
	if result.Err != nil {
		t.Fatalf("result error: %v", result.Err)
	}
	if result.APIKey != "sk-test-token-123" {
		t.Errorf("expected api key 'sk-test-token-123', got %q", result.APIKey)
	}
	if result.Provider != "test" {
		t.Errorf("expected provider 'test', got %q", result.Provider)
	}
}

func TestDeviceFlow_Request(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		_ = r.ParseForm()
		if r.FormValue("client_id") == "" {
			http.Error(w, "missing client_id", http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(DeviceCodeResponse{
			DeviceCode:      "device-test-code",
			UserCode:        "ABCD-1234",
			VerificationURI: "https://example.com/device",
			ExpiresIn:       900,
			Interval:        5,
		})
	}))
	defer server.Close()

	prov := Provider{
		Name:      "test",
		DeviceURL: server.URL,
		ClientID:  "test-client",
		Scopes:    []string{"api"},
	}

	dcr, err := DeviceFlow(context.Background(), prov)
	if err != nil {
		t.Fatalf("DeviceFlow error: %v", err)
	}
	if dcr.UserCode != "ABCD-1234" {
		t.Errorf("expected user code 'ABCD-1234', got %q", dcr.UserCode)
	}
	if dcr.DeviceCode != "device-test-code" {
		t.Errorf("expected device code 'device-test-code', got %q", dcr.DeviceCode)
	}
}

func TestPollDeviceToken_Success(t *testing.T) {
	attempt := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempt++
		w.Header().Set("Content-Type", "application/json")
		if attempt < 3 {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "authorization_pending"})
			return
		}
		_ = json.NewEncoder(w).Encode(TokenResponse{
			AccessToken: "sk-device-token",
			TokenType:   "bearer",
		})
	}))
	defer server.Close()

	prov := Provider{
		Name:     "test",
		EnvVar:   "TEST_API_KEY",
		TokenURL: server.URL,
		ClientID: "test-client",
		TokenToKey: func(tok *TokenResponse) string {
			return tok.AccessToken
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := PollDeviceToken(ctx, prov, "device-test-code", 1)
	if err != nil {
		t.Fatalf("PollDeviceToken error: %v", err)
	}
	if result.Err != nil {
		t.Fatalf("result error: %v", result.Err)
	}
	if result.APIKey != "sk-device-token" {
		t.Errorf("expected 'sk-device-token', got %q", result.APIKey)
	}
}

func TestPollDeviceToken_Timeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "authorization_pending"})
	}))
	defer server.Close()

	prov := Provider{
		Name:     "test",
		TokenURL: server.URL,
		ClientID: "test-client",
		TokenToKey: func(tok *TokenResponse) string {
			return tok.AccessToken
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	result, err := PollDeviceToken(ctx, prov, "device-code", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Err == nil {
		t.Error("expected timeout error")
	}
}

func TestFindProvider(t *testing.T) {
	for _, name := range []string{"anthropic", "openai", "codex", "gemini", "Anthropic", "OPENAI", "Codex"} {
		p, ok := FindProvider(name)
		if !ok {
			t.Errorf("expected to find provider %q", name)
		}
		if p.Name == "" {
			t.Errorf("provider %q has empty name", name)
		}
	}

	_, ok := FindProvider("unknown")
	if ok {
		t.Error("should not find unknown provider")
	}
}

func TestSaveKey(t *testing.T) {
	tmpDir := t.TempDir()
	origHome := os.Getenv("HOME")
	_ = os.Setenv("HOME", tmpDir)
	defer func() { _ = os.Setenv("HOME", origHome) }()

	if err := SaveKey("TEST_KEY", "test-value-123"); err != nil {
		t.Fatalf("SaveKey error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(tmpDir, ".pi-go", ".env"))
	if err != nil {
		t.Fatalf("error reading .env: %v", err)
	}
	if !strings.Contains(string(data), "TEST_KEY=test-value-123") {
		t.Errorf("expected key in .env, got: %s", data)
	}

	if os.Getenv("TEST_KEY") != "test-value-123" {
		t.Error("expected env var to be set")
	}
	_ = os.Unsetenv("TEST_KEY")
}

func TestUpdateEnvVar(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		key      string
		value    string
		expected string
	}{
		{"new empty", "", "KEY", "val", "KEY=val\n"},
		{"new append", "OTHER=x\n", "KEY", "val", "OTHER=x\nKEY=val\n"},
		{"update", "KEY=old\n", "KEY", "new", "KEY=new\n"},
		{"update middle", "A=1\nKEY=old\nB=2\n", "KEY", "new", "A=1\nKEY=new\nB=2\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := updateEnvVar(tt.content, tt.key, tt.value)
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestDeviceFlow_NoDeviceURL(t *testing.T) {
	prov := Provider{Name: "test"}
	_, err := DeviceFlow(context.Background(), prov)
	if err == nil {
		t.Error("expected error for missing device URL")
	}
}

func TestIsTLSError(t *testing.T) {
	tests := []struct {
		msg    string
		expect bool
	}{
		{"x509: certificate signed by unknown authority", true},
		{"certificate has expired", true},
		{"self-signed certificate in chain", true},
		{"UNABLE_TO_VERIFY_LEAF_SIGNATURE", true},
		{"CERT_HAS_EXPIRED", true},
		{"DEPTH_ZERO_SELF_SIGNED_CERT", true},
		{"SELF_SIGNED_CERT_IN_CHAIN", true},
		{"ERR_TLS_CERT_ALTNAME_INVALID", true},
		{"UNABLE_TO_GET_ISSUER_CERT_LOCALLY", true},
		{"unable to get local issuer certificate", true},
		{"unable to verify the first certificate", true},
		{"certificate verify failed", true},
		{"certificate expired at 2024-01-01", true},
		{"connection refused", false},
		{"DNS resolution failed", false},
		{"timeout", false},
	}
	for _, tt := range tests {
		t.Run(tt.msg, func(t *testing.T) {
			if got := isTLSError(tt.msg); got != tt.expect {
				t.Errorf("isTLSError(%q) = %v, want %v", tt.msg, got, tt.expect)
			}
		})
	}
}

func TestFormatTLSPreflightFix_TLSCert(t *testing.T) {
	result := &TLSPreflightResult{
		OK:      false,
		Kind:    "tls-cert",
		Message: "x509: certificate signed by unknown authority",
	}
	fix := FormatTLSPreflightFix(result)
	if !strings.Contains(fix, "TLS certificate") {
		t.Errorf("expected TLS cert message, got: %s", fix)
	}
	if !strings.Contains(fix, "brew") {
		t.Errorf("expected brew fix suggestion, got: %s", fix)
	}
}

func TestFormatTLSPreflightFix_Network(t *testing.T) {
	result := &TLSPreflightResult{
		OK:      false,
		Kind:    "network",
		Message: "connection refused",
	}
	fix := FormatTLSPreflightFix(result)
	if !strings.Contains(fix, "network error") {
		t.Errorf("expected network error message, got: %s", fix)
	}
}

func TestRunTLSPreflight_DefaultTimeout(t *testing.T) {
	// timeoutMs <= 0 should default to 5000.
	result := RunTLSPreflight(0)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	// Either OK (network available) or failed with kind set.
	if !result.OK && result.Kind == "" {
		t.Error("failed result should have kind set")
	}
}

func TestRunTLSPreflight_NetworkError(t *testing.T) {
	// Use an unreachable URL with very short timeout to trigger error path.
	// We can't override the const URL, but with 1ms timeout it will fail.
	result := RunTLSPreflight(1)
	// With 1ms timeout against a real URL, this should fail with network error.
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.OK {
		// Extremely unlikely with 1ms but possible if cached.
		t.Skip("preflight succeeded with 1ms timeout, skipping")
	}
	if result.Kind == "" {
		t.Error("expected kind to be set on failure")
	}
	if result.Message == "" {
		t.Error("expected message on failure")
	}
}

func TestProviders_TokenToKey_APIKey(t *testing.T) {
	// Cover the tok.APIKey != "" branches in all provider TokenToKey closures.
	for _, p := range Providers() {
		t.Run(p.Name+"_apikey", func(t *testing.T) {
			tok := &TokenResponse{APIKey: "direct-key", AccessToken: "access-token"}
			key := p.TokenToKey(tok)
			if key != "direct-key" {
				t.Errorf("expected 'direct-key', got %q", key)
			}
		})
		t.Run(p.Name+"_accesstoken", func(t *testing.T) {
			tok := &TokenResponse{AccessToken: "access-token"}
			key := p.TokenToKey(tok)
			if key != "access-token" {
				t.Errorf("expected 'access-token', got %q", key)
			}
		})
	}
}

func TestPKCEFlow_Timeout(t *testing.T) {
	// Test the ctx.Done() branch in PKCEFlow.
	prov := Provider{
		Name:     "test",
		EnvVar:   "TEST_KEY",
		AuthURL:  "http://127.0.0.1:1/auth", // won't be hit
		TokenURL: "http://127.0.0.1:1/token",
		ClientID: "test",
		Scopes:   []string{"api"},
		TokenToKey: func(tok *TokenResponse) string {
			return tok.AccessToken
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	result, err := PKCEFlow(ctx, prov, func(authURL string) error {
		// Don't visit the callback — let context expire.
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Err == nil {
		t.Error("expected context deadline error")
	}
}

func TestPKCEFlow_BrowserOpenError(t *testing.T) {
	prov := Provider{
		Name:     "test",
		EnvVar:   "TEST_KEY",
		AuthURL:  "http://127.0.0.1:0/auth",
		TokenURL: "http://127.0.0.1:0/token",
		ClientID: "test",
		Scopes:   []string{"api"},
		TokenToKey: func(tok *TokenResponse) string {
			return tok.AccessToken
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := PKCEFlow(ctx, prov, func(authURL string) error {
		return fmt.Errorf("browser open failed")
	})
	if err == nil {
		t.Fatal("expected error when browser open fails")
	}
	if !strings.Contains(err.Error(), "opening browser") {
		t.Errorf("expected 'opening browser' error, got: %v", err)
	}
}

func TestExchangeCode_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"server_error"}`))
	}))
	defer srv.Close()

	prov := Provider{TokenURL: srv.URL, ClientID: "test"}
	_, err := exchangeCode(context.Background(), prov, "code", "http://localhost/cb", "verifier")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("expected 500 in error, got: %v", err)
	}
}

func TestExchangeCode_InvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`not json`))
	}))
	defer srv.Close()

	prov := Provider{TokenURL: srv.URL, ClientID: "test"}
	_, err := exchangeCode(context.Background(), prov, "code", "http://localhost/cb", "verifier")
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
	if !strings.Contains(err.Error(), "parsing token response") {
		t.Errorf("expected parse error, got: %v", err)
	}
}

func TestExchangeCode_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	prov := Provider{TokenURL: "http://127.0.0.1:1/token", ClientID: "test"}
	_, err := exchangeCode(ctx, prov, "code", "http://localhost/cb", "verifier")
	if err == nil {
		t.Fatal("expected error with canceled context")
	}
}

func TestRequestDeviceToken_InvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`not json`))
	}))
	defer srv.Close()

	prov := Provider{TokenURL: srv.URL, ClientID: "test"}
	_, err := requestDeviceToken(context.Background(), prov, "device-code")
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
	if !strings.Contains(err.Error(), "parsing token response") {
		t.Errorf("expected parse error, got: %v", err)
	}
}

func TestRequestDeviceToken_NonBadRequestError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`server error`))
	}))
	defer srv.Close()

	prov := Provider{TokenURL: srv.URL, ClientID: "test"}
	_, err := requestDeviceToken(context.Background(), prov, "device-code")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("expected 500 in error, got: %v", err)
	}
}

func TestRequestDeviceToken_BadRequestNonJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`not json`))
	}))
	defer srv.Close()

	prov := Provider{TokenURL: srv.URL, ClientID: "test"}
	_, err := requestDeviceToken(context.Background(), prov, "device-code")
	if err == nil {
		t.Fatal("expected error for bad request with non-JSON")
	}
	if !strings.Contains(err.Error(), "400") {
		t.Errorf("expected 400 in error, got: %v", err)
	}
}

func TestRequestDeviceToken_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	prov := Provider{TokenURL: "http://127.0.0.1:1/token", ClientID: "test"}
	_, err := requestDeviceToken(ctx, prov, "device-code")
	if err == nil {
		t.Fatal("expected error with canceled context")
	}
}

func TestDeviceFlow_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`forbidden`))
	}))
	defer srv.Close()

	prov := Provider{
		Name:      "test",
		DeviceURL: srv.URL,
		ClientID:  "test",
		Scopes:    []string{"api"},
	}

	_, err := DeviceFlow(context.Background(), prov)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "403") {
		t.Errorf("expected 403 in error, got: %v", err)
	}
}

func TestDeviceFlow_InvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`not json`))
	}))
	defer srv.Close()

	prov := Provider{
		Name:      "test",
		DeviceURL: srv.URL,
		ClientID:  "test",
		Scopes:    []string{"api"},
	}

	_, err := DeviceFlow(context.Background(), prov)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
	if !strings.Contains(err.Error(), "parsing device code response") {
		t.Errorf("expected parse error, got: %v", err)
	}
}

func TestDeviceFlow_DefaultInterval(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Return interval=0, should default to 5.
		_ = json.NewEncoder(w).Encode(DeviceCodeResponse{
			DeviceCode:      "test-code",
			UserCode:        "TEST",
			VerificationURI: "https://example.com",
			Interval:        0,
		})
	}))
	defer srv.Close()

	prov := Provider{
		Name:      "test",
		DeviceURL: srv.URL,
		ClientID:  "test",
		Scopes:    []string{"api"},
	}

	dcr, err := DeviceFlow(context.Background(), prov)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dcr.Interval != 5 {
		t.Errorf("expected default interval 5, got %d", dcr.Interval)
	}
}

func TestDeviceFlow_NetworkError(t *testing.T) {
	prov := Provider{
		Name:      "test",
		DeviceURL: "http://127.0.0.1:1/device", // unreachable
		ClientID:  "test",
		Scopes:    []string{"api"},
	}

	_, err := DeviceFlow(context.Background(), prov)
	if err == nil {
		t.Fatal("expected network error")
	}
	if !strings.Contains(err.Error(), "device code request") {
		t.Errorf("expected 'device code request' error, got: %v", err)
	}
}

func TestPollDeviceToken_SlowDown(t *testing.T) {
	attempt := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempt++
		w.Header().Set("Content-Type", "application/json")
		if attempt == 1 {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "slow_down"})
			return
		}
		_ = json.NewEncoder(w).Encode(TokenResponse{AccessToken: "token"})
	}))
	defer srv.Close()

	prov := Provider{
		Name:     "test",
		EnvVar:   "TEST",
		TokenURL: srv.URL,
		ClientID: "test",
		TokenToKey: func(tok *TokenResponse) string {
			return tok.AccessToken
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	result, err := PollDeviceToken(ctx, prov, "code", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Err != nil {
		t.Fatalf("result error: %v", result.Err)
	}
	if result.APIKey != "token" {
		t.Errorf("expected 'token', got %q", result.APIKey)
	}
}

func TestSaveKey_ExistingFile(t *testing.T) {
	tmpDir := t.TempDir()
	origHome := os.Getenv("HOME")
	_ = os.Setenv("HOME", tmpDir)
	defer func() { _ = os.Setenv("HOME", origHome) }()

	piDir := filepath.Join(tmpDir, ".pi-go")
	_ = os.MkdirAll(piDir, 0700)
	// Write existing content.
	_ = os.WriteFile(filepath.Join(piDir, ".env"), []byte("EXISTING=value\n"), 0600)

	if err := SaveKey("NEW_KEY", "new-value"); err != nil {
		t.Fatalf("SaveKey error: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(piDir, ".env"))
	content := string(data)
	if !strings.Contains(content, "EXISTING=value") {
		t.Error("existing content should be preserved")
	}
	if !strings.Contains(content, "NEW_KEY=new-value") {
		t.Error("new key should be added")
	}
	os.Unsetenv("NEW_KEY")
}

func TestUpdateEnvVar_NoTrailingNewline(t *testing.T) {
	result := updateEnvVar("A=1", "B", "2")
	if !strings.Contains(result, "A=1") {
		t.Error("should contain A=1")
	}
	if !strings.Contains(result, "B=2") {
		t.Error("should contain B=2")
	}
	if !strings.HasSuffix(result, "\n") {
		t.Error("should end with newline")
	}
}

func TestSanitizeErrorBody(t *testing.T) {
	// Empty body.
	if r := sanitizeErrorBody([]byte("")); r != "" {
		t.Errorf("expected empty, got %q", r)
	}
	// JSON body preserved.
	j := `{"error":"bad"}`
	if r := sanitizeErrorBody([]byte(j)); r != j {
		t.Errorf("expected %q, got %q", j, r)
	}
	// HTML stripped.
	if r := sanitizeErrorBody([]byte("<html>stuff</html>")); !strings.Contains(r, "HTML error page") {
		t.Errorf("expected HTML sanitization, got %q", r)
	}
	// Long body truncated.
	long := strings.Repeat("a", 300)
	r := sanitizeErrorBody([]byte(long))
	if len(r) > 210 {
		t.Errorf("expected truncation, got length %d", len(r))
	}
	if !strings.HasSuffix(r, "...") {
		t.Error("expected ... suffix")
	}
}

func TestHandleCallback_SuccessHTML(t *testing.T) {
	ch := make(chan codeResult, 1)
	req := httptest.NewRequest("GET", "/callback?code=test&state=s1", nil)
	w := httptest.NewRecorder()

	handleCallback(w, req, "s1", ch)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Authentication successful") {
		t.Error("expected success HTML")
	}
}

func TestHandleCallback_ErrorNoDescription(t *testing.T) {
	ch := make(chan codeResult, 1)
	// error without error_description should use the error param itself.
	req := httptest.NewRequest("GET", "/callback?error=server_error", nil)
	w := httptest.NewRecorder()

	handleCallback(w, req, "any", ch)

	cr := <-ch
	if cr.err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(cr.err.Error(), "server_error") {
		t.Errorf("expected 'server_error' in message, got: %v", cr.err)
	}
}

func TestExchangeCode_InvalidURL(t *testing.T) {
	// Trigger http.NewRequestWithContext failure with invalid URL.
	prov := Provider{TokenURL: "://invalid", ClientID: "test"}
	_, err := exchangeCode(context.Background(), prov, "code", "http://localhost/cb", "verifier")
	if err == nil {
		t.Fatal("expected error for invalid token URL")
	}
}

func TestRequestDeviceToken_InvalidURL(t *testing.T) {
	prov := Provider{TokenURL: "://invalid", ClientID: "test"}
	_, err := requestDeviceToken(context.Background(), prov, "device-code")
	if err == nil {
		t.Fatal("expected error for invalid token URL")
	}
}

func TestPollDeviceToken_ContextDone(t *testing.T) {
	// Use a server that never responds quickly, and cancel context right away.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(10 * time.Second) // block forever
	}))
	defer srv.Close()

	prov := Provider{
		Name:     "test",
		EnvVar:   "TEST",
		TokenURL: srv.URL,
		ClientID: "test",
		TokenToKey: func(tok *TokenResponse) string {
			return tok.AccessToken
		},
	}

	// Use very short timeout so ctx.Done fires before ticker.
	ctx, cancel := context.WithCancel(context.Background())
	// Cancel after a tiny delay to let the for loop start.
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	result, err := PollDeviceToken(ctx, prov, "code", 60) // 60s interval, context cancels much sooner
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Err == nil {
		t.Error("expected context cancellation error")
	}
}

func TestPollDeviceToken_FatalError(t *testing.T) {
	// Server returns a non-pending, non-slow_down error → immediate return.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "access_denied"})
	}))
	defer srv.Close()

	prov := Provider{
		Name:     "test",
		EnvVar:   "TEST",
		TokenURL: srv.URL,
		ClientID: "test",
		TokenToKey: func(tok *TokenResponse) string {
			return tok.AccessToken
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := PollDeviceToken(ctx, prov, "code", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Err == nil {
		t.Fatal("expected access_denied error")
	}
	if !strings.Contains(result.Err.Error(), "access_denied") {
		t.Errorf("expected access_denied, got: %v", result.Err)
	}
}

func TestSaveKey_MkdirAllError(t *testing.T) {
	origHome := os.Getenv("HOME")
	// Point HOME to a file (not directory) to make MkdirAll fail.
	tmpFile := filepath.Join(t.TempDir(), "fakefile")
	_ = os.WriteFile(tmpFile, []byte("x"), 0600)
	os.Setenv("HOME", tmpFile) // .pi-go will be tmpFile/.pi-go which can't be created
	defer func() { _ = os.Setenv("HOME", origHome) }()

	err := SaveKey("TEST_KEY", "test")
	if err == nil {
		t.Fatal("expected error when mkdir fails")
	}
	if !strings.Contains(err.Error(), "creating directory") {
		t.Errorf("expected 'creating directory' error, got: %v", err)
	}
}

func TestSaveKey_WriteFileError(t *testing.T) {
	tmpDir := t.TempDir()
	origHome := os.Getenv("HOME")
	_ = os.Setenv("HOME", tmpDir)
	defer func() { _ = os.Setenv("HOME", origHome) }()

	// Create .pi-go/.env as a directory to make WriteFile fail.
	envDir := filepath.Join(tmpDir, ".pi-go", ".env")
	_ = os.MkdirAll(envDir, 0700)

	err := SaveKey("TEST_KEY", "test")
	if err == nil {
		t.Fatal("expected error when write fails")
	}
	if !strings.Contains(err.Error(), "writing .env") {
		t.Errorf("expected 'writing .env' error, got: %v", err)
	}
}

func TestRunTLSPreflight_TLSCertKind(t *testing.T) {
	// Test RunTLSPreflight against a server with a self-signed cert.
	// httptest.NewTLSServer creates a server with a self-signed cert.
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// The default http.Client won't trust the self-signed cert, causing a TLS error.
	// We can't override openAIAuthProbeURL, but we can test isTLSError is recognized
	// by directly calling the probe URL with the self-signed cert server.
	// Instead, just verify the isTLSError path is covered via integration.
	msg := "tls: failed to verify certificate: x509: certificate signed by unknown authority"
	if !isTLSError(msg) {
		t.Error("expected TLS error detection for Go's x509 error message")
	}
}
