package provider

import (
	"context"
	"fmt"
	"iter"
	"net/http"
	"os"

	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/model/gemini"
	"google.golang.org/genai"
)

// NewGemini creates a Gemini model.LLM using ADK Go's native Gemini support.
// It reads the API key from GEMINI_API_KEY or GOOGLE_API_KEY env vars.
// If neither is set, it falls back to Application Default Credentials.
// If baseURL is non-empty, it overrides the default API endpoint.
func NewGemini(ctx context.Context, modelName, baseURL string, opts *LLMOptions) (model.LLM, error) {
	cfg := &genai.ClientConfig{
		Backend: genai.BackendGeminiAPI,
	}

	// Check for API key in env vars
	if apiKey := geminiAPIKey(); apiKey != "" {
		cfg.APIKey = apiKey
	}

	cfg.HTTPOptions = geminiHTTPOptions(baseURL, opts)
	if geminiNeedsHTTPClient(opts) {
		httpClient, err := BuildHTTPClient(opts, 0)
		if err != nil {
			return nil, err
		}
		cfg.HTTPClient = httpClient
	}

	llm, err := gemini.NewModel(ctx, modelName, cfg)
	if err != nil {
		return nil, fmt.Errorf("creating gemini model %q: %w", modelName, err)
	}

	return geminiRetryModel{inner: llm}, nil
}

// geminiSDKRetryAttempts is how many times the genai SDK sends one HTTP
// request, the initial call included.
//
// The SDK's retry is off unless RetryOptions is set, so without this a Gemini
// request got no retry at all — not even for a dropped connection. It is set
// low rather than at the SDK's default of 5 because it is the inner of two
// nets and the two multiply: geminiRetryModel re-sends the whole request up to
// five more times on top of whatever happens here.
//
// Three is the split that matches what each net is good at. The SDK retries
// blind, on a fixed exponential schedule that cannot read the server's own
// "retry in 13s" hint, so it is useful for faults that clear in a second or
// two — a reset connection, a timeout, a 5xx blip — and close to useless for a
// per-minute quota window. Two quick retries here cover those, and the outer
// net, which does honor the server's window, owns the rate-limit case.
//
// 429 is deliberately left in the SDK's retryable set even so, because a
// non-streaming call (commit messages, summaries) never reaches the outer net
// and would otherwise have nothing retrying it.
const geminiSDKRetryAttempts int32 = 3

// geminiRetryModel re-sends a Gemini streaming request that failed before it
// emitted anything, the way every other provider in this package does.
//
// Gemini was the one provider that reached ADK's model directly, so it had
// neither net: the SDK's retry was disabled (see geminiSDKRetryAttempts) and
// the per-request retry that internal/provider gives OpenAI, Anthropic,
// Mistral, OpenRouter, xAI and Ollama was never wired up. That left only the
// coarse per-run retry in internal/agent, which refuses once a turn has
// yielded anything — so a rate limit arriving after the turn's first tool call
// ended the turn, even though the error named the seconds to wait.
type geminiRetryModel struct {
	inner model.LLM
}

var _ model.LLM = geminiRetryModel{}

func (m geminiRetryModel) Name() string { return m.inner.Name() }

// GenerateContent forwards to the wrapped model, retrying a streaming request
// that fails before producing output. A non-streaming call is passed straight
// through: it has no partial state to protect, and the SDK's own retry already
// covers it.
func (m geminiRetryModel) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		// send performs the request once. It takes its own yield so
		// retryStream can re-run it across attempts.
		send := func(y func(*model.LLMResponse, error) bool) {
			for resp, err := range m.inner.GenerateContent(ctx, req, stream) {
				if !y(resp, err) {
					return
				}
			}
		}

		if !stream {
			send(yield)
			return
		}
		retryStream(ctx, streamRetryConfig(), yield, send)
	}
}

// geminiAPIKey reads the Gemini API key from the environment, preferring
// GEMINI_API_KEY over GOOGLE_API_KEY. It returns "" when neither is set, in
// which case the client falls back to Application Default Credentials.
func geminiAPIKey() string {
	if apiKey := os.Getenv("GEMINI_API_KEY"); apiKey != "" {
		return apiKey
	}
	return os.Getenv("GOOGLE_API_KEY")
}

// geminiHTTPOptions builds the HTTP options for the Gemini client: the retry
// policy, which is always set because the SDK does not retry at all without
// it, plus the endpoint and header overrides when they were asked for. An
// empty BaseURL leaves the SDK on its default endpoint, so the struct is safe
// to set unconditionally.
func geminiHTTPOptions(baseURL string, opts *LLMOptions) genai.HTTPOptions {
	attempts := geminiSDKRetryAttempts
	httpOpts := genai.HTTPOptions{
		RetryOptions: &genai.HTTPRetryOptions{Attempts: &attempts},
	}
	if baseURL != "" {
		httpOpts.BaseURL = baseURL
	}
	if opts != nil && len(opts.ExtraHeaders) > 0 {
		httpOpts.Headers = make(http.Header)
		for k, v := range opts.ExtraHeaders {
			httpOpts.Headers.Set(k, v)
		}
	}
	return httpOpts
}

// geminiNeedsHTTPClient reports whether opts asks for transport settings that
// only a custom *http.Client can carry.
func geminiNeedsHTTPClient(opts *LLMOptions) bool {
	return opts != nil && (opts.InsecureSkipTLS || opts.CACertPath != "" ||
		opts.ConnectTimeout > 0 || opts.RateLimit.Enabled())
}
