package tools

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"

	"github.com/dimetron/pi-go/internal/subagent"
)

// resolveContext extracts a context.Context from agent.Context, defaulting to context.Background().
func resolveContext(ctx agent.Context) context.Context {
	if ctx != nil {
		return ctx
	}
	return context.Background()
}

// maxParallelTasks is the maximum number of tasks allowed in parallel mode.
const maxParallelTasks = 8

// maxChainSteps is the maximum number of steps allowed in chain mode.
const maxChainSteps = 8

// SubagentInput defines the parameters for the subagent tool.
type SubagentInput struct {
	// Single mode: agent name to spawn.
	Agent string `json:"agent,omitempty"`
	// Single mode: task prompt for the agent.
	Task string `json:"task,omitempty"`

	// Parallel mode: list of tasks to run concurrently.
	Tasks []TaskItem `json:"tasks,omitempty"`

	// Chain mode: sequential pipeline of agents.
	Chain []ChainItem `json:"chain,omitempty"`
}

// TaskItem defines a single task in parallel mode.
type TaskItem struct {
	Agent string `json:"agent"`
	Task  string `json:"task"`
}

// ChainItem defines a single step in chain mode.
type ChainItem struct {
	Agent string `json:"agent"`
	Task  string `json:"task"` // supports {previous} and {previous_json}
}

// SubagentOutput is the result from a completed subagent call.
type SubagentOutput struct {
	Mode    string        `json:"mode"`
	Results []AgentResult `json:"results"`
	Summary string        `json:"summary"`
}

// AgentResult holds the result from a single agent execution.
type AgentResult struct {
	Agent     string `json:"agent"`
	AgentID   string `json:"agent_id"`
	Status    string `json:"status"` // "completed", "failed", "timeout"
	Result    string `json:"result"`
	Error     string `json:"error,omitempty"`
	Duration  string `json:"duration"`
	SessionID string `json:"session_id,omitempty"`
}

// SubagentEvent extends agent events with pipeline metadata for the TUI.
type SubagentEvent struct {
	AgentID    string `json:"agent_id"`
	Kind       string `json:"kind"` // "spawn", "text_delta", "tool_call", "tool_result", "error", "done"
	Content    string `json:"content"`
	PipelineID string `json:"pipeline_id"` // groups agents in same call
	Mode       string `json:"mode"`        // "single", "parallel", "chain"
	Step       int    `json:"step"`        // 1-based position
	Total      int    `json:"total"`       // total agents in pipeline
}

// SubagentEventCallback is called for each subagent event with pipeline metadata.
type SubagentEventCallback func(event SubagentEvent)

// NewSubagentTool creates the subagent ADK tool wired to an Orchestrator.
func NewSubagentTool(orch *subagent.Orchestrator, onEvent SubagentEventCallback) (tool.Tool, error) {
	desc := buildSubagentDescription(orch)

	return newTool("subagent", desc,
		func(ctx agent.Context, input SubagentInput) (SubagentOutput, error) {
			return subagentHandler(ctx, orch, input, onEvent)
		},
		// Common LLM parameter name mistakes
		map[string]string{
			"type":    "agent", // LLM sends "type" instead of "agent"
			"prompt":  "task",  // LLM sends "prompt" instead of "task"
			"message": "task",  // LLM sends "message" instead of "task"
			"items":   "tasks", // LLM sends "items" instead of "tasks"
			"steps":   "chain", // LLM sends "steps" instead of "chain"
		},
	)
}

// SubagentTools returns tools containing the subagent tool.
func SubagentTools(orch *subagent.Orchestrator, onEvent SubagentEventCallback) ([]tool.Tool, error) {
	t, err := NewSubagentTool(orch, onEvent)
	if err != nil {
		return nil, err
	}
	return []tool.Tool{t}, nil
}

