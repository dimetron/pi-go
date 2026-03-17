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
	"time"
)

// Provider holds OAuth configuration for an LLM provider.
type Provider struct {
	Name         string
	EnvVar       string
	AuthURL      string // OAuth authorization endpoint
	TokenURL     string // OAuth token endpoint
	ClientID     string // OAuth client ID (public client)
	Scopes       []string
	ExtraParams  map[string]string // additional auth URL params
	TokenToKey   func(tok *TokenResponse) string
	KeyPageURL   string // fallback manual key page
	DeviceURL     string // device authorization endpoint (optional)
	UseDeviceFlow bool   // prefer device code flow over PKCE
	TLSPreflight  bool   // run TLS preflight before OAuth (OpenAI Codex)
}

// TokenResponse holds the OAuth token response.
type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
	IDToken      string `json:"id_token"`
	APIKey       string `json:"api_key"` // some providers return key directly
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
			Name:     "anthropic",
			EnvVar:   "ANTHROPIC_API_KEY",
			AuthURL:  "https://console.anthropic.com/oauth/authorize",
			TokenURL: "https://console.anthropic.com/oauth/token",
			ClientID: "pi-go-cli",
			Scopes:   []string{"api"},
			TokenToKey: func(tok *TokenResponse) string {
				if tok.APIKey != "" {
					return tok.APIKey
				}
				return tok.AccessToken
			},
			KeyPageURL: "https://console.anthropic.com/settings/keys",
		},
		{
			Name:          "openai",
			EnvVar:        "OPENAI_API_KEY",
			AuthURL:       "https://auth.openai.com/authorize",
			TokenURL:      "https://auth.openai.com/oauth/token",
			DeviceURL:     "https://auth.openai.com/device/code",
			ClientID:      "pi-go-cli",
			Scopes:        []string{"openai.public"},
			UseDeviceFlow: true,
			ExtraParams:   map[string]string{"audience": "https://api.openai.com/v1"},
			TokenToKey: func(tok *TokenResponse) string {
				if tok.APIKey != "" {
					return tok.APIKey
				}
				return tok.AccessToken
			},
			KeyPageURL: "https://platform.openai.com/api-keys",
		},
		{
			Name:     "codex",
			EnvVar:   "OPENAI_API_KEY",
			AuthURL:  "https://auth.openai.com/authorize",
			TokenURL: "https://auth.openai.com/oauth/token",
			ClientID: "pi-go-cli",
			Scopes:   []string{"openid", "profile", "email", "offline_access"},
			ExtraParams: map[string]string{
				"audience": "https://api.openai.com/v1",
			},
			TokenToKey: func(tok *TokenResponse) string {
				if tok.APIKey != "" {
					return tok.APIKey
				}
				return tok.AccessToken
			},
			KeyPageURL:   "https://platform.openai.com/api-keys",
			TLSPreflight: true,
		},
		{
			Name:     "gemini",
			EnvVar:   "GOOGLE_API_KEY",
			AuthURL:  "https://accounts.google.com/o/oauth2/v2/auth",
			TokenURL: "https://oauth2.googleapis.com/token",
			ClientID: "pi-go-cli",
			Scopes:   []string{"https://www.googleapis.com/auth/generative-language"},
			TokenToKey: func(tok *TokenResponse) string {
				if tok.APIKey != "" {
					return tok.APIKey
				}
				return tok.AccessToken
			},
			KeyPageURL: "https://aistudio.google.com/apikey",
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

	// Start local callback server on a random port.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("starting callback server: %w", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	redirectURI := fmt.Sprintf("http://127.0.0.1:%d/callback", port)

	state := generateState()

	// Build authorization URL.
	authURL := buildAuthURL(prov, redirectURI, state, challenge)

	// Channel to receive the auth code.
	codeCh := make(chan codeResult, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		handleCallback(w, r, state, codeCh)
	})

	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(listener) }()
	defer func() {
		shutCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()

	// Open browser.
	if err := openBrowser(authURL); err != nil {
		return nil, fmt.Errorf("opening browser: %w", err)
	}

	// Wait for callback or timeout.
	select {
	case <-ctx.Done():
		return &Result{Provider: prov.Name, Err: ctx.Err()}, nil
	case cr := <-codeCh:
		if cr.err != nil {
			return &Result{Provider: prov.Name, Err: cr.err}, nil
		}
		// Exchange code for token.
		tok, err := exchangeCode(ctx, prov, cr.code, redirectURI, verifier)
		if err != nil {
			return &Result{Provider: prov.Name, Err: fmt.Errorf("token exchange: %w", err)}, nil
		}
		apiKey := prov.TokenToKey(tok)
		return &Result{
			Provider: prov.Name,
			APIKey:   apiKey,
			EnvVar:   prov.EnvVar,
		}, nil
	}
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
	defer resp.Body.Close()

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

	if errParam := q.Get("error"); errParam != "" {
		desc := q.Get("error_description")
		if desc == "" {
			desc = errParam
		}
		ch <- codeResult{err: fmt.Errorf("OAuth error: %s", desc)}
		http.Error(w, "Authentication failed: "+desc, http.StatusBadRequest)
		return
	}

	if q.Get("state") != expectedState {
		ch <- codeResult{err: fmt.Errorf("state mismatch")}
		http.Error(w, "Invalid state parameter", http.StatusBadRequest)
		return
	}

	code := q.Get("code")
	if code == "" {
		ch <- codeResult{err: fmt.Errorf("no authorization code received")}
		http.Error(w, "No code received", http.StatusBadRequest)
		return
	}

	ch <- codeResult{code: code}

	w.Header().Set("Content-Type", "text/html")
	fmt.Fprint(w, `<!DOCTYPE html><html><body>
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
	defer resp.Body.Close()

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
	defer resp.Body.Close()

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

	os.Setenv(envVar, apiKey)
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

	_, err := client.Get(openAIAuthProbeURL)
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
