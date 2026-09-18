package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/dimetron/pi-go/internal/testenv"
)

// makeXAIToken builds a JWT-shaped token with the given claims. Signature is
// opaque — nothing here verifies it, matching how pi-go treats these tokens.
func makeXAIToken(t *testing.T, claims map[string]any) string {
	t.Helper()
	enc := func(v any) string {
		body, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		return base64.RawURLEncoding.EncodeToString(body)
	}
	return enc(map[string]string{"alg": "RS256", "typ": "JWT"}) + "." +
		enc(claims) + ".signature-not-verified"
}

func TestIsXAISubscriptionToken(t *testing.T) {
	subTok := makeXAIToken(t, map[string]any{
		"iss":   "https://auth.x.ai",
		"scope": "openid offline_access grok-cli:access api:access",
		"exp":   time.Now().Add(time.Hour).Unix(),
	})
	scopedTok := makeXAIToken(t, map[string]any{
		// No issuer claim, but the grok-cli scope is present.
		"scope": "openid grok-cli:access",
		"exp":   time.Now().Add(time.Hour).Unix(),
	})

	cases := []struct {
		name string
		key  string
		want bool
	}{
		{"subscription token by issuer", subTok, true},
		{"subscription token by scope", scopedTok, true},
		{"developer api key", "xai-abcdef123456", false},
		{"empty", "", false},
		{"opaque string", "not-a-jwt", false},
		{"codex oauth jwt is not xai", makeXAIToken(t, map[string]any{
			"https://api.openai.com/auth": map[string]any{"chatgpt_account_id": "x"},
		}), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsXAISubscriptionToken(tc.key); got != tc.want {
				t.Errorf("IsXAISubscriptionToken(%q) = %v, want %v", tc.key, got, tc.want)
			}
		})
	}
}

func TestXAITokenExpiry(t *testing.T) {
	want := time.Now().Add(2 * time.Hour).Truncate(time.Second)
	tok := makeXAIToken(t, map[string]any{"exp": want.Unix()})
	got := XAITokenExpiry(tok)
	if !got.Equal(want) {
		t.Errorf("XAITokenExpiry = %v, want %v", got, want)
	}
	if got := XAITokenExpiry("xai-key"); !got.IsZero() {
		t.Errorf("XAITokenExpiry(non-jwt) = %v, want zero", got)
	}
}

func TestXAIPrincipalID(t *testing.T) {
	tok := makeXAIToken(t, map[string]any{"principal_id": "3feefecd-863d", "sub": "other"})
	if got := XAIPrincipalID(tok); got != "3feefecd-863d" {
		t.Errorf("XAIPrincipalID = %q, want principal_id claim", got)
	}
	subOnly := makeXAIToken(t, map[string]any{"sub": "sub-value"})
	if got := XAIPrincipalID(subOnly); got != "sub-value" {
		t.Errorf("XAIPrincipalID = %q, want sub fallback", got)
	}
}

