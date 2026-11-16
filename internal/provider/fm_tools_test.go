package provider

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

// fmCapturedFilename holds the 40 tool declarations pi-go actually sends,
// captured by running the CLI with --trace-http and reading the request body out
// of the session log.
//
// A captured fixture is the point of this file. The defect was not that *some*
// schema failed: four of pi's real tools and all five tavily tools were rejected
// while hand-written simple schemas passed. A fixture written to illustrate the
// bug would have encoded the same assumption the code did, and gone green either
// way.
const fmCapturedFilename = "testdata/fm-tools.json"

// fmCapturedTool is one entry of the captured request.
type fmCapturedTool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Parameters  any    `json:"parameters"`
}

// loadCapturedTools reads the captured declarations and builds genai tools, the
// way ADK hands them to the provider.
func loadCapturedTools(t *testing.T) []*genai.Tool {
	t.Helper()
	data, err := os.ReadFile(fmCapturedFilename)
	if err != nil {
		t.Fatalf("reading %s: %v", fmCapturedFilename, err)
	}
	var payload struct {
		Tools []fmCapturedTool `json:"tools"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("parsing %s: %v", fmCapturedFilename, err)
	}
	if len(payload.Tools) == 0 {
		t.Fatalf("%s carries no tools", fmCapturedFilename)
	}

	tools := make([]*genai.Tool, 0, len(payload.Tools))
	for _, ct := range payload.Tools {
		tools = append(tools, &genai.Tool{
			FunctionDeclarations: []*genai.FunctionDeclaration{{
				Name:                 ct.Name,
				Description:          ct.Description,
				ParametersJsonSchema: ct.Parameters,
			}},
		})
	}
	return tools
}

// capturedToolNames lists the names present in the fixture.
func capturedToolNames(t *testing.T) []string {
	t.Helper()
	var names []string
	for _, tl := range loadCapturedTools(t) {
		for _, fd := range tl.FunctionDeclarations {
			names = append(names, fd.Name)
		}
	}
	return names
}

// TestFMSanitizeSchemaDropsRejectedKeywords is the regression guard for the 400
// that made every tool-using fm turn fail:
//
//	400 Invalid request: The data couldn't be read because it isn't in the
//	correct format.
//
// fm names no field, so the only evidence is that a request carrying these
// keywords was rejected and the same request without them was accepted. Each
// entry below was bisected against a running server.
func TestFMSanitizeSchemaDropsRejectedKeywords(t *testing.T) {
	// Keywords verified to produce a 400 (or, for pattern, a 500) from fm.
	rejected := []string{"$ref", "$defs", "$schema", "oneOf", "allOf", "not", "pattern"}

	for _, keyword := range rejected {
		t.Run(keyword, func(t *testing.T) {
			schema := map[string]any{
				"type":  "object",
				"oneOf": []any{map[string]any{"type": "string"}},
				// A plain string value: several of these keywords expect a
				// reference or a scalar rather than a nested schema, and the
				// check is that the key is gone, not what it held.
				keyword: "#/$defs/X",
			}
			got, ok := fmSanitizeSchema(schema).(map[string]any)
			if !ok {
				t.Fatal("sanitized schema is not a map")
			}
			if _, present := got[keyword]; present {
				t.Errorf("%s survived sanitizing; fm rejects it", keyword)
			}
			if _, present := got["oneOf"]; present {
				t.Error("oneOf survived sanitizing; fm rejects it")
			}
		})
	}
}

// TestFMSanitizeSchemaCollapsesUnions covers the union forms: ["null", T] is
// rejected as a type, and anyOf is rewritten rather than dropped — dropping it
// would leave the property untyped, which fm rejects too.
func TestFMSanitizeSchemaCollapsesUnions(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"flag":  map[string]any{"type": []any{"null", "boolean"}},
			"list":  map[string]any{"type": []any{"null", "array"}, "items": map[string]any{"type": "integer"}},
			"opt":   map[string]any{"anyOf": []any{map[string]any{"type": "null"}, map[string]any{"type": "string"}}},
			"plain": map[string]any{"type": "string"},
		},
	}

	got, ok := fmSanitizeSchema(schema).(map[string]any)
	if !ok {
		t.Fatal("sanitized schema is not a map")
	}
	props, ok := got["properties"].(map[string]any)
	if !ok {
		t.Fatal("properties is not a map")
	}

	want := map[string]string{"flag": "boolean", "list": "array", "opt": "string", "plain": "string"}
	for name, wantType := range want {
		prop, ok := props[name].(map[string]any)
		if !ok {
			t.Fatalf("%s: property is not a map", name)
		}
		if prop["type"] != wantType {
			t.Errorf("%s type = %v, want %q (a union must collapse to its non-null branch)", name, prop["type"], wantType)
		}
	}
	// The array must keep saying what it holds.
	list := props["list"].(map[string]any)
	items, ok := list["items"].(map[string]any)
	if !ok || items["type"] != "integer" {
		t.Errorf("list items = %v, want the integer item type preserved", list["items"])
	}
}

// TestFMSanitizeSchemaFixesArraysAndObjects covers the two shapes fm reports as
// "because it is missing" — an array with no items, and an object with no
// properties.
func TestFMSanitizeSchemaFixesArraysAndObjects(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"bare":   map[string]any{"type": "array"},
			"empty":  map[string]any{"type": "array", "items": map[string]any{}},
			"object": map[string]any{"type": "object"},
		},
	}

	got, _ := fmSanitizeSchema(schema).(map[string]any)
	props, _ := got["properties"].(map[string]any)

	for _, name := range []string{"bare", "empty"} {
		prop := props[name].(map[string]any)
		items, ok := prop["items"].(map[string]any)
		if !ok || items["type"] == nil {
			t.Errorf("%s items = %v, want a typed item schema", name, prop["items"])
		}
	}
	obj := props["object"].(map[string]any)
	if _, ok := obj["properties"].(map[string]any); !ok {
		t.Errorf("object properties = %v, want an empty map", obj["properties"])
	}
}

// TestFMSanitizeSchemaPreservesValidSchema guards the other direction: a schema
// fm already accepted must come back unchanged, so the rewrite cannot quietly
// loosen constraints that were working.
func TestFMSanitizeSchemaPreservesValidSchema(t *testing.T) {
	raw := `{"type":"object","properties":{
		"file_path":{"type":"string"},
		"limit":{"type":"integer"}},
		"required":["file_path"],"additionalProperties":true}`

	var schema any
	if err := json.Unmarshal([]byte(raw), &schema); err != nil {
		t.Fatal(err)
	}
	got := fmSanitizeSchema(schema)

	before, _ := json.Marshal(schema)
	after, _ := json.Marshal(got)
	if string(before) != string(after) {
		t.Errorf("valid schema was rewritten:\n before: %s\n after:  %s", before, after)
	}
}

// TestFMSanitizeSchemaHandlesLists pins the recursion over a JSON array.
//
// A []any reaching fmSanitizeSchema's default branch would marshal, decode to
// another []any, and re-enter itself until the stack died — which is exactly what
// happened the first time this was written.
func TestFMSanitizeSchemaHandlesLists(t *testing.T) {
	schema := map[string]any{
		"type":     "object",
		"required": []any{"a", "b"},
		"properties": map[string]any{
			"a": map[string]any{"type": "string"},
			"b": map[string]any{"type": "array", "items": map[string]any{"type": []any{"null", "integer"}}},
		},
	}

	got, ok := fmSanitizeSchema(schema).(map[string]any)
	if !ok {
		t.Fatal("sanitized schema is not a map")
	}
	if required, ok := got["required"].([]any); !ok || len(required) != 2 {
		t.Errorf("required = %v, want both entries kept", got["required"])
	}
	props := got["properties"].(map[string]any)
	items := props["b"].(map[string]any)["items"].(map[string]any)
	if items["type"] != "integer" {
		t.Errorf("nested items type = %v, want the union collapsed to integer", items["type"])
	}
}

// TestFMPrepareRequestSanitizesEveryTool runs pi's real 40 tools through the
// function the model actually calls and asserts that not one rejected keyword
// survives, and that no tool is dropped.
//
// Before the fix this failed on git-overview, subagent, mem-get, agent_run and
// every tavily tool — each of which made the whole request a 400, not just
// itself.
func TestFMPrepareRequestSanitizesEveryTool(t *testing.T) {
	before := capturedToolNames(t)
	req := &model.LLMRequest{
		Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "list files"}}}},
		Config:   &genai.GenerateContentConfig{Tools: loadCapturedTools(t)},
	}
	fmPrepareRequest(req)

	body, err := json.Marshal(req.Config.Tools)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{`"$ref"`, `"$defs"`, `"oneOf"`, `"allOf"`, `"pattern"`, `"anyOf"`, `"default":null`} {
		if strings.Contains(string(body), bad) {
			t.Errorf("%s survived fmPrepareRequest; fm rejects the whole request for it", bad)
		}
	}
	// A union type serializes as an array of type names; none may remain.
	if strings.Contains(string(body), `"type":["`) {
		t.Error(`a union "type" survived fmPrepareRequest; fm rejects it`)
	}

	// No tool may be dropped: a tool the model is never offered is a capability
	// the user loses silently.
	var after []string
	for _, tl := range req.Config.Tools {
		for _, fd := range tl.FunctionDeclarations {
			after = append(after, fd.Name)
		}
	}
	if len(after) != len(before) {
		t.Errorf("tool count = %d, want all %d preserved", len(after), len(before))
	}
	slices.Sort(after)
	slices.Sort(before)
	if !slices.Equal(after, before) {
		t.Errorf("tool names changed:\n got: %v\nwant: %v", after, before)
	}
}

// TestFMSanitizedToolSetStillTooLarge is the honest-wall check, run against a
// real server when one is listening.
//
// The measurement that makes this the right behavior, and that a schema rewrite
// cannot change: after sanitizing, the 40 real declarations are 7,443 tokens of
// the 8,192-token window. They fit. What does not fit is pi-go's default system
// prompt — 13,376 tokens on its own — so the prompt has to come first.
//
// This test therefore sends the tools plus a prompt that leaves no room, and
// requires the guard to name the window rather than let the server answer with a
// bare 400 or 500.
func TestFMSanitizedToolSetStillTooLarge(t *testing.T) {
	if !fmReachable("") {
		t.Skip("no `fm serve` on the default endpoint")
	}
	llm, err := NewFoundationModels(t.Context(), fmModelSystem, "", "", nil)
	if err != nil {
		t.Fatalf("NewFoundationModels: %v", err)
	}

	req := &model.LLMRequest{
		Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: strings.Repeat("a", 20000)}}}},
		Config:   &genai.GenerateContentConfig{Tools: loadCapturedTools(t)},
	}

	var gotErr error
	for _, err := range llm.GenerateContent(t.Context(), req, true) {
		if err != nil {
			gotErr = err
			break
		}
	}
	if gotErr == nil {
		t.Fatal("the tools plus a 20k-character prompt were accepted; the window guard did not fire")
	}
	if !strings.Contains(gotErr.Error(), "context window") {
		t.Errorf("error = %q, want the window guard's message, not a server rejection", gotErr)
	}
}

