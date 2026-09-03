package provider

import (
	"encoding/base64"
	"encoding/json"

	"google.golang.org/genai"
)

// Gemini 3 attaches an opaque "thought signature" to every function call it
// emits and refuses to accept that call back without it:
//
//	400 Function call is missing a thought_signature in functionCall parts.
//	This is required for tools to work correctly...
//
// The first turn of a tool conversation therefore succeeds and the second
// fails, which reads like an intermittent fault rather than a missing field.
// Gemini 2.x emits no signature and does not require one, so a client that
// drops the field works everywhere until it meets a Gemini 3 model.
//
// Over the OpenAI Chat Completions wire shape — the one pi-go uses for
// agentgateway, and the one Google's own OpenAI-compatible endpoint speaks —
// the signature rides in a non-standard sibling of the tool call:
//
//	{"id": "...", "type": "function", "function": {...},
//	 "extra_content": {"google": {"thought_signature": "<base64>"}}}
//
// openai-go models only the standard fields, so the signature survives just
// long enough to be read out of the raw JSON. These helpers move it between
// that wire shape and [genai.Part.ThoughtSignature], which is where the genai
// types this package converts through already expect it to live.
const (
	oaiExtraContentField     = "extra_content"
	oaiExtraContentVendor    = "google"
	oaiThoughtSignatureField = "thought_signature"
)

// oaiExtraContent is the decoding shape for a tool call's extra_content.
type oaiExtraContent struct {
	ExtraContent struct {
		Google struct {
			ThoughtSignature string `json:"thought_signature"`
		} `json:"google"`
	} `json:"extra_content"`
}

// oaiThoughtSignature reads the Gemini thought signature out of one tool
// call's raw JSON, returning nil when there is none — the ordinary case for
// every provider that is not Gemini 3. Malformed JSON and undecodable base64
// are treated the same way: a signature that cannot be read is one that cannot
// be replayed, and failing the whole turn over it would be worse than the 400
// this exists to avoid.
func oaiThoughtSignature(rawJSON string) []byte {
	if rawJSON == "" {
		return nil
	}
	var wire oaiExtraContent
	if err := json.Unmarshal([]byte(rawJSON), &wire); err != nil {
		return nil
	}
	encoded := wire.ExtraContent.Google.ThoughtSignature
	if encoded == "" {
		return nil
	}
	sig, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil
	}
	return sig
}

// genaiThoughtSignatures indexes the thought signatures carried by one
// content's function-call parts, keyed by call ID. Splitting the parts loses
// the [genai.Part] wrapper the signature lives on, so it is collected before
// the split rather than threaded through it — which keeps genaiSplitParts
// shared with the Anthropic and Ollama paths, neither of which has any use for
// a signature.
func genaiThoughtSignatures(parts []*genai.Part) map[string][]byte {
	var signatures map[string][]byte
	for _, part := range parts {
		if part == nil || part.FunctionCall == nil || len(part.ThoughtSignature) == 0 {
			continue
		}
		if part.FunctionCall.ID == "" {
			continue
		}
		if signatures == nil {
			signatures = make(map[string][]byte, len(parts))
		}
		signatures[part.FunctionCall.ID] = part.ThoughtSignature
	}
	return signatures
}

// oaiThoughtSignatureExtraContent renders a signature back into the
// extra_content object Google expects on a replayed tool call. It returns nil
// for an empty signature so callers can set the field unconditionally without
// sending `extra_content: null` to providers that would reject it.
func oaiThoughtSignatureExtraContent(sig []byte) map[string]any {
	if len(sig) == 0 {
		return nil
	}
	return map[string]any{
		oaiExtraContentField: map[string]any{
			oaiExtraContentVendor: map[string]any{
				oaiThoughtSignatureField: base64.StdEncoding.EncodeToString(sig),
			},
		},
	}
}
