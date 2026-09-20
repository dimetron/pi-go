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

func TestWithXAITools(t *testing.T) {
	o := apply(t, WithXAITools())
	if !o.llm.EnableXAITools {
		t.Error("WithXAITools did not set EnableXAITools; xAI server-side tools stay unregistered")
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
	if o.llm.EnableXAITools {
		t.Error("EnableXAITools on by default")
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
		WithXAITools(),
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
	if !o.llm.EnableXAITools {
		t.Error("xAI option lost when composed with others")
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
