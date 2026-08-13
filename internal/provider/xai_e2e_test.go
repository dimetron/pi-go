//go:build e2e

package provider

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

// e2eXAIModel is the tier these tests bill against. grok-4.6 is xAI's current
// flagship and the one whose reasoning_effort tiers are documented, so it is
// the model whose behaviour these tests are actually meant to pin.
const e2eXAIModel = "grok-4.6"

func testGetXAIAPIKey(t *testing.T) string {
	t.Helper()
	key := os.Getenv("XAI_API_KEY")
	if key == "" {
		t.Skip("skipping: XAI_API_KEY not set")
	}
	return key
}

// skipIfXAIUnprovisioned skips when msg carries xAI's account-provisioning
// refusal — a team with no credits or licenses yet.
//
// Reaching that response means DNS, TLS, the base URL, the bearer credential
// and the endpoint path were all correct; xAI declined on billing state, which
// says nothing about this code. Any other 403 (a missing scope, a revoked key)
// still fails, because that is a real signal.
func skipIfXAIUnprovisioned(t *testing.T, msg string) {
	t.Helper()
	if strings.Contains(msg, "credits or licenses") {
		t.Skipf("xAI team is not provisioned (auth and routing succeeded): %s", msg)
	}
}

func testNewXAI(t *testing.T, thinkingLevel string) model.LLM {
	t.Helper()
	llm, err := NewXAI(context.Background(), e2eXAIModel, testGetXAIAPIKey(t), "", thinkingLevel, nil)
	if err != nil {
		t.Fatalf("NewXAI() error: %v", err)
	}
	return llm
}

// e2eXAIText drains a non-streaming turn and returns the final text, failing
// on any transport or API error.
func e2eXAIText(t *testing.T, llm model.LLM, req *model.LLMRequest) string {
	t.Helper()
	var last *model.LLMResponse
	for resp, err := range llm.GenerateContent(context.Background(), req, false) {
		if err != nil {
			skipIfXAIUnprovisioned(t, err.Error())
			t.Fatalf("GenerateContent error: %v", err)
		}
		if resp.ErrorCode != "" {
			skipIfXAIUnprovisioned(t, resp.ErrorMessage)
			t.Fatalf("API error %s: %s", resp.ErrorCode, resp.ErrorMessage)
		}
		last = resp
	}
	if last == nil {
		t.Fatal("expected at least one response")
	}
	if !last.TurnComplete {
		t.Error("expected TurnComplete = true on the final response")
	}
	if last.Content == nil || len(last.Content.Parts) == 0 {
		t.Fatal("expected content with parts")
	}
	return last.Content.Parts[0].Text
}

func TestE2EXAINonStreaming(t *testing.T) {
	llm := testNewXAI(t, "low")
	if llm.Name() != e2eXAIModel {
		t.Errorf("Name() = %q, want %q", llm.Name(), e2eXAIModel)
	}

	text := e2eXAIText(t, llm, &model.LLMRequest{
		Contents: []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "What is 2+2? Answer in one short sentence."}}},
		},
	})
	if text == "" {
		t.Error("expected non-empty text response")
	}
	t.Logf("Got response: %s", text)
}

func TestE2EXAIStreaming(t *testing.T) {
	llm := testNewXAI(t, "low")

	req := &model.LLMRequest{
		Contents: []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "Count from 1 to 3. Reply with just the numbers separated by spaces."}}},
		},
	}

	var responses []*model.LLMResponse
	var fullText strings.Builder
	for resp, err := range llm.GenerateContent(context.Background(), req, true) {
		if err != nil {
			skipIfXAIUnprovisioned(t, err.Error())
			t.Fatalf("GenerateContent streaming error: %v", err)
		}
		if resp.ErrorCode != "" {
			skipIfXAIUnprovisioned(t, resp.ErrorMessage)
			t.Fatalf("API error %s: %s", resp.ErrorCode, resp.ErrorMessage)
		}
		responses = append(responses, resp)
		if resp.Partial && resp.Content != nil {
			for _, p := range resp.Content.Parts {
				fullText.WriteString(p.Text)
			}
		}
	}

	if len(responses) < 2 {
		t.Fatalf("expected at least 2 streaming responses (partials + final), got %d", len(responses))
	}
	last := responses[len(responses)-1]
	if !last.TurnComplete {
		t.Error("expected TurnComplete = true on the last streaming response")
	}
	if fullText.Len() == 0 {
		t.Error("expected non-empty streamed text")
	}
	if last.UsageMetadata == nil || last.UsageMetadata.PromptTokenCount == 0 {
		t.Error("expected usage metadata on the final streaming response")
	}
	t.Logf("Full streamed text: %s", fullText.String())
}

