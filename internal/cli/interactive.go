package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	adkagent "google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	adkmodel "google.golang.org/adk/v2/model"
	adktool "google.golang.org/adk/v2/tool"

	"github.com/dimetron/pi-go/internal/agent"
	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/extension"
	"github.com/dimetron/pi-go/internal/guardrail"
	"github.com/dimetron/pi-go/internal/httplog"
	"github.com/dimetron/pi-go/internal/logger"
	"github.com/dimetron/pi-go/internal/lsp"
	"github.com/dimetron/pi-go/internal/memory"
	"github.com/dimetron/pi-go/internal/provider"
	pisession "github.com/dimetron/pi-go/internal/session"
	"github.com/dimetron/pi-go/internal/subagent"
	"github.com/dimetron/pi-go/internal/tools"
	"github.com/dimetron/pi-go/internal/tui"
)

// initResources tracks resources created during deferred init for cleanup.
type initResources struct {
	sandbox    *tools.Sandbox
	lspMgr     *lsp.Manager
	orch       *subagent.Orchestrator
	memStore   memory.Store
	memWorker  *memory.Worker
	sessionLog *logger.Logger
	sessionID  string // captured for resume hint on exit
	bashSup    *tools.BashSupervisor
}

func (r *initResources) cleanup() {
	// Stop backgrounded shell commands first. Nothing else owns them, so a
	// command still running when the session ends is a leaked process — the
	// exact failure this supervisor was introduced to prevent.
	if r.bashSup != nil {
		r.bashSup.KillAll()
	}
	if r.sessionLog != nil {
		// Detach before closing: a late trace from an in-flight streaming body
		// would otherwise write into a closed file.
		httplog.SetSink(nil)
		_ = r.sessionLog.Close()
	}
	if r.memWorker != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = r.memWorker.Shutdown(ctx)
	}
	if r.memStore != nil {
		_ = r.memStore.Close()
	}
	if r.lspMgr != nil {
		r.lspMgr.Shutdown()
	}
	if r.orch != nil {
		r.orch.Shutdown()
	}
	if r.sandbox != nil {
		_ = r.sandbox.Close()
	}
}

// runInteractive starts the TUI immediately and performs heavy initialization
// in a background goroutine, reporting progress via InitEvent channel.
func runInteractive(
	ctx context.Context,
	cfg config.Config,
	llm adkmodel.LLM,
	info provider.Info,
	tokenTracker *guardrail.Tracker,
	activeRole, cwd, sandboxRoot, worktreeDir string,
) error {
	initCh := make(chan tui.InitEvent, 32)

	var res initResources
	initDone := make(chan struct{})

	// Create a child context so deferred init is canceled when the TUI exits.
	initCtx, initCancel := context.WithCancel(ctx)

	go func() {
		defer close(initDone)
		defer close(initCh)
		deferredInit(initCtx, cfg, llm, info.Provider, info.BaseURL, tokenTracker, cwd, sandboxRoot, worktreeDir, initCh, &res)
	}()

	tuiErr := tui.Run(ctx, tui.Config{
		LLM:            llm,
		AppVersion:     versionString(),
		ModelName:      llm.Name(),
		ProviderName:   info.Provider,
		ThinkingLevel:  cfg.ThinkingLevel,
		ActiveRole:     activeRole,
		Roles:          cfg.Roles,
		WorkDir:        cwd,
		ThemeName:      cfg.Theme,
		TokenTracker:   tokenTracker,
		LifecycleHooks: convertHooks(cfg.Hooks),
		DeferredInit:   initCh,
		ModelSwitcher: func(switchCtx context.Context, modelName string) (adkmodel.LLM, string, string, error) {
			return buildSwitchedLLM(switchCtx, cfg, tokenTracker, modelName)
		},
	})

	initCancel() // signal deferred init to stop
	<-initDone

	// Print session ID and resume command on exit.
	if res.sessionID != "" {
		fmt.Fprintf(os.Stderr, "\nSession: %s\nResume:  pi --session %s\n", res.sessionID, res.sessionID)
	}

	res.cleanup()
	return tuiErr
}

