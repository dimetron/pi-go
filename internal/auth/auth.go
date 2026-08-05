// Package auth implements OAuth PKCE and device-code flows for SSO login.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"time"
)

// DebugLog is an optional callback invoked with low-level auth diagnostics
// (callback hits, token-exchange status codes, redacted error bodies). It is
// nil by default so the package has no stdout/stderr side effects; callers
// that want OAuth tracing in the pi-go session log set it to a logger.Info-
// compatible function during startup.
var debugLog atomic.Pointer[func(string)]

// SetDebugLogger installs a diagnostic sink for auth flows. Passing nil
// disables logging. The function is invoked from goroutines handling OAuth
// callbacks, so implementations must be goroutine-safe.
func SetDebugLogger(fn func(string)) {
	if fn == nil {
		debugLog.Store(nil)
		return
	}
	debugLog.Store(&fn)
}

func logf(format string, args ...any) {
	p := debugLog.Load()
	if p == nil || *p == nil {
		return
	}
	(*p)(fmt.Sprintf(format, args...))
}

// Debug emits a pre-formatted diagnostic line to the debug sink installed
// via SetDebugLogger. Used by adjacent packages (e.g. provider/openai.go's
// codex backend transport) so they can share the session logger the TUI
// already wires for auth events, without each package plumbing its own
// logger interface.
func Debug(msg string) {
	p := debugLog.Load()
	if p == nil || *p == nil {
		return
	}
	(*p)(msg)
}

// Provider holds OAuth configuration for an LLM provider.
type Provider struct {
	Name              string
	EnvVar            string
	AuthURL           string // OAuth authorization endpoint
	TokenURL          string // OAuth token endpoint
	ClientID          string // OAuth client ID (public client)
	Scopes            []string
	ExtraParams       map[string]string // additional auth URL params
	TokenToKey        func(tok *TokenResponse) string
	KeyPageURL        string // fallback manual key page
	DeviceURL         string // device authorization endpoint (optional)
	UseDeviceFlow     bool   // prefer RFC 8628 device code flow over PKCE
	CodexDeviceAuth   bool   // use OpenAI's non-RFC-8628 device auth (see codex_device.go)
	DeviceVerifyURL   string // page the user opens to enter the code
	DeviceRedirectURI string // redirect_uri asserted in the device code exchange
	TLSPreflight      bool   // run TLS preflight before OAuth (OpenAI Codex)
	CodexOAuth        bool   // use Codex OAuth callback + token-exchange semantics
	ManualCode        bool   // user pastes a code or callback URL (no local listener)
	ManualRedirectURI string // fixed redirect URI for manual-code flow
	TokenJSONBody     bool   // POST token exchange as JSON (Anthropic) instead of form-encoded
	APIKeyURL         string // optional: exchange OAuth access_token for an API key via this endpoint
}

// TokenResponse holds the OAuth token response.
type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
	IDToken      string `json:"id_token"`
	APIKey       string `json:"api_key"`        // some providers return key directly
	APIKeyCamel  string `json:"apiKey"`         // alternate camelCase response key
	OpenAIAPIKey string `json:"openai_api_key"` // alternate token-exchange response key
	RawKey       string `json:"raw_key"`        // Anthropic create_api_key response
}

// DeviceCodeResponse holds the device authorization response.
type DeviceCodeResponse struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
}

// Result is the outcome of an SSO login flow.
type Result struct {
	Provider string
	APIKey   string
	EnvVar   string
	Err      error
}

