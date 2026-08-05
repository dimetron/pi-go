package tui

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/dimetron/pi-go/internal/auth"
	"github.com/dimetron/pi-go/internal/logger"
)

// newMockCodexDeviceServer stands in for auth.openai.com's device endpoints.
// approvalsBeforeOK controls how many polls answer 403 ("not approved yet")
// before the approval lands, which is the only signal that path gets.
func newMockCodexDeviceServer(t *testing.T, approvalsBeforeOK int) *httptest.Server {
	t.Helper()
	polls := 0
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/deviceauth/usercode":
			// The interval arrives as a JSON string upstream, so exercise that
			// encoding rather than the more obvious number.
			fmt.Fprint(w, `{"device_auth_id":"dev-1","user_code":"WXYZ-1234","interval":"1"}`)
		case "/deviceauth/token":
			polls++
			if polls <= approvalsBeforeOK {
				w.WriteHeader(http.StatusForbidden)
				fmt.Fprint(w, `{"error":"pending"}`)
				return
			}
			fmt.Fprint(w, `{"authorization_code":"auth-code-1","code_verifier":"verifier-1"}`)
		case "/oauth/token":
			fmt.Fprint(w, `{"access_token":"codex-device-token","token_type":"bearer"}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

// codexDeviceProvider wires a Provider at a mock server.
func codexDeviceProvider(srvURL string) auth.Provider {
	return auth.Provider{
		Name:              "codex",
		EnvVar:            "OPENAI_API_KEY",
		DeviceURL:         srvURL + "/deviceauth",
		TokenURL:          srvURL + "/oauth/token",
		ClientID:          "pi-go-cli",
		CodexDeviceAuth:   true,
		DeviceVerifyURL:   "https://auth.openai.com/codex/device",
		DeviceRedirectURI: "http://localhost/callback",
		TokenToKey:        func(tok *auth.TokenResponse) string { return tok.AccessToken },
	}
}

// loggedModel returns a model wired to a real session log in an isolated HOME,
// plus a func returning the log's contents. The async login commands take a
// separate branch when a logger is configured, and that branch carries the only
// record of what a failed authentication did.
func loggedModel(t *testing.T) (*model, func() string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())

	log, err := logger.New()
	if err != nil {
		t.Fatalf("logger.New() error = %v", err)
	}
	t.Cleanup(func() { _ = log.Close() })

	return &model{cfg: Config{Logger: log}}, func() string {
		t.Helper()
		if err := log.Close(); err != nil {
			t.Fatalf("closing log: %v", err)
		}
		contents, err := os.ReadFile(log.Path())
		if err != nil {
			t.Fatalf("reading log: %v", err)
		}
		return string(contents)
	}
}

// The device code has to reach the chat pane before polling starts, or the
// user is left staring at a spinner with no code to enter.
func TestLoginStartCodexDeviceFlow_PromptsThenPolls(t *testing.T) {
	mb := withMockBrowser(t)
	srv := newMockCodexDeviceServer(t, 1)
	defer srv.Close()

	m, logContents := loggedModel(t)
	_, cmd := m.loginStartCodexDeviceFlow(codexDeviceProvider(srv.URL))

	if m.login == nil || m.login.phase != "device" {
		t.Fatalf("login state = %+v, want device phase", m.login)
	}
	if cmd == nil {
		t.Fatal("expected a polling command")
	}

	last := m.chatModel.Messages[len(m.chatModel.Messages)-1].content
	if !strings.Contains(last, "WXYZ-1234") {
		t.Errorf("prompt is missing the user code, got: %s", last)
	}
	// The verification URL is the one the user opens; the device endpoint is
	// pi's own business and must not be shown.
	if !strings.Contains(last, "https://auth.openai.com/codex/device") {
		t.Errorf("prompt is missing the verification URL, got: %s", last)
	}
	if mb.lastURL() != "https://auth.openai.com/codex/device" {
		t.Errorf("browser opened %q, want the verification URL", mb.lastURL())
	}

	msg := cmd()
	result, ok := msg.(loginSSOResultMsg)
	if !ok {
		t.Fatalf("cmd returned %T, want loginSSOResultMsg", msg)
	}
	if result.result.Err != nil {
		t.Fatalf("device auth failed: %v", result.result.Err)
	}
	if result.result.APIKey != "codex-device-token" {
		t.Errorf("APIKey = %q, want codex-device-token", result.result.APIKey)
	}

	if got := logContents(); !strings.Contains(got, "codex device auth ok") {
		t.Errorf("session log does not record the successful device auth:\n%s", got)
	}
}

// A failed start must clear the login state, or the TUI stays wedged in a
// device phase that nothing will ever complete.
func TestLoginStartCodexDeviceFlow_StartFailureClearsState(t *testing.T) {
	withMockBrowser(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"error":"boom"}`)
	}))
	defer srv.Close()

	m := &model{}
	_, cmd := m.loginStartCodexDeviceFlow(codexDeviceProvider(srv.URL))

	if m.login != nil {
		t.Errorf("login state = %+v, want nil after a failed start", m.login)
	}
	if cmd != nil {
		t.Error("expected no polling command after a failed start")
	}
	if len(m.chatModel.Messages) == 0 {
		t.Fatal("expected an error message")
	}
	if last := m.chatModel.Messages[len(m.chatModel.Messages)-1].content; !strings.Contains(last, "Login error") {
		t.Errorf("message = %q, want a login error", last)
	}
}

