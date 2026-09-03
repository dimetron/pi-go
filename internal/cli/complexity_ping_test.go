package cli

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"iter"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"

	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/memory"
	"github.com/dimetron/pi-go/internal/palace"
	"github.com/dimetron/pi-go/internal/provider"
	"github.com/dimetron/pi-go/internal/testenv"
)

// These tests pin the branch structure that the complexity refactor extracted
// out of modelPing, ollamaPingFull, resolvePingTarget, reportHTTPVerdict,
// runModelList, runMemoryRecent, scanFiles and runMemoryMine. Each extracted
// helper is exercised at the boundaries its original branch encoded, so a
// behavior change shows up as a failing case rather than as a diff nobody can
// verify by eye.

// ---------------------------------------------------------------------------
// ping request construction
// ---------------------------------------------------------------------------

func TestPingSystemMessage(t *testing.T) {
	t.Parallel()
	pingPong := pingSystemMessage(true)
	if !strings.Contains(pingPong, `reply with exactly "prompt-prompt"`) {
		t.Errorf("ping-pong system message lost its exact-reply instruction: %q", pingPong)
	}
	custom := pingSystemMessage(false)
	if custom != "You are a connectivity test. Reply briefly and concisely." {
		t.Errorf("custom-prompt system message = %q", custom)
	}
	if pingPong == custom {
		t.Error("the two modes must not share a system message")
	}
}

func TestNewPingRequest(t *testing.T) {
	t.Parallel()
	req := newPingRequest("2+2", false)
	if len(req.Contents) != 1 || req.Contents[0].Role != genai.RoleUser {
		t.Fatalf("expected a single user turn, got %#v", req.Contents)
	}
	if got := req.Contents[0].Parts[0].Text; got != "2+2" {
		t.Errorf("prompt = %q, want %q", got, "2+2")
	}
	sys := req.Config.SystemInstruction.Parts[0].Text
	if sys != pingSystemMessage(false) {
		t.Errorf("system instruction = %q, want the custom-prompt message", sys)
	}
	if got := newPingRequest("x", true).Config.SystemInstruction.Parts[0].Text; got != pingSystemMessage(true) {
		t.Errorf("ping-pong request did not carry the ping-pong system message")
	}
}

// ---------------------------------------------------------------------------
// modelPing: the non-streaming half
// ---------------------------------------------------------------------------

func TestModelPingNonStreamCollectsAndCounts(t *testing.T) {
	t.Parallel()
	llm := &pingMockLLM{
		name: "mock",
		responses: []*model.LLMResponse{
			{Content: genai.NewContentFromText("hel", genai.RoleModel)},
			{Content: genai.NewContentFromText("lo", genai.RoleModel)},
		},
	}
	w, read := captureWriter(t)
	text, events, err := modelPingNonStream(context.Background(), llm, newPingRequest("hi", false), w)
	if err != nil {
		t.Fatalf("modelPingNonStream: %v", err)
	}
	if text != "hello" {
		t.Errorf("text = %q, want %q", text, "hello")
	}
	if events != 2 {
		t.Errorf("events = %d, want 2", events)
	}
	if out := read(); !strings.Contains(out, "[non-stream]") {
		t.Errorf("expected traced events, got:\n%s", out)
	}
}

// A non-stream error keeps whatever text already arrived: the Azure soft-failure
// path in modelPing depends on that partial result surviving.
func TestModelPingNonStreamErrorKeepsPartialText(t *testing.T) {
	t.Parallel()
	llm := &pingMockLLM{name: "mock", err: errors.New("boom")}
	w, _ := captureWriter(t)
	text, events, err := modelPingNonStream(context.Background(), llm, newPingRequest("hi", false), w)
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "non-streaming LLM error") {
		t.Errorf("error = %v, want it wrapped as a non-streaming LLM error", err)
	}
	if text != "" || events != 1 {
		t.Errorf("got (text=%q, events=%d), want (\"\", 1)", text, events)
	}
}

func TestLogPingNonStreamEvent(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		resp    *model.LLMResponse
		want    []string
		notWant []string
	}{
		{
			name:    "bare event",
			resp:    &model.LLMResponse{Partial: true},
			want:    []string{"event 7: partial=true"},
			notWant: []string{"errorCode", "tokens("},
		},
		{
			name: "error code is shown",
			resp: &model.LLMResponse{ErrorCode: "429", ErrorMessage: "slow down"},
			want: []string{"errorCode=429", "errorMsg=slow down"},
		},
		{
			name: "usage is shown",
			resp: &model.LLMResponse{UsageMetadata: &genai.GenerateContentResponseUsageMetadata{
				PromptTokenCount: 11, CandidatesTokenCount: 22,
			}},
			want: []string{"tokens(in=11 out=22)"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			w, read := captureWriter(t)
			logPingNonStreamEvent(w, tt.resp, 7)
			out := read()
			for _, want := range tt.want {
				if !strings.Contains(out, want) {
					t.Errorf("expected %q in:\n%s", want, out)
				}
			}
			for _, no := range tt.notWant {
				if strings.Contains(out, no) {
					t.Errorf("did not expect %q in:\n%s", no, out)
				}
			}
		})
	}
}

func TestLogPingNonStreamContent(t *testing.T) {
	t.Parallel()

	t.Run("nil content writes nothing", func(t *testing.T) {
		t.Parallel()
		w, read := captureWriter(t)
		var sb strings.Builder
		logPingNonStreamContent(w, &model.LLMResponse{}, &sb)
		if out := read(); out != "" {
			t.Errorf("expected no output for a content-less event, got:\n%s", out)
		}
		if sb.Len() != 0 {
			t.Errorf("expected no accumulated text, got %q", sb.String())
		}
	})

	t.Run("parts are traced and text accumulated", func(t *testing.T) {
		t.Parallel()
		resp := &model.LLMResponse{Content: &genai.Content{
			Role: genai.RoleModel,
			Parts: []*genai.Part{
				{Text: "answer"},
				{FunctionCall: &genai.FunctionCall{Name: "search"}},
				{Text: "thought text", Thought: true},
			},
		}}
		w, read := captureWriter(t)
		var sb strings.Builder
		logPingNonStreamContent(w, resp, &sb)
		out := read()
		for _, want := range []string{"role=model parts=3", "part[0] text(6 chars): answer", "part[1] tool_call: search", "part[2] thought=true"} {
			if !strings.Contains(out, want) {
				t.Errorf("expected %q in:\n%s", want, out)
			}
		}
		// Every text part is accumulated, thought parts included — that is what
		// the original loop did.
		if sb.String() != "answerthought text" {
			t.Errorf("accumulated %q", sb.String())
		}
	})

	t.Run("long text is previewed at 120 chars", func(t *testing.T) {
		t.Parallel()
		long := strings.Repeat("x", 200)
		resp := &model.LLMResponse{Content: &genai.Content{
			Role:  genai.RoleModel,
			Parts: []*genai.Part{{Text: long}},
		}}
		w, read := captureWriter(t)
		var sb strings.Builder
		logPingNonStreamContent(w, resp, &sb)
		if out := read(); !strings.Contains(out, strings.Repeat("x", 120)+"...") {
			t.Errorf("expected a 120-char preview with an ellipsis, got:\n%s", out)
		}
		if sb.String() != long {
			t.Error("the accumulated text must not be truncated, only the preview")
		}
	})
}

