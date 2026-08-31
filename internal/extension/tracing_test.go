package extension

import (
	"context"
	"errors"
	"testing"

	sdkotel "go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"

	piotel "github.com/dimetron/pi-go/internal/otel"
)

// newSpanRecorder installs an in-memory tracer provider and returns the
// recorder. piotel.Tracer is warmed up first: its lazy init sets the global
// provider on first use and would otherwise replace the recorder.
func newSpanRecorder(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	_ = piotel.Tracer("warmup")
	rec := tracetest.NewSpanRecorder()
	prev := sdkotel.GetTracerProvider()
	sdkotel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec)))
	t.Cleanup(func() { sdkotel.SetTracerProvider(prev) })
	return rec
}

// endedSpan returns the single ended span with the given name.
func endedSpan(t *testing.T, rec *tracetest.SpanRecorder, name string) sdktrace.ReadOnlySpan {
	t.Helper()
	var found sdktrace.ReadOnlySpan
	for _, s := range rec.Ended() {
		if s.Name() == name {
			if found != nil {
				t.Fatalf("more than one ended span named %q", name)
			}
			found = s
		}
	}
	if found == nil {
		names := make([]string, 0, len(rec.Ended()))
		for _, s := range rec.Ended() {
			names = append(names, s.Name())
		}
		t.Fatalf("no ended span named %q; ended spans: %v", name, names)
	}
	return found
}

func attrValue(t *testing.T, span sdktrace.ReadOnlySpan, key string) attribute.Value {
	t.Helper()
	for _, kv := range span.Attributes() {
		if string(kv.Key) == key {
			return kv.Value
		}
	}
	t.Fatalf("span %q has no attribute %q (attributes: %v)", span.Name(), key, span.Attributes())
	return attribute.Value{}
}

func hasAttr(span sdktrace.ReadOnlySpan, key string) bool {
	for _, kv := range span.Attributes() {
		if string(kv.Key) == key {
			return true
		}
	}
	return false
}