// buildSubagentDescription generates the tool description with available agent names.
func buildSubagentDescription(orch *subagent.Orchestrator) string {
	var b strings.Builder
	b.WriteString(`Spawn a subagent to perform a task autonomously.

Modes:
- Single: {agent: "<name>", task: "<prompt>"} — spawn one agent
- Parallel: {tasks: [{agent: "<name>", task: "<prompt>"}, ...]} — run multiple agents concurrently (max 8)
- Chain: {chain: [{agent: "<name>", task: "<prompt>"}, ...]} — run agents sequentially, passing results forward

Worktree agents:
- Agents marked [worktree] edit an isolated git worktree. Normal subagent calls return only the agent output; those edits are not applied to the current tree unless the caller keeps and merges that worktree.
- If changes need to land in the current tree, ask the worktree agent for an exact patch/file list to apply, or choose a non-worktree editing agent.

`)

	b.WriteString("Available agents:\n")
	for _, name := range orch.AgentNames() {
		ac, err := orch.LookupAgent(name)
		if err != nil {
			continue
		}
		marker := ""
		if ac.Worktree {
			marker = " [worktree]"
		}
		fmt.Fprintf(&b, "- %s%s: %s\n", ac.Name, marker, ac.Description)
	}

	fmt.Fprintf(&b, "\nMaximum %d concurrent subagents. Each agent runs as a separate process.", maxParallelTasks)
	return b.String()
}

// subagentHandler dispatches to the appropriate mode handler.
func subagentHandler(ctx agent.Context, orch *subagent.Orchestrator, input SubagentInput, onEvent SubagentEventCallback) (SubagentOutput, error) {
	mode := detectMode(input)

	switch mode {
	case "single":
		return singleModeHandler(ctx, orch, input, onEvent)
	case "parallel":
		return parallelModeHandler(ctx, orch, input, onEvent)
	case "chain":
		return chainModeHandler(ctx, orch, input, onEvent)
	default:
		return SubagentOutput{}, fmt.Errorf(
			"could not detect mode: provide exactly ONE of:\n"+
				"  Single: {agent: \"\\u003cname\\u003e\", task: \"\\u003cprompt\\u003e\"} — spawn one agent\n"+
				"  Parallel: {tasks: [{agent: \"\\u003cname\\u003e\", task: \"\\u003cprompt\\u003e\"}, ...]} — run multiple agents concurrently\n"+
				"  Chain: {chain: [{agent: \"\\u003cname\\u003e\", task: \"\\u003cprompt\\u003e\"}, ...]} — run agents sequentially\n"+
				"Received: agent=%q, task=%q, tasks=%d, chain=%d",
			input.Agent, input.Task, len(input.Tasks), len(input.Chain))
	}
}

// detectMode determines the execution mode from the input fields.
// Chain takes priority over parallel, which takes priority over single.
func detectMode(input SubagentInput) string {
	// Chain mode takes priority
	if len(input.Chain) > 0 {
		return "chain"
	}
	// Parallel mode
	if len(input.Tasks) > 0 {
		return "parallel"
	}
	// Single mode: allow if either agent or task is present
	// This is lenient - we accept partial input to help recover from LLM mistakes
	if input.Agent != "" || input.Task != "" {
		return "single"
	}
	return ""
}