// A device auth that fails during polling still has to produce a result the
// TUI can render, rather than leaving the command hanging.
func TestLoginStartCodexDeviceFlow_PollFailureReportsResult(t *testing.T) {
	withMockBrowser(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/deviceauth/usercode":
			fmt.Fprint(w, `{"device_auth_id":"dev-1","user_code":"WXYZ-1234","interval":"1"}`)
		default:
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":"device_code_expired"}`)
		}
	}))
	defer srv.Close()

	m, logContents := loggedModel(t)
	_, cmd := m.loginStartCodexDeviceFlow(codexDeviceProvider(srv.URL))
	if cmd == nil {
		t.Fatal("expected a polling command")
	}

	result, ok := cmd().(loginSSOResultMsg)
	if !ok {
		t.Fatal("cmd did not return a loginSSOResultMsg")
	}
	if result.result.Err == nil {
		t.Fatal("expected the poll failure to be reported")
	}
	if !strings.Contains(result.result.Err.Error(), "device_code_expired") {
		t.Errorf("error = %v, want it to name the server's reason", result.result.Err)
	}
	if got := logContents(); !strings.Contains(got, "codex device auth failed") {
		t.Errorf("session log does not record the failure:\n%s", got)
	}
}

// manualCodeProvider wires an Anthropic-style paste-the-code provider.
func manualCodeProvider(srvURL string) auth.Provider {
	return auth.Provider{
		Name:              "manual",
		EnvVar:            "MANUAL_API_KEY",
		AuthURL:           srvURL + "/oauth/authorize",
		TokenURL:          srvURL + "/oauth/token",
		ClientID:          "pi-go-cli",
		Scopes:            []string{"api"},
		ManualCode:        true,
		ManualRedirectURI: "https://example.invalid/callback",
		TokenToKey:        func(tok *auth.TokenResponse) string { return tok.AccessToken },
	}
}

func TestLoginStartManualCode_PromptsForPaste(t *testing.T) {
	mb := withMockBrowser(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"access_token":"manual-token","token_type":"bearer"}`)
	}))
	defer srv.Close()

	m := &model{}
	m.loginStartManualCode(manualCodeProvider(srv.URL))

	if m.login == nil || m.login.phase != "manual-code" {
		t.Fatalf("login state = %+v, want manual-code phase", m.login)
	}
	if m.login.manualCode == nil {
		t.Fatal("manual-code session was not retained; the paste would have nothing to exchange")
	}
	if mb.called() != 1 {
		t.Errorf("browser called %d times, want 1", mb.called())
	}
	if last := m.chatModel.Messages[len(m.chatModel.Messages)-1].content; !strings.Contains(last, "redirect URL") {
		t.Errorf("prompt does not explain what to paste, got: %s", last)
	}
}

// A provider not configured for the manual-code flow must surface the error
// rather than leave the user at a prompt with no session behind it.
func TestLoginStartManualCode_StartFailureIsReported(t *testing.T) {
	withMockBrowser(t)
	m := &model{}

	// ManualCode is set but ManualRedirectURI is not, which StartManualCodeFlow
	// rejects.
	m.loginStartManualCode(auth.Provider{Name: "broken", ManualCode: true})

	if m.login != nil {
		t.Errorf("login state = %+v, want nil", m.login)
	}
	if len(m.chatModel.Messages) == 0 {
		t.Fatal("expected an error message")
	}
	if last := m.chatModel.Messages[len(m.chatModel.Messages)-1].content; !strings.Contains(last, "Login error") {
		t.Errorf("message = %q, want a login error", last)
	}
}

func TestHandleLoginCodeSubmit_ExchangesPastedCode(t *testing.T) {
	withMockBrowser(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"access_token":"manual-token","token_type":"bearer"}`)
	}))
	defer srv.Close()

	sess, err := auth.StartManualCodeFlow(manualCodeProvider(srv.URL))
	if err != nil {
		t.Fatalf("StartManualCodeFlow() error = %v", err)
	}

	m, logContents := loggedModel(t)
	m.login = &loginState{phase: "manual-code", provider: "manual", manualCode: sess}
	_, cmd := m.handleLoginCodeSubmit("auth-code-123")
	if cmd == nil {
		t.Fatal("expected an exchange command")
	}
	if last := m.chatModel.Messages[len(m.chatModel.Messages)-1].content; !strings.Contains(last, "Exchanging code") {
		t.Errorf("message = %q, want progress feedback", last)
	}

	result, ok := cmd().(loginSSOResultMsg)
	if !ok {
		t.Fatal("cmd did not return a loginSSOResultMsg")
	}
	if result.result.Err != nil {
		t.Fatalf("exchange failed: %v", result.result.Err)
	}
	if result.result.APIKey != "manual-token" {
		t.Errorf("APIKey = %q, want manual-token", result.result.APIKey)
	}
	if result.result.EnvVar != "MANUAL_API_KEY" {
		t.Errorf("EnvVar = %q, want MANUAL_API_KEY", result.result.EnvVar)
	}
	if got := logContents(); !strings.Contains(got, "manual-code exchange ok") {
		t.Errorf("session log does not record the exchange:\n%s", got)
	}
}

