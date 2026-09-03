package provider

import (
	"encoding/json"

	"google.golang.org/genai"
)

// marshalFunctionCallArgs serializes a replayed function call's arguments for
// the OpenAI wire shape, rendering an absent argument map as "{}" rather than
// "null".
//
// A tool call whose arguments failed to parse — a stream cut off mid-JSON at
// the output-token cap is the usual way — reaches history with a nil Args map.
// Marshaling that directly produces the literal `null`, and a gateway
// translating the turn onward for Anthropic then sends `"input": null`, which
// the API rejects:
//
//	400 messages.24.content.0.tool_use.input: Input should be an object
//
// The damage is not the one bad call: that turn is *in* the conversation, so
// every later request replays it and fails the same way. One truncated tool
// call bricks the session. Anthropic's own path has always substituted an
// empty map here (see antToolUseBlocks); this gives the OpenAI-compatible
// paths — which is how pi-go reaches agentgateway — the same floor.
func marshalFunctionCallArgs(args map[string]any) []byte {
	if args == nil {
		return []byte("{}")
	}
	encoded, err := json.Marshal(args)
	if err != nil || len(encoded) == 0 {
		return []byte("{}")
	}
	return encoded
}

// nonNilFunctionCallArgs is marshalFunctionCallArgs' counterpart for parts
// being built from a provider response: it keeps a nil argument map from
// entering the conversation at all.
func nonNilFunctionCallArgs(args map[string]any) map[string]any {
	if args == nil {
		return map[string]any{}
	}
	return args
}

// newFunctionCallPart builds a function call part whose Args map is never nil.
func newFunctionCallPart(name string, args map[string]any) *genai.Part {
	return genai.NewPartFromFunctionCall(name, nonNilFunctionCallArgs(args))
}

// oaiMaxOutputTokens picks the output cap for one request: what the caller
// asked for, else the model's configured default.
//
// genai carries MaxOutputTokens on the request config and the
// OpenAI-compatible paths used to drop it on the floor, so a caller that set
// it got the server's default anyway. Honor it first — a caller who names a
// number means it — and fall back to the model's own default (see
// defaultOaiMaxOutputTokens).
func oaiMaxOutputTokens(config *genai.GenerateContentConfig, fallback int64) int64 {
	if config != nil && config.MaxOutputTokens > 0 {
		return int64(config.MaxOutputTokens)
	}
	return fallback
}
