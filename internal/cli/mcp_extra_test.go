package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimetron/pi-go/internal/extension"
)

// stubMCPLogin replaces the authorization flow for the duration of a test so all
// three of the command's outcomes can be driven without an authorization server.
func stubMCPLogin(t *testing.T, fn func(context.Context, extension.MCPServerConfig) error) *extension.MCPServerConfig {
	t.Helper()
	var got extension.MCPServerConfig
	prev := mcpLoginFn
	mcpLoginFn = func(ctx context.Context, srv extension.MCPServerConfig) error {
		got = srv
		if fn != nil {
			return fn(ctx, srv)
		}
		return nil
	}
	t.Cleanup(func() { mcpLoginFn = prev })
	return &got
}

// A fresh login must be reported as authorized, with the promise that the token
// will be reused — that promise is the reason to run the command at all.
func TestMCPLogin_ReportsFreshAuthorization(t *testing.T) {
	home := t.TempDir()
	dir := t.TempDir()
	chdir(t, dir)
	if _, err := runMCPCmd(t, home, "add", "srv", "--url", "https://example.com/mcp"); err != nil {
		t.Fatalf("add: %v", err)
	}
	stubMCPLogin(t, nil)

	out, err := runMCPCmd(t, home, "login", "srv")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if !strings.Contains(out, "Authorized") {
		t.Errorf("output = %q, want a confirmation", out)
	}
	if strings.Contains(out, "Already authorized") {
		t.Errorf("a fresh login was reported as reuse:\n%s", out)
	}
}

// Cached credentials are a success for the user's purpose, so the command must
// exit zero and say so rather than surfacing the sentinel as an error.
func TestMCPLogin_ReportsReusedCredentials(t *testing.T) {
	home := t.TempDir()
	dir := t.TempDir()
	chdir(t, dir)
	if _, err := runMCPCmd(t, home, "add", "srv", "--url", "https://example.com/mcp"); err != nil {
		t.Fatalf("add: %v", err)
	}
	stubMCPLogin(t, func(context.Context, extension.MCPServerConfig) error {
		return extension.ErrMCPLoginReusedToken
	})

	out, err := runMCPCmd(t, home, "login", "srv")
	if err != nil {
		t.Fatalf("reuse must not be an error: %v", err)
	}
	if !strings.Contains(out, "Already authorized") {
		t.Errorf("output = %q, want it to say the credentials were reused", out)
	}
}

// Any other failure is the command's failure: it must propagate so the exit code
// is non-zero.
func TestMCPLogin_PropagatesFailure(t *testing.T) {
	home := t.TempDir()
	dir := t.TempDir()
	chdir(t, dir)
	if _, err := runMCPCmd(t, home, "add", "srv", "--url", "https://example.com/mcp"); err != nil {
		t.Fatalf("add: %v", err)
	}
	sentinel := errors.New("authorization server refused")
	stubMCPLogin(t, func(context.Context, extension.MCPServerConfig) error { return sentinel })

	_, err := runMCPCmd(t, home, "login", "srv")
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want the underlying failure", err)
	}
}

// The flow must receive the server's configured headers and URL, not a
// reconstructed subset: a header dropped here would authorize a different client
// than the one the agent later uses.
func TestMCPLogin_PassesConfiguredServerToTheFlow(t *testing.T) {
	home := t.TempDir()
	dir := t.TempDir()
	chdir(t, dir)
	if _, err := runMCPCmd(t, home, "add", "guarded",
		"--url", "https://example.com/mcp", "--header", "X-Api-Key=abc"); err != nil {
		t.Fatalf("add: %v", err)
	}
	got := stubMCPLogin(t, nil)

	if _, err := runMCPCmd(t, home, "login", "guarded"); err != nil {
		t.Fatalf("login: %v", err)
	}
	if got.Name != "guarded" || got.URL != "https://example.com/mcp" {
		t.Errorf("flow received %+v, want the configured name and url", *got)
	}
	if got.Headers["X-Api-Key"] != "abc" {
		t.Errorf("flow received headers %v, want X-Api-Key", got.Headers)
	}
}

// A URL that cannot be parsed must be rejected with an error rather than stored,
// because the connect failure it causes names nothing the user can act on.
func TestMCPAdd_RejectsUnparseableURL(t *testing.T) {
	home := t.TempDir()
	// A control character makes url.Parse fail.
	_, err := runMCPCmd(t, home, "add", "srv", "--url", "https://exa\x7fmple.com/mcp")
	if err == nil {
		t.Fatal("expected an error for an unparseable URL")
	}
	if !strings.Contains(err.Error(), "invalid --url") {
		t.Errorf("err = %v, want it to name the flag", err)
	}
}

