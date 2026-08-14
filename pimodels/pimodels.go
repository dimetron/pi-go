// Package pimodels builds LLM clients for the providers pi-go supports, for use
// from outside pi-go.
//
// It resolves a model name to a provider, finds the API key, and returns a
// [Model] — the ADK interface an agent consumes. That is the whole remit.
//
// # Isolation
//
// This package knows about models and providers. It knows nothing about agents,
// tools, sessions or skills, and it must stay that way: an embedder composes the
// two halves itself.
//
//	m, err := pimodels.New(ctx, "gpt-5.6-luna")
//	if err != nil {
//	    return err
//	}
//	a, err := piagent.New(ctx, piagent.WithModel(m))
//
// Because both packages meet at ADK's model.LLM rather than at each other,
// neither imports the other, and a change to provider handling cannot become a
// breaking change to the agent API.
//
// # API keys
//
// [New] reads the key from the provider's environment variable — OPENAI_API_KEY,
// ANTHROPIC_API_KEY, GEMINI_API_KEY, AZURE_OPENAI_API_KEY, or <PROVIDER>_API_KEY
// for the rest. [WithAPIKey] overrides that. Providers that need no key, such as
// a local Ollama, work with neither set.
//
// Nothing here reads pi-go's config file. [FromConfig] does, explicitly, for
// embedders that want the same model a `pi` session would pick.
package pimodels

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/adk/v2/model"

	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/provider"
)

// ProviderNamer reports which provider family serves a model.
//
// Every [Model] returned by [New] and [FromConfig] implements it. It is declared
// as a named interface for documentation, but consumers should type-assert the
// *shape* rather than import this package for the type:
//
//	if p, ok := m.(interface{ Provider() string }); ok {
//	    span.SetAttributes(attribute.String("gen_ai.provider.name", p.Provider()))
//	}
//
// That keeps the dependency direction right. A consumer needing the provider —
// for a span attribute, or to enable a provider-specific tool such as Gemini's
// server-side grounding — gets it without importing pimodels, and without
// keeping a second copy of the model-name-to-provider table. Those copies drift
// the moment a vendor adds a naming convention.
//
// A model built any other way will not satisfy the assertion, so always handle
// the not-ok branch.
type ProviderNamer interface {
	// Provider returns the provider family: "openai", "anthropic", "gemini",
	// "mistral", "xai", "ollama", "azure" or "opencode".
	Provider() string
}

// providerModel attaches the resolved provider to a model.
//
// model.LLM is embedded rather than delegated field by field, so any method ADK
// adds later is forwarded automatically and this wrapper cannot silently drift
// from the thing it wraps. internal/guardrail wraps models the same way, and
// nothing in pi-go type-asserts a concrete model type, so wrapping is safe.
type providerModel struct {
	model.LLM
	provider string
}

func (m providerModel) Provider() string { return m.provider }

// Model is the interface an ADK agent consumes. Aliased so an embedder using
// only this package does not have to import ADK to name the return type; it is
// the same type, so passing it to any ADK agent works unchanged.
type Model = model.LLM

// Info describes how a model name was resolved.
type Info struct {
	// Provider is the backend that will serve the model: "openai",
	// "anthropic", "gemini", "mistral", "xai", "ollama", "azure", "opencode".
	Provider string
	// Model is the model name as the provider expects it, with any routing
	// prefix such as "ollama/" removed.
	Model string
	// BaseURL is the endpoint finally selected. Recorded because the same
	// model name served by Ollama, by a gateway and by a vendor API behaves
	// differently, and a trace without it cannot be reproduced.
	BaseURL string
	// Ollama reports whether the model is served by an Ollama daemon.
	Ollama bool
	// Custom reports whether an explicit OpenAI-compatible endpoint was used.
	Custom bool
}

// options carries everything New needs beyond the model name. Callers set it
// through Option values; the zero value is a working default.
type options struct {
	apiKey        string
	baseURL       string
	thinkingLevel string
	llm           provider.LLMOptions
}

// Option configures [New]. Options are applied in order, so a later one wins.
type Option func(*options)

// WithAPIKey sets the credential explicitly, overriding the environment.
func WithAPIKey(key string) Option {
	return func(o *options) { o.apiKey = key }
}

// WithBaseURL points the client at a different endpoint — a gateway, a proxy,
// or a self-hosted OpenAI-compatible server.
func WithBaseURL(url string) Option {
	return func(o *options) { o.baseURL = url }
}

// WithThinkingLevel sets the reasoning effort for models that support it
// ("none", "low", "medium", "high"). Ignored by models that do not.
func WithThinkingLevel(level string) Option {
	return func(o *options) { o.thinkingLevel = level }
}

// WithHeaders adds headers to every request, for gateways that need routing or
// tenancy metadata.
func WithHeaders(h map[string]string) Option {
	return func(o *options) { o.llm.ExtraHeaders = h }
}

// WithConnectTimeout bounds connection establishment. It deliberately does not
// bound the request: streaming completions run long, and a request timeout that
// is short enough to be useful for connect is short enough to kill them.
func WithConnectTimeout(d time.Duration) Option {
	return func(o *options) { o.llm.ConnectTimeout = d }
}

// WithCACert trusts a PEM bundle in addition to the system roots — the answer
// for a TLS-intercepting corporate proxy, which otherwise forces callers to
// disable verification for every endpoint.
func WithCACert(path string) Option {
	return func(o *options) { o.llm.CACertPath = path }
}