// Providers returns the list of configured OAuth providers.
func Providers() []Provider {
	return []Provider{
		{
			// Codex ChatGPT OAuth: the OAuth access_token itself is the
			// credential we send to OpenAI. We deliberately do NOT attempt
			// the `requested_token=openai-api-key` token-exchange, because
			// ChatGPT accounts without a platform organization cannot mint
			// API keys. Matches pi-mono's openai-codex provider.
			Name:     "codex",
			EnvVar:   "OPENAI_API_KEY",
			AuthURL:  "https://auth.openai.com/oauth/authorize",
			TokenURL: "https://auth.openai.com/oauth/token",
			ClientID: "app_EMoamEEZ73f0CkXaXp7hrann",
			Scopes:   []string{"openid", "profile", "email", "offline_access"},
			ExtraParams: map[string]string{
				"id_token_add_organizations": "true",
				"codex_cli_simplified_flow":  "true",
				"originator":                 "pi-go",
			},
			TokenToKey: func(tok *TokenResponse) string {
				return tok.AccessToken
			},
			KeyPageURL:   "https://platform.openai.com/api-keys",
			TLSPreflight: true,
			CodexOAuth:   true,
			// Device auth is preferred over PKCE because it needs no local
			// callback listener, so it works unchanged in a dev container, a
			// Codespace, or over SSH — where the localhost redirect does not.
			CodexDeviceAuth:   true,
			DeviceURL:         "https://auth.openai.com/api/accounts/deviceauth",
			DeviceVerifyURL:   "https://auth.openai.com/codex/device",
			DeviceRedirectURI: "https://auth.openai.com/deviceauth/callback",
		},
	}
}

// FindProvider returns a provider by name.
func FindProvider(name string) (Provider, bool) {
	for _, p := range Providers() {
		if strings.EqualFold(p.Name, name) {
			return p, true
		}
	}
	return Provider{}, false
}

// --- PKCE Flow ---

