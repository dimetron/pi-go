// Package extension provides hooks (before/after tool call), skill loading,
// and MCP tool integration for the pi-go agent.
package extension

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	semconv "go.opentelemetry.io/otel/semconv/v1.37.0"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/tool"

	"github.com/dimetron/pi-go/internal/otel"
	"github.com/dimetron/pi-go/internal/procs"
)

// HookConfig defines a shell command hook that runs before or after tool calls.
type HookConfig struct {
	// Event is "before_tool" or "after_tool".
	Event string `json:"event"`
	// Command is the shell command to execute.
	Command string `json:"command"`
	// Tools optionally restricts this hook to specific tool names.
	// If empty, the hook fires for all tools.
	Tools []string `json:"tools,omitempty"`
	// Timeout in seconds for hook execution. Default: 10.
	Timeout int `json:"timeout,omitempty"`
}

// matchesTool returns true if the hook should fire for the given tool name.
func (h HookConfig) matchesTool(name string) bool {
	if len(h.Tools) == 0 {
		return true
	}
	return slices.Contains(h.Tools, name)
}

func (h HookConfig) timeout() time.Duration {
	if h.Timeout > 0 {
		return time.Duration(h.Timeout) * time.Second
	}
	return 10 * time.Second
}

// ToolCallReporter is the interface that adapter.Stream exposes for reporting
// tool call lifecycle events to the ACP peer. This allows BuildToolCallCallbacks
// to bridge ADK Before/AfterToolCallbacks → ACP StartToolCall/UpdateToolCall.
type ToolCallReporter interface {
	OnToolStart(ctx context.Context, name string, args map[string]any) (string, error)
	OnToolEnd(ctx context.Context, callID string, args map[string]any, result any, runErr error) error
}

// BuildToolCallCallbacks creates ADK before/after tool callbacks that report
// tool calls to the ACP peer via s. The BeforeToolCallback emits StartToolCall;
// the AfterToolCallback emits UpdateToolCall with the result or error.
//
// Correlation: ctx.FunctionCallID() is the ADK-assigned unique identifier for
// each tool invocation. It is the same value in both the before and after
// callback, so it is used as the key to carry the ACP call ID (returned by
// OnToolStart) across the two callbacks. Without this, the result of every
// tool call would be silently dropped because OnToolEnd would receive an empty
// call ID and treat it as a no-op.
func BuildToolCallCallbacks(s ToolCallReporter) ([]llmagent.BeforeToolCallback, []llmagent.AfterToolCallback) {
	var mu sync.Mutex
	pending := map[string]string{} // ADK FunctionCallID → ACP call ID

	beforeCB := func(ctx agent.Context, t tool.Tool, args map[string]any) (map[string]any, error) {
		acpID, err := s.OnToolStart(ctx, t.Name(), args)
		if ctx != nil && acpID != "" {
			if fid := ctx.FunctionCallID(); fid != "" {
				mu.Lock()
				pending[fid] = acpID
				mu.Unlock()
			}
		}
		return nil, err
	}
	afterCB := func(ctx agent.Context, t tool.Tool, args, result map[string]any, runErr error) (map[string]any, error) {
		var acpID string
		if ctx != nil {
			if fid := ctx.FunctionCallID(); fid != "" {
				mu.Lock()
				acpID = pending[fid]
				delete(pending, fid)
				mu.Unlock()
			}
		}
		_ = s.OnToolEnd(context.Background(), acpID, args, result, runErr)
		return result, nil
	}
	return []llmagent.BeforeToolCallback{beforeCB}, []llmagent.AfterToolCallback{afterCB}
}

// hookLog is the sink for hook-failure diagnostics. It is nil by default,
// which means failures go to the standard logger — the right destination for
// the one-shot CLI and the ACP server, whose stderr is an ordinary stream.
//
// The TUI is the exception: it owns the alternate screen, and a line written
// to stderr is painted over the UI without the renderer knowing those cells
// were dirtied, so the damage persists until a full redraw. It installs a sink
// pointing at the session log file instead. Same late-binding idiom as
// auth.SetDebugLogger.
var hookLog atomic.Pointer[func(string)]

