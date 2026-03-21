package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

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
)

// Version is set at build time via -ldflags.
var Version = "dev"

func newRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "pi [prompt]",
		Short:   "pi-go coding agent",
		Long:    "A Go coding agent with multi-provider LLM support, tool calling, and interactive TUI.",
		Version: Version,
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
	cmd.Flags().BoolVar(&flagInsecure, "insecure", false, "Skip TLS certificate verification for LLM API calls")
	cmd.Flags().BoolVar(&flagMemoryOff, "memory-off", false, "Disable the persistent memory system for this session")

	cmd.AddCommand(newPingCmd())
	cmd.AddCommand(newAuditCmd())

	return cmd
}

func runRoot(cmd *cobra.Command, args []string) error {
	// Load API keys from ~/.pi-go/.env (set by /login command).
	loadDotEnv()

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// Resolve model: CLI flag overrides default role.
	if flagModel != "" {
		cfg.Roles["default"] = config.RoleConfig{Model: flagModel}
	}

	// Determine active role from flags.
	activeRole := "default"
	switch {
	case flagSmol:
		activeRole = "smol"
	case flagSlow:
		activeRole = "slow"
	case flagPlan:
		activeRole = "plan"
	}

	modelName, providerName, err := cfg.ResolveRole(activeRole)
	if err != nil {
		return fmt.Errorf("resolving model role: %w", err)
	}

	mode := flagMode
	if mode == "" {
		mode = detectMode()
	}

	info, err := provider.Resolve(modelName)
	// If config explicitly set a provider, use it over auto-detection.
	if err == nil && providerName != "" {
		info.Provider = providerName
	}
	if err != nil {
		return fmt.Errorf("resolving model: %w", err)
	}

	keys := config.APIKeys()
	apiKey := keys[info.Provider]
	if apiKey == "" && info.Provider != "gemini" && info.Provider != "ollama" && !info.Ollama {
		envVar := providerEnvVar(info.Provider)
		return fmt.Errorf("no API key found for provider %q (set %s)", info.Provider, envVar)
	}

	// Resolve base URL: --url flag takes precedence over env var, then Ollama default.
	baseURL := flagURL
	if baseURL == "" {
		baseURLs := config.BaseURLs()
		baseURL = baseURLs[info.Provider]
	}
	if baseURL == "" && info.Ollama {
		baseURL = "http://localhost:11434"
	}

	// Check Ollama is online before proceeding.
	if info.Ollama {
		if err := provider.CheckOllama(baseURL); err != nil {
			return fmt.Errorf("ollama health check: %w", err)
		}
	}

	// Build LLM options: extra headers + insecure TLS.
	llmOpts := &provider.LLMOptions{
		ExtraHeaders:    mergeExtraHeaders(cfg.ExtraHeaders, flagHeaders),
		InsecureSkipTLS: cfg.InsecureSkipTLS || flagInsecure,
	}

	// Create the LLM provider.
	llm, err := provider.NewLLM(cmd.Context(), info, apiKey, baseURL, cfg.ThinkingLevel, llmOpts)
	if err != nil {
		return fmt.Errorf("creating LLM provider: %w", err)
	}

	// Create token usage tracker and wrap LLM with guardrail.
	tokenTracker := guardrail.New(cfg.MaxDailyTokens)
	llm = guardrail.WrapModel(llm, tokenTracker)

	prompt := strings.Join(args, " ")

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getting working directory: %w", err)
	}
	sandboxRoot := os.Getenv("PI_SANDBOX_ROOT")
	if sandboxRoot == "" {
		sandboxRoot = cwd
	}

	// Resolve --continue early (before TUI) so errors surface immediately.
	if flagContinue {
		homeDir, hErr := os.UserHomeDir()
		if hErr != nil {
			return fmt.Errorf("getting home dir: %w", hErr)
		}
		sessionsDir := filepath.Join(homeDir, ".pi-go", "sessions")
		sessionSvc, sErr := pisession.NewFileService(sessionsDir)
		if sErr != nil {
			return fmt.Errorf("creating session service: %w", sErr)
		}
		lastID := sessionSvc.LastSessionID(agent.AppName, agent.DefaultUserID)
		if lastID == "" {
			return fmt.Errorf("no previous session found to continue")
		}
		flagSession = lastID
	}

	// Interactive mode: show TUI immediately, initialize in background.
	if mode == "interactive" {
		return runInteractive(cmd.Context(), cfg, llm, info, tokenTracker, activeRole, cwd, sandboxRoot)
	}

	// Non-interactive modes: synchronous initialization.
	return runNonInteractive(cmd.Context(), cmd, cfg, llm, info, tokenTracker, cwd, sandboxRoot, mode, prompt)
}