// PKCEFlow runs the OAuth PKCE authorization code flow.
// It starts a local HTTP server, opens the browser, and waits for the callback.
func PKCEFlow(ctx context.Context, prov Provider, openBrowser func(string) error) (*Result, error) {
	verifier, challenge := generatePKCE()

	listenAddr, callbackHost, callbackPath := callbackConfig(prov)

	// Start local callback server.
	listener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return nil, fmt.Errorf("starting callback server: %w", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port //nolint:errcheck // type assertion is guaranteed for TCP listener
	redirectURI := fmt.Sprintf("http://%s:%d%s", callbackHost, port, callbackPath)

	state := generateState()

	// Build authorization URL.
	authURL := buildAuthURL(prov, redirectURI, state, challenge)

	// Channel to receive the auth code.
	codeCh := make(chan codeResult, 1)

	mux := http.NewServeMux()
	mux.HandleFunc(callbackPath, func(w http.ResponseWriter, r *http.Request) {
		handleCallback(w, r, state, codeCh)
	})

	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(listener) }()
	defer func() {
		shutCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()

	logf("pkce: authorize redirect_uri=%s state_len=%d", redirectURI, len(state))

	// Open browser.
	if err := openBrowser(authURL); err != nil {
		return nil, fmt.Errorf("opening browser: %w", err)
	}

	// Wait for callback or timeout.
	select {
	case <-ctx.Done():
		logf("pkce: ctx done before callback: %v", ctx.Err())
		return &Result{Provider: prov.Name, Err: ctx.Err()}, nil
	case cr := <-codeCh:
		if cr.err != nil {
			logf("pkce: callback error: %v", cr.err)
			return &Result{Provider: prov.Name, Err: cr.err}, nil
		}
		logf("pkce: callback ok code_len=%d; exchanging code", len(cr.code))
		// Exchange code for token.
		tok, err := exchangeCode(ctx, prov, cr.code, redirectURI, verifier)
		if err != nil {
			logf("pkce: token exchange failed: %v", err)
			return &Result{Provider: prov.Name, Err: fmt.Errorf("token exchange: %w", err)}, nil
		}
		logf("pkce: token exchange ok access_len=%d id_len=%d refresh_len=%d api_key_present=%v",
			len(tok.AccessToken), len(tok.IDToken), len(tok.RefreshToken),
			tok.APIKey != "" || tok.APIKeyCamel != "" || tok.OpenAIAPIKey != "")
		apiKey := prov.TokenToKey(tok)
		logf("pkce: final api_key_present=%v", apiKey != "")
		return &Result{
			Provider: prov.Name,
			APIKey:   apiKey,
			EnvVar:   prov.EnvVar,
		}, nil
	}
}

// --- Manual Code Flow ---

// ManualCodeSession holds the state needed to complete a manual-code OAuth flow.
// The caller builds the auth URL via StartManualCodeFlow, opens a browser, then
// asks the user to paste the callback URL or authorization code and passes it
// to CompleteManualCodeFlow.
type ManualCodeSession struct {
	Provider    Provider
	AuthURL     string
	Verifier    string
	State       string
	RedirectURI string
}

// StartManualCodeFlow builds an authorization URL for a provider that expects the
// user to copy a callback URL or code from the browser and paste it into the
// CLI. No local HTTP listener is started.
func StartManualCodeFlow(prov Provider) (*ManualCodeSession, error) {
	if !prov.ManualCode {
		return nil, fmt.Errorf("provider %s is not configured for manual-code flow", prov.Name)
	}
	if prov.ManualRedirectURI == "" {
		return nil, fmt.Errorf("provider %s has no ManualRedirectURI", prov.Name)
	}
	verifier, challenge := generatePKCE()
	state := generateState()
	authURL := buildAuthURL(prov, prov.ManualRedirectURI, state, challenge)
	return &ManualCodeSession{
		Provider:    prov,
		AuthURL:     authURL,
		Verifier:    verifier,
		State:       state,
		RedirectURI: prov.ManualRedirectURI,
	}, nil
}

// CompleteManualCodeFlow exchanges a pasted authorization code for a token.
// Anthropic's manual-code flow may provide either a full redirect URL
// ("http://localhost:53692/callback?code=...&state=..."), a query string, a
// "<code>#<state>" pair, or just the code. When state is present it is
// validated against the session state before the token exchange. When the
// provider has an APIKeyURL, the OAuth access token is exchanged for a
// provider-managed API key.
func CompleteManualCodeFlow(ctx context.Context, sess *ManualCodeSession, pasted string) (*Result, error) {
	if sess == nil {
		return nil, fmt.Errorf("nil session")
	}
	code, state := parseAuthorizationInput(pasted)
	if state != "" && state != sess.State {
		return nil, fmt.Errorf("OAuth state mismatch")
	}
	if code == "" {
		if looksLikeAuthorizationRequest(pasted) {
			return nil, fmt.Errorf("missing authorization code; paste the final redirect URL containing code=... or the authorization code, not the authorization request parameters")
		}
		return nil, fmt.Errorf("missing authorization code")
	}
	if state == "" {
		state = sess.State
	}
	tok, err := exchangeCodeManual(ctx, sess.Provider, code, sess.RedirectURI, sess.Verifier, state)
	if err != nil {
		return &Result{Provider: sess.Provider.Name, Err: fmt.Errorf("token exchange: %w", err)}, nil
	}
	apiKey := sess.Provider.TokenToKey(tok)
	if sess.Provider.APIKeyURL != "" && tok.AccessToken != "" {
		raw, err := createAPIKey(ctx, sess.Provider, tok.AccessToken)
		if err != nil {
			return &Result{Provider: sess.Provider.Name, Err: fmt.Errorf("api key creation: %w", err)}, nil
		}
		apiKey = raw
	}
	return &Result{
		Provider: sess.Provider.Name,
		APIKey:   apiKey,
		EnvVar:   sess.Provider.EnvVar,
	}, nil
}

// exchangeCodeManual exchanges an authorization code for a token, using JSON
// body + state (required by Anthropic) when Provider.TokenJSONBody is set.
func exchangeCodeManual(ctx context.Context, prov Provider, code, redirectURI, verifier, state string) (*TokenResponse, error) {
	var req *http.Request
	var err error
	if prov.TokenJSONBody {
		payload := map[string]string{
			"grant_type":    "authorization_code",
			"code":          code,
			"redirect_uri":  redirectURI,
			"client_id":     prov.ClientID,
			"code_verifier": verifier,
			"state":         state,
		}
		body, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, prov.TokenURL, strings.NewReader(string(body)))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
	} else {
		data := url.Values{
			"grant_type":    {"authorization_code"},
			"code":          {code},
			"redirect_uri":  {redirectURI},
			"client_id":     {prov.ClientID},
			"code_verifier": {verifier},
			"state":         {state},
		}
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, prov.TokenURL, strings.NewReader(data.Encode()))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token exchange failed (%d): %s", resp.StatusCode, sanitizeErrorBody(body))
	}
	var tok TokenResponse
	if err := json.Unmarshal(body, &tok); err != nil {
		return nil, fmt.Errorf("parsing token response: %w", err)
	}
	return &tok, nil
}

