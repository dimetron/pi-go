package extension

import "testing"

// TestProviderForwardsInlineImages pins which providers can carry an image
// part. Only the ADK-native Gemini path hands genai.Content to the SDK
// unchanged; every other adapter rebuilds the request from text and function
// calls alone, so an InlineData part handed to it is dropped silently.
func TestProviderForwardsInlineImages(t *testing.T) {
	if !providerForwardsInlineImages("gemini") {
		t.Error("gemini must forward inline images: NewGemini returns ADK's native model")
	}
	for _, p := range []string{
		"ollama", "openai", "azure", "anthropic", "mistral",
		"openrouter", "xai", "opencode", "", "unknown",
	} {
		if providerForwardsInlineImages(p) {
			t.Errorf("providerForwardsInlineImages(%q) = true; no adapter for it converts InlineData", p)
		}
	}
}
