package provider

import (
	"context"
	"fmt"
	"strings"

	"google.golang.org/adk/v2/model"
)

// agentgatewayDefaultBaseURL is the default endpoint for the agentgateway
// provider — a local OpenAI-compatible gateway. The OpenAI client appends /v1
// to it, so requests land on <base>/v1/chat/completions.
const agentgatewayDefaultBaseURL = "http://localhost:4000"

// NewAgentGateway creates an agentgateway model.LLM. agentgateway is an
// OpenAI-compatible local gateway, so the client is the OpenAI one pointed at
// the gateway's base URL. No API key is required: NewOpenAI substitutes a
// dummy key whenever a base URL is set, and a gateway that needs one can still
// be given AGENTGATEWAY_API_KEY.
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
	if routesToOllama(modelName) {
		opts.UseLegacyMaxTokens = true
	}
	llm, err := NewOpenAI(ctx, modelName, apiKey, baseURL, opts)
	if err != nil {
		return nil, fmt.Errorf("creating agentgateway client: %w", err)
	}
	return llm, nil
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