// A URL that is only whitespace is not a transport.
func TestMCPAdd_RejectsBlankName(t *testing.T) {
	home := t.TempDir()
	_, err := runMCPCmd(t, home, "add", "   ", "--url", "https://example.com/mcp")
	if err == nil || !strings.Contains(err.Error(), "name is required") {
		t.Fatalf("err = %v, want a name-required error", err)
	}
}

// --oauth adds the authorize hint: without a cached token the browser would
// otherwise open in the middle of an agent run.
func TestMCPAdd_OAuthSuggestsLogin(t *testing.T) {
	home := t.TempDir()
	dir := t.TempDir()
	chdir(t, dir)

	out, err := runMCPCmd(t, home, "add", "guarded",
		"--url", "https://example.com/mcp", "--oauth")
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if !strings.Contains(out, "pi mcp login guarded") {
		t.Errorf("output = %q, want it to suggest the login command", out)
	}

	entry := readServers(t, filepath.Join(home, ".pi-go", "mcp.json"))["guarded"].(map[string]any)
	if entry["oauth"] != true {
		t.Errorf("oauth = %v, want true", entry["oauth"])
	}
}

// A stdio server gets no such hint: it has no browser flow to offer.
func TestMCPAdd_StdioGetsNoLoginHint(t *testing.T) {
	home := t.TempDir()
	dir := t.TempDir()
	chdir(t, dir)

	out, err := runMCPCmd(t, home, "add", "local", "--command", "npx")
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if strings.Contains(out, "pi mcp login") {
		t.Errorf("output = %q, want no login hint for a stdio server", out)
	}
}

// The list reports each auth state distinctly, since "cached" is what tells a
// headless user the server will work without a browser.
func TestMCPList_AuthStates(t *testing.T) {
	home := t.TempDir()
	dir := t.TempDir()
	chdir(t, dir)
	if _, err := runMCPCmd(t, home, "add", "plain", "--url", "https://a.example/mcp"); err != nil {
		t.Fatalf("add plain: %v", err)
	}
	if _, err := runMCPCmd(t, home, "add", "guarded", "--url", "https://b.example/mcp", "--oauth"); err != nil {
		t.Fatalf("add guarded: %v", err)
	}
	if _, err := runMCPCmd(t, home, "add", "local", "--command", "npx"); err != nil {
		t.Fatalf("add local: %v", err)
	}

	out, err := runMCPCmd(t, home, "list")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, want := range []struct {
		row     string
		substrs []string
	}{
		{"plain", []string{"plain", "http", "none", "https://a.example/mcp"}},
		{"guarded", []string{"guarded", "http", "oauth"}},
		{"local", []string{"local", "stdio", "-", "npx"}},
	} {
		var line string
		for _, l := range strings.Split(out, "\n") {
			if strings.HasPrefix(l, want.row) {
				line = l
			}
		}
		if line == "" {
			t.Fatalf("no row for %q in:\n%s", want.row, out)
		}
		for _, sub := range want.substrs {
			if !strings.Contains(line, sub) {
				t.Errorf("row %q = %q, want it to contain %q", want.row, line, sub)
			}
		}
	}
}

// Rows are sorted so a diff of the output is stable across runs, which matters
// when the file itself is rewritten alphabetically.
func TestMCPList_Sorts(t *testing.T) {
	home := t.TempDir()
	dir := t.TempDir()
	chdir(t, dir)
	for _, name := range []string{"zulu", "alpha", "mike"} {
		if _, err := runMCPCmd(t, home, "add", name, "--url", "https://example.com/mcp"); err != nil {
			t.Fatalf("add %s: %v", name, err)
		}
	}

	out, err := runMCPCmd(t, home, "list")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	iAlpha, iMike, iZulu := strings.Index(out, "alpha"), strings.Index(out, "mike"), strings.Index(out, "zulu")
	if iAlpha >= iMike || iMike >= iZulu {
		t.Errorf("rows are not sorted (alpha=%d mike=%d zulu=%d):\n%s", iAlpha, iMike, iZulu, out)
	}
}

// A remove failure must surface: a silent success hides a typo in the name or the
// wrong scope.
func TestMCPRemove_PropagatesFailure(t *testing.T) {
	home := t.TempDir()
	dir := t.TempDir()
	chdir(t, dir)
	// Nothing has been added, so the global mcp.json does not exist.
	_, err := runMCPCmd(t, home, "remove", "ghost")
	if err == nil {
		t.Fatal("expected an error removing from a missing file")
	}
}

// Adding with --project into a tree whose .pi-go is a file rather than a
// directory must fail rather than silently writing nowhere.
func TestMCPAdd_ReportsUnwritableDestination(t *testing.T) {
	home := t.TempDir()
	dir := t.TempDir()
	blocker := filepath.Join(dir, ".pi-go")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	chdir(t, dir)

	_, err := runMCPCmd(t, home, "add", "srv", "--url", "https://example.com/mcp", "--project")
	if err == nil {
		t.Fatal("expected an error writing into a file")
	}
}
