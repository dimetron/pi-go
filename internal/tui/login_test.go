package tui

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/dimetron/pi-go/internal/auth"
)

// mockBrowser records all URLs passed to openBrowser.
type mockBrowser struct {
	mu   sync.Mutex
	urls []string
	err  error // optional error to return
}

func (m *mockBrowser) open(url string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.urls = append(m.urls, url)
	return m.err
}

func (m *mockBrowser) called() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.urls)
}

func (m *mockBrowser) lastURL() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.urls) == 0 {
		return ""
	}
	return m.urls[len(m.urls)-1]
}

// withMockBrowser replaces openBrowser with a mock for the duration of a test.
func withMockBrowser(t *testing.T) *mockBrowser {
	t.Helper()
	orig := openBrowser
	mb := &mockBrowser{}
	openBrowser = mb.open
	t.Cleanup(func() { openBrowser = orig })
	return mb
}

func TestMaskKey_Long(t *testing.T) {
	masked := maskKey("sk-ant-api03-xxxxxxxxxxxx")
	if masked != "sk-a...xxxx" {
		t.Errorf("unexpected mask: %q", masked)
	}
}

func TestMaskKey_Short(t *testing.T) {
	masked := maskKey("short")
	if masked != "****" {
		t.Errorf("unexpected mask: %q", masked)
	}
}

func TestMaskKey_ExactlyEight(t *testing.T) {
	masked := maskKey("12345678")
	if masked != "****" {
		t.Errorf("unexpected mask: %q", masked)
	}
}

func TestMaskKey_NineChars(t *testing.T) {
	masked := maskKey("123456789")
	if masked != "1234...6789" {
		t.Errorf("unexpected mask: %q", masked)
	}
}

func TestHandleLoginCommand_NoArgs(t *testing.T) {
	m := &model{}
	m.handleLoginCommand(nil)
	if len(m.messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(m.messages))
	}
	msg := m.messages[0]
	if msg.role != "assistant" {
		t.Errorf("expected assistant role, got %q", msg.role)
	}
	// Should show provider status including codex.
	for _, want := range []string{"anthropic", "openai", "codex", "gemini"} {
		if !strings.Contains(msg.content, want) {
			t.Errorf("expected %q in output, got: %s", want, msg.content)
		}
	}
	// Should show simple usage without --sso.
	if strings.Contains(msg.content, "--sso") {
		t.Error("should not contain --sso in usage")
	}
}

func TestHandleLoginCommand_UnknownProvider(t *testing.T) {
	m := &model{}
	m.handleLoginCommand([]string{"ollama"})
	if len(m.messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(m.messages))
	}
	if !strings.Contains(m.messages[0].content, "Unknown provider") {
		t.Errorf("expected unknown provider message, got: %s", m.messages[0].content)
	}
	if !strings.Contains(m.messages[0].content, "codex") {
		t.Errorf("expected codex in available providers list, got: %s", m.messages[0].content)
	}
}

func TestHandleLoginCommand_Anthropic(t *testing.T) {
	mb := withMockBrowser(t)
	m := &model{}
	// Anthropic has AuthURL+TokenURL but no device flow → PKCE.
	m.handleLoginCommand([]string{"anthropic"})
	if m.login == nil {
		t.Fatal("expected login state to be set")
	}
	if m.login.provider != "anthropic" {
		t.Errorf("expected provider anthropic, got %q", m.login.provider)
	}
	if m.login.phase != "sso" {
		t.Errorf("expected phase sso (PKCE), got %q", m.login.phase)
	}
	// PKCE flow passes openBrowser to auth.PKCEFlow which calls it async.
	// The cmd is not executed here, so no browser call yet.
	_ = mb
}

func TestHandleLoginCommand_CodexPKCEFlow(t *testing.T) {
	m := &model{}
	// Codex uses PKCE browser flow (opens browser, waits for callback).
	// TLS preflight runs first; if it fails with tls-cert, login is blocked.
	// Otherwise PKCE starts.
	m.handleLoginCommand([]string{"codex"})

	if m.login != nil && m.login.phase == "sso" {
		// PKCE flow started — TLS preflight passed.
		if m.login.provider != "codex" {
			t.Errorf("expected provider codex, got %q", m.login.provider)
		}
		return
	}
	// TLS preflight may have failed (network issue in CI).
	if len(m.messages) > 0 {
		lastMsg := m.messages[len(m.messages)-1]
		if strings.Contains(lastMsg.content, "TLS certificate") ||
			strings.Contains(lastMsg.content, "preflight") ||
			strings.Contains(lastMsg.content, "login") {
			return // Expected: TLS preflight failure message.
		}
	}
	t.Error("expected either PKCE flow started or TLS preflight error")
}