// singleModeHandler spawns a single agent and collects its result.
func singleModeHandler(ctx agent.Context, orch *subagent.Orchestrator, input SubagentInput, onEvent SubagentEventCallback) (SubagentOutput, error) {
	start := time.Now()
	pipelineID := fmt.Sprintf("pipe-%d", time.Now().UnixNano())

	// Validate agent exists.
	if _, err := orch.LookupAgent(input.Agent); err != nil {
		return SubagentOutput{
			Mode: "single",
			Results: []AgentResult{{
				Agent:    input.Agent,
				Status:   "failed",
				Error:    err.Error(),
				Duration: time.Since(start).Truncate(time.Millisecond).String(),
			}},
			Summary: fmt.Sprintf("unknown agent %q", input.Agent),
		}, nil
	}

	// Spawn the agent.
	events, agentID, err := orch.SpawnWithInput(resolveContext(ctx), subagent.AgentInput{
		Type:   input.Agent,
		Prompt: input.Task,
	})
	if err != nil {
		return SubagentOutput{
			Mode: "single",
			Results: []AgentResult{{
				Agent:    input.Agent,
				Status:   "failed",
				Error:    err.Error(),
				Duration: time.Since(start).Truncate(time.Millisecond).String(),
			}},
			Summary: fmt.Sprintf("failed to spawn %s: %s", input.Agent, err),
		}, nil
	}

	// Emit spawn event.
	emitEvent(onEvent, SubagentEvent{
		AgentID:    agentID,
		Kind:       "spawn",
		Content:    input.Agent,
		PipelineID: pipelineID,
		Mode:       "single",
		Step:       1,
		Total:      1,
	})

	// Consume events, forward to TUI, accumulate result.
	forwardAndConsume := func(events <-chan subagent.Event) (string, string, string, string) {
		var result strings.Builder
		st := "completed"
		var em string
		var sessID string
		for ev := range events {
			evContent := ev.Content
			if ev.Type == "error" && evContent == "" {
				evContent = ev.Error
			}
			emitEvent(onEvent, SubagentEvent{
				AgentID: agentID, Kind: ev.Type, Content: evContent,
				PipelineID: pipelineID, Mode: "single", Step: 1, Total: 1,
			})
			switch ev.Type {
			case "text_delta":
				result.WriteString(ev.Content)
			case "error":
				st = "failed"
				em = ev.Error
			case "message_start":
				if ev.SessionID != "" {
					sessID = ev.SessionID
				}
			case "run_done":
				// The orchestrator's terminal status is more specific than the
				// "failed" inferred from an error event: it distinguishes a
				// timeout from a crash. That distinction is actionable for the
				// model — a timed-out agent is worth retrying with a narrower
				// task, a crashed one is not — and until now it was computed
				// and then dropped on the floor here.
				if ev.Status == "timeout" {
					st = "timeout"
				}
			}
		}
		return truncateOutput(result.String()), st, em, sessID
	}

	resultText, status, errMsg, subSessionID := forwardAndConsume(events)

	emitEvent(onEvent, SubagentEvent{
		AgentID: agentID, Kind: "done",
		PipelineID: pipelineID, Mode: "single", Step: 1, Total: 1,
	})

	duration := time.Since(start).Truncate(time.Millisecond).String()

	return SubagentOutput{
		Mode: "single",
		Results: []AgentResult{{
			Agent:     input.Agent,
			AgentID:   agentID,
			Status:    status,
			Result:    resultText,
			Error:     errMsg,
			Duration:  duration,
			SessionID: subSessionID,
		}},
		Summary: fmt.Sprintf("%s %s in %s", input.Agent, status, duration),
	}, nil
}

// parallelModeHandler spawns multiple agents concurrently and collects all results.
func parallelModeHandler(ctx agent.Context, orch *subagent.Orchestrator, input SubagentInput, onEvent SubagentEventCallback) (SubagentOutput, error) {
	start := time.Now()
	pipelineID := fmt.Sprintf("pipe-%d", time.Now().UnixNano())
	total := len(input.Tasks)

	// Enforce max parallel tasks.
	if total > maxParallelTasks {
		return SubagentOutput{
			Mode:    "parallel",
			Summary: fmt.Sprintf("too many parallel tasks: %d (max %d)", total, maxParallelTasks),
			Results: []AgentResult{{
				Agent:    "parallel",
				Status:   "failed",
				Error:    fmt.Sprintf("too many parallel tasks: %d exceeds maximum of %d", total, maxParallelTasks),
				Duration: time.Since(start).Truncate(time.Millisecond).String(),
			}},
		}, nil
	}

	// Validate all agents exist upfront before spawning any.
	for _, task := range input.Tasks {
		if _, err := orch.LookupAgent(task.Agent); err != nil {
			return SubagentOutput{
				Mode: "parallel",
				Results: []AgentResult{{
					Agent:    task.Agent,
					Status:   "failed",
					Error:    err.Error(),
					Duration: time.Since(start).Truncate(time.Millisecond).String(),
				}},
				Summary: fmt.Sprintf("validation failed: unknown agent %q", task.Agent),
			}, nil
		}
	}

	// Spawn all agents concurrently and collect results.
	results := make([]AgentResult, total)
	var wg sync.WaitGroup
	spawnCtx := resolveContext(ctx)

	for i, task := range input.Tasks {
		wg.Add(1)
		go func(idx int, t TaskItem) {
			defer wg.Done()
			taskStart := time.Now()
			step := idx + 1 // 1-based

			// Spawn agent.
			events, agentID, err := orch.SpawnWithInput(spawnCtx, subagent.AgentInput{
				Type:   t.Agent,
				Prompt: t.Task,
			})
			if err != nil {
				results[idx] = AgentResult{
					Agent:    t.Agent,
					Status:   "failed",
					Error:    err.Error(),
					Duration: time.Since(taskStart).Truncate(time.Millisecond).String(),
				}
				return
			}

			// Emit spawn event.
			emitEvent(onEvent, SubagentEvent{
				AgentID:    agentID,
				Kind:       "spawn",
				Content:    t.Agent,
				PipelineID: pipelineID,
				Mode:       "parallel",
				Step:       step,
				Total:      total,
			})

			// Consume events, accumulate result, forward to callback.
			var result strings.Builder
			status := "completed"
			var errMsg string
			var sessID string

			for ev := range events {
				evContent := ev.Content
				if ev.Type == "error" && evContent == "" {
					evContent = ev.Error
				}
				emitEvent(onEvent, SubagentEvent{
					AgentID:    agentID,
					Kind:       ev.Type,
					Content:    evContent,
					PipelineID: pipelineID,
					Mode:       "parallel",
					Step:       step,
					Total:      total,
				})

				switch ev.Type {
				case "text_delta":
					result.WriteString(ev.Content)
				case "error":
					status = "failed"
					errMsg = ev.Error
				case "message_start":
					if ev.SessionID != "" {
						sessID = ev.SessionID
					}
				}
			}

			// Emit done event.
			emitEvent(onEvent, SubagentEvent{
				AgentID:    agentID,
				Kind:       "done",
				PipelineID: pipelineID,
				Mode:       "parallel",
				Step:       step,
				Total:      total,
			})

			results[idx] = AgentResult{
				Agent:     t.Agent,
				AgentID:   agentID,
				Status:    status,
				Result:    truncateOutput(result.String()),
				Error:     errMsg,
				Duration:  time.Since(taskStart).Truncate(time.Millisecond).String(),
				SessionID: sessID,
			}
		}(i, task)
	}

	wg.Wait()

	duration := time.Since(start).Truncate(time.Millisecond).String()
	return SubagentOutput{
		Mode:    "parallel",
		Results: results,
		Summary: buildParallelSummary(results, total, duration),
	}, nil
}

