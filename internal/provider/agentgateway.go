package provider

import (
	"context"
	"fmt"
	"iter"
	"net/url"
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
// one — see routesToOllama for why that is decided by the name prefix.
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
// The model name is passed to the client whole. Resolve has already stripped
// the outer agentgateway/ wrapper — an agentgateway/gemini/gemini-2.5-flash
// model arrives here as gemini/gemini-2.5-flash — and stripping the remaining
// vendor segment would eat a legitimate vendor-namespaced id.
//
// That remaining slash is escaped before it reaches the wire. See
// geminiWireName for why the gateway needs it and why Name() must not carry it.
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
	return gatewayGeminiWireName{inner: llm, modelName: modelName}, nil
}

// gatewayGeminiWireName rewrites the model name a Gemini request travels with,
// and only on the wire.
//
// A layered gateway route — agentgateway/gemini/gemini-4-neon, the shape the
// gateway's own pi-fast failover uses — reaches here as gemini/gemini-4-neon.
// The gateway resolves that name on the OpenAI-compatible endpoint, where the
// model is a JSON field, but NOT on the native one, where the SDK pastes it
// into the URL path: /v1beta/models/gemini/gemini-4-neon:generateContent. The
// genai SDK escapes nothing (createAPIURL → url.JoinPath), so the slash splits
// the path and the gateway answers 404 with an empty body. Nothing in that
// error names the model or the slash, which is what makes it worth fixing here
// rather than leaving as a documented limitation.
//
// Percent-escaping the slash makes the path one segment again —
// /v1beta/models/gemini%2Fgemini-4-neon:generateContent — which the gateway
// routes and strips to gemini-4-neon. An unlayered name has no slash and is
// left byte-for-byte unchanged, so this is a no-op for the common case.
//
// The rewrite is deliberately not applied to Name(). That string is user-facing
// and goes into session metadata (agent.go's SetSessionModel) and the TUI's
// model label; a %2F there would be a second, cosmetic bug. ADK builds every
// request as LLMRequest{Model: f.Model.Name()} (base_flow.go), so escaping
// inside NewGemini alone would be overwritten on the very next turn — hence a
// per-request rewrite on the way through instead.
type gatewayGeminiWireName struct {
	inner     model.LLM
	modelName string
}

var _ model.LLM = gatewayGeminiWireName{}

func (m gatewayGeminiWireName) Name() string { return m.inner.Name() }

// GenerateContent forwards to the wrapped model with the wire model name set.
//
// The name escaped is the one the request is actually for, not the one this
// client was built with. Those differ whenever a BeforeModelCallback overrides
// req.Model — a supported way to retarget a single call — and the SDK honors
// the override, so escaping the configured name instead would silently send
// the request to the wrong model. The configured name is only the fallback for
// a request that names none, which is also the case the SDK falls back on.
//
// req is copied rather than mutated. ADK's request value is shared with the
// flow that built it — the same pointer feeds tracing hooks and the transcript
// — so writing the escaped name into it would leak %2F into places that should
// show the name the user typed.
func (m gatewayGeminiWireName) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	if req == nil {
		return m.inner.GenerateContent(ctx, req, stream)
	}
	requested := strings.TrimSpace(req.Model)
	if requested == "" {
		requested = m.modelName
	}
	wireName := geminiWireName(requested)
	if wireName == req.Model || (req.Model == "" && wireName == m.modelName) {
		return m.inner.GenerateContent(ctx, req, stream)
	}
	escaped := *req
	escaped.Model = wireName
	return m.inner.GenerateContent(ctx, &escaped, stream)
}

// geminiWireName percent-escapes the path-bearing characters of a Gemini model
// name so the SDK's URL builder keeps it to a single path segment.
//
// url.PathEscape is the right tool rather than a manual slash replacement: it
// escapes exactly the characters that would change the URL's structure (/ ? #)
// and leaves the rest of the id — colons in gemma4:e4b, dashes, dots — alone.
// Escaping more than that would rename models the gateway already serves.
func geminiWireName(modelName string) string {
	return url.PathEscape(strings.TrimSpace(modelName))
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
// the gateway's Ollama routes.
//
// The rule is the `ollama` prefix, and it is deliberately one rule rather than a
// list. The gateway's config routes ollama, ollama1/2/3, ollama-cloud and the
// virtual models that fail over across them (ollama-deepseek, ollama-glm53, …)
// under that shared prefix, so a new Ollama route is covered the moment it is
// named. Measured against a running gateway's /v1/models: every id with prefix
// `ollama` is owner `ollama` or a virtual model over Ollama, and no id owned by
// `ollama` lacks the prefix.
//
// The virtual-model exemption matters. pi-fast used to be Ollama-first, so it
// was listed here by name; the gateway now routes it to openai/gpt-5.6-luna and
// gemini/gemini-3.8-flash, and the name kept it on max_tokens anyway — a route
// that speaks the modern field silently downgraded to the deprecated one
// because a name no longer described where it went. That is the failure this
// rule has to be able to express, so it does not carry a name list. Everything
// else — anthropic/, openai/, gemini/, pi-default, pi-fast — speaks
// max_completion_tokens and must not be downgraded.
func routesToOllama(modelName string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(modelName)), "ollama")
}