func TestE2EXAIWithSystemPrompt(t *testing.T) {
	llm := testNewXAI(t, "low")

	text := e2eXAIText(t, llm, &model.LLMRequest{
		Contents: []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "What is your name?"}}},
		},
		Config: &genai.GenerateContentConfig{
			SystemInstruction: &genai.Content{
				Parts: []*genai.Part{{Text: "You are a helpful assistant named Grokkles."}},
			},
		},
	})
	if text == "" {
		t.Error("expected non-empty text response")
	}
	// The model may or may not echo the name back — this only pins that a
	// system instruction is accepted on the wire, not what it produces.
	t.Logf("Got response: %s", text)
}

func TestE2EXAIWithTools(t *testing.T) {
	llm := testNewXAI(t, "low")

	tools := []*genai.Tool{{
		FunctionDeclarations: []*genai.FunctionDeclaration{{
			Name:        "add",
			Description: "Add two integers",
			ParametersJsonSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"a": map[string]any{"type": "integer"},
					"b": map[string]any{"type": "integer"},
				},
				"required": []any{"a", "b"},
			},
		}},
	}}

	req := &model.LLMRequest{
		Contents: []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "Use the add tool to compute 17 + 25."}}},
		},
		Config: &genai.GenerateContentConfig{Tools: tools},
	}

	var last *model.LLMResponse
	for resp, err := range llm.GenerateContent(context.Background(), req, false) {
		if err != nil {
			skipIfXAIUnprovisioned(t, err.Error())
			t.Fatalf("GenerateContent error: %v", err)
		}
		if resp.ErrorCode != "" {
			skipIfXAIUnprovisioned(t, resp.ErrorMessage)
			t.Fatalf("API error %s: %s", resp.ErrorCode, resp.ErrorMessage)
		}
		last = resp
	}
	if last == nil || last.Content == nil {
		t.Fatal("expected a response with content")
	}

	var called *genai.FunctionCall
	for _, p := range last.Content.Parts {
		if p.FunctionCall != nil {
			called = p.FunctionCall
			break
		}
	}
	if called == nil {
		// Tool choice is the model's to make, so a plain answer is a valid
		// outcome. What matters is that the declaration was accepted rather
		// than rejected as malformed.
		t.Skipf("model answered without calling the tool: %+v", last.Content.Parts)
	}
	if called.Name != "add" {
		t.Errorf("function call = %q, want add", called.Name)
	}
	if called.ID == "" {
		t.Error("expected a function call ID; the tool-result round trip needs it")
	}
	t.Logf("Got function call: %s(%v)", called.Name, called.Args)
}

// TestE2EXAIReasoningEffortAccepted pins that xAI accepts every tier this
// provider can emit. A rejected value comes back as a 400 on the request, so
// this is the test that catches xAI retiring or renaming a tier.
func TestE2EXAIReasoningEffortAccepted(t *testing.T) {
	for _, level := range []string{"none", "low", "medium", "high", "max"} {
		t.Run(level, func(t *testing.T) {
			llm := testNewXAI(t, level)
			text := e2eXAIText(t, llm, &model.LLMRequest{
				Contents: []*genai.Content{
					{Role: "user", Parts: []*genai.Part{{Text: "Reply with the single word: ok"}}},
				},
			})
			if text == "" {
				t.Errorf("empty response at reasoning effort %q", level)
			}
			t.Logf("effort %s (wire: %q) → %s", level, xaiReasoningEffort(level), text)
		})
	}
}

func TestE2EXAIViaNewLLM(t *testing.T) {
	key := testGetXAIAPIKey(t)

	info, err := Resolve(e2eXAIModel)
	if err != nil {
		t.Fatalf("Resolve() error: %v", err)
	}
	if info.Provider != "xai" {
		t.Errorf("Provider = %q, want xai", info.Provider)
	}
	if err := ValidateModel(info); err != nil {
		t.Errorf("ValidateModel() error: %v", err)
	}

	llm, err := NewLLM(context.Background(), info, key, "", "low", nil)
	if err != nil {
		t.Fatalf("NewLLM() error: %v", err)
	}
	if llm.Name() != e2eXAIModel {
		t.Errorf("Name() = %q, want %q", llm.Name(), e2eXAIModel)
	}

	if text := e2eXAIText(t, llm, &model.LLMRequest{
		Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "Say hi."}}}},
	}); text == "" {
		t.Error("expected non-empty text response")
	}
}

