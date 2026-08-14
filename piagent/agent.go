package piagent

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/session"
	adktool "google.golang.org/adk/v2/tool"

	"github.com/dimetron/pi-go/internal/agent"
	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/extension"
	"github.com/dimetron/pi-go/internal/logger"
	"github.com/dimetron/pi-go/internal/lsp"
	"github.com/dimetron/pi-go/internal/memory"
	pisession "github.com/dimetron/pi-go/internal/session"
	"github.com/dimetron/pi-go/internal/tools"
)

// Agent is an embedded pi-go coding agent: an ADK runner assembled with
// pi-go's tools, prompt, skills, subagents and memory. Create one with [New]
// and release it with [Close].
//
// An Agent is safe for concurrent use across sessions; a single session should
// be driven by one goroutine at a time, since turns append to shared history.
type Agent struct {
	inner     *agent.Agent
	workDir   string
	modelName string
	provider  string
	tools     []adktool.Tool
	memStore  memory.Store
	// sessionLog is nil-safe: every method tolerates a nil receiver, so a
	// failed log file costs logging and nothing else.
	sessionLog *logger.Logger
	// onNewSession arms the subsystems that can only be told a session ID
	// after the session exists — memory recording and the ACP event log.
	onNewSession func(sessionID string)
	closers      []func() error
}

// New assembles an embedded agent. With no options it reproduces the pi CLI's
// headless setup for the process working directory; see the package
// documentation for exactly what that includes.
//
// The returned Agent owns a sandbox, a process supervisor, a subagent
// orchestrator and possibly an LSP manager, a memory worker and a palace.
// Always defer [Agent.Close].
func New(ctx context.Context, opts ...Option) (*Agent, error) {
	o := defaultOptions()
	for _, opt := range opts {
		opt(&o)
	}

	workDir, err := resolveWorkDir(o.workDir)
	if err != nil {
		return nil, err
	}
	sessionDir, err := resolveSessionDir(o.sessionDir)
	if err != nil {
		return nil, err
	}
	cfg, err := config.LoadFrom(workDir)
	if err != nil {
		return nil, fmt.Errorf("loading config: %w", err)
	}

	llm, info, err := buildLLM(ctx, cfg, o)
	if err != nil {
		return nil, err
	}

	a := &Agent{
		workDir:   workDir,
		modelName: llm.Name(),
		provider:  info.Provider,
	}

	sandbox, err := buildSandbox(workDir, o.extraSandbox)
	if err != nil {
		return nil, err
	}
	a.push(sandbox.Close)

	bashSup := tools.NewBashSupervisor()
	// Backgrounded commands have no owner but this supervisor; leaving them
	// running past Close is a leaked process tree.
	a.push(func() error { bashSup.KillAll(); return nil })

	coreTools, err := tools.CoreTools(sandbox, tools.WithBashSupervisor(bashSup))
	if err != nil {
		return nil, a.abort(fmt.Errorf("creating core tools: %w", err))
	}
	bashCtl, err := tools.BashControlTools(bashSup)
	if err != nil {
		return nil, a.abort(fmt.Errorf("creating bash control tools: %w", err))
	}
	coreTools = append(coreTools, bashCtl...)

	onEvent := o.onAgentEvent
	if onEvent == nil {
		onEvent = func(string, string, string) {}
	}
	bashSup.SetSink(func(execID, kind, content string) { onEvent(execID, kind, content) })

	orch := buildSubagents(ctx, &cfg, workDir)
	a.push(func() error { orch.Shutdown(); return nil })
	if o.subagentEnabled {
		agentTools, aErr := tools.AgentTools(orch, tools.AgentEventCallback(onEvent))
		if aErr != nil {
			return nil, a.abort(fmt.Errorf("creating agent tools: %w", aErr))
		}
		coreTools = append(coreTools, agentTools...)
	}

	memStore, memWorker, closeMemory := setupMemory(ctx, o, cfg, orch)
	a.push(func() error { closeMemory(); return nil })
	a.memStore = memStore
	if memStore != nil {
		memTools, mErr := tools.MemoryTools(memStore)
		if mErr != nil {
			slog.Warn("piagent: memory tools disabled", "error", mErr)
		} else {
			coreTools = append(coreTools, memTools...)
		}
	}

	palaceTools, palaceContext, closePalace := setupPalace(o, cfg, memWorker)
	a.push(func() error { closePalace(); return nil })
	coreTools = append(coreTools, palaceTools...)

	lspMgr, lspTools, err := setupLSP(o.lspMode)
	if err != nil {
		return nil, a.abort(err)
	}
	a.push(func() error { lspMgr.Shutdown(); return nil })
	coreTools = append(coreTools, lspTools...)

	// Gemini search grounding is a server-side tool. APPEND, never replace:
	// replacing the slice here would strip every real tool and leave the model
	// with nothing to call.
	if gTool, ok := agent.GeminiGroundingTool(info.Provider); ok {
		coreTools = append(coreTools, gTool)
	}
	coreTools = append(coreTools, o.tools...)
	a.tools = coreTools

	instruction := buildInstruction(o, workDir)
	if palaceContext != "" {
		instruction += "\n\n## Palace Memory Context\n\n" + palaceContext
	}
	instruction += memoryContext(ctx, memStore, cfg, workDir)

	sessionSvc, err := pisession.NewFileService(sessionDir)
	if err != nil {
		return nil, a.abort(fmt.Errorf("creating session service: %w", err))
	}

	// Created before the agent so it captures the agent's own non-fatal
	// diagnostics (unresolved instruction placeholders, say) instead of
	// letting them reach stderr.
	sessionLog, err := logger.New()
	if err != nil {
		slog.Warn("piagent: session log unavailable", "error", err)
	}
	a.push(sessionLog.Close)

	// memSessionID is only known once a session exists; the memory callback
	// reads it through the pointer, so recording starts from that point.
	var memSessionID string
	before, after := buildCallbacks(callbackDeps{
		cfg:       cfg,
		sandbox:   sandbox,
		lspMgr:    lspMgr,
		provider:  info.Provider,
		worker:    memWorker,
		project:   workDir,
		sessionID: &memSessionID,
		opts:      o,
	})

	inner, err := agent.New(agent.Config{
		Model:                llm,
		Tools:                coreTools,
		Toolsets:             append(buildToolsets(cfg), o.toolsets...),
		Instruction:          instruction,
		SessionService:       sessionSvc,
		WorkingDir:           workDir,
		BeforeToolCallbacks:  before.tool,
		AfterToolCallbacks:   after.tool,
		BeforeModelCallbacks: before.model,
		AfterModelCallbacks:  after.model,
		Logger:               sessionLog,
	})
	if err != nil {
		return nil, a.abort(fmt.Errorf("creating agent: %w", err))
	}
	a.inner = inner
	a.onNewSession = func(sessionID string) {
		memSessionID = sessionID
		orch.SetACPLogPath(filepath.Join(sessionDir, sessionID, "acp.jsonl"))
	}
	a.sessionLog = sessionLog
	return a, nil
}

