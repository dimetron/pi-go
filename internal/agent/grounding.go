// Package agent sets up the ADK Go agent loop with tools, system prompt,
// and runner for the pi-go coding agent.
package agent

import (
	"fmt"
	"net/url"
	"os"
	"strings"

	adkagent "google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/geminitool"
	"google.golang.org/genai"
)

// GroundingToolName is the display name for Gemini's server-side search.
//
// It is deliberately not a registered tool: grounding runs inside the Gemini
// API and never emits a FunctionCall, so without a synthetic name there is
// nothing for the UI to label. Every surface (TUI, print mode, JSON mode)
// reports the search under this name so a grounded answer shows its provenance
// instead of silently arriving with fresh facts.
const GroundingToolName = "google_search"

// OpenAIWebSearchToolName is the display name for OpenAI's built-in web_search.
//
// It is a separate constant from GroundingToolName because the two searches are
// not the same search: they hit different indexes and return different sources.
// Sharing one name would tell a reader that OpenAI's index is Google's, which is
// exactly the kind of provenance the synthetic name exists to report. Like
// Gemini's, it is not a registered tool — nothing in pi's loop executes it.
const OpenAIWebSearchToolName = "openai_web_search"

// GroundingToolNameFor returns the display name for a server-side search run by
// the given provider. Gemini grounds with Google Search and OpenAI with its own
// web_search; anything else returning GroundingMetadata is treated as the
// Gemini shape, which is where the convention started.
//
// Azure is included with OpenAI because it serves the same model and the same
// Responses API.
func GroundingToolNameFor(providerName string) string {
	switch providerName {
	case "openai", "azure":
		return OpenAIWebSearchToolName
	default:
		return GroundingToolName
	}
}

// GroundingQueryKey identifies a search by its query set. GroundingMetadata is
// repeated on every streamed chunk of the response it grounds, so callers key
// on this to report each search exactly once per turn.
func GroundingQueryKey(queries []string) string {
	return strings.Join(queries, "\x00")
}

// GroundingQuery renders the searched queries as a single human-readable string.
func GroundingQuery(gm *genai.GroundingMetadata) string {
	if gm == nil {
		return ""
	}
	return strings.Join(gm.WebSearchQueries, ", ")
}

// groundingSourceLabel names a chunk for display. For Google Search grounding
// the Gemini API sets Title to the source's domain (e.g. "wikipedia.org") and
// leaves Domain empty, so Title is the useful label; fall back to Domain, then
// to the URI's host.
func groundingSourceLabel(w *genai.GroundingChunkWeb) string {
	if w.Title != "" {
		return w.Title
	}
	if w.Domain != "" {
		return w.Domain
	}
	if u, err := url.Parse(w.URI); err == nil && u.Host != "" {
		return u.Host
	}
	return w.URI
}

// GroundingSummary lists every source a search returned, one per line.
//
// Sources are deduplicated by label. Gemini commonly cites several distinct
// pages from the same site, and since it sets Title to the site's domain, those
// arrive as identical labels — printing them verbatim gave
// "kyivindependent.com kyivindependent.com kyivindependent.com". A repeated
// label is instead shown once with a count: "kyivindependent.com (3)".
//
// Only the source *label* is shown, not its URI. Google Search grounding does
// not return the real source URL: it returns an opaque ~200-character
// vertexaisearch.cloud.google.com/grounding-api-redirect/... link. Printing that
// tells the reader nothing, soft-wraps across several rows, and shreds the chat
// panel's layout. The full URIs are still written to the trace log via
// GroundingSources, so nothing is lost.
func GroundingSummary(gm *genai.GroundingMetadata) string {
	if gm == nil {
		return ""
	}

	// First-seen order, so the model's own ranking is preserved.
	var labels []string
	counts := map[string]int{}
	for _, chunk := range gm.GroundingChunks {
		if chunk == nil || chunk.Web == nil {
			continue
		}
		label := groundingSourceLabel(chunk.Web)
		if label == "" {
			continue
		}
		if counts[label] == 0 {
			labels = append(labels, label)
		}
		counts[label]++
	}

	if len(labels) == 0 {
		if total := len(gm.GroundingChunks); total > 0 {
			return fmt.Sprintf("%d source(s)", total)
		}
		return "no sources returned"
	}

	var b strings.Builder
	for _, label := range labels {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString(label)
		if n := counts[label]; n > 1 {
			fmt.Fprintf(&b, " (%d)", n)
		}
	}
	return b.String()
}