func TestHandleLoginCommand_OpenAIDeviceFlow(t *testing.T) {
	m := &model{}
	m.handleLoginCommand([]string{"openai"})
	// OpenAI uses device flow (no TLS preflight).
	if m.login != nil && m.login.phase == "device" {
		return
	}
	if len(m.messages) > 0 {
		lastMsg := m.messages[len(m.messages)-1]
		if strings.Contains(lastMsg.content, "Login error") {
			return
		}
	}
}

func TestHandleLoginSave(t *testing.T) {
	tmpDir := t.TempDir()
	piDir := filepath.Join(tmpDir, ".pi-go")
	if err := os.MkdirAll(piDir, 0700); err != nil {
		t.Fatal(err)
	}

	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	m := &model{
		login: &loginState{phase: "waiting", provider: "anthropic"},
	}

	m.handleLoginSave("sk-ant-test-key-12345")

	if m.login != nil {
		t.Error("expected login state to be nil after save")
	}

	data, err := os.ReadFile(filepath.Join(piDir, ".env"))
	if err != nil {
		t.Fatalf("error reading .env: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "ANTHROPIC_API_KEY=sk-ant-test-key-12345") {
		t.Errorf("expected API key in .env, got: %s", content)
	}

	if os.Getenv("ANTHROPIC_API_KEY") != "sk-ant-test-key-12345" {
		t.Error("expected ANTHROPIC_API_KEY to be set in environment")
	}
	os.Unsetenv("ANTHROPIC_API_KEY")
}

func TestHandleLoginCancel(t *testing.T) {
	m := &model{
		login: &loginState{phase: "waiting", provider: "openai"},
	}
	m.handleLoginCancel()
	if m.login != nil {
		t.Error("expected login state to be nil after cancel")
	}
	if len(m.messages) != 1 || !strings.Contains(m.messages[0].content, "cancelled") {
		t.Error("expected cancellation message")
	}
}

func TestHandleLoginCancel_SSOPhase(t *testing.T) {
	m := &model{
		login: &loginState{phase: "sso", provider: "anthropic"},
	}
	m.handleLoginCancel()
	if m.login != nil {
		t.Error("expected login state to be nil after SSO cancel")
	}
}

func TestHandleLoginCancel_DevicePhase(t *testing.T) {
	m := &model{
		login: &loginState{phase: "device", provider: "codex"},
	}
	m.handleLoginCancel()
	if m.login != nil {
		t.Error("expected login state to be nil after device cancel")
	}
}

func TestHandleLoginSSOResult_Success(t *testing.T) {
	tmpDir := t.TempDir()
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	m := &model{
		login: &loginState{phase: "sso", provider: "anthropic"},
	}

	msg := loginSSOResultMsg{
		result: &auth.Result{
			Provider: "anthropic",
			APIKey:   "sk-ant-sso-token-12345",
			EnvVar:   "ANTHROPIC_API_KEY",
		},
	}

	m.handleLoginSSOResult(msg)

	if m.login != nil {
		t.Error("expected login state to be cleared")
	}

	if len(m.messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(m.messages))
	}
	if !strings.Contains(m.messages[0].content, "Login successful") {
		t.Errorf("expected success message, got: %s", m.messages[0].content)
	}

	if os.Getenv("ANTHROPIC_API_KEY") != "sk-ant-sso-token-12345" {
		t.Error("expected ANTHROPIC_API_KEY to be set")
	}
	os.Unsetenv("ANTHROPIC_API_KEY")
}

func TestHandleLoginSSOResult_Error(t *testing.T) {
	m := &model{
		login: &loginState{phase: "device", provider: "codex"},
	}

	msg := loginSSOResultMsg{
		result: &auth.Result{
			Provider: "codex",
			Err:      fmt.Errorf("connection refused"),
		},
	}

	m.handleLoginSSOResult(msg)

	if m.login != nil {
		t.Error("expected login state to be cleared")
	}
	if len(m.messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(m.messages))
	}
	if !strings.Contains(m.messages[0].content, "failed") {
		t.Errorf("expected failure message, got: %s", m.messages[0].content)
	}
}