// ---------------------------------------------------------------------------
// modelPing: the streaming half
// ---------------------------------------------------------------------------

func TestAccumulatePingStreamParts(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		resp         *model.LLMResponse
		wantThinking int
		wantText     int
		wantOut      string
	}{
		{name: "nil content", resp: &model.LLMResponse{}},
		{
			name: "model text counts as text",
			resp: &model.LLMResponse{Content: &genai.Content{
				Role: genai.RoleModel, Parts: []*genai.Part{{Text: "a"}, {Text: "b"}},
			}},
			wantText: 2, wantOut: "ab",
		},
		{
			name: "thinking role is counted but never joins the reply",
			resp: &model.LLMResponse{Content: &genai.Content{
				Role: "thinking", Parts: []*genai.Part{{Text: "hmm"}},
			}},
			wantThinking: 1,
		},
		{
			name: "empty text parts are ignored entirely",
			resp: &model.LLMResponse{Content: &genai.Content{
				Role: genai.RoleModel, Parts: []*genai.Part{{Text: ""}, {FunctionCall: &genai.FunctionCall{Name: "f"}}},
			}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var sb strings.Builder
			thinking, text := accumulatePingStreamParts(tt.resp, &sb)
			if thinking != tt.wantThinking || text != tt.wantText {
				t.Errorf("got (thinking=%d, text=%d), want (%d, %d)", thinking, text, tt.wantThinking, tt.wantText)
			}
			if sb.String() != tt.wantOut {
				t.Errorf("accumulated %q, want %q", sb.String(), tt.wantOut)
			}
		})
	}
}

func TestLogPingStreamFinalEvent(t *testing.T) {
	t.Parallel()
	w, read := captureWriter(t)
	logPingStreamFinalEvent(w, &model.LLMResponse{TurnComplete: true}, 3)
	out := read()
	if !strings.Contains(out, "final event 3: turnComplete=true") {
		t.Errorf("unexpected final-event line:\n%s", out)
	}
	if strings.Contains(out, "tokens(") {
		t.Errorf("no usage metadata means no token line:\n%s", out)
	}

	w2, read2 := captureWriter(t)
	logPingStreamFinalEvent(w2, &model.LLMResponse{UsageMetadata: &genai.GenerateContentResponseUsageMetadata{
		PromptTokenCount: 5, CandidatesTokenCount: 6,
	}}, 4)
	if out := read2(); !strings.Contains(out, "tokens(in=5 out=6)") {
		t.Errorf("expected token counts, got:\n%s", out)
	}
}

func TestModelPingStream(t *testing.T) {
	t.Parallel()

	t.Run("collects text and reports chunk counts", func(t *testing.T) {
		t.Parallel()
		llm := &cliThinkingLLM{name: "mock", thoughtText: "hmm", responseText: "done"}
		w, read := captureWriter(t)
		text, err := modelPingStream(context.Background(), llm, newPingRequest("hi", false), w)
		if err != nil {
			t.Fatalf("modelPingStream: %v", err)
		}
		if text != "done" {
			t.Errorf("text = %q, want %q — thinking output must not join the reply", text, "done")
		}
		if out := read(); !strings.Contains(out, "(1 thinking, 1 text chunks)") {
			t.Errorf("expected the chunk tally, got:\n%s", out)
		}
	})

	t.Run("an error-code event is reported and skipped", func(t *testing.T) {
		t.Parallel()
		llm := &pingMockLLM{name: "mock", responses: []*model.LLMResponse{
			{ErrorCode: "overloaded", ErrorMessage: "try later"},
			{Content: genai.NewContentFromText("recovered", genai.RoleModel)},
		}}
		w, read := captureWriter(t)
		text, err := modelPingStream(context.Background(), llm, newPingRequest("hi", false), w)
		if err != nil {
			t.Fatalf("an error-code event must not fail the stream: %v", err)
		}
		if text != "recovered" {
			t.Errorf("text = %q, want %q", text, "recovered")
		}
		out := read()
		if !strings.Contains(out, "errorCode=overloaded") {
			t.Errorf("expected the error code to be traced, got:\n%s", out)
		}
		// The skipped event must not also print a final-event summary line.
		if strings.Count(out, "final event") != 1 {
			t.Errorf("expected exactly one final-event line, got:\n%s", out)
		}
	})

	t.Run("a transport error is wrapped", func(t *testing.T) {
		t.Parallel()
		llm := &pingMockLLM{name: "mock", err: errors.New("reset")}
		w, _ := captureWriter(t)
		_, err := modelPingStream(context.Background(), llm, newPingRequest("hi", false), w)
		if err == nil || !strings.Contains(err.Error(), "streaming LLM error") {
			t.Fatalf("err = %v, want it wrapped as a streaming LLM error", err)
		}
	})

	t.Run("long replies are previewed at 200 chars", func(t *testing.T) {
		t.Parallel()
		long := strings.Repeat("y", 300)
		llm := &pingMockLLM{name: "mock", responses: []*model.LLMResponse{
			{Content: genai.NewContentFromText(long, genai.RoleModel)},
		}}
		w, read := captureWriter(t)
		text, err := modelPingStream(context.Background(), llm, newPingRequest("hi", false), w)
		if err != nil {
			t.Fatalf("modelPingStream: %v", err)
		}
		if text != long {
			t.Error("the returned reply must not be truncated")
		}
		if out := read(); !strings.Contains(out, "Reply: "+strings.Repeat("y", 200)+"...") {
			t.Errorf("expected a 200-char preview, got:\n%s", out)
		}
	})

	t.Run("an empty reply prints no preview line", func(t *testing.T) {
		t.Parallel()
		llm := &pingMockLLM{name: "mock", responses: []*model.LLMResponse{{TurnComplete: true}}}
		w, read := captureWriter(t)
		text, err := modelPingStream(context.Background(), llm, newPingRequest("hi", false), w)
		if err != nil || text != "" {
			t.Fatalf("got (%q, %v), want (\"\", nil)", text, err)
		}
		if out := read(); strings.Contains(out, "Reply:") {
			t.Errorf("expected no Reply line for empty output, got:\n%s", out)
		}
	})
}