// TestXAITokenSetExpired pins the freshness rule: a token inside the refresh
// skew is treated as stale so it cannot lapse mid-request, and a token with no
// expiry is never trusted.
func TestXAITokenSetExpired(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name string
		set  *XAITokenSet
		want bool
	}{
		{"nil set", nil, true},
		{"no access token", &XAITokenSet{ExpiresAt: now.Add(time.Hour)}, true},
		{"no expiry", &XAITokenSet{AccessToken: "t"}, true},
		{"well in future", &XAITokenSet{AccessToken: "t", ExpiresAt: now.Add(time.Hour)}, false},
		{"inside skew", &XAITokenSet{AccessToken: "t", ExpiresAt: now.Add(time.Minute)}, true},
		{"already expired", &XAITokenSet{AccessToken: "t", ExpiresAt: now.Add(-time.Minute)}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.set.Expired(now); got != tc.want {
				t.Errorf("Expired = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestXAITokenSetRefreshable(t *testing.T) {
	if (&XAITokenSet{AccessToken: "t"}).Refreshable() {
		t.Error("set without refresh token should not be refreshable")
	}
	if !(&XAITokenSet{AccessToken: "t", RefreshToken: "r"}).Refreshable() {
		t.Error("set with refresh token should be refreshable")
	}
	if (*XAITokenSet)(nil).Refreshable() {
		t.Error("nil set should not be refreshable")
	}
}

// TestXAITokenStoreRoundTrip covers save/load/clear and the file mode, since a
// world-readable refresh token would be a credential leak.
func TestXAITokenStoreRoundTrip(t *testing.T) {
	home := t.TempDir()
	testenv.SetHome(t, home)

	if got, err := LoadXAITokens(); err != nil || got != nil {
		t.Fatalf("LoadXAITokens on missing store = (%v, %v), want (nil, nil)", got, err)
	}

	want := &XAITokenSet{
		AccessToken:  "access-1",
		RefreshToken: "refresh-1",
		TokenType:    "Bearer",
		ExpiresAt:    time.Now().Add(time.Hour).UTC().Truncate(time.Second),
		AccountID:    "acct-1",
	}
	if err := SaveXAITokens(want); err != nil {
		t.Fatalf("SaveXAITokens: %v", err)
	}

	got, err := LoadXAITokens()
	if err != nil {
		t.Fatalf("LoadXAITokens: %v", err)
	}
	if got.AccessToken != want.AccessToken || got.RefreshToken != want.RefreshToken {
		t.Errorf("round trip mismatch: got %+v, want %+v", got, want)
	}
	if !got.ExpiresAt.Equal(want.ExpiresAt) {
		t.Errorf("ExpiresAt = %v, want %v", got.ExpiresAt, want.ExpiresAt)
	}

	if err := ClearXAITokens(); err != nil {
		t.Fatalf("ClearXAITokens: %v", err)
	}
	if got, err := LoadXAITokens(); err != nil || got != nil {
		t.Errorf("after clear = (%v, %v), want (nil, nil)", got, err)
	}
	// Clearing twice must not error — logout is not idempotent-sensitive.
	if err := ClearXAITokens(); err != nil {
		t.Errorf("second ClearXAITokens: %v", err)
	}
}

// TestXAITokenStorePermissions pins the file and directory modes. Split from
// the round-trip test because POSIX permission bits are not meaningful on
// Windows, where the same assertions would fail for reasons that have nothing
// to do with the code under test.
func TestXAITokenStorePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not meaningful on Windows")
	}
	home := t.TempDir()
	testenv.SetHome(t, home)

	if err := SaveXAITokens(&XAITokenSet{
		AccessToken:  "access-1",
		RefreshToken: "refresh-1",
		ExpiresAt:    time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("SaveXAITokens: %v", err)
	}

	path, err := XAITokenStorePath()
	if err != nil {
		t.Fatalf("XAITokenStorePath: %v", err)
	}
	// The refresh token is a durable credential, so the file must not be
	// group- or world-readable.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat token store: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("token store mode = %o, want 0600", perm)
	}

	di, err := os.Stat(filepath.Join(home, ".pi-go"))
	if err != nil {
		t.Fatalf("stat token dir: %v", err)
	}
	if perm := di.Mode().Perm(); perm != 0700 {
		t.Errorf("token dir mode = %o, want 0700", perm)
	}

	// The atomic write must not leave a temp file behind.
	entries, err := os.ReadDir(filepath.Join(home, ".pi-go"))
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp") {
			t.Errorf("leftover temp file: %s", e.Name())
		}
	}
}

func TestSaveXAITokensRejectsEmpty(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := SaveXAITokens(nil); err == nil {
		t.Error("SaveXAITokens(nil) should error")
	}
	if err := SaveXAITokens(&XAITokenSet{}); err == nil {
		t.Error("SaveXAITokens(empty access token) should error")
	}
}

// TestRefreshXAIToken covers the grant that keeps a 6-hour subscription token
// usable, including the rotated refresh token that must be carried forward.
func TestRefreshXAIToken(t *testing.T) {
	var gotForm map[string]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/x-www-form-urlencoded" {
			t.Errorf("Content-Type = %q, want form-encoded", ct)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm: %v", err)
		}
		gotForm = map[string]string{
			"grant_type":    r.PostForm.Get("grant_type"),
			"refresh_token": r.PostForm.Get("refresh_token"),
			"client_id":     r.PostForm.Get("client_id"),
		}
		tok := makeXAIToken(t, map[string]any{
			"iss":          "https://auth.x.ai",
			"principal_id": "acct-rotated",
			"exp":          time.Now().Add(6 * time.Hour).Unix(),
		})
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  tok,
			"refresh_token": "rotated-refresh",
			"token_type":    "Bearer",
			"expires_in":    21600,
		})
	}))
	defer server.Close()

	restore := setXAIEndpoints(t, server.URL, server.URL, server.URL)
	defer restore()

	set, err := RefreshXAIToken(context.Background(), "original-refresh")
	if err != nil {
		t.Fatalf("RefreshXAIToken: %v", err)
	}
	if gotForm["grant_type"] != "refresh_token" {
		t.Errorf("grant_type = %q, want refresh_token", gotForm["grant_type"])
	}
	if gotForm["refresh_token"] != "original-refresh" {
		t.Errorf("refresh_token = %q, want original-refresh", gotForm["refresh_token"])
	}
	if gotForm["client_id"] != xaiClientID {
		t.Errorf("client_id = %q, want %q", gotForm["client_id"], xaiClientID)
	}
	if set.RefreshToken != "rotated-refresh" {
		t.Errorf("RefreshToken = %q, want the rotated value", set.RefreshToken)
	}
	if set.AccountID != "acct-rotated" {
		t.Errorf("AccountID = %q, want acct-rotated", set.AccountID)
	}
	if remaining := time.Until(set.ExpiresAt); remaining < 5*time.Hour {
		t.Errorf("ExpiresAt = %v, want ~6h out", set.ExpiresAt)
	}
}

