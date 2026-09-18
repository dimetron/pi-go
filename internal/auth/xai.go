// xAI subscription OAuth (SuperGrok / X Premium+).
//
// xAI sells two independent products that both reach Grok: a per-token
// developer API (api.x.ai, authenticated with an `xai-…` key from
// console.x.ai) and a consumer subscription (grok.com). A subscription can
// also drive inference, but not through the developer API — that surface
// rejects a subscription bearer with HTTP 402
// `personal-team-blocked:spending-limit`, because it bills against prepaid
// credits rather than the subscription.
//
// The subscription path is the Grok CLI chat proxy (cli-chat-proxy.grok.com),
// which accepts an OAuth access token minted by auth.x.ai and gates it behind
// a set of Grok-CLI identity headers (see internal/provider/xai.go). That is
// the same surface the official `grok` CLI, Warp, and other harnesses use.
//
// This file owns the credential half of that path: the OAuth provider
// definition, the refresh grant, the on-disk token store, and the importer for
// an existing `grok login` session. The request half lives in the provider
// package.
//
// Why refresh is not optional here: xAI access tokens live 6 hours
// (expires_in 21600) and a stored token is used until it expires. Without the
// refresh grant every session would need a fresh `pi login xai` after six
// hours, which is not a usable arrangement for an agent harness. The refresh
// response rotates the refresh token, so the rotated value must be persisted
// or the next refresh fails.
package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// xAI OAuth endpoints and the public Grok-CLI client id. The client id is a
// public client (no secret) — xAI only completes OAuth for allowlisted
// clients, and reusing the Grok-CLI id is what makes the subscription flow
// work from a third-party harness. The consent screen may therefore say
// "Grok Build" or "Grok CLI".
//
// Declared as vars rather than consts so tests can point them at an
// httptest server.
var (
	xaiAuthURL   = "https://auth.x.ai/oauth2/authorize"
	xaiTokenURL  = "https://auth.x.ai/oauth2/token"
	xaiDeviceURL = "https://auth.x.ai/oauth2/device/code"
)

const (
	xaiClientID = "b1a00492-073a-47ea-816f-4c329264a828"

	// xaiScopes is the scope set the Grok CLI requests. `offline_access` is
	// what makes the token response carry a refresh_token; the grok-cli and
	// api scopes gate the CLI chat proxy; conversations:*/workspaces:* are
	// required by that proxy to accept the bearer at all.
	xaiScopes = "openid profile email offline_access grok-cli:access api:access " +
		"conversations:read conversations:write"

	// xaiAuthEnvVar is the env var xAI credentials land in. Both shapes —
	// a developer API key and a subscription access token — travel in it; the
	// provider distinguishes them structurally.
	xaiAuthEnvVar = "XAI_API_KEY"

	// xaiTokenFileName is the token store, relative to the pi-go config dir.
	// Separate from ~/.pi-go/.env because the refresh token and expiry are
	// part of the credential, and .env is a flat KEY=value file.
	xaiTokenFileName = "xai_auth.json"

	// xaiRefreshSkew refreshes an access token this long before it actually
	// expires, so a token cannot lapse mid-request.
	xaiRefreshSkew = 5 * time.Minute
)

// XAITokenSet is a stored xAI subscription credential. It is written to
// ~/.pi-go/xai_auth.json with 0600 permissions.
type XAITokenSet struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	TokenType    string    `json:"token_type,omitempty"`
	ExpiresAt    time.Time `json:"expires_at,omitempty"`
	// AccountID is the xAI principal id from the JWT, kept for diagnostics.
	AccountID string `json:"account_id,omitempty"`
}

// Expired reports whether the access token is past (or close to) its expiry.
// A zero ExpiresAt is treated as expired, because an unparseable or absent
// expiry cannot be trusted to still be valid.
func (t *XAITokenSet) Expired(now time.Time) bool {
	if t == nil || t.AccessToken == "" {
		return true
	}
	if t.ExpiresAt.IsZero() {
		return true
	}
	return now.Add(xaiRefreshSkew).After(t.ExpiresAt)
}

// Refreshable reports whether the set carries a refresh token, i.e. whether
// Expired can be recovered from without a new browser login.
func (t *XAITokenSet) Refreshable() bool {
	return t != nil && t.RefreshToken != ""
}