// streamAwareLLM answers differently depending on the stream flag, which is the
// only way to reach modelPing's "the stream failed but the non-stream did not"
// branch — a mock that fails both calls stops at the non-stream check first.
type streamAwareLLM struct {
	name        string
	nonStream   []*model.LLMResponse
	streamErr   error
	streamTexts []string
}

func (m *streamAwareLLM) Name() string { return m.name }

func (m *streamAwareLLM) GenerateContent(_ context.Context, _ *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		if !stream {
			for _, r := range m.nonStream {
				if !yield(r, nil) {
					return
				}
			}
			return
		}
		if m.streamErr != nil {
			yield(nil, m.streamErr)
			return
		}
		for _, text := range m.streamTexts {
			if !yield(&model.LLMResponse{Content: genai.NewContentFromText(text, genai.RoleModel)}, nil) {
				return
			}
		}
	}
}

// A stream failure returns the text the non-streaming call already produced,
// not the streaming accumulator — a caller that reported the empty streaming
// result would throw away a reply that proves the model is alive.
func TestModelPingStreamFailureReturnsNonStreamText(t *testing.T) {
	t.Parallel()
	llm := &streamAwareLLM{
		name:      "mock",
		nonStream: []*model.LLMResponse{{Content: genai.NewContentFromText("ns reply", genai.RoleModel)}},
		streamErr: errors.New("stream died"),
	}
	reply, err := modelPing(context.Background(), llm, "hi", false, "openai")
	if err == nil || !strings.Contains(err.Error(), "streaming LLM error") {
		t.Fatalf("err = %v, want a streaming LLM error", err)
	}
	if reply != "ns reply" {
		t.Errorf("reply = %q, want the non-streaming text %q", reply, "ns reply")
	}
}

// When the non-streaming call comes back empty the streaming text is the reply.
func TestModelPingEmptyNonStreamFallsBackToStream(t *testing.T) {
	t.Parallel()
	llm := &streamAwareLLM{
		name:        "mock",
		nonStream:   []*model.LLMResponse{{TurnComplete: true}},
		streamTexts: []string{"stream only"},
	}
	reply, err := modelPing(context.Background(), llm, "hi", false, "openai")
	if err != nil {
		t.Fatalf("modelPing: %v", err)
	}
	if reply != "stream only" {
		t.Errorf("reply = %q, want %q", reply, "stream only")
	}
}

// Both modes silent is the one case that is a hard failure.
func TestModelPingBothModesEmpty(t *testing.T) {
	t.Parallel()
	llm := &streamAwareLLM{name: "mock", nonStream: []*model.LLMResponse{{TurnComplete: true}}}
	if _, err := modelPing(context.Background(), llm, "hi", false, "openai"); err == nil ||
		!strings.Contains(err.Error(), "empty response in both streaming and non-streaming modes") {
		t.Fatalf("err = %v, want the both-modes-empty error", err)
	}
}

// ---------------------------------------------------------------------------
// ollamaPingFull helpers
// ---------------------------------------------------------------------------

// ollamaStreamAwareServer answers /api/chat differently for the streaming and
// non-streaming calls, which mockOllamaPingServer cannot do. The non-streaming
// request is the one that carries "stream":false.
func ollamaStreamAwareServer(t *testing.T, models []string, nonStreamText, streamText string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/tags":
			resp := struct {
				Models []struct{ Name string } `json:"models"`
			}{}
			for _, m := range models {
				resp.Models = append(resp.Models, struct{ Name string }{Name: m})
			}
			_ = json.NewEncoder(w).Encode(resp)
		case "/api/chat":
			body, _ := io.ReadAll(r.Body)
			text := streamText
			if strings.Contains(string(body), `"stream":false`) {
				text = nonStreamText
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"message": map[string]string{"role": "assistant", "content": text},
				"done":    true,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// Which of the two probes the reply comes from is the branch under test. The
// assertions name the source rather than the exact string, because the mock
// server's single JSON object is read as more than one streaming event and the
// resulting chunk count is an artifact of the mock, not of ollamaPingFull.
func TestOllamaPingFullReplySelection(t *testing.T) {
	const nsMarker, sMarker = "NONSTREAM", "STREAMED"

	tests := []struct {
		name                      string
		nonStreamText, streamText string
		wantFrom                  string
		notFrom                   string
		wantErr                   string
	}{
		{
			name:          "non-stream wins when both answer",
			nonStreamText: nsMarker, streamText: sMarker,
			wantFrom: nsMarker, notFrom: sMarker,
		},
		{
			name:       "empty non-stream falls back to the stream",
			streamText: sMarker,
			wantFrom:   sMarker,
		},
		{name: "both silent is an error", wantErr: "empty response in both modes"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := ollamaStreamAwareServer(t, []string{"test-model"}, tt.nonStreamText, tt.streamText)
			w, _ := captureWriter(t)
			reply, err := ollamaPingFull(context.Background(), srv.URL, "test-model", "hello", false, w)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want it to mention %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ollamaPingFull: %v", err)
			}
			if !strings.Contains(reply, tt.wantFrom) {
				t.Errorf("reply = %q, want it to come from %s", reply, tt.wantFrom)
			}
			if tt.notFrom != "" && strings.Contains(reply, tt.notFrom) {
				t.Errorf("reply = %q, must not include the %s probe's text", reply, tt.notFrom)
			}
		})
	}
}

func TestOllamaEnsureModel(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		available []string
		want      string
		wantErr   string
	}{
		{name: "exact match", available: []string{"llama3:8b"}, want: "llama3:8b"},
		{name: "tag-less prefix match", available: []string{"llama3:8b"}, want: "llama3"},
		{name: "not served", available: []string{"mistral"}, want: "llama3", wantErr: "not found in available models"},
		{name: "empty daemon", available: nil, want: "llama3", wantErr: "not found in available models"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv := mockOllamaPingServer(t, tt.available, "")
			defer srv.Close()
			w, read := captureWriter(t)
			err := ollamaEnsureModel(context.Background(), srv.URL, tt.want, w)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want it to mention %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ollamaEnsureModel: %v", err)
			}
			if out := read(); !strings.Contains(out, "found ✓") {
				t.Errorf("expected a found line, got:\n%s", out)
			}
		})
	}
}

