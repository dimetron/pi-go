package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"net/http"
	_ "net/http/pprof" // registers pprof HTTP handlers on /debug/pprof
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	adkagent "google.golang.org/adk/agent"
	adkmodel "google.golang.org/adk/model"
	"google.golang.org/adk/session"
	adktool "google.golang.org/adk/tool"

	"github.com/dimetron/pi-go/internal/agent"
	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/extension"
	"github.com/dimetron/pi-go/internal/guardrail"
	"github.com/dimetron/pi-go/internal/jsonrpc"
	"github.com/dimetron/pi-go/internal/logger"
	"github.com/dimetron/pi-go/internal/lsp"
	"github.com/dimetron/pi-go/internal/memory"
	"github.com/dimetron/pi-go/internal/palace"
	"github.com/dimetron/pi-go/internal/provider"
	pisession "github.com/dimetron/pi-go/internal/session"
	"github.com/dimetron/pi-go/internal/subagent"
	"github.com/dimetron/pi-go/internal/tools"
	"github.com/dimetron/pi-go/internal/tui"

	"github.com/spf13/cobra"
)

var (
	flagModel   string
	flagMode    string
	flagSession string
	flagSocket  string
	flagURL     string
	flagHeaders []string

	flagContinue  bool
	flagInsecure  bool
	flagSmol      bool
	flagSlow      bool
	flagPlan      bool
	flagMemoryOff bool
	flagSystem    string
	flagPprof     string
	flagPprofPort string

	// lastSessionFile persists the last session start metadata across invocations.
	// Used to detect rapid restart loops (e.g. print mode crashes).
	lastSessionFile = filepath.Join(os.Getenv("HOME"), ".pi-go", "last-session.json")
)

// lastSessionData is written to lastSessionFile on each print-mode start.
type lastSessionData struct {
	Timestamp time.Time `json:"timestamp"`
	SessionID string    `json:"session_id"`
	WorkDir   string    `json:"work_dir"`
	Model     string    `json:"model"`
}

// Version and BuildTag are set at build time via -ldflags.
var (
	Version  = "dev"
	BuildTag = ""
)

func versionString() string {
	if BuildTag == "" {
		return Version
	}
	return Version + "+" + BuildTag
}

func newRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "pi [prompt]",
		Short:   "pi-go coding agent",
		Long:    "A Go coding agent with multi-provider LLM support, tool calling, and interactive TUI.",
		Version: versionString(),
		Args:    cobra.ArbitraryArgs,
		RunE:    runRoot,
	}

	cmd.Flags().StringVar(&flagModel, "model", "", "LLM model to use (e.g. claude-sonnet-4-6, gpt-4o, gemini-2.5-pro)")
	cmd.Flags().StringVar(&flagMode, "mode", "", "Output mode: interactive, print, json, rpc")
	cmd.Flags().StringVar(&flagSocket, "socket", "/tmp/pi-go.sock", "Unix socket path for RPC mode")
	cmd.Flags().StringVar(&flagSession, "session", "", "Session ID to resume")
	cmd.Flags().StringVar(&flagURL, "url", "", "Alternative base URL for the LLM API endpoint")
	cmd.Flags().BoolVar(&flagContinue, "continue", false, "Continue last session")
	cmd.Flags().BoolVar(&flagSmol, "smol", false, "Use the smol role (fast/cheap model)")
	cmd.Flags().BoolVar(&flagSlow, "slow", false, "Use the slow role (powerful model)")
	cmd.Flags().BoolVar(&flagPlan, "plan", false, "Use the plan role (planning model)")
	cmd.Flags().StringVar(&flagSystem, "system", "", "System instruction (overrides default)")
	cmd.Flags().StringArrayVar(&flagHeaders, "header", nil, "Extra HTTP header for LLM requests (key=value, repeatable)")
	// Allow bare --header so a following flag is not consumed as a header value.
	if f := cmd.Flags().Lookup("header"); f != nil {
		f.NoOptDefVal = ""
	}
	cmd.Flags().BoolVar(&flagInsecure, "insecure", false, "Skip TLS certificate verification for LLM API calls")
	cmd.Flags().BoolVar(&flagMemoryOff, "memory-off", false, "Disable the persistent memory system for this session")
	cmd.Flags().StringVar(&flagPprof, "pprof", "", "Enable pprof profiling (cpu, mem, goroutine, mutex, block, trace)")
	cmd.Flags().StringVar(&flagPprofPort, "pprof-port", "6060", "Port for pprof HTTP server (default 6060)")

	cmd.AddCommand(newPingCmd())
	cmd.AddCommand(newAuditCmd())
	cmd.AddCommand(newServeCmd())
	cmd.AddCommand(newMemoryCmd())
	cmd.AddCommand(newLoginCmd())
	cmd.AddCommand(newACPServerCmd())
	cmd.AddCommand(newUpgradeCmd())

	return cmd
}

type rootRuntime struct {
	cfg          config.Config
	llm          adkmodel.LLM
	info         provider.Info
	tokenTracker *guardrail.Tracker
	activeRole   string
	mode         string
	prompt       string
	cwd          string
	sandboxRoot  string
	worktreeDir  string
}