// push registers a cleanup function, to be run in reverse order by Close.
func (a *Agent) push(fn func() error) {
	a.closers = append(a.closers, fn)
}

// abort releases everything acquired so far and returns err, so a failure
// partway through New never leaks the resources that already succeeded.
func (a *Agent) abort(err error) error {
	return errors.Join(err, a.Close())
}

// Close releases every resource the agent acquired: the sandbox, backgrounded
// processes, the subagent orchestrator, the LSP manager, the memory worker and
// store, the palace, and the session log. It is safe to call more than once.
func (a *Agent) Close() error {
	var errs []error
	for i := len(a.closers) - 1; i >= 0; i-- {
		if err := a.closers[i](); err != nil {
			errs = append(errs, err)
		}
	}
	a.closers = nil
	return errors.Join(errs...)
}

// NewSession creates a session and returns its ID. Sessions persist under the
// session directory, so the pi CLI can resume one an embedder started.
func (a *Agent) NewSession(ctx context.Context) (string, error) {
	sessionID, _, err := a.inner.CreateSession(ctx)
	if err != nil {
		return "", err
	}
	if a.onNewSession != nil {
		a.onNewSession(sessionID)
	}
	if a.memStore != nil {
		_ = a.memStore.CreateSession(ctx, &memory.Session{
			SessionID: sessionID,
			Project:   a.workDir,
			StartedAt: time.Now(),
			Status:    "active",
		})
	}
	a.sessionLog.SessionStart(sessionID, a.modelName, "embedded")
	return sessionID, nil
}

// Run sends a message and returns the turn's ADK events. Iterate the sequence
// to completion, or stop early to abandon the turn.
func (a *Agent) Run(ctx context.Context, sessionID, message string) iter.Seq2[*session.Event, error] {
	return a.inner.Run(ctx, sessionID, message)
}

// RunStreaming is [Agent.Run] with SSE streaming, so text arrives as deltas.
// The final aggregate event repeats the whole reply; a caller that prints
// deltas must skip it.
func (a *Agent) RunStreaming(ctx context.Context, sessionID, message string) iter.Seq2[*session.Event, error] {
	return a.inner.RunStreaming(ctx, sessionID, message)
}

