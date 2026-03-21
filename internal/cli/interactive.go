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

	adkmodel "google.golang.org/adk/model"
	adktool "google.golang.org/adk/tool"

	"github.com/dimetron/pi-go/internal/agent"
	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/extension"
	"github.com/dimetron/pi-go/internal/guardrail"
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
}

func (r *initResources) cleanup() {
	if r.sessionLog != nil {
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
	activeRole, cwd, sandboxRoot string,
) error {
	initCh := make(chan tui.InitEvent, 32)

	var res initResources
	initDone := make(chan struct{})

	// Create a child context so deferred init is canceled when the TUI exits.
	initCtx, initCancel := context.WithCancel(ctx)

	go func() {
		defer close(initDone)
		defer close(initCh)
		deferredInit(initCtx, cfg, llm, tokenTracker, cwd, sandboxRoot, initCh, &res)
	}()

	tuiErr := tui.Run(ctx, tui.Config{
		LLM:          llm,
		ModelName:    llm.Name(),
		ProviderName: info.Provider,
		ActiveRole:   activeRole,
		Roles:        cfg.Roles,
		WorkDir:      cwd,
		ThemeName:    cfg.Theme,
		TokenTracker: tokenTracker,
		DeferredInit: initCh,
	})

	initCancel() // signal deferred init to stop
	<-initDone
	res.cleanup()
	return tuiErr
}

// deferredInit performs all heavy initialization, sending progress via ch.
// Resources that need cleanup are stored in res.
func deferredInit(
	ctx context.Context,
	cfg config.Config,
	llm adkmodel.LLM,
	tokenTracker *guardrail.Tracker,
	cwd, sandboxRoot string,
	ch chan<- tui.InitEvent,
	res *initResources,
) {
	send := func(item string, done bool) {
		ch <- tui.InitEvent{Item: item, Done: done}
	}
	fail := func(err error) {
		ch <- tui.InitEvent{Err: err}
	}

	// --- Phase 1: Core tools (fast, needed by everything) ---
	send("tools", false)

	sandbox, err := tools.NewSandbox(sandboxRoot)
	if err != nil {
		fail(fmt.Errorf("creating sandbox: %w", err))
		return
	}
	res.sandbox = sandbox

	coreTools, err := tools.CoreTools(sandbox)
	if err != nil {
		fail(fmt.Errorf("creating core tools: %w", err))
		return
	}

	screen := &tui.Screen{}
	screenTool, err := tools.NewScreenTool(screen)
	if err != nil {
		fail(fmt.Errorf("creating screen tool: %w", err))
		return
	}
	coreTools = append(coreTools, screenTool)

	restartCh := make(chan struct{}, 1)
	restartTool, err := tools.NewRestartTool(func() {
		select {
		case restartCh <- struct{}{}:
		default:
		}
	})
	if err != nil {
		fail(fmt.Errorf("creating restart tool: %w", err))
		return
	}
	coreTools = append(coreTools, restartTool)
	send("tools", true)

	// --- Phase 2: Parallel subsystems ---
	type parallelState struct {
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

		// Memory (DB + tools opened in parallel; worker created in sequential phase)
		memStore      memory.Store
		memTools      []adktool.Tool
		memContext    string
		memMaxPending int

		// MCP
		mcpToolsets []adktool.Toolset

		// Skills
		skills    []extension.Skill
		skillDirs []string
	}
	var ps parallelState
	var wg sync.WaitGroup

	// Git + subagent discovery
	wg.Add(1)
	go func() {
		defer wg.Done()
		send("git", false)
		ps.repoRoot = detectGitRoot(cwd)
		discovery, _ := subagent.DiscoverAgents(cwd, subagent.ScopeBoth)
		if discovery != nil {
			ps.agentConfigs = discovery.All
		}
		ps.gitBranch = detectBranch(cwd)
		ps.diffAdded, ps.diffRemoved = computeDiffStats(cwd)
		send("git", true)
	}()

	// LSP
	wg.Add(1)
	go func() {
		defer wg.Done()
		send("lsp", false)
		mgr := lsp.NewManager(nil)
		lt, _ := tools.LSPTools(mgr)
		ps.mu.Lock()
		ps.lspMgr = mgr
		ps.lspTools = lt
		ps.mu.Unlock()
		send("lsp", true)
	}()

	// Memory (DB open + tools + context gen; worker created after orchestrator)
	wg.Add(1)
	go func() {
		defer wg.Done()
		memEnabled := !flagMemoryOff && (cfg.Memory == nil || cfg.Memory.Enabled == nil || *cfg.Memory.Enabled)
		if !memEnabled {
			return
		}
		send("memory", false)
		memCfg := config.MemoryDefaults()
		if cfg.Memory != nil {
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
		}
		dbPath := memCfg.DBPath
		if dbPath == "" {
			if home, hErr := os.UserHomeDir(); hErr == nil {
				dbPath = filepath.Join(home, ".pi-go", "memory", "claude-mem.db")
			}
		}
		memDB, memErr := memory.OpenDB(dbPath)
		if memErr != nil {
			send("memory", true) // non-fatal
			return
		}
		store := memory.NewSQLiteStore(memDB)
		mt, _ := tools.MemoryTools(store)

		var memCtx string
		tokenBudget := memCfg.TokenBudget
		if cfg.Memory != nil && cfg.Memory.TokenBudget > 0 {
			tokenBudget = cfg.Memory.TokenBudget
		}
		ctxGen := memory.NewContextGenerator(store, tokenBudget)
		memCtx, _ = ctxGen.Generate(ctx, cwd)

		ps.mu.Lock()
		ps.memStore = store
		ps.memTools = mt
		ps.memContext = memCtx
		ps.memMaxPending = memCfg.MaxPending
		ps.mu.Unlock()
		send("memory", true)
	}()

	// MCP
	wg.Add(1)
	go func() {
		defer wg.Done()
		if cfg.MCP == nil || len(cfg.MCP.Servers) == 0 {
			return
		}
		send("mcp", false)
		mcpServers := make([]extension.MCPServerConfig, len(cfg.MCP.Servers))
		for i, s := range cfg.MCP.Servers {
			mcpServers[i] = extension.MCPServerConfig{
				Name:    s.Name,
				Command: s.Command,
				Args:    s.Args,
			}
		}
		ts, _ := extension.BuildMCPToolsets(mcpServers)
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
		dirs := []string{}
		if homeDir, hErr := os.UserHomeDir(); hErr == nil {
			dirs = append(dirs, filepath.Join(homeDir, ".pi-go", "skills"))
		}
		dirs = append(dirs,
			filepath.Join(".pi-go", "skills"),
			filepath.Join(".claude", "skills"),
			filepath.Join(".cursor", "skills"),
		)
		sk, _ := extension.LoadSkills(dirs...)
		ps.mu.Lock()
		ps.skills = sk
		ps.skillDirs = dirs
		ps.mu.Unlock()
		send("skills", true)
	}()

	wg.Wait()

	// --- Phase 3: Sequential finalization ---
	send("agent", false)

	// Store cleanup resources.
	res.lspMgr = ps.lspMgr
	res.memStore = ps.memStore

	// Build orchestrator (needs git results).
	orch := subagent.NewOrchestrator(&cfg, ps.repoRoot, ps.agentConfigs)
	res.orch = orch

	// Create memory worker now that orchestrator is available.
	var memWorker *memory.Worker
	if ps.memStore != nil {
		compressor := memory.NewSubagentCompressor(orch)
		memWorker = memory.NewWorker(ps.memStore, compressor, ps.memMaxPending)
		memWorker.Start(ctx)
		res.memWorker = memWorker
	}

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

	// Append LSP tools.
	if ps.lspTools != nil {
		coreTools = append(coreTools, ps.lspTools...)
	}

	// Append memory tools.
	if ps.memTools != nil {
		coreTools = append(coreTools, ps.memTools...)
	}

	// Build system instruction.
	var instruction string
	if flagSystem != "" {
		instruction = flagSystem
	} else {
		instruction = agent.LoadInstruction(agent.SystemInstruction)
	}
	if len(ps.skills) > 0 {
		instruction += "\n\n# Available Skills\n\n"
		for _, s := range ps.skills {
			instruction += fmt.Sprintf("- /%s: %s\n", s.Name, s.Description)
		}
	}
	if ps.memContext != "" {
		instruction += "\n\n" + ps.memContext
	}

	// Build callbacks.
	compactorCfg := tools.DefaultCompactorConfig()
	if cfg.Compactor != nil {
		if cfg.Compactor.Enabled != nil {
			compactorCfg.Enabled = *cfg.Compactor.Enabled
		}
		if cfg.Compactor.SourceCodeFiltering != "" {
			compactorCfg.SourceCodeFiltering = cfg.Compactor.SourceCodeFiltering
		}
		if cfg.Compactor.MaxChars > 0 {
			compactorCfg.MaxChars = cfg.Compactor.MaxChars
		}
		if cfg.Compactor.MaxLines > 0 {
			compactorCfg.MaxLines = cfg.Compactor.MaxLines
		}
	}
	compactMetrics := tools.NewCompactMetrics()
	compactorCB := tools.BuildCompactorCallback(compactorCfg, compactMetrics)

	hooks := convertHooks(cfg.Hooks)
	beforeCBs := extension.BuildBeforeToolCallbacks(hooks)
	afterCBs := extension.BuildAfterToolCallbacks(hooks)
	if ps.lspMgr != nil {
		afterCBs = append(afterCBs, lsp.BuildLSPAfterToolCallback(ps.lspMgr))
	}
	afterCBs = append(afterCBs, compactorCB)

	// Memory after-tool callback.
	var memSessionID string
	if memWorker != nil {
		var excludedTools map[string]bool
		if cfg.Memory != nil && len(cfg.Memory.ExcludedTools) > 0 {
			excludedTools = make(map[string]bool, len(cfg.Memory.ExcludedTools))
			for _, t := range cfg.Memory.ExcludedTools {
				excludedTools[t] = true
			}
		}
		afterCBs = append(afterCBs, func(_ adktool.Context, t adktool.Tool, args, result map[string]any, toolErr error) (map[string]any, error) {
			if toolErr != nil || memSessionID == "" {
				return result, nil
			}
			name := t.Name()
			if excludedTools[name] {
				return result, nil
			}
			memWorker.Enqueue(memory.RawObservation{
				SessionID:  memSessionID,
				Project:    cwd,
				ToolName:   name,
				ToolInput:  args,
				ToolOutput: result,
				Timestamp:  time.Now(),
			})
			return result, nil
		})
	}

	// Session service.
	homeDir, err := os.UserHomeDir()
	if err != nil {
		fail(fmt.Errorf("getting home dir: %w", err))
		return
	}
	sessionsDir := filepath.Join(homeDir, ".pi-go", "sessions")
	sessionSvc, err := pisession.NewFileService(sessionsDir)
	if err != nil {
		fail(fmt.Errorf("creating session service: %w", err))
		return
	}

	// Create agent.
	ag, err := agent.New(agent.Config{
		Model:               llm,
		Tools:               coreTools,
		Toolsets:            ps.mcpToolsets,
		Instruction:         instruction,
		SessionService:      sessionSvc,
		BeforeToolCallbacks: beforeCBs,
		AfterToolCallbacks:  afterCBs,
	})
	if err != nil {
		fail(fmt.Errorf("creating agent: %w", err))
		return
	}

	// Resolve session (--continue is resolved in fast path, flagSession is set).
	sessionID := flagSession
	if sessionID == "" {
		sessionID, err = ag.CreateSession(ctx)
		if err != nil {
			fail(fmt.Errorf("creating session: %w", err))
			return
		}
	}

	// Activate memory capture.
	if ps.memStore != nil {
		memSessionID = sessionID
		_ = ps.memStore.CreateSession(ctx, &memory.Session{
			SessionID: sessionID,
			Project:   cwd,
			StartedAt: time.Now(),
			Status:    "active",
		})
	}

	// Session logger.
	sessionLog, logErr := logger.New()
	if logErr == nil {
		res.sessionLog = sessionLog
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
			SessionService:    sessionSvc,
			Orchestrator:      orch,
			Logger:            sessionLog,
			Skills:            ps.skills,
			SkillDirs:         ps.skillDirs,
			GenerateCommitMsg: commitMsgFn,
			AgentEventCh:      agentEventCh,
			TokenTracker:      tokenTracker,
			CompactMetrics:    compactMetrics,
			RestartCh:         restartCh,
			Screen:            screen,
			GitBranch:         ps.gitBranch,
			DiffAdded:         ps.diffAdded,
			DiffRemoved:       ps.diffRemoved,
		},
	}
}

// detectBranch returns the current git branch name.
func detectBranch(workDir string) string {
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
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
func computeDiffStats(cwd string) (added, removed int) {
	cmd := exec.Command("git", "diff", "--numstat", "HEAD")
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
	added += countUntrackedLines(cwd)
	return added, removed
}

// countUntrackedLines counts total lines across untracked files.
func countUntrackedLines(cwd string) int {
	cmd := exec.Command("git", "ls-files", "--others", "--exclude-standard")
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
		wc := exec.Command("wc", "-l", file)
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
