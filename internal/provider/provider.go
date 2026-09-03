package provider

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/dimetron/pi-go/internal/auth"
	"github.com/dimetron/pi-go/internal/ratelimit"

	"google.golang.org/adk/v2/model"
)

// BuildTransport creates an http.RoundTripper carrying the TLS trust, connect
// timeout and extra headers from opts. It returns (nil, nil) when opts asks for
// no customization, so callers can leave the SDK's own client in place.
//
// Every variant starts from a clone of http.DefaultTransport rather than a
// fresh &http.Transport{}: the default carries ProxyFromEnvironment,
// connection pooling and HTTP/2, and building from scratch silently drops
// HTTPS_PROXY — which is exactly the environment where a custom CA or a TLS
// skip is needed in the first place.
func BuildTransport(opts *LLMOptions) (http.RoundTripper, error) {
	// TraceHTTP is checked before the nil guard: a nil opts still has to be
	// traceable, because several callers pass one and they issue real requests.
	traceHTTP := opts != nil && opts.TraceHTTP
	if opts == nil {
		return maybeTrace(nil, traceHTTP), nil
	}
	hasHeaders := len(opts.ExtraHeaders) > 0
	needsTLS := opts.InsecureSkipTLS || opts.CACertPath != ""
	needsPacing := opts.RateLimit.Enabled()
	if !needsTLS && !hasHeaders && !needsPacing && opts.ConnectTimeout <= 0 {
		return maybeTrace(nil, traceHTTP), nil
	}

	base := http.DefaultTransport
	if def, ok := http.DefaultTransport.(*http.Transport); ok && (needsTLS || opts.ConnectTimeout > 0) {
		cloned := def.Clone()
		if needsTLS {
			tlsConfig, err := buildTLSConfig(opts)
			if err != nil {
				return nil, err
			}
			cloned.TLSClientConfig = tlsConfig
		}
		if opts.ConnectTimeout > 0 {
			// Bounds connection establishment only, so a long request timeout
			// (streaming completions run for minutes) doesn't mean an
			// unreachable endpoint hangs for that whole budget.
			dialer := &net.Dialer{Timeout: opts.ConnectTimeout, KeepAlive: 30 * time.Second}
			cloned.DialContext = dialer.DialContext
		}
		base = cloned
	}
	// Innermost, beneath headerTransport: the trace has to record the request
	// as it actually goes on the wire. Wrapping the other way round would run
	// the trace first and log a request missing every ExtraHeader the server
	// went on to receive — the headers a proxy or gateway problem is usually
	// about.
	base = maybeTrace(base, traceHTTP)
	if hasHeaders {
		base = &headerTransport{base: base, headers: opts.ExtraHeaders}
	}
	// Outermost, above the trace and the header injection: the wait has to
	// happen before the request is spent, and the trace should record the
	// moment the request actually went on the wire rather than the moment the
	// caller queued it. Wrapping the other way round would log a send time
	// that could be a minute earlier than the send.
	if needsPacing {
		base = &ratelimit.Transport{
			Base:    base,
			Limiter: ratelimit.Shared(opts.RateLimitScope, opts.RateLimit),
		}
	}
	return base, nil
}

// maybeTrace wraps base in the HTTP trace transport when tracing is on.
//
// A nil base means "no customization was needed"; that becomes
// http.DefaultTransport rather than staying nil, because a nil RoundTripper
// tells the caller to leave the SDK's own client alone and the trace would
// never run.
func maybeTrace(base http.RoundTripper, trace bool) http.RoundTripper {
	if !trace {
		return base
	}
	if base == nil {
		base = http.DefaultTransport
	}
	return &traceTransport{base: base}
}

// buildTLSConfig turns the TLS fields of opts into a *tls.Config.
// InsecureSkipTLS wins over CACertPath: it is the bigger hammer, and honoring
// a CA pool while verification is off would be misleading.
func buildTLSConfig(opts *LLMOptions) (*tls.Config, error) {
	if opts.InsecureSkipTLS {
		return &tls.Config{InsecureSkipVerify: true}, nil //nolint:gosec // user-requested
	}

	caCert, err := os.ReadFile(opts.CACertPath)
	if err != nil {
		return nil, fmt.Errorf("reading CA certificate %s: %w", opts.CACertPath, err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caCert) {
		return nil, fmt.Errorf("no PEM certificate found in %s", opts.CACertPath)
	}

	// Additive by default: trust the corporate CA *and* the public roots, so
	// pointing at an intercepting proxy doesn't break every other endpoint.
	// An unreadable system pool is not fatal — fall back to the CA alone.
	if !opts.DisableSystemCAs {
		if systemCAs, sysErr := x509.SystemCertPool(); sysErr == nil {
			systemCAs.AppendCertsFromPEM(caCert)
			roots = systemCAs
		}
	}
	return &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}, nil
}