func resolveActiveRole() string {
	activeRole := "default"
	switch {
	case flagSmol:
		activeRole = "smol"
	case flagSlow:
		activeRole = "slow"
	case flagPlan:
		activeRole = "plan"
	}
	return activeRole
}

func resolveMode() string {
	if flagMode != "" {
		return flagMode
	}
	return detectMode()
}

func loadRootConfig() (config.Config, error) {
	cfg, err := config.Load()
	if err != nil {
		return config.Config{}, fmt.Errorf("loading config: %w", err)
	}
	if flagModel != "" {
		cfg.Roles["default"] = config.RoleConfig{Model: flagModel}
	}
	return cfg, nil
}

func buildRootRuntime(ctx context.Context, args []string) (rootRuntime, error) {
	cfg, err := loadRootConfig()
	if err != nil {
		return rootRuntime{}, err
	}

	activeRole := resolveActiveRole()
	modelName, providerName, advisorModel, advisorMaxUses, advisorCaching, err := cfg.ResolveRole(activeRole)
	if err != nil {
		return rootRuntime{}, fmt.Errorf("resolving model role: %w", err)
	}

	mode := resolveMode()
	baseURL := flagURL
	if baseURL == "" && providerName != "" {
		baseURLs := config.BaseURLs()
		baseURL = baseURLs[providerName]
	}
	info, err := provider.ResolveWithBaseURL(modelName, baseURL)
	if err != nil {
		return rootRuntime{}, fmt.Errorf("resolving model: %w", err)
	}
	if providerName != "" {
		info.Provider = providerName
		info.Custom = baseURL != ""
	}
	if baseURL == "" {
		baseURLs := config.BaseURLs()
		baseURL = baseURLs[info.Provider]
		if baseURL != "" {
			info.Custom = true
		}
	}
	if err := provider.ValidateModel(info); err != nil {
		return rootRuntime{}, fmt.Errorf("model validation: %w", err)
	}

	keys := config.APIKeys()
	apiKey := keys[info.Provider]
	if apiKey == "" && baseURL == "" && info.Provider != "gemini" && info.Provider != "ollama" && info.Provider != "azure" && !info.Ollama {
		envVar := providerEnvVar(info.Provider)
		return rootRuntime{}, fmt.Errorf("no API key found for provider %q (set %s)", info.Provider, envVar)
	}

	if baseURL == "" && info.Ollama {
		baseURL = "http://localhost:11434"
	}
	if info.Ollama {
		if err := provider.CheckOllama(baseURL); err != nil {
			return rootRuntime{}, fmt.Errorf("ollama health check: %w", err)
		}
	}

	llmOpts := &provider.LLMOptions{
		ExtraHeaders:    mergeExtraHeaders(cfg.ExtraHeaders, flagHeaders),
		InsecureSkipTLS: cfg.InsecureSkipTLS || flagInsecure,
		AdvisorModel:    advisorModel,
		AdvisorMaxUses:  advisorMaxUses,
		AdvisorCaching:  advisorCaching,
	}
	llm, err := provider.NewLLM(ctx, info, apiKey, baseURL, cfg.ThinkingLevel, llmOpts)
	if err != nil {
		return rootRuntime{}, fmt.Errorf("creating LLM provider: %w", err)
	}

	tokenTracker := guardrail.New(cfg.MaxDailyTokens)
	ctxWindowSize := provider.ContextWindowSize(info.Model)
	if info.Ollama {
		if n := provider.OllamaContextWindowSize(ctx, baseURL, info.Model); n > 0 {
			ctxWindowSize = n
		}
	}
	tokenTracker.SetContextWindowSize(ctxWindowSize)
	llm = guardrail.WrapModel(llm, tokenTracker)

	cwd, err := os.Getwd()
	if err != nil {
		return rootRuntime{}, fmt.Errorf("getting working directory: %w", err)
	}
	sandboxRoot := os.Getenv("PI_SANDBOX_ROOT")
	if sandboxRoot == "" {
		sandboxRoot = cwd
	}
	worktreeDir := os.Getenv("PI_WORKTREE_ROOT")

	if flagContinue {
		homeDir, hErr := os.UserHomeDir()
		if hErr != nil {
			return rootRuntime{}, fmt.Errorf("getting home dir: %w", hErr)
		}
		sessionsDir := filepath.Join(homeDir, ".pi-go", "sessions")
		sessionSvc, sErr := pisession.NewFileService(sessionsDir)
		if sErr != nil {
			return rootRuntime{}, fmt.Errorf("creating session service: %w", sErr)
		}
		lastID := sessionSvc.LastSessionID(agent.AppName, agent.DefaultUserID)
		if lastID == "" {
			return rootRuntime{}, fmt.Errorf("no previous session found to continue")
		}
		flagSession = lastID
	}

	checkForRapidRestartAndWarn(cwd)
	_ = writeLastSession(cwd, info.Provider, llm.Name())

	return rootRuntime{
		cfg:          cfg,
		llm:          llm,
		info:         info,
		tokenTracker: tokenTracker,
		activeRole:   activeRole,
		mode:         mode,
		prompt:       strings.Join(args, " "),
		cwd:          cwd,
		sandboxRoot:  sandboxRoot,
		worktreeDir:  worktreeDir,
	}, nil
}

