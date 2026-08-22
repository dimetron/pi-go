package extension

import (
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

	"golang.org/x/oauth2"
)

// setTestHome points os.UserHomeDir at a fresh temp directory for the test.
// Both HOME (Unix) and USERPROFILE (Windows) are set so the store never
// touches the real user profile on any platform.
func setTestHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return home
}

func TestMCPOAuthTokenFile(t *testing.T) {
	path, err := mcpOAuthTokenFile("My Server/1", "https://a.example.com/mcp")
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Base(path)
	if strings.Contains(base, " ") || strings.Contains(base, "/") {
		t.Errorf("expected sanitized filename, got %q", base)
	}
	if filepath.Ext(path) != ".json" {
		t.Errorf("expected .json suffix, got %q", path)
	}

	// Different URL under the same name must produce a different file.
	otherURL, err := mcpOAuthTokenFile("My Server/1", "https://b.example.com/mcp")
	if err != nil {
		t.Fatal(err)
	}
	if otherURL == path {
		t.Error("same name with different URL must not share a cache file")
	}

	// Different name, similar sanitized form, must not collide either.
	otherName, err := mcpOAuthTokenFile("My-Server-1", "https://a.example.com/mcp")
	if err != nil {
		t.Fatal(err)
	}
	if otherName == path {
		t.Error("distinct server names must not share a cache file")
	}
}

func TestSaveLoadMCPOAuthToken_RoundTrip(t *testing.T) {
	setTestHome(t)

	cfg := &oauth2.Config{
		ClientID: "client-123",
		Endpoint: oauth2.Endpoint{TokenURL: "https://as.example.com/token"},
	}
	tok := &oauth2.Token{
		AccessToken:  "at",
		RefreshToken: "rt",
		TokenType:    "Bearer",
		Expiry:       time.Now().Add(time.Hour),
	}
	if err := saveMCPOAuthToken("cloudflare", "https://srv.example.com/mcp", cfg, tok); err != nil {
		t.Fatal(err)
	}

	ts := loadMCPOAuthTokenSource("cloudflare", "https://srv.example.com/mcp")
	if ts == nil {
		t.Fatal("expected usable token source after save")
	}
	got, err := ts.Token()
	if err != nil {
		t.Fatal(err)
	}
	if got.AccessToken != "at" || got.RefreshToken != "rt" {
		t.Errorf("unexpected token round-trip: %+v", got)
	}
}

func TestLoadMCPOAuthToken_IdentityDrift(t *testing.T) {
	setTestHome(t)
	url := "https://srv.example.com/mcp"

	cfg := &oauth2.Config{ClientID: "c", Endpoint: oauth2.Endpoint{TokenURL: "https://as.example.com/token"}}
	tok := &oauth2.Token{AccessToken: "at", RefreshToken: "rt", Expiry: time.Now().Add(time.Hour)}
	if err := saveMCPOAuthToken("cloudflare", url, cfg, tok); err != nil {
		t.Fatal(err)
	}

	// Retargeted URL: the identity hash differs, so the old entry is never
	// consulted and nothing is returned.
	if ts := loadMCPOAuthTokenSource("cloudflare", "https://evil.example.com/mcp"); ts != nil {
		t.Error("expected nil for retargeted URL")
	}

	// Renamed server: entry must be rejected as well.
	if ts := loadMCPOAuthTokenSource("other", url); ts != nil {
		t.Error("expected nil for renamed server")
	}
}

func TestLoadMCPOAuthToken_Missing(t *testing.T) {
	setTestHome(t)
	if ts := loadMCPOAuthTokenSource("nope", "https://x.example.com"); ts != nil {
		t.Error("expected nil token source with no cached file")
	}
}

func TestLoadMCPOAuthToken_TTLExpired(t *testing.T) {
	setTestHome(t)

	path, err := mcpOAuthTokenFile("stale", "https://s.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	st := mcpOAuthToken{
		Server:      "stale",
		URL:         "https://s.example.com",
		SavedAt:     time.Now().Add(-mcpOAuthTokenTTL - time.Minute),
		AccessToken: "old",
	}
	data, _ := json.Marshal(st)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	if ts := loadMCPOAuthTokenSource("stale", "https://s.example.com"); ts != nil {
		t.Error("expected nil token source past the reuse window")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("expected stale cache file to be removed")
	}
}

