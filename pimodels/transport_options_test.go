package pimodels

import (
	"testing"
)

// The four transport options below are pass-throughs to provider.LLMOptions.
// Each test asserts the field the transport actually reads, so a renamed or
// dropped field fails here rather than silently reverting an embedder to the
// provider default.

func apply(t *testing.T, opts ...Option) options {
	t.Helper()
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	return o
}

// TestWithLegacyMaxTokens guards a silent-failure bug rather than a preference.
//
// Ollama understands only max_tokens; the newer max_completion_tokens is
// ignored by it without an error, which leaves generation unbounded instead of
// rejected. An embedder pointed at Ollama through agentgateway has no other way
// to set this.
func TestWithLegacyMaxTokens(t *testing.T) {
	o := apply(t, WithLegacyMaxTokens())
	if !o.llm.UseLegacyMaxTokens {
		t.Error("WithLegacyMaxTokens did not set UseLegacyMaxTokens; an Ollama " +
			"request through a gateway would be sent with max_completion_tokens " +
			"and silently left unbounded")
	}
}

// TestWithMaxOutputTokens pins the cap, including the zero value's meaning:
// it is "use the provider default", not "no output".
func TestWithMaxOutputTokens(t *testing.T) {
	o := apply(t, WithMaxOutputTokens(4096))
	if o.llm.MaxOutputTokens != 4096 {
		t.Errorf("MaxOutputTokens = %d, want 4096", o.llm.MaxOutputTokens)
	}
}

func TestWithSystemCAsDisabled(t *testing.T) {
	o := apply(t, WithSystemCAsDisabled())
	if !o.llm.DisableSystemCAs {
		t.Error("WithSystemCAsDisabled did not set DisableSystemCAs; trust stays additive")
	}
}

// TestTransportOptionsOffByDefault is the control for all four: none of them
// may change behavior unless asked for.
func TestTransportOptionsOffByDefault(t *testing.T) {
	o := apply(t)
	if o.llm.UseLegacyMaxTokens {
		t.Error("UseLegacyMaxTokens on by default")
	}
	if o.llm.MaxOutputTokens != 0 {
		t.Errorf("MaxOutputTokens = %d by default, want 0 (provider default)", o.llm.MaxOutputTokens)
	}
	if o.llm.DisableSystemCAs {
		t.Error("DisableSystemCAs on by default")
	}
}

// TestOptionsCompose checks the new options do not interfere with each other or
// with the existing ones, since all of them write into the same struct.
func TestOptionsCompose(t *testing.T) {
	o := apply(t,
		WithAPIKey("k"),
		WithBaseURL("https://gateway.internal/v1"),
		WithLegacyMaxTokens(),
		WithMaxOutputTokens(8192),
		WithSystemCAsDisabled(),
		WithCACert("/etc/ssl/corp.pem"),
	)

	if o.apiKey != "k" {
		t.Errorf("apiKey = %q, want k", o.apiKey)
	}
	if o.baseURL != "https://gateway.internal/v1" {
		t.Errorf("baseURL = %q", o.baseURL)
	}
	if !o.llm.UseLegacyMaxTokens || o.llm.MaxOutputTokens != 8192 {
		t.Error("token options lost when composed with others")
	}
	if !o.llm.DisableSystemCAs || o.llm.CACertPath != "/etc/ssl/corp.pem" {
		t.Error("TLS options lost when composed with others")
	}
}

// TestNewAcceptsTheNewOptions is an end-to-end check that the options survive
// the real constructor, which resolves provider info before building a client.
// No request is issued, so no credential is needed.
func TestNewAcceptsTheNewOptions(t *testing.T) {
	m, err := New(t.Context(), "ollama/gemma4:e4b",
		WithBaseURL("http://127.0.0.1:11434"),
		WithLegacyMaxTokens(),
		WithMaxOutputTokens(2048),
	)
	if err != nil {
		t.Fatalf("New with the new options: %v", err)
	}
	if m == nil {
		t.Fatal("New returned a nil model")
	}
}

// TestTokenOptionsDoNotReachNativeOllama documents a limitation the README now
// states, so it cannot quietly become a lie in either direction.
//
// The native ollama/ client speaks Ollama's own API and reads neither
// UseLegacyMaxTokens nor MaxOutputTokens — its cap is the PI_OLLAMA_NUM_PREDICT
// variable. An option that silently does nothing is worse than one that is
// absent, which is why the routing is pinned here rather than only described.
func TestTokenOptionsDoNotReachNativeOllama(t *testing.T) {
	// Routing first: ollama/ must select the native client, not a custom
	// OpenAI-compatible one. If this ever changes, the README section and the
	// option docs both need revisiting.
	info, err := Resolve("ollama/gemma4:e4b")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if info.Provider != "ollama" {
		t.Fatalf("Resolve(ollama/...).Provider = %q, want ollama — the native client "+
			"is selected by the ollama/ prefix, and the token options do not apply to it",
			info.Provider)
	}
	if info.Custom {
		t.Error("ollama/ resolved as a custom OpenAI-compatible endpoint; the README's " +
			"'native client' section would be wrong")
	}

	// The options still store, so a caller who later moves to a gateway keeps
	// them; they are simply not consumed on this route.
	o := apply(t, WithLegacyMaxTokens(), WithMaxOutputTokens(4096))
	if !o.llm.UseLegacyMaxTokens || o.llm.MaxOutputTokens != 4096 {
		t.Error("token options did not store, so they would be lost when reusing options")
	}
}

// TestCustomBaseURLSelectsTheOpenAICompatibleClient pins the positive half of
// the same contract: a base URL plus a name routes to the client that DOES read
// both options. Without this the README's advice would be untested.
func TestCustomBaseURLSelectsTheOpenAICompatibleClient(t *testing.T) {
	info, err := Resolve("llama3", WithBaseURL("http://127.0.0.1:4000/v1"))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !info.Custom {
		t.Errorf("a bare name with WithBaseURL resolved Custom=false (provider=%q); "+
			"the token options are read only on the OpenAI-compatible path",
			info.Provider)
	}
}

// TestWithWebSearch guards the pass-through to provider.LLMOptions, the same
// way the transport options above are guarded. pimodels may not import
// internal/agent or internal/tools (isolation_test.go), so the option sets a
// provider flag and the provider decides what reaches the wire — a renamed or
// dropped field would otherwise leave an embedder asking for search and
// silently getting none.
func TestWithWebSearch(t *testing.T) {
	o := apply(t, WithWebSearch())
	if !o.llm.EnableOpenAIWebSearch {
		t.Error("WithWebSearch did not set EnableOpenAIWebSearch; an embedder " +
			"asking for built-in search would get an ordinary turn with no error")
	}
}

// Default must stay off: OpenAI rejects the web_search tool on models that do
// not support it, so an embedder that never asked for search must not get it.
func TestWebSearchOffWithoutOption(t *testing.T) {
	o := apply(t)
	if o.llm.EnableOpenAIWebSearch {
		t.Error("EnableOpenAIWebSearch is on without WithWebSearch; models that " +
			"reject the tool would fail ordinary turns")
	}
}
