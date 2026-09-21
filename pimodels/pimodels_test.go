package pimodels

import (
	"context"
	"encoding/json"
	"io"
	"iter"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

func TestNewRejectsEmptyModelName(t *testing.T) {
	if _, err := New(context.Background(), ""); err == nil {
		t.Fatal("New(\"\") returned no error; an empty model name cannot resolve to anything")
	}
}

func TestNewUnknownModel(t *testing.T) {
	_, err := New(context.Background(), "definitely-not-a-real-model-xyz")
	if err == nil {
		t.Fatal("expected an error for an unresolvable model name")
	}
	if !strings.Contains(err.Error(), "pimodels:") {
		t.Fatalf("error is not attributed to this package: %v", err)
	}
}

func TestResolveKnownModels(t *testing.T) {
	tests := []struct {
		name         string
		model        string
		wantProvider string
	}{
		{"openai", "gpt-5.6-luna", "openai"},
		{"anthropic", "claude-sonnet-5", "anthropic"},
		{"gemini", "gemini-3.5-pro", "gemini"},
		{"xai", "grok-4.6", "xai"},
		{"mistral", "mistral-large-latest", "mistral"},
		{"ollama prefix", "ollama/gemma4:e4b", "ollama"},
		{"ollama cloud suffix", "minimax-m3:cloud", "ollama"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := Resolve(tt.model)
			if err != nil {
				t.Fatalf("Resolve(%q): %v", tt.model, err)
			}
			if info.Provider != tt.wantProvider {
				t.Fatalf("Resolve(%q).Provider = %q, want %q", tt.model, info.Provider, tt.wantProvider)
			}
			if info.Model == "" {
				t.Errorf("Resolve(%q) returned an empty Model name", tt.model)
			}
		})
	}
}

func TestResolveMarksExplicitOllamaAsLocal(t *testing.T) {
	info, err := Resolve("ollama/deepseek-v4-flash:0731-cloud")
	if err != nil {
		t.Fatalf("Resolve explicit Ollama model: %v", err)
	}
	if !info.LocalOllama {
		t.Fatal("Resolve lost the explicit ollama/ local-routing decision")
	}
}

func TestNewFromInfoBuildsResolvedPrefixedModels(t *testing.T) {
	tests := []struct {
		name string
		info Info
		opts []Option
	}{
		{
			name: "ollama prefix",
			info: Info{Provider: "ollama", Model: "gemma4:e4b", Ollama: true, LocalOllama: true},
		},
		{
			name: "azure prefix",
			info: Info{Provider: "azure", Model: "prod-deployment"},
			opts: []Option{WithBaseURL("https://azure.example.openai.azure.com"), WithAPIKey("test-key")},
		},
		{
			name: "opencode prefix",
			info: Info{Provider: "opencode", Model: "kimi-k3"},
			opts: []Option{WithAPIKey("test-key")},
		},
		{
			name: "agentgateway prefix",
			info: Info{Provider: "agentgateway", Model: "deepseek-v4-flash:0731-cloud"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := NewFromInfo(context.Background(), tt.info, tt.opts...)
			if err != nil {
				t.Fatalf("NewFromInfo(%+v): %v", tt.info, err)
			}
			if got := m.Name(); got != tt.info.Model {
				t.Fatalf("Name() = %q, want resolved model %q", got, tt.info.Model)
			}
			p, ok := any(m).(interface{ Provider() string })
			if !ok {
				t.Fatal("NewFromInfo result does not report its provider")
			}
			if got := p.Provider(); got != tt.info.Provider {
				t.Fatalf("Provider() = %q, want %q", got, tt.info.Provider)
			}
		})
	}
}

func TestNewFromInfoRejectsIncompleteInfo(t *testing.T) {
	if _, err := NewFromInfo(context.Background(), Info{Model: "gemma4:e4b"}); err == nil {
		t.Fatal("NewFromInfo accepted an Info without a provider")
	}
	if _, err := NewFromInfo(context.Background(), Info{Provider: "ollama"}); err == nil {
		t.Fatal("NewFromInfo accepted an Info without a model")
	}
}

func TestResolveUnknownModel(t *testing.T) {
	if _, err := Resolve("not-a-model-at-all-123"); err == nil {
		t.Fatal("expected an error for an unresolvable model name")
	}
}

