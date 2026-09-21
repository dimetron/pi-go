package provider

// Tests for OpenAI's built-in web_search on the Responses API. Three things
// have to hold and are pinned separately: the tool and its include list reach
// the wire only when opted in, the ChatGPT codex backend never receives them,
// and a server-side search is reported through GroundingMetadata rather than as
// a FunctionCall part — ADK executes FunctionCall parts, and a name with no
// registered tool lands a "tool not found" result in the conversation.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/openai/openai-go/v3/responses"
	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

// openaiCaptureServer records one request body and answers with a plain
// message, so a test can assert on what was sent.
func openaiCaptureServer(t *testing.T, body *map[string]any) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := readAll(r)
		_ = json.Unmarshal(raw, body)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":     "resp_ws",
			"object": "response",
			"status": "completed",
			"model":  "gpt-6-astra",
			"output": []map[string]any{{
				"type":   "message",
				"id":     "msg_ws",
				"role":   "assistant",
				"status": "completed",
				"content": []map[string]any{{
					"type":        "output_text",
					"text":        "A positive story.",
					"annotations": []any{},
				}},
			}},
			"usage": map[string]any{"input_tokens": 10, "output_tokens": 5, "total_tokens": 15},
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func readAll(r *http.Request) ([]byte, error) {
	defer func() { _ = r.Body.Close() }()
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 4096)
	for {
		n, err := r.Body.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if err != nil {
			return buf, nil
		}
	}
}

func openaiToolTypes(body map[string]any) []string {
	raw, _ := body["tools"].([]any)
	var types []string
	for _, item := range raw {
		tool, _ := item.(map[string]any)
		if typ, _ := tool["type"].(string); typ != "" {
			types = append(types, typ)
		}
	}
	return types
}

func containsType(types []string, want string) bool {
	for _, typ := range types {
		if typ == want {
			return true
		}
	}
	return false
}

// A caller who opts in gets the built-in tool and the include list that makes
// its sources readable, alongside the client-side function declarations.
func TestOpenAIWebSearchOptInSendsToolAndInclude(t *testing.T) {
	var body map[string]any
	srv := openaiCaptureServer(t, &body)

	m, err := NewOpenAI(context.Background(), "gpt-6-astra", "sk-test", srv.URL,
		&LLMOptions{EnableOpenAIWebSearch: true})
	if err != nil {
		t.Fatalf("NewOpenAI: %v", err)
	}

	req := &model.LLMRequest{
		Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "good news?"}}}},
		Config: &genai.GenerateContentConfig{
			Tools: []*genai.Tool{{
				FunctionDeclarations: []*genai.FunctionDeclaration{{Name: "read_file"}},
			}},
		},
	}
	for _, err := range m.GenerateContent(context.Background(), req, false) {
		if err != nil {
			t.Fatalf("GenerateContent: %v", err)
		}
	}

	types := openaiToolTypes(body)
	if !containsType(types, "web_search") {
		t.Errorf("tools = %v, want the built-in web_search", types)
	}
	// The function declaration must survive next to the built-in tool; the
	// Gemini grounding feature once dropped every tool when grounding was on,
	// which left the model hallucinating tool names.
	if !containsType(types, "function") {
		t.Errorf("tools = %v, want the client-side function declaration", types)
	}

	include, _ := body["include"].([]any)
	if !containsType(anyToStrings(include), "web_search_call.action.sources") {
		t.Errorf("include = %v, want web_search_call.action.sources", body["include"])
	}
}