// BuildHTTPClient creates an *http.Client with the TLS trust, extra headers,
// connect timeout and request timeout from opts. Returns a client with the
// default transport when opts asks for no customization.
func BuildHTTPClient(opts *LLMOptions, timeout time.Duration) (*http.Client, error) {
	transport, err := BuildTransport(opts)
	if err != nil {
		return nil, err
	}
	if transport == nil {
		return &http.Client{Timeout: timeout}, nil
	}
	return &http.Client{Timeout: timeout, Transport: transport}, nil
}

// headerTransport injects extra HTTP headers into every request.
type headerTransport struct {
	base    http.RoundTripper
	headers map[string]string
}

func (t *headerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// RoundTrippers must not modify the request they are given: the caller
	// still owns it, and the SDKs reuse it across retries.
	req = req.Clone(req.Context())
	for k, v := range t.headers {
		req.Header.Set(k, v)
	}
	return t.base.RoundTrip(req)
}

// BackendName returns a safe description of the selected request backend.
// It intentionally exposes no credential material.
func BackendName(info Info, apiKey, baseURL string) string {
	if baseURL != "" {
		return info.Provider + "-custom"
	}
	if info.Provider == "openai" {
		if auth.IsCodexOAuthToken(apiKey) {
			return "openai-codex-chatgpt"
		}
		return "openai-platform"
	}
	return info.Provider
}

// Info describes a provider and the model to use.
type Info struct {
	Provider string
	Model    string
	Ollama   bool // true when model is served by Ollama
	// LocalOllama records that the model was named with the explicit ollama/
	// prefix, which the CLI help documents as "Ollama, local". It keeps a
	// cloud-looking tag on such a name from routing to api.ollama.com.
	LocalOllama bool
	Custom      bool // true when using an explicit custom OpenAI-compatible endpoint
	// BaseURL is the endpoint finally selected for this model, recorded so a
	// session transcript identifies the backend and not just the model name.
	// The same name served by ollama, by a gateway, and by a vendor API behaves
	// differently, and a transcript without it cannot be reproduced.
	BaseURL string
}

// Known model prefixes mapped to providers.
var modelPrefixes = map[string]string{
	"claude":    "anthropic",
	"gpt":       "openai",
	"gpt-5":     "openai",
	"gemini":    "gemini",
	"mistral":   "mistral",
	"magistral": "mistral",
	"grok":      "xai",
}

// OllamaModelPrefixes are model prefixes that previously auto-routed to Ollama.
// Bare Ollama model names are intentionally not auto-detected; use the explicit
// ollama/ prefix, or the :cloud tag for Ollama cloud models.
var OllamaModelPrefixes = []string{}

// IsOllamaCloudModel reports whether a model name is tagged for ollama.com's
// hosted service. Ollama publishes both forms — a bare ":cloud" and the
// ":<size>-cloud" that most of the catalog uses — so a check for one alone
// silently misses the other.
//
// This is the single fact that decides local versus cloud, and it is the
// model's name that decides it. Nothing about the caller's environment enters
// into it.
func IsOllamaCloudModel(modelName string) bool {
	return strings.HasSuffix(modelName, ":cloud") || strings.HasSuffix(modelName, "-cloud")
}

// KnownModels lists recognized model names per provider.
// The check is prefix-based: a model is valid if it starts with any entry.
// Ollama models are not validated here (they are dynamic).
//
// OpenAI and Anthropic IDs are loaded from embedded llm-prices snapshots under
// modeldata/. Gemini and Mistral IDs are maintained in model_catalog.go.
// Update context-window metadata in modeldata/context-windows.json in the same
// change when official limits change.
var KnownModels = mustLoadKnownModels()

// contextWindowSizes maps model name prefixes to context window sizes (in
// tokens), flattened across every vendor except Azure.
var contextWindowSizes = mustLoadContextWindowSizes()

// contextWindowSizesByProvider keeps each vendor's windows addressable on their
// own, which is the only way to answer for an Azure deployment whose name
// matches an OpenAI model with a different window.
var contextWindowSizesByProvider = mustLoadContextWindowSizesByProvider()