// TestRefreshXAITokenKeepsOldRefreshWhenOmitted: a response without a rotated
// refresh token must not wipe the one that worked, or the next refresh fails.
func TestRefreshXAITokenKeepsOldRefreshWhenOmitted(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": makeXAIToken(t, map[string]any{"exp": time.Now().Add(time.Hour).Unix()}),
			"expires_in":   3600,
		})
	}))
	defer server.Close()
	restore := setXAIEndpoints(t, server.URL, server.URL, server.URL)
	defer restore()

	set, err := RefreshXAIToken(context.Background(), "keep-me")
	if err != nil {
		t.Fatalf("RefreshXAIToken: %v", err)
	}
	if set.RefreshToken != "keep-me" {
		t.Errorf("RefreshToken = %q, want keep-me retained", set.RefreshToken)
	}
}

func TestRefreshXAITokenErrors(t *testing.T) {
	if _, err := RefreshXAIToken(context.Background(), ""); err == nil {
		t.Error("empty refresh token should error")
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
	}))
	defer server.Close()
	restore := setXAIEndpoints(t, server.URL, server.URL, server.URL)
	defer restore()

	_, err := RefreshXAIToken(context.Background(), "dead-refresh")
	if err == nil {
		t.Fatal("expected error for 400 response")
	}
	if !strings.Contains(err.Error(), "invalid_grant") {
		t.Errorf("error = %v, want it to carry the server reason", err)
	}
}

// TestResolveXAICredential pins the precedence that keeps refresh from ever
// overriding an operator's explicitly configured developer key.
func TestResolveXAICredential(t *testing.T) {
	t.Run("developer key is returned untouched", func(t *testing.T) {
		testenv.SetHome(t, t.TempDir())
		unsetXAIKey(t)
		t.Setenv("XAI_API_KEY", "xai-real-key")

		key, sub, err := ResolveXAICredential(context.Background())
		if err != nil {
			t.Fatalf("ResolveXAICredential: %v", err)
		}
		if key != "xai-real-key" {
			t.Errorf("key = %q, want the developer key", key)
		}
		if sub {
			t.Error("developer key must not be reported as a subscription token")
		}
	})

	t.Run("fresh stored subscription token is used without refreshing", func(t *testing.T) {
		testenv.SetHome(t, t.TempDir())
		unsetXAIKey(t)
		fresh := makeXAIToken(t, map[string]any{
			"iss": "https://auth.x.ai",
			"exp": time.Now().Add(4 * time.Hour).Unix(),
		})
		if err := SaveXAITokens(&XAITokenSet{
			AccessToken:  fresh,
			RefreshToken: "r",
			ExpiresAt:    time.Now().Add(4 * time.Hour),
		}); err != nil {
			t.Fatalf("SaveXAITokens: %v", err)
		}
		// Point the token endpoint at a server that must not be called.
		called := false
		server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			called = true
		}))
		defer server.Close()
		restore := setXAIEndpoints(t, server.URL, server.URL, server.URL)
		defer restore()

		key, sub, err := ResolveXAICredential(context.Background())
		if err != nil {
			t.Fatalf("ResolveXAICredential: %v", err)
		}
		if key != fresh {
			t.Error("expected the stored fresh token")
		}
		if !sub {
			t.Error("expected subscription=true")
		}
		if called {
			t.Error("a fresh token must not trigger a refresh request")
		}
	})

	t.Run("expired stored token is refreshed and persisted", func(t *testing.T) {
		testenv.SetHome(t, t.TempDir())
		unsetXAIKey(t)
		stale := makeXAIToken(t, map[string]any{
			"iss": "https://auth.x.ai",
			"exp": time.Now().Add(-time.Hour).Unix(),
		})
		if err := SaveXAITokens(&XAITokenSet{
			AccessToken:  stale,
			RefreshToken: "good-refresh",
			ExpiresAt:    time.Now().Add(-time.Hour),
		}); err != nil {
			t.Fatalf("SaveXAITokens: %v", err)
		}
		refreshed := makeXAIToken(t, map[string]any{
			"iss": "https://auth.x.ai",
			"exp": time.Now().Add(6 * time.Hour).Unix(),
		})
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token":  refreshed,
				"refresh_token": "rotated",
				"expires_in":    21600,
			})
		}))
		defer server.Close()
		restore := setXAIEndpoints(t, server.URL, server.URL, server.URL)
		defer restore()

		key, sub, err := ResolveXAICredential(context.Background())
		if err != nil {
			t.Fatalf("ResolveXAICredential: %v", err)
		}
		if key != refreshed {
			t.Error("expected the refreshed token")
		}
		if !sub {
			t.Error("expected subscription=true")
		}
		// The rotated refresh token must have been persisted, or the next
		// refresh would present a spent token.
		stored, err := LoadXAITokens()
		if err != nil {
			t.Fatalf("LoadXAITokens: %v", err)
		}
		if stored.RefreshToken != "rotated" {
			t.Errorf("stored refresh token = %q, want rotated", stored.RefreshToken)
		}
	})

	t.Run("no credential configured", func(t *testing.T) {
		testenv.SetHome(t, t.TempDir())
		unsetXAIKey(t)
		key, sub, err := ResolveXAICredential(context.Background())
		if err != nil {
			t.Fatalf("ResolveXAICredential: %v", err)
		}
		if key != "" || sub {
			t.Errorf("got (%q, %v), want empty and false", key, sub)
		}
	})
}

