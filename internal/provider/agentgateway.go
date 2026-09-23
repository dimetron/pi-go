package provider

import (
	"context"
	"fmt"
	"os"
	"strings"

	"google.golang.org/adk/v2/model"
)

// agentgatewayDefaultBaseURL is the default endpoint for the agentgateway
// provider — a local OpenAI-compatible gateway. The OpenAI client appends /v1
// to it, so requests land on <base>/v1/chat/completions.
const agentgatewayDefaultBaseURL = "http://localhost:4000"

// agentGatewayPlaceholderKey stands in for the model credential on a gateway
// native-Gemini route, where the gateway substitutes the real key before the
// request leaves for Google. It exists only because the genai SDK refuses to
// build a client with no key at all — it is never presented to a model.
const agentGatewayPlaceholderKey = "agentgateway-injects-the-real-key"

// NewAgentGateway creates an agentgateway model.LLM.
//
// The gateway speaks more than one protocol, and which one a request must use
// is a property of the ROUTE, not of the gateway: a route's upstream decides
// the wire shape. So the client is chosen per model name — see
// GatewayRoutesToGemini and routesToOllama below — rather than once for the
// whole provider. Everything not named here is OpenAI-shaped, which is the
// gateway's default and the common case.
//
// No API key is required: NewOpenAI substitutes a dummy key whenever a base URL
// is set, and a gateway that needs one can still be given AGENTGATEWAY_API_KEY.
//
// UseLegacyMaxTokens is scoped to the gateway's known Ollama routes, not forced
// provider-wide. Ollama's API only understands the legacy max_tokens field —
// the newer max_completion_tokens is silently ignored, so the model runs
// unbounded and hits Ollama's own 65536-token default. Sending max_tokens lets
// pi-go's cap (defaultOaiMaxOutputTokens = 64000) reach the model instead. The
// gateway's other routes (anthropic, openai, gemini, …) speak the modern
// max_completion_tokens field, so they must not be downgraded to the legacy
// one.
func NewAgentGateway(ctx context.Context, modelName, apiKey, baseURL string, opts *LLMOptions) (model.LLM, error) {
	if baseURL == "" {
		baseURL = agentgatewayDefaultBaseURL
	}
	if opts == nil {
		opts = &LLMOptions{}
	}
	if GatewayRoutesToGemini(modelName) {
		return newAgentGatewayGemini(ctx, modelName, apiKey, baseURL, opts)
	}
	if routesToOllama(modelName) {
		opts.UseLegacyMaxTokens = true
	}
	llm, err := NewOpenAI(ctx, modelName, apiKey, baseURL, opts)
	if err != nil {
		return nil, fmt.Errorf("creating agentgateway client: %w", err)
	}
	return llm, nil
}

// newAgentGatewayGemini builds the native Gemini client for a gateway model
// whose route forwards generateContent upstream.
//
// Two credentials travel in two different headers, and getting either wrong
// fails in a way that does not name the cause:
//
//   - The GATEWAY key authenticates the request to the gateway itself, which
//     reads Authorization: Bearer by default (llm.policies.apiKey). The genai
//     SDK never sets that header, so it has to be injected through
//     ExtraHeaders.
//   - The MODEL key, x-goog-api-key, is injected by the gateway's own
//     provider `defaults.requestHeaders.set`, which OVERRIDES whatever the
//     client sent. So a dummy is both safe and necessary here: the SDK
//     refuses to build a client at all without some key, and the gateway
//     replaces it before the request leaves for Google.
//
// The model name is passed through whole. Resolve has already stripped the
// vendor segment — an agentgateway/gemini/gemini-2.5-flash model arrives here
// as gemini-2.5-flash — and stripping again would eat a legitimate
// vendor-namespaced id.
func newAgentGatewayGemini(ctx context.Context, modelName, apiKey, baseURL string, opts *LLMOptions) (model.LLM, error) {
	if apiKey == "" {
		apiKey = os.Getenv("AGENTGATEWAY_API_KEY")
	}
	if apiKey != "" {
		if opts.ExtraHeaders == nil {
			opts.ExtraHeaders = map[string]string{}
		}
		opts.ExtraHeaders["Authorization"] = "Bearer " + apiKey
	}
	// The model key, as opposed to the gateway key above. The gateway injects
	// the real one, so this value does not reach Google — but it must be
	// non-empty, because the SDK refuses to build a client without one. A real
	// GEMINI_API_KEY is preferred when the environment has one, so a gateway
	// that does NOT substitute a key still works.
	modelKey := geminiAPIKey()
	if modelKey == "" {
		modelKey = agentGatewayPlaceholderKey
	}
	llm, err := NewGemini(ctx, modelName, modelKey, baseURL, opts)
	if err != nil {
		return nil, fmt.Errorf("creating agentgateway gemini client: %w", err)
	}
	return llm, nil
}

// GatewayRoutesToGemini reports whether an agentgateway model name resolves to
// a route whose upstream speaks Gemini's native protocol.
//
// This exists because the OpenAI-compatible shape cannot express Gemini's
// built-in server-side tools. A request carrying {"type":"google_search"}
// through an OpenAI→generateContent conversion does not fail: the tool is
// dropped without a word, and the model answers confidently from its own
// priors — a wrong answer that looks like a right one. Only the native path
// delivers {"googleSearch":{}} to Google, which is what grounding needs.
//
// Mirrors routesToOllama deliberately: both encode the same kind of fact, that
// a route's upstream constrains the request shape, and keeping the two facts in
// one file and one style is what stops the next route from being special-cased
// somewhere else.
//
// Caveat: this is a NAME match, and the gateway is free to reconfigure its
// routes underneath us. It assumes the convention its own config.yaml
// follows — gemini/* and gemini-* are Gemini routes. A gateway serving native
// Gemini under some other prefix would need that prefix added here.
//
// Exported because the grounding gate asks the same question about the same
// route: Gemini's server-side search only means anything where the upstream
// accepts it, and the two answers must not disagree. Note that
// agent.GatewayRouteSpeaksGemini holds a second copy of this rule, because
// piagent imports internal/agent and TestPiagentStaysIsolated forbids this
// package in piagent's transitive graph. agent's
// TestGatewayRouteSpeaksGeminiAgreesWithProvider fails if the two drift, so
// change both lists together.
func GatewayRoutesToGemini(modelName string) bool {
	lower := strings.ToLower(strings.TrimSpace(modelName))
	for _, prefix := range []string{"gemini/", "gemini-"} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

// routesToOllama reports whether an agentgateway model name resolves to one of
// the gateway's Ollama routes. The gateway's config.yaml routes the ollama,
// ollama1/2/3 and ollama-cloud prefixes to Ollama backends, and the virtual
// models that fail over across them (ollama-deepseek, ollama-gemma4, …) also
// land on Ollama. Everything else — anthropic/, openai/, gemini/, pi-default,
// and the bare vendor model names — speaks the modern max_completion_tokens
// field and must not be downgraded to the legacy max_tokens.
func routesToOllama(modelName string) bool {
	lower := strings.ToLower(strings.TrimSpace(modelName))
	for _, prefix := range []string{
		"ollama/", "ollama1/", "ollama2/", "ollama3/", "ollama-cloud/",
	} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	// Virtual models that route to Ollama backends. pi-fast's primary failover
	// target is ollama1/deepseek-v4-flash:0731-cloud, so it is Ollama-first.
	for _, name := range []string{
		"ollama-deepseek", "ollama-deepseek-balanced", "ollama-gemma4",
		"ollama-glm-flash", "ollama-minimax", "pi-fast",
	} {
		if lower == name {
			return true
		}
	}
	return false
}
