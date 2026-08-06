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
	"google.golang.org/adk/v2/model"
)

// DefaultAzureAPIVersion is the api-version used when neither the caller nor
// OPENAI_API_VERSION supplies one.
const DefaultAzureAPIVersion = "2025-04-01-preview"

// AzureAPIKey resolves the Azure credential from an explicit value, then the
// three accepted environment variables, in the order NewAzureOpenAI uses.
//
// Exported so `pi ping` authenticates exactly as a real run does rather than
// reimplementing the fallback chain and disagreeing about which var wins.
func AzureAPIKey(apiKey string) string {
	if apiKey != "" {
		return apiKey
	}
	for _, env := range []string{"AZURE_OPENAI_API_KEY", "AZUREOPENAI_API_KEY", "AZURE_API_KEY"} {
		if v := osGetenv(env); v != "" {
			return v
		}
	}
	return ""
}

// AzureEndpoint resolves the Azure resource endpoint, falling back to
// AZURE_OPENAI_ENDPOINT.
func AzureEndpoint(endpoint string) string {
	if endpoint != "" {
		return endpoint
	}
	return osGetenv("AZURE_OPENAI_ENDPOINT")
}

// AzureAPIVersion resolves the api-version, falling back to OPENAI_API_VERSION
// and then DefaultAzureAPIVersion.
func AzureAPIVersion(apiVersion string) string {
	if apiVersion != "" {
		return apiVersion
	}
	if v := osGetenv("OPENAI_API_VERSION"); v != "" {
		return v
	}
	return DefaultAzureAPIVersion
}

// IsAzureCompatProxyEndpoint reports whether the endpoint is an
// OpenAI-compatible gateway rather than a native Azure resource, in which case
// deployment-path rewriting and the api-version parameter must be skipped.
func IsAzureCompatProxyEndpoint(endpoint string) bool {
	return isAzureOpenAICompatProxyEndpoint(endpoint)
}

// AzureProbePath returns the path `pi ping` should GET for an Azure endpoint,
// built from the same rules NewAzureOpenAI applies to real traffic.
//
// A native resource gets /openai/deployments/{deployment}?api-version=…, the
// data-plane route that actually proves the deployment exists and the key is
// scoped to it. Pinging the bare host instead — which is what ping did before —
// only proved a TLS handshake to Azure's front door, and returned the same 404
// whether the deployment was missing, misnamed, or fine.
//
// A compat proxy gets /models, since deployment paths and api-version break
// routing there for the same reason they are skipped on real requests.
func AzureProbePath(deployment, apiVersion, endpoint string) string {
	if IsAzureCompatProxyEndpoint(endpoint) {
		return "/models"
	}
	path := "/openai/deployments"
	if deployment != "" {
		path += "/" + url.PathEscape(deployment)
	}
	return path + "?api-version=" + url.QueryEscape(AzureAPIVersion(apiVersion))
}

// NewAzureOpenAI creates an Azure OpenAI model.LLM.
// It uses AZURE_OPENAI_API_KEY, AZURE_OPENAI_ENDPOINT, and OPENAI_API_VERSION (defaults to 2025-04-01-preview).
// The deploymentName is the Azure deployment name (not the model ID).
func NewAzureOpenAI(_ context.Context, deploymentName, apiKey, endpoint, apiVersion string, llmOpts *LLMOptions) (model.LLM, error) {
	apiKey = AzureAPIKey(apiKey)
	endpoint = AzureEndpoint(endpoint)
	apiVersion = AzureAPIVersion(apiVersion)
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
		transport, err := BuildTransport(llmOpts)
		if err != nil {
			return nil, err
		}
		if transport != nil {
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