// TestImportGrokCLITokens covers adopting an existing `grok login` session, so
// a user who already authenticated with the official CLI need not log in twice.
func TestImportGrokCLITokens(t *testing.T) {
	home := t.TempDir()
	testenv.SetHome(t, home)

	t.Run("missing file", func(t *testing.T) {
		set, err := ImportGrokCLITokens()
		if err != nil || set != nil {
			t.Fatalf("got (%v, %v), want (nil, nil)", set, err)
		}
	})

	t.Run("adopts the auth.x.ai entry", func(t *testing.T) {
		dir := filepath.Join(home, ".grok")
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		exp := time.Now().Add(3 * time.Hour).UTC().Truncate(time.Second)
		tok := makeXAIToken(t, map[string]any{
			"iss":          "https://auth.x.ai",
			"principal_id": "acct-from-cli",
			"exp":          exp.Unix(),
		})
		body := map[string]any{
			// A foreign entry must be ignored in favor of the xAI one.
			"https://other.example::client": map[string]any{"key": "nope"},
			"https://auth.x.ai::" + xaiClientID: map[string]any{
				"key":           tok,
				"refresh_token": "cli-refresh",
				"expires_at":    exp.Format(time.RFC3339),
				"user_id":       "ignored",
			},
		}
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "auth.json"), raw, 0600); err != nil {
			t.Fatalf("write: %v", err)
		}

		set, err := ImportGrokCLITokens()
		if err != nil {
			t.Fatalf("ImportGrokCLITokens: %v", err)
		}
		if set == nil {
			t.Fatal("expected to adopt the grok CLI token")
		}
		if set.AccessToken != tok {
			t.Error("access token mismatch")
		}
		if set.RefreshToken != "cli-refresh" {
			t.Errorf("RefreshToken = %q, want cli-refresh", set.RefreshToken)
		}
		if set.AccountID != "acct-from-cli" {
			t.Errorf("AccountID = %q, want acct-from-cli", set.AccountID)
		}
	})

	t.Run("ignores a file with no xAI entry", func(t *testing.T) {
		dir := filepath.Join(home, ".grok")
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "auth.json"),
			[]byte(`{"https://other.example::c":{"key":"a.b.c"}}`), 0600); err != nil {
			t.Fatalf("write: %v", err)
		}
		set, err := ImportGrokCLITokens()
		if err != nil {
			t.Fatalf("ImportGrokCLITokens: %v", err)
		}
		if set != nil {
			t.Errorf("got %+v, want nil for a non-xAI entry", set)
		}
	})
}

// TestXAIProviderRegistered pins the provider definition the login flows use.
func TestXAIProviderRegistered(t *testing.T) {
	p, ok := FindProvider("xai")
	if !ok {
		t.Fatal("xai provider is not registered")
	}
	if p.EnvVar != xaiAuthEnvVar {
		t.Errorf("EnvVar = %q, want %q", p.EnvVar, xaiAuthEnvVar)
	}
	if !p.UseDeviceFlow {
		t.Error("xai should prefer the device flow: it needs no localhost listener")
	}
	if p.TokenURL != xaiTokenURL {
		t.Errorf("TokenURL = %q, want %q", p.TokenURL, xaiTokenURL)
	}
	if p.OnToken == nil {
		t.Fatal("xai must install OnToken, or a 6-hour token cannot be refreshed")
	}
	// The access token itself is the credential — there is no key exchange.
	got := p.TokenToKey(&TokenResponse{AccessToken: "sub-token"})
	if got != "sub-token" {
		t.Errorf("TokenToKey = %q, want the raw access token", got)
	}
}