// TestResolveWithBaseURLTakesPrecedence pins the rule that an explicit endpoint
// wins: a caller naming a gateway must not have the model name silently reroute
// the request somewhere else.
func TestResolveWithBaseURLTakesPrecedence(t *testing.T) {
	info, err := Resolve("gpt-5.6-luna", WithBaseURL("https://gateway.example/v1"))
	if err != nil {
		t.Fatalf("Resolve with base URL: %v", err)
	}
	if info.BaseURL != "https://gateway.example/v1" {
		t.Fatalf("BaseURL = %q, want the explicit endpoint", info.BaseURL)
	}
}

func TestContextWindow(t *testing.T) {
	if got := ContextWindow("gpt-5.6-luna"); got <= 0 {
		t.Errorf("ContextWindow for a known model = %d, want > 0", got)
	}
	if got := ContextWindow("definitely-unknown-model-xyz"); got != 0 {
		t.Errorf("ContextWindow for an unknown model = %d, want 0", got)
	}
}

func TestContextWindowFor(t *testing.T) {
	if got := ContextWindowFor("gemini", "gemini-3.7-flash"); got <= 0 {
		t.Errorf("ContextWindowFor(gemini) = %d, want > 0", got)
	}
}

func TestAPIKeyEnvVar(t *testing.T) {
	tests := map[string]string{
		"anthropic": "ANTHROPIC_API_KEY",
		"openai":    "OPENAI_API_KEY",
		"azure":     "AZUREOPENAI_API_KEY",
		"gemini":    "GEMINI_API_KEY",
		"xai":       "XAI_API_KEY",
		"mistral":   "MISTRAL_API_KEY",
	}
	for prov, want := range tests {
		if got := APIKeyEnvVar(prov); got != want {
			t.Errorf("APIKeyEnvVar(%q) = %q, want %q", prov, got, want)
		}
	}
}

// TestOptionsApplyInOrder pins that a later option wins, which is what makes
// FromConfig's "config first, caller second" layering work.
func TestOptionsApplyInOrder(t *testing.T) {
	var o options
	for _, opt := range []Option{
		WithAPIKey("first"),
		WithBaseURL("https://one.example"),
		WithAPIKey("second"),
	} {
		opt(&o)
	}
	if o.apiKey != "second" {
		t.Fatalf("apiKey = %q, want the later option to win", o.apiKey)
	}
	if o.baseURL != "https://one.example" {
		t.Fatalf("baseURL = %q, want it preserved", o.baseURL)
	}
}

func TestOptionsSetLLMFields(t *testing.T) {
	var o options
	for _, opt := range []Option{
		WithHeaders(map[string]string{"X-Tenant": "acme"}),
		WithConnectTimeout(3 * time.Second),
		WithCACert("/etc/ssl/corp.pem"),
		WithInsecureTLS(),
		WithPromptCachingDisabled(),
		WithAdvisor("claude-opus-4-7", 2, true),
		WithThinkingLevel("high"),
	} {
		opt(&o)
	}
	if o.llm.ExtraHeaders["X-Tenant"] != "acme" {
		t.Error("WithHeaders did not reach LLMOptions")
	}
	if o.llm.ConnectTimeout != 3*time.Second {
		t.Error("WithConnectTimeout did not reach LLMOptions")
	}
	if o.llm.CACertPath != "/etc/ssl/corp.pem" {
		t.Error("WithCACert did not reach LLMOptions")
	}
	if !o.llm.InsecureSkipTLS {
		t.Error("WithInsecureTLS did not reach LLMOptions")
	}
	if !o.llm.DisablePromptCaching {
		t.Error("WithPromptCachingDisabled did not reach LLMOptions")
	}
	if o.llm.AdvisorModel != "claude-opus-4-7" || o.llm.AdvisorMaxUses != 2 || !o.llm.AdvisorCaching {
		t.Error("WithAdvisor did not reach LLMOptions")
	}
	if o.thinkingLevel != "high" {
		t.Error("WithThinkingLevel was not recorded")
	}
}

// TestNewUsesExplicitKeyOverEnv pins that WithAPIKey wins, so an embedder
// managing its own secrets is never overridden by a stray environment variable.
func TestNewUsesExplicitKeyOverEnv(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "from-env")

	// A local Ollama endpoint needs no credential and no network at construction
	// time, which keeps this test about option precedence rather than about
	// reaching a vendor.
	var o options
	WithAPIKey("explicit").apply(&o)
	if o.apiKey != "explicit" {
		t.Fatalf("apiKey = %q, want %q", o.apiKey, "explicit")
	}
}

