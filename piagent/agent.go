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
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/session"
	adktool "google.golang.org/adk/v2/tool"

	"github.com/dimetron/pi-go/internal/agent"
	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/extension"
	"github.com/dimetron/pi-go/internal/logger"
	"github.com/dimetron/pi-go/internal/lsp"
	"github.com/dimetron/pi-go/internal/memory"
	pisession "github.com/dimetron/pi-go/internal/session"
	"github.com/dimetron/pi-go/internal/subagent"
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
	beforeTurn   []BeforeTurnFunc
	afterTurn    []AfterTurnFunc
	closers      []func() error
}

// ErrNoModel is returned by [New] when no model was supplied. piagent does not
// construct providers, so there is nothing to fall back to.
var ErrNoModel = errors.New("piagent: no model supplied — pass WithModel(m) with an ADK model.LLM (pi-go's providers are in the pimodels package)")

// New assembles an embedded agent around the model given to [WithModel], which
// is the one required option. Everything else — working directory, tools,
// skills, subagents, project rules, toolsets, memory and palace — is resolved
// from pi-go's conventions; see the package documentation.
//
// The returned Agent owns a sandbox, a process supervisor, a subagent
// orchestrator and possibly an LSP manager, a memory worker and a palace.
// Always defer [Agent.Close].
func New(ctx context.Context, opts ...Option) (*Agent, error) {
	o := defaultOptions()
	for _, opt := range opts {
		opt(&o)
	}
	if o.model == nil {
		return nil, ErrNoModel
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

	llm := o.model
	providerName := providerOf(llm)
	a := &Agent{
		workDir:    workDir,
		modelName:  llm.Name(),
		provider:   providerName,
		beforeTurn: o.beforeTurn,
		afterTurn:  o.afterTurn,
	}

	rt, err := a.buildRuntime(ctx, o, &cfg, providerName)
	if err != nil {
		return nil, err
	}
	a.tools = rt.tools

	instruction := buildInstruction(o, workDir)
	if rt.palaceContext != "" {
		instruction += "\n\n## Palace Memory Context\n\n" + rt.palaceContext
	}
	instruction += memoryContext(ctx, a.memStore, cfg, workDir)

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
		sandbox:   rt.sandbox,
		lspMgr:    rt.lspMgr,
		provider:  providerName,
		worker:    rt.memWorker,
		project:   workDir,
		sessionID: &memSessionID,
		opts:      o,
	})

	inner, err := agent.New(agent.Config{
		Model:                llm,
		Tools:                rt.tools,
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
		rt.orch.SetACPLogPath(filepath.Join(sessionDir, sessionID, "acp.jsonl"))
	}
	a.sessionLog = sessionLog
	return a, nil
}

// runtimeParts are the subsystems [New] assembles before the inner agent
// exists. tools is the finished tool list; the rest is what callback wiring
// and session bookkeeping still need afterwards.
type runtimeParts struct {
	sandbox       *tools.Sandbox
	orch          *subagent.Orchestrator
	lspMgr        *lsp.Manager
	memWorker     *memory.Worker
	palaceContext string
	tools         []adktool.Tool
}