// TestPersistXAITokens verifies the OnToken hook writes refreshable credentials.
func TestPersistXAITokens(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	tok := makeXAIToken(t, map[string]any{
		"iss":          "https://auth.x.ai",
		"principal_id": "acct-hook",
		"exp":          time.Now().Add(6 * time.Hour).Unix(),
	})
	if err := persistXAITokens(&TokenResponse{
		AccessToken:  tok,
		RefreshToken: "hook-refresh",
		ExpiresIn:    21600,
	}); err != nil {
		t.Fatalf("persistXAITokens: %v", err)
	}
	stored, err := LoadXAITokens()
	if err != nil {
		t.Fatalf("LoadXAITokens: %v", err)
	}
	if stored.RefreshToken != "hook-refresh" {
		t.Errorf("RefreshToken = %q, want hook-refresh", stored.RefreshToken)
	}
	if stored.AccountID != "acct-hook" {
		t.Errorf("AccountID = %q, want acct-hook", stored.AccountID)
	}
	if stored.Expired(time.Now()) {
		t.Error("a token expiring in 6 hours should not be stale")
	}

	if err := persistXAITokens(nil); err == nil {
		t.Error("persistXAITokens(nil) should error")
	}
}

// unsetXAIKey clears XAI_API_KEY for the duration of a test. t.Setenv does not
// remove a variable, only overwrite it, so an empty value must be set
// explicitly to model "no developer key configured".
func unsetXAIKey(t *testing.T) {
	t.Helper()
	t.Setenv(xaiAuthEnvVar, "")
}

// setXAIEndpoints points the xAI OAuth endpoints at a test server, returning a
// restore func. Kept here so the tests share one mechanism.
func setXAIEndpoints(t *testing.T, authURL, tokenURL, deviceURL string) func() {
	t.Helper()
	prevAuth, prevToken, prevDevice := xaiAuthURL, xaiTokenURL, xaiDeviceURL
	xaiAuthURL, xaiTokenURL, xaiDeviceURL = authURL, tokenURL, deviceURL
	return func() {
		xaiAuthURL, xaiTokenURL, xaiDeviceURL = prevAuth, prevToken, prevDevice
	}
}

// TestDeviceFlowInvokesOnToken guards the hook that persists the refresh token.
// The device flow is the path `pi login xai` actually takes, so a missing
// OnToken call here ships a login whose credential cannot be refreshed — and
// nothing else would notice, because the login itself still succeeds.
func TestDeviceFlowInvokesOnToken(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/device/code"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"device_code":      "dev-code",
				"user_code":        "ABCD-1234",
				"verification_uri": srv.URL + "/verify",
				"expires_in":       900,
				"interval":         1,
			})
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token":  makeXAIToken(t, map[string]any{"iss": "https://auth.x.ai", "exp": time.Now().Add(time.Hour).Unix()}),
				"refresh_token": "device-flow-refresh",
				"expires_in":    21600,
			})
		}
	}))
	defer srv.Close()

	restore := setXAIEndpoints(t, srv.URL, srv.URL, srv.URL+"/device/code")
	defer restore()

	prov := xaiProvider()
	dev, err := DeviceFlow(context.Background(), prov)
	if err != nil {
		t.Fatalf("DeviceFlow: %v", err)
	}
	if dev.UserCode != "ABCD-1234" {
		t.Fatalf("UserCode = %q, want ABCD-1234", dev.UserCode)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := PollDeviceToken(ctx, prov, dev)
	if err != nil {
		t.Fatalf("PollDeviceToken: %v", err)
	}
	if res.Err != nil {
		t.Fatalf("device flow failed: %v", res.Err)
	}
	if res.APIKey == "" {
		t.Fatal("no api key returned")
	}
	// Assert the refresh token reached the store — the whole point of OnToken.
	set, err := LoadXAITokens()
	if err != nil {
		t.Fatalf("LoadXAITokens: %v", err)
	}
	if set == nil {
		t.Fatal("device flow did not persist the token set: OnToken was not invoked")
	}
	if set.RefreshToken != "device-flow-refresh" {
		t.Errorf("stored refresh token = %q, want device-flow-refresh", set.RefreshToken)
	}
}