// XAITokenStorePath returns the absolute path of the token store, honoring
// the home directory the process sees (tests redirect HOME).
func XAITokenStorePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %w", err)
	}
	return filepath.Join(home, ".pi-go", xaiTokenFileName), nil
}

// SaveXAITokens writes the token set to ~/.pi-go/xai_auth.json, creating the
// directory 0700 and the file 0600. The write is atomic (temp file + rename)
// so a crash cannot leave a truncated credential behind.
func SaveXAITokens(t *XAITokenSet) error {
	if t == nil || t.AccessToken == "" {
		return fmt.Errorf("refusing to save an empty xAI token set")
	}
	path, err := XAITokenStorePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("creating directory: %w", err)
	}
	body, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding token set: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), xaiTokenFileName+".tmp*")
	if err != nil {
		return fmt.Errorf("creating temporary token file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if err := tmp.Chmod(0600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("setting token file permissions: %w", err)
	}
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("writing token file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing token file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("installing token file: %w", err)
	}
	return nil
}

// LoadXAITokens reads the token store. A missing file is not an error: it
// returns (nil, nil) so callers can treat "never logged in" as a normal state.
func LoadXAITokens() (*XAITokenSet, error) {
	path, err := XAITokenStorePath()
	if err != nil {
		return nil, err
	}
	body, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading token store: %w", err)
	}
	var set XAITokenSet
	if err := json.Unmarshal(body, &set); err != nil {
		return nil, fmt.Errorf("parsing token store: %w", err)
	}
	return &set, nil
}

// ClearXAITokens removes the token store. Used by logout; a missing file is
// not an error.
func ClearXAITokens() error {
	path, err := XAITokenStorePath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing token store: %w", err)
	}
	return nil
}

// --- Detection ---

// IsXAISubscriptionToken reports whether key is an xAI OAuth access token
// rather than a developer API key. Detection is structural: developer keys are
// `xai-…` prefixed, subscription tokens are three-segment JWTs whose payload
// carries the xAI issuer or a grok-cli scope. The token itself is never
// logged.
func IsXAISubscriptionToken(key string) bool {
	k := strings.TrimSpace(key)
	if k == "" || strings.HasPrefix(k, "xai-") {
		return false
	}
	claims, ok := decodeJWTPayload(k)
	if !ok {
		return false
	}
	if iss, _ := claims["iss"].(string); iss == "https://auth.x.ai" {
		return true
	}
	scope, _ := claims["scope"].(string)
	return strings.Contains(scope, "grok-cli:access")
}

// decodeJWTPayload base64-decodes the payload segment of a JWT. Returns false
// for anything that is not a three-segment token with a JSON-object payload.
func decodeJWTPayload(token string) (map[string]any, bool) {
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) != 3 {
		return nil, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		if payload, err = base64.URLEncoding.DecodeString(parts[1]); err != nil {
			return nil, false
		}
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, false
	}
	return claims, true
}

// XAITokenExpiry extracts the `exp` claim from an xAI access token. An
// unparseable token returns the zero time, which Expired treats as stale.
func XAITokenExpiry(token string) time.Time {
	claims, ok := decodeJWTPayload(token)
	if !ok {
		return time.Time{}
	}
	exp, ok := claims["exp"].(float64)
	if !ok || exp <= 0 {
		return time.Time{}
	}
	return time.Unix(int64(exp), 0).UTC()
}

// XAIPrincipalID extracts the xAI principal (account) id, for diagnostics.
func XAIPrincipalID(token string) string {
	claims, ok := decodeJWTPayload(token)
	if !ok {
		return ""
	}
	if v, ok := claims["principal_id"].(string); ok && v != "" {
		return v
	}
	v, _ := claims["sub"].(string)
	return v
}

// --- Refresh ---

// xaiRefreshMu serializes refresh attempts. Two goroutines that both notice an
// expired token would otherwise race, and because xAI rotates the refresh
// token on every use the loser's rotated token is already spent — it would
// clobber the winner's store entry with a dead token.
var xaiRefreshMu sync.Mutex