// createAPIKey exchanges an OAuth access token for a provider-managed API key.
// Anthropic exposes POST /api/oauth/claude_cli/create_api_key with a bearer
// token; the response body contains {"raw_key": "sk-ant-..."}.
func createAPIKey(ctx context.Context, prov Provider, accessToken string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, prov.APIKeyURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("create_api_key failed (%d): %s", resp.StatusCode, sanitizeErrorBody(body))
	}
	var out struct {
		RawKey string `json:"raw_key"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", fmt.Errorf("parsing create_api_key response: %w", err)
	}
	if out.RawKey == "" {
		return "", fmt.Errorf("create_api_key response missing raw_key")
	}
	return out.RawKey, nil
}

func parseAuthorizationInput(pasted string) (code, state string) {
	pasted = strings.TrimSpace(pasted)
	if pasted == "" {
		return "", ""
	}
	if strings.HasPrefix(pasted, "{") {
		var body map[string]string
		if err := json.Unmarshal([]byte(pasted), &body); err == nil {
			return body["code"], body["state"]
		}
	}
	if u, err := url.Parse(pasted); err == nil && u.IsAbs() {
		q := u.Query()
		return q.Get("code"), q.Get("state")
	}
	if i := strings.Index(pasted, "#"); i >= 0 {
		return pasted[:i], pasted[i+1:]
	}
	if strings.Contains(pasted, "code=") {
		q, err := url.ParseQuery(strings.TrimPrefix(pasted, "?"))
		if err == nil {
			return q.Get("code"), q.Get("state")
		}
	}
	return pasted, ""
}

func looksLikeAuthorizationRequest(pasted string) bool {
	pasted = strings.TrimSpace(pasted)
	if strings.HasPrefix(pasted, "{") {
		var body map[string]string
		if err := json.Unmarshal([]byte(pasted), &body); err != nil {
			return false
		}
		return body["response_type"] == "code" && body["redirect_uri"] != "" && body["code"] == ""
	}
	q, err := url.ParseQuery(strings.TrimPrefix(pasted, "?"))
	if err != nil {
		return false
	}
	return q.Get("response_type") == "code" && q.Get("redirect_uri") != "" && q.Get("code") == ""
}

// --- Device Code Flow ---

// DeviceFlow runs the OAuth device authorization grant (RFC 8628).
// Returns the device code response so the caller can display the user code,
// then polls for completion.
func DeviceFlow(ctx context.Context, prov Provider) (*DeviceCodeResponse, error) {
	if prov.DeviceURL == "" {
		return nil, fmt.Errorf("provider %s does not support device code flow", prov.Name)
	}

	data := url.Values{
		"client_id": {prov.ClientID},
		"scope":     {strings.Join(prov.Scopes, " ")},
	}
	for k, v := range prov.ExtraParams {
		data.Set(k, v)
	}

	resp, err := http.PostForm(prov.DeviceURL, data)
	if err != nil {
		return nil, fmt.Errorf("device code request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("device code request failed (%d): %s", resp.StatusCode, sanitizeErrorBody(body))
	}

	var dcr DeviceCodeResponse
	if err := json.Unmarshal(body, &dcr); err != nil {
		return nil, fmt.Errorf("parsing device code response: %w", err)
	}
	if dcr.Interval == 0 {
		dcr.Interval = 5
	}
	return &dcr, nil
}

// PollDeviceToken polls for the device code token until authorized or expired.
func PollDeviceToken(ctx context.Context, prov Provider, deviceCode string, interval int) (*Result, error) {
	ticker := time.NewTicker(time.Duration(interval) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return &Result{Provider: prov.Name, Err: ctx.Err()}, nil
		case <-ticker.C:
			tok, err := requestDeviceToken(ctx, prov, deviceCode)
			if err != nil {
				// Check for "authorization_pending" — keep polling.
				if strings.Contains(err.Error(), "authorization_pending") {
					continue
				}
				// "slow_down" — increase interval.
				if strings.Contains(err.Error(), "slow_down") {
					ticker.Reset(time.Duration(interval+5) * time.Second)
					continue
				}
				return &Result{Provider: prov.Name, Err: err}, nil
			}

			apiKey := prov.TokenToKey(tok)
			return &Result{
				Provider: prov.Name,
				APIKey:   apiKey,
				EnvVar:   prov.EnvVar,
			}, nil
		}
	}
}

// --- Helpers ---

type codeResult struct {
	code string
	err  error
}

func generatePKCE() (verifier, challenge string) {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	verifier = base64.RawURLEncoding.EncodeToString(b)
	h := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(h[:])
	return
}

func generateState() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func callbackConfig(prov Provider) (listenAddr, callbackHost, callbackPath string) {
	if prov.CodexOAuth {
		return "localhost:1455", "localhost", "/auth/callback"
	}
	return "127.0.0.1:0", "127.0.0.1", "/callback"
}

func buildAuthURL(prov Provider, redirectURI, state, challenge string) string {
	params := url.Values{
		"response_type":         {"code"},
		"client_id":             {prov.ClientID},
		"redirect_uri":          {redirectURI},
		"state":                 {state},
		"scope":                 {strings.Join(prov.Scopes, " ")},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}
	for k, v := range prov.ExtraParams {
		params.Set(k, v)
	}
	return prov.AuthURL + "?" + params.Encode()
}

func handleCallback(w http.ResponseWriter, r *http.Request, expectedState string, ch chan<- codeResult) {
	q := r.URL.Query()
	logf("callback: path=%s has_code=%v has_state=%v has_error=%v",
		r.URL.Path, q.Get("code") != "", q.Get("state") != "", q.Get("error") != "")

	if errParam := q.Get("error"); errParam != "" {
		desc := q.Get("error_description")
		if desc == "" {
			desc = errParam
		}
		logf("callback: oauth error=%s description=%q", errParam, desc)
		ch <- codeResult{err: fmt.Errorf("OAuth error: %s", desc)}
		http.Error(w, "Authentication failed: "+desc, http.StatusBadRequest)
		return
	}

	if q.Get("state") != expectedState {
		logf("callback: state mismatch got_len=%d want_len=%d", len(q.Get("state")), len(expectedState))
		ch <- codeResult{err: fmt.Errorf("state mismatch")}
		http.Error(w, "Invalid state parameter", http.StatusBadRequest)
		return
	}

	code := q.Get("code")
	if code == "" {
		logf("callback: no code received")
		ch <- codeResult{err: fmt.Errorf("no authorization code received")}
		http.Error(w, "No code received", http.StatusBadRequest)
		return
	}

	logf("callback: ok code_len=%d", len(code))
	ch <- codeResult{code: code}

	w.Header().Set("Content-Type", "text/html")
	_, _ = fmt.Fprint(w, `<!DOCTYPE html><html><body>
<h2>✓ Authentication successful</h2>
<p>You can close this tab and return to pi.</p>
<script>window.close()</script>
</body></html>`)
}

func exchangeCode(ctx context.Context, prov Provider, code, redirectURI, verifier string) (*TokenResponse, error) {
	data := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"client_id":     {prov.ClientID},
		"code_verifier": {verifier},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, prov.TokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token exchange failed (%d): %s", resp.StatusCode, sanitizeErrorBody(body))
	}

	var tok TokenResponse
	if err := json.Unmarshal(body, &tok); err != nil {
		return nil, fmt.Errorf("parsing token response: %w", err)
	}
	return &tok, nil
}

func requestDeviceToken(ctx context.Context, prov Provider, deviceCode string) (*TokenResponse, error) {
	data := url.Values{
		"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
		"device_code": {deviceCode},
		"client_id":   {prov.ClientID},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, prov.TokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)

	// Check for pending/slow_down errors (returned as 400 with error JSON).
	if resp.StatusCode == http.StatusBadRequest {
		var errResp struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(body, &errResp) == nil && errResp.Error != "" {
			return nil, fmt.Errorf("%s", errResp.Error)
		}
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("device token request failed (%d): %s", resp.StatusCode, sanitizeErrorBody(body))
	}

	var tok TokenResponse
	if err := json.Unmarshal(body, &tok); err != nil {
		return nil, fmt.Errorf("parsing token response: %w", err)
	}
	return &tok, nil
}

// KeyKind identifies the shape of an OpenAI credential. "api-key" is a
// classic `sk-…` platform key; "codex-oauth" is a ChatGPT OAuth access
// token (a JWT whose payload carries the `https://api.openai.com/auth`
// claim). "unknown" is anything else — treat as opaque.
type KeyKind string

const (
	KeyKindAPIKey     KeyKind = "api-key"
	KeyKindCodexOAuth KeyKind = "codex-oauth"
	KeyKindUnknown    KeyKind = "unknown"
)

// IdentifyKey classifies an OpenAI credential. Detection is structural —
// `sk-` / `sk_live_` / `sk-proj-` prefixes indicate a platform API key;
// a three-segment JWT whose payload decodes to JSON and contains the
// `https://api.openai.com/auth` claim indicates a codex OAuth token.
// The token itself is never logged or returned.
func IdentifyKey(key string) KeyKind {
	k := strings.TrimSpace(key)
	if k == "" {
		return KeyKindUnknown
	}
	if strings.HasPrefix(k, "sk-") || strings.HasPrefix(k, "sk_") {
		return KeyKindAPIKey
	}
	parts := strings.Split(k, ".")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return KeyKindUnknown
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		if payload, err = base64.URLEncoding.DecodeString(parts[1]); err != nil {
			return KeyKindUnknown
		}
	}
	var raw map[string]json.RawMessage
	if json.Unmarshal(payload, &raw) != nil {
		return KeyKindUnknown
	}
	if _, ok := raw["https://api.openai.com/auth"]; ok {
		return KeyKindCodexOAuth
	}
	// JWT-shaped but not an OpenAI-issued OAuth token.
	return KeyKindUnknown
}