// TestDeviceFlowOnTokenFailureIsReported covers the other half of the OnToken
// contract: when the hook cannot persist the credential, the login must fail
// rather than report success. A "successful" login whose refresh token was
// never written stops working six hours later with no explanation.
//
// The hook here is a stub rather than a filesystem fault, because the point is
// the call site's error handling, not persistXAITokens' internals — those are
// covered by TestPersistXAITokens.
func TestDeviceFlowOnTokenFailureIsReported(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/device/code") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"device_code":      "dev-code",
				"user_code":        "ABCD-1234",
				"verification_uri": srv.URL + "/verify",
				"expires_in":       900,
				"interval":         1,
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  makeXAIToken(t, map[string]any{"iss": "https://auth.x.ai", "exp": time.Now().Add(time.Hour).Unix()}),
			"refresh_token": "device-flow-refresh",
			"expires_in":    21600,
		})
	}))
	defer srv.Close()

	restore := setXAIEndpoints(t, srv.URL, srv.URL, srv.URL+"/device/code")
	defer restore()

	prov := xaiProvider()
	// The provider's real hook writes to disk; replace it with one that fails,
	// which is the condition the call site has to handle.
	prov.OnToken = func(*TokenResponse) error { return errors.New("disk full") }

	dev, err := DeviceFlow(context.Background(), prov)
	if err != nil {
		t.Fatalf("DeviceFlow: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := PollDeviceToken(ctx, prov, dev)
	if err != nil {
		t.Fatalf("PollDeviceToken: %v", err)
	}
	if res.Err == nil {
		t.Fatal("a failed OnToken must fail the login, not report success")
	}
	if !strings.Contains(res.Err.Error(), "persisting token") {
		t.Errorf("Err = %v, want it to name the persistence phase", res.Err)
	}
	if !strings.Contains(res.Err.Error(), "disk full") {
		t.Errorf("Err = %v, want it to carry the hook's reason", res.Err)
	}
}

// TestDecodeJWTPayloadRejectsMalformed pins that a non-JWT, a wrong segment
// count, a non-base64 payload and a non-JSON payload are all rejected rather
// than yielding partial claims. A false positive here would route an opaque
// credential to the subscription proxy.
func TestDecodeJWTPayloadRejectsMalformed(t *testing.T) {
	padded := base64.URLEncoding.EncodeToString([]byte(`{"iss":"https://auth.x.ai"}`))
	cases := []struct {
		name  string
		token string
		want  bool
	}{
		{"empty", "", false},
		{"two segments", "a.b", false},
		{"four segments", "a.b.c.d", false},
		{"not base64", "a.!!!not-base64!!!.c", false},
		{"base64 but not json", "a." + base64.RawURLEncoding.EncodeToString([]byte("nope")) + ".c", false},
		{"padded base64 payload", "a." + padded + ".c", true},
		{"valid", makeXAIToken(t, map[string]any{"iss": "https://auth.x.ai"}), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			claims, ok := decodeJWTPayload(tc.token)
			if ok != tc.want {
				t.Fatalf("decodeJWTPayload ok = %v, want %v", ok, tc.want)
			}
			if ok && claims == nil {
				t.Error("ok=true but claims is nil")
			}
		})
	}
}

// TestXAITokenExpiryRejectsBadClaims covers the shapes that must not be read as
// a valid expiry — a wrong type, a non-positive value, or a missing claim. Any
// of these read as "expired", which is the safe direction.
func TestXAITokenExpiryRejectsBadClaims(t *testing.T) {
	cases := []struct {
		name string
		tok  string
	}{
		{"exp as string", makeXAIToken(t, map[string]any{"exp": "soon"})},
		{"exp zero", makeXAIToken(t, map[string]any{"exp": 0})},
		{"exp negative", makeXAIToken(t, map[string]any{"exp": -1})},
		{"no exp", makeXAIToken(t, map[string]any{"iss": "https://auth.x.ai"})},
		{"not a jwt", "opaque"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := XAITokenExpiry(tc.tok); !got.IsZero() {
				t.Errorf("XAITokenExpiry = %v, want zero time", got)
			}
		})
	}
	// A valid principal claim must be ignored when it is not a string.
	if got := XAIPrincipalID(makeXAIToken(t, map[string]any{"principal_id": 42, "sub": "s"})); got != "s" {
		t.Errorf("XAIPrincipalID = %q, want the sub fallback", got)
	}
	if got := XAIPrincipalID("not-a-jwt"); got != "" {
		t.Errorf("XAIPrincipalID(non-jwt) = %q, want empty", got)
	}
}

// TestImportGrokCLITokensErrors covers the unreadable and unparseable cases.
// Both must surface rather than silently looking like "not logged in", which
// would send the user through a needless second login.
func TestImportGrokCLITokensErrors(t *testing.T) {
	home := t.TempDir()
	testenv.SetHome(t, home)
	dir := filepath.Join(home, ".grok")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, "auth.json")

	if err := os.WriteFile(path, []byte("not json at all"), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := ImportGrokCLITokens(); err == nil {
		t.Error("expected an error for an unparseable auth.json")
	}

	// A directory in place of the file is a read error, not a missing file.
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if _, err := ImportGrokCLITokens(); err == nil {
		t.Error("expected an error when auth.json is a directory")
	}
}

// TestLoadXAITokensRejectsCorruptStore: a corrupt store must be reported, not
// mistaken for "no credentials".
func TestLoadXAITokensRejectsCorruptStore(t *testing.T) {
	home := t.TempDir()
	testenv.SetHome(t, home)
	if err := os.MkdirAll(filepath.Join(home, ".pi-go"), 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(home, ".pi-go", xaiTokenFileName)
	if err := os.WriteFile(path, []byte("{broken"), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := LoadXAITokens(); err == nil {
		t.Error("expected an error for a corrupt token store")
	}
}

// TestRefreshXAITokenRejectsEmptyAccessToken: a 200 response with no access
// token must not be persisted as a usable credential.
func TestRefreshXAITokenRejectsEmptyAccessToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"refresh_token": "rotated"})
	}))
	defer server.Close()
	restore := setXAIEndpoints(t, server.URL, server.URL, server.URL)
	defer restore()

	if _, err := RefreshXAIToken(context.Background(), "r"); err == nil {
		t.Error("expected an error when the response carries no access token")
	}
}