func TestPersistingTokenSource_SavesRotatedTokens(t *testing.T) {
	saved := 0
	initial := &oauth2.Token{AccessToken: "at1", RefreshToken: "rt1"}
	ts := newPersistingTokenSource(
		oauth2.StaticTokenSource(initial),
		nil,
		func(*oauth2.Token) error { saved++; return nil },
	)

	// First call sees a change vs the nil snapshot and persists.
	if _, err := ts.Token(); err != nil {
		t.Fatal(err)
	}
	if saved != 1 {
		t.Fatalf("expected 1 save after first Token(), got %d", saved)
	}

	// Unchanged token must not trigger another write.
	if _, err := ts.Token(); err != nil {
		t.Fatal(err)
	}
	if saved != 1 {
		t.Fatalf("expected no extra save for unchanged token, got %d saves", saved)
	}

	// Rotated access token must be persisted.
	rotated := &oauth2.Token{AccessToken: "at2", RefreshToken: "rt1"}
	ts2 := newPersistingTokenSource(oauth2.StaticTokenSource(rotated), initial,
		func(*oauth2.Token) error { saved++; return nil })
	if _, err := ts2.Token(); err != nil {
		t.Fatal(err)
	}
	if saved != 2 {
		t.Fatalf("expected save for rotated token, got %d saves", saved)
	}
}

func TestSaveMCPOAuthToken_IgnoresEmpty(t *testing.T) {
	setTestHome(t)
	if err := saveMCPOAuthToken("x", "https://x", &oauth2.Config{}, &oauth2.Token{}); err != nil {
		t.Fatal(err)
	}
	if err := saveMCPOAuthToken("x", "https://x", &oauth2.Config{}, nil); err != nil {
		t.Fatal(err)
	}
	if ts := loadMCPOAuthTokenSource("x", "https://x"); ts != nil {
		t.Error("expected no token source for empty tokens")
	}
}