// A paste with no code in it must come back as a result the TUI renders, not
// as a silent no-op.
func TestHandleLoginCodeSubmit_UnusablePasteIsReported(t *testing.T) {
	withMockBrowser(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"access_token":"unused","token_type":"bearer"}`)
	}))
	defer srv.Close()

	sess, err := auth.StartManualCodeFlow(manualCodeProvider(srv.URL))
	if err != nil {
		t.Fatalf("StartManualCodeFlow() error = %v", err)
	}

	m, logContents := loggedModel(t)
	m.login = &loginState{phase: "manual-code", provider: "manual", manualCode: sess}
	_, cmd := m.handleLoginCodeSubmit("")

	result, ok := cmd().(loginSSOResultMsg)
	if !ok {
		t.Fatal("cmd did not return a loginSSOResultMsg")
	}
	if result.result.Err == nil {
		t.Fatal("expected an error for a paste with no authorization code")
	}
	if got := logContents(); !strings.Contains(got, "manual-code exchange aborted") {
		t.Errorf("session log does not record the aborted exchange:\n%s", got)
	}
}

func TestHandleLoginCodeSubmit_WithoutSession(t *testing.T) {
	m := &model{login: &loginState{phase: "manual-code", provider: "manual"}}
	_, cmd := m.handleLoginCodeSubmit("anything")

	if cmd != nil {
		t.Error("expected no command when there is no session to exchange against")
	}
	if m.login != nil {
		t.Errorf("login state = %+v, want nil", m.login)
	}
	if last := m.chatModel.Messages[len(m.chatModel.Messages)-1].content; !strings.Contains(last, "Internal error") {
		t.Errorf("message = %q, want an internal error", last)
	}
}

// TestLoginStartPKCEFlow_CompletesViaCallback drives the whole PKCE round trip:
// the mock browser plays the user, fetching the callback URL that the real
// browser would have been redirected to.
func TestLoginStartPKCEFlow_CompletesViaCallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"access_token":"pkce-token","token_type":"bearer"}`)
	}))
	defer srv.Close()

	orig := openBrowser
	t.Cleanup(func() { openBrowser = orig })
	// PKCEFlow builds the redirect URI from the port its listener actually got,
	// so the callback address is only knowable from the authorize URL.
	openBrowser = func(authURL string) error {
		u, err := url.Parse(authURL)
		if err != nil {
			return err
		}
		q := u.Query()
		redirect, err := url.Parse(q.Get("redirect_uri"))
		if err != nil {
			return err
		}
		cb := *redirect
		cb.RawQuery = url.Values{
			"code":  {"pkce-code"},
			"state": {q.Get("state")},
		}.Encode()

		resp, err := http.Get(cb.String())
		if err != nil {
			return err
		}
		return resp.Body.Close()
	}

	m, logContents := loggedModel(t)
	_, cmd := m.loginStartPKCEFlow(auth.Provider{
		Name:       "pkce",
		EnvVar:     "PKCE_API_KEY",
		AuthURL:    srv.URL + "/oauth/authorize",
		TokenURL:   srv.URL + "/oauth/token",
		ClientID:   "pi-go-cli",
		Scopes:     []string{"api"},
		TokenToKey: func(tok *auth.TokenResponse) string { return tok.AccessToken },
	})
	if cmd == nil {
		t.Fatal("expected a PKCE command")
	}

	result, ok := cmd().(loginSSOResultMsg)
	if !ok {
		t.Fatal("cmd did not return a loginSSOResultMsg")
	}
	if result.result.Err != nil {
		t.Fatalf("PKCE flow failed: %v", result.result.Err)
	}
	if result.result.APIKey != "pkce-token" {
		t.Errorf("APIKey = %q, want pkce-token", result.result.APIKey)
	}
	if got := logContents(); !strings.Contains(got, "pkce callback/exchange ok") {
		t.Errorf("session log does not record the exchange:\n%s", got)
	}
}