// TestRefreshXAITokenRejectsNonJSON: a non-JSON 200 must be an error, not an
// empty credential that overwrites a working one.
func TestRefreshXAITokenRejectsNonJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<html>proxy error</html>"))
	}))
	defer server.Close()
	restore := setXAIEndpoints(t, server.URL, server.URL, server.URL)
	defer restore()

	if _, err := RefreshXAIToken(context.Background(), "r"); err == nil {
		t.Error("expected an error for a non-JSON token response")
	}
}

// TestXaiTokenSetFromResponseFallsBackToJWTExpiry: when the token endpoint
// omits expires_in, the expiry must come from the JWT's own exp claim rather
// than defaulting to the zero time, which would mark every token stale and
// refresh on every single request.
func TestXaiTokenSetFromResponseFallsBackToJWTExpiry(t *testing.T) {
	want := time.Now().Add(3 * time.Hour).Truncate(time.Second)
	tok := makeXAIToken(t, map[string]any{
		"iss": "https://auth.x.ai",
		"exp": want.Unix(),
	})

	// With expires_in: derived from the response.
	withExpiresIn := xaiTokenSetFromResponse(&TokenResponse{AccessToken: tok, ExpiresIn: 3600})
	if remaining := time.Until(withExpiresIn.ExpiresAt); remaining < 55*time.Minute || remaining > 65*time.Minute {
		t.Errorf("expires_in path gave %s, want ~1h", remaining)
	}

	// Without expires_in: derived from the JWT.
	withoutExpiresIn := xaiTokenSetFromResponse(&TokenResponse{AccessToken: tok})
	if !withoutExpiresIn.ExpiresAt.Equal(want) {
		t.Errorf("ExpiresAt = %v, want the JWT exp %v", withoutExpiresIn.ExpiresAt, want)
	}
	if withoutExpiresIn.Expired(time.Now()) {
		t.Error("a token expiring in 3h must not be reported stale")
	}
}

// TestResolveXAICredentialEnvOnly covers the env-token paths that the store
// tests do not reach: a fresh env token is used directly, and an env token
// with no stored refresh token is handed back rather than reported missing.
func TestResolveXAICredentialEnvOnly(t *testing.T) {
	t.Run("fresh env token is used directly", func(t *testing.T) {
		testenv.SetHome(t, t.TempDir())
		fresh := makeXAIToken(t, map[string]any{
			"iss":          "https://auth.x.ai",
			"principal_id": "acct-env",
			"exp":          time.Now().Add(4 * time.Hour).Unix(),
		})
		t.Setenv("XAI_API_KEY", fresh)

		called := false
		server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
		defer server.Close()
		restore := setXAIEndpoints(t, server.URL, server.URL, server.URL)
		defer restore()

		key, sub, err := ResolveXAICredential(context.Background())
		if err != nil {
			t.Fatalf("ResolveXAICredential: %v", err)
		}
		if key != fresh || !sub {
			t.Errorf("got (%q…, %v), want the fresh env token and true", key[:12], sub)
		}
		if called {
			t.Error("a fresh env token must not trigger a refresh")
		}
	})

	t.Run("expired env token with no refresh token is handed back", func(t *testing.T) {
		testenv.SetHome(t, t.TempDir())
		stale := makeXAIToken(t, map[string]any{
			"iss": "https://auth.x.ai",
			"exp": time.Now().Add(-time.Hour).Unix(),
		})
		t.Setenv("XAI_API_KEY", stale)

		key, sub, err := ResolveXAICredential(context.Background())
		if err != nil {
			t.Fatalf("ResolveXAICredential: %v", err)
		}
		// Handing it back lets the upstream 401 surface instead of a
		// confusing "no credential configured".
		if key != stale {
			t.Error("expected the expired env token to be returned")
		}
		if !sub {
			t.Error("expected subscription=true so the proxy is used")
		}
	})
}