// SetHookLogger installs a sink for hook-failure diagnostics. Passing nil
// restores the default (standard logger, i.e. stderr). Hooks run from callback
// goroutines, so implementations must be goroutine-safe.
func SetHookLogger(fn func(string)) {
	if fn == nil {
		hookLog.Store(nil)
		return
	}
	hookLog.Store(&fn)
}

// hookLogf reports a hook failure to the installed sink, or to the standard
// logger when none is installed.
func hookLogf(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	if p := hookLog.Load(); p != nil && *p != nil {
		(*p)(msg)
		return
	}
	log.Print(msg)
}

// BuildBeforeToolCallbacks converts HookConfigs with event "before_tool" into
// ADK BeforeToolCallback functions.
func BuildBeforeToolCallbacks(hooks []HookConfig) []llmagent.BeforeToolCallback {
	var cbs []llmagent.BeforeToolCallback
	for _, h := range hooks {
		if h.Event != "before_tool" {
			continue
		}
		hook := h // capture
		cbs = append(cbs, func(ctx agent.Context, t tool.Tool, args map[string]any) (map[string]any, error) {
			if !hook.matchesTool(t.Name()) {
				return nil, nil
			}
			if err := runHookCommand(ctx, hook, t.Name(), args); err != nil {
				hookLogf("hook %q failed for tool %q: %v", hook.Command, t.Name(), err)
				// Non-fatal: log and continue.
			}
			return nil, nil
		})
	}
	return cbs
}

// BuildAfterToolCallbacks converts HookConfigs with event "after_tool" into
// ADK AfterToolCallback functions.
func BuildAfterToolCallbacks(hooks []HookConfig) []llmagent.AfterToolCallback {
	var cbs []llmagent.AfterToolCallback
	for _, h := range hooks {
		if h.Event != "after_tool" {
			continue
		}
		hook := h // capture
		cbs = append(cbs, func(ctx agent.Context, t tool.Tool, args, result map[string]any, err error) (map[string]any, error) {
			if !hook.matchesTool(t.Name()) {
				return result, nil
			}
			if hookErr := runHookCommand(ctx, hook, t.Name(), result); hookErr != nil {
				hookLogf("hook %q failed for tool %q: %v", hook.Command, t.Name(), hookErr)
			}
			return result, nil
		})
	}
	return cbs
}

// spanRegistry hands a span from a before-callback to its matching
// after-callback.
//
// ADK's callback signatures return no context, so the context returned by
// tracer.Start cannot reach the after-callback: reading the span back with
// trace.SpanFromContext there finds the *parent* span instead, ends that, and
// leaves the real span unended — and an unended span is never exported. The
// registry keys spans by an identifier both callbacks can see: the function
// call id for tools, the invocation id for model calls.
//
// Values are stacks because one invocation issues several model calls in
// sequence; push/pop keeps each paired with its own span.
type spanRegistry struct {
	mu    sync.Mutex
	spans map[string][]trace.Span
}

func newSpanRegistry() *spanRegistry {
	return &spanRegistry{spans: make(map[string][]trace.Span)}
}

func (r *spanRegistry) push(key string, span trace.Span) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.spans[key] = append(r.spans[key], span)
}

// pop removes and returns the most recent span for key, or nil when there is
// none — an after-callback firing without a matching before-callback must not
// end somebody else's span.
func (r *spanRegistry) pop(key string) trace.Span {
	r.mu.Lock()
	defer r.mu.Unlock()
	stack := r.spans[key]
	if len(stack) == 0 {
		return nil
	}
	span := stack[len(stack)-1]
	if len(stack) == 1 {
		delete(r.spans, key)
	} else {
		r.spans[key] = stack[:len(stack)-1]
	}
	return span
}

