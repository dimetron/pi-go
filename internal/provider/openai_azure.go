package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"google.golang.org/adk/model"
)

// NewAzureOpenAI creates an Azure OpenAI model.LLM.
// It uses AZURE_OPENAI_API_KEY, AZURE_OPENAI_ENDPOINT, and OPENAI_API_VERSION (defaults to 2025-04-01-preview).
// The deploymentName is the Azure deployment name (not the model ID).
func NewAzureOpenAI(_ context.Context, deploymentName, apiKey, endpoint, apiVersion string, llmOpts *LLMOptions) (model.LLM, error) {
	if apiKey == "" {
		apiKey = osGetenv("AZURE_OPENAI_API_KEY")
		if apiKey == "" {
			apiKey = osGetenv("AZUREOPENAI_API_KEY")
		}
		if apiKey == "" {
			apiKey = osGetenv("AZURE_API_KEY")
		}
	}
	if endpoint == "" {
		endpoint = osGetenv("AZURE_OPENAI_ENDPOINT")
	}
	if apiVersion == "" {
		apiVersion = osGetenv("OPENAI_API_VERSION")
	}
	if apiVersion == "" {
		apiVersion = "2025-04-01-preview"
	}
	if endpoint == "" {
		return nil, fmt.Errorf("azure OpenAI endpoint is required (set AZURE_OPENAI_ENDPOINT)")
	}

	baseURL := strings.TrimSuffix(endpoint, "/") + "/"
	opts := []option.RequestOption{option.WithBaseURL(baseURL)}
	// Some enterprise gateways expose Azure models behind an OpenAI-compatible
	// proxy path (for example, /api/v1/proxy). In that mode, Azure-specific path
	// rewriting and forced api-version query parameters break routing/auth.
	if !isAzureOpenAICompatProxyEndpoint(endpoint) {
		opts = append(opts,
			option.WithQueryAdd("api-version", apiVersion),
			option.WithMiddleware(azurePathRewriteMiddleware()),
		)
	}
	if apiKey != "" {
		opts = append(opts, option.WithHeader("Api-Key", apiKey))
	}
	if llmOpts != nil {
		for k, v := range llmOpts.ExtraHeaders {
			opts = append(opts, option.WithHeader(k, v))
		}
		if transport := BuildTransport(llmOpts); transport != nil {
			opts = append(opts, option.WithHTTPClient(&http.Client{Transport: transport}))
		}
	}

	client := openai.NewClient(opts...)
	return &openaiModel{modelName: deploymentName, client: client}, nil
}

func isAzureOpenAICompatProxyEndpoint(endpoint string) bool {
	u, err := url.Parse(endpoint)
	if err != nil {
		return false
	}
	path := strings.ToLower(strings.TrimSpace(u.Path))
	if path == "" || path == "/" {
		return false
	}
	return strings.Contains(path, "/openai/v1")
}

// azurePathRewriteMiddleware rewrites .../chat/completions and .../responses to
// .../openai/deployments/{deployment}/... so Azure can route to the correct deployment.
func azurePathRewriteMiddleware() option.Middleware {
	return func(r *http.Request, next option.MiddlewareNext) (*http.Response, error) {
		pathSuffix := strings.TrimPrefix(r.URL.Path, "/")
		var suffix string
		switch {
		case strings.HasSuffix(pathSuffix, "chat/completions"):
			suffix = "chat/completions"
		case strings.HasSuffix(pathSuffix, "responses"):
			suffix = "responses"
		case strings.HasSuffix(pathSuffix, "completions"):
			suffix = "completions"
		case strings.HasSuffix(pathSuffix, "embeddings"):
			suffix = "embeddings"
		default:
			return next(r)
		}
		if r.Body == nil {
			return next(r)
		}
		var buf bytes.Buffer
		if _, err := buf.ReadFrom(r.Body); err != nil {
			return nil, err
		}
		r.Body = io.NopCloser(&buf)
		var payload struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(bytes.NewReader(buf.Bytes())).Decode(&payload); err != nil || payload.Model == "" {
			r.Body = io.NopCloser(bytes.NewReader(buf.Bytes()))
			return next(r)
		}
		deployment := url.PathEscape(payload.Model)
		// Keep base path (e.g. /api/v1/proxy), replace suffix with Azure-style path
		basePath := strings.TrimSuffix(r.URL.Path, suffix)
		basePath = strings.TrimRight(basePath, "/")
		r.URL.Path = basePath + "/openai/deployments/" + deployment + "/" + suffix
		r.Body = io.NopCloser(bytes.NewReader(buf.Bytes()))
		return next(r)
	}
}

// osGetenv wraps os.Getenv for testability.
var osGetenv = func(key string) string {
	return os.Getenv(key)
}