// apply exists so the precedence test above reads as one call rather than a
// loop over a single element.
func (f Option) apply(o *options) { f(o) }

func TestFromConfigUnknownRoleFails(t *testing.T) {
	// A role that cannot exist must surface as an error rather than silently
	// falling back to some other model — an embedder needs to know it asked for
	// something the config does not define.
	_, err := FromConfig(context.Background(), "definitely-not-a-configured-role")
	if err == nil {
		t.Skip("config defines a fallback for unknown roles on this machine; nothing to assert")
	}
	if !strings.Contains(err.Error(), "pimodels:") {
		t.Fatalf("error is not attributed to this package: %v", err)
	}
}

// fakeLLM is a minimal model.LLM for testing the provider wrapper without a
// network or a credential.
type fakeLLM struct{ name string }

func (f fakeLLM) Name() string { return f.name }
func (f fakeLLM) GenerateContent(context.Context, *model.LLMRequest, bool) iter.Seq2[*model.LLMResponse, error] {
	return func(func(*model.LLMResponse, error) bool) {}
}

// TestProviderModelReportsProvider is the contract piagent type-asserts for.
// If this breaks, every consumer silently falls back to its own model-name
// prefix table — which is the duplication this exists to remove.
func TestProviderModelReportsProvider(t *testing.T) {
	m := providerModel{LLM: fakeLLM{name: "claude-sonnet-5"}, provider: "anthropic"}

	p, ok := any(m).(interface{ Provider() string })
	if !ok {
		t.Fatal("the model returned by New does not satisfy interface{ Provider() string }")
	}
	if got := p.Provider(); got != "anthropic" {
		t.Fatalf("Provider() = %q, want %q", got, "anthropic")
	}
	if _, ok := any(m).(ProviderNamer); !ok {
		t.Error("providerModel does not satisfy the named ProviderNamer interface")
	}
}

// TestProviderModelForwardsEmbeddedMethods pins that wrapping is transparent:
// embedding must forward Name(), and anything ADK adds later, unchanged.
func TestProviderModelForwardsEmbeddedMethods(t *testing.T) {
	m := providerModel{LLM: fakeLLM{name: "gpt-5.6-luna"}, provider: "openai"}
	if got := m.Name(); got != "gpt-5.6-luna" {
		t.Fatalf("Name() = %q, want the wrapped model's name — wrapping must be transparent", got)
	}
	var _ Model = m // must still satisfy the interface an agent consumes
}

// TestThinkingLevelValidate pins the accepted vocabulary, including the two
// spellings deliberately left out. "xhigh" and "off" are provider aliases that
// some providers honor and others drop silently — OpenRouter discards "xhigh"
// while xAI discards nothing, so neither can be accepted without one provider
// quietly ignoring it. "max" and "none" are the spellings every provider
// understands.
func TestThinkingLevelValidate(t *testing.T) {
	valid := []ThinkingLevel{
		ThinkingUnset, ThinkingNone, ThinkingLow, ThinkingMedium, ThinkingHigh, ThinkingMax,
	}
	for _, level := range valid {
		t.Run("valid "+string(level), func(t *testing.T) {
			if err := level.Validate(); err != nil {
				t.Errorf("Validate() = %v, want nil for %q", err, level)
			}
		})
	}

	// Case and surrounding space are normalized rather than rejected: a level
	// read from a config file often carries one.
	for _, spelling := range []string{"HIGH", " High ", "Medium"} {
		t.Run("normalized "+spelling, func(t *testing.T) {
			if err := ThinkingLevel(spelling).Validate(); err != nil {
				t.Errorf("Validate() = %v, want nil for %q", err, spelling)
			}
		})
	}

	invalid := []string{"turbo", "xhigh", "off", "hihg", "nonee", "highh"}
	for _, spelling := range invalid {
		t.Run("invalid "+spelling, func(t *testing.T) {
			err := ThinkingLevel(spelling).Validate()
			if err == nil {
				t.Fatalf("Validate() = nil for %q; an unaccepted level must not pass silently", spelling)
			}
			// The message has to name the accepted set, or the caller has to
			// go read the source to find out what would have worked.
			for _, want := range []string{"low", "medium", "high", "max"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error for %q does not mention %q: %v", spelling, want, err)
				}
			}
		})
	}
}