// deferredInit performs all heavy initialization, sending progress via ch.
// Resources that need cleanup are stored in res.
func deferredInit(
	ctx context.Context,
	cfg config.Config,
	llm adkmodel.LLM,
	providerName string,
	baseURL string,
	tokenTracker *guardrail.Tracker,
	cwd, sandboxRoot, worktreeDir string,
	ch chan<- tui.InitEvent,
	res *initResources,
) {
	initTotal := deferredInitTotal(cfg)
	send := func(item string, done bool) {
		ch <- tui.InitEvent{Item: item, Done: done, Total: initTotal}
	}
	fail := func(err error) {
		ch <- tui.InitEvent{Err: err}
	}

	// --- Phase 1: Core tools (fast, needed by everything) ---
	send("tools", false)

	coreTools, err := deferredInitCoreTools(sandboxRoot, worktreeDir, res)
	if err != nil {
		fail(err)
		return
	}
	sandbox, bashSup := res.sandbox, res.bashSup

	send("tools", true)

	// --- Phase 2: Parallel subsystems ---
	ps := runDeferredInitPhase2(ctx, cfg, cwd, send)

	// --- Phase 3: Sequential finalization ---
	send("agent", false)

	// Store cleanup resources.
	res.lspMgr = ps.lspMgr

	// Build orchestrator (needs git results).
	orch := subagent.NewOrchestrator(&cfg, ps.repoRoot, ps.agentConfigs)
	orch.SetProviderOptions(flagURL, flagInsecure, flagHeaders)
	res.orch = orch

	// Build agent event channel and tools.
	agentEventCh := make(chan tui.AgentSubEvent, 128)
	agentEventCB := func(agentID, eventType, content string) {
		select {
		case agentEventCh <- tui.AgentSubEvent{AgentID: agentID, Kind: eventType, Content: content}:
		default:
		}
	}
	agentTools, _ := tools.AgentTools(orch, agentEventCB)
	coreTools = append(coreTools, agentTools...)

	// Stream live shell output to the same channel the subagent cards use. The
	// prefix keeps the two streams apart; the non-blocking send in agentEventCB
	// is what keeps a slow UI from stalling a running command.
	bashSup.SetSink(func(execID, kind, content string) {
		agentEventCB(execID, tui.BashEventKind(kind), content)
	})

	// Append LSP tools.
	if ps.lspTools != nil {
		coreTools = append(coreTools, ps.lspTools...)
	}

	coreTools, memStore, memRecorder := appendDeferredMemoryTools(cfg, cwd, coreTools)

	// Build system instruction. The parts are kept so the context gauge can
	// attribute overhead to each section; composing them here is what keeps the
	// breakdown honest — instruction is literally parts.String().
	instructionParts := buildDeferredInstructionParts()
	instruction := instructionParts.String()

	cbs := buildDeferredCallbacks(cfg, providerName, sandbox, ps.lspMgr, memRecorder)

	// Session service.
	sessionsPath, sessionSvc, err := openSessionService()
	if err != nil {
		fail(err)
		return
	}

	mcpToolsets := ps.mcpToolsets
	// llms.txt documentation sources are cheap to build (no network), so they
	// attach synchronously here rather than through the deferred loader that
	// handles MCP servers. Without this the fetch_docs tool exists in one-shot
	// and piagent modes but never in the interactive TUI. It goes into
	// coreTools rather than mcpToolsets: the context gauge's toolsetBytes only
	// counts *extension.resilientToolset entries, so a local toolset there
	// would be live for the model yet invisible in the breakdown, and the MCP
	// panel would list a non-MCP source.
	if cfg.LLMS != nil && len(cfg.LLMS.Sources) > 0 {
		coreTools = append(coreTools, tools.LLMSTools(tools.NewLLMSCachedToolset(cfg.LLMS))...)
	}
	// Gemini search grounding (see agent.GeminiGroundingTool doc).
	//
	// APPEND — never replace. Assigning coreTools = []adktool.Tool{gTool} here
	// leaves the agent with *no* tools at all: bash, read, write, edit, grep,
	// ls, subagent, LSP and memory all vanish, and the model, given no function
	// declarations, invents names like "execute_command" and gets back
	// "tool not found. Available tools: " with an empty list.
	//
	// The built-in search coexists with function declarations: geminitool's
	// ProcessRequest appends to req.Config.Tools rather than overwriting it, and
	// a single Gemini turn will happily call `read` and `google_search` both.
	if gTool, ok := agent.GeminiGroundingTool(providerName); ok {
		coreTools = append(coreTools, gTool)
	}

	// Session logger. Created before the agent so it can capture the agent's
	// non-fatal diagnostics (e.g. unresolved instruction placeholders) in the
	// session log instead of leaking them to stderr and corrupting the TUI.
	// SessionStart is deferred until the session ID is resolved below.
	sessionLog, logErr := logger.New()
	if logErr == nil {
		res.sessionLog = sessionLog
		// --trace-http entries are dropped until the log file exists; the
		// transport is built before this point. See the matching note in
		// cli.go. Detached on shutdown where sessionLog is closed.
		httplog.SetSink(logger.HTTPSink(sessionLog))
	}

	// Create agent.
	ag, err := agent.New(agent.Config{
		Model:                llm,
		Tools:                coreTools,
		Toolsets:             mcpToolsets,
		Instruction:          instruction,
		SessionService:       sessionSvc,
		BeforeToolCallbacks:  cbs.beforeTool,
		AfterToolCallbacks:   cbs.afterTool,
		BeforeModelCallbacks: cbs.beforeModel,
		AfterModelCallbacks:  cbs.afterModel,
		Logger:               sessionLog,
	})
	if err != nil {
		fail(fmt.Errorf("creating agent: %w", err))
		return
	}

	sessionID, defaultTitle, resumed, err := resolveDeferredSession(ctx, ag, sessionSvc, llm, providerName, baseURL)
	if err != nil {
		fail(err)
		return
	}

	// Store session ID for resume hint on exit.
	res.sessionID = sessionID

	// Two-stage auto-compaction, installed as a pre-turn hook so history is
	// only ever rewritten between turns. Buffered so a compaction notice never
	// blocks the turn if the TUI is momentarily busy.
	noticeCh := make(chan string, 8)
	if hook := buildAutoCompactHook(autoCompactDeps{
		SessionSvc:    sessionSvc,
		Tracker:       tokenTracker,
		Deduper:       cbs.deduper,
		Cfg:           autoCompactConfigFrom(cfg),
		Log:           sessionLog,
		SummarizerLLM: llm,
		Notify: func(msg string) {
			select {
			case noticeCh <- msg:
			default:
			}
		},
	}); hook != nil {
		ag.SetPreTurnHook(hook)
	}

	// Capture ACP subagent events (claude, gemini) under the session dir.
	res.orch.SetACPLogPath(filepath.Join(sessionsPath, sessionID, "acp.jsonl"))

	// Session logger was created above; record the session start now that the
	// session ID is known.
	if logErr == nil {
		sessionLog.SessionStart(sessionID, llm.Name(), "interactive")
	}

	// Commit message function.
	commitMsgFn := buildCommitMsgFunc(ctx, cfg)

	send("agent", true)

	// Send final result.
	ch <- tui.InitEvent{
		Done: true,
		Result: &tui.InitResult{
			Agent:             ag,
			SessionID:         sessionID,
			SessionTitle:      defaultTitle,
			Resumed:           resumed,
			SessionService:    sessionSvc,
			Orchestrator:      orch,
			Logger:            sessionLog,
			Skills:            ps.skills,
			SkillDirs:         ps.skillDirs,
			GenerateCommitMsg: commitMsgFn,
			AgentEventCh:      agentEventCh,
			SystemNoticeCh:    noticeCh,
			ContextBreakdown: buildContextBreakdown(
				instructionParts, coreTools, mcpToolsets, ps.skills, ps.agentConfigs),
			TokenTracker:   tokenTracker,
			CompactMetrics: cbs.compactMetrics,
			GitBranch:      ps.gitBranch,
			DiffAdded:      ps.diffAdded,
			DiffRemoved:    ps.diffRemoved,
			MCPToolsets:    ps.mcpToolsets,
			MCPServers:     buildMCPServerConfigs(cfg),
		},
	}

	if memStore != nil {
		initMemoryAfterUI(ctx, cfg, cwd, sessionID, orch, memStore, memRecorder, res)
	}
}

