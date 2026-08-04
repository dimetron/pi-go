// Package provider — provider-neutral extension point for prompt caching.
//
// cache_apply.go declares the shared shape that any provider's wire builder
// may use if it wants opt-in/opt-out plumbing for prompt caching. Today
// only the Anthropic provider honors it; the openai, gemini, mistral,
// ollama, and azure builders ignore the interface entirely.
//
// The interface is intentionally sealed (an unexported method): only code in
// this package can implement it, which prevents external packages from
// accidentally claiming to be a cache-aware provider.
package provider

// cacheApplier is the sealed marker for providers that participate in the
// shared prompt-cache opt-out plumbing. The implementation detail — how to
// stamp the cache_control marker on the wire — is provider-specific and
// lives in the provider's own _caching.go file.
//
//nolint:unused // sealed interface reserved for cross-package provider implementations; none are wired up yet
type cacheApplier interface {
	cacheApplier()
}

// shouldDisablePromptCache returns true when the user has opted out of
// prompt caching via LLMOptions. Providers that implement cacheApplier
// call this at the end of their wire-build helper. Today only Anthropic
// implements the interface; the helper is a no-op for other providers.
func shouldDisablePromptCache(opts *LLMOptions) bool {
	return opts != nil && opts.DisablePromptCaching
}
