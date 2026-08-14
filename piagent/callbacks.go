package piagent

import (
	"time"

	adkagent "google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	adktool "google.golang.org/adk/v2/tool"

	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/memory"
)

// composeAfterTool folds a chain of after-tool callbacks into the single
// callback ADK is given.
//
// ADK's own chain (llminternal.Flow.invokeAfterToolCallbacks) stops at the
// first callback that returns a non-nil result, and passes the *original*
// result to each callback rather than the previous one's output. pi-go
// registers several callbacks that all return the result map — LSP hooks, the
// compactor, dedup, memory recording — so under ADK's rules only the first of
// them ever runs. That defect silently disabled four subsystems for months; it
// is the reason this function exists.
//
// The composed semantics are:
//
//   - (nil, nil): the result is unchanged, and the chain continues.
//   - (m, nil): m becomes the result every later callback sees, and the one
//     returned to ADK.
//   - (_, err): the chain aborts and err propagates.
//
// A nil result from every callback yields (nil, nil), which tells ADK to keep
// the tool's own result — the same thing an empty chain would do.
func composeAfterTool(cbs []llmagent.AfterToolCallback) llmagent.AfterToolCallback {
	cbs = nonNil(cbs)
	if len(cbs) == 0 {
		return nil
	}
	return func(ctx adkagent.Context, t adktool.Tool, args, result map[string]any, toolErr error) (map[string]any, error) {
		current := result
		replaced := false
		for _, cb := range cbs {
			out, err := cb(ctx, t, args, current, toolErr)
			if err != nil {
				return nil, err
			}
			if out == nil {
				continue
			}
			current = out
			replaced = true
		}
		if !replaced {
			return nil, nil
		}
		return current, nil
	}
}

// memoryObservationCallback records each successful tool call as a raw
// observation. sessionID is read through a pointer because the callbacks are
// wired before any session exists; until one does, the callback is inert.
func memoryObservationCallback(worker *memory.Worker, cfg config.Config, project string, sessionID *string) llmagent.AfterToolCallback {
	var excluded map[string]bool
	if cfg.Memory != nil && len(cfg.Memory.ExcludedTools) > 0 {
		excluded = make(map[string]bool, len(cfg.Memory.ExcludedTools))
		for _, name := range cfg.Memory.ExcludedTools {
			excluded[name] = true
		}
	}
	return func(_ adkagent.Context, t adktool.Tool, args, result map[string]any, toolErr error) (map[string]any, error) {
		// A failed tool call is not worth remembering, and swallowing toolErr
		// here is correct: it is the tool's error to report, and this callback
		// only observes.
		//nolint:nilerr // toolErr belongs to the tool, not to this observer
		if toolErr != nil || *sessionID == "" || excluded[t.Name()] {
			return nil, nil
		}
		worker.Enqueue(memory.RawObservation{
			SessionID:  *sessionID,
			Project:    project,
			ToolName:   t.Name(),
			ToolInput:  args,
			ToolOutput: result,
			Timestamp:  time.Now(),
		})
		return nil, nil
	}
}

// nonNil drops nil entries so an optional callback can be appended
// unconditionally at the call site.
func nonNil(cbs []llmagent.AfterToolCallback) []llmagent.AfterToolCallback {
	out := make([]llmagent.AfterToolCallback, 0, len(cbs))
	for _, cb := range cbs {
		if cb != nil {
			out = append(out, cb)
		}
	}
	return out
}