// deferredInitCoreTools builds the sandbox, the bash supervisor and the core
// tool set. Both the sandbox and the supervisor are recorded on res as soon as
// they exist, so a later failure here still leaves them for cleanup to close.
func deferredInitCoreTools(sandboxRoot, worktreeDir string, res *initResources) ([]adktool.Tool, error) {
	sandbox, err := tools.NewSandbox(sandboxRoot, worktreeDir)
	if err != nil {
		return nil, fmt.Errorf("creating sandbox: %w", err)
	}
	res.sandbox = sandbox

	// Allow agent tools to access ~/.pi-go/ (logs, sessions, config).
	if home, hErr := os.UserHomeDir(); hErr == nil {
		_ = sandbox.AddExtraDir(filepath.Join(home, ".pi-go"))
	}

	// The supervisor is built here, before the UI event channel exists, because
	// the bash tool needs it at construction time. Its sink is attached later,
	// once there is somewhere to stream to.
	bashSup := tools.NewBashSupervisor()
	res.bashSup = bashSup

	coreTools, err := tools.CoreTools(sandbox, tools.WithBashSupervisor(bashSup))
	if err != nil {
		return nil, fmt.Errorf("creating core tools: %w", err)
	}
	bashCtlTools, err := tools.BashControlTools(bashSup)
	if err != nil {
		return nil, fmt.Errorf("creating bash control tools: %w", err)
	}
	return append(coreTools, bashCtlTools...), nil
}