// TestFMMeasureRequest pins the size guard on the real requests that
// matter: pi's default tool-using turn cannot fit, and a small prompt can.
//
// The numbers are the measured ones — the default system prompt is 13,376
// tokens on its own against an 8,192 window, and the 40 real tools are 8,570 —
// so a guard that disagrees with them is failing at something other than what
// this test is about.
func TestFMMeasureRequest(t *testing.T) {
	for _, tc := range []struct {
		name      string
		prompt    string
		withTools bool
		wantOver  bool
	}{
		{
			name:     "a short prompt with no tools fits",
			prompt:   "hi",
			wantOver: false,
		},
		{
			// 8192 tokens at ~4 chars each is 32,768; 20,000 characters is
			// comfortably inside it.
			name:     "a 20k-character prompt fits",
			prompt:   strings.Repeat("a", 20000),
			wantOver: false,
		},
		{
			// The real default system prompt is 56,571 characters, which the
			// server reports as 13,376 tokens.
			name:     "pi's default system prompt does not fit",
			prompt:   strings.Repeat("a", 56571),
			wantOver: true,
		},
		{
			// Measured: the sanitized 40 declarations are 33,811 characters of
			// wire JSON, which `fm count-tokens` reports as 8,510 — already past
			// the 8,192-token window before any prompt is added. This is the fact
			// that makes the coding agent's fm turn impossible rather than merely
			// large, and the guard has to see it.
			name:      "the real tool set alone does not fit",
			prompt:    "list files",
			withTools: true,
			wantOver:  true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := &model.LLMRequest{
				Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: tc.prompt}}}},
				Config:   &genai.GenerateContentConfig{},
			}
			if tc.withTools {
				req.Config.Tools = loadCapturedTools(t)
			}

			budget := fmMeasureRequest(req)
			over := budget.total > fmContextWindow
			if over != tc.wantOver {
				t.Errorf("exceeds = %v (estimated %d tokens), want %v", over, budget.total, tc.wantOver)
			}
			// The parts must add up to the total, or advice() points at the
			// wrong thing.
			if sum := budget.system + budget.contents + budget.tools; sum != budget.total {
				t.Errorf("parts sum to %d, total is %d", sum, budget.total)
			}
		})
	}
}