// Default off: an ordinary OpenAI model must not receive a tool it may reject.
// gpt-4.1-nano and minimal-reasoning gpt-5 refuse it outright, which is why
// this is opt-in rather than default-on like xAI's tools.
func TestOpenAIWebSearchOffByDefault(t *testing.T) {
	var body map[string]any
	srv := openaiCaptureServer(t, &body)

	m, err := NewOpenAI(context.Background(), "gpt-6-astra", "sk-test", srv.URL, nil)
	if err != nil {
		t.Fatalf("NewOpenAI: %v", err)
	}

	req := &model.LLMRequest{
		Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "hi"}}}},
	}
	for _, err := range m.GenerateContent(context.Background(), req, false) {
		if err != nil {
			t.Fatalf("GenerateContent: %v", err)
		}
	}

	if types := openaiToolTypes(body); containsType(types, "web_search") {
		t.Errorf("tools = %v, want no built-in web_search without an opt-in", types)
	}
	if body["include"] != nil {
		t.Errorf("include = %v, want it omitted without an opt-in", body["include"])
	}
}

// The kill switch beats an explicit opt-in, so an operator can take the tool
// away from a caller that asked for it.
func TestOpenAIWebSearchKillSwitchBeatsOptIn(t *testing.T) {
	t.Setenv(openaiWebSearchDisableEnvVar, "1")

	var body map[string]any
	srv := openaiCaptureServer(t, &body)

	m, err := NewOpenAI(context.Background(), "gpt-6-astra", "sk-test", srv.URL,
		&LLMOptions{EnableOpenAIWebSearch: true})
	if err != nil {
		t.Fatalf("NewOpenAI: %v", err)
	}

	req := &model.LLMRequest{
		Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "hi"}}}},
	}
	for _, err := range m.GenerateContent(context.Background(), req, false) {
		if err != nil {
			t.Fatalf("GenerateContent: %v", err)
		}
	}

	if types := openaiToolTypes(body); containsType(types, "web_search") {
		t.Errorf("tools = %v, want web_search suppressed by %s", types, openaiWebSearchDisableEnvVar)
	}
	if body["include"] != nil {
		t.Errorf("include = %v, want it omitted when %s is set", body["include"], openaiWebSearchDisableEnvVar)
	}
}

// The env var turns the feature on process-wide, without a code change.
func TestOpenAIWebSearchEnvVarEnables(t *testing.T) {
	t.Setenv(openaiWebSearchEnvVar, "1")

	var body map[string]any
	srv := openaiCaptureServer(t, &body)

	m, err := NewOpenAI(context.Background(), "gpt-6-astra", "sk-test", srv.URL, nil)
	if err != nil {
		t.Fatalf("NewOpenAI: %v", err)
	}

	req := &model.LLMRequest{
		Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "hi"}}}},
	}
	for _, err := range m.GenerateContent(context.Background(), req, false) {
		if err != nil {
			t.Fatalf("GenerateContent: %v", err)
		}
	}

	if types := openaiToolTypes(body); !containsType(types, "web_search") {
		t.Errorf("tools = %v, want web_search enabled by %s", types, openaiWebSearchEnvVar)
	}
}

// The ChatGPT codex backend exposes a fixed tool set and is not the platform
// API, so neither the built-in tool nor the include list may be sent to it.
// The xAI equivalent had to be removed outright when its endpoint rejected the
// include field (commit 233134f); this keeps the same boundary up front.
func TestOpenAIWebSearchNotSentToCodexBackend(t *testing.T) {
	m := &openaiModel{
		modelName:       "gpt-5-codex",
		codexBackend:    true,
		enableWebSearch: true,
	}
	params, _, err := m.buildResponsesParams(&model.LLMRequest{
		Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "hi"}}}},
	}, "gpt-5-codex")
	if err != nil {
		t.Fatalf("buildResponsesParams: %v", err)
	}

	for _, tool := range params.Tools {
		if tool.OfWebSearch != nil {
			t.Error("codex backend received the built-in web_search tool")
		}
	}
	// The codex path sets include for encrypted reasoning; web search sources
	// must not be appended to it.
	for _, inc := range params.Include {
		if inc == responses.ResponseIncludableWebSearchCallActionSources {
			t.Error("codex backend received web_search_call.action.sources in include")
		}
	}
}

