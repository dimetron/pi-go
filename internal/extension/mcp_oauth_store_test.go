package extension

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

func TestMCPOAuthTokenFile(t *testing.T) {
	path, err := mcpOAuthTokenFile("My Server/1")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) == "My Server/1.json" {
		t.Error("expected sanitized filename")
	}
	if filepath.Ext(path) != ".json" {
		t.Errorf("expected .json suffix, got %q", path)
	}
	other, err := mcpOAuthTokenFile("My-Server-1")
	if err != nil {
		t.Fatal(err)
	}
	if other == path {
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
	if err := saveMCPOAuthToken("cloudflare", cfg, tok); err != nil {
		t.Fatal(err)
	}

	ts := loadMCPOAuthTokenSource("cloudflare")
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

func TestLoadMCPOAuthToken_Missing(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if ts := loadMCPOAuthTokenSource("nope"); ts != nil {
		t.Error("expected nil token source with no cached file")
	}
}

func TestLoadMCPOAuthToken_TTLExpired(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	path, err := mcpOAuthTokenFile("stale")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	st := mcpOAuthToken{
		Server:      "stale",
		SavedAt:     time.Now().Add(-mcpOAuthTokenTTL - time.Minute),
		AccessToken: "old",
	}
	data, _ := json.Marshal(st)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	if ts := loadMCPOAuthTokenSource("stale"); ts != nil {
		t.Error("expected nil token source past the reuse window")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("expected stale cache file to be removed")
	}
}

func TestSaveMCPOAuthToken_IgnoresEmpty(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := saveMCPOAuthToken("x", &oauth2.Config{}, &oauth2.Token{}); err != nil {
		t.Fatal(err)
	}
	if err := saveMCPOAuthToken("x", &oauth2.Config{}, nil); err != nil {
		t.Fatal(err)
	}
	if ts := loadMCPOAuthTokenSource("x"); ts != nil {
		t.Error("expected no token source for empty tokens")
	}
}