// Ask runs one turn and returns the assistant's text, discarding tool calls,
// tool results and thinking. It is the shortest path from a question to an
// answer; use [Agent.Run] when you need to observe the turn.
func (a *Agent) Ask(ctx context.Context, sessionID, message string) (string, error) {
	var b strings.Builder
	for ev, err := range a.Run(ctx, sessionID, message) {
		if err != nil {
			return b.String(), fmt.Errorf("agent run: %w", err)
		}
		if ev == nil {
			continue
		}
		// A provider failure arrives as an error field on an ordinary event,
		// not as an iteration error; without this the turn returns empty and
		// looks successful.
		if evErr := agent.EventError(ev); evErr != nil {
			return b.String(), fmt.Errorf("agent run: %w", evErr)
		}
		if ev.Content == nil || ev.Content.Role == "thinking" {
			continue
		}
		for _, part := range ev.Content.Parts {
			if part.Text != "" {
				b.WriteString(part.Text)
			}
		}
	}
	return b.String(), nil
}

// SetSessionTitle records a human-readable label for a session. It is metadata
// only, used by the CLI's session listing.
func (a *Agent) SetSessionTitle(sessionID, title string) error {
	return a.inner.SetSessionTitle(sessionID, title)
}

// Tools returns the tools registered with the model, in the order they were
// declared. Toolsets (MCP, A2A) resolve their tools per request and are not
// included.
func (a *Agent) Tools() []adktool.Tool {
	return slices.Clone(a.tools)
}

// Model returns the resolved model name.
func (a *Agent) Model() string { return a.modelName }

// Provider returns the resolved provider name, or "" for an injected model.
func (a *Agent) Provider() string { return a.provider }

// WorkingDir returns the absolute directory the agent operates in.
func (a *Agent) WorkingDir() string { return a.workDir }

// callbackDeps carries what the callback chains need to be built.
type callbackDeps struct {
	cfg       config.Config
	sandbox   *tools.Sandbox
	lspMgr    *lsp.Manager
	provider  string
	worker    *memory.Worker
	project   string
	sessionID *string
	opts      options
}

// callbackSet groups the tool and model callbacks for one phase.
type callbackSet struct {
	tool  []llmagent.BeforeToolCallback
	model []llmagent.BeforeModelCallback
}

// afterCallbackSet is callbackSet's after-phase twin. The two phases use
// different ADK function types, so they cannot share one struct.
type afterCallbackSet struct {
	tool  []llmagent.AfterToolCallback
	model []llmagent.AfterModelCallback
}

// buildCallbacks wires pi-go's callbacks and the embedder's.
//
// The after-tool chain is folded into a single ADK callback by
// [composeAfterTool]; handing ADK the slice would run only the first entry.
// The other three phases keep ADK's own semantics, which are already correct
// for them.
func buildCallbacks(d callbackDeps) (callbackSet, afterCallbackSet) {
	hooks := convertHooks(d.cfg.Hooks)
	beforeTool := extension.BuildBeforeToolCallbacks(hooks)
	afterTool := extension.BuildAfterToolCallbacks(hooks)

	tracingBefore, tracingAfter := extension.BuildTracingCallbacks()
	beforeTool = append(beforeTool, tracingBefore...)
	afterTool = append(afterTool, tracingAfter...)

	beforeModel, afterModel := extension.BuildLLMTracingCallbacks(d.provider)
	beforeModel = append(beforeModel, extension.BuildReadImageCallback(d.sandbox))

	// Dedup runs after the compactor so both calls are compared in their
	// final, post-compaction form.
	afterTool = append(afterTool,
		lsp.BuildLSPAfterToolCallback(d.lspMgr),
		tools.BuildCompactorCallback(compactorConfig(d.cfg), tools.NewCompactMetrics()),
		tools.BuildDedupCallback(tools.NewResultDeduper()))

	if d.worker != nil {
		afterTool = append(afterTool, memoryObservationCallback(d.worker, d.cfg, d.project, d.sessionID))
	}

	// The embedder's callbacks run last, so they observe the compacted and
	// deduplicated result rather than the raw one.
	beforeTool = append(beforeTool, d.opts.beforeTool...)
	afterTool = append(afterTool, d.opts.afterTool...)
	beforeModel = append(beforeModel, d.opts.beforeModel...)
	afterModel = append(afterModel, d.opts.afterModel...)

	var composed []llmagent.AfterToolCallback
	if cb := composeAfterTool(afterTool); cb != nil {
		composed = []llmagent.AfterToolCallback{cb}
	}
	return callbackSet{tool: beforeTool, model: beforeModel},
		afterCallbackSet{tool: composed, model: afterModel}
}