// BuildTracingCallbacks returns before/after tool callbacks that emit OTEL spans
// for every tool invocation. The parent span context is propagated from the
// context passed to the callback, so spans are linked to the agent's trace.
//
// Spans are carried between the two callbacks through a registry keyed by
// function call id, which is unique per tool call and so stays correct when
// the model runs several tools in parallel.
func BuildTracingCallbacks() ([]llmagent.BeforeToolCallback, []llmagent.AfterToolCallback) {
	tracer := otel.Tracer("pi-go")
	spans := newSpanRegistry()

	beforeCB := func(ctx agent.Context, t tool.Tool, args map[string]any) (map[string]any, error) {
		_, span := tracer.Start(ctx, "execute_tool "+t.Name())
		span.SetAttributes(
			semconv.GenAIOperationNameExecuteTool,
			semconv.GenAIToolName(t.Name()),
			otel.AttributeString("tool.name", t.Name()),
			otel.AttributeInt("tool.args_count", len(args)),
		)
		spans.push(toolSpanKey(ctx, t), span)
		return nil, nil
	}

	afterCB := func(ctx agent.Context, t tool.Tool, args, result map[string]any, runErr error) (map[string]any, error) {
		span := spans.pop(toolSpanKey(ctx, t))
		if span == nil {
			return result, nil
		}
		if runErr != nil {
			span.RecordError(runErr)
			span.SetStatus(codes.Error, runErr.Error())
		} else {
			span.SetStatus(codes.Ok, "")
		}
		span.SetAttributes(otel.AttributeBool("tool.success", runErr == nil))
		span.End()
		return result, nil
	}

	return []llmagent.BeforeToolCallback{beforeCB}, []llmagent.AfterToolCallback{afterCB}
}

// toolSpanKey identifies one tool call. FunctionCallID is unique per call and
// is what keeps parallel tool calls from ending each other's spans; it falls
// back to invocation+tool name for contexts that leave it empty.
func toolSpanKey(ctx agent.Context, t tool.Tool) string {
	if id := ctx.FunctionCallID(); id != "" {
		return id
	}
	return ctx.InvocationID() + "/" + t.Name()
}

// BuildLLMTracingCallbacks returns before/after model callbacks that emit one
// OTEL span per LLM invocation, named and attributed per the OpenTelemetry
// GenAI semantic conventions (semconv v1.37.0): `chat <model>`, carrying
// gen_ai.provider.name, gen_ai.request.model, gen_ai.response.model,
// gen_ai.response.finish_reasons and the gen_ai.usage.* token counts.
//
// providerName is pi-go's provider id ("openai", "anthropic", …); it is mapped
// onto the semconv enum where one exists and passed through otherwise.
//
// ADK callbacks cannot hand a context back to the framework, so the span
// cannot be carried between the two callbacks through ctx. It is held in a
// registry keyed by invocation instead — see llmSpans.
func BuildLLMTracingCallbacks(providerName string) ([]llmagent.BeforeModelCallback, []llmagent.AfterModelCallback) {
	tracer := otel.Tracer("pi-go")
	spans := newSpanRegistry()
	provider := genAIProviderAttr(providerName)

	beforeCB := func(ctx agent.Context, req *model.LLMRequest) (*model.LLMResponse, error) {
		// Span name is `<operation> <model>` per the GenAI conventions.
		_, span := tracer.Start(ctx, "chat "+req.Model)
		span.SetAttributes(
			semconv.GenAIOperationNameChat,
			provider,
			semconv.GenAIRequestModel(req.Model),
		)
		spans.push(ctx.InvocationID(), span)
		return nil, nil
	}

	afterCB := func(ctx agent.Context, resp *model.LLMResponse, respErr error) (*model.LLMResponse, error) {
		// A streaming call reports every chunk through this callback. Only the
		// terminal one closes the span — usage metadata rides on that final
		// response, so ending early would drop the token counts entirely.
		if respErr == nil && resp != nil && resp.Partial {
			return resp, nil
		}

		span := spans.pop(ctx.InvocationID())
		if span == nil {
			return resp, nil
		}
		if resp != nil {
			setLLMResponseAttributes(span, resp)
		}
		switch {
		case respErr != nil:
			span.RecordError(respErr)
			span.SetStatus(codes.Error, respErr.Error())
		case resp != nil && resp.ErrorCode != "":
			span.SetStatus(codes.Error, resp.ErrorCode+": "+resp.ErrorMessage)
		default:
			span.SetStatus(codes.Ok, "")
		}
		span.End()
		return resp, nil
	}

	return []llmagent.BeforeModelCallback{beforeCB}, []llmagent.AfterModelCallback{afterCB}
}