// A daemon that cannot be reached at all is a different failure from a model
// that is not served, and the wrapping says which.
func TestOllamaEnsureModelListFailure(t *testing.T) {
	t.Parallel()
	w, _ := captureWriter(t)
	err := ollamaEnsureModel(context.Background(), "http://127.0.0.1:1", "llama3", w)
	if err == nil || !strings.Contains(err.Error(), "list models") {
		t.Fatalf("err = %v, want it wrapped as a list-models failure", err)
	}
}

func TestAppendOllamaPingText(t *testing.T) {
	t.Parallel()
	thinkingResp := func() *model.LLMResponse {
		return &model.LLMResponse{Content: &genai.Content{
			Role: "thinking", Parts: []*genai.Part{{Text: "hmm"}},
		}}
	}
	tests := []struct {
		name         string
		resp         *model.LLMResponse
		skipThinking bool
		wantChunks   int
		wantOut      string
	}{
		{name: "nil content", resp: &model.LLMResponse{}},
		{
			name: "model text is taken either way",
			resp: &model.LLMResponse{Content: &genai.Content{
				Role: genai.RoleModel, Parts: []*genai.Part{{Text: "a"}, {Text: "b"}},
			}},
			skipThinking: true, wantChunks: 2, wantOut: "ab",
		},
		{
			// The streaming probe drops thinking output...
			name: "thinking dropped when skipping",
			resp: thinkingResp(), skipThinking: true,
		},
		{
			// ...while the non-streaming probe has always kept every text part.
			name: "thinking kept when not skipping",
			resp: thinkingResp(), skipThinking: false, wantChunks: 1, wantOut: "hmm",
		},
		{
			name: "empty text is never a chunk",
			resp: &model.LLMResponse{Content: &genai.Content{
				Role: genai.RoleModel, Parts: []*genai.Part{{Text: ""}},
			}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var sb strings.Builder
			got := appendOllamaPingText(tt.resp, &sb, tt.skipThinking)
			if got != tt.wantChunks {
				t.Errorf("chunks = %d, want %d", got, tt.wantChunks)
			}
			if sb.String() != tt.wantOut {
				t.Errorf("accumulated %q, want %q", sb.String(), tt.wantOut)
			}
		})
	}
}

func TestLogOllamaPingUsage(t *testing.T) {
	t.Parallel()
	w, read := captureWriter(t)
	logOllamaPingUsage(w, "non-stream", &model.LLMResponse{})
	if out := read(); out != "" {
		t.Errorf("expected nothing without usage metadata, got:\n%s", out)
	}

	w2, read2 := captureWriter(t)
	logOllamaPingUsage(w2, "stream", &model.LLMResponse{UsageMetadata: &genai.GenerateContentResponseUsageMetadata{
		PromptTokenCount: 3, CandidatesTokenCount: 4,
	}})
	out := read2()
	if !strings.Contains(out, "[stream]") || !strings.Contains(out, "tokens(in=3 out=4)") {
		t.Errorf("unexpected usage line:\n%s", out)
	}
}

func TestOllamaPingNonStreamAndStream(t *testing.T) {
	t.Parallel()

	t.Run("non-stream keeps thinking text", func(t *testing.T) {
		t.Parallel()
		llm := &cliThinkingLLM{name: "mock", thoughtText: "hmm", responseText: "hi"}
		w, read := captureWriter(t)
		text, err := ollamaPingNonStream(context.Background(), llm, newPingRequest("p", false), w)
		if err != nil {
			t.Fatalf("ollamaPingNonStream: %v", err)
		}
		if text != "hmmhi" {
			t.Errorf("text = %q, want %q", text, "hmmhi")
		}
		if out := read(); !strings.Contains(out, "[non-stream] Done") && !strings.Contains(out, "Done") {
			t.Errorf("expected a Done line, got:\n%s", out)
		}
	})

	t.Run("stream drops thinking text and counts chunks", func(t *testing.T) {
		t.Parallel()
		llm := &cliThinkingLLM{name: "mock", thoughtText: "hmm", responseText: "hi"}
		w, read := captureWriter(t)
		text, err := ollamaPingStream(context.Background(), llm, newPingRequest("p", false), w)
		if err != nil {
			t.Fatalf("ollamaPingStream: %v", err)
		}
		if text != "hi" {
			t.Errorf("text = %q, want %q", text, "hi")
		}
		if out := read(); !strings.Contains(out, "1 chunks") {
			t.Errorf("expected one counted chunk, got:\n%s", out)
		}
	})

	t.Run("errors are wrapped per phase", func(t *testing.T) {
		t.Parallel()
		llm := &pingMockLLM{name: "mock", err: errors.New("down")}
		w, _ := captureWriter(t)
		if _, err := ollamaPingNonStream(context.Background(), llm, newPingRequest("p", false), w); err == nil ||
			!strings.Contains(err.Error(), "non-streaming:") {
			t.Errorf("non-stream err = %v, want a \"non-streaming:\" prefix", err)
		}
		if _, err := ollamaPingStream(context.Background(), llm, newPingRequest("p", false), w); err == nil ||
			!strings.Contains(err.Error(), "streaming:") {
			t.Errorf("stream err = %v, want a \"streaming:\" prefix", err)
		}
	})
}

// ---------------------------------------------------------------------------
// resolvePingTarget helpers
// ---------------------------------------------------------------------------

