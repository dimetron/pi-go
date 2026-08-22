package provider

import (
	"context"
	"fmt"
	"iter"
	"net/http"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

const openrouterDefaultBaseURL = "https://openrouter.ai/api/v1"

// openrouterModel implements model.LLM for the OpenRouter API.
// OpenRouter exposes an OpenAI-compatible chat completions endpoint,
// so we reuse the OpenAI SDK with a custom base URL.
type openrouterModel struct {
	modelName string
	client    openai.Client
}

// NewOpenRouter creates an OpenRouter model.LLM.
// If baseURL is empty, the default OpenRouter API endpoint is used.
func NewOpenRouter(_ context.Context, modelName, apiKey, baseURL string, llmOpts *LLMOptions) (model.LLM, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("openrouter API key is required (set OPENROUTER_API_KEY)")
	}
	if baseURL == "" {
		baseURL = openrouterDefaultBaseURL
	}
	opts := []option.RequestOption{
		option.WithAPIKey(apiKey),
		option.WithBaseURL(baseURL),
	}
	if llmOpts != nil {
		for k, v := range llmOpts.ExtraHeaders {
			opts = append(opts, option.WithHeader(k, v))
		}
		transport, err := BuildTransport(llmOpts)
		if err != nil {
			return nil, err
		}
		if transport != nil {
			opts = append(opts, option.WithHTTPClient(&http.Client{Transport: transport}))
		}
	}
	client := openai.NewClient(opts...)
	return &openrouterModel{modelName: modelName, client: client}, nil
}

func (m *openrouterModel) Name() string { return m.modelName }

func (m *openrouterModel) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		messages, systemInstruction := oaiContentsToMessages(req.Contents, req.Config)

		modelName := req.Model
		if modelName == "" {
			modelName = m.modelName
		}

		params := openai.ChatCompletionNewParams{
			Model:    modelName,
			Messages: messages,
		}
		if systemInstruction != "" {
			params.Messages = append([]openai.ChatCompletionMessageParamUnion{
				openai.SystemMessage(systemInstruction),
			}, params.Messages...)
		}

		if req.Config != nil && len(req.Config.Tools) > 0 {
			params.Tools = oaiGenaiToolsToOpenAI(req.Config.Tools)
			params.ToolChoice = openai.ChatCompletionToolChoiceOptionUnionParam{
				OfAuto: openai.String("auto"),
			}
		}

		if stream {
			retryStream(ctx, streamRetryConfig(), yield, func(y func(*model.LLMResponse, error) bool) {
				oaiRunStreaming(ctx, &m.client, params, y)
			})
		} else {
			oaiRunNonStreaming(ctx, &m.client, params, yield)
		}
	}
}

// openrouterFinishReasonToGenai maps OpenRouter finish_reason to genai.FinishReason.
// OpenRouter uses the same finish reasons as OpenAI.
func openrouterFinishReasonToGenai(reason string) genai.FinishReason {
	return oaiFinishReasonToGenai(reason)
}