// RefreshXAIToken exchanges a refresh token for a new access token. The
// response rotates the refresh token, so callers must persist the whole
// returned set rather than just the access token.
func RefreshXAIToken(ctx context.Context, refreshToken string) (*XAITokenSet, error) {
	if strings.TrimSpace(refreshToken) == "" {
		return nil, fmt.Errorf("no xAI refresh token available")
	}
	form := map[string]string{
		"grant_type":    "refresh_token",
		"refresh_token": refreshToken,
		"client_id":     xaiClientID,
	}
	tok, err := xaiFormTokenRequest(ctx, form)
	if err != nil {
		return nil, fmt.Errorf("xAI token refresh failed: %w", err)
	}
	if tok.AccessToken == "" {
		return nil, fmt.Errorf("xAI token refresh returned no access token")
	}
	set := xaiTokenSetFromResponse(tok)
	if set.RefreshToken == "" {
		// Some responses omit the rotated refresh token; keep using the one
		// that worked instead of losing the ability to refresh again.
		set.RefreshToken = refreshToken
	}
	return set, nil
}

// xaiTokenSetFromResponse converts a token response into the stored shape,
// deriving the expiry from expires_in when present and falling back to the
// JWT's own exp claim.
func xaiTokenSetFromResponse(tok *TokenResponse) *XAITokenSet {
	set := &XAITokenSet{
		AccessToken:  tok.AccessToken,
		RefreshToken: tok.RefreshToken,
		TokenType:    tok.TokenType,
		AccountID:    XAIPrincipalID(tok.AccessToken),
	}
	switch {
	case tok.ExpiresIn > 0:
		set.ExpiresAt = time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second).UTC()
	default:
		set.ExpiresAt = XAITokenExpiry(tok.AccessToken)
	}
	return set
}

// xaiFormTokenRequest POSTs a form-encoded body to the token endpoint and
// parses the response. Shared by the refresh grant and the device-code grant.
func xaiFormTokenRequest(ctx context.Context, form map[string]string) (*TokenResponse, error) {
	values := url.Values{}
	for k, v := range form {
		values.Set(k, v)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, xaiTokenURL,
		strings.NewReader(values.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token request failed (%d): %s", resp.StatusCode, sanitizeErrorBody(body))
	}
	var tok TokenResponse
	if err := json.Unmarshal(body, &tok); err != nil {
		return nil, fmt.Errorf("parsing token response: %w", err)
	}
	return &tok, nil
}

// --- Resolution ---

// ResolveXAICredential returns the xAI credential to use for a request, in
// precedence order:
//
//  1. A non-empty env value that is NOT an xAI subscription token. This is a
//     developer API key (or an explicit base-URL override the caller owns), so
//     it is returned untouched — refresh must never override an operator's
//     configured key.
//  2. A subscription token from env or the token store, refreshed when stale.
//
// It returns the credential and whether it is a subscription token, so the
// provider can pick the matching endpoint. A nil error with an empty credential
// means no xAI credential is configured at all.
func ResolveXAICredential(ctx context.Context) (key string, subscription bool, err error) {
	if envKey := strings.TrimSpace(os.Getenv(xaiAuthEnvVar)); envKey != "" && !IsXAISubscriptionToken(envKey) {
		return envKey, false, nil
	}

	// Gather candidate subscription credentials: env wins over the store, but
	// both are considered so a stale env token can fall back to a stored
	// refresh token.
	var fromEnv *XAITokenSet
	if envKey := strings.TrimSpace(os.Getenv(xaiAuthEnvVar)); IsXAISubscriptionToken(envKey) {
		fromEnv = &XAITokenSet{
			AccessToken: envKey,
			ExpiresAt:   XAITokenExpiry(envKey),
			AccountID:   XAIPrincipalID(envKey),
		}
	}
	stored, err := LoadXAITokens()
	if err != nil {
		// A corrupt store must not block a usable env token.
		logf("xai: token store unreadable: %v", err)
	}

	now := time.Now()
	if fromEnv != nil && !fromEnv.Expired(now) {
		return fromEnv.AccessToken, true, nil
	}
	if stored != nil && !stored.Expired(now) {
		return stored.AccessToken, true, nil
	}

	// Something is expired. Refuse to refresh out from under an explicit env
	// token unless a refresh token is actually available.
	refreshToken := ""
	if stored != nil {
		refreshToken = stored.RefreshToken
	}
	if refreshToken == "" {
		if fromEnv != nil {
			// Expired subscription token and nothing to refresh with: hand it
			// back so the failure surfaces as the upstream 401 rather than as a
			// confusing "no credential" error.
			return fromEnv.AccessToken, true, nil
		}
		return "", false, nil
	}

	set, err := refreshXAITokenLocked(ctx, refreshToken)
	if err != nil {
		if fromEnv != nil {
			return fromEnv.AccessToken, true, nil
		}
		return "", true, err
	}
	return set.AccessToken, true, nil
}

// refreshXAITokenLocked refreshes under a process-wide mutex and persists the
// rotated set. The lock matters because xAI rotates the refresh token: a
// concurrent second refresh would spend a token that is no longer valid.
func refreshXAITokenLocked(ctx context.Context, refreshToken string) (*XAITokenSet, error) {
	xaiRefreshMu.Lock()
	defer xaiRefreshMu.Unlock()

	set, err := RefreshXAIToken(ctx, refreshToken)
	if err != nil {
		return nil, err
	}
	if err := SaveXAITokens(set); err != nil {
		// The refreshed token still works for this process; a persistence
		// failure costs the next run a re-login, so log rather than fail.
		logf("xai: refreshed token could not be persisted: %v", err)
	}
	return set, nil
}

// --- grok CLI import ---

// ImportGrokCLITokens adopts the credential an existing `grok` CLI has already
// authenticated, so a user who runs the official CLI does not have to log in
// twice. It reads ~/.grok/auth.json.
//
// The file is a map keyed by "<issuer>::<client_id>" whose values hold the
// token material under `key` / `refresh_token` / `expires_at`. Returns
// (nil, nil) when the file is absent or carries no usable xAI token.
func ImportGrokCLITokens() (*XAITokenSet, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("cannot determine home directory: %w", err)
	}
	path := filepath.Join(home, ".grok", "auth.json")
	body, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading grok CLI credentials: %w", err)
	}

	var entries map[string]struct {
		Key          string `json:"key"`
		RefreshToken string `json:"refresh_token"`
		ExpiresAt    string `json:"expires_at"`
	}
	if err := json.Unmarshal(body, &entries); err != nil {
		return nil, fmt.Errorf("parsing grok CLI credentials: %w", err)
	}

	for key, entry := range entries {
		// Only adopt entries this build can actually use: a token for a
		// different issuer or client would be sent to the same endpoints but
		// rejected, which reads as a mysterious 401.
		if !strings.HasPrefix(key, "https://auth.x.ai") {
			continue
		}
		if !IsXAISubscriptionToken(entry.Key) {
			continue
		}
		set := &XAITokenSet{
			AccessToken:  entry.Key,
			RefreshToken: entry.RefreshToken,
			ExpiresAt:    XAITokenExpiry(entry.Key),
			AccountID:    XAIPrincipalID(entry.Key),
		}
		if entry.ExpiresAt != "" {
			if ts, err := time.Parse(time.RFC3339, entry.ExpiresAt); err == nil {
				set.ExpiresAt = ts.UTC()
			}
		}
		return set, nil
	}
	return nil, nil
}