// deferredParallelState collects what the phase-2 goroutines discover. Fields
// written by more than one goroutine are guarded by mu.
type deferredParallelState struct {
	mu sync.Mutex

	// Git + subagents
	repoRoot     string
	agentConfigs []subagent.AgentConfig
	gitBranch    string
	diffAdded    int
	diffRemoved  int

	// LSP
	lspMgr   *lsp.Manager
	lspTools []adktool.Tool

	// MCP
	mcpToolsets []adktool.Toolset

	// Skills
	skills    []extension.Skill
	skillDirs []string
}

// runDeferredInitPhase2 discovers git state, LSP servers, MCP toolsets and
// skills concurrently and returns once all four are done.
func runDeferredInitPhase2(ctx context.Context, cfg config.Config, cwd string, send func(item string, done bool)) *deferredParallelState {
	var ps deferredParallelState
	var wg sync.WaitGroup

	// Git + subagent discovery
	wg.Add(1)
	go func() {
		defer wg.Done()
		send("git", false)
		ps.repoRoot = detectGitRoot(ctx, cwd)
		discovery, _ := subagent.DiscoverAgents(cwd, subagent.ScopeBoth)
		if discovery != nil {
			ps.agentConfigs = discovery.All
		}
		ps.gitBranch = detectBranch(ctx, cwd)
		ps.diffAdded, ps.diffRemoved = computeDiffStats(ctx, cwd)
		send("git", true)
	}()

	// LSP
	wg.Add(1)
	go func() {
		defer wg.Done()
		send("lsp", false)
		mgr := lsp.NewManager(nil)
		// Only advertise the LSP tools when a server can actually answer them —
		// see the matching note in cli.go. The manager itself is always kept:
		// the after-tool callback and diagnostics plumbing cost nothing idle.
		var lt []adktool.Tool
		if mgr.AnyAvailable() {
			lt, _ = tools.LSPToolsFor(mgr, resolveLSPMode())
		}
		ps.mu.Lock()
		ps.lspMgr = mgr
		ps.lspTools = lt
		ps.mu.Unlock()
		send("lsp", true)
	}()

	// MCP
	wg.Add(1)
	go func() {
		defer wg.Done()
		if cfg.MCP == nil || len(cfg.MCP.Servers) == 0 {
			return
		}
		send("mcp", false)
		ts, _ := extension.BuildMCPToolsets(buildMCPServerConfigs(cfg))
		ps.mu.Lock()
		ps.mcpToolsets = ts
		ps.mu.Unlock()
		send("mcp", true)
	}()

	// Skills
	wg.Add(1)
	go func() {
		defer wg.Done()
		send("skills", false)
		dirs := extension.DefaultSkillDirs()
		sk, _ := extension.LoadSkills(dirs...)
		ps.mu.Lock()
		ps.skills = sk
		ps.skillDirs = dirs
		ps.mu.Unlock()
		send("skills", true)
	}()

	wg.Wait()
	return &ps
}

// appendDeferredMemoryTools adds the memory tools when memory is enabled and
// returns the lazy store and observation recorder they run against. Both are
// nil when memory is off, which is what gates the post-UI memory init.
func appendDeferredMemoryTools(cfg config.Config, cwd string, coreTools []adktool.Tool) ([]adktool.Tool, *lazyMemoryStore, *deferredMemoryRecorder) {
	if !deferredMemoryEnabled(cfg) {
		return coreTools, nil, nil
	}
	memStore := newLazyMemoryStore()
	if memTools, memErr := tools.MemoryTools(memStore); memErr == nil {
		coreTools = append(coreTools, memTools...)
	}
	return coreTools, memStore, newDeferredMemoryRecorder(cfg, cwd)
}