func TestPingRoleFromFlags(t *testing.T) {
	origSmol, origSlow, origPlan := flagSmol, flagSlow, flagPlan
	t.Cleanup(func() { flagSmol, flagSlow, flagPlan = origSmol, origSlow, origPlan })

	tests := []struct {
		name             string
		smol, slow, plan bool
		want             string
	}{
		{name: "no flag", want: "default"},
		{name: "smol", smol: true, want: "smol"},
		{name: "slow", slow: true, want: "slow"},
		{name: "plan", plan: true, want: "plan"},
		// The switch is ordered, so smol wins when several are set.
		{name: "smol wins over slow and plan", smol: true, slow: true, plan: true, want: "smol"},
		{name: "slow wins over plan", slow: true, plan: true, want: "slow"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flagSmol, flagSlow, flagPlan = tt.smol, tt.slow, tt.plan
			if got := pingRoleFromFlags(); got != tt.want {
				t.Errorf("pingRoleFromFlags() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolvePingCredentials(t *testing.T) {
	tests := []struct {
		name        string
		info        provider.Info
		baseURL     string
		explicit    bool
		env         map[string]string
		wantBaseURL string
		wantCodex   bool
	}{
		{
			name:        "default base URL fills in for a bare provider",
			info:        provider.Info{Provider: "anthropic", Model: "claude-x"},
			wantBaseURL: "https://api.anthropic.com",
		},
		{
			name:        "a configured base URL survives",
			info:        provider.Info{Provider: "openai", Model: "gpt-x"},
			baseURL:     "https://proxy.example",
			explicit:    true,
			wantBaseURL: "https://proxy.example",
		},
		{
			name:        "an unknown provider gets no default",
			info:        provider.Info{Provider: "who", Model: "m"},
			wantBaseURL: "",
		},
		{
			name:        "ollama routes through the tag resolver",
			info:        provider.Info{Provider: "ollama", Model: "qwen3:8b", Ollama: true},
			wantBaseURL: "http://localhost:11434",
		},
		{
			name:        "azure reads its endpoint env var",
			info:        provider.Info{Provider: "azure", Model: "gpt-4o"},
			env:         map[string]string{"AZURE_OPENAI_ENDPOINT": "https://unit.openai.azure.com"},
			wantBaseURL: "https://unit.openai.azure.com",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testenv.SetHome(t, t.TempDir())
			t.Setenv("OLLAMA_HOST", "")
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			gotURL, _, gotCodex := resolvePingCredentials(tt.info, tt.baseURL, tt.explicit)
			if gotURL != tt.wantBaseURL {
				t.Errorf("baseURL = %q, want %q", gotURL, tt.wantBaseURL)
			}
			if gotCodex != tt.wantCodex {
				t.Errorf("codexBackend = %v, want %v", gotCodex, tt.wantCodex)
			}
		})
	}
}

func TestPingProbePaths(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		info          provider.Info
		baseURL       string
		codex         bool
		wantEndpoint  string
		wantFallbacks bool
	}{
		{
			name: "codex overrides everything with POST /responses",
			// The provider is openai, which would otherwise probe /v1/models.
			info: provider.Info{Provider: "openai", Model: "gpt-5"}, baseURL: codexPingBaseURL,
			codex: true, wantEndpoint: "/responses",
		},
		{
			name: "openai probes the model list",
			info: provider.Info{Provider: "openai", Model: "gpt-5"}, baseURL: "https://api.openai.com",
			wantEndpoint: "/v1/models",
		},
		{
			name: "a /v1 base URL is not doubled up",
			info: provider.Info{Provider: "openai", Model: "gpt-5"}, baseURL: "https://proxy.example/v1",
			wantEndpoint: "/models",
		},
		{
			name: "anthropic probes messages",
			info: provider.Info{Provider: "anthropic", Model: "claude-x"}, baseURL: "https://api.anthropic.com",
			wantEndpoint: "/v1/messages",
		},
		{
			name: "azure supplies fallbacks",
			info: provider.Info{Provider: "azure", Model: "gpt-4o"}, baseURL: "https://unit.openai.azure.com",
			wantFallbacks: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			endpoint, fallbacks := pingProbePaths(tt.info, tt.baseURL, tt.codex)
			if tt.wantEndpoint != "" && endpoint != tt.wantEndpoint {
				t.Errorf("endpoint = %q, want %q", endpoint, tt.wantEndpoint)
			}
			if tt.wantFallbacks {
				if endpoint == "" {
					t.Error("azure must still name a first probe path")
				}
				if len(fallbacks) == 0 {
					t.Error("azure must supply at least one fallback path")
				}
				return
			}
			if len(fallbacks) != 0 {
				t.Errorf("fallbacks = %v, want none — only azure has them", fallbacks)
			}
		})
	}
}

func TestResolvePingModelInfo(t *testing.T) {
	origURL, origModel := flagURL, flagModel
	t.Cleanup(func() { flagURL, flagModel = origURL, origModel })
	flagURL, flagModel = "", ""
	testenv.SetHome(t, t.TempDir())

	t.Run("a configured provider wins over inference", func(t *testing.T) {
		cfg := config.Config{Roles: map[string]config.RoleConfig{
			"default": {Model: "claude-sonnet-4-5", Provider: "anthropic"},
		}}
		info, _, explicit, err := resolvePingModelInfo(cfg, "default")
		if err != nil {
			t.Fatalf("resolvePingModelInfo: %v", err)
		}
		if info.Provider != "anthropic" {
			t.Errorf("provider = %q, want anthropic", info.Provider)
		}
		if explicit {
			t.Error("no --url and no configured base URL means the base URL is not explicit")
		}
	})

	t.Run("--url marks the base URL explicit", func(t *testing.T) {
		flagURL = "https://proxy.example"
		t.Cleanup(func() { flagURL = "" })
		cfg := config.Config{Roles: map[string]config.RoleConfig{
			"default": {Model: "gpt-5", Provider: "openai"},
		}}
		_, baseURL, explicit, err := resolvePingModelInfo(cfg, "default")
		if err != nil {
			t.Fatalf("resolvePingModelInfo: %v", err)
		}
		if baseURL != "https://proxy.example" || !explicit {
			t.Errorf("got (%q, %v), want (\"https://proxy.example\", true)", baseURL, explicit)
		}
	})

	t.Run("an unresolvable role is an error", func(t *testing.T) {
		cfg := config.Config{Roles: map[string]config.RoleConfig{}}
		if _, _, _, err := resolvePingModelInfo(cfg, "nope"); err == nil {
			t.Fatal("expected an error for a role with no model")
		}
	})
}

// ---------------------------------------------------------------------------
// reportHTTPVerdict's per-status helpers
// ---------------------------------------------------------------------------

func TestPingVerdictHelpers(t *testing.T) {
	t.Parallel()

	t.Run("reachable resolves an openai alias", func(t *testing.T) {
		t.Parallel()
		target := pingVerdictTarget("openai")
		target.info.Model = "gpt-5"
		resp := &http.Response{
			Status: "200 OK",
			Body:   io.NopCloser(strings.NewReader(`{"data":[{"id":"gpt-5.1"}]}`)),
		}
		w, read := captureWriter(t)
		if !target.verdictReachable(w, resp) {
			t.Fatal("a 2xx must count as reachable")
		}
		if target.info.Model != "gpt-5.1" {
			t.Errorf("model = %q, want the resolved alias gpt-5.1", target.info.Model)
		}
		if out := read(); !strings.Contains(out, "Endpoint reachable") {
			t.Errorf("unexpected output:\n%s", out)
		}
	})

	t.Run("reachable leaves non-openai models alone", func(t *testing.T) {
		t.Parallel()
		target := pingVerdictTarget("anthropic")
		w, _ := captureWriter(t)
		resp := &http.Response{Status: "200 OK", Body: io.NopCloser(strings.NewReader(`{"data":[{"id":"x"}]}`))}
		if !target.verdictReachable(w, resp) {
			t.Fatal("a 2xx must count as reachable")
		}
		if target.info.Model != "test-model" {
			t.Errorf("model = %q, want it untouched", target.info.Model)
		}
	})

	t.Run("unauthorized", func(t *testing.T) {
		t.Parallel()
		target := pingVerdictTarget("openai")
		w, read := captureWriter(t)
		if target.verdictUnauthorized(w, 401, false) {
			t.Error("401 without a custom azure endpoint must stop the run")
		}
		if out := read(); !strings.Contains(out, "Authentication failed") {
			t.Errorf("unexpected output:\n%s", out)
		}

		w2, read2 := captureWriter(t)
		if !target.verdictUnauthorized(w2, 403, true) {
			t.Error("a custom azure endpoint is allowed to reject the probe")
		}
		if out := read2(); !strings.Contains(out, "continuing with model ping") {
			t.Errorf("unexpected output:\n%s", out)
		}
	})

	t.Run("not found", func(t *testing.T) {
		t.Parallel()
		w, read := captureWriter(t)
		if pingVerdictTarget("openai").verdictNotFound(w, 404) {
			t.Error("404 stops a non-azure run")
		}
		if out := read(); !strings.Contains(out, "Endpoint not found") || !strings.Contains(out, "https://example.com") {
			t.Errorf("expected the base URL to be named, got:\n%s", out)
		}

		w2, read2 := captureWriter(t)
		if !pingVerdictTarget("azure").verdictNotFound(w2, 404) {
			t.Error("404 continues for azure")
		}
		if out := read2(); !strings.Contains(out, "continuing with model ping") {
			t.Errorf("unexpected output:\n%s", out)
		}
	})

	t.Run("unprocessable", func(t *testing.T) {
		t.Parallel()
		target := pingVerdictTarget("azure")
		resp := &http.Response{Status: "422 Unprocessable Entity"}
		w, read := captureWriter(t)
		if target.verdictUnprocessable(w, resp, false) {
			t.Error("422 without a custom endpoint is a dead end")
		}
		if out := read(); !strings.Contains(out, "Unexpected status") {
			t.Errorf("unexpected output:\n%s", out)
		}

		w2, read2 := captureWriter(t)
		if !target.verdictUnprocessable(w2, resp, true) {
			t.Error("422 on a custom proxy continues to the model ping")
		}
		if out := read2(); !strings.Contains(out, "structured POST") {
			t.Errorf("unexpected output:\n%s", out)
		}
	})
}

// ---------------------------------------------------------------------------
// model list helpers
// ---------------------------------------------------------------------------

func TestSelectModelListProviders(t *testing.T) {
	origURL := flagURL
	t.Cleanup(func() { flagURL = origURL })

	tests := []struct {
		name        string
		args        []string
		keys        map[string]string
		baseURLs    map[string]string
		flagURL     string
		azureEnv    string
		want        []string
		wantErr     string
		wantPrinted string
	}{
		{
			name: "a named provider is queried alone",
			args: []string{"anthropic"}, want: []string{"anthropic"},
		},
		{
			name: "the name is case-insensitive",
			args: []string{"OpenAI"}, want: []string{"openai"},
		},
		{
			name: "azure prints its catalog and queries nothing",
			args: []string{"azure"}, want: nil, wantPrinted: "azure (",
		},
		{
			name: "an unknown name is an error",
			args: []string{"nope"}, wantErr: `unknown provider "nope"`,
		},
		{
			name: "ollama is always queried, keys or not",
			keys: map[string]string{}, want: []string{"ollama"},
		},
		{
			name: "a key opts a provider in",
			keys: map[string]string{"anthropic": "sk-a"},
			want: []string{"anthropic", "ollama"},
		},
		{
			name:     "a base URL opts a provider in without a key",
			baseURLs: map[string]string{"mistral": "https://m.example"},
			want:     []string{"mistral", "ollama"},
		},
		{
			name:    "--url opts every provider in",
			flagURL: "https://proxy.example",
			want:    []string{"anthropic", "openai", "gemini", "mistral", "xai", "ollama", "openrouter", "agentgateway"},
		},
		{
			name: "a configured azure prints alongside the queried providers",
			keys: map[string]string{"azure": "sk-az"}, want: []string{"ollama"},
			wantPrinted: "azure (",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flagURL = tt.flagURL
			t.Setenv("AZURE_OPENAI_ENDPOINT", tt.azureEnv)
			keys, baseURLs := tt.keys, tt.baseURLs
			if keys == nil {
				keys = map[string]string{}
			}
			if baseURLs == nil {
				baseURLs = map[string]string{}
			}

			var out strings.Builder
			got, err := selectModelListProviders(&out, tt.args, keys, baseURLs)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want it to mention %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("selectModelListProviders: %v", err)
			}
			if strings.Join(got, ",") != strings.Join(tt.want, ",") {
				t.Errorf("providers = %v, want %v", got, tt.want)
			}
			if tt.wantPrinted != "" && !strings.Contains(out.String(), tt.wantPrinted) {
				t.Errorf("expected %q in the printed output, got:\n%s", tt.wantPrinted, out.String())
			}
			if tt.wantPrinted == "" && out.String() != "" {
				t.Errorf("expected nothing printed, got:\n%s", out.String())
			}
		})
	}
}