// WithInsecureTLS disables certificate verification.
//
// Prefer [WithCACert]: this turns verification off for every endpoint the
// client reaches, not just the one that needed it.
func WithInsecureTLS() Option {
	return func(o *options) { o.llm.InsecureSkipTLS = true }
}

// WithPromptCachingDisabled turns off the cache_control breakpoints the
// Anthropic provider sets by default.
//
// Caching is on by default because it only ever lowers the bill: a request
// whose prefix is below the minimum cacheable length is simply not cached, with
// no error and no extra cost. Disable it only when a provider-side behavior
// makes it undesirable.
func WithPromptCachingDisabled() Option {
	return func(o *options) { o.llm.DisablePromptCaching = true }
}

// WithAdvisor enables an advisor model for providers that support it, bounded
// by maxUses per request (0 = unbounded).
func WithAdvisor(advisorModel string, maxUses int, caching bool) Option {
	return func(o *options) {
		o.llm.AdvisorModel = advisorModel
		o.llm.AdvisorMaxUses = maxUses
		o.llm.AdvisorCaching = caching
	}
}

// New resolves modelName and returns a client for it.
//
// The provider is inferred from the name: "gpt-*" is OpenAI, "claude-*" is
// Anthropic, "gemini-*" is Gemini, "grok-*" is xAI, an "ollama/" prefix or a
// ":cloud" suffix routes to Ollama, and so on. Pass [WithBaseURL] to override
// the endpoint for any of them.
func New(ctx context.Context, modelName string, opts ...Option) (Model, error) {
	if modelName == "" {
		return nil, fmt.Errorf("pimodels: model name must not be empty")
	}

	var o options
	for _, opt := range opts {
		opt(&o)
	}

	info, err := resolveInfo(modelName, o.baseURL)
	if err != nil {
		return nil, err
	}

	apiKey := o.apiKey
	if apiKey == "" {
		apiKey = provider.APIKeyFromEnv(info.Provider)
	}

	llmOpts := o.llm
	m, err := provider.NewLLM(ctx, info, apiKey, o.baseURL, o.thinkingLevel, &llmOpts)
	if err != nil {
		return nil, fmt.Errorf("pimodels: building %s model %q: %w", info.Provider, info.Model, err)
	}
	// Carry the resolved provider on the model itself, so a consumer never has
	// to keep its own model-name prefix table to find out.
	return providerModel{LLM: m, provider: info.Provider}, nil
}

// FromConfig builds the model a `pi` session would use for the given role,
// reading ~/.pi-go/config.json and any project config.
//
// An empty role means "default". This is the one function here that touches
// pi-go's own configuration; [New] is self-contained and takes an explicit name.
func FromConfig(ctx context.Context, role string, opts ...Option) (Model, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("pimodels: loading config: %w", err)
	}
	if role == "" {
		role = "default"
	}
	modelName, _, advisorModel, advisorMaxUses, advisorCaching, err := cfg.ResolveRole(role)
	if err != nil {
		return nil, fmt.Errorf("pimodels: resolving role %q: %w", role, err)
	}

	// Config-declared advisor settings come first so an explicit option can
	// still override them.
	merged := append([]Option{WithAdvisor(advisorModel, advisorMaxUses, advisorCaching)}, opts...)
	if cfg.ThinkingLevel != "" {
		merged = append([]Option{WithThinkingLevel(cfg.ThinkingLevel)}, merged...)
	}
	return New(ctx, modelName, merged...)
}

// Resolve reports how a model name would be routed, without building a client
// or needing a credential. Useful for validating configuration at startup.
func Resolve(modelName string, opts ...Option) (Info, error) {
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	info, err := resolveInfo(modelName, o.baseURL)
	if err != nil {
		return Info{}, err
	}
	return Info{
		Provider: info.Provider,
		Model:    info.Model,
		BaseURL:  info.BaseURL,
		Ollama:   info.Ollama,
		Custom:   info.Custom,
	}, nil
}

// ContextWindow returns a model's context window in tokens, or 0 when the model
// is unknown. Prefer [ContextWindowFor] when the provider is known: an Azure
// deployment can share a name with an OpenAI model without sharing its window.
func ContextWindow(modelName string) int64 {
	return provider.ContextWindowSize(modelName)
}

// ContextWindowFor returns a model's context window for a specific provider.
func ContextWindowFor(providerName, modelName string) int64 {
	return provider.ContextWindowSizeFor(providerName, modelName)
}

// APIKeyEnvVar names the environment variable [New] reads a provider's key
// from, so an embedder can report a missing credential precisely.
func APIKeyEnvVar(providerName string) string {
	return provider.APIKeyEnvVar(providerName)
}

// resolveInfo picks the resolution path: an explicit base URL means the caller
// is naming an endpoint, and the model name alone must not override it.
func resolveInfo(modelName, baseURL string) (provider.Info, error) {
	if baseURL != "" {
		info, err := provider.ResolveWithBaseURL(modelName, baseURL)
		if err != nil {
			return provider.Info{}, fmt.Errorf("pimodels: resolving %q at %s: %w", modelName, baseURL, err)
		}
		// ResolveWithBaseURL marks the model Custom but leaves Info.BaseURL
		// empty, even though the field documents itself as the endpoint finally
		// selected. Inside pi-go only the TUI fills it in, by hand. Fill it here
		// so Resolve's answer is complete for an embedder, who has no second
		// place to look.
		if info.BaseURL == "" {
			info.BaseURL = baseURL
		}
		return info, nil
	}
	info, err := provider.Resolve(modelName)
	if err != nil {
		return provider.Info{}, fmt.Errorf("pimodels: resolving %q: %w", modelName, err)
	}
	return info, nil
}
