package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

// ---------------------------------------------------------------------------
// pingRequest
// ---------------------------------------------------------------------------

func TestPingRequest(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		prompt      string
		isPingPong  bool
		wantSysHas  string
		wantPromptA string
	}{
		{
			name:        "ping pong pins the expected answer",
			prompt:      "prompt-prompt",
			isPingPong:  true,
			wantSysHas:  `reply with exactly "prompt-prompt"`,
			wantPromptA: "prompt-prompt",
		},
		{
			name:        "custom prompt asks only for brevity",
			prompt:      "2+2",
			isPingPong:  false,
			wantSysHas:  "Reply briefly and concisely",
			wantPromptA: "2+2",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := pingRequest(tc.prompt, tc.isPingPong)

			if len(req.Contents) != 1 || len(req.Contents[0].Parts) != 1 {
				t.Fatalf("Contents = %#v, want one part in one content", req.Contents)
			}
			if got := req.Contents[0].Parts[0].Text; got != tc.wantPromptA {
				t.Errorf("prompt text = %q, want %q", got, tc.wantPromptA)
			}
			if got := req.Contents[0].Role; got != genai.RoleUser {
				t.Errorf("prompt role = %q, want %q", got, genai.RoleUser)
			}

			sys := req.Config.SystemInstruction
			if sys == nil || len(sys.Parts) != 1 {
				t.Fatalf("SystemInstruction = %#v, want one part", sys)
			}
			if got := sys.Parts[0].Text; !strings.Contains(got, tc.wantSysHas) {
				t.Errorf("system instruction = %q, want it to contain %q", got, tc.wantSysHas)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// traceNonStreamParts
// ---------------------------------------------------------------------------

func TestTraceNonStreamParts(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("x", 130)

	cases := []struct {
		name     string
		parts    []*genai.Part
		wantText string
		wantOut  []string
		notOut   []string
	}{
		{
			name:     "no parts traces nothing",
			parts:    nil,
			wantText: "",
		},
		{
			name:     "text parts concatenate in order",
			parts:    []*genai.Part{{Text: "he"}, {Text: "llo"}},
			wantText: "hello",
			wantOut:  []string{"part[0] text(2 chars): he", "part[1] text(3 chars): llo"},
		},
		{
			name: "long text is truncated in the trace but not in the reply",
			// The trace caps the preview at 120 runes; the accumulated reply
			// keeps every byte, which is what the verdict is judged on.
			parts:    []*genai.Part{{Text: long}},
			wantText: long,
			wantOut:  []string{fmt.Sprintf("text(130 chars): %s...", long[:120])},
		},
		{
			name:     "tool call contributes no text",
			parts:    []*genai.Part{{FunctionCall: &genai.FunctionCall{Name: "read"}}},
			wantText: "",
			wantOut:  []string{"part[0] tool_call: read"},
			notOut:   []string{"text("},
		},
		{
			name:     "thought is flagged and its text still counts",
			parts:    []*genai.Part{{Text: "hm", Thought: true}},
			wantText: "hm",
			wantOut:  []string{"part[0] thought=true", "part[0] text(2 chars): hm"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var out strings.Builder
			w := pingWriter(func(format string, a ...any) { fmt.Fprintf(&out, format, a...) })

			if got := traceNonStreamParts(w, tc.parts); got != tc.wantText {
				t.Errorf("traceNonStreamParts() = %q, want %q", got, tc.wantText)
			}
			for _, want := range tc.wantOut {
				if !strings.Contains(out.String(), want) {
					t.Errorf("trace output missing %q:\n%s", want, out.String())
				}
			}
			for _, unwanted := range tc.notOut {
				if strings.Contains(out.String(), unwanted) {
					t.Errorf("trace output should not contain %q:\n%s", unwanted, out.String())
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// modelPingNonStream / modelPingStream
// ---------------------------------------------------------------------------

func TestModelPingNonStream(t *testing.T) {
	t.Parallel()

	boom := errors.New("boom")

	cases := []struct {
		name      string
		llm       model.LLM
		wantReply string
		wantErr   bool
		wantOut   []string
	}{
		{
			name:      "text is accumulated and the phase reports done",
			llm:       &cliMockLLM{name: "m", response: "hi"},
			wantReply: "hi",
			wantOut:   []string{"Calling GenerateContent(stream=false)", "Done: 1 events"},
		},
		{
			name:    "an error stops the phase without a done line",
			llm:     &cliErrorLLM{name: "m", err: boom},
			wantErr: true,
			wantOut: []string{"ERROR at event 1: boom"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var out strings.Builder
			w := pingWriter(func(format string, a ...any) { fmt.Fprintf(&out, format, a...) })

			reply, err := modelPingNonStream(context.Background(), tc.llm, pingRequest("p", false), w)
			if (err != nil) != tc.wantErr {
				t.Fatalf("modelPingNonStream() error = %v, wantErr %v", err, tc.wantErr)
			}
			if reply != tc.wantReply {
				t.Errorf("reply = %q, want %q", reply, tc.wantReply)
			}
			if tc.wantErr && strings.Contains(out.String(), "Done:") {
				t.Errorf("a failed phase must not print its done line:\n%s", out.String())
			}
			for _, want := range tc.wantOut {
				if !strings.Contains(out.String(), want) {
					t.Errorf("output missing %q:\n%s", want, out.String())
				}
			}
		})
	}
}

func TestModelPingStream(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		llm       model.LLM
		wantReply string
		wantErr   bool
		wantOut   []string
	}{
		{
			name:      "assistant text is the reply",
			llm:       &cliMockLLM{name: "m", response: "hello"},
			wantReply: "hello",
			wantOut:   []string{"1 text chunks", "Reply: hello"},
		},
		{
			name: "thinking chunks are counted apart from the reply",
			llm: &cliThinkingLLM{
				name:         "m",
				thoughtText:  "pondering",
				responseText: "answer",
			},
			wantReply: "answer",
			wantOut:   []string{"(1 thinking, 1 text chunks)"},
		},
		{
			name:    "an error ends the phase",
			llm:     &cliErrorLLM{name: "m", err: errors.New("nope")},
			wantErr: true,
			wantOut: []string{"ERROR at event 1: nope"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var out strings.Builder
			w := pingWriter(func(format string, a ...any) { fmt.Fprintf(&out, format, a...) })

			reply, err := modelPingStream(context.Background(), tc.llm, pingRequest("p", false), w)
			if (err != nil) != tc.wantErr {
				t.Fatalf("modelPingStream() error = %v, wantErr %v", err, tc.wantErr)
			}
			if reply != tc.wantReply {
				t.Errorf("reply = %q, want %q", reply, tc.wantReply)
			}
			for _, want := range tc.wantOut {
				if !strings.Contains(out.String(), want) {
					t.Errorf("output missing %q:\n%s", want, out.String())
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// ollamaCheckModel
// ---------------------------------------------------------------------------

func TestOllamaCheckModel(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		served    []string
		requested string
		wantErr   string
	}{
		{
			name:      "exact tag matches",
			served:    []string{"llama3:8b", "qwen2.5:7b"},
			requested: "llama3:8b",
		},
		{
			name:      "bare name matches a tagged model",
			served:    []string{"llama3:8b"},
			requested: "llama3",
		},
		{
			name:      "a different tag of the same model matches",
			served:    []string{"llama3:70b"},
			requested: "llama3:8b",
		},
		{
			name:      "unknown model is reported",
			served:    []string{"llama3:8b"},
			requested: "mistral",
			wantErr:   `model "mistral" not found`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv := mockOllamaPingServer(t, tc.served, "unused")
			defer srv.Close()

			var out strings.Builder
			w := pingWriter(func(format string, a ...any) { fmt.Fprintf(&out, format, a...) })

			err := ollamaCheckModel(context.Background(), srv.URL, tc.requested, w)
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("ollamaCheckModel() error = %v, want nil", err)
			case tc.wantErr != "" && err == nil:
				t.Fatalf("ollamaCheckModel() = nil, want error containing %q", tc.wantErr)
			case tc.wantErr != "":
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %v, want it to contain %q", err, tc.wantErr)
				}
				return
			}
			if !strings.Contains(out.String(), "found ✓") {
				t.Errorf("a resolved model should be reported as found:\n%s", out.String())
			}
		})
	}
}

func TestOllamaCheckModelListError(t *testing.T) {
	t.Parallel()

	// An endpoint nothing is listening on: the listing itself must fail, and the
	// error has to name the step so a ping transcript stays readable.
	err := ollamaCheckModel(context.Background(), "http://127.0.0.1:1", "llama3", func(string, ...any) {})
	if err == nil || !strings.Contains(err.Error(), "list models") {
		t.Fatalf("ollamaCheckModel() error = %v, want it to mention \"list models\"", err)
	}
}