// With nothing configured at all the command has to say so rather than exit
// quietly — but a lone Azure setup is a configuration, so it does not error.
func TestSelectModelListProvidersNothingConfigured(t *testing.T) {
	origURL := flagURL
	t.Cleanup(func() { flagURL = origURL })
	flagURL = ""
	t.Setenv("AZURE_OPENAI_ENDPOINT", "")

	// allProviders always contributes ollama, so the empty case is reached by
	// asking about a provider set that excludes it.
	origAll := allProviders
	t.Cleanup(func() { allProviders = origAll })
	allProviders = []string{"anthropic"}

	var out strings.Builder
	if _, err := selectModelListProviders(&out, nil, map[string]string{}, map[string]string{}); err == nil ||
		!strings.Contains(err.Error(), "no providers configured") {
		t.Fatalf("err = %v, want a no-providers-configured error", err)
	}

	t.Setenv("AZURE_OPENAI_ENDPOINT", "https://unit.openai.azure.com")
	var out2 strings.Builder
	got, err := selectModelListProviders(&out2, nil, map[string]string{}, map[string]string{})
	if err != nil {
		t.Fatalf("a configured azure must not be an error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("providers = %v, want none to query", got)
	}
	if !strings.Contains(out2.String(), "azure (") {
		t.Errorf("expected the azure catalog, got:\n%s", out2.String())
	}
}

func TestModelListBaseURL(t *testing.T) {
	origURL := flagURL
	t.Cleanup(func() { flagURL = origURL })

	tests := []struct {
		name     string
		provider string
		flagURL  string
		baseURLs map[string]string
		want     string
	}{
		{name: "--url wins", provider: "openai", flagURL: "https://flag.example",
			baseURLs: map[string]string{"openai": "https://cfg.example"}, want: "https://flag.example"},
		{name: "config is next", provider: "openai",
			baseURLs: map[string]string{"openai": "https://cfg.example"}, want: "https://cfg.example"},
		{name: "ollama falls back to localhost", provider: "ollama", want: "http://localhost:11434"},
		{name: "--url beats the ollama default", provider: "ollama", flagURL: "https://flag.example",
			want: "https://flag.example"},
		{name: "agentgateway falls back to localhost", provider: "agentgateway", want: "http://localhost:4000"},
		{name: "--url beats the agentgateway default", provider: "agentgateway", flagURL: "https://flag.example",
			want: "https://flag.example"},
		{name: "everyone else gets nothing", provider: "anthropic", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flagURL = tt.flagURL
			baseURLs := tt.baseURLs
			if baseURLs == nil {
				baseURLs = map[string]string{}
			}
			if got := modelListBaseURL(tt.provider, baseURLs); got != tt.want {
				t.Errorf("modelListBaseURL(%q) = %q, want %q", tt.provider, got, tt.want)
			}
		})
	}
}