// IsCodexOAuthToken is a convenience wrapper reporting whether key is a
// ChatGPT OAuth access token (as minted by `/login codex`).
func IsCodexOAuthToken(key string) bool {
	return IdentifyKey(key) == KeyKindCodexOAuth
}

// SaveKey saves an API key to ~/.pi-go/.env.
func SaveKey(envVar, apiKey string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("cannot determine home directory: %w", err)
	}

	envPath := filepath.Join(home, ".pi-go", ".env")

	existing := ""
	if data, err := os.ReadFile(envPath); err == nil {
		existing = string(data)
	}

	newContent := updateEnvVar(existing, envVar, apiKey)

	if err := os.MkdirAll(filepath.Dir(envPath), 0700); err != nil {
		return fmt.Errorf("creating directory: %w", err)
	}

	if err := os.WriteFile(envPath, []byte(newContent), 0600); err != nil {
		return fmt.Errorf("writing .env: %w", err)
	}

	_ = os.Setenv(envVar, apiKey)
	return nil
}

// sanitizeErrorBody truncates HTML or very long error responses for display.
func sanitizeErrorBody(body []byte) string {
	s := strings.TrimSpace(string(body))
	// If it looks like HTML, extract a short summary.
	if strings.HasPrefix(s, "<") || strings.HasPrefix(s, "<!") {
		return "(HTML error page — server returned non-JSON response)"
	}
	if len(s) > 200 {
		return s[:200] + "..."
	}
	return s
}