// GroundingSources returns every source as "label — uri", uncapped. This is the
// full-fidelity form for the trace log and session record, where width does not
// matter and the redirect URIs are worth keeping.
func GroundingSources(gm *genai.GroundingMetadata) string {
	if gm == nil {
		return ""
	}
	var b strings.Builder
	for _, chunk := range gm.GroundingChunks {
		if chunk == nil || chunk.Web == nil {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		label := groundingSourceLabel(chunk.Web)
		if chunk.Web.URI != "" {
			fmt.Fprintf(&b, "%s — %s", label, chunk.Web.URI)
		} else {
			b.WriteString(label)
		}
	}
	return b.String()
}

// Compile-time interface assertion to ensure geminitool.GoogleSearch satisfies tool.Tool.
var _ tool.Tool = geminitool.GoogleSearch{}

// groundingTool is geminitool.GoogleSearch plus the one request setting Gemini
// requires to run a built-in tool alongside function declarations.
//
// Without it the API rejects the whole request:
//
//	400 INVALID_ARGUMENT — Please enable
//	tool_config.include_server_side_tool_invocations to use Built-in tools
//	with Function calling.
//
// This is the trap that makes grounding look incompatible with pi's tools. The
// tempting "fix" is to drop every tool whenever grounding is on — which is what
// the first cut of this feature did, leaving Gemini with no bash/read/write/grep
// at all, so the model hallucinated tool names and every call came back with
// "tool not found. Available tools: " (empty). Setting this flag is what
// actually lets the two coexist.
//
// Not supported on Vertex AI, per the genai docs; pi targets the Gemini API.
type groundingTool struct {
	geminitool.GoogleSearch
}

var _ tool.Tool = groundingTool{}

// ProcessRequest adds the built-in search, then permits it to run next to the
// function declarations the rest of pi's tools contribute.
func (g groundingTool) ProcessRequest(ctx adkagent.Context, req *model.LLMRequest) error {
	if err := g.GoogleSearch.ProcessRequest(ctx, req); err != nil {
		return err
	}
	if req.Config == nil {
		req.Config = &genai.GenerateContentConfig{}
	}
	if req.Config.ToolConfig == nil {
		req.Config.ToolConfig = &genai.ToolConfig{}
	}
	req.Config.ToolConfig.IncludeServerSideToolInvocations = genai.Ptr(true)
	return nil
}

// groundingEnvVar is the env var that disables Gemini search grounding
// process-wide. Empty / unset / any value other than a recognized truthy
// token means "grounding is enabled (subject to provider check)".
const groundingEnvVar = "PI_NO_GROUNDING"

// groundingDisabled reports whether the PI_NO_GROUNDING env var is set to a
// truthy value. Recognized truthy tokens (case-insensitive): "1", "true",
// "yes", "on". Anything else (including unset and empty string) is treated
// as "not disabled".
func groundingDisabled() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(groundingEnvVar)))
	switch v {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// GatewayRouteSpeaksGemini reports whether an agentgateway model name resolves
// to a route whose upstream speaks Gemini's native generateContent protocol.
//
// The gateway fronts every provider behind one endpoint, so the provider name
// alone cannot say whether Gemini's server-side search is available — the ROUTE
// decides. The convention mirrored here is the one the gateway's own config.yaml
// follows: gemini/* and gemini-* are Gemini routes.
//
// This is deliberately a second implementation of
// internal/provider.GatewayRoutesToGemini rather than a call into it, and the
// reason is a hard constraint rather than a preference: piagent imports this
// package, and TestPiagentStaysIsolated forbids internal/provider anywhere in
// piagent's transitive graph, so importing it here would break the embedder.
// The two lists are pinned together by
// TestGatewayRouteSpeaksGeminiAgreesWithProvider, which fails the moment either
// one changes alone — that test is what makes the duplication safe.
func GatewayRouteSpeaksGemini(modelName string) bool {
	lower := strings.ToLower(strings.TrimSpace(modelName))
	for _, prefix := range []string{"gemini/", "gemini-"} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

// GroundsViaGemini reports whether a resolved provider/model pair is served by
// Gemini, and so may carry Gemini's server-side search.
//
// Two shapes reach Gemini and they are not the same provider:
//
//   - "gemini" — the AI Studio API directly, where the provider name is the
//     whole answer.
//   - "agentgateway" — the local gateway, but only for a route whose upstream
//     speaks native Gemini. The gateway also fronts OpenAI, Anthropic and
//     Ollama, and a grounding tool registered for one of those is dropped in
//     conversion rather than rejected: {"type":"google_search"} simply
//     disappears and the model then answers from its own priors. So the model
//     name has to agree, and this is the same question
//     internal/provider asks before choosing the native client.
//
// The model name is therefore required for the gateway case and optional for
// the direct one, so a caller that knows only the provider still works.
func GroundsViaGemini(providerName, modelName string) bool {
	switch providerName {
	case "gemini":
		return true
	case "agentgateway":
		return GatewayRouteSpeaksGemini(modelName)
	default:
		return false
	}
}

// GeminiGroundingTool returns the geminitool.GoogleSearch tool if and only if
// the active provider is served by Gemini (see GroundsViaGemini) and grounding
// has not been disabled by the PI_NO_GROUNDING env var. It returns (nil, false)
// otherwise. Callers append the returned tool to the agent's Tools slice only
// when the second return value is true.
//
// modelName may be empty; a caller that has already established the provider is
// "gemini" does not need it. It is required for the agentgateway case, where
// the provider name alone does not say which upstream the route reaches.
func GeminiGroundingTool(providerName, modelName string) (tool.Tool, bool) {
	if !GroundsViaGemini(providerName, modelName) {
		return nil, false
	}
	if groundingDisabled() {
		return nil, false
	}
	return groundingTool{}, true
}