func runRoot(cmd *cobra.Command, args []string) error {
	// Load API keys from ~/.pi-go/.env (set by /login command).
	loadDotEnv()

	// Optionally start pprof server.
	if flagPprof != "" {
		addr := ":" + flagPprofPort
		go func() {
			fmt.Printf("pprof server listening on %s (profile: %s)\n", addr, flagPprof)
			switch flagPprof {
			case "cpu":
				fmt.Println("note: cpu profiling started via runtime/pprof; run: go tool pprof http://localhost:" + flagPprofPort + "/profile")
			case "trace":
				fmt.Println("note: trace profiling started via runtime/trace; run: go tool trace http://localhost:" + flagPprofPort + "/trace")
			}
			if err := http.ListenAndServe(addr, nil); err != nil {
				fmt.Fprintf(os.Stderr, "pprof server error: %v\n", err)
			}
		}()
	}

	go checkForUpdate(cmd.Context(), Version)

	runtime, err := buildRootRuntime(cmd.Context(), args)
	if err != nil {
		return err
	}

	if runtime.mode == "interactive" {
		return runInteractive(
			cmd.Context(),
			runtime.cfg,
			runtime.llm,
			runtime.info,
			runtime.tokenTracker,
			runtime.activeRole,
			runtime.cwd,
			runtime.sandboxRoot,
			runtime.worktreeDir,
		)
	}

	return runNonInteractive(
		cmd.Context(),
		cmd,
		runtime.cfg,
		runtime.llm,
		runtime.info,
		runtime.tokenTracker,
		runtime.cwd,
		runtime.sandboxRoot,
		runtime.worktreeDir,
		runtime.mode,
		runtime.prompt,
	)
}

type nonInteractiveRuntime struct {
	sandbox      *tools.Sandbox
	coreTools    []adktool.Tool
	orch         *subagent.Orchestrator
	agentEventCh chan tui.AgentSubEvent
}

func initNonInteractiveRuntime(cfg *config.Config, cwd, sandboxRoot, worktreeDir string) (*nonInteractiveRuntime, error) {
	sandbox, err := tools.NewSandbox(sandboxRoot, worktreeDir)
	if err != nil {
		return nil, fmt.Errorf("creating sandbox: %w", err)
	}

	if home, hErr := os.UserHomeDir(); hErr == nil {
		if aErr := sandbox.AddExtraDir(filepath.Join(home, ".pi-go")); aErr != nil {
			fmt.Fprintf(os.Stderr, "pi-go: warning: could not add ~/.pi-go to sandbox: %v\n", aErr)
		}
	}

	coreTools, err := tools.CoreTools(sandbox)
	if err != nil {
		_ = sandbox.Close()
		return nil, fmt.Errorf("creating core tools: %w", err)
	}

	repoRoot := detectGitRoot(cwd)
	discovery, err := subagent.DiscoverAgents(cwd, subagent.ScopeBoth)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pi-go: warning: agent discovery failed: %v\n", err)
	}
	var agentConfigs []subagent.AgentConfig
	if discovery != nil {
		agentConfigs = discovery.All
	}
	orch := subagent.NewOrchestrator(cfg, repoRoot, agentConfigs)
	orch.SetProviderOptions(flagURL, flagInsecure, flagHeaders)

	agentEventCh := make(chan tui.AgentSubEvent, 128)
	agentEventCB := func(agentID, eventType, content string) {
		select {
		case agentEventCh <- tui.AgentSubEvent{AgentID: agentID, Kind: eventType, Content: content}:
		default:
		}
	}
	agentTools, err := tools.AgentTools(orch, agentEventCB)
	if err != nil {
		orch.Shutdown()
		_ = sandbox.Close()
		return nil, fmt.Errorf("creating agent tools: %w", err)
	}
	coreTools = append(coreTools, agentTools...)

	return &nonInteractiveRuntime{
		sandbox:      sandbox,
		coreTools:    coreTools,
		orch:         orch,
		agentEventCh: agentEventCh,
	}, nil
}

func (r *nonInteractiveRuntime) close() {
	if r == nil {
		return
	}
	if r.orch != nil {
		r.orch.Shutdown()
	}
	if r.sandbox != nil {
		_ = r.sandbox.Close()
	}
}