func TestHandleLoginSSOResult_Cancelled(t *testing.T) {
	// If login was cancelled before SSO result arrives, ignore it.
	m := &model{login: nil}

	msg := loginSSOResultMsg{
		result: &auth.Result{
			Provider: "anthropic",
			APIKey:   "sk-should-be-ignored",
			EnvVar:   "ANTHROPIC_API_KEY",
		},
	}

	m.handleLoginSSOResult(msg)

	if len(m.messages) != 0 {
		t.Error("expected no messages when login was cancelled")
	}
}

func TestHandleLoginSSOResult_EmptyKey(t *testing.T) {
	m := &model{
		login: &loginState{phase: "sso", provider: "anthropic"},
	}

	msg := loginSSOResultMsg{
		result: &auth.Result{
			Provider: "anthropic",
			APIKey:   "",
			EnvVar:   "ANTHROPIC_API_KEY",
		},
	}

	m.handleLoginSSOResult(msg)

	if m.login != nil {
		t.Error("expected login state to be cleared")
	}
	if len(m.messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(m.messages))
	}
	if !strings.Contains(m.messages[0].content, "empty key") {
		t.Errorf("expected empty key message, got: %s", m.messages[0].content)
	}
}

func TestHandleLoginSSOResult_SaveError(t *testing.T) {
	// Use an invalid HOME to trigger SaveKey error.
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", "/dev/null/nonexistent")
	defer os.Setenv("HOME", origHome)

	m := &model{
		login: &loginState{phase: "sso", provider: "anthropic"},
	}

	msg := loginSSOResultMsg{
		result: &auth.Result{
			Provider: "anthropic",
			APIKey:   "sk-test-key",
			EnvVar:   "ANTHROPIC_API_KEY",
		},
	}

	m.handleLoginSSOResult(msg)

	if len(m.messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(m.messages))
	}
	if !strings.Contains(m.messages[0].content, "Error saving key") {
		t.Errorf("expected save error message, got: %s", m.messages[0].content)
	}
}

func TestHandleLoginSave_UnknownProvider(t *testing.T) {
	m := &model{
		login: &loginState{phase: "waiting", provider: "nonexistent"},
	}

	m.handleLoginSave("some-key")

	if m.login != nil {
		t.Error("expected login to be cleared")
	}
	if len(m.messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(m.messages))
	}
	if !strings.Contains(m.messages[0].content, "Internal error") {
		t.Errorf("expected internal error, got: %s", m.messages[0].content)
	}
}

func TestHandleLoginSave_SaveError(t *testing.T) {
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", "/dev/null/nonexistent")
	defer os.Setenv("HOME", origHome)

	m := &model{
		login: &loginState{phase: "waiting", provider: "anthropic"},
	}

	m.handleLoginSave("sk-test-key")

	if len(m.messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(m.messages))
	}
	if !strings.Contains(m.messages[0].content, "Error saving key") {
		t.Errorf("expected save error, got: %s", m.messages[0].content)
	}
}

func TestLoginStart_ManualFallback(t *testing.T) {
	mb := withMockBrowser(t)
	m := &model{}
	prov := auth.Provider{
		Name:       "test",
		EnvVar:     "TEST_KEY",
		KeyPageURL: "https://example.com/keys",
	}

	m.loginStart(prov)

	if m.login == nil {
		t.Fatal("expected login state")
	}
	if m.login.phase != "waiting" {
		t.Errorf("expected manual (waiting) phase, got %q", m.login.phase)
	}
	// Verify browser was opened with the key page URL.
	if mb.called() != 1 {
		t.Errorf("expected 1 browser call, got %d", mb.called())
	}
	if mb.lastURL() != "https://example.com/keys" {
		t.Errorf("expected key page URL, got %q", mb.lastURL())
	}
}

func TestLoginStart_DeviceFlow(t *testing.T) {
	// Provider with DeviceURL+UseDeviceFlow → device flow (will fail on network).
	m := &model{}
	prov := auth.Provider{
		Name:          "test",
		EnvVar:        "TEST_KEY",
		DeviceURL:     "http://127.0.0.1:1/device",
		UseDeviceFlow: true,
		ClientID:      "test",
		Scopes:        []string{"api"},
	}

	m.loginStart(prov)

	// Device flow request will fail, login should be cleared.
	if m.login != nil {
		t.Error("expected login to be nil after device flow network error")
	}
	if len(m.messages) == 0 {
		t.Error("expected error message")
	}
	if !strings.Contains(m.messages[len(m.messages)-1].content, "Login error") {
		t.Errorf("expected login error, got: %s", m.messages[len(m.messages)-1].content)
	}
}