// --- OnToken persistence hook ---

// persistXAITokens is installed as the xAI provider's OnToken hook so a
// successful login writes the refresh token and expiry alongside the access
// token. Without it a subscription login would die six hours later.
func persistXAITokens(tok *TokenResponse) error {
	if tok == nil || tok.AccessToken == "" {
		return fmt.Errorf("login returned no xAI access token")
	}
	return SaveXAITokens(xaiTokenSetFromResponse(tok))
}

// xaiProvider returns the OAuth provider definition for the xAI subscription
// flow. Device flow is preferred over PKCE: it is RFC 8628, needs no local
// callback listener, and works unchanged over SSH or in a container.
func xaiProvider() Provider {
	return Provider{
		Name:     "xai",
		EnvVar:   xaiAuthEnvVar,
		AuthURL:  xaiAuthURL,
		TokenURL: xaiTokenURL,
		ClientID: xaiClientID,
		Scopes:   strings.Fields(xaiScopes),
		TokenToKey: func(tok *TokenResponse) string {
			// The OAuth access token is the credential. Unlike the Anthropic
			// flow there is no key-exchange endpoint: the subscription bearer
			// is presented to the Grok CLI proxy as-is.
			return tok.AccessToken
		},
		KeyPageURL:      "https://console.x.ai",
		UseDeviceFlow:   true,
		DeviceURL:       xaiDeviceURL,
		DeviceVerifyURL: "https://accounts.x.ai/oauth2/device",
		OnToken:         persistXAITokens,
	}
}