// runNonInteractive performs synchronous initialization and runs print/json/rpc modes.
func runNonInteractive(
	parentCtx context.Context,
	cmd *cobra.Command,
	cfg config.Config,
	llm adkmodel.LLM,
	info provider.Info,
	tokenTracker *guardrail.Tracker,
	cwd, sandboxRoot, worktreeDir, mode, prompt string,
) error {
	runtime, err := initNonInteractiveRuntime(&cfg, cwd, sandboxRoot, worktreeDir)
	if err != nil {
		return err
	}
	defer runtime.close()

	coreTools := runtime.coreTools
	orch := runtime.orch

	// Initialize memory system.
	var memStore memory.Store
	var memWorker *memory.Worker
	memEnabled := !flagMemoryOff && (cfg.Memory == nil || cfg.Memory.Enabled == nil || *cfg.Memory.Enabled)
	if memEnabled {
		memCfg := config.MemoryDefaults()
		if cfg.Memory != nil {
			if cfg.Memory.DBPath != "" {
				memCfg.DBPath = cfg.Memory.DBPath
			}
			if cfg.Memory.TokenBudget > 0 {
				memCfg.TokenBudget = cfg.Memory.TokenBudget //nolint:govet // used by context generator
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
			fmt.Fprintf(os.Stderr, "pi-go: warning: memory system disabled: %v\n", memErr)
		} else {
			memStore = memory.NewSQLiteStore(memDB)
			compressor := memory.NewSubagentCompressor(orch)
			memWorker = memory.NewWorker(memStore, compressor, memCfg.MaxPending)
			memWorker.Start(parentCtx)
		}
	}
	defer func() {
		if memWorker != nil {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = memWorker.Shutdown(shutdownCtx)
		}
		if memStore != nil {
			_ = memStore.Close()
		}
	}()

	if memStore != nil {
		memTools, memErr := tools.MemoryTools(memStore)
		if memErr != nil {
			fmt.Fprintf(os.Stderr, "pi-go: warning: memory tools disabled: %v\n", memErr)
		} else if memTools != nil {
			coreTools = append(coreTools, memTools...)
		}
	}

	// Initialize palace tools if enabled.
	var palaceContext string
	palaceEnabled := !flagMemoryOff && (cfg.Palace == nil || cfg.Palace.Enabled == nil || *cfg.Palace.Enabled)
	if palaceEnabled {
		palaceCfg := palaceConfigFromCLI(&cfg)
		if palaceCfg.DBPath != "" {
			if _, statErr := os.Stat(palaceCfg.DBPath); statErr == nil {
				p, pErr := palace.New(
					palace.WithDBPath(palaceCfg.DBPath),
					palace.WithModelPath(palaceCfg.ModelPath),
				)
				if pErr != nil {
					fmt.Fprintf(os.Stderr, "pi-go: warning: palace tools disabled: %v\n", pErr)
				} else {
					defer p.Close()
					pTools, ptErr := palace.PalaceTools(p)
					if ptErr != nil {
						fmt.Fprintf(os.Stderr, "pi-go: warning: palace tools disabled: %v\n", ptErr)
					} else if pTools != nil {
						coreTools = append(coreTools, pTools...)
					}
					// Wire observation bridge: auto-file observations as palace drawers.
					if memWorker != nil {
						bridge := palace.NewObservationBridge(p)
						memWorker.OnAfterStore(bridge.ConvertAndStore)
					}
					// Load palace wake-up context for system prompt injection.
					if wakeUp, wErr := p.WakeUp(context.Background(), ""); wErr == nil && wakeUp != "" {
						palaceContext = wakeUp
					}
				}
			}
		}
	}

	var instruction string
	if flagSystem != "" {
		instruction = flagSystem
	} else {
		instruction = agent.LoadInstruction(agent.SystemInstruction)
	}
	if palaceContext != "" {
		instruction += "\n\n## Palace Memory Context\n\n" + palaceContext
	}

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
	compactorCB := tools.BuildCompactorCallback(compactorCfg, tools.NewCompactMetrics())

	hooks := convertHooks(cfg.Hooks)
	beforeCBs := extension.BuildBeforeToolCallbacks(hooks)
	afterCBs := extension.BuildAfterToolCallbacks(hooks)

	lspMgr := lsp.NewManager(nil)
	defer lspMgr.Shutdown()
	afterCBs = append(afterCBs, lsp.BuildLSPAfterToolCallback(lspMgr))
	afterCBs = append(afterCBs, compactorCB)

	var memSessionID string
	if memWorker != nil {
		var excludedTools map[string]bool
		if cfg.Memory != nil && len(cfg.Memory.ExcludedTools) > 0 {
			excludedTools = make(map[string]bool, len(cfg.Memory.ExcludedTools))
			for _, t := range cfg.Memory.ExcludedTools {
				excludedTools[t] = true
			}
		}
		afterCBs = append(afterCBs, func(_ adkagent.ToolContext, t adktool.Tool, args, result map[string]any, toolErr error) (map[string]any, error) {
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

	lspTools, err := tools.LSPTools(lspMgr)
	if err != nil {
		return fmt.Errorf("creating LSP tools: %w", err)
	}
	coreTools = append(coreTools, lspTools...)

	var mcpToolsets []adktool.Toolset
	if cfg.MCP != nil && len(cfg.MCP.Servers) > 0 {
		mcpServers := make([]extension.MCPServerConfig, len(cfg.MCP.Servers))
		for i, s := range cfg.MCP.Servers {
			mcpServers[i] = extension.MCPServerConfig{
				Name:    s.Name,
				Command: s.Command,
				Args:    s.Args,
				URL:     s.URL,
			}
		}
		mcpToolsets, _ = extension.BuildMCPToolsets(mcpServers)
	}

	// Add A2A toolsets if configured
	var a2aToolsets []adktool.Toolset
	if cfg.A2A != nil && len(cfg.A2A.Agents) > 0 {
		a2aToolsets = append(a2aToolsets, tools.NewA2AToolset(cfg.A2A))
	}
	// Merge MCP and A2A toolsets
	allToolsets := append(mcpToolsets, a2aToolsets...)

	skillDirs := extension.DefaultSkillDirs()
	skills, skillErr := extension.LoadSkills(skillDirs...)
	if mode == "print" {
		fmt.Fprint(os.Stderr, formatPrintSkillLoad(len(skills), skillErr))
	}
	if skillErr != nil {
		fmt.Fprintf(os.Stderr, "pi-go: warning: skills disabled: %v\n", skillErr)
	}

	if memStore != nil {
		memCfg := config.MemoryDefaults()
		if cfg.Memory != nil && cfg.Memory.TokenBudget > 0 {
			memCfg.TokenBudget = cfg.Memory.TokenBudget
		}
		ctxGen := memory.NewContextGenerator(memStore, memCfg.TokenBudget)
		memContext, ctxErr := ctxGen.Generate(parentCtx, cwd)
		if ctxErr != nil {
			fmt.Fprintf(os.Stderr, "pi-go: warning: memory context generation failed: %v\n", ctxErr)
		} else if memContext != "" {
			instruction += "\n\n" + memContext
		}
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("getting home dir: %w", err)
	}
	sessionsDir := filepath.Join(homeDir, ".pi-go", "sessions")
	sessionSvc, err := pisession.NewFileService(sessionsDir)
	if err != nil {
		return fmt.Errorf("creating session service: %w", err)
	}

	ag, err := agent.New(agent.Config{
		Model:               llm,
		Tools:               coreTools,
		Toolsets:            allToolsets,
		Instruction:         instruction,
		SessionService:      sessionSvc,
		BeforeToolCallbacks: beforeCBs,
		AfterToolCallbacks:  afterCBs,
	})
	if err != nil {
		return fmt.Errorf("creating agent: %w", err)
	}

	ctx, stop := signal.NotifyContext(parentCtx, os.Interrupt)
	defer stop()

	sessionID := flagSession
	if flagContinue {
		lastID := sessionSvc.LastSessionID(agent.AppName, agent.DefaultUserID)
		if lastID == "" {
			return fmt.Errorf("no previous session found to continue")
		}
		sessionID = lastID
		fmt.Fprintf(os.Stderr, "pi-go: continuing session %s\n", sessionID)
	}
	if sessionID == "" {
		sessionID, err = ag.CreateSession(ctx)
		if err != nil {
			return fmt.Errorf("creating session: %w", err)
		}
	}

	// Capture ACP subagent events (claude, gemini) under the session dir.
	orch.SetACPLogPath(filepath.Join(sessionsDir, sessionID, "acp.jsonl"))

	if memStore != nil {
		memSessionID = sessionID
		_ = memStore.CreateSession(ctx, &memory.Session{
			SessionID: sessionID,
			Project:   cwd,
			StartedAt: time.Now(),
			Status:    "active",
		})
	}

	sessionLog, err := logger.New()
	if err != nil {
		fmt.Fprintf(os.Stderr, "pi-go: warning: could not create session log: %v\n", err)
	}
	defer func() { _ = sessionLog.Close() }()
	sessionLog.SessionStart(sessionID, llm.Name(), mode)

	switch mode {
	case "rpc":
		srv := jsonrpc.NewServer(jsonrpc.Config{
			Agent:      ag,
			SocketPath: flagSocket,
		})
		return srv.Run(ctx)
	case "json":
		if prompt == "" {
			fmt.Fprintf(os.Stderr, "pi-go: no prompt provided (model: %s, mode: %s)\n", llm.Name(), mode)
			return nil
		}
		return runJSON(ctx, ag, sessionID, prompt, sessionLog)
	default:
		if prompt == "" {
			fmt.Fprintf(os.Stderr, "pi-go: no prompt provided (model: %s, mode: %s)\n", llm.Name(), mode)
			return nil
		}
		return runPrint(ctx, ag, sessionID, prompt, sessionLog)
	}
}

func providerEnvVar(p string) string {
	switch p {
	case "anthropic":
		return "ANTHROPIC_API_KEY"
	case "openai":
		return "OPENAI_API_KEY"
	case "azure":
		return "AZURE_OPENAI_API_KEY"
	case "gemini":
		return "GEMINI_API_KEY"
	default:
		return strings.ToUpper(p) + "_API_KEY"
	}
}

// detectMode returns the default output mode based on terminal state.
// If stdin is not a terminal, defaults to "print" for piped input.
func detectMode() string {
	if fi, err := os.Stdin.Stat(); err == nil {
		if (fi.Mode() & os.ModeCharDevice) == 0 {
			return "print"
		}
	}
	return "interactive"
}

// writeLastSession persists session start metadata for rapid-restart detection.
func writeLastSession(workDir, provider, model string) error {
	data := lastSessionData{
		Timestamp: time.Now(),
		WorkDir:   workDir,
		Model:     model,
	}
	blob, err := json.Marshal(data)
	if err != nil {
		return err
	}
	dir := filepath.Dir(lastSessionFile)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(lastSessionFile, blob, 0o600)
}

// readLastSession reads the last session metadata, or nil if unavailable.
func readLastSession() (*lastSessionData, error) {
	data := &lastSessionData{}
	blob, err := os.ReadFile(lastSessionFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(blob, data); err != nil {
		return nil, err
	}
	return data, nil
}

// checkForRapidRestartAndWarn detects if print mode is restarting repeatedly.
// If the same workdir started a session within 3 seconds, it shows the warning.
func checkForRapidRestartAndWarn(workDir string) {
	prev, err := readLastSession()
	if err != nil || prev == nil || prev.WorkDir != workDir {
		return
	}
	elapsed := time.Since(prev.Timestamp)
	if elapsed < 3*time.Second {
		fmt.Fprintf(os.Stderr,
			"pi-go: warning: rapid restart detected (%.0fs since last session). "+
				"If init keeps failing, check ~/.pi-go/log/ for errors.\n",
			elapsed.Seconds())
		path, msg, readErr := lastLoggedError()
		switch {
		case readErr != nil:
			fmt.Fprintf(os.Stderr, "pi-go: warning: failed to inspect session logs: %v\n", readErr)
		case msg != "":
			fmt.Fprintf(os.Stderr, "pi-go: last logged error (%s): %s\n", path, msg)
		default:
			fmt.Fprintln(os.Stderr, "pi-go: no recent logged errors found.")
		}
	}
}

// lastLoggedError returns the most recent "error" entry from session logs.
func lastLoggedError() (path, msg string, err error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", fmt.Errorf("getting home dir: %w", err)
	}
	logRoot := filepath.Join(home, ".pi-go", "log")
	dateDirs, err := os.ReadDir(logRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return "", "", nil
		}
		return "", "", err
	}
	for i := len(dateDirs) - 1; i >= 0; i-- {
		d := dateDirs[i]
		if !d.IsDir() {
			continue
		}
		datePath := filepath.Join(logRoot, d.Name())
		files, listErr := os.ReadDir(datePath)
		if listErr != nil {
			continue
		}
		for j := len(files) - 1; j >= 0; j-- {
			f := files[j]
			if f.IsDir() || !strings.HasPrefix(f.Name(), "session-") || !strings.HasSuffix(f.Name(), ".log") {
				continue
			}
			p := filepath.Join(datePath, f.Name())
			blob, readErr := os.ReadFile(p)
			if readErr != nil {
				continue
			}
			lines := strings.Split(string(blob), "\n")
			for k := len(lines) - 1; k >= 0; k-- {
				line := strings.TrimSpace(lines[k])
				if line == "" {
					continue
				}
				var entry logger.Entry
				if err := json.Unmarshal([]byte(line), &entry); err != nil {
					continue
				}
				if entry.Type == "error" && entry.Content != "" {
					return p, entry.Content, nil
				}
			}
		}
	}
	return "", "", nil
}

func toolArgsPreview(args map[string]any) string {
	if len(args) == 0 {
		return ""
	}
	data, err := json.Marshal(args)
	if err != nil {
		data = []byte(fmt.Sprintf("%v", args))
	}
	preview := string(data)
	const maxToolArgsPreviewLen = 100
	runes := []rune(preview)
	if len(runes) <= maxToolArgsPreviewLen {
		return preview
	}
	return string(runes[:maxToolArgsPreviewLen])
}

const (
	printToolCallColor = "\033[36m"
	printToolDoneColor = "\033[32m"
	printToolDimColor  = "\033[2m"
	printToolReset     = "\033[0m"
)

func formatPrintToolCall(name string, args map[string]any) string {
	if preview := toolArgsPreview(args); preview != "" {
		return fmt.Sprintf("%s🛠️  ⚙ tool: %s %s%s%s\n", printToolCallColor, name, printToolDimColor, preview, printToolReset)
	}
	return fmt.Sprintf("%s🛠️  ⚙ tool: %s%s\n", printToolCallColor, name, printToolReset)
}

func formatPrintToolDone(name string) string {
	return fmt.Sprintf("%s✅ ✓ tool: %s done%s\n", printToolDoneColor, name, printToolReset)
}

func formatPrintSkillLoad(count int, err error) string {
	if err != nil {
		return fmt.Sprintf("%s⚠ skills: failed%s\n", printToolDimColor, printToolReset)
	}
	return fmt.Sprintf("%s✅ ✓ skills: loaded %d%s\n", printToolDoneColor, count, printToolReset)
}

// runPrint runs the agent and prints text responses to stdout.
// Tool calls are shown as status lines on stderr.
func runPrint(ctx context.Context, ag *agent.Agent, sessionID, prompt string, log *logger.Logger) error {
	log.UserMessage(prompt)
	retryCfg := agent.DefaultRetryConfig()
	for ev, err := range agent.WithRetry(retryCfg, func() iter.Seq2[*session.Event, error] {
		return ag.RunStreaming(ctx, sessionID, prompt)
	}) {
		if err != nil {
			if ctx.Err() != nil {
				fmt.Fprintln(os.Stderr, "\ninterrupted")
				return nil
			}
			log.Error(err.Error())
			return fmt.Errorf("agent run: %w", err)
		}
		if ev == nil || ev.Content == nil {
			continue
		}
		for _, part := range ev.Content.Parts {
			if part.Text != "" && ev.Content.Role == "thinking" {
				fmt.Fprintf(os.Stderr, "\033[2m%s\033[0m", part.Text)
				continue
			}
			if part.Text != "" {
				fmt.Print(part.Text)
				log.LLMText(ev.Author, part.Text)
			}
			if part.FunctionCall != nil {
				fmt.Fprint(os.Stderr, formatPrintToolCall(part.FunctionCall.Name, part.FunctionCall.Args))
				log.ToolCall(ev.Author, part.FunctionCall.Name, part.FunctionCall.Args)
			}
			if part.FunctionResponse != nil {
				fmt.Fprint(os.Stderr, formatPrintToolDone(part.FunctionResponse.Name))
				log.ToolResult(ev.Author, part.FunctionResponse.Name, fmt.Sprintf("%v", part.FunctionResponse.Response))
			}
		}
	}
	fmt.Println()
	return nil
}

// jsonEvent represents a JSONL event for JSON output mode.
// Event types follow the spec: message_start, text_delta, tool_call, tool_result, message_end.
type jsonEvent struct {
	Type      string `json:"type"`
	Agent     string `json:"agent,omitempty"`
	Role      string `json:"role,omitempty"`
	Delta     string `json:"delta,omitempty"`
	Content   string `json:"content,omitempty"`
	ToolName  string `json:"tool_name,omitempty"`
	ToolInput any    `json:"tool_input,omitempty"`
	SessionID string `json:"session_id,omitempty"`
}

// runJSON runs the agent and emits JSONL events to stdout.
// Events: message_start (once), text_delta (per text chunk), tool_call, tool_result, message_end (once).
func runJSON(ctx context.Context, ag *agent.Agent, sessionID, prompt string, log *logger.Logger) error {
	log.UserMessage(prompt)
	enc := json.NewEncoder(os.Stdout)
	started := false

	retryCfg := agent.DefaultRetryConfig()
	for ev, err := range agent.WithRetry(retryCfg, func() iter.Seq2[*session.Event, error] {
		return ag.RunStreaming(ctx, sessionID, prompt)
	}) {
		if err != nil {
			if ctx.Err() != nil {
				_ = enc.Encode(jsonEvent{Type: "message_end"})
				return nil
			}
			log.Error(err.Error())
			return fmt.Errorf("agent run: %w", err)
		}
		if ev == nil || ev.Content == nil {
			continue
		}

		// Emit message_start on the first event from the assistant.
		if !started {
			_ = enc.Encode(jsonEvent{
				Type:      "message_start",
				Agent:     ev.Author,
				Role:      ev.Content.Role,
				SessionID: sessionID,
			})
			started = true
		}

		for _, part := range ev.Content.Parts {
			if part.Text != "" && ev.Content.Role == "thinking" {
				_ = enc.Encode(jsonEvent{
					Type:  "thinking_delta",
					Agent: ev.Author,
					Delta: part.Text,
				})
				continue
			}
			if part.Text != "" {
				_ = enc.Encode(jsonEvent{
					Type:  "text_delta",
					Agent: ev.Author,
					Delta: part.Text,
				})
				log.LLMText(ev.Author, part.Text)
			}
			if part.FunctionCall != nil {
				_ = enc.Encode(jsonEvent{
					Type:      "tool_call",
					Agent:     ev.Author,
					ToolName:  part.FunctionCall.Name,
					ToolInput: part.FunctionCall.Args,
				})
				log.ToolCall(ev.Author, part.FunctionCall.Name, part.FunctionCall.Args)
			}
			if part.FunctionResponse != nil {
				_ = enc.Encode(jsonEvent{
					Type:     "tool_result",
					Agent:    ev.Author,
					ToolName: part.FunctionResponse.Name,
					Content:  fmt.Sprintf("%v", part.FunctionResponse.Response),
				})
				log.ToolResult(ev.Author, part.FunctionResponse.Name, fmt.Sprintf("%v", part.FunctionResponse.Response))
			}
		}
	}
	if !started {
		const warn = "pi-go: warning: no assistant events received before message_end"
		fmt.Fprintln(os.Stderr, warn)
		log.Error(warn)
	}
	_ = enc.Encode(jsonEvent{Type: "message_end"})
	return nil
}

// buildCommitMsgFunc creates the GenerateCommitMsg callback for /commit.
// It resolves the "commit" role (falling back to "default") and creates a one-shot LLM.
func buildCommitMsgFunc(ctx context.Context, cfg config.Config) func(context.Context, string) (string, error) {
	// Resolve commit role, fall back to default.
	commitModel, commitProvider, _, _, _, err := cfg.ResolveRole("commit")
	if err != nil {
		commitModel, commitProvider, _, _, _, err = cfg.ResolveRole("default")
		if err != nil {
			return nil // no model available
		}
	}

	info, err := provider.Resolve(commitModel)
	if err != nil {
		return nil
	}
	if commitProvider != "" {
		info.Provider = commitProvider
	}
	if err := provider.ValidateModel(info); err != nil {
		return nil
	}

	keys := config.APIKeys()
	apiKey := keys[info.Provider]
	// Resolve base URL: --url flag takes precedence over env var, then Ollama default.
	baseURL := flagURL
	if baseURL == "" {
		baseURLs := config.BaseURLs()
		baseURL = baseURLs[info.Provider]
	}
	if baseURL == "" && info.Ollama {
		baseURL = "http://localhost:11434"
	}

	if info.Ollama {
		if err := provider.CheckOllama(baseURL); err != nil {
			return nil
		}
	}

	llm, err := provider.NewLLM(ctx, info, apiKey, baseURL, "none", &provider.LLMOptions{
		ExtraHeaders:    cfg.ExtraHeaders,
		InsecureSkipTLS: cfg.InsecureSkipTLS,
	})
	if err != nil {
		return nil
	}

	return tui.GenerateCommitMsgFunc(llm)
}

// mergeExtraHeaders merges config extraHeaders with CLI --header flags.
// CLI flags override config values on key conflict.
func mergeExtraHeaders(cfgHeaders map[string]string, cliHeaders []string) map[string]string {
	if len(cfgHeaders) == 0 && len(cliHeaders) == 0 {
		return nil
	}
	merged := make(map[string]string)
	for k, v := range cfgHeaders {
		merged[k] = v
	}
	for _, h := range cliHeaders {
		key, val, ok := strings.Cut(h, "=")
		if ok {
			merged[strings.TrimSpace(key)] = strings.TrimSpace(val)
		}
	}
	if len(merged) == 0 {
		return nil
	}
	return merged
}

// convertHooks converts config.HookConfig to extension.HookConfig.
func convertHooks(cfgHooks []config.HookConfig) []extension.HookConfig {
	hooks := make([]extension.HookConfig, len(cfgHooks))
	for i, h := range cfgHooks {
		hooks[i] = extension.HookConfig{
			Event:   h.Event,
			Command: h.Command,
			Tools:   h.Tools,
			Timeout: h.Timeout,
		}
	}
	return hooks
}

// detectGitRoot returns the git repository root for the given directory,
// or empty string if not inside a git repo.
func detectGitRoot(dir string) string {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// LoadDotEnv loads environment variables from ~/.pi-go/.env and the nearest
// project .pi-go/.env. Project values override global values and both override
// the inherited shell environment.
func LoadDotEnv() {
	loadDotEnv()
}

// loadDotEnv loads environment variables from ~/.pi-go/.env and project
// .pi-go/.env. These files are written by login/config flows and take
// precedence over the inherited shell environment — a user who ran `/login`
// expects the saved credential to be used even if their shell still exports a
// different API key from earlier. Lines in the files override the process env;
// missing keys fall through to whatever the shell set.
func loadDotEnv() {
	if home, err := os.UserHomeDir(); err == nil {
		loadDotEnvFile(filepath.Join(home, ".pi-go", ".env"))
	}
	if cwd, err := os.Getwd(); err == nil {
		if projectEnv := findNearestDotEnv(cwd); projectEnv != "" {
			loadDotEnvFile(projectEnv)
		}
	}
}

func loadDotEnvFile(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		if val == "" {
			continue
		}
		_ = os.Setenv(key, val)
	}
}

func findNearestDotEnv(start string) string {
	dir, err := filepath.Abs(start)
	if err != nil {
		return ""
	}
	for {
		candidate := filepath.Join(dir, ".pi-go", ".env")
		if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// palaceConfigFromCLI derives palace.Option parameters from the application config.
func palaceConfigFromCLI(cfg *config.Config) struct{ DBPath, ModelPath string } {
	var dbPath, modelPath string
	if cfg.Palace != nil {
		dbPath = cfg.Palace.DBPath
		modelPath = cfg.Palace.ModelPath
	}
	if dbPath == "" {
		if home, err := os.UserHomeDir(); err == nil {
			dbPath = filepath.Join(home, ".pi-go", "palace.db")
		}
	}
	if modelPath == "" {
		if home, err := os.UserHomeDir(); err == nil {
			modelPath = filepath.Join(home, ".pi-go", "models", "KnightsAnalytics_all-MiniLM-L6-v2")
		}
	}
	return struct{ DBPath, ModelPath string }{dbPath, modelPath}
}

// Execute runs the root command.
func Execute() error {
	return newRootCmd().Execute()
}
