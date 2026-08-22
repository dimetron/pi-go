package extension

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"

	"github.com/dimetron/pi-go/internal/notice"
)

// mcpOAuthTokenTTL bounds how long a persisted MCP OAuth token is reused.
// After this window the cached credentials are discarded and the next connect
// runs the browser authorization flow again.
const mcpOAuthTokenTTL = 24 * time.Hour

// mcpOAuthToken is the JSON document persisted per MCP server under
// ~/.pi-go/mcp-oauth/. ClientID and TokenURL are captured from the OAuth
// config at authorize time so a later session can rebuild a refreshing token
// source (refreshing needs both) without re-running discovery/registration.
// Server and URL identify the cache entry: a token minted for one endpoint
// must never be replayed against another, so loading validates both against
// the configured server and discards mismatches.
type mcpOAuthToken struct {
	Server       string    `json:"server"`
	URL          string    `json:"url,omitempty"`
	SavedAt      time.Time `json:"saved_at"`
	ClientID     string    `json:"client_id,omitempty"`
	TokenURL     string    `json:"token_url,omitempty"`
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	TokenType    string    `json:"token_type,omitempty"`
	Expiry       time.Time `json:"expiry,omitempty"`
}

// mcpOAuthTokenFile returns the cache-file path for a server identity. The
// name carries a sanitized form of the server name for readability and the
// full SHA-256 of "name\0url" for uniqueness, so distinct identities never
// share a file and renaming or retargeting a server invalidates old entries.
func mcpOAuthTokenFile(server, serverURL string) (string, error) {
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
	sum := sha256.Sum256([]byte(server + "\x00" + serverURL))
	name := fmt.Sprintf("%s-%x.json", sb.String(), sum)
	return filepath.Join(home, ".pi-go", "mcp-oauth", name), nil
}

// loadMCPOAuthTokenSource returns a token source rebuilt from the cached
// credentials for the given server identity, or nil when nothing usable is
// stored (missing, older than mcpOAuthTokenTTL, expired with no refresh
// token, or written for a different server URL).
func loadMCPOAuthTokenSource(server, serverURL string) oauth2.TokenSource {
	path, err := mcpOAuthTokenFile(server, serverURL)
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
	if st.Server != server || st.URL != serverURL {
		_ = os.Remove(path) // identity drift (retargeted or renamed server)
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
	// Wrap the refreshing source so rotated tokens are written back to disk;
	// otherwise a provider that rotates refresh tokens leaves the revoked
	// value persisted and every restart fails to refresh.
	ts := cfg.TokenSource(context.Background(), tok)
	return newPersistingTokenSource(ts, tok, func(t *oauth2.Token) error {
		return saveMCPOAuthToken(server, serverURL, cfg, t)
	})
}

// persistingTokenSource wraps an inner source and persists newly issued
// tokens whenever access or refresh credentials change. Persistence errors
// are reported but do not fail the token call: a lost cache write degrades
// to a future re-authorization, not a broken request.
type persistingTokenSource struct {
	mu     sync.Mutex
	inner  oauth2.TokenSource
	last   *oauth2.Token
	saveFn func(*oauth2.Token) error
}

func newPersistingTokenSource(inner oauth2.TokenSource, last *oauth2.Token, saveFn func(*oauth2.Token) error) oauth2.TokenSource {
	return &persistingTokenSource{inner: inner, last: last, saveFn: saveFn}
}

func (p *persistingTokenSource) Token() (*oauth2.Token, error) {
	tok, err := p.inner.Token()
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	snapshot := p.last
	changed := snapshot == nil ||
		tok.AccessToken != snapshot.AccessToken ||
		tok.RefreshToken != snapshot.RefreshToken ||
		!tok.Expiry.Equal(snapshot.Expiry)
	if changed {
		cp := *tok
		p.last = &cp
	}
	p.mu.Unlock()

	if changed {
		if err := p.saveFn(tok); err != nil {
			notice.Notifyf("could not update cached MCP OAuth token: %v", err)
		}
	}
	return tok, nil
}

// saveMCPOAuthToken persists the token obtained for a server identity, along
// with the OAuth config values needed to refresh it in a later session. The
// write is atomic (temp file + rename in the target directory) and the
// permissions of pre-existing directories are tightened, since MkdirAll modes
// only apply at creation time.
func saveMCPOAuthToken(server, serverURL string, cfg *oauth2.Config, tok *oauth2.Token) error {
	if tok == nil || tok.AccessToken == "" {
		return nil
	}
	path, err := mcpOAuthTokenFile(server, serverURL)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating MCP OAuth cache directory: %w", err)
	}
	_ = os.Chmod(dir, 0o700) // tighten pre-existing directories

	st := mcpOAuthToken{
		Server:       server,
		URL:          serverURL,
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

	// Atomic write: temp file in the target directory, then rename. CreateTemp
	// uses 0600, so the installed file is never more permissive than that.
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-")
	if err != nil {
		return fmt.Errorf("creating MCP OAuth cache temp file: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = tmp.Close(); _ = os.Remove(tmpName) }
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return fmt.Errorf("writing MCP OAuth cache: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("syncing MCP OAuth cache: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("closing MCP OAuth cache: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("installing MCP OAuth cache: %w", err)
	}
	return nil
}

// removeMCPOAuthToken discards the cached credentials for a server identity,
// so the next connect runs the authorization flow instead of replaying a token
// the provider has refused. A missing file is success: the goal is that no
// cached token remains, not that one was deleted.
func removeMCPOAuthToken(server, serverURL string) error {
	path, err := mcpOAuthTokenFile(server, serverURL)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
