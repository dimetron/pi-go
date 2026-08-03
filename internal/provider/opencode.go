package provider

import (
	"context"
	"fmt"
	"strings"

	"google.golang.org/adk/v2/model"
)

// opencodeDefaultBaseURL is the OpenCode Go API base (OpenAI-family paths append
// /v1; the Anthropic SDK already includes v1/ in its request paths, so it gets
// the parent URL).
const opencodeDefaultBaseURL = "https://opencode.ai/zen/go/v1"

// opencodeGoModelCatalog maps each officially documented OpenCode Go model ID
// to its endpoint family. Values: "chat" (OpenAI chat/completions), "responses"
// (OpenAI responses), "messages" (Anthropic).
//
// Only the 19 officially documented models are included. Undocumented runtime
// extras (glm-5, kimi-k2.5, qwen3.5-plus, mimo-v2-pro, mimo-v2-omni,
// hy3-preview) are NOT routable — selecting one returns an unknown-model error.
var opencodeGoModelCatalog = map[string]string{
	"grok-4.5":          "chat",
	"glm-5.2":           "chat",
	"glm-5.1":           "chat",
	"kimi-k3":           "chat",
	"kimi-k2.7-code":    "chat",
	"kimi-k2.6":         "chat",
	"deepseek-v4-pro":   "chat",
	"deepseek-v4-flash": "chat",
	"mimo-v2.5-pro":     "chat",
	"mimo-v2.5":         "chat",
	"hy3":               "chat",
	"gpt-5.6-luna":      "responses",
	"minimax-m3":        "messages",
	"minimax-m2.7":      "messages",
	"minimax-m2.5":      "messages",
	"qwen3.8-max":       "messages",
	"qwen3.7-max":       "messages",
	"qwen3.7-plus":      "messages",
	"qwen3.6-plus":      "messages",
}

// opencodeAnthropicBaseURL strips the trailing /v1 so the Anthropic SDK's own
// v1/messages path lands on the correct URL.
func opencodeAnthropicBaseURL(baseURL string) string {
	return strings.TrimSuffix(strings.TrimRight(baseURL, "/"), "/v1")
}

// NewOpenCode creates an OpenCode Go model.LLM, routing the model to its
// endpoint family based on the hardcoded catalog.
//
//   - "chat" models → NewOpenAI (chat/completions, Authorization: Bearer)
//   - "responses" models → NewOpenAI (responses, Authorization: Bearer)
//   - "messages" models → NewAnthropic (messages, x-api-key)
//
// Unknown models return an error. The opencode/ prefix must already be stripped.
func NewOpenCode(ctx context.Context, modelName, apiKey, baseURL, thinkingLevel string, opts *LLMOptions) (model.LLM, error) {
	if baseURL == "" {
		baseURL = opencodeDefaultBaseURL
	}

	family, ok := opencodeGoModelCatalog[modelName]
	if !ok {
		return nil, fmt.Errorf("unknown OpenCode Go model %q", modelName)
	}

	switch family {
	case "chat", "responses":
		return NewOpenAI(ctx, modelName, apiKey, baseURL, opts)
	case "messages":
		anthropicBase := opencodeAnthropicBaseURL(baseURL)
		return NewAnthropic(ctx, modelName, apiKey, anthropicBase, thinkingLevel, opts)
	default:
		return nil, fmt.Errorf("unknown OpenCode Go endpoint family %q for model %q", family, modelName)
	}
}
