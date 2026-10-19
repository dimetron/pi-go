package httplog

import (
	"strings"
	"testing"
)

// These URLs are the shapes that actually leaked: a key carried in the query
// string rather than a header, which no header-based redaction can see.
func TestRedactURL(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "tavily api key in query",
			in:   "https://mcp.tavily.com/mcp/?tavilyApiKey=tvly-dev-abc123def456",
			want: "https://mcp.tavily.com/mcp/?tavilyApiKey=%2A%2A%2A",
		},
		{
			name: "exa api key in query",
			in:   "https://mcp.exa.ai/mcp?exaApiKey=9f8e7d6c5b4a",
			want: "https://mcp.exa.ai/mcp?exaApiKey=%2A%2A%2A",
		},
		{
			name: "no query left intact",
			in:   "https://api.githubcopilot.com/mcp/",
			want: "https://api.githubcopilot.com/mcp/",
		},
		{
			name: "non-credential params preserved",
			in:   "https://mcp.exa.ai/mcp?exaApiKey=secret123&profile=research",
			want: "https://mcp.exa.ai/mcp?exaApiKey=%2A%2A%2A&profile=research",
		},
		{
			name: "userinfo password dropped, username kept",
			in:   "https://alice:hunter2@example.com/mcp",
			want: "https://alice@example.com/mcp",
		},
		{
			name: "generic api_key and token",
			in:   "https://example.com/x?api_key=abc&token=def",
			want: "https://example.com/x?api_key=%2A%2A%2A&token=%2A%2A%2A",
		},
		{
			name: "empty stays empty",
			in:   "",
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := RedactURL(tc.in); got != tc.want {
				t.Errorf("RedactURL(%q)\n got: %q\nwant: %q", tc.in, got, tc.want)
			}
		})
	}
}

// The redacted form must not contain the original secret, which is the property
// the test above cannot state directly because it asserts a whole URL.
func TestRedactURL_SecretNeverSurvives(t *testing.T) {
	const secret = "tvly-dev-9a8b7c6d5e4f3a2b1c0d"
	out := RedactURL("https://mcp.tavily.com/mcp/?tavilyApiKey=" + secret)
	if strings.Contains(out, secret) {
		t.Fatalf("secret survived redaction: %q", out)
	}
	if !strings.Contains(out, "tavilyApiKey") {
		t.Errorf("parameter name should survive so the log stays diagnostic: %q", out)
	}
	if !strings.Contains(out, "mcp.tavily.com") {
		t.Errorf("host should survive so the log stays diagnostic: %q", out)
	}
}

// A URL that cannot be parsed has no safe partial form; the query must go.
func TestRedactURL_MalformedDropsQuery(t *testing.T) {
	got := RedactURL("ht!tp://%%%bad?tavilyApiKey=supersecret")
	if strings.Contains(got, "supersecret") {
		t.Fatalf("secret survived on the malformed path: %q", got)
	}
}

// A fragment can carry a token too — OAuth implicit flows put the access_token
// there — so it is dropped rather than logged.
func TestRedactURL_DropsFragment(t *testing.T) {
	got := RedactURL("https://example.com/cb#access_token=abcdef123456")
	if strings.Contains(got, "abcdef123456") {
		t.Fatalf("fragment token survived: %q", got)
	}
	if !strings.Contains(got, "example.com/cb") {
		t.Errorf("path should survive: %q", got)
	}
}

// Userinfo with no username still carries a password; it must not be logged.
func TestRedactURL_PasswordOnlyUserinfo(t *testing.T) {
	got := RedactURL("https://:hunter2@example.com/mcp")
	if strings.Contains(got, "hunter2") {
		t.Fatalf("password-only userinfo survived: %q", got)
	}
	if !strings.Contains(got, "example.com/mcp") {
		t.Errorf("host and path should survive: %q", got)
	}
}

// An unparseable URL with no query has nothing to strip; it is returned as-is
// rather than being blanked, which would lose the only diagnostic available.
func TestRedactURL_MalformedWithoutQuery(t *testing.T) {
	const bad = "ht!tp://%%%bad"
	if got := RedactURL(bad); got != bad {
		t.Errorf("malformed URL without query = %q, want it unchanged", got)
	}
}