// chainModeHandler runs agents sequentially, passing each result to the next step.
// Task prompts support {previous} (text result) and {previous_json} (JSON-escaped) placeholders.
func chainModeHandler(ctx agent.Context, orch *subagent.Orchestrator, input SubagentInput, onEvent SubagentEventCallback) (SubagentOutput, error) {
	start := time.Now()
	pipelineID := fmt.Sprintf("pipe-%d", time.Now().UnixNano())
	total := len(input.Chain)

	// Enforce max chain steps.
	if total > maxChainSteps {
		return SubagentOutput{
			Mode:    "chain",
			Summary: fmt.Sprintf("too many chain steps: %d (max %d)", total, maxChainSteps),
			Results: []AgentResult{{
				Agent:    "chain",
				Status:   "failed",
				Error:    fmt.Sprintf("too many chain steps: %d exceeds maximum of %d", total, maxChainSteps),
				Duration: time.Since(start).Truncate(time.Millisecond).String(),
			}},
		}, nil
	}

	// Validate all agents exist upfront before executing any.
	for _, step := range input.Chain {
		if _, err := orch.LookupAgent(step.Agent); err != nil {
			return SubagentOutput{
				Mode: "chain",
				Results: []AgentResult{{
					Agent:    step.Agent,
					Status:   "failed",
					Error:    err.Error(),
					Duration: time.Since(start).Truncate(time.Millisecond).String(),
				}},
				Summary: fmt.Sprintf("validation failed: unknown agent %q", step.Agent),
			}, nil
		}
	}

	// Execute steps sequentially, passing results forward.
	results := make([]AgentResult, 0, total)
	spawnCtx := resolveContext(ctx)
	previousResult := ""

	for idx, step := range input.Chain {
		stepStart := time.Now()
		stepNum := idx + 1 // 1-based

		// Expand template placeholders in the task prompt.
		prompt := expandChainTemplate(step.Task, previousResult)

		// Spawn agent.
		events, agentID, err := orch.SpawnWithInput(spawnCtx, subagent.AgentInput{
			Type:   step.Agent,
			Prompt: prompt,
		})
		if err != nil {
			results = append(results, AgentResult{
				Agent:    step.Agent,
				Status:   "failed",
				Error:    err.Error(),
				Duration: time.Since(stepStart).Truncate(time.Millisecond).String(),
			})
			// Chain stops on first failure.
			break
		}

		// Emit spawn event.
		emitEvent(onEvent, SubagentEvent{
			AgentID:    agentID,
			Kind:       "spawn",
			Content:    step.Agent,
			PipelineID: pipelineID,
			Mode:       "chain",
			Step:       stepNum,
			Total:      total,
		})

		// Consume events, accumulate result, forward to callback.
		var result strings.Builder
		status := "completed"
		var errMsg string
		var sessID string

		for ev := range events {
			evContent := ev.Content
			if ev.Type == "error" && evContent == "" {
				evContent = ev.Error
			}
			emitEvent(onEvent, SubagentEvent{
				AgentID:    agentID,
				Kind:       ev.Type,
				Content:    evContent,
				PipelineID: pipelineID,
				Mode:       "chain",
				Step:       stepNum,
				Total:      total,
			})

			switch ev.Type {
			case "text_delta":
				result.WriteString(ev.Content)
			case "error":
				status = "failed"
				errMsg = ev.Error
			case "message_start":
				if ev.SessionID != "" {
					sessID = ev.SessionID
				}
			}
		}

		// Emit done event.
		emitEvent(onEvent, SubagentEvent{
			AgentID:    agentID,
			Kind:       "done",
			PipelineID: pipelineID,
			Mode:       "chain",
			Step:       stepNum,
			Total:      total,
		})

		resultText := truncateOutput(result.String())
		results = append(results, AgentResult{
			Agent:     step.Agent,
			AgentID:   agentID,
			Status:    status,
			Result:    resultText,
			Error:     errMsg,
			Duration:  time.Since(stepStart).Truncate(time.Millisecond).String(),
			SessionID: sessID,
		})

		// Chain stops on failure.
		if status != "completed" {
			break
		}

		// Pass result to next step.
		previousResult = resultText
	}

	duration := time.Since(start).Truncate(time.Millisecond).String()
	return SubagentOutput{
		Mode:    "chain",
		Results: results,
		Summary: buildChainSummary(results, total, duration),
	}, nil
}

