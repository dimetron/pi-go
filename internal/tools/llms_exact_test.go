package tools

import (
	"net/url"
	"testing"

	"github.com/dimetron/pi-go/internal/config"
)

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parsing %q: %v", raw, err)
	}
	return u
}

// A source the user configured grants its whole host: an llms.txt index exists
// to be followed to the pages it links.
func TestURLAllowedConfiguredSourceGrantsHost(t *testing.T) {
	ts := NewLLMSToolset(&config.LLMSConfig{Sources: []config.LLMSSource{
		{Name: "adk", URL: "https://adk.dev/llms.txt"},
	}})
	for _, raw := range []string{
		"https://adk.dev/llms.txt",
		"https://adk.dev/docs/agents.md",
		"https://adk.dev/anything/else",
	} {
		if !ts.urlAllowed(mustURL(t, raw)) {
			t.Errorf("urlAllowed(%q) = false, want true", raw)
		}
	}
	if ts.urlAllowed(mustURL(t, "https://elsewhere.example/llms.txt")) {
		t.Error("allowed an unrelated host")
	}
}

// A source pi-go inferred from a file name grants only that URL. The name is a
// guess, and a wrong guess must not hand the model a whole host.
func TestURLAllowedInferredSourceGrantsOnlyItsURL(t *testing.T) {
	ts := NewLLMSToolset(&config.LLMSConfig{Sources: []config.LLMSSource{
		{Name: "guessed", URL: "https://internal.corp/llms.txt", ExactURLOnly: true},
	}})

	if !ts.urlAllowed(mustURL(t, "https://internal.corp/llms.txt")) {
		t.Error("the inferred URL itself was refused")
	}
	for _, raw := range []string{
		"https://internal.corp/secrets",
		"https://internal.corp/",
		"https://internal.corp:8443/llms.txt",
		"http://internal.corp/llms.txt",
		// A query can select a different document entirely.
		"https://internal.corp/llms.txt?resource=other",
	} {
		if ts.urlAllowed(mustURL(t, raw)) {
			t.Errorf("urlAllowed(%q) = true; an inference must not widen host access", raw)
		}
	}
}

// One configured source and one inferred source on the same host: the
// configured grant wins, because the user made it deliberately.
func TestURLAllowedConfiguredWinsOverInferredOnSameHost(t *testing.T) {
	ts := NewLLMSToolset(&config.LLMSConfig{Sources: []config.LLMSSource{
		{Name: "inferred", URL: "https://adk.dev/llms.txt", ExactURLOnly: true},
		{Name: "configured", URL: "https://adk.dev/llms-full.txt"},
	}})
	if !ts.urlAllowed(mustURL(t, "https://adk.dev/docs/page.md")) {
		t.Error("a deliberately configured source did not grant its host")
	}
}

// checkRedirectURL applies urlAllowed to every hop, so a server cannot widen an
// inferred source by choosing where to send the client. That path is not
// exercised here: checkRedirectURL first calls rejectPrivateHost, which
// resolves the host, and any fixture host either needs DNS or fails for the
// wrong reason. The rule itself is covered by the urlAllowed tests above.

// A bare trailing "?" carries no parameters, so it addresses the same resource
// and is not a widening.
func TestURLAllowedInferredSourceAllowsEmptyQuery(t *testing.T) {
	ts := NewLLMSToolset(&config.LLMSConfig{Sources: []config.LLMSSource{
		{Name: "guessed", URL: "https://internal.corp/llms.txt", ExactURLOnly: true},
	}})
	if !ts.urlAllowed(mustURL(t, "https://internal.corp/llms.txt?")) {
		t.Error("refused the same resource spelled with an empty query")
	}
}
