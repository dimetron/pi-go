package extension

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/oauth2"
)

// mcpOAuthTokenTTL bounds how long a persisted MCP OAuth token is reused.
// After this window the cached credentials are discarded and the next connect
// runs the browser authorization flow again.
const mcpOAuthTokenTTL = 24 * time.Hour

// mcpOAuthToken is the JSON document persisted per MCP server under
// ~/.pi-go/mcp-oauth/. ClientID and TokenURL are captured from the OAuth
// config at authorize time so a later session can rebuild a refreshing token
// source (refreshing needs both) without re-running discovery/registration.
type mcpOAuthToken struct {
	Server       string    `json:"server"`
	SavedAt      time.Time `json:"saved_at"`
	ClientID     string    `json:"client_id,omitempty"`
	TokenURL     string    `json:"token_url,omitempty"`
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	TokenType    string    `json:"token_type,omitempty"`
	Expiry       time.Time `json:"expiry,omitempty"`
}

// mcpOAuthTokenFile returns the cache-file path for a server. The name is
// sanitized for filesystem use and suffixed with a short hash of the exact
// name so distinct server names never collide on one file.
func mcpOAuthTokenFile(server string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %w", err)
	}
	var sb strings.Builder
	for _, r := range strings.ToLower(server) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' {
			sb.WriteRune(r)
			continue
		}
		sb.WriteByte('-')
	}
	sum := sha256.Sum256([]byte(server))
	name := fmt.Sprintf("%s-%s.json", sb.String(), hex.EncodeToString(sum[:4]))
	return filepath.Join(home, ".pi-go", "mcp-oauth", name), nil
}

// loadMCPOAuthTokenSource returns a token source rebuilt from the cached
// credentials for server, or nil when nothing usable is stored (missing,
// older than mcpOAuthTokenTTL, or expired with no refresh token).
func loadMCPOAuthTokenSource(server string) oauth2.TokenSource {
	path, err := mcpOAuthTokenFile(server)
	if err != nil {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var st mcpOAuthToken
	if err := json.Unmarshal(data, &st); err != nil {
		return nil
	}
	if st.AccessToken == "" || time.Since(st.SavedAt) > mcpOAuthTokenTTL {
		_ = os.Remove(path) // stale beyond the reuse window
		return nil
	}
	tok := &oauth2.Token{
		AccessToken:  st.AccessToken,
		RefreshToken: st.RefreshToken,
		TokenType:    st.TokenType,
		Expiry:       st.Expiry,
	}
	if !tok.Valid() && tok.RefreshToken == "" {
		return nil // expired and cannot refresh
	}
	if st.ClientID == "" || st.TokenURL == "" || tok.RefreshToken == "" {
		return oauth2.StaticTokenSource(tok)
	}
	cfg := &oauth2.Config{
		ClientID: st.ClientID,
		Endpoint: oauth2.Endpoint{TokenURL: st.TokenURL},
	}
	return cfg.TokenSource(context.Background(), tok)
}

// saveMCPOAuthToken persists the token obtained for server, along with the
// OAuth config values needed to refresh it in a later session.
func saveMCPOAuthToken(server string, cfg *oauth2.Config, tok *oauth2.Token) error {
	if tok == nil || tok.AccessToken == "" {
		return nil
	}
	path, err := mcpOAuthTokenFile(server)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("creating MCP OAuth cache directory: %w", err)
	}
	st := mcpOAuthToken{
		Server:       server,
		SavedAt:      time.Now(),
		ClientID:     cfg.ClientID,
		TokenURL:     cfg.Endpoint.TokenURL,
		AccessToken:  tok.AccessToken,
		RefreshToken: tok.RefreshToken,
		TokenType:    tok.TokenType,
		Expiry:       tok.Expiry,
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("writing MCP OAuth cache: %w", err)
	}
	return nil
}
