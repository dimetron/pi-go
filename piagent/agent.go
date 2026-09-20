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
	"sync"
	"time"

	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/session"
	adktool "google.golang.org/adk/v2/tool"

	"github.com/dimetron/pi-go/internal/agent"
	"github.com/dimetron/pi-go/internal/autocompact"
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
	// memSummarizer writes end-of-session summaries. Nil when no summary model
	// was supplied, which makes the flush on Close a no-op.
	memSummarizer *memory.LLMSummarizer
	// memSessionsMu guards memSessions. [Agent] is documented as safe for
	// concurrent use across sessions, and two goroutines calling NewSession at
	// once append to this slice; Close also reads and clears it.
	memSessionsMu sync.Mutex
	// memSessions records, in order, the sessions this agent created. Close
	// summarizes and completes them: a session that is never completed stays
	// 'active' in the memory database forever, which is what the CLI has been
	// doing — ~/.pi-go/memory/claude-mem.db held thousands of active sessions
	// and zero completed ones.
	memSessions []string
	// project is the memory store's project key, which is the working
	// directory. Kept because Close still needs it after the sandbox is gone.
	project string
	// sessionLog is nil-safe: every method tolerates a nil receiver, so a
	// failed log file costs logging and nothing else.
	sessionLog *logger.Logger
	// onNewSession arms the subsystems that can only be told a session ID
	// after the session exists — memory recording and the ACP event log.
	onNewSession func(sessionID string)
	// meter is what auto-compaction reads the live context size from. It is
	// fed by an after-model callback rather than by a model wrapper, because
	// wrapping the model is guardrail's job and guardrail is out of reach.
	meter      *autocompact.Meter
	beforeTurn []BeforeTurnFunc
	afterTurn  []AfterTurnFunc
	closers    []func() error
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

	// Measures, never enforces: nothing here caps an embedder's spend, and
	// the CLI's guardrail is out of reach anyway (see the isolation test).
	// The meter exists so auto-compaction can tell how much of the context
	// window the transcript is using; it is fed by an after-model callback.
	contextMeter := autocompact.NewMeter()
	contextMeter.SetContextWindowSize(contextWindowFor(cfg, o))

	a := &Agent{
		workDir:    workDir,
		modelName:  llm.Name(),
		provider:   providerName,
		beforeTurn: o.beforeTurn,
		afterTurn:  o.afterTurn,
		meter:      contextMeter,
		project:    workDir,
	}

	rt, err := a.buildRuntime(ctx, o, &cfg, providerName)
	if err != nil {
		return nil, err
	}
	a.tools = rt.tools
	// A summarizer with no store to write to would summarize into nothing, so
	// it is only held when memory is on. This also keeps the "memory off"
	// promise: no store, no summary, no model call.
	if a.memStore != nil && o.sessionSummary != nil {
		a.memSummarizer = memory.NewLLMSummarizer(o.sessionSummary)
	}

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

	// Held rather than created inline: a summarizing compaction rebuilds the
	// transcript, which invalidates every pointer the deduper holds into the
	// old one, so the hook has to be able to reset it.
	resultDeduper := tools.NewResultDeduper()
	before, after := buildCallbacks(callbackDeps{
		deduper:  resultDeduper,
		meter:    contextMeter,
		cfg:      cfg,
		sandbox:  rt.sandbox,
		lspMgr:   rt.lspMgr,
		provider: providerName,
		worker:   rt.memWorker,
		project:  workDir,
		opts:     o,
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
	// Auto-compaction, on the same terms as the CLI and the ACP server. An
	// embedded agent is no less prone to outgrowing its window than an
	// interactive one — it re-sends the whole transcript every turn just the
	// same — and before this it was the one entry point with no defense.
	// The summarizer defaults to the session model. An embedder can hand over a
	// cheaper one: summarizing a transcript is a different job from the
	// conversation itself, and often a better one for a small model.
	if hook := autocompact.BuildHook(autocompact.Deps{
		SessionSvc:    sessionSvc,
		Tracker:       contextMeter,
		Deduper:       resultDeduper,
		Cfg:           autocompact.ConfigFrom(cfg),
		Log:           sessionLog,
		SummarizerLLM: resolveSummarizer(o, llm),
		// An embedder reads the outcome through its notifier when it supplies
		// one, and otherwise in the session log — never on stdout, which would
		// be output it never asked for.
		Notify: o.compactNotify,
	}); hook != nil {
		inner.SetPreTurnHook(hook)
	}
	a.onNewSession = func(sessionID string) {
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

	memStore, memWorker, closeMemory := setupMemory(ctx, o, *cfg, orch, func() { a.summarizeSessions() })
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
//
// When a session summarizer is configured, Close writes a summary for every
// session this agent created and marks those sessions completed. Both happen
// inside the memory closer, after the observation worker has drained and before
// the store is closed — see setupMemory for why that order is required.
// Summarization is best-effort: a failure is logged and does not prevent any
// resource from being released.
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

// summarizeSessions writes a summary for each recorded session and completes it.
//
// Only the summary write is gated on a summarizer being configured. Sessions are
// completed either way: leaving them 'active' is what makes a store accumulate
// rows that no reader can distinguish from a session still in progress.
//
// Each summary gets its own deadline rather than sharing one across the loop.
// With a shared budget, a slow first session starves every later one — and worse,
// the completion call would then receive an already-expired context, leaving
// those sessions 'active' precisely because the summary overran. Completion is
// bookkeeping on a local database, so it gets a fresh, short context that cannot
// be consumed by a summary deadline.
func (a *Agent) summarizeSessions() {
	// Snapshot and clear under the lock: a concurrent NewSession must not append
	// to a slice Close is walking, and clearing up front means a second Close
	// cannot re-summarize the same sessions.
	a.memSessionsMu.Lock()
	sessions := a.memSessions
	a.memSessions = nil
	a.memSessionsMu.Unlock()

	if a.memStore == nil || len(sessions) == 0 {
		return
	}

	for _, sessionID := range sessions {
		if a.memSummarizer != nil {
			ctx, cancel := context.WithTimeout(context.Background(), sessionSummaryTimeout)
			err := a.SummarizeSession(ctx, sessionID)
			cancel()
			if err != nil {
				slog.Warn("piagent: session summary failed",
					"session", sessionID, "error", err)
			}
		}

		ctx, cancel := context.WithTimeout(context.Background(), sessionCompleteTimeout)
		err := a.memStore.CompleteSession(ctx, sessionID)
		cancel()
		if err != nil {
			slog.Warn("piagent: completing session failed",
				"session", sessionID, "error", err)
		}
	}
}

// SummarizeSession summarizes one session's recorded observations and stores the
// result, so it can be read back with `pi memory recent`.
//
// It is exported for embedders that want to choose the moment rather than wait
// for [Agent.Close] — a long-lived agent may want a summary per logical unit of
// work, not per process. [Agent.Close] calls it for every session the agent
// created.
//
// It returns an error when memory is off, when no summarizer was configured, or
// when the session has no observations. A session with no observations is not an
// error in the "something broke" sense, but there is no summary to write, so it
// is reported rather than silently succeeding.
func (a *Agent) SummarizeSession(ctx context.Context, sessionID string) error {
	if a.memStore == nil {
		return errors.New("piagent: session summary requires memory — create the agent with WithMemory(true)")
	}
	if a.memSummarizer == nil {
		return errors.New("piagent: session summary requires a summary model — pass WithSessionSummary(m)")
	}

	observations, err := a.memStore.SessionObservations(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("piagent: reading session observations: %w", err)
	}
	if len(observations) == 0 {
		return fmt.Errorf("piagent: session %q has no observations to summarize", sessionID)
	}

	sum, err := a.memSummarizer.SummarizeSession(ctx, sessionID, a.project, observations)
	if err != nil {
		return fmt.Errorf("piagent: summarizing session: %w", err)
	}
	if err := a.memStore.UpsertSummary(ctx, sum); err != nil {
		return fmt.Errorf("piagent: storing session summary: %w", err)
	}
	return nil
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
		a.memSessionsMu.Lock()
		a.memSessions = append(a.memSessions, sessionID)
		a.memSessionsMu.Unlock()
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

// LogPath returns the file the agent writes its session log to — the same
// JSONL stream a `pi` session produces, with tool calls, model output and any
// auto-compaction outcome in it.
//
// It returns "" when no log could be created, which is not fatal: the agent
// runs without one. Callers should treat "" as "no log" rather than as an
// error.
//
// The path is fixed at construction and the file is created then, so this is
// safe to call from the moment [New] returns.
func (a *Agent) LogPath() string {
	if a.sessionLog == nil {
		return ""
	}
	return a.sessionLog.Path()
}

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
// contextWindowFor reports the model's context window in tokens, which is the
// denominator auto-compaction measures its thresholds against.
//
// It cannot consult the model catalog the way the CLI does: looking a model up
// means importing internal/provider, and that is exactly the dependency
// TestPiagentStaysIsolated exists to prevent. So the window is stated rather
// than inferred — [WithContextWindow] first, then context_window in
// ~/.pi-go/config.json.
//
// A zero result leaves compaction off rather than guessing, which is what an
// unknown window means everywhere else in pi-go. An embedder using pimodels
// can pass provider.ContextWindowSizeFor's answer straight into
// [WithContextWindow]; that composition belongs in the caller, which is the
// whole point of the split.
func contextWindowFor(cfg config.Config, o options) int64 {
	if o.contextWindow > 0 {
		return o.contextWindow
	}
	return cfg.ContextWindow
}

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
	cfg      config.Config
	sandbox  *tools.Sandbox
	lspMgr   *lsp.Manager
	provider string
	worker   *memory.Worker
	project  string
	deduper  *tools.ResultDeduper
	meter    *autocompact.Meter
	opts     options
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
		tools.BuildDedupCallback(d.deduper))

	if d.worker != nil {
		afterTool = append(afterTool, memoryObservationCallback(d.worker, d.cfg, d.project))
	}

	// Ahead of the embedder's callbacks: the meter reports what the provider
	// actually billed for, which is not the embedder's to alter.
	afterModel = append(afterModel, autocompact.MeterCallback(d.meter))

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

// resolveSummarizer picks the model that writes compaction summaries: the one
// the embedder named, or the session model when it named none.
//
// Extracted from the [New] body so the choice is testable on its own. Staging
// a real compaction to observe which model ran would mean building a transcript
// past the window, which is far more machinery than the decision warrants.
func resolveSummarizer(o options, session model.LLM) model.LLM {
	if o.summarizer != nil {
		return o.summarizer
	}
	return session
}