// expandChainTemplate replaces {previous} and {previous_json} placeholders in the task prompt.
func expandChainTemplate(task, previousResult string) string {
	if previousResult == "" {
		return task
	}
	result := strings.ReplaceAll(task, "{previous}", previousResult)
	// JSON-escape: escape backslashes, quotes, and newlines for embedding in JSON strings.
	jsonEscaped := strings.ReplaceAll(previousResult, `\`, `\\`)
	jsonEscaped = strings.ReplaceAll(jsonEscaped, `"`, `\"`)
	jsonEscaped = strings.ReplaceAll(jsonEscaped, "\n", `\n`)
	jsonEscaped = strings.ReplaceAll(jsonEscaped, "\r", `\r`)
	jsonEscaped = strings.ReplaceAll(jsonEscaped, "\t", `\t`)
	result = strings.ReplaceAll(result, "{previous_json}", jsonEscaped)
	return result
}

// emitEvent safely calls the event callback if non-nil.
func emitEvent(cb SubagentEventCallback, ev SubagentEvent) {
	if cb != nil {
		cb(ev)
	}
}

// consumeAgentEvents reads events from a channel, accumulates text, and detects errors.
// Returns the accumulated result text, final status, and error message.
func consumeAgentEvents(events <-chan subagent.Event) (resultText, status, errMsg string) {
	var result strings.Builder
	status = "completed"
	for ev := range events {
		switch ev.Type {
		case "text_delta":
			result.WriteString(ev.Content)
		case "error":
			status = "failed"
			errMsg = ev.Error
		}
	}
	resultText = truncateOutput(result.String())
	return
}

// buildParallelSummary formats the summary line for parallel mode.
func buildParallelSummary(results []AgentResult, total int, duration string) string {
	completed, failed := 0, 0
	for _, r := range results {
		if r.Status == "completed" {
			completed++
		} else {
			failed++
		}
	}
	if failed > 0 {
		return fmt.Sprintf("parallel: %d/%d completed, %d failed in %s", completed, total, failed, duration)
	}
	return fmt.Sprintf("parallel: %d/%d completed in %s", completed, total, duration)
}

// buildChainSummary formats the summary line for chain mode.
func buildChainSummary(results []AgentResult, total int, duration string) string {
	completed := 0
	for _, r := range results {
		if r.Status == "completed" {
			completed++
		}
	}
	if completed < total {
		return fmt.Sprintf("chain: stopped at step %d/%d in %s", len(results), total, duration)
	}
	return fmt.Sprintf("chain: %d/%d steps completed in %s", completed, total, duration)
}
