// Package extension provides hooks (before/after tool call), skill loading,
// and MCP tool integration for the pi-go agent.
package extension

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os/exec"
	"slices"
	"sync"
	"time"

	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/tool"

	"github.com/dimetron/pi-go/internal/otel"
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
		_ = s.OnToolEnd(ctx, acpID, args, result, runErr)
		return result, nil
	}
	return []llmagent.BeforeToolCallback{beforeCB}, []llmagent.AfterToolCallback{afterCB}
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
				log.Printf("hook %q failed for tool %q: %v", hook.Command, t.Name(), err)
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
				log.Printf("hook %q failed for tool %q: %v", hook.Command, t.Name(), hookErr)
			}
			return result, nil
		})
	}
	return cbs
}

// BuildTracingCallbacks returns before/after tool callbacks that emit OTEL spans
// for every tool invocation. The parent span context is propagated from the
// context passed to the callback, so spans are linked to the agent's trace.
func BuildTracingCallbacks() ([]llmagent.BeforeToolCallback, []llmagent.AfterToolCallback) {
	tracer := otel.Tracer("pi-go")

	beforeCB := func(ctx agent.Context, t tool.Tool, args map[string]any) (map[string]any, error) {
		_, span := tracer.Start(ctx, "tool."+t.Name())
		span.SetAttributes(
			otel.AttributeString("tool.name", t.Name()),
			otel.AttributeInt("tool.args_count", len(args)),
		)
		_ = span // span lifetime managed by afterCB
		return nil, nil
	}

	afterCB := func(ctx agent.Context, t tool.Tool, args, result map[string]any, runErr error) (map[string]any, error) {
		span := trace.SpanFromContext(ctx)
		if span.IsRecording() {
			if runErr != nil {
				span.RecordError(runErr)
				span.SetStatus(codes.Error, runErr.Error())
			} else {
				span.SetStatus(codes.Ok, "")
			}
			span.SetAttributes(
				otel.AttributeString("tool.name", t.Name()),
				otel.AttributeBool("tool.success", runErr == nil),
			)
			span.End()
		}
		return result, nil
	}

	return []llmagent.BeforeToolCallback{beforeCB}, []llmagent.AfterToolCallback{afterCB}
}

// BuildLLMTracingCallbacks returns before/after model callbacks that emit OTEL spans
// for each LLM invocation inside the agent loop.
func BuildLLMTracingCallbacks() ([]llmagent.BeforeModelCallback, []llmagent.AfterModelCallback) {
	tracer := otel.Tracer("pi-go")

	beforeCB := func(ctx agent.Context, req *model.LLMRequest) (*model.LLMResponse, error) {
		_, span := tracer.Start(ctx, "llm."+req.Model)
		span.SetAttributes(
			otel.AttributeString("llm.model", req.Model),
		)
		return nil, nil
	}

	afterCB := func(ctx agent.Context, resp *model.LLMResponse, respErr error) (*model.LLMResponse, error) {
		span := trace.SpanFromContext(ctx)
		if span.IsRecording() {
			if respErr != nil {
				span.RecordError(respErr)
				span.SetStatus(codes.Error, respErr.Error())
			} else {
				span.SetStatus(codes.Ok, "")
			}
			span.End()
		}
		return resp, nil
	}

	return []llmagent.BeforeModelCallback{beforeCB}, []llmagent.AfterModelCallback{afterCB}
}

// runHookCommand executes a hook's shell command with the tool name and data as JSON on stdin.
func runHookCommand(ctx context.Context, hook HookConfig, toolName string, data map[string]any) error {
	hookCtx, cancel := context.WithTimeout(ctx, hook.timeout())
	defer cancel()

	cmd := exec.CommandContext(hookCtx, "sh", "-c", hook.Command)

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