// TestFMGenerateContentRefusesOversizedRequest checks the error a user sees. It
// must name the limit and what fits, because the whole defect was an opaque
// "400 Bad Request" that said neither.
func TestFMGenerateContentRefusesOversizedRequest(t *testing.T) {
	var called bool
	m := fmAlwaysStreamingModel{LLM: &recordingLLM{gotStream: &called}}
	req := &model.LLMRequest{
		Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: strings.Repeat("a", 60000)}}}},
	}

	var gotErr error
	for _, err := range m.GenerateContent(t.Context(), req, false) {
		gotErr = err
	}
	if gotErr == nil {
		t.Fatal("oversized request was not refused")
	}
	for _, want := range []string{"8192-token", "context window", "fm serve"} {
		if !strings.Contains(gotErr.Error(), want) {
			t.Errorf("error = %q, want it to contain %q", gotErr.Error(), want)
		}
	}
	if called {
		t.Error("the oversized request still reached the inner model")
	}
}

// TestFMBudgetAdviceNamesTheLargestPart guards the message, not just the refusal.
//
// The first version of this guard reported only a total and blamed the system
// prompt. Against a request whose tool declarations were the bulk of it — which
// is exactly pi-go's coding agent — that sends the user to shrink the wrong
// thing. The advice must name whichever part dominates.
func TestFMBudgetAdviceNamesTheLargestPart(t *testing.T) {
	for _, tc := range []struct {
		name   string
		budget fmBudget
		want   string
	}{
		{"tools dominate", fmBudget{system: 100, contents: 100, tools: 900, total: 1100}, "tool declarations"},
		{"system prompt dominates", fmBudget{system: 900, contents: 100, tools: 100, total: 1100}, "system prompt"},
		{"transcript dominates", fmBudget{system: 100, contents: 900, tools: 100, total: 1100}, "transcript"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.budget.advice(); !strings.Contains(got, tc.want) {
				t.Errorf("advice = %q, want it to name %q", got, tc.want)
			}
		})
	}
}