// --- TLS Preflight (OpenAI OAuth) ---

// TLS certificate error codes that indicate cert-chain problems.
var tlsCertErrorCodes = map[string]bool{
	"UNABLE_TO_GET_ISSUER_CERT_LOCALLY": true,
	"UNABLE_TO_VERIFY_LEAF_SIGNATURE":   true,
	"CERT_HAS_EXPIRED":                  true,
	"DEPTH_ZERO_SELF_SIGNED_CERT":       true,
	"SELF_SIGNED_CERT_IN_CHAIN":         true,
	"ERR_TLS_CERT_ALTNAME_INVALID":      true,
}

var tlsCertErrorPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)unable to get local issuer certificate`),
	regexp.MustCompile(`(?i)unable to verify the first certificate`),
	regexp.MustCompile(`(?i)self[- ]signed certificate`),
	regexp.MustCompile(`(?i)certificate has expired`),
	regexp.MustCompile(`(?i)x509`),
}

// TLSPreflightResult is the outcome of the OAuth TLS preflight check.
type TLSPreflightResult struct {
	OK      bool
	Kind    string // "tls-cert" or "network"
	Code    string
	Message string
}

const openAIAuthProbeURL = "https://auth.openai.com/oauth/authorize?response_type=code&client_id=pi-go-preflight&redirect_uri=http%3A%2F%2Flocalhost%3A1455%2Fauth%2Fcallback&scope=openid+profile+email"

// RunTLSPreflight probes the OpenAI auth endpoint to detect TLS certificate issues.
func RunTLSPreflight(timeoutMs int) *TLSPreflightResult {
	if timeoutMs <= 0 {
		timeoutMs = 5000
	}
	client := &http.Client{
		Timeout: time.Duration(timeoutMs) * time.Millisecond,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	resp, err := client.Get(openAIAuthProbeURL) //nolint:bodyclose // response may be nil on TLS errors
	if resp != nil && resp.Body != nil {
		resp.Body.Close()
	}
	if err == nil {
		return &TLSPreflightResult{OK: true}
	}

	msg := err.Error()
	kind := "network"

	// Check for TLS-specific errors.
	if isTLSError(msg) {
		kind = "tls-cert"
	}

	return &TLSPreflightResult{
		OK:      false,
		Kind:    kind,
		Message: msg,
	}
}

func isTLSError(msg string) bool {
	for code := range tlsCertErrorCodes {
		if strings.Contains(msg, code) {
			return true
		}
	}
	for _, pat := range tlsCertErrorPatterns {
		if pat.MatchString(msg) {
			return true
		}
	}
	// Go's TLS errors
	var tlsErr *tls.CertificateVerificationError
	_ = tlsErr // type check only
	if strings.Contains(msg, "certificate") && (strings.Contains(msg, "verify") || strings.Contains(msg, "unknown authority") || strings.Contains(msg, "expired")) {
		return true
	}
	return false
}

// FormatTLSPreflightFix returns a user-friendly message for TLS preflight failures.
func FormatTLSPreflightFix(result *TLSPreflightResult) string {
	if result.Kind != "tls-cert" {
		return fmt.Sprintf("OAuth preflight failed (network error): %s\nVerify DNS/firewall/proxy access to auth.openai.com and retry.", result.Message)
	}
	return fmt.Sprintf("OAuth preflight failed: TLS certificate validation error.\nCause: %s\n\nFix (macOS/Homebrew):\n  brew postinstall ca-certificates\n  brew postinstall openssl@3\nThen retry the login.", result.Message)
}

func updateEnvVar(content, key, value string) string {
	prefix := key + "="
	var lines []string
	found := false

	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(line, prefix) {
			lines = append(lines, prefix+value)
			found = true
		} else if line != "" || found {
			lines = append(lines, line)
		}
	}

	if !found {
		if content != "" && !strings.HasSuffix(content, "\n") {
			lines = append(lines, "")
		}
		lines = append(lines, prefix+value)
	}

	result := strings.Join(lines, "\n")
	if !strings.HasSuffix(result, "\n") {
		result += "\n"
	}
	return result
}