// runNonInteractive performs synchronous initialization and runs print/json/rpc modes.
func runNonInteractive(
	parentCtx context.Context,
	cmd *cobra.Command,
	cfg config.Config,
	llm adkmodel.LLM,
	info provider.Info,
	tokenTracker *guardrail.Tracker,
	cwd, sandboxRoot, mode, prompt string,
) error {
	sandbox, err := tools.NewSandbox(sandboxRoot)
	if err != nil {
		return fmt.Errorf("creating sandbox: %w", err)
	}
	defer func() { _ = sandbox.Close() }()

	coreTools, err := tools.CoreTools(sandbox)
	if err != nil {
		return fmt.Errorf("creating core tools: %w", err)
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
	orch := subagent.NewOrchestrator(&cfg, repoRoot, agentConfigs)
	defer orch.Shutdown()

	agentEventCh := make(chan tui.AgentSubEvent, 128)
	agentEventCB := func(agentID, eventType, content string) {
		select {
		case agentEventCh <- tui.AgentSubEvent{AgentID: agentID, Kind: eventType, Content: content}:
		default:
		}
	}
	agentTools, err := tools.AgentTools(orch, agentEventCB)
	if err != nil {
		return fmt.Errorf("creating agent tools: %w", err)
	}
	coreTools = append(coreTools, agentTools...)

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

	var instruction string
	if flagSystem != "" {
		instruction = flagSystem
	} else {
		instruction = agent.LoadInstruction(agent.SystemInstruction)
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
			}
		}
		var err error
		mcpToolsets, err = extension.BuildMCPToolsets(mcpServers)
		if err != nil {
			fmt.Fprintf(os.Stderr, "pi-go: warning: MCP setup failed: %v\n", err)
		}
	}

	skillDirs := []string{}
	if homeDir, hErr := os.UserHomeDir(); hErr == nil {
		skillDirs = append(skillDirs, filepath.Join(homeDir, ".pi-go", "skills"))
	}
	skillDirs = append(skillDirs,
		filepath.Join(".pi-go", "skills"),
		filepath.Join(".claude", "skills"),
		filepath.Join(".cursor", "skills"),
	)
	skills, _ := extension.LoadSkills(skillDirs...)
	if len(skills) > 0 {
		instruction += "\n\n# Available Skills\n\n"
		for _, s := range skills {
			instruction += fmt.Sprintf("- /%s: %s\n", s.Name, s.Description)
		}
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
		Toolsets:            mcpToolsets,
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
	case "gemini":
		return "GOOGLE_API_KEY"
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
				fmt.Fprintf(os.Stderr, "⚙ tool: %s\n", part.FunctionCall.Name)
				log.ToolCall(ev.Author, part.FunctionCall.Name, part.FunctionCall.Args)
			}
			if part.FunctionResponse != nil {
				fmt.Fprintf(os.Stderr, "✓ tool: %s done\n", part.FunctionResponse.Name)
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
				Type:  "message_start",
				Agent: ev.Author,
				Role:  ev.Content.Role,
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
	_ = enc.Encode(jsonEvent{Type: "message_end"})
	return nil
}

// buildCommitMsgFunc creates the GenerateCommitMsg callback for /commit.
// It resolves the "commit" role (falling back to "default") and creates a one-shot LLM.
func buildCommitMsgFunc(ctx context.Context, cfg config.Config) func(context.Context, string) (string, error) {
	// Resolve commit role, fall back to default.
	commitModel, commitProvider, err := cfg.ResolveRole("commit")
	if err != nil {
		commitModel, commitProvider, err = cfg.ResolveRole("default")
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

	keys := config.APIKeys()
	apiKey := keys[info.Provider]
	baseURLs := config.BaseURLs()
	baseURL := baseURLs[info.Provider]
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

// loadDotEnv loads environment variables from ~/.pi-go/.env.
// This file is written by the /login command.
func loadDotEnv() {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	data, err := os.ReadFile(filepath.Join(home, ".pi-go", ".env"))
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
		// Don't override existing env vars.
		if os.Getenv(key) == "" {
			_ = os.Setenv(key, val)
		}
	}
}

// Execute runs the root command.
func Execute() error {
	return newRootCmd().Execute()
}