// A PKCE round trip whose token exchange is rejected comes back as a result
// carrying the error, not as a transport failure.
func TestLoginStartPKCEFlow_TokenExchangeFailureIsLogged(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":"invalid_grant"}`)
	}))
	defer srv.Close()

	orig := openBrowser
	t.Cleanup(func() { openBrowser = orig })
	openBrowser = func(authURL string) error {
		u, err := url.Parse(authURL)
		if err != nil {
			return err
		}
		q := u.Query()
		redirect, err := url.Parse(q.Get("redirect_uri"))
		if err != nil {
			return err
		}
		cb := *redirect
		cb.RawQuery = url.Values{"code": {"pkce-code"}, "state": {q.Get("state")}}.Encode()
		resp, err := http.Get(cb.String())
		if err != nil {
			return err
		}
		return resp.Body.Close()
	}

	m, logContents := loggedModel(t)
	_, cmd := m.loginStartPKCEFlow(auth.Provider{
		Name:       "pkce",
		EnvVar:     "PKCE_API_KEY",
		AuthURL:    srv.URL + "/oauth/authorize",
		TokenURL:   srv.URL + "/oauth/token",
		ClientID:   "pi-go-cli",
		TokenToKey: func(tok *auth.TokenResponse) string { return tok.AccessToken },
	})

	result, ok := cmd().(loginSSOResultMsg)
	if !ok {
		t.Fatal("cmd did not return a loginSSOResultMsg")
	}
	if result.result.Err == nil {
		t.Fatal("expected the rejected token exchange to be reported")
	}
	if got := logContents(); !strings.Contains(got, "pkce callback/exchange failed") {
		t.Errorf("session log does not record the failed exchange:\n%s", got)
	}
}

// A browser that cannot be opened aborts PKCE, since nothing will ever reach
// the callback — the failure has to come back as a renderable result.
func TestLoginStartPKCEFlow_BrowserFailureIsReported(t *testing.T) {
	mb := withMockBrowser(t)
	mb.err = fmt.Errorf("no browser handler")

	m, logContents := loggedModel(t)
	_, cmd := m.loginStartPKCEFlow(auth.Provider{
		Name:       "pkce",
		EnvVar:     "PKCE_API_KEY",
		AuthURL:    "http://127.0.0.1:1/authorize",
		TokenURL:   "http://127.0.0.1:1/token",
		ClientID:   "pi-go-cli",
		TokenToKey: func(tok *auth.TokenResponse) string { return tok.AccessToken },
	})

	result, ok := cmd().(loginSSOResultMsg)
	if !ok {
		t.Fatal("cmd did not return a loginSSOResultMsg")
	}
	if result.result.Err == nil {
		t.Fatal("expected the browser failure to be reported")
	}
	if got := logContents(); !strings.Contains(got, "pkce flow aborted") {
		t.Errorf("session log does not record the aborted flow:\n%s", got)
	}
}

// The RFC 8628 device flow is the other headless path; its async poll takes a
// separate logging branch from the codex variant.
func TestLoginStartDeviceFlow_LogsPollOutcome(t *testing.T) {
	withMockBrowser(t)
	attempt := 0
	srv := newMockDeviceServer(t, &attempt)
	defer srv.Close()

	m, logContents := loggedModel(t)
	_, cmd := m.loginStartDeviceFlow(auth.Provider{
		Name:          "test-device",
		EnvVar:        "TEST_KEY",
		DeviceURL:     srv.URL + "/device/code",
		TokenURL:      srv.URL + "/oauth/token",
		UseDeviceFlow: true,
		ClientID:      "test",
		Scopes:        []string{"api"},
		TokenToKey:    func(tok *auth.TokenResponse) string { return tok.AccessToken },
	})
	if cmd == nil {
		t.Fatal("expected a polling command")
	}

	result, ok := cmd().(loginSSOResultMsg)
	if !ok {
		t.Fatal("cmd did not return a loginSSOResultMsg")
	}
	if result.result.Err != nil {
		t.Fatalf("device flow failed: %v", result.result.Err)
	}

	got := logContents()
	if !strings.Contains(got, "device flow prompt") {
		t.Errorf("session log does not record the prompt:\n%s", got)
	}
	if !strings.Contains(got, "device flow ok") {
		t.Errorf("session log does not record the successful poll:\n%s", got)
	}
}