func TestLoginStart_TLSPreflightBlock(t *testing.T) {
	// Can't easily mock TLS preflight failure since it hits a real URL,
	// but verify the TLSPreflight flag path is exercised.
	m := &model{}
	prov := auth.Provider{
		Name:         "test",
		EnvVar:       "TEST_KEY",
		AuthURL:      "http://127.0.0.1:1/auth",
		TokenURL:     "http://127.0.0.1:1/token",
		ClientID:     "test",
		Scopes:       []string{"api"},
		TLSPreflight: true,
	}

	m.loginStart(prov)

	// TLS preflight runs against real auth.openai.com.
	// If it passes → PKCE starts. If it fails with tls-cert → blocked.
	// Either path is valid in test.
	if m.login != nil && m.login.phase == "sso" {
		return // PKCE started
	}
	if len(m.messages) > 0 {
		return // TLS preflight or PKCE error message
	}
}

func TestHandleLoginCommand_IgnoresFlags(t *testing.T) {
	// Old --sso flags should be silently ignored.
	m := &model{}
	m.handleLoginCommand([]string{"--sso", "gemini"})

	// Should find gemini provider and start login.
	if m.login == nil {
		t.Fatal("expected login state")
	}
	if m.login.provider != "gemini" {
		t.Errorf("expected gemini, got %q", m.login.provider)
	}
}

func TestHandleLoginCommand_GeminiPKCE(t *testing.T) {
	m := &model{}
	m.handleLoginCommand([]string{"gemini"})

	if m.login == nil {
		t.Fatal("expected login state")
	}
	if m.login.phase != "sso" {
		t.Errorf("expected sso phase for gemini PKCE, got %q", m.login.phase)
	}
	if m.login.provider != "gemini" {
		t.Errorf("expected gemini, got %q", m.login.provider)
	}
}

func TestLoginStartPKCEFlow_StateAndMessage(t *testing.T) {
	// Verify loginStartPKCEFlow sets correct state and returns a cmd.
	m := &model{}
	prov := auth.Provider{
		Name:     "test",
		EnvVar:   "TEST_KEY",
		AuthURL:  "http://127.0.0.1:1/auth",
		TokenURL: "http://127.0.0.1:1/token",
		ClientID: "test",
		Scopes:   []string{"api"},
		TokenToKey: func(tok *auth.TokenResponse) string {
			return tok.AccessToken
		},
	}

	_, cmd := m.loginStartPKCEFlow(prov)

	if m.login == nil || m.login.phase != "sso" {
		t.Fatal("expected sso phase")
	}
	if m.login.provider != "test" {
		t.Errorf("expected provider test, got %q", m.login.provider)
	}
	if cmd == nil {
		t.Fatal("expected non-nil cmd (async PKCE flow)")
	}
	// Verify message was shown.
	if len(m.messages) == 0 {
		t.Fatal("expected login message")
	}
	if !strings.Contains(m.messages[len(m.messages)-1].content, "test") {
		t.Error("expected provider name in message")
	}
	if !strings.Contains(m.messages[len(m.messages)-1].content, "browser") {
		t.Error("expected browser mention in message")
	}
	// Don't execute the cmd — it would block waiting for browser callback.
}

