// Package provider — provider-neutral extension point for prompt caching.
//
// cache_apply.go provides the shared helper that any provider's wire builder
// may call to check whether the user has opted out of prompt caching. Today
// only the Anthropic provider honors it; the openai, gemini, mistral, xai,
// ollama, and azure builders ignore the option entirely.
package provider

// shouldDisablePromptCache returns true when the user has opted out of
// prompt caching via LLMOptions. Providers call this at the end of their
// wire-build helper. Today only Anthropic checks it; the helper is a no-op
// for other providers.
func shouldDisablePromptCache(opts *LLMOptions) bool {
	return opts != nil && opts.DisablePromptCaching
}