func TestPrintProviderModels(t *testing.T) {
	out := captureStdout(t, func() {
		printProviderModels("openai", []provider.ModelInfo{
			{ID: "gpt-z"},
			{ID: "gpt-a", OwnedBy: "openai"},
		})
	})
	if !strings.Contains(out, "openai (2 models):") {
		t.Errorf("expected the count header, got:\n%s", out)
	}
	// Rendered as a markdown table with a header and separator.
	if !strings.Contains(out, "| Model | Release | Price | Notes |") {
		t.Errorf("expected the markdown table header, got:\n%s", out)
	}
	if !strings.Contains(out, "|---|---|---|---|") {
		t.Errorf("expected the markdown separator row, got:\n%s", out)
	}
	// Both models present, and gpt-a shows its owner in the Notes column.
	if !strings.Contains(out, "| gpt-a |") || !strings.Contains(out, "| gpt-z |") {
		t.Errorf("expected both model rows, got:\n%s", out)
	}
	if !strings.Contains(out, "| gpt-a |") || !strings.Contains(out, "openai |") {
		t.Errorf("expected the owner in the Notes column for gpt-a, got:\n%s", out)
	}
}

// ---------------------------------------------------------------------------
// memory recent helpers
// ---------------------------------------------------------------------------

func obsOfType(typ string) *memory.Observation {
	return &memory.Observation{Type: memory.ObservationType(typ), Title: "t " + typ, CreatedAt: time.Now()}
}

func TestLimitRecentObservations(t *testing.T) {
	t.Parallel()
	mixed := []*memory.Observation{
		obsOfType("bugfix"), obsOfType("decision"), obsOfType("bugfix"), obsOfType("feature"),
	}
	tests := []struct {
		name     string
		in       []*memory.Observation
		obsType  string
		limit    int
		wantLen  int
		wantType string
	}{
		{name: "no filter truncates to the limit", in: mixed, limit: 2, wantLen: 2},
		{name: "no filter keeps a short list whole", in: mixed, limit: 10, wantLen: 4},
		{name: "no filter at the exact limit", in: mixed, limit: 4, wantLen: 4},
		{name: "filter keeps only the type", in: mixed, obsType: "bugfix", limit: 10, wantLen: 2, wantType: "bugfix"},
		{name: "filter stops at the limit", in: mixed, obsType: "bugfix", limit: 1, wantLen: 1, wantType: "bugfix"},
		{name: "filter with no hits yields nothing", in: mixed, obsType: "refactor", limit: 10, wantLen: 0},
		{name: "empty input", in: nil, limit: 5, wantLen: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := limitRecentObservations(tt.in, tt.obsType, tt.limit)
			if len(got) != tt.wantLen {
				t.Fatalf("len = %d, want %d", len(got), tt.wantLen)
			}
			for _, obs := range got {
				if tt.wantType != "" && string(obs.Type) != tt.wantType {
					t.Errorf("type = %q, want %q", obs.Type, tt.wantType)
				}
			}
		})
	}
}

func TestPrintRecentReport(t *testing.T) {
	t.Run("empty names the project", func(t *testing.T) {
		out := captureStdout(t, func() { printRecentReport("/tmp/proj", nil, nil) })
		if !strings.Contains(out, "No observations found.") || !strings.Contains(out, "Project: /tmp/proj") {
			t.Errorf("unexpected output:\n%s", out)
		}
	})

	t.Run("observations only", func(t *testing.T) {
		out := captureStdout(t, func() {
			printRecentReport("/tmp/proj", []*memory.Observation{obsOfType("bugfix")}, nil)
		})
		if !strings.Contains(out, "Observations (1)") || !strings.Contains(out, "t bugfix") {
			t.Errorf("unexpected output:\n%s", out)
		}
		if strings.Contains(out, "Session Summaries") {
			t.Errorf("no summaries means no summary section:\n%s", out)
		}
	})

	t.Run("summaries only, with a long request truncated", func(t *testing.T) {
		long := strings.Repeat("q", 200)
		out := captureStdout(t, func() {
			printRecentReport("/tmp/proj", nil, []*memory.SessionSummary{
				{Request: long, CreatedAt: time.Now()},
			})
		})
		if !strings.Contains(out, "Session Summaries (1)") {
			t.Errorf("unexpected output:\n%s", out)
		}
		if !strings.Contains(out, strings.Repeat("q", 80)+"...") {
			t.Errorf("expected the request truncated at 80 chars, got:\n%s", out)
		}
		if strings.Contains(out, "Observations (") {
			t.Errorf("no observations means no observation section:\n%s", out)
		}
	})
}

