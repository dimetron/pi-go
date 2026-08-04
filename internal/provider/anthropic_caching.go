// Package provider — Anthropic prompt-cache breakpoint helpers.
//
// caching.go stamps Anthropic prompt-cache markers on a request before it
// goes on the wire. The cache is opt-in and prefix-based: a cache_control
// marker on a block makes the API cache everything up to and including that
// block, and a later request with a byte-identical prefix reads it back at
// ~10% of the input price. Setting a marker is always safe: prefixes shorter
// than the model's minimum cacheable length (1024–4096 tokens) are silently
// not cached, with no error and no extra cost.
//
// Anthropic caches in the order tools → system → messages and allows at most
// 4 breakpoints per request. We set 3:
//
//  1. The last tool definition. Tool schemas are static per agent, so this
//     prefix survives every request of a session.
//
//  2. The last system block. Static too, and separate from (1) so editing
//     the prompt (e.g. a hot reload) does not invalidate the tools cache.
//
//  3. The last cacheable block of the last message. Turn N's history becomes
//     turn N+1's cached prefix; this is where the savings come from in
//     agentic loops, which re-send the whole history on every tool
//     round-trip. Thinking and redacted-thinking blocks cannot carry a
//     marker, so this one walks backwards past them to the nearest eligible
//     block.
//
// Call applyAnthropicCacheControl as the LAST step of wire construction so
// every section is final.
package provider

import "github.com/anthropics/anthropic-sdk-go"

// applyAnthropicCacheControl stamps the three ephemeral cache_control
// breakpoints on a standard MessageNewParams, in place. See file header
// for the breakpoint strategy.
func applyAnthropicCacheControl(params *anthropic.MessageNewParams) {
	marker := anthropic.NewCacheControlEphemeralParam()

	// (1) Tools: mark the last definition so the whole tool array is
	// covered by one breakpoint.
	if n := len(params.Tools); n > 0 {
		if cc := params.Tools[n-1].GetCacheControl(); cc != nil {
			*cc = marker
		}
	}

	// (2) System prompt: mark the last block.
	if n := len(params.System); n > 0 {
		params.System[n-1].CacheControl = marker
	}

	// (3) Conversation: walk messages from the tail looking for the
	// last block that supports cache_control. GetCacheControl returns
	// nil for thinking/redacted-thinking blocks, which skips them.
	for i := len(params.Messages) - 1; i >= 0; i-- {
		if markLastCacheableAnthropicBlock(params.Messages[i].Content, marker) {
			return
		}
	}
}

// applyAnthropicCacheControlBeta mirrors applyAnthropicCacheControl for the
// beta API path (advisor tool support). Same three breakpoints, different
// concrete SDK types.
func applyAnthropicCacheControlBeta(params *anthropic.BetaMessageNewParams) {
	marker := anthropic.NewBetaCacheControlEphemeralParam()

	if n := len(params.Tools); n > 0 {
		if cc := params.Tools[n-1].GetCacheControl(); cc != nil {
			*cc = marker
		}
	}

	if n := len(params.System); n > 0 {
		params.System[n-1].CacheControl = marker
	}

	for i := len(params.Messages) - 1; i >= 0; i-- {
		if markLastCacheableAnthropicBetaBlock(params.Messages[i].Content, marker) {
			return
		}
	}
}

// markLastCacheableAnthropicBlock sets the marker on the last block of the
// slice that can carry cache_control. Reports whether a block was marked, so
// the caller knows to stop scanning earlier messages.
func markLastCacheableAnthropicBlock(blocks []anthropic.ContentBlockParamUnion, marker anthropic.CacheControlEphemeralParam) bool {
	for i := len(blocks) - 1; i >= 0; i-- {
		if cc := blocks[i].GetCacheControl(); cc != nil {
			*cc = marker
			return true
		}
	}
	return false
}

// markLastCacheableAnthropicBetaBlock is the beta-path equivalent of
// markLastCacheableAnthropicBlock.
func markLastCacheableAnthropicBetaBlock(blocks []anthropic.BetaContentBlockParamUnion, marker anthropic.BetaCacheControlEphemeralParam) bool {
	for i := len(blocks) - 1; i >= 0; i-- {
		if cc := blocks[i].GetCacheControl(); cc != nil {
			*cc = marker
			return true
		}
	}
	return false
}