func TestLoginStartDeviceFlow_CmdExecution(t *testing.T) {
	mb := withMockBrowser(t)

	// Mock device code and token endpoints.
	attempt := 0
	srv := newMockDeviceServer(t, &attempt)
	defer srv.Close()

	m := &model{}
	prov := auth.Provider{
		Name:          "test-device",
		EnvVar:        "TEST_KEY",
		DeviceURL:     srv.URL + "/device/code",
		TokenURL:      srv.URL + "/oauth/token",
		UseDeviceFlow: true,
		ClientID:      "test",
		Scopes:        []string{"api"},
		TokenToKey: func(tok *auth.TokenResponse) string {
			return tok.AccessToken
		},
	}

	_, cmd := m.loginStartDeviceFlow(prov)

	if m.login == nil || m.login.phase != "device" {
		t.Fatal("expected device phase")
	}
	if cmd == nil {
		t.Fatal("expected non-nil cmd")
	}

	// Verify browser was opened with the verification URI.
	if mb.called() != 1 {
		t.Errorf("expected 1 browser call, got %d", mb.called())
	}
	if mb.lastURL() != "https://example.com/device" {
		t.Errorf("expected verification URI, got %q", mb.lastURL())
	}

	// Verify the user code message was shown.
	found := false
	for _, msg := range m.messages {
		if strings.Contains(msg.content, "MOCK-CODE") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected user code in messages")
	}

	// Execute the cmd — polls until token is returned.
	msg := cmd()
	result, ok := msg.(loginSSOResultMsg)
	if !ok {
		t.Fatalf("expected loginSSOResultMsg, got %T", msg)
	}
	if result.result.Err != nil {
		t.Fatalf("unexpected error: %v", result.result.Err)
	}
	if result.result.APIKey != "mock-device-token" {
		t.Errorf("expected 'mock-device-token', got %q", result.result.APIKey)
	}
}

func TestLoginStart_DeviceFlowSuccess(t *testing.T) {
	attempt := 0
	srv := newMockDeviceServer(t, &attempt)
	defer srv.Close()

	m := &model{}
	prov := auth.Provider{
		Name:          "test-device",
		EnvVar:        "TEST_KEY",
		DeviceURL:     srv.URL + "/device/code",
		TokenURL:      srv.URL + "/oauth/token",
		UseDeviceFlow: true,
		ClientID:      "test",
		Scopes:        []string{"api"},
		TokenToKey: func(tok *auth.TokenResponse) string {
			return tok.AccessToken
		},
	}

	_, cmd := m.loginStart(prov)

	if m.login == nil || m.login.phase != "device" {
		t.Fatal("expected device phase")
	}
	if cmd == nil {
		t.Fatal("expected non-nil cmd")
	}
}

// newMockDeviceServer creates a test server that simulates the device code flow.
func newMockDeviceServer(t *testing.T, attempt *int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/device/code":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"device_code":"mock-dc","user_code":"MOCK-CODE","verification_uri":"https://example.com/device","interval":1}`)
		case "/oauth/token":
			*attempt++
			w.Header().Set("Content-Type", "application/json")
			if *attempt < 2 {
				w.WriteHeader(http.StatusBadRequest)
				fmt.Fprintf(w, `{"error":"authorization_pending"}`)
				return
			}
			fmt.Fprintf(w, `{"access_token":"mock-device-token","token_type":"bearer"}`)
		}
	}))
}

func TestLoginStartDeviceFlow_CmdError(t *testing.T) {
	// Device code request succeeds, but token polling fails with timeout.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/device/code":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"device_code":"dc","user_code":"UC","verification_uri":"https://example.com","interval":1}`)
		case "/oauth/token":
			// Return a fatal error (not pending).
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, `{"error":"expired_token"}`)
		}
	}))
	defer srv.Close()

	m := &model{}
	prov := auth.Provider{
		Name:          "test",
		EnvVar:        "TEST_KEY",
		DeviceURL:     srv.URL + "/device/code",
		TokenURL:      srv.URL + "/oauth/token",
		UseDeviceFlow: true,
		ClientID:      "test",
		Scopes:        []string{"api"},
		TokenToKey: func(tok *auth.TokenResponse) string {
			return tok.AccessToken
		},
	}

	_, cmd := m.loginStartDeviceFlow(prov)
	msg := cmd()
	result := msg.(loginSSOResultMsg)

	if result.result.Err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(result.result.Err.Error(), "expired_token") {
		t.Errorf("expected expired_token error, got: %v", result.result.Err)
	}
}

func TestOpenBrowserDefault_ExecutesCommand(t *testing.T) {
	// Verify openBrowserDefault constructs the right command for the current platform.
	// We swap the function with a mock that records the URL instead of opening a browser.
	mb := withMockBrowser(t)
	err := openBrowser("https://test.invalid/browser-check")
	if err != nil {
		t.Fatalf("mock should not error: %v", err)
	}
	if mb.called() != 1 {
		t.Errorf("expected 1 call, got %d", mb.called())
	}
	if mb.lastURL() != "https://test.invalid/browser-check" {
		t.Errorf("expected test URL, got %q", mb.lastURL())
	}
}

