package provider

import (
	"context"

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
func NewAgentGateway(ctx context.Context, modelName, apiKey, baseURL string, opts *LLMOptions) (model.LLM, error) {
	if baseURL == "" {
		baseURL = agentgatewayDefaultBaseURL
	}
	return NewOpenAI(ctx, modelName, apiKey, baseURL, opts)
}