// buildDeferredInstructionParts returns the system instruction as its parts:
// --system replaces the base outright, otherwise the built-in set is loaded.
func buildDeferredInstructionParts() agent.InstructionParts {
	if flagSystem != "" {
		return agent.InstructionParts{Base: flagSystem}
	}
	return agent.LoadInstructionParts(agent.SystemInstruction)
}

// deferredCallbacks is the callback wiring for the interactive agent, plus the
// two objects the caller still needs a handle on: the deduper the auto-compact
// hook shares, and the metrics the context gauge reads.
type deferredCallbacks struct {
	beforeTool     []llmagent.BeforeToolCallback
	afterTool      []llmagent.AfterToolCallback
	beforeModel    []llmagent.BeforeModelCallback
	afterModel     []llmagent.AfterModelCallback
	deduper        *tools.ResultDeduper
	compactMetrics *tools.CompactMetrics
}

// buildDeferredCallbacks assembles the tool and model callback chains in the
// order they must run.
func buildDeferredCallbacks(
	cfg config.Config,
	providerName string,
	sandbox *tools.Sandbox,
	lspMgr *lsp.Manager,
	memRecorder *deferredMemoryRecorder,
) deferredCallbacks {
	compactorCfg := compactorConfigFrom(cfg)
	compactMetrics := tools.NewCompactMetrics()
	compactorCB := tools.BuildCompactorCallback(compactorCfg, compactMetrics)
	resultDeduper := tools.NewResultDeduper()

	hooks := convertHooks(cfg.Hooks)
	beforeCBs := extension.BuildBeforeToolCallbacks(hooks)
	afterCBs := extension.BuildAfterToolCallbacks(hooks)

	// Always add OTEL tracing callbacks so all tool calls are traced.
	tracingBefore, tracingAfter := extension.BuildTracingCallbacks()
	beforeCBs = append(beforeCBs, tracingBefore...)
	afterCBs = append(afterCBs, tracingAfter...)
	if lspMgr != nil {
		afterCBs = append(afterCBs, lsp.BuildLSPAfterToolCallback(lspMgr))
	}
	// Dedup runs after the compactor so both calls are compared in their final,
	// post-compaction form.
	afterCBs = append(afterCBs, compactorCB, tools.BuildDedupCallback(resultDeduper))

	// LLM tracing: before/after model callbacks emit spans per LLM invocation.
	llmBefore, llmAfter := extension.BuildLLMTracingCallbacks(providerName)

	// Inject image bytes (screenshots) as visible InlineData parts for the model.
	llmBefore = append(llmBefore, extension.BuildReadImageCallback(sandbox))

	if memRecorder != nil {
		afterCBs = append(afterCBs, memRecorder.afterTool)
	}

	return deferredCallbacks{
		beforeTool:     beforeCBs,
		afterTool:      afterCBs,
		beforeModel:    llmBefore,
		afterModel:     llmAfter,
		deduper:        resultDeduper,
		compactMetrics: compactMetrics,
	}
}

// resolveDeferredSession returns the session to run in — the one named by
// --continue/--session if there is one, otherwise a fresh one — and records the
// model and backend it is running under.
func resolveDeferredSession(
	ctx context.Context,
	ag *agent.Agent,
	sessionSvc *pisession.FileService,
	llm adkmodel.LLM,
	providerName, baseURL string,
) (sessionID, defaultTitle string, resumed bool, err error) {
	// --continue is resolved in the fast path, which sets flagSession.
	sessionID = flagSession
	resumed = sessionID != ""
	if sessionID == "" {
		sessionID, defaultTitle, err = ag.CreateSession(ctx)
		if err != nil {
			return "", "", false, fmt.Errorf("creating session: %w", err)
		}
	}

	// Keep the recorded model honest. meta.Model used to be written once, at
	// creation, so a session resumed under a different model (via --model) kept
	// advertising the old one — and the next resume would restore that instead
	// of what the session actually last ran with.
	if resumed {
		_ = sessionSvc.SetSessionModel(sessionID, llm.Name()) // best-effort metadata
	}
	// Record the backend for every session, resumed or fresh. The model name on
	// its own does not identify what actually served the request, which is the
	// first thing anyone needs when reading a transcript back.
	_ = sessionSvc.SetSessionProvider(sessionID, providerName, baseURL) // best-effort metadata

	return sessionID, defaultTitle, resumed, nil
}