// TestFMRefusalReportsTheBreakdown checks that the user-facing error carries the
// per-part numbers, so the advice can be verified against them.
func TestFMRefusalReportsTheBreakdown(t *testing.T) {
	m := fmAlwaysStreamingModel{LLM: &recordingLLM{}}
	req := &model.LLMRequest{
		Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: strings.Repeat("a", 40000)}}}},
	}
	var gotErr error
	for _, err := range m.GenerateContent(t.Context(), req, false) {
		gotErr = err
	}
	if gotErr == nil {
		t.Fatal("oversized request was not refused")
	}
	for _, want := range []string{"system prompt", "transcript", "tool declarations"} {
		if !strings.Contains(gotErr.Error(), want) {
			t.Errorf("error = %q, want the breakdown to mention %q", gotErr.Error(), want)
		}
	}
}

// TestFMGuardAgreesWithTheServer is the calibration guard, and it is the one
// that decides whether the estimate is trustworthy.
//
// The estimate exists to keep a request off the wire only when the server would
// reject it. Being wrong in either direction costs something real: refusing a
// request the server accepts makes fm unusable for work it can do, and passing
// one it rejects spends a round trip to arrive at an error the user cannot act
// on. Both happened while this was being calibrated — a divisor of 3 refused an
// accepted 40-tool request, and a divisor of 5 passed a rejected one.
//
// Skipped without a running server, since the comparison is with the server.
func TestFMGuardAgreesWithTheServer(t *testing.T) {
	if !fmReachable("") {
		t.Skip("no `fm serve` on the default endpoint")
	}
	// The *unwrapped* client, so the server's verdict is observed directly.
	// Asking the wrapped model would be circular: when the guard fires the
	// request never leaves the process, so "guard refused" and "server refused"
	// could never disagree — which is exactly how a broken estimate passed this
	// test's first version at divisor 3.
	inner, err := NewOpenAI(t.Context(), fmModelSystem, "", fmDefaultBaseURL, nil)
	if err != nil {
		t.Fatalf("NewOpenAI: %v", err)
	}

	for _, tc := range []struct {
		name   string
		system string
		user   string
		tools  bool
	}{
		// Measured 7,749 server tokens: accepted, so the guard must let it pass.
		// This is the case the coding agent's shape reduces to, and a guard that
		// refuses it makes fm useless for anything but bare print mode.
		{
			name:   "40 tools with a tiny prompt is accepted, so the guard must pass it",
			system: "Be terse.",
			user:   "say hi in three words",
			tools:  true,
		},
		// Measured 8,249 server tokens: rejected, so the guard must refuse it
		// before the round trip.
		{
			name:   "40 tools plus filler is rejected, so the guard must refuse it",
			system: "Be terse.",
			user:   "say hi in three words" + strings.Repeat("a", 2000),
			tools:  true,
		},
		// Comfortably inside the window with no tools at all.
		{
			name: "a long prompt with no tools is accepted",
			user: strings.Repeat("The quick brown fox jumps. ", 700),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := &model.LLMRequest{Config: &genai.GenerateContentConfig{}}
			if tc.system != "" {
				req.Config.SystemInstruction = &genai.Content{Parts: []*genai.Part{{Text: tc.system}}}
			}
			if tc.tools {
				req.Config.Tools = loadCapturedTools(t)
			}
			req.Contents = []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: tc.user}}}}

			// The real flow sanitizes before measuring; measuring the raw schemas
			// would judge a request that is never sent.
			fmPrepareRequest(req)
			guardRefuses := fmMeasureRequest(req).total > fmContextWindow

			// A provider reports a stream failure as a content-less response
			// carrying ErrorCode, NOT as a Go error — see oaiRunStreaming. Testing
			// only `err != nil` makes every rejection look like a success, which
			// is how this test first "passed" against a wrong estimate.
			var serverRejected bool
			for resp, err := range inner.GenerateContent(t.Context(), req, true) {
				if err != nil || (resp != nil && resp.ErrorCode != "") {
					serverRejected = true
					break
				}
			}

			if guardRefuses != serverRejected {
				t.Errorf("guard refuses = %v but the server rejects = %v; the estimate disagrees with the server it is protecting",
					guardRefuses, serverRejected)
			}
		})
	}
}
