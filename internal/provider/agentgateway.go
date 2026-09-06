package provider

import (
	"context"
	"fmt"

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
// UseLegacyMaxTokens is forced on because agentgateway typically routes to
// Ollama, whose API only understands the legacy max_tokens field — the newer
// max_completion_tokens is silently ignored, so the model runs unbounded and
// hits Ollama's own 65536-token default. Sending max_tokens lets pi-go's cap
// (defaultOaiMaxOutputTokens = 64000) reach the model instead.
func NewAgentGateway(ctx context.Context, modelName, apiKey, baseURL string, opts *LLMOptions) (model.LLM, error) {
	if baseURL == "" {
		baseURL = agentgatewayDefaultBaseURL
	}
	if opts == nil {
		opts = &LLMOptions{}
	}
	opts.UseLegacyMaxTokens = true
	llm, err := NewOpenAI(ctx, modelName, apiKey, baseURL, opts)
	if err != nil {
		return nil, fmt.Errorf("creating agentgateway client: %w", err)
	}
	return llm, nil
}