// TestLLMSpanIsExportedWithUsage is the regression test for the span never
// reaching the exporter: the before-callback's context cannot be handed back to
// ADK, so the span has to be carried out of band. Without that, no llm span was
// ever ended and the token counts had nowhere to land.
func TestLLMSpanIsExportedWithUsage(t *testing.T) {
	rec := newSpanRecorder(t)

	before, after := BuildLLMTracingCallbacks("openai")
	ctx := &mockReadonlyContext{Context: context.Background(), invocationID: "inv-1"}

	if _, err := before[0](ctx, &model.LLMRequest{Model: "gpt-5.6-sol"}); err != nil {
		t.Fatalf("before: %v", err)
	}
	resp := &model.LLMResponse{
		TurnComplete: true,
		ModelVersion: "gpt-5.6-sol-2026-01-01",
		FinishReason: genai.FinishReasonStop,
		UsageMetadata: &genai.GenerateContentResponseUsageMetadata{
			PromptTokenCount:        1200,
			CandidatesTokenCount:    340,
			CachedContentTokenCount: 1024,
			ThoughtsTokenCount:      88,
			TotalTokenCount:         1540,
		},
	}
	if _, err := after[0](ctx, resp, nil); err != nil {
		t.Fatalf("after: %v", err)
	}

	span := endedSpan(t, rec, "chat gpt-5.6-sol")

	wantInts := map[string]int64{
		"gen_ai.usage.input_tokens":        1200,
		"gen_ai.usage.output_tokens":       340,
		"gen_ai.usage.cached_input_tokens": 1024,
		"gen_ai.usage.reasoning_tokens":    88,
		"gen_ai.usage.total_tokens":        1540,
	}
	for key, want := range wantInts {
		if got := attrValue(t, span, key).AsInt64(); got != want {
			t.Errorf("%s = %d, want %d", key, got, want)
		}
	}

	wantStrings := map[string]string{
		"gen_ai.operation.name": "chat",
		"gen_ai.provider.name":  "openai",
		"gen_ai.request.model":  "gpt-5.6-sol",
		"gen_ai.response.model": "gpt-5.6-sol-2026-01-01",
	}
	for key, want := range wantStrings {
		if got := attrValue(t, span, key).AsString(); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
	if got := attrValue(t, span, "gen_ai.response.finish_reasons").AsStringSlice(); len(got) != 1 || got[0] != "STOP" {
		t.Errorf("gen_ai.response.finish_reasons = %v, want [STOP]", got)
	}
}

// TestLLMSpanSurvivesPartialResponses covers streaming: ADK reports every chunk
// through the after-callback, but usage only rides on the final one.
func TestLLMSpanSurvivesPartialResponses(t *testing.T) {
	rec := newSpanRecorder(t)

	before, after := BuildLLMTracingCallbacks("anthropic")
	ctx := &mockReadonlyContext{Context: context.Background(), invocationID: "inv-stream"}

	if _, err := before[0](ctx, &model.LLMRequest{Model: "claude-sonnet-5"}); err != nil {
		t.Fatalf("before: %v", err)
	}
	for range 3 {
		if _, err := after[0](ctx, &model.LLMResponse{Partial: true}, nil); err != nil {
			t.Fatalf("after(partial): %v", err)
		}
	}
	if len(rec.Ended()) != 0 {
		t.Fatalf("partial responses must not end the span, got %d ended", len(rec.Ended()))
	}

	final := &model.LLMResponse{
		TurnComplete:  true,
		UsageMetadata: &genai.GenerateContentResponseUsageMetadata{PromptTokenCount: 10, CandidatesTokenCount: 5},
	}
	if _, err := after[0](ctx, final, nil); err != nil {
		t.Fatalf("after(final): %v", err)
	}

	span := endedSpan(t, rec, "chat claude-sonnet-5")
	if got := attrValue(t, span, "gen_ai.usage.input_tokens").AsInt64(); got != 10 {
		t.Errorf("input tokens = %d, want 10", got)
	}
	if got := attrValue(t, span, "gen_ai.provider.name").AsString(); got != "anthropic" {
		t.Errorf("provider = %q, want anthropic", got)
	}
}

// TestLLMCallbacksLeaveParentSpanOpen pins the other half of the old bug: the
// after-callback used to read the span back out of the context, which found the
// enclosing agent span and ended that instead.
func TestLLMCallbacksLeaveParentSpanOpen(t *testing.T) {
	rec := newSpanRecorder(t)

	parentCtx, parent := piotel.Tracer("pi-go").Start(context.Background(), "agent.prompt")
	before, after := BuildLLMTracingCallbacks("openai")
	ctx := &mockReadonlyContext{Context: parentCtx, invocationID: "inv-parent"}

	if _, err := before[0](ctx, &model.LLMRequest{Model: "gpt-5"}); err != nil {
		t.Fatalf("before: %v", err)
	}
	if _, err := after[0](ctx, &model.LLMResponse{TurnComplete: true}, nil); err != nil {
		t.Fatalf("after: %v", err)
	}

	if !parent.IsRecording() {
		t.Error("the parent span was ended by the LLM callbacks")
	}
	llmSpan := endedSpan(t, rec, "chat gpt-5")
	if llmSpan.Parent().SpanID() != parent.SpanContext().SpanID() {
		t.Error("llm span is not a child of the enclosing agent span")
	}
	parent.End()
}

// TestLLMSpanRecordsError checks the failure path still closes the span.
func TestLLMSpanRecordsError(t *testing.T) {
	rec := newSpanRecorder(t)

	before, after := BuildLLMTracingCallbacks("ollama")
	ctx := &mockReadonlyContext{Context: context.Background(), invocationID: "inv-err"}

	if _, err := before[0](ctx, &model.LLMRequest{Model: "glm-5.2:cloud"}); err != nil {
		t.Fatalf("before: %v", err)
	}
	if _, err := after[0](ctx, nil, errors.New("upstream exploded")); err != nil {
		t.Fatalf("after: %v", err)
	}

	span := endedSpan(t, rec, "chat glm-5.2:cloud")
	if span.Status().Code.String() != "Error" {
		t.Errorf("status = %v, want Error", span.Status().Code)
	}
	if len(span.Events()) == 0 {
		t.Error("expected the error to be recorded as a span event")
	}
	// A provider with no semconv enum entry passes through unchanged.
	if got := attrValue(t, span, "gen_ai.provider.name").AsString(); got != "ollama" {
		t.Errorf("provider = %q, want ollama", got)
	}
}

// TestLLMSpansPairPerCall checks that sequential model calls in one invocation
// each get their own span rather than overwriting one another.
func TestLLMSpansPairPerCall(t *testing.T) {
	rec := newSpanRecorder(t)

	before, after := BuildLLMTracingCallbacks("openai")
	ctx := &mockReadonlyContext{Context: context.Background(), invocationID: "inv-loop"}

	for _, name := range []string{"gpt-5", "gpt-5-mini"} {
		if _, err := before[0](ctx, &model.LLMRequest{Model: name}); err != nil {
			t.Fatalf("before(%s): %v", name, err)
		}
		if _, err := after[0](ctx, &model.LLMResponse{TurnComplete: true}, nil); err != nil {
			t.Fatalf("after(%s): %v", name, err)
		}
	}

	endedSpan(t, rec, "chat gpt-5")
	endedSpan(t, rec, "chat gpt-5-mini")
	if len(rec.Ended()) != 2 {
		t.Errorf("ended %d spans, want 2", len(rec.Ended()))
	}
}

// TestUnmatchedAfterCallbackIsInert guards the registry's miss path: an
// after-callback with no matching before-callback must not end another span.
func TestUnmatchedAfterCallbackIsInert(t *testing.T) {
	rec := newSpanRecorder(t)

	parentCtx, parent := piotel.Tracer("pi-go").Start(context.Background(), "agent.prompt")
	_, after := BuildLLMTracingCallbacks("openai")
	ctx := &mockReadonlyContext{Context: parentCtx, invocationID: "inv-orphan"}

	if _, err := after[0](ctx, &model.LLMResponse{TurnComplete: true}, nil); err != nil {
		t.Fatalf("after: %v", err)
	}
	if !parent.IsRecording() {
		t.Error("an unmatched after-callback ended the enclosing span")
	}
	if len(rec.Ended()) != 0 {
		t.Errorf("expected no ended spans, got %d", len(rec.Ended()))
	}
	parent.End()
}

// TestToolSpansAreExportedPerCall covers the same fix on the tool callbacks,
// including two calls in flight at once — they are keyed by function call id,
// so neither can close the other's span.
func TestToolSpansAreExportedPerCall(t *testing.T) {
	rec := newSpanRecorder(t)

	before, after := BuildTracingCallbacks()
	readCtx := &mockToolCtx{Context: context.Background(), funcCallID: "call-read"}
	bashCtx := &mockToolCtx{Context: context.Background(), funcCallID: "call-bash"}
	readTool := mockTool{nameVal: "read"}
	bashTool := mockTool{nameVal: "bash"}

	// Interleaved: both start before either finishes.
	if _, err := before[0](readCtx, readTool, map[string]any{"path": "/x"}); err != nil {
		t.Fatalf("before(read): %v", err)
	}
	if _, err := before[0](bashCtx, bashTool, map[string]any{"cmd": "ls"}); err != nil {
		t.Fatalf("before(bash): %v", err)
	}
	if _, err := after[0](bashCtx, bashTool, nil, nil, errors.New("exit 1")); err != nil {
		t.Fatalf("after(bash): %v", err)
	}
	if _, err := after[0](readCtx, readTool, nil, map[string]any{"content": "ok"}, nil); err != nil {
		t.Fatalf("after(read): %v", err)
	}

	readSpan := endedSpan(t, rec, "execute_tool read")
	if got := attrValue(t, readSpan, "gen_ai.tool.name").AsString(); got != "read" {
		t.Errorf("gen_ai.tool.name = %q, want read", got)
	}
	if !attrValue(t, readSpan, "tool.success").AsBool() {
		t.Error("read tool should be recorded as successful")
	}

	bashSpan := endedSpan(t, rec, "execute_tool bash")
	if attrValue(t, bashSpan, "tool.success").AsBool() {
		t.Error("bash tool should be recorded as failed")
	}
	if bashSpan.Status().Code.String() != "Error" {
		t.Errorf("bash status = %v, want Error", bashSpan.Status().Code)
	}
}

func TestGenAIProviderAttr(t *testing.T) {
	tests := map[string]string{
		"openai":       "openai",
		"anthropic":    "anthropic",
		"gemini":       "gcp.gemini",
		"mistral":      "mistral_ai",
		"xai":          "x_ai",
		"azure":        "azure.ai.openai",
		"opencode":     "opencode",
		"agentgateway": "agentgateway",
		"":             "",
	}
	for in, want := range tests {
		if got := genAIProviderAttr(in).Value.AsString(); got != want {
			t.Errorf("genAIProviderAttr(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSetLLMResponseAttributesSkipsZeroCounts(t *testing.T) {
	rec := newSpanRecorder(t)
	_, span := piotel.Tracer("pi-go").Start(context.Background(), "probe")

	setLLMResponseAttributes(span, &model.LLMResponse{
		UsageMetadata: &genai.GenerateContentResponseUsageMetadata{PromptTokenCount: 7},
	})
	span.End()

	got := endedSpan(t, rec, "probe")
	if v := attrValue(t, got, "gen_ai.usage.input_tokens").AsInt64(); v != 7 {
		t.Errorf("input tokens = %d, want 7", v)
	}
	// A provider that reports no output tokens should leave the attribute off
	// entirely rather than claim a measured zero.
	if hasAttr(got, "gen_ai.usage.output_tokens") {
		t.Error("zero output tokens should not be reported")
	}
}