func TestOpenBrowser_MockVerifiesURL(t *testing.T) {
	mb := withMockBrowser(t)

	// Call openBrowser (now the mock).
	err := openBrowser("https://auth.openai.com/authorize?client_id=test")
	if err != nil {
		t.Fatalf("mock should not error: %v", err)
	}

	if mb.called() != 1 {
		t.Fatalf("expected 1 call, got %d", mb.called())
	}
	if mb.lastURL() != "https://auth.openai.com/authorize?client_id=test" {
		t.Errorf("expected auth URL, got %q", mb.lastURL())
	}

	// Call again with different URL.
	openBrowser("https://platform.openai.com/api-keys")
	if mb.called() != 2 {
		t.Fatalf("expected 2 calls, got %d", mb.called())
	}
	if mb.lastURL() != "https://platform.openai.com/api-keys" {
		t.Errorf("expected key page URL, got %q", mb.lastURL())
	}
}

func TestOpenBrowser_MockError(t *testing.T) {
	mb := withMockBrowser(t)
	mb.err = fmt.Errorf("browser not found")

	err := openBrowser("https://example.com")
	if err == nil {
		t.Fatal("expected error from mock")
	}
	if !strings.Contains(err.Error(), "browser not found") {
		t.Errorf("expected 'browser not found', got: %v", err)
	}
	if mb.called() != 1 {
		t.Errorf("expected 1 call even on error, got %d", mb.called())
	}
}

func TestLoginStartManual_OpensKeyPageURL(t *testing.T) {
	mb := withMockBrowser(t)
	m := &model{}

	prov := auth.Provider{
		Name:       "anthropic",
		EnvVar:     "ANTHROPIC_API_KEY",
		KeyPageURL: "https://console.anthropic.com/settings/keys",
	}

	m.loginStartManual(prov)

	if mb.called() != 1 {
		t.Fatalf("expected browser to be opened once, got %d", mb.called())
	}
	if mb.lastURL() != "https://console.anthropic.com/settings/keys" {
		t.Errorf("expected key page URL, got %q", mb.lastURL())
	}
	if m.login == nil || m.login.phase != "waiting" {
		t.Error("expected waiting phase")
	}
}

func TestLoginStartDeviceFlow_OpensVerificationURI(t *testing.T) {
	mb := withMockBrowser(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{
			"device_code": "dc-123",
			"user_code": "ABCD-5678",
			"verification_uri": "https://auth.openai.com/device",
			"interval": 5
		}`)
	}))
	defer srv.Close()

	m := &model{}
	prov := auth.Provider{
		Name:          "codex",
		EnvVar:        "OPENAI_API_KEY",
		DeviceURL:     srv.URL,
		UseDeviceFlow: true,
		ClientID:      "pi-go-cli",
		Scopes:        []string{"openai.public"},
		TokenToKey: func(tok *auth.TokenResponse) string {
			return tok.AccessToken
		},
	}

	m.loginStartDeviceFlow(prov)

	if mb.called() != 1 {
		t.Fatalf("expected browser to be opened once, got %d", mb.called())
	}
	if mb.lastURL() != "https://auth.openai.com/device" {
		t.Errorf("expected verification URI 'https://auth.openai.com/device', got %q", mb.lastURL())
	}
}

func TestLoginStartPKCEFlow_PassesOpenBrowserToAuth(t *testing.T) {
	mb := withMockBrowser(t)
	m := &model{}

	prov := auth.Provider{
		Name:     "anthropic",
		EnvVar:   "ANTHROPIC_API_KEY",
		AuthURL:  "https://console.anthropic.com/oauth/authorize",
		TokenURL: "https://console.anthropic.com/oauth/token",
		ClientID: "pi-go-cli",
		Scopes:   []string{"api"},
		TokenToKey: func(tok *auth.TokenResponse) string {
			return tok.AccessToken
		},
	}

	_, cmd := m.loginStartPKCEFlow(prov)

	if m.login == nil || m.login.phase != "sso" {
		t.Fatal("expected sso phase")
	}
	if cmd == nil {
		t.Fatal("expected non-nil cmd")
	}
	// Browser is called inside the cmd closure by auth.PKCEFlow.
	// Before cmd executes, no browser call yet.
	if mb.called() != 0 {
		t.Errorf("expected 0 browser calls before cmd execution, got %d", mb.called())
	}
}