type lazyMemoryStore struct {
	mu    sync.RWMutex
	ready chan struct{}
	store memory.Store
	err   error
}

func newLazyMemoryStore() *lazyMemoryStore {
	return &lazyMemoryStore{ready: make(chan struct{})}
}

func (s *lazyMemoryStore) setReady(store memory.Store, err error) {
	s.mu.Lock()
	s.store = store
	s.err = err
	s.mu.Unlock()
	close(s.ready)
}

func (s *lazyMemoryStore) wait(ctx context.Context) (memory.Store, error) {
	select {
	case <-s.ready:
		s.mu.RLock()
		defer s.mu.RUnlock()
		if s.err != nil {
			return nil, s.err
		}
		if s.store == nil {
			return nil, fmt.Errorf("memory store unavailable")
		}
		return s.store, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *lazyMemoryStore) CreateSession(ctx context.Context, sess *memory.Session) error {
	store, err := s.wait(ctx)
	if err != nil {
		return err
	}
	return store.CreateSession(ctx, sess)
}

func (s *lazyMemoryStore) CompleteSession(ctx context.Context, sessionID string) error {
	store, err := s.wait(ctx)
	if err != nil {
		return err
	}
	return store.CompleteSession(ctx, sessionID)
}

func (s *lazyMemoryStore) InsertObservation(ctx context.Context, obs *memory.Observation) error {
	store, err := s.wait(ctx)
	if err != nil {
		return err
	}
	return store.InsertObservation(ctx, obs)
}

func (s *lazyMemoryStore) GetObservations(ctx context.Context, ids []int64) ([]*memory.Observation, error) {
	store, err := s.wait(ctx)
	if err != nil {
		return nil, err
	}
	return store.GetObservations(ctx, ids)
}

func (s *lazyMemoryStore) RecentObservations(ctx context.Context, project string, limit int) ([]*memory.Observation, error) {
	store, err := s.wait(ctx)
	if err != nil {
		return nil, err
	}
	return store.RecentObservations(ctx, project, limit)
}

func (s *lazyMemoryStore) UpsertSummary(ctx context.Context, sum *memory.SessionSummary) error {
	store, err := s.wait(ctx)
	if err != nil {
		return err
	}
	return store.UpsertSummary(ctx, sum)
}

func (s *lazyMemoryStore) RecentSummaries(ctx context.Context, project string, limit int) ([]*memory.SessionSummary, error) {
	store, err := s.wait(ctx)
	if err != nil {
		return nil, err
	}
	return store.RecentSummaries(ctx, project, limit)
}

func (s *lazyMemoryStore) Search(ctx context.Context, q memory.SearchQuery) (*memory.SearchResult, error) {
	store, err := s.wait(ctx)
	if err != nil {
		return nil, err
	}
	return store.Search(ctx, q)
}

func (s *lazyMemoryStore) Timeline(ctx context.Context, anchorID int64, before, after int) ([]*memory.Observation, error) {
	store, err := s.wait(ctx)
	if err != nil {
		return nil, err
	}
	return store.Timeline(ctx, anchorID, before, after)
}

func (s *lazyMemoryStore) Close() error {
	return nil
}

type deferredMemoryRecorder struct {
	mu            sync.RWMutex
	project       string
	excludedTools map[string]bool
	sessionID     string
	worker        *memory.Worker
}

func newDeferredMemoryRecorder(cfg config.Config, project string) *deferredMemoryRecorder {
	excluded := make(map[string]bool)
	if cfg.Memory != nil {
		for _, name := range cfg.Memory.ExcludedTools {
			excluded[name] = true
		}
	}
	return &deferredMemoryRecorder{
		project:       project,
		excludedTools: excluded,
	}
}

func (r *deferredMemoryRecorder) setReady(sessionID string, worker *memory.Worker) {
	r.mu.Lock()
	r.sessionID = sessionID
	r.worker = worker
	r.mu.Unlock()
}

func (r *deferredMemoryRecorder) afterTool(_ adkagent.Context, t adktool.Tool, args, result map[string]any, toolErr error) (map[string]any, error) {
	if toolErr != nil {
		return result, nil
	}

	name := t.Name()
	r.mu.RLock()
	worker := r.worker
	sessionID := r.sessionID
	excluded := r.excludedTools[name]
	project := r.project
	r.mu.RUnlock()

	if worker == nil || sessionID == "" || excluded {
		return result, nil
	}

	worker.Enqueue(memory.RawObservation{
		SessionID:  sessionID,
		Project:    project,
		ToolName:   name,
		ToolInput:  args,
		ToolOutput: result,
		Timestamp:  time.Now(),
	})
	return result, nil
}

func initMemoryAfterUI(
	ctx context.Context,
	cfg config.Config,
	cwd string,
	sessionID string,
	orch *subagent.Orchestrator,
	store *lazyMemoryStore,
	recorder *deferredMemoryRecorder,
	res *initResources,
) {
	memCfg := deferredMemoryConfig(cfg)
	dbPath := deferredMemoryDBPath(memCfg)
	if dbPath == "" {
		store.setReady(nil, fmt.Errorf("memory init: home directory unavailable"))
		return
	}

	memDB, err := memory.OpenDB(dbPath)
	if err != nil {
		store.setReady(nil, fmt.Errorf("memory init: %w", err))
		return
	}

	memStore := memory.NewSQLiteStore(memDB)
	_ = memStore.CreateSession(ctx, &memory.Session{
		SessionID: sessionID,
		Project:   cwd,
		StartedAt: time.Now(),
		Status:    "active",
	})

	worker := memory.NewWorker(memStore, memory.NewSubagentCompressor(orch), memCfg.MaxPending)
	worker.Start(ctx)

	res.memStore = memStore
	res.memWorker = worker
	if recorder != nil {
		recorder.setReady(sessionID, worker)
	}
	store.setReady(memStore, nil)
}

func deferredMemoryConfig(cfg config.Config) config.MemoryConfig {
	memCfg := config.MemoryDefaults()
	if cfg.Memory == nil {
		return memCfg
	}
	if cfg.Memory.DBPath != "" {
		memCfg.DBPath = cfg.Memory.DBPath
	}
	if cfg.Memory.TokenBudget > 0 {
		memCfg.TokenBudget = cfg.Memory.TokenBudget
	}
	if cfg.Memory.MaxPending > 0 {
		memCfg.MaxPending = cfg.Memory.MaxPending
	}
	if cfg.Memory.LookbackHours > 0 {
		memCfg.LookbackHours = cfg.Memory.LookbackHours //nolint:govet // reserved for future use
	}
	return memCfg
}

func deferredMemoryDBPath(memCfg config.MemoryConfig) string {
	if memCfg.DBPath != "" {
		return memCfg.DBPath
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".pi-go", "memory", "claude-mem.db")
}

func deferredInitTotal(cfg config.Config) int {
	total := 5 // tools, git, lsp, skills, agent
	if cfg.MCP != nil && len(cfg.MCP.Servers) > 0 {
		total++
	}
	if deferredMemoryEnabled(cfg) {
		total++
	}
	return total
}

func deferredMemoryEnabled(cfg config.Config) bool {
	if flagMemoryOff {
		return false
	}
	return cfg.Memory == nil || cfg.Memory.Enabled == nil || *cfg.Memory.Enabled
}

// detectBranch returns the current git branch name.
func detectBranch(ctx context.Context, workDir string) string {
	ctx, cancel := context.WithTimeout(ctx, gitCmdTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--abbrev-ref", "HEAD")
	if workDir != "" {
		cmd.Dir = workDir
	}
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// computeDiffStats returns added and removed line counts from git diff,
// including lines from untracked files.
func computeDiffStats(ctx context.Context, cwd string) (added, removed int) {
	diffCtx, cancel := context.WithTimeout(ctx, gitCmdTimeout)
	defer cancel()
	cmd := exec.CommandContext(diffCtx, "git", "diff", "--numstat", "HEAD")
	cmd.Dir = cwd
	out, err := cmd.Output()
	if err != nil {
		return 0, 0
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		var a, r int
		if _, err := fmt.Sscanf(line, "%d\t%d\t", &a, &r); err == nil {
			added += a
			removed += r
		}
	}
	added += countUntrackedLines(ctx, cwd)
	return added, removed
}

// countUntrackedLines counts total lines across untracked files. The whole
// operation (ls-files plus one wc per file) shares a single bounded timeout
// so a large or stalled untracked-file set can't hang the init pipeline.
func countUntrackedLines(ctx context.Context, cwd string) int {
	ctx, cancel := context.WithTimeout(ctx, gitCmdTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "ls-files", "--others", "--exclude-standard")
	cmd.Dir = cwd
	out, err := cmd.Output()
	if err != nil {
		return 0
	}
	total := 0
	for _, file := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if file == "" {
			continue
		}
		wc := exec.CommandContext(ctx, "wc", "-l", file)
		wc.Dir = cwd
		wcOut, err := wc.Output()
		if err != nil {
			continue
		}
		var lines int
		if _, err := fmt.Sscanf(strings.TrimSpace(string(wcOut)), "%d", &lines); err == nil {
			total += lines
		}
	}
	return total
}

// buildMCPServerConfigs converts config.MCPServer slice to extension.MCPServerConfig slice.
func buildMCPServerConfigs(cfg config.Config) []extension.MCPServerConfig {
	if cfg.MCP == nil {
		return nil
	}
	out := make([]extension.MCPServerConfig, len(cfg.MCP.Servers))
	for i, s := range cfg.MCP.Servers {
		out[i] = extension.MCPServerConfig{
			Name:    s.Name,
			Command: s.Command,
			Args:    s.Args,
			URL:     s.URL,
			Headers: s.Headers,
			OAuth:   s.OAuth,
		}
	}
	return out
}

// buildSwitchedLLM creates a new LLM instance for the given model name using
// the current config and token tracker. It resolves the provider, validates
// the model, creates the LLM, updates the token tracker's context window size,
// and wraps it with the guardrail. Used by the TUI /model <name> command.
func buildSwitchedLLM(ctx context.Context, cfg config.Config, tokenTracker *guardrail.Tracker, modelName string) (adkmodel.LLM, string, string, error) {
	// Try to auto-detect provider from config's default role.
	providerName := ""
	if rc, ok := cfg.Roles["default"]; ok && rc.Provider != "" {
		providerName = rc.Provider
	}

	baseURL := flagURL
	if baseURL == "" && providerName != "" {
		baseURLs := cfg.ResolveBaseURLs()
		baseURL = baseURLs[providerName]
	}

	info, err := provider.ResolveWithBaseURL(modelName, baseURL)
	if err != nil {
		return nil, "", "", fmt.Errorf("resolving model: %w", err)
	}
	if providerName != "" {
		info.Provider = providerName
		info.Custom = baseURL != ""
	}
	if baseURL == "" {
		baseURLs := cfg.ResolveBaseURLs()
		baseURL = baseURLs[info.Provider]
		if baseURL != "" {
			info.Custom = true
		}
	}
	if err := provider.ValidateModel(info); err != nil {
		return nil, "", "", fmt.Errorf("model validation: %w", err)
	}

	keys := config.APIKeys()
	apiKey := keys[info.Provider]

	if info.Ollama {
		baseURL = provider.ResolveOllamaEndpoint(info.Model, baseURL)
	}

	// Record the endpoint actually chosen, after every fallback above has had
	// its say, so session metadata names the backend rather than leaving the
	// model name to be interpreted.
	info.BaseURL = baseURL

	llmOpts := &provider.LLMOptions{
		ExtraHeaders: mergeExtraHeaders(cfg.ExtraHeaders, flagHeaders),
	}
	applyTransportOptions(llmOpts, cfg)
	llm, err := provider.NewLLM(ctx, info, apiKey, baseURL, cfg.ThinkingLevel, llmOpts)
	if err != nil {
		return nil, "", "", fmt.Errorf("creating LLM: %w", err)
	}

	// Update context window size on the existing token tracker.
	ctxWindowSize := provider.ContextWindowSizeFor(info.Provider, info.Model)
	if info.Ollama {
		if n := provider.OllamaContextWindowSize(ctx, baseURL, info.Model); n > 0 {
			ctxWindowSize = n
		}
	}
	if info.Provider == "openrouter" {
		if n := provider.OpenRouterContextWindowSize(ctx, baseURL, info.Model); n > 0 {
			ctxWindowSize = n
		}
	}
	// An explicit config value wins: the embedded catalog does not cover every
	// provider's models, and auto-compaction needs a real window to work from.
	if cfg.ContextWindow > 0 {
		ctxWindowSize = cfg.ContextWindow
	}
	tokenTracker.SetContextWindowSize(ctxWindowSize)
	llm = guardrail.WrapModel(llm, tokenTracker)

	return llm, info.Model, info.Provider, nil
}