// buildRuntime assembles the sandbox, bash supervisor, tools, subagents,
// memory, palace and LSP subsystems. Every acquisition registers its cleanup
// on a first, so a failure partway through releases what already succeeded.
// It also sets a.memStore, which the memory tools and instruction need.
func (a *Agent) buildRuntime(ctx context.Context, o options, cfg *config.Config, providerName string) (*runtimeParts, error) {
	workDir := a.workDir

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

	orch := buildSubagents(ctx, cfg, workDir)
	a.push(func() error { orch.Shutdown(); return nil })
	if o.subagentEnabled {
		agentTools, aErr := tools.AgentTools(orch, tools.AgentEventCallback(onEvent))
		if aErr != nil {
			return nil, a.abort(fmt.Errorf("creating agent tools: %w", aErr))
		}
		coreTools = append(coreTools, agentTools...)
	}

	memStore, memWorker, closeMemory := setupMemory(ctx, o, *cfg, orch)
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

	palaceTools, palaceContext, closePalace := setupPalace(o, *cfg, memWorker)
	a.push(func() error { closePalace(); return nil })
	coreTools = append(coreTools, palaceTools...)

	lspMgr, lspTools, err := setupLSP(o.lspMode)
	if err != nil {
		return nil, a.abort(err)
	}
	a.push(func() error { lspMgr.Shutdown(); return nil })
	coreTools = append(coreTools, lspTools...)

	// Gemini search grounding is a server-side tool, so it only means anything
	// on a Gemini model; see providerOf for how that is decided.
	// APPEND, never replace: replacing the slice here would strip every real
	// tool and leave the model with nothing to call.
	if gTool, ok := agent.GeminiGroundingTool(providerName); ok {
		coreTools = append(coreTools, gTool)
	}
	coreTools = append(coreTools, o.tools...)

	return &runtimeParts{
		sandbox:       sandbox,
		orch:          orch,
		lspMgr:        lspMgr,
		memWorker:     memWorker,
		palaceContext: palaceContext,
		tools:         coreTools,
	}, nil
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
	a.sessionLog.SessionStart(sessionID, a.modelName, a.provider, "embedded", "", "embedded")
	return sessionID, nil
}

// Run sends a message and returns the turn's ADK events. Iterate the sequence
// to completion, or stop early to abandon the turn.
func (a *Agent) Run(ctx context.Context, sessionID, message string) iter.Seq2[*session.Event, error] {
	return a.observeTurn(ctx, sessionID, message, a.inner.Run)
}

// RunStreaming is [Agent.Run] with SSE streaming, so text arrives as deltas.
// The final aggregate event repeats the whole reply; a caller that prints
// deltas must skip it.
func (a *Agent) RunStreaming(ctx context.Context, sessionID, message string) iter.Seq2[*session.Event, error] {
	return a.observeTurn(ctx, sessionID, message, a.inner.RunStreaming)
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

// Model returns the name reported by the model the agent runs on.
func (a *Agent) Model() string { return a.modelName }

// WorkingDir returns the absolute directory the agent operates in.
func (a *Agent) WorkingDir() string { return a.workDir }

// providerNamer is satisfied by a model that knows which provider it talks to.
// The models package implements it; it is declared here structurally, and
// deliberately not imported, so piagent depends on the shape rather than on
// the package. isolation_test.go enforces that.
type providerNamer interface{ Provider() string }

// providerOf names the provider family behind a model: from the model itself
// when it can say, and from its name when it cannot. Only two things read it,
// and both degrade quietly on a miss — the OTel gen_ai.provider.name span
// attribute (which falls back to the raw string) and the Gemini grounding tool
// (which simply does not register).
func providerOf(m model.LLM) string {
	if pn, ok := m.(providerNamer); ok {
		if name := pn.Provider(); name != "" {
			return name
		}
	}
	return providerFromModelName(m.Name())
}

// modelNamePrefixes maps a model-name prefix onto a provider family, for
// models that cannot name their own provider. It is a fallback, not the
// mechanism: prefer implementing [providerNamer].
var modelNamePrefixes = map[string]string{
	"claude":    "anthropic",
	"gpt":       "openai",
	"gemini":    "gemini",
	"grok":      "xai",
	"mistral":   "mistral",
	"magistral": "mistral",
}

// providerFromModelName returns the provider family for a model name, or ""
// when the name is not recognized.
func providerFromModelName(modelName string) string {
	lower := strings.ToLower(modelName)
	for prefix, name := range modelNamePrefixes {
		if strings.HasPrefix(lower, prefix) {
			return name
		}
	}
	return ""
}

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
	beforeModel = append(beforeModel, extension.BuildReadImageCallback(d.sandbox, d.provider))

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