// setLLMResponseAttributes stamps the response model, finish reason and token
// usage onto span. Zero counts are skipped rather than reported as 0, so a
// provider that omits usage is distinguishable from one that reports none.
func setLLMResponseAttributes(span trace.Span, resp *model.LLMResponse) {
	attrs := make([]attribute.KeyValue, 0, 8)
	if resp.ModelVersion != "" {
		attrs = append(attrs, semconv.GenAIResponseModel(resp.ModelVersion))
	}
	if resp.FinishReason != "" {
		attrs = append(attrs, semconv.GenAIResponseFinishReasons(string(resp.FinishReason)))
	}
	if u := resp.UsageMetadata; u != nil {
		if u.PromptTokenCount > 0 {
			attrs = append(attrs, semconv.GenAIUsageInputTokens(int(u.PromptTokenCount)))
		}
		if u.CandidatesTokenCount > 0 {
			attrs = append(attrs, semconv.GenAIUsageOutputTokens(int(u.CandidatesTokenCount)))
		}
		// The remaining counts have no semconv equivalent yet, but they are
		// what the bill actually turns on: a cache read costs a fraction of a
		// fresh input token, and reasoning tokens are billed as output.
		if u.CachedContentTokenCount > 0 {
			attrs = append(attrs, otel.AttributeInt("gen_ai.usage.cached_input_tokens", int(u.CachedContentTokenCount)))
		}
		if u.ThoughtsTokenCount > 0 {
			attrs = append(attrs, otel.AttributeInt("gen_ai.usage.reasoning_tokens", int(u.ThoughtsTokenCount)))
		}
		if u.TotalTokenCount > 0 {
			attrs = append(attrs, otel.AttributeInt("gen_ai.usage.total_tokens", int(u.TotalTokenCount)))
		}
	}
	if len(attrs) > 0 {
		span.SetAttributes(attrs...)
	}
}

// genAIProviderAttr maps a pi-go provider id onto the semconv gen_ai.provider.name
// enum. Providers with no enum entry (ollama, opencode) pass through as-is,
// which the convention allows.
func genAIProviderAttr(providerName string) attribute.KeyValue {
	switch providerName {
	case "openai":
		return semconv.GenAIProviderNameOpenAI
	case "anthropic":
		return semconv.GenAIProviderNameAnthropic
	case "gemini":
		return semconv.GenAIProviderNameGCPGemini
	case "mistral":
		return semconv.GenAIProviderNameMistralAI
	case "azure":
		return semconv.GenAIProviderNameAzureAIOpenAI
	default:
		return semconv.GenAIProviderNameKey.String(providerName)
	}
}

// RunLifecycleHook executes a lifecycle hook's shell command with the event
// name and data as JSON on stdin. Lifecycle hooks (turn_complete,
// user_input_required) have no tool, so the payload carries the event instead.
// A non-zero exit or timeout is logged by the caller, never fatal.
func RunLifecycleHook(ctx context.Context, hook HookConfig, event string, data map[string]any) error {
	hookCtx, cancel := context.WithTimeout(ctx, hook.timeout())
	defer cancel()

	cmd := procs.CommandContext(hookCtx, "sh", "-c", hook.Command)

	input := map[string]any{
		"event": event,
		"data":  data,
	}
	jsonBytes, err := json.Marshal(input)
	if err != nil {
		return fmt.Errorf("marshaling hook input: %w", err)
	}
	cmd.Stdin = bytes.NewReader(jsonBytes)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("command %q: %w (stderr: %s)", hook.Command, err, stderr.String())
	}
	return nil
}

// runHookCommand executes a hook's shell command with the tool name and data as JSON on stdin.
func runHookCommand(ctx context.Context, hook HookConfig, toolName string, data map[string]any) error {
	hookCtx, cancel := context.WithTimeout(ctx, hook.timeout())
	defer cancel()

	cmd := procs.CommandContext(hookCtx, "sh", "-c", hook.Command)

	// Pass context as JSON on stdin.
	input := map[string]any{
		"tool": toolName,
		"data": data,
	}
	jsonBytes, err := json.Marshal(input)
	if err != nil {
		return fmt.Errorf("marshaling hook input: %w", err)
	}
	cmd.Stdin = bytes.NewReader(jsonBytes)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("command %q: %w (stderr: %s)", hook.Command, err, stderr.String())
	}
	return nil
}