// ContextWindowSize returns the context window size for a model (in tokens).
// Returns 0 if the model is unknown.
//
// This searches every vendor except Azure. Prefer ContextWindowSizeFor when the
// provider is known — an Azure deployment shares its name with an OpenAI model
// but not necessarily its window, so only the provider-aware lookup separates
// them.
func ContextWindowSize(modelName string) int64 {
	return longestPrefixSize(contextWindowSizes, modelName)
}

// ContextWindowSizeFor returns the context window for a model as served by a
// specific provider, falling back to the vendor-agnostic table when that
// provider publishes no window of its own.
//
// The fallback is what keeps a newly provisioned Azure deployment usable when
// an OpenAI entry matches the name it was provisioned from — that beats
// returning 0, since zero disables auto-compaction and lets the session grow
// unchecked until the API rejects it. A name that matches nothing anywhere
// still returns 0; there is no window to guess.
func ContextWindowSizeFor(providerName, modelName string) int64 {
	key := strings.ToLower(strings.TrimSpace(providerName))
	if sizes, ok := contextWindowSizesByProvider[key]; ok {
		if size := longestPrefixSize(sizes, modelName); size > 0 {
			return size
		}
	}
	return ContextWindowSize(modelName)
}

// ModelWindow pairs a model or deployment name with its context window.
type ModelWindow struct {
	Name          string
	ContextWindow int64
}