// TestResolveXAICredentialRefreshFailurePaths covers the two failure
// fallbacks: when refresh fails and an expired env token is present, the env
// token is returned so the upstream error surfaces (rather than a misleading
// "no credential"); with no env token the refresh error is reported.
func TestResolveXAICredentialRefreshFailurePaths(t *testing.T) {
	brokenRefresh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
	}))
	defer brokenRefresh.Close()

	seedStaleStore := func(t *testing.T) {
		t.Helper()
		testenv.SetHome(t, t.TempDir())
		if err := SaveXAITokens(&XAITokenSet{
			AccessToken:  makeXAIToken(t, map[string]any{"iss": "https://auth.x.ai", "exp": time.Now().Add(-time.Hour).Unix()}),
			RefreshToken: "dead-refresh",
			ExpiresAt:    time.Now().Add(-time.Hour),
		}); err != nil {
			t.Fatalf("SaveXAITokens: %v", err)
		}
	}

	t.Run("refresh failure falls back to an expired env token", func(t *testing.T) {
		seedStaleStore(t)
		staleEnv := makeXAIToken(t, map[string]any{"iss": "https://auth.x.ai", "exp": time.Now().Add(-time.Hour).Unix()})
		t.Setenv("XAI_API_KEY", staleEnv)
		restore := setXAIEndpoints(t, brokenRefresh.URL, brokenRefresh.URL, brokenRefresh.URL)
		defer restore()

		key, sub, err := ResolveXAICredential(context.Background())
		if err != nil {
			t.Fatalf("a refresh failure with an env token present should not be fatal: %v", err)
		}
		if key != staleEnv {
			t.Error("expected the expired env token to be returned")
		}
		if !sub {
			t.Error("expected subscription=true")
		}
	})

	t.Run("refresh failure with no env token is reported", func(t *testing.T) {
		seedStaleStore(t)
		t.Setenv("XAI_API_KEY", "")
		restore := setXAIEndpoints(t, brokenRefresh.URL, brokenRefresh.URL, brokenRefresh.URL)
		defer restore()

		_, _, err := ResolveXAICredential(context.Background())
		if err == nil {
			t.Fatal("expected the refresh failure to be reported")
		}
		if !strings.Contains(err.Error(), "invalid_grant") {
			t.Errorf("error = %v, want it to carry the server reason", err)
		}
	})
}

// TestResolveXAICredentialUnreadableStore: a corrupt store must not block a
// usable env credential — the store error is logged, not fatal.
func TestResolveXAICredentialUnreadableStore(t *testing.T) {
	home := t.TempDir()
	testenv.SetHome(t, home)
	if err := os.MkdirAll(filepath.Join(home, ".pi-go"), 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, ".pi-go", xaiTokenFileName), []byte("{corrupt"), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}

	fresh := makeXAIToken(t, map[string]any{"iss": "https://auth.x.ai", "exp": time.Now().Add(4 * time.Hour).Unix()})
	t.Setenv("XAI_API_KEY", fresh)

	key, sub, err := ResolveXAICredential(context.Background())
	if err != nil {
		t.Fatalf("a corrupt store must not be fatal when an env token exists: %v", err)
	}
	if key != fresh || !sub {
		t.Error("expected the env token to win")
	}
}

// TestImportGrokCLITokensSkipsNonXAIShapedEntry pins the second skip: an entry
// under the xAI key space whose token is not a subscription token (a developer
// key, say) must not be adopted.
func TestImportGrokCLITokensSkipsNonXAIShapedEntry(t *testing.T) {
	home := t.TempDir()
	testenv.SetHome(t, home)
	dir := filepath.Join(home, ".grok")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := map[string]any{
		"https://auth.x.ai::" + xaiClientID: map[string]any{"key": "xai-developer-key-not-a-jwt"},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), raw, 0600); err != nil {
		t.Fatalf("write: %v", err)
	}

	set, err := ImportGrokCLITokens()
	if err != nil {
		t.Fatalf("ImportGrokCLITokens: %v", err)
	}
	if set != nil {
		t.Errorf("adopted a non-subscription token: %+v", set)
	}
}

// TestImportGrokCLITokensIgnoresStoredExpiryWhenTokenHasNone pins that a
// malformed expires_at does not zero out the expiry — the JWT's own exp claim
// is the fallback.
func TestImportGrokCLITokensIgnoresMalformedExpiry(t *testing.T) {
	home := t.TempDir()
	testenv.SetHome(t, home)
	dir := filepath.Join(home, ".grok")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	want := time.Now().Add(5 * time.Hour).Truncate(time.Second)
	tok := makeXAIToken(t, map[string]any{"iss": "https://auth.x.ai", "exp": want.Unix()})
	body := map[string]any{
		"https://auth.x.ai::" + xaiClientID: map[string]any{
			"key":        tok,
			"expires_at": "not-a-timestamp",
		},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), raw, 0600); err != nil {
		t.Fatalf("write: %v", err)
	}

	set, err := ImportGrokCLITokens()
	if err != nil {
		t.Fatalf("ImportGrokCLITokens: %v", err)
	}
	if set == nil {
		t.Fatal("expected to adopt the token")
	}
	if !set.ExpiresAt.Equal(want) {
		t.Errorf("ExpiresAt = %v, want the JWT exp %v", set.ExpiresAt, want)
	}
}
