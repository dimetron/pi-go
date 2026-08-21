package provider

import (
	"strings"

	"google.golang.org/genai"
)

// This file holds the provider-neutral half of the Contents-to-messages
// conversion. Every provider converter — Anthropic, Ollama, OpenAI Chat
// Completions and OpenAI Responses — walks the same genai.Content slice and
// needs the same four answers out of it: the system prompt, an index of
// function responses, the text/function-call split of one content, and whether
// a role is the model's own turn.
//
// Nothing below knows about any provider. The part that does — how a turn
// becomes wire-format messages — stays in each provider's own file, because
// the providers genuinely diverge there (placeholder text for a missing tool
// result, whether a blank call ID is dropped, one message or two) and those
// differences are worth seeing at the call site rather than behind a shared
// abstraction.

// genaiSystemInstruction joins the text parts of a request's system
// instruction into one prompt. Empty and nil parts are skipped and the result
// is trimmed, so a config carrying no usable text yields "".
func genaiSystemInstruction(config *genai.GenerateContentConfig) string {
	if config == nil || config.SystemInstruction == nil {
		return ""
	}
	var b strings.Builder
	for _, p := range config.SystemInstruction.Parts {
		if p != nil && p.Text != "" {
			b.WriteString(p.Text)
			b.WriteByte('\n')
		}
	}
	return strings.TrimSpace(b.String())
}

// genaiFunctionResponses indexes every function response in contents by its
// call ID, so a converter walking the conversation in order can pair a
// function call with a result that appears in a later content. When two
// responses share an ID the last one wins.
func genaiFunctionResponses(contents []*genai.Content) map[string]*genai.FunctionResponse {
	out := make(map[string]*genai.FunctionResponse)
	for _, c := range contents {
		if c == nil || c.Parts == nil {
			continue
		}
		for _, p := range c.Parts {
			if p != nil && p.FunctionResponse != nil {
				out[p.FunctionResponse.ID] = p.FunctionResponse
			}
		}
	}
	return out
}

// genaiSplitParts separates one content's parts into its text runs and its
// function calls. A part carrying text counts as text even if it also carries
// a function call — the precedence every provider converter has always used.
func genaiSplitParts(content *genai.Content) (textParts []string, functionCalls []*genai.FunctionCall) {
	for _, part := range content.Parts {
		if part == nil {
			continue
		}
		if part.Text != "" {
			textParts = append(textParts, part.Text)
		} else if part.FunctionCall != nil {
			functionCalls = append(functionCalls, part.FunctionCall)
		}
	}
	return textParts, functionCalls
}

// genaiRoleIsAssistant reports whether a content role denotes the model's own
// turn. genai names it "model"; histories that round-tripped through an
// OpenAI-shaped store carry "assistant" instead. The role is compared as
// given, so callers trim it first.
func genaiRoleIsAssistant(role string) bool {
	return role == "model" || role == "assistant"
}