// AzureDeployments returns the cataloged Azure OpenAI deployments and the
// context window each was provisioned with, sorted by name.
//
// Azure has no listing endpoint reachable with only an API key — enumerating
// deployments needs ARM credentials and the resource ID — so this is the
// embedded catalog, not a live query. It is therefore a description of one
// subscription's deployments and may not match another's.
func AzureDeployments() []ModelWindow {
	sizes := contextWindowSizesByProvider["azure"]
	out := make([]ModelWindow, 0, len(sizes))
	for name, window := range sizes {
		out = append(out, ModelWindow{Name: name, ContextWindow: window})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// longestPrefixSize resolves modelName against a prefix table, preferring the
// longest match so "gpt-5.1" beats "gpt-5" and "o1-mini" beats "o1".
func longestPrefixSize(sizes map[string]int64, modelName string) int64 {
	lower := strings.ToLower(modelName)
	lower = strings.TrimPrefix(lower, "ollama/")
	lower = strings.TrimPrefix(lower, "azure/")
	bestLen := 0
	var bestSize int64
	for prefix, size := range sizes {
		if strings.HasPrefix(lower, prefix) && len(prefix) > bestLen {
			bestLen = len(prefix)
			bestSize = size
		}
	}
	return bestSize
}

// ValidateModel checks whether the model name is recognized for its provider.
// Returns an error with suggestions if the model is unknown.
// Ollama and custom endpoint models are always considered valid (they are dynamic).
func ValidateModel(info Info) error {
	if info.Ollama || info.Custom {
		return nil
	}
	known := CatalogFor(info.Provider)
	if len(known) == 0 {
		return nil // unknown provider, skip validation
	}
	lower := strings.ToLower(info.Model)
	if matchPrefix(known, lower) {
		return nil
	}
	// Validation miss: refresh once when an API key is available, then
	// re-check against what the provider just returned. Network errors are
	// non-fatal.
	//
	// Match against the returned slice rather than re-reading CatalogFor:
	// RefreshCatalog deliberately returns the fetched models even when it
	// could not persist them (no resolvable cache dir, a read-only or full
	// cache), and re-reading would then see only the embedded snapshot. A
	// model the provider just confirmed must not be rejected because caching
	// failed.
	if key := apiKeyForProvider(info.Provider); key != "" {
		opts := ListModelsOptions{APIKey: key, BaseURL: baseURLForProvider(info.Provider)}
		if fresh, err := RefreshCatalog(context.Background(), info.Provider, opts); err == nil || len(fresh) > 0 {
			ids := make([]string, 0, len(fresh))
			for _, m := range fresh {
				ids = append(ids, strings.ToLower(m.ID))
			}
			if matchPrefix(ids, lower) {
				return nil
			}
		}
	}
	return fmt.Errorf("unknown %s model %q; known models: %s",
		info.Provider, info.Model, strings.Join(known, ", "))
}

// matchPrefix reports whether lower starts with any of the given prefixes.
func matchPrefix(prefixes []string, lower string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

// apiKeyForProvider returns the API key for a provider from the conventional
// env var, without importing internal/config (which would risk an import
// cycle). Only providers that support /v1/models are listed.
func apiKeyForProvider(p string) string {
	switch p {
	case "anthropic":
		return os.Getenv("ANTHROPIC_API_KEY")
	case "openai":
		return os.Getenv("OPENAI_API_KEY")
	case "gemini":
		return os.Getenv("GEMINI_API_KEY")
	case "mistral":
		return os.Getenv("MISTRAL_API_KEY")
	case "xai":
		return os.Getenv("XAI_API_KEY")
	case "openrouter":
		return os.Getenv("OPENROUTER_API_KEY")
	case "agentgateway":
		return os.Getenv("AGENTGATEWAY_API_KEY")
	}
	return ""
}

// baseURLForProvider returns the configured endpoint override for a provider,
// from the same env vars config.BaseURLs reads. Without it a validation refresh
// would go to the vendor's public API even for a user who has pointed pi at a
// gateway, which answers 401 and turns a valid model into "unknown". Empty
// means "use the provider default"; internal/config is not imported here
// because that would be an import cycle.
func baseURLForProvider(p string) string {
	switch p {
	case "anthropic":
		return os.Getenv("ANTHROPIC_BASE_URL")
	case "openai":
		return os.Getenv("OPENAI_BASE_URL")
	case "gemini":
		return os.Getenv("GEMINI_BASE_URL")
	case "mistral":
		return os.Getenv("MISTRAL_BASE_URL")
	case "xai":
		return os.Getenv("XAI_BASE_URL")
	case "openrouter":
		return os.Getenv("OPENROUTER_BASE_URL")
	case "agentgateway":
		return os.Getenv("AGENTGATEWAY_BASE_URL")
	}
	return ""
}

// ResolveWithBaseURL determines the provider from a model name.
// When baseURL is provided and the model is not otherwise recognized, it routes
// the model to an OpenAI-compatible custom endpoint. The optional openai/ prefix
// is stripped so `--model openai/foo` sends `foo` to the custom endpoint.
func ResolveWithBaseURL(modelName, baseURL string) (Info, error) {
	if baseURL != "" {
		lower := strings.ToLower(modelName)
		if strings.HasPrefix(lower, "ollama/") || strings.HasPrefix(lower, "azure/") {
			return Resolve(modelName)
		}
		if strings.HasPrefix(lower, "openrouter/") {
			return Resolve(modelName)
		}
		if strings.HasPrefix(lower, "openai/") {
			modelName = modelName[len("openai/"):]
			return Info{Provider: "openai", Model: modelName, Custom: true}, nil
		}
		info, err := Resolve(modelName)
		if err == nil && !info.Ollama {
			info.Custom = true
			return info, nil
		}
		return Info{Provider: "openai", Model: modelName, Custom: true}, nil
	}

	return Resolve(modelName)
}

// Resolve determines the provider from a model name.
// Ollama models are routed to the native "ollama" provider.
func Resolve(modelName string) (Info, error) {
	if modelName == "" {
		return Info{}, fmt.Errorf("no model specified")
	}

	// Detect ollama/ prefix → native Ollama provider.
	// The prefix is stripped; the remainder is the Ollama model name.
	if strings.HasPrefix(strings.ToLower(modelName), "ollama/") {
		return Info{Provider: "ollama", Model: modelName[len("ollama/"):], Ollama: true, LocalOllama: true}, nil
	}

	// Detect azure/ prefix → Azure OpenAI provider.
	// The prefix is stripped; the remainder is the Azure deployment name.
	if strings.HasPrefix(strings.ToLower(modelName), "azure/") {
		return Info{Provider: "azure", Model: modelName[len("azure/"):]}, nil
	}

	// Detect opencode/ prefix → OpenCode Go provider.
	// The prefix is stripped; the remainder is the bare model ID.
	if strings.HasPrefix(strings.ToLower(modelName), "opencode/") {
		return Info{Provider: "opencode", Model: modelName[len("opencode/"):]}, nil
	}

	// Detect openrouter/ prefix → OpenRouter provider.
	// The prefix is stripped; the remainder is the bare model ID.
	if strings.HasPrefix(strings.ToLower(modelName), "openrouter/") {
		return Info{Provider: "openrouter", Model: modelName[len("openrouter/"):]}, nil
	}

	// Detect agentgateway/ prefix → agentgateway provider. Checked before the
	// :cloud/-cloud suffix check below: agentgateway model IDs carry a
	// "-cloud" tag (e.g. deepseek-v4-flash:0731-cloud) that would otherwise
	// route them to Ollama.
	if strings.HasPrefix(strings.ToLower(modelName), "agentgateway/") {
		return Info{Provider: "agentgateway", Model: modelName[len("agentgateway/"):]}, nil
	}

	// Detect mistral/ prefix → native Mistral provider.
	// The prefix is stripped; the remainder is the Mistral model name.
	if strings.HasPrefix(strings.ToLower(modelName), "mistral/") {
		return Info{Provider: "mistral", Model: modelName[len("mistral/"):]}, nil
	}

	// Detect :cloud or -cloud suffix → native Ollama provider.
	// Keep the full model name — :cloud and -cloud are valid Ollama model tags.
	if IsOllamaCloudModel(modelName) {
		return Info{Provider: "ollama", Model: modelName, Ollama: true}, nil
	}

	lower := strings.ToLower(modelName)
	for prefix, provider := range modelPrefixes {
		if strings.HasPrefix(lower, prefix) {
			return Info{Provider: provider, Model: modelName}, nil
		}
	}

	// Detect common Ollama model prefixes → native Ollama provider.
	for _, prefix := range OllamaModelPrefixes {
		if strings.HasPrefix(lower, prefix) {
			model := modelName
			if !strings.Contains(model, ":") {
				model = modelName + ":latest"
			}
			return Info{Provider: "ollama", Model: model, Ollama: true}, nil
		}
	}

	return Info{}, fmt.Errorf("unknown model %q: cannot determine provider (known prefixes: claude, gpt, gemini, mistral, grok, openrouter, agentgateway; use ollama/ prefix for Ollama, or :cloud/-cloud suffix for Ollama cloud)", modelName)
}

func normalizeBaseURL(baseURL string) string {
	if baseURL == "" {
		return ""
	}
	if !strings.Contains(baseURL, "://") {
		return "http://" + baseURL
	}
	return baseURL
}

// CheckOllama verifies that the Ollama server at baseURL is reachable.
// It first checks TCP connectivity on the port, then issues a GET to the root
// endpoint (Ollama returns "Ollama is running").
func CheckOllama(baseURL string) error {
	baseURL = normalizeBaseURL(baseURL)
	u, err := url.Parse(baseURL)
	if err != nil {
		return fmt.Errorf("invalid Ollama URL %q: %w", baseURL, err)
	}

	host := u.Host
	if !strings.Contains(host, ":") {
		if u.Scheme == "https" {
			host += ":443"
		} else {
			host += ":80"
		}
	}

	// TCP port check.
	conn, err := net.DialTimeout("tcp", host, 3*time.Second)
	if err != nil {
		return fmt.Errorf("ollama not reachable at %s: %w", host, err)
	}
	conn.Close()

	// HTTP health check.
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(baseURL)
	if err != nil {
		return fmt.Errorf("ollama HTTP check failed at %s: %w", baseURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ollama returned status %d at %s", resp.StatusCode, baseURL)
	}
	return nil
}

// LLMOptions holds optional configuration for LLM provider creation.
type LLMOptions struct {
	ExtraHeaders    map[string]string
	InsecureSkipTLS bool
	// CACertPath is a PEM bundle to trust in addition to the system roots —
	// the answer for a TLS-intercepting corporate proxy, which otherwise
	// forces InsecureSkipTLS and drops verification for every endpoint.
	// Ignored when InsecureSkipTLS is set.
	CACertPath string
	// DisableSystemCAs narrows trust to CACertPath alone. Only useful when
	// the endpoint must not be reachable through any public root.
	DisableSystemCAs bool
	// ConnectTimeout bounds connection establishment only. Zero leaves the Go
	// default in place. Distinct from the per-client request timeout, which
	// has to stay generous for streaming completions.
	ConnectTimeout time.Duration
	AdvisorModel   string // Advisor model (e.g., "claude-opus-4-7")
	AdvisorMaxUses int    // Max advisor calls per request (0 = unlimited)
	AdvisorCaching bool   // Enable ephemeral prompt caching for advisor
	// DisablePromptCaching turns OFF the cache_control breakpoints the
	// Anthropic provider stamps on every request. Caching is on by default
	// because it only ever lowers the bill: requests whose prefix is below
	// Anthropic's minimum cacheable length (1024-4096 tokens) are simply
	// not cached, with no error and no extra cost. Other providers ignore
	// this flag today.
	DisablePromptCaching bool
	// TraceHTTP records every LLM request and response — method, URL, full
	// headers and body — to the session log and to OTel span events. Set by
	// --trace-http. Credentials are masked (see httplog.Redact), but prompts
	// and completions are not: the log holds the entire conversation in
	// cleartext, which is the point and also the reason it is off by default.
	TraceHTTP bool
	// EnableXAITools opts into xAI server-side tools (web search, X search,
	// and code interpreter) for xAI Responses API requests.
	EnableXAITools bool
	// RateLimit paces outbound requests so a turn stays inside the provider's
	// per-minute quota rather than being rejected by it. A zero value sends at
	// whatever rate the caller manages. Resolved from config by
	// Config.ResolveRateLimits.
	RateLimit ratelimit.Limits
	// RateLimitScope names the budget RateLimit applies to. Every client
	// aimed at the same budget must pass the same scope, because the quota is
	// enforced per account and not per client — see ratelimit.Shared. Left
	// empty, NewLLM fills it in from the provider, model and base URL.
	RateLimitScope string
}

// NewLLM creates a model.LLM for the given provider info, API key, optional base URL, thinking level, and options.
func NewLLM(ctx context.Context, info Info, apiKey, baseURL, thinkingLevel string, opts *LLMOptions) (model.LLM, error) {
	if opts == nil {
		opts = &LLMOptions{}
	}
	// Name the pacing budget here rather than at every call site: this is the
	// one place that holds the provider, the model and the endpoint together,
	// and every client aimed at the same three has to agree on the name or
	// they get separate buckets over one quota.
	if opts.RateLimitScope == "" {
		opts.RateLimitScope = ratelimit.ScopeFor(info.Provider, info.Model, baseURL)
	}
	switch info.Provider {
	case "ollama":
		return NewOllama(ctx, OllamaRouting{
			Model:      info.Model,
			BaseURL:    baseURL,
			APIKey:     apiKey,
			ForceLocal: info.LocalOllama,
		}, thinkingLevel, opts)
	case "gemini":
		return NewGemini(ctx, info.Model, baseURL, opts)
	case "openai":
		return NewOpenAI(ctx, info.Model, apiKey, baseURL, opts)
	case "azure":
		// For Azure, baseURL can be used as endpoint override if provided via --url flag.
		// API key and api-version are read from env vars if not provided.
		return NewAzureOpenAI(ctx, info.Model, apiKey, baseURL, "", opts)
	case "anthropic":
		return NewAnthropic(ctx, info.Model, apiKey, baseURL, thinkingLevel, opts)
	case "mistral":
		return NewMistral(ctx, info.Model, apiKey, baseURL, thinkingLevel, opts)
	case "openrouter":
		return NewOpenRouter(ctx, info.Model, apiKey, baseURL, thinkingLevel, opts)
	case "xai":
		// xAI server-side tools, including x_search, are enabled for the
		// xAI provider by default. PI_NO_XAI_TOOLS remains the kill switch.
		opts.EnableXAITools = true
		return NewXAI(ctx, info.Model, apiKey, baseURL, thinkingLevel, opts)
	case "opencode":
		return NewOpenCode(ctx, info.Model, apiKey, baseURL, thinkingLevel, opts)
	case "agentgateway":
		return NewAgentGateway(ctx, info.Model, apiKey, baseURL, opts)
	default:
		return nil, fmt.Errorf("unsupported provider: %s", info.Provider)
	}
}

// APIKeyEnvVar returns the environment variable a provider's key is read from.
//
// Kept here rather than in a front-end so every caller — the CLI, the public
// pimodels package, ping — agrees on where a key comes from. A second copy of
// this mapping is how "works in the CLI, not when embedded" bugs start.
func APIKeyEnvVar(providerName string) string {
	switch providerName {
	case "anthropic":
		return "ANTHROPIC_API_KEY"
	case "openai":
		return "OPENAI_API_KEY"
	case "azure":
		return "AZURE_OPENAI_API_KEY"
	case "gemini":
		return "GEMINI_API_KEY"
	default:
		return strings.ToUpper(providerName) + "_API_KEY"
	}
}

// APIKeyFromEnv returns the configured key for a provider, or "" when unset.
// Providers that need no key (ollama on localhost) are fine with the empty
// string; the provider constructors decide, not this helper.
func APIKeyFromEnv(providerName string) string {
	return os.Getenv(APIKeyEnvVar(providerName))
}