// writeMCPOAuthTokenFile writes st verbatim at the cache path computed for
// (server, serverURL), bypassing saveMCPOAuthToken so tests can plant
// malformed or mismatched entries.
func writeMCPOAuthTokenFile(t *testing.T, server, serverURL string, data []byte) string {
	t.Helper()
	path, err := mcpOAuthTokenFile(server, serverURL)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadMCPOAuthToken_IdentityMismatchInFile(t *testing.T) {
	setTestHome(t)
	const server, url = "cloudflare", "https://srv.example.com/mcp"

	// A file at the right path whose payload claims a different identity
	// (e.g. copied from another machine or tampered with) must be rejected
	// and removed.
	data, _ := json.Marshal(mcpOAuthToken{
		Server:      "other",
		URL:         "https://evil.example.com/mcp",
		SavedAt:     time.Now(),
		AccessToken: "at",
		Expiry:      time.Now().Add(time.Hour),
	})
	path := writeMCPOAuthTokenFile(t, server, url, data)

	if ts := loadMCPOAuthTokenSource(server, url); ts != nil {
		t.Error("expected nil token source for mismatched identity")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("expected mismatched cache file to be removed")
	}
}

func TestLoadMCPOAuthToken_CorruptJSON(t *testing.T) {
	setTestHome(t)
	writeMCPOAuthTokenFile(t, "bad", "https://b.example.com", []byte("{not json"))
	if ts := loadMCPOAuthTokenSource("bad", "https://b.example.com"); ts != nil {
		t.Error("expected nil token source for corrupt cache file")
	}
}

func TestLoadMCPOAuthToken_ExpiredWithoutRefresh(t *testing.T) {
	setTestHome(t)
	const server, url = "exp", "https://e.example.com"
	cfg := &oauth2.Config{ClientID: "c", Endpoint: oauth2.Endpoint{TokenURL: "https://as.example.com/token"}}
	tok := &oauth2.Token{AccessToken: "at", Expiry: time.Now().Add(-time.Minute)}
	if err := saveMCPOAuthToken(server, url, cfg, tok); err != nil {
		t.Fatal(err)
	}
	if ts := loadMCPOAuthTokenSource(server, url); ts != nil {
		t.Error("expected nil token source for expired token with no refresh token")
	}
}

func TestLoadMCPOAuthToken_StaticWithoutRefreshConfig(t *testing.T) {
	setTestHome(t)
	const server, url = "static", "https://s.example.com"

	// Valid access token but no refresh token: nothing to refresh with, so
	// the loader hands back a static source for the remaining lifetime.
	tok := &oauth2.Token{AccessToken: "at-static", TokenType: "Bearer", Expiry: time.Now().Add(time.Hour)}
	if err := saveMCPOAuthToken(server, url, &oauth2.Config{}, tok); err != nil {
		t.Fatal(err)
	}
	ts := loadMCPOAuthTokenSource(server, url)
	if ts == nil {
		t.Fatal("expected static token source")
	}
	got, err := ts.Token()
	if err != nil {
		t.Fatal(err)
	}
	if got.AccessToken != "at-static" || got.TokenType != "Bearer" {
		t.Errorf("unexpected token: %+v", got)
	}

	// Refresh token present but no client/token URL recorded: still static,
	// since a refresh cannot be performed without them.
	tok2 := &oauth2.Token{AccessToken: "at2", RefreshToken: "rt2", Expiry: time.Now().Add(time.Hour)}
	if err := saveMCPOAuthToken(server, url, &oauth2.Config{}, tok2); err != nil {
		t.Fatal(err)
	}
	ts2 := loadMCPOAuthTokenSource(server, url)
	if ts2 == nil {
		t.Fatal("expected static token source")
	}
	if _, isPersisting := ts2.(*persistingTokenSource); isPersisting {
		t.Error("expected a static source when refresh config is missing")
	}
}

func TestLoadMCPOAuthToken_RefreshesAndPersistsRotatedToken(t *testing.T) {
	setTestHome(t)
	const server, url = "rot", "https://r.example.com/mcp"

	// Fake token endpoint: every refresh returns a rotated pair.
	var refreshes int
	as := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse form: %v", err)
		}
		if r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("refresh_token") != "rt1" {
			t.Errorf("unexpected refresh request: %v", r.Form)
		}
		refreshes++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"at2","refresh_token":"rt2","token_type":"Bearer","expires_in":3600}`))
	}))
	defer as.Close()

	cfg := &oauth2.Config{ClientID: "client-1", Endpoint: oauth2.Endpoint{TokenURL: as.URL}}
	expired := &oauth2.Token{AccessToken: "at1", RefreshToken: "rt1", Expiry: time.Now().Add(-time.Minute)}
	if err := saveMCPOAuthToken(server, url, cfg, expired); err != nil {
		t.Fatal(err)
	}

	ts := loadMCPOAuthTokenSource(server, url)
	if ts == nil {
		t.Fatal("expected refreshing token source for expired token with refresh token")
	}
	if _, ok := ts.(*persistingTokenSource); !ok {
		t.Fatalf("expected *persistingTokenSource, got %T", ts)
	}
	got, err := ts.Token()
	if err != nil {
		t.Fatal(err)
	}
	if got.AccessToken != "at2" || got.RefreshToken != "rt2" {
		t.Errorf("expected refreshed token at2/rt2, got %+v", got)
	}
	if refreshes != 1 {
		t.Errorf("expected exactly one refresh, got %d", refreshes)
	}

	// The rotated pair must now be on disk: a new load must not need the
	// revoked rt1 any more.
	path, _ := mcpOAuthTokenFile(server, url)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var st mcpOAuthToken
	if err := json.Unmarshal(data, &st); err != nil {
		t.Fatal(err)
	}
	if st.AccessToken != "at2" || st.RefreshToken != "rt2" {
		t.Errorf("expected rotated token persisted, got %+v", st)
	}
	if st.ClientID != "client-1" || st.TokenURL != as.URL {
		t.Errorf("expected refresh config preserved, got client=%q url=%q", st.ClientID, st.TokenURL)
	}
}

type errTokenSource struct{ err error }

func (e errTokenSource) Token() (*oauth2.Token, error) { return nil, e.err }

func TestPersistingTokenSource_InnerErrorPropagates(t *testing.T) {
	saved := false
	ts := newPersistingTokenSource(errTokenSource{err: errors.New("boom")}, nil,
		func(*oauth2.Token) error { saved = true; return nil })
	if _, err := ts.Token(); err == nil || err.Error() != "boom" {
		t.Fatalf("expected inner error, got %v", err)
	}
	if saved {
		t.Error("save must not run when the inner source fails")
	}
}

func TestPersistingTokenSource_SaveErrorIsNonFatal(t *testing.T) {
	ts := newPersistingTokenSource(oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "at"}), nil,
		func(*oauth2.Token) error { return errors.New("disk full") })
	got, err := ts.Token()
	if err != nil {
		t.Fatalf("persistence failure must not fail the token call: %v", err)
	}
	if got.AccessToken != "at" {
		t.Errorf("unexpected token %+v", got)
	}
}

func TestSaveMCPOAuthToken_MkdirError(t *testing.T) {
	home := setTestHome(t)
	// A regular file where the cache directory's parent should be makes
	// MkdirAll fail on every platform.
	if err := os.WriteFile(filepath.Join(home, ".pi-go"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := &oauth2.Config{}
	tok := &oauth2.Token{AccessToken: "at"}
	err := saveMCPOAuthToken("x", "https://x", cfg, tok)
	if err == nil || !strings.Contains(err.Error(), "cache directory") {
		t.Fatalf("expected cache directory error, got %v", err)
	}
}

func TestMCPOAuthTokenFile_NoHomeDir(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	if _, err := mcpOAuthTokenFile("x", "https://x"); err == nil {
		t.Error("expected error when home directory is unknown")
	}
	if ts := loadMCPOAuthTokenSource("x", "https://x"); ts != nil {
		t.Error("expected nil token source when home directory is unknown")
	}
	err := saveMCPOAuthToken("x", "https://x", &oauth2.Config{}, &oauth2.Token{AccessToken: "at"})
	if err == nil {
		t.Error("expected save error when home directory is unknown")
	}
}

func TestSaveMCPOAuthToken_FilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not meaningful on Windows")
	}
	home := setTestHome(t)
	cfg := &oauth2.Config{}
	if err := saveMCPOAuthToken("perm", "https://p.example.com", cfg, &oauth2.Token{AccessToken: "at"}); err != nil {
		t.Fatal(err)
	}
	path, _ := mcpOAuthTokenFile("perm", "https://p.example.com")
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("expected 0600 cache file, got %o", fi.Mode().Perm())
	}
	di, err := os.Stat(filepath.Join(home, ".pi-go", "mcp-oauth"))
	if err != nil {
		t.Fatal(err)
	}
	if di.Mode().Perm() != 0o700 {
		t.Errorf("expected 0700 cache dir, got %o", di.Mode().Perm())
	}
	// No temp file left behind after the atomic rename.
	entries, _ := os.ReadDir(filepath.Dir(path))
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Errorf("temp file left behind: %s", e.Name())
		}
	}
}

func TestSaveMCPOAuthToken_RenameErrorCleansTemp(t *testing.T) {
	setTestHome(t)
	path, err := mcpOAuthTokenFile("clash", "https://c.example.com")
	if err != nil {
		t.Fatal(err)
	}
	// A non-empty directory squatting on the target path makes the final
	// rename fail on every platform; the temp file must not be left behind.
	if err := os.MkdirAll(filepath.Join(path, "child"), 0o700); err != nil {
		t.Fatal(err)
	}
	err = saveMCPOAuthToken("clash", "https://c.example.com", &oauth2.Config{}, &oauth2.Token{AccessToken: "at"})
	if err == nil || !strings.Contains(err.Error(), "installing MCP OAuth cache") {
		t.Fatalf("expected rename error, got %v", err)
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Errorf("temp file left behind: %s", e.Name())
		}
	}
}
