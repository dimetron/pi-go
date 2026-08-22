package extension

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

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
	t.Setenv("HOME", t.TempDir())

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
	t.Setenv("HOME", t.TempDir())
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
	t.Setenv("HOME", t.TempDir())
	if ts := loadMCPOAuthTokenSource("nope", "https://x.example.com"); ts != nil {
		t.Error("expected nil token source with no cached file")
	}
}

func TestLoadMCPOAuthToken_TTLExpired(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

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
	t.Setenv("HOME", t.TempDir())
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