// ---------------------------------------------------------------------------
// memory mine helpers
// ---------------------------------------------------------------------------

func TestSkipMineDir(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		want bool
	}{
		{"node_modules", true},
		{"vendor", true},
		{".git", true},
		{".hidden", true},
		{".", false},
		{"internal", false},
		{"src", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := skipMineDir(tt.name); got != tt.want {
				t.Errorf("skipMineDir(%q) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}

func TestMineFileEligible(t *testing.T) {
	dir := t.TempDir()

	write := func(name, content string) string {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		return p
	}
	entryFor := func(path string) os.DirEntry {
		t.Helper()
		entries, err := os.ReadDir(filepath.Dir(path))
		if err != nil {
			t.Fatalf("ReadDir: %v", err)
		}
		for _, e := range entries {
			if e.Name() == filepath.Base(path) {
				return e
			}
		}
		t.Fatalf("no dir entry for %s", path)
		return nil
	}

	goFile := write("a.go", "package main\n")
	blank := write("blank.go", "   \n\t\n")
	binary := write("x.bin", "data")
	jsonl := write("chat.jsonl", "{}\n")
	big := write("big.go", strings.Repeat("x", 512*1024+1))

	tests := []struct {
		name   string
		path   string
		convos bool
		want   bool
	}{
		{name: "supported source file", path: goFile, want: true},
		{name: "unsupported extension", path: binary, want: false},
		{name: "blank file is skipped", path: blank, want: false},
		{name: "oversized file is skipped", path: big, want: false},
		{name: "convos takes jsonl", path: jsonl, convos: true, want: true},
		{name: "convos ignores source files", path: goFile, convos: true, want: false},
		// A convos run applies none of the size or content checks.
		{name: "convos does not check size", path: write("big.md", strings.Repeat("y", 512*1024+1)), convos: true, want: true},
		{name: "convos does not check for content", path: write("empty.txt", ""), convos: true, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := mineFileEligible(tt.path, entryFor(tt.path), tt.convos); got != tt.want {
				t.Errorf("mineFileEligible(%q, convos=%v) = %v, want %v",
					filepath.Base(tt.path), tt.convos, got, tt.want)
			}
		})
	}
}

func TestPrintMineFileList(t *testing.T) {
	out := captureStdout(t, func() { printMineFileList([]string{"a.go", "b.go"}) })
	if !strings.Contains(out, "Files (2 total):") {
		t.Errorf("expected the total, got:\n%s", out)
	}
	if !strings.Contains(out, "[ 1] a.go") || !strings.Contains(out, "[ 2] b.go") {
		t.Errorf("expected a 1-based numbered list, got:\n%s", out)
	}
}

func TestMinePalaceConfig(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	cfg := minePalaceConfig("/tmp/db.sqlite", "/tmp/model")
	if cfg.DBPath != "/tmp/db.sqlite" || cfg.ModelPath != "/tmp/model" {
		t.Errorf("paths not carried through: %+v", cfg)
	}
	// With no user config on disk the palace defaults have to survive intact.
	def := palace.DefaultConfig()
	if cfg.UseOllama != def.UseOllama || cfg.OllamaURL != def.OllamaURL || cfg.OllamaModel != def.OllamaModel {
		t.Errorf("embedder defaults were altered: %+v", cfg)
	}
}

func TestPrintMineBanner(t *testing.T) {
	t.Run("ollama embedder names the daemon", func(t *testing.T) {
		cfg := palace.PalaceConfig{UseOllama: true, OllamaModel: "embed-m", OllamaURL: "http://d:1"}
		out := captureStdout(t, func() { printMineBanner(cfg, "/db", "/model", "wing-x") })
		for _, want := range []string{"Palace DB: /db", "ollama embed-m (http://d:1)", "Wing:      wing-x"} {
			if !strings.Contains(out, want) {
				t.Errorf("expected %q in:\n%s", want, out)
			}
		}
	})

	t.Run("local embedder names the model path", func(t *testing.T) {
		out := captureStdout(t, func() {
			printMineBanner(palace.PalaceConfig{UseOllama: false}, "/db", "/model", "wing-x")
		})
		if !strings.Contains(out, "in-process /model") {
			t.Errorf("expected the in-process line, got:\n%s", out)
		}
	})
}

func TestMinePalaceOptions(t *testing.T) {
	t.Parallel()
	// Both variants add exactly one embedder option to the two path options.
	if got := len(minePalaceOptions(palace.PalaceConfig{UseOllama: true}, "/db", "/model")); got != 3 {
		t.Errorf("ollama options = %d, want 3", got)
	}
	if got := len(minePalaceOptions(palace.PalaceConfig{UseOllama: false}, "/db", "/model")); got != 3 {
		t.Errorf("local options = %d, want 3", got)
	}

	// The options have to actually configure the embedder they name.
	ollamaCfg := palace.DefaultConfig()
	for _, opt := range minePalaceOptions(palace.PalaceConfig{
		UseOllama: true, OllamaURL: "http://d:1", OllamaModel: "embed-m",
	}, "/db", "/model") {
		opt(&ollamaCfg)
	}
	if !ollamaCfg.UseOllama || ollamaCfg.OllamaURL != "http://d:1" || ollamaCfg.OllamaModel != "embed-m" {
		t.Errorf("ollama options did not apply: %+v", ollamaCfg)
	}

	localCfg := palace.DefaultConfig()
	for _, opt := range minePalaceOptions(palace.PalaceConfig{UseOllama: false}, "/db", "/model") {
		opt(&localCfg)
	}
	if localCfg.UseOllama {
		t.Error("local options must turn the ollama embedder off")
	}
}

// The auto-init step has to fail loudly when it cannot create the directories
// it needs — a silent failure here leaves the run indexing into nothing.
func TestEnsureMineModelDirectoryFailure(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	err := ensureMineModel(filepath.Join(blocker, "sub", "palace.db"), filepath.Join(dir, "model", "m.onnx"))
	if err == nil || !strings.Contains(err.Error(), "creating palace directory") {
		t.Fatalf("err = %v, want a palace-directory failure", err)
	}
}
