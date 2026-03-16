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

	"github.com/dimetron/pi-go/internal/agent"
	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/extension"
	"github.com/dimetron/pi-go/internal/logger"
	"github.com/dimetron/pi-go/internal/lsp"
	"github.com/dimetron/pi-go/internal/provider"
	"github.com/dimetron/pi-go/internal/rpc"
	pisession "github.com/dimetron/pi-go/internal/session"
	"github.com/dimetron/pi-go/internal/subagent"
	"github.com/dimetron/pi-go/internal/tools"
	"github.com/dimetron/pi-go/internal/tui"
	"google.golang.org/adk/session"
	adktool "google.golang.org/adk/tool"

	"github.com/spf13/cobra"
)

var (
	flagModel    string
	flagMode     string
	flagSession  string
	flagSocket   string
	flagURL      string
	flagContinue bool
	flagSmol     bool
	flagSlow     bool
	flagPlan     bool
	flagSystem   string
)

func newRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pi [prompt]",
		Short: "pi-go coding agent",
		Long:  "A Go coding agent with multi-provider LLM support, tool calling, and interactive TUI.",
		Args:  cobra.ArbitraryArgs,
		RunE:  runRoot,
	}

	cmd.Flags().StringVar(&flagModel, "model", "", "LLM model to use (e.g. claude-sonnet-4-20250514, gpt-4o, gemini-2.5-pro)")
	cmd.Flags().StringVar(&flagMode, "mode", "", "Output mode: interactive, print, json, rpc")
	cmd.Flags().StringVar(&flagSocket, "socket", "/tmp/pi-go.sock", "Unix socket path for RPC mode")
	cmd.Flags().StringVar(&flagSession, "session", "", "Session ID to resume")
	cmd.Flags().StringVar(&flagURL, "url", "", "Alternative base URL for the LLM API endpoint")
	cmd.Flags().BoolVar(&flagContinue, "continue", false, "Continue last session")
	cmd.Flags().BoolVar(&flagSmol, "smol", false, "Use the smol role (fast/cheap model)")
	cmd.Flags().BoolVar(&flagSlow, "slow", false, "Use the slow role (powerful model)")
	cmd.Flags().BoolVar(&flagPlan, "plan", false, "Use the plan role (planning model)")
	cmd.Flags().StringVar(&flagSystem, "system", "", "System instruction (overrides default)")

	return cmd
}

func runRoot(cmd *cobra.Command, args []string) error {
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
	if apiKey == "" && info.Provider != "gemini" && !info.Ollama {
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

	// Create the LLM provider.
	llm, err := provider.NewLLM(cmd.Context(), info, apiKey, baseURL, cfg.ThinkingLevel)
	if err != nil {
		return fmt.Errorf("creating LLM provider: %w", err)
	}

	prompt := strings.Join(args, " ")

	// Build sandbox rooted at current working directory.
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getting working directory: %w", err)
	}
	sandbox, err := tools.NewSandbox(cwd)
	if err != nil {
		return fmt.Errorf("creating sandbox: %w", err)
	}
	defer sandbox.Close()

	// Build core tools.
	coreTools, err := tools.CoreTools(sandbox)
	if err != nil {
		return fmt.Errorf("creating core tools: %w", err)
	}

	// Build subagent orchestrator and agent tool.
	// Detect git repo root for worktree support.
	repoRoot := detectGitRoot(cwd)
	orch := subagent.NewOrchestrator(&cfg, repoRoot)
	defer orch.Shutdown()

	agentTools, err := tools.AgentTools(orch)
	if err != nil {
		return fmt.Errorf("creating agent tools: %w", err)
	}
	coreTools = append(coreTools, agentTools...)

	// Build screen tool for interactive mode (gives LLM access to terminal content).
	var screen *tui.Screen
	if mode == "interactive" {
		screen = &tui.Screen{}
		screenTool, err := tools.NewScreenTool(screen)
		if err != nil {
			return fmt.Errorf("creating screen tool: %w", err)
		}
		coreTools = append(coreTools, screenTool)
	}

	// Load system instruction: --system flag overrides default.
	var instruction string
	if flagSystem != "" {
		instruction = flagSystem
	} else {
		instruction = agent.LoadInstruction(agent.SystemInstruction)
	}

	// Load extensions: hooks, skills, MCP toolsets.
	hooks := convertHooks(cfg.Hooks)
	beforeCBs := extension.BuildBeforeToolCallbacks(hooks)
	afterCBs := extension.BuildAfterToolCallbacks(hooks)

	// Create LSP manager for format-on-write and diagnostics-on-edit.
	lspMgr := lsp.NewManager(nil)
	defer lspMgr.Shutdown()
	afterCBs = append(afterCBs, lsp.BuildLSPAfterToolCallback(lspMgr))

	// Build LSP explicit tools (lsp-diagnostics, lsp-definition, etc.).
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

	// Load skills from global and project directories.
	skillDirs := []string{}
	if homeDir, hErr := os.UserHomeDir(); hErr == nil {
		skillDirs = append(skillDirs, filepath.Join(homeDir, ".pi-go", "skills"))
	}
	skillDirs = append(skillDirs, filepath.Join(".pi-go", "skills"))
	skills, _ := extension.LoadSkills(skillDirs...)
	if len(skills) > 0 {
		instruction += "\n\n# Available Skills\n\n"
		for _, s := range skills {
			instruction += fmt.Sprintf("- /%s: %s\n", s.Name, s.Description)
		}
	}

	// Set up persistent session service.
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("getting home dir: %w", err)
	}
	sessionsDir := filepath.Join(homeDir, ".pi-go", "sessions")
	sessionSvc, err := pisession.NewFileService(sessionsDir)
	if err != nil {
		return fmt.Errorf("creating session service: %w", err)
	}

	// Create the agent with persistent session service and extensions.
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

	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
	defer stop()

	// Resolve session: --continue resumes last, --session resumes specific, else create new.
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

	// Create session logger (always enabled).
	sessionLog, err := logger.New()
	if err != nil {
		fmt.Fprintf(os.Stderr, "pi-go: warning: could not create session log: %v\n", err)
	} else {
		fmt.Fprintf(os.Stderr, "pi-go: session log: %s\n", sessionLog.Path())
	}
	defer sessionLog.Close()
	sessionLog.SessionStart(sessionID, llm.Name(), mode)

	// Run the agent based on output mode.
	switch mode {
	case "interactive":
		commitMsgFn := buildCommitMsgFunc(cmd.Context(), cfg)
		return tui.Run(ctx, tui.Config{
			Agent:             ag,
			SessionID:         sessionID,
			ModelName:         llm.Name(),
			ActiveRole:        activeRole,
			Roles:             cfg.Roles,
			SessionService:    sessionSvc,
			WorkDir:           cwd,
			Orchestrator:      orch,
			GenerateCommitMsg: commitMsgFn,
			Logger:            sessionLog,
			Screen:            screen,
		})
	case "rpc":
		srv := rpc.NewServer(rpc.Config{
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
				Role:  string(ev.Content.Role),
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
	baseURL := ""
	baseURLs := config.BaseURLs()
	baseURL = baseURLs[info.Provider]
	if baseURL == "" && info.Ollama {
		baseURL = "http://localhost:11434"
	}

	llm, err := provider.NewLLM(ctx, info, apiKey, baseURL, "none")
	if err != nil {
		return nil
	}

	return tui.GenerateCommitMsgFunc(llm)
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

// Execute runs the root command.
func Execute() error {
	return newRootCmd().Execute()
}