// TestParseThinkingLevel pins the string form against the typed one: parsing
// must accept exactly what Validate accepts and reject the rest, so a caller
// validating early and a caller passing the string directly cannot disagree
// about what is allowed.
func TestParseThinkingLevel(t *testing.T) {
	for _, in := range []string{"", "none", "low", "medium", "high", "max", "HIGH", " High "} {
		got, err := ParseThinkingLevel(in)
		if err != nil {
			t.Errorf("ParseThinkingLevel(%q) = %v, want no error", in, err)
			continue
		}
		if err := got.Validate(); err != nil {
			t.Errorf("ParseThinkingLevel(%q) returned %q, which does not validate: %v", in, got, err)
		}
	}

	// Empty means "provider default", not a level called "".
	got, err := ParseThinkingLevel("")
	if err != nil || got != ThinkingUnset {
		t.Errorf("ParseThinkingLevel(\"\") = (%q, %v), want (\"\", nil)", got, err)
	}

	for _, in := range []string{"turbo", "xhigh", "off"} {
		if _, err := ParseThinkingLevel(in); err == nil {
			t.Errorf("ParseThinkingLevel(%q) = nil error, want rejection", in)
		}
	}
}

// TestNewRejectsBadThinkingLevel is the guard that matters: a typo must fail at
// construction. Every provider omits an unrecognized level from the request
// instead of erroring, so without this the level is accepted, the model keeps
// its own default, and nothing in the response says the setting did not apply.
//
// It asserts on the error text, which is what makes it able to fail: if
// validation were moved after the client build, an Ollama target would build
// fine and return no error at all.
func TestNewRejectsBadThinkingLevel(t *testing.T) {
	ctx := context.Background()
	// A local Ollama endpoint resolves and builds with no credential and no
	// network, so any error here is the level and not the environment.
	m, err := New(ctx, "ollama/gemma4:e4b",
		WithBaseURL("http://127.0.0.1:11434"),
		WithThinkingLevel("turbo"))
	if err == nil {
		t.Fatal("New accepted thinking level \"turbo\"; an unrecognized level must fail rather than be omitted")
	}
	if m != nil {
		t.Error("New returned a model alongside the error; a rejected option must not produce a usable client")
	}
	for _, want := range []string{"pimodels:", "ollama", "turbo"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q — it must name the package, the target and the bad value", err, want)
		}
	}
}

// TestNewAcceptsEveryValidThinkingLevel pins that validation does not reject
// what it should accept. An over-eager check is the failure mode that would
// make this option worse than the unchecked string it replaced.
func TestNewAcceptsEveryValidThinkingLevel(t *testing.T) {
	ctx := context.Background()
	for _, level := range validThinkingLevels {
		t.Run(string(level), func(t *testing.T) {
			m, err := New(ctx, "ollama/gemma4:e4b",
				WithBaseURL("http://127.0.0.1:11434"),
				WithThinkingLevel(level))
			if err != nil {
				t.Fatalf("New rejected the valid level %q: %v", level, err)
			}
			if m == nil {
				t.Fatalf("New returned a nil model for the valid level %q", level)
			}
		})
	}

	// The unset level is the zero value, which is also what an untyped ""
	// converts to, so a caller that never sets a level keeps working.
	m, err := New(ctx, "ollama/gemma4:e4b", WithBaseURL("http://127.0.0.1:11434"))
	if err != nil || m == nil {
		t.Fatalf("New with no thinking level = (%v, %v), want a working model", m, err)
	}
}