func anyToStrings(vals []any) []string {
	out := make([]string, 0, len(vals))
	for _, v := range vals {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// decodeOutputItems builds output items the way the API delivers them, by
// unmarshaling real JSON into the SDK union. Hand-constructing the union would
// encode the shape this test assumes rather than the shape production sees.
func decodeOutputItems(t *testing.T, raw string) []responses.ResponseOutputItemUnion {
	t.Helper()
	var items []responses.ResponseOutputItemUnion
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		t.Fatalf("decoding output items: %v", err)
	}
	return items
}

// A server-side search must reach the display layer as GroundingMetadata, with
// its query and every source URL preserved. Dropping it is the interesting
// failure: the answer arrives with fresh facts and no provenance at all.
func TestOpenAIServerSideSearchBecomesGroundingMetadata(t *testing.T) {
	items := decodeOutputItems(t, `[
		{"type":"message","id":"msg_1","role":"assistant","status":"completed",
		 "content":[{"type":"output_text","text":"A positive story.","annotations":[]}]},
		{"type":"web_search_call","id":"ws_1","status":"completed",
		 "action":{"type":"search","query":"positive news","queries":["positive news today"],
		           "sources":[{"type":"url","url":"https://example.test/good-news"},
		                      {"type":"url","url":"https://other.test/story"}]}}
	]`)

	gm := openaiWebSearchGrounding(items)
	if gm == nil {
		t.Fatal("openaiWebSearchGrounding returned nil; the search would be invisible")
	}
	if len(gm.WebSearchQueries) != 1 || gm.WebSearchQueries[0] != "positive news today" {
		t.Errorf("WebSearchQueries = %v, want the searched query", gm.WebSearchQueries)
	}
	if len(gm.GroundingChunks) != 2 {
		t.Fatalf("GroundingChunks = %d, want 2 sources", len(gm.GroundingChunks))
	}
	if got := gm.GroundingChunks[0].Web.URI; got != "https://example.test/good-news" {
		t.Errorf("first source URI = %q, want the consulted URL", got)
	}

	// The search must NOT become a FunctionCall part: ADK executes those, and
	// an unregistered name produces a "tool not found" result fed back to the
	// model. Assert on the real parser, not just the grounding helper.
	parts, _ := parseResponsesOutput(items)
	for _, p := range parts {
		if p.FunctionCall != nil {
			t.Errorf("parseResponsesOutput emitted a FunctionCall %q; ADK would execute it and report tool-not-found", p.FunctionCall.Name)
		}
	}
}

// A response with no search must leave GroundingMetadata unset, so an ordinary
// turn does not carry an empty metadata object.
func TestOpenAIGroundingAbsentWithoutSearch(t *testing.T) {
	items := decodeOutputItems(t, `[
		{"type":"message","id":"msg_1","role":"assistant","status":"completed",
		 "content":[{"type":"output_text","text":"hi","annotations":[]}]}
	]`)
	if gm := openaiWebSearchGrounding(items); gm != nil {
		t.Errorf("openaiWebSearchGrounding = %+v, want nil when nothing searched", gm)
	}
}

// open_page and find are the other web_search actions; neither carries a query
// or a source list, so they must not invent an empty metadata object.
func TestOpenAIGroundingIgnoresNonSearchActions(t *testing.T) {
	for _, raw := range []string{
		`[{"type":"web_search_call","id":"ws_2","status":"completed","action":{"type":"open_page","url":"https://example.test"}}]`,
		`[{"type":"web_search_call","id":"ws_3","status":"completed","action":{"type":"find","pattern":"x"}}]`,
	} {
		if gm := openaiWebSearchGrounding(decodeOutputItems(t, raw)); gm != nil {
			t.Errorf("%s: got %+v, want nil (no query, no sources)", raw, gm)
		}
	}
}

// The deprecated singular `query` field must NOT be read alongside `queries`.
// The live API sends both, with `query` duplicating queries[0]; appending it
// would report the first query twice.
func TestOpenAIGroundingIgnoresDeprecatedSingularQuery(t *testing.T) {
	items := decodeOutputItems(t, `[
		{"type":"web_search_call","id":"ws_4","status":"completed",
		 "action":{"type":"search","queries":["modern query"],"query":"modern query"}}
	]`)
	gm := openaiWebSearchGrounding(items)
	if gm == nil {
		t.Fatal("no grounding produced")
	}
	if len(gm.WebSearchQueries) != 1 {
		t.Fatalf("WebSearchQueries = %v, want exactly one query; the deprecated "+
			"singular field must not be appended as a second", gm.WebSearchQueries)
	}
	if gm.WebSearchQueries[0] != "modern query" {
		t.Errorf("query = %q, want %q", gm.WebSearchQueries[0], "modern query")
	}
}

// The end-to-end half: a real request through the real model must come back
// with GroundingMetadata populated. Without this, deleting the grounding call
// from runResponsesNonStreaming leaves every test above green — they exercise
// the helper, not the path that has to call it.
func TestOpenAIWebSearchGroundingReachesLLMResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"resp_e2e","object":"response","created_at":1.0,"status":"completed",
			"model":"gpt-6-astra","error":null,"incomplete_details":null,
			"output":[
				{"type":"web_search_call","id":"ws_e2e","status":"completed",
				 "action":{"type":"search","query":"positive news",
				           "queries":["positive news today"],
				           "sources":[{"type":"url","url":"https://example.test/good-news"}]}},
				{"type":"message","id":"msg_e2e","role":"assistant","status":"completed",
				 "content":[{"type":"output_text","text":"A positive story.","annotations":[]}]}
			],
			"usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15}
		}`))
	}))
	t.Cleanup(srv.Close)

	m, err := NewOpenAI(context.Background(), "gpt-6-astra", "sk-test", srv.URL,
		&LLMOptions{EnableOpenAIWebSearch: true})
	if err != nil {
		t.Fatalf("NewOpenAI: %v", err)
	}

	req := &model.LLMRequest{
		Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "good news?"}}}},
	}
	var final *model.LLMResponse
	for resp, err := range m.GenerateContent(context.Background(), req, false) {
		if err != nil {
			t.Fatalf("GenerateContent: %v", err)
		}
		if resp != nil && !resp.Partial {
			final = resp
		}
	}
	if final == nil {
		t.Fatal("no final response yielded")
	}
	gm := final.GroundingMetadata
	if gm == nil {
		t.Fatal("final response has no GroundingMetadata; the search's sources never reach the display layer")
	}
	if len(gm.WebSearchQueries) != 1 || gm.WebSearchQueries[0] != "positive news today" {
		t.Errorf("WebSearchQueries = %v, want the searched query", gm.WebSearchQueries)
	}
	if len(gm.GroundingChunks) != 1 {
		t.Fatalf("GroundingChunks = %d, want 1 source", len(gm.GroundingChunks))
	}
	if got := gm.GroundingChunks[0].Web.URI; got != "https://example.test/good-news" {
		t.Errorf("source URI = %q, want the consulted URL", got)
	}
}

// The chat shows the source *label*, so it must be the host and not the raw
// URL: a live search returned 39 sources, and full URLs with tracking query
// strings soft-wrap across the panel. The full URL must survive in the URI,
// which is what the trace log records.
func TestOpenAIGroundingLabelsSourcesByHost(t *testing.T) {
	items := decodeOutputItems(t, `[
		{"type":"web_search_call","id":"ws_l","status":"completed",
		 "action":{"type":"search","queries":["q"],
		           "sources":[{"type":"url","url":"https://www.nasa.gov/2026-news-releases/?utm_source=openai"}]}}
	]`)
	gm := openaiWebSearchGrounding(items)
	if gm == nil || len(gm.GroundingChunks) != 1 {
		t.Fatalf("gm = %+v, want one chunk", gm)
	}
	web := gm.GroundingChunks[0].Web
	if web.Title != "www.nasa.gov" {
		t.Errorf("Title = %q, want the host as the display label", web.Title)
	}
	if web.URI != "https://www.nasa.gov/2026-news-releases/?utm_source=openai" {
		t.Errorf("URI = %q, want the full URL kept for the trace log", web.URI)
	}
}