// The Codex variant renames the prompt; the wording is what tells the user
// which credential they are about to hand over.
func TestLoginStartPKCEFlow_CodexOAuthWording(t *testing.T) {
	withMockBrowser(t)
	m := &model{}
	m.loginStartPKCEFlow(auth.Provider{
		Name:       "codex",
		EnvVar:     "OPENAI_API_KEY",
		AuthURL:    "http://127.0.0.1:1/authorize",
		TokenURL:   "http://127.0.0.1:1/token",
		ClientID:   "pi-go-cli",
		CodexOAuth: true,
		TokenToKey: func(tok *auth.TokenResponse) string { return tok.AccessToken },
	})

	last := m.chatModel.Messages[len(m.chatModel.Messages)-1].content
	if !strings.Contains(last, "codex OAuth") {
		t.Errorf("message = %q, want it to name Codex OAuth", last)
	}
}

// loginStart routes on provider capability; the codex device path is the one
// that exists so a dev container with no browser and no callback can log in.
func TestLoginStart_RoutesCodexDeviceAuth(t *testing.T) {
	withMockBrowser(t)
	srv := newMockCodexDeviceServer(t, 0)
	defer srv.Close()

	m := &model{}
	m.loginStart(codexDeviceProvider(srv.URL))

	if m.login == nil || m.login.phase != "device" {
		t.Fatalf("login state = %+v, want the device phase", m.login)
	}
	if last := m.chatModel.Messages[len(m.chatModel.Messages)-1].content; !strings.Contains(last, "WXYZ-1234") {
		t.Errorf("message = %q, want the device code", last)
	}
}

func TestLoginStart_RoutesManualCode(t *testing.T) {
	withMockBrowser(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer srv.Close()

	m := &model{}
	m.loginStart(manualCodeProvider(srv.URL))

	if m.login == nil || m.login.phase != "manual-code" {
		t.Fatalf("login state = %+v, want the manual-code phase", m.login)
	}
}

// Login events are the only trace a failed authentication leaves, so they have
// to reach the session log when one is configured.
func TestLogLogin_WritesToTheSessionLog(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	log, err := logger.New()
	if err != nil {
		t.Fatalf("logger.New() error = %v", err)
	}

	m := &model{cfg: Config{Logger: log}}
	m.logLogin("selected %s flow for %s", "device", "codex")

	if err := log.Close(); err != nil {
		t.Fatalf("closing log: %v", err)
	}
	contents, err := os.ReadFile(log.Path())
	if err != nil {
		t.Fatalf("reading log: %v", err)
	}
	if !strings.Contains(string(contents), "login: selected device flow for codex") {
		t.Errorf("log is missing the login entry:\n%s", contents)
	}
}

// A nil model or a session with no logger must not panic — logLogin is called
// from paths that run before the logger is wired.
func TestLogLogin_WithoutLoggerIsSafe(t *testing.T) {
	var nilModel *model
	nilModel.logLogin("no panic please")
	(&model{}).logLogin("still no panic")
}

// The status view is what `/login` with no argument shows; it must report each
// provider's real state rather than a fixed string.
func TestLoginShowStatus_ReflectsConfiguredKeys(t *testing.T) {
	m := &model{}
	m.loginShowStatus()

	if len(m.chatModel.Messages) != 1 {
		t.Fatalf("got %d messages, want 1", len(m.chatModel.Messages))
	}
	content := m.chatModel.Messages[0].content
	for _, want := range []string{"API Key Status", "codex", "/login <provider>"} {
		if !strings.Contains(content, want) {
			t.Errorf("status output is missing %q, got:\n%s", want, content)
		}
	}
	if !strings.Contains(content, "configured") && !strings.Contains(content, "not set") {
		t.Errorf("status output reports no state at all, got:\n%s", content)
	}
}

// openBrowserDefault is the real handler behind the openBrowser var that every
// other test replaces, so it needs its own exercise.
func TestOpenBrowserDefault_ReportsNoHandler(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("BROWSER", "")

	if err := openBrowserDefault("https://example.invalid/"); err == nil {
		t.Error("openBrowserDefault() = nil, want an error when nothing can open a URL")
	}
}