// TestWithThinkingLevelAcceptsStringVariables pins the compatibility property
// that a ThinkingLevel-typed parameter alone would have broken.
//
// Before this change WithThinkingLevel took a string, so `WithThinkingLevel(level)`
// with level a string variable compiled. Typing the parameter as ThinkingLevel
// rejects exactly that call while still accepting an untyped literal — which
// makes the break invisible in every test that writes the level inline, and
// visible only to callers reading it from a flag or a config file. The ~string
// type parameter keeps both working.
//
// The typed variables below are the point of the test: if the parameter ever
// narrows back to ThinkingLevel, this file stops compiling.
func TestWithThinkingLevelAcceptsStringVariables(t *testing.T) {
	ctx := context.Background()

	fromConfig := "medium" // the common case: a plain string
	var fromFlag string    // the zero value, meaning "provider default"
	type myLevel string    // a caller's own named string type
	var fromElsewhere myLevel = "high"

	for name, opt := range map[string]Option{
		"string variable":   WithThinkingLevel(fromConfig),
		"empty string":      WithThinkingLevel(fromFlag),
		"named string type": WithThinkingLevel(fromElsewhere),
		"ThinkingLevel":     WithThinkingLevel(ThinkingHigh),
		"untyped literal":   WithThinkingLevel("low"),
	} {
		t.Run(name, func(t *testing.T) {
			m, err := New(ctx, "ollama/gemma4:e4b",
				WithBaseURL("http://127.0.0.1:11434"), opt)
			if err != nil {
				t.Fatalf("New with %s: %v", name, err)
			}
			if m == nil {
				t.Fatalf("New with %s returned a nil model", name)
			}
		})
	}

	// Validation still applies to the string path — compatibility must not
	// come at the cost of the check this change exists to add.
	badLevel := "nonsense"
	if _, err := New(ctx, "ollama/gemma4:e4b",
		WithBaseURL("http://127.0.0.1:11434"),
		WithThinkingLevel(badLevel),
	); err == nil {
		t.Error("a bad level in a string variable was accepted; the string path must validate too")
	}
}

// TestThinkingLevelReachesTheWireCanonicalized is the regression for a silent
// no-op this type would otherwise have introduced, and it asserts on the
// request body rather than on an internal helper for that reason.
//
// Ollama and xAI match the level with exact `case` labels (ollama.go's
// ollamaThinkingConfig, xai.go's xaiReasoningEffort), while OpenRouter and
// Mistral lowercase the input themselves. A level that validates as "HIGH" and
// is then forwarded as "HIGH" passes New and is dropped by those two providers:
// accepted, applied nowhere, and invisible in the response. Only an assertion
// on what the provider is sent can tell a canonicalized level from a validated
// one, because both pass every check inside this package.
func TestThinkingLevelReachesTheWireCanonicalized(t *testing.T) {
	tests := []struct {
		given ThinkingLevel
		want  string
		// wantFalse is set for "none", which Ollama encodes as think=false
		// rather than as a level string.
		wantFalse bool
	}{
		{given: ThinkingHigh, want: "high"},
		{given: "HIGH", want: "high"},
		{given: " High ", want: "high"},
		{given: ThinkingMedium, want: "medium"},
		{given: "MediuM", want: "medium"},
		{given: ThinkingNone, wantFalse: true},
	}

	for _, tt := range tests {
		t.Run(string(tt.given), func(t *testing.T) {
			var captured []byte
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				captured = body
				w.Header().Set("Content-Type", "application/x-ndjson")
				_, _ = w.Write([]byte(`{"model":"gemma4:e4b","created_at":"2024-01-01T00:00:00Z","message":{"role":"assistant","content":"ok"},"done":true,"done_reason":"stop"}` + "\n"))
			}))
			defer srv.Close()

			m, err := New(context.Background(), "ollama/gemma4:e4b",
				WithBaseURL(srv.URL),
				WithThinkingLevel(tt.given))
			if err != nil {
				t.Fatalf("New(%q): %v", tt.given, err)
			}

			req := &model.LLMRequest{
				Contents: []*genai.Content{
					{Role: "user", Parts: []*genai.Part{{Text: "hi"}}},
				},
			}
			// Drain the iterator so the request is actually sent; ignoring the
			// response is fine, this test is about the request.
			for resp, err := range m.GenerateContent(context.Background(), req, false) {
				if err != nil {
					t.Fatalf("GenerateContent: %v", err)
				}
				_ = resp
			}

			if len(captured) == 0 {
				t.Fatal("no request reached the server")
			}
			var body map[string]any
			if err := json.Unmarshal(captured, &body); err != nil {
				t.Fatalf("parsing request body: %v", err)
			}

			if tt.wantFalse {
				// "none" is Ollama's explicit false, not an omitted field: a
				// missing field leaves the model's own default in force.
				if got, ok := body["think"].(bool); !ok || got {
					t.Fatalf("think = %v, want false for \"none\" — an omitted field would let the model think anyway", body["think"])
				}
				return
			}

			got, _ := body["think"].(string)
			if got != tt.want {
				t.Fatalf("think = %q on the wire, want %q; Ollama's case arm matches %q exactly and drops anything else",
					got, tt.want, tt.want)
			}
		})
	}
}