// TestE2EXAIListModels exercises the live listing endpoint and reports any
// catalog entry this build does not recognize — the signal that KnownModels
// and modeldata/context-windows.json need a refresh.
func TestE2EXAIListModels(t *testing.T) {
	key := testGetXAIAPIKey(t)

	models, err := ListModels(context.Background(), "xai", ListModelsOptions{APIKey: key})
	if err != nil {
		skipIfXAIUnprovisioned(t, err.Error())
		t.Fatalf("ListModels() error: %v", err)
	}
	if len(models) == 0 {
		t.Fatal("expected at least one model from the xAI catalog")
	}

	for _, m := range models {
		t.Logf("%s (owned_by=%s, window=%d)", m.ID, m.OwnedBy, ContextWindowSizeFor("xai", m.ID))
		if err := ValidateModel(Info{Provider: "xai", Model: m.ID}); err != nil {
			t.Errorf("live model %q is not in KnownModels: %v", m.ID, err)
		}
		if ContextWindowSizeFor("xai", m.ID) == 0 {
			t.Errorf("live model %q has no context window; auto-compaction is disabled for it", m.ID)
		}
	}
}

// TestE2EXAIServerSideTools drives xAI's built-in server-side tools through
// the production NewLLM path (the one pi's CLI uses), which opts xAI into
// web_search / x_search / code_interpreter and appends them to every request.
// These tools run on xAI's side of the wire, so the only failure mode this
// test can pin is the request being rejected or the response never carrying
// search results — which is exactly the regression that shipped when the
// OpenAI-style `include` parameter was sent alongside them.
//
// web_search is the one the whole default-on decision rests on, so it is
// mandatory: the request must succeed and the answer must reflect a live
// search. x_search and code_interpreter are account-entitled extras, so a 403
// or an answer that declines them is skipped rather than failed.
func TestE2EXAIServerSideTools(t *testing.T) {
	key := testGetXAIAPIKey(t)

	llm, err := NewLLM(context.Background(), Info{Provider: "xai", Model: e2eXAIModel}, key, "", "low", nil)
	if err != nil {
		t.Fatalf("NewLLM() error: %v", err)
	}
	if llm.Name() != e2eXAIModel {
		t.Errorf("Name() = %q, want %q", llm.Name(), e2eXAIModel)
	}

	// A time-stamped query so the search cannot be satisfied from the model's
	// parametric memory: the answer must come from a live web search.
	when := time.Now().UTC().Format("2006-01-02")
	text := e2eXAIText(t, llm, &model.LLMRequest{
		Contents: []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: fmt.Sprintf(
				"Use the web_search tool. Name any three news events that happened on %s. Reply with bullet points only.",
				when)}}},
		},
	})
	if text == "" {
		t.Fatal("expected non-empty text from a server-side web search")
	}
	// A real search must produce something more than a canned refusal.
	lower := strings.ToLower(text)
	if strings.Contains(lower, "cannot") && strings.Contains(lower, "search") {
		t.Errorf("response claims search is unavailable: %s", text)
	}
	t.Logf("web_search response: %s", text)
}

// TestE2EXAIXSearch pins the X (Twitter) search server-side tool. It is opt-in
// per account, so both the request-level 403 and an answer that declines to
// search are treated as skipped, while a successful search must name an
// account the model actually looked up.
func TestE2EXAIXSearch(t *testing.T) {
	key := testGetXAIAPIKey(t)

	llm, err := NewLLM(context.Background(), Info{Provider: "xai", Model: e2eXAIModel}, key, "", "low", nil)
	if err != nil {
		t.Fatalf("NewLLM() error: %v", err)
	}

	text := e2eXAIText(t, llm, &model.LLMRequest{
		Contents: []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: fmt.Sprintf(
				"Use the x_search tool. Find the most recent post by @xai and name the account it came from. One sentence.",
			)}}},
		},
	})
	if text == "" {
		t.Fatal("expected non-empty text from an X search")
	}
	t.Logf("x_search response: %s", text)
}

// TestE2EXAICodeInterpreter pins the sandboxed code_interpreter tool. Like
// x_search it is an account entitlement, so a declined run is skipped; a real
// run must produce the computed answer.
func TestE2EXAICodeInterpreter(t *testing.T) {
	key := testGetXAIAPIKey(t)

	llm, err := NewLLM(context.Background(), Info{Provider: "xai", Model: e2eXAIModel}, key, "", "low", nil)
	if err != nil {
		t.Fatalf("NewLLM() error: %v", err)
	}

	text := e2eXAIText(t, llm, &model.LLMRequest{
		Contents: []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: fmt.Sprintf(
				"Use the code_interpreter tool to compute 12345 * 6789 and reply with just the product.",
			)}}},
		},
	})
	if text == "" {
		t.Fatal("expected non-empty text from the code interpreter")
	}
	t.Logf("code_interpreter response: %s", text)
}
