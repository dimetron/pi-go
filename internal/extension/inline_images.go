package extension

// providerForwardsInlineImages reports whether the model adapter behind a
// provider puts genai InlineData parts on the wire.
//
// Only the Gemini path does: provider.NewGemini returns ADK's native gemini
// model, which hands genai.Content to the SDK unchanged. Every other provider
// goes through an adapter that rebuilds the request from genai parts and reads
// only text and function calls — see genaiSplitParts in
// internal/provider/openai_completions.go, and the equivalent conversions in
// anthropic.go and mistral.go. An InlineData part handed to those is dropped
// without a word.
//
// It lives here rather than beside the adapters because piagent imports this
// package and is walled off from internal/provider by TestPiagentStaysIsolated.
//
// This is not a statement about the model: a vision-capable model reached
// through the OpenAI-compatible adapter still cannot be sent an image this
// way. When an adapter learns to forward images, add its provider here.
func providerForwardsInlineImages(providerName string) bool {
	return providerName == "gemini"
}
