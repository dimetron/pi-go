package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"log/slog"
	"maps"
	"net/http"
	_ "net/http/pprof" // registers pprof HTTP handlers on /debug/pprof
	"os"
	"os/signal"
	"path/filepath"
	"runtime/pprof"
	"strings"
	"sync"
	"time"

	adkagent "google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	adkmodel "google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/session"
	adktool "google.golang.org/adk/v2/tool"

	"github.com/dimetron/pi-go/internal/agent"
	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/extension"
	"github.com/dimetron/pi-go/internal/gitroot"
	"github.com/dimetron/pi-go/internal/guardrail"
	"github.com/dimetron/pi-go/internal/httplog"
	"github.com/dimetron/pi-go/internal/jsonrpc"
	"github.com/dimetron/pi-go/internal/logger"
	"github.com/dimetron/pi-go/internal/lsp"
	"github.com/dimetron/pi-go/internal/memory"
	"github.com/dimetron/pi-go/internal/otel"
	"github.com/dimetron/pi-go/internal/palace"
	"github.com/dimetron/pi-go/internal/pirpc"
	"github.com/dimetron/pi-go/internal/provider"
	"github.com/dimetron/pi-go/internal/retry"
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

	// flagSocketChanged records whether --socket was passed explicitly, so
	// the deprecated `--mode rpc --socket` spelling can be distinguished
	// from the default value. Set by runRoot.
	flagSocketChanged bool

	flagContinue     bool
	flagInsecure     bool
	flagCACert       string
	flagSmol         bool
	flagSlow         bool
	flagPlan         bool
	flagMemoryOff    bool
	flagLSP          string
	flagSystem       string
	flagPprof        string
	flagPprofPort    string
	flagCPUProfile   string
	flagTraceHTTP    bool
	flagA2AAddr      string
	flagA2AReadyAddr string

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
		Use:   "pi [prompt]",
		Short: "pi-go coding agent",
		Long: `A Go coding agent with multi-provider LLM support, tool calling, and interactive TUI.

Run with no prompt for the interactive TUI; pass a prompt to answer once and exit.

The provider is inferred from the model name, so --model is usually the only
routing you need:

  claude-*                 Anthropic       ANTHROPIC_API_KEY (or ANTHROPIC_AUTH_TOKEN)
  gpt-*                    OpenAI          OPENAI_API_KEY
  gemini-*                 Google Gemini   GEMINI_API_KEY (or GOOGLE_API_KEY)
  mistral-*, magistral-*   Mistral         MISTRAL_API_KEY
  grok-*                   xAI             XAI_API_KEY
  openrouter/<model>       OpenRouter      OPENROUTER_API_KEY
  agentgateway/<model>     agentgateway    none; http://localhost:4000
  ollama/<model>           Ollama, local   none; http://localhost:11434
  <model>:cloud            Ollama Cloud    OLLAMA_API_KEY; https://api.ollama.com
                                           without a key: the local daemon
  azure/<deployment>       Azure OpenAI    AZURE_OPENAI_API_KEY
  opencode/<model>         OpenCode        OPENCODE_API_KEY

A name with no recognized prefix is rejected rather than guessed at — reach for
the ollama/ prefix or the :cloud suffix to name an Ollama model explicitly.

Set a default in ~/.pi-go/config.json so --model is only needed to deviate;
--smol, --slow and --plan switch between the roles configured there.`,
		Example: `  # Anthropic
  pi --model claude-sonnet-5 "explain what this repo does"

  # OpenAI
  pi --model gpt-5.2 "add a table-driven test for the parser"

  # Google Gemini
  pi --model gemini-3.5-pro "review the diff on this branch"

  # Mistral
  pi --model mistral-large-latest "summarize the changelog"

  # xAI
  pi --model grok-4.6 "trace where this request handler blocks"

  # OpenRouter — any model in the OpenRouter catalog, vendor-prefixed ID
  pi --model openrouter/google/gemini-3.7-flash "compare these two APIs"

  # Ollama against a local daemon — no API key needed
  pi --model ollama/gemma4:e4b "rename this symbol everywhere"

  # Ollama Cloud — the :cloud tag routes to api.ollama.com with OLLAMA_API_KEY
  # set; without one it falls back to the local daemon, which serves cloud
  # models on your "ollama signin" identity. The ollama/ prefix forces local.
  pi --model minimax-m3:cloud "port this module to generics"

  # Azure OpenAI — the deployment name follows azure/
  pi --model azure/my-gpt5-deployment "draft release notes"

  # OpenCode
  pi --model opencode/claude-sonnet-5 "find the goroutine leak"

  # agentgateway — a local OpenAI-compatible gateway, no API key needed
  pi --model agentgateway/deepseek-v4-flash:0731-cloud "draft release notes"

  # Any OpenAI-compatible gateway, with an extra header and a corporate CA
  pi --url https://llm.corp.internal/v1 --model gpt-5.2 \
     --header X-Team=platform --ca-cert /etc/ssl/corp.pem "run the tests"

  # One-shot answer instead of the TUI, and resuming a session
  pi --mode print "what changed in the last commit?"
  pi --continue
  pi --session 01JQ8Z... "carry on where we left off"

  # Diagnosing a provider
  pi ping                                 # DNS/TCP/TLS/HTTP trace, curl -v style
  pi ping --model minimax-m3:cloud        # check one model end to end
  pi --trace-http "why was that rejected?"  # full request/response in the session log`,
		Version: versionString(),
		Args:    cobra.ArbitraryArgs,
		// Start pprof here rather than in runRoot: subcommands (`pi memory mine`,
		// `pi audit`, ...) have their own RunE and never reach runRoot, so
		// profiling them was impossible. PersistentPreRun runs for the root and
		// every subcommand alike.
		PersistentPreRun: func(*cobra.Command, []string) {
			startPprofServer()
			startCPUProfile()
		},
		RunE: runRoot,
	}

	cmd.Flags().StringVar(&flagModel, "model", "", "LLM model to use (e.g. claude-sonnet-5, gpt-5.2, gemini-3.5-pro, ollama/gemma4:e4b, minimax-m3:cloud)")
	cmd.Flags().StringVar(&flagMode, "mode", "", "Output mode: interactive, print, json, socket, rpc")
	cmd.Flags().StringVar(&flagSocket, "socket", "/tmp/pi-go.sock", "Unix socket path for socket mode")
	// pi-acp unconditionally spawns `pi --mode rpc --no-themes`. pi-go has no
	// themes to disable, but rejecting the flag kills the child on spawn and
	// the adapter reports only "stream was destroyed", so accept and ignore.
	cmd.Flags().Bool("no-themes", false, "Accepted for pi CLI compatibility; ignored")
	_ = cmd.Flags().MarkHidden("no-themes")
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
	cmd.Flags().StringVar(&flagCACert, "ca-cert", "", "PEM bundle to trust for LLM API calls, in addition to the system roots")
	cmd.Flags().BoolVar(&flagMemoryOff, "memory-off", false, "Disable the persistent memory system for this session")
	cmd.Flags().StringVar(&flagLSP, "lsp", "min", "Language-server tools: off, min (symbols+diagnostics), or full (all seven)")
	// Persistent, not local: `pi memory mine . --pprof true` and every other
	// subcommand must accept these too. As local flags they were rejected with
	// "unknown flag: --pprof" the moment a subcommand was used.
	cmd.PersistentFlags().StringVar(&flagPprof, "pprof", "", "Enable pprof profiling (serves /debug/pprof; any non-empty value enables it)")
	cmd.PersistentFlags().StringVar(&flagPprofPort, "pprof-port", "6060", "Port for the pprof HTTP server")
	// --cpuprofile writes a runtime CPU profile to the given path for the whole
	// process lifetime. This is the profile PGO consumes: `go build` reads a CPU
	// pprof profile (default.pgo in the main package dir, or -pgo=<path>) to
	// guide inlining and layout. Collect it from a representative workload —
	// the eval-tools suite (`make record-pgo`) — not from a microbenchmark.
	cmd.PersistentFlags().StringVar(&flagCPUProfile, "cpuprofile", "", "Write a CPU profile to this path for the process lifetime (PGO input)")
	// Persistent for the same reason as --pprof: `pi ping --trace-http` and the
	// other subcommands that reach a provider all need it.
	cmd.PersistentFlags().BoolVar(&flagTraceHTTP, "trace-http", false,
		"Log full LLM request/response headers and bodies to the session log and OTel spans (credentials masked; prompts are not)")

	// Append the resolved role table to `pi --help`. A help func set on the
	// root is inherited by every subcommand, so this reproduces the default
	// output and only adds the footer when help was asked for the root itself
	// — `pi audit --help` has no use for it.
	defaultHelp := cmd.HelpFunc()
	cmd.SetHelpFunc(func(c *cobra.Command, args []string) {
		defaultHelp(c, args)
		if c == cmd {
			writeRoleSummary(c.OutOrStdout())
		}
	})

	cmd.AddCommand(newPingCmd())
	cmd.AddCommand(newAuditCmd())
	cmd.AddCommand(newServeCmd())
	cmd.AddCommand(newMemoryCmd())
	cmd.AddCommand(newModelCmd())
	cmd.AddCommand(newLoginCmd())
	cmd.AddCommand(newACPServerCmd())
	cmd.AddCommand(newA2AServerCmd())
	cmd.AddCommand(newUpgradeCmd())
	cmd.AddCommand(newSessionStatsCmd())
	cmd.AddCommand(newVerifyCmd())

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

// resolveRuntimeModel turns the role's model and provider names into a
// validated provider.Info plus the base URL actually used to reach it. An
// explicit --url wins; otherwise the config's per-provider base URL is
// consulted, first under the role's provider name and then under the provider
// the model itself resolved to.
func resolveRuntimeModel(cfg config.Config, modelName, providerName string) (provider.Info, string, error) {
	return resolveRuntimeModelForRole(cfg, modelName, providerName, "")
}

func resolveRuntimeModelForRole(cfg config.Config, modelName, providerName, activeRole string) (provider.Info, string, error) {
	baseURL := flagURL
	resumedProvider := ""
	if baseURL == "" && flagSession != "" && flagModel == "" && activeRole == "default" {
		if dir, err := sessionsDir(); err == nil {
			if rp, resumedURL, ok := pisession.SessionBackend(dir, flagSession); ok {
				if rp != "" {
					providerName = rp
					resumedProvider = rp
				}
				baseURL = resumedURL
			}
		}
	}
	if baseURL == "" && providerName != "" {
		baseURLs := cfg.ResolveBaseURLs()
		baseURL = baseURLs[providerName]
	}
	info, err := provider.ResolveWithBaseURL(modelName, baseURL)
	if err != nil {
		// A resumed session's model may be a virtual name that only its
		// recorded provider understands — e.g. an agentgateway virtual model
		// like "ollama-deepseek", whose dash spelling carries no provider
		// prefix and so cannot be resolved from the name alone. The provider
		// recorded in the session metadata is the authority for which backend
		// served it, so fall back to it rather than failing the resume.
		if resumedProvider != "" {
			info = provider.Info{Provider: resumedProvider, Model: modelName}
		} else {
			return provider.Info{}, "", fmt.Errorf("resolving model: %w", err)
		}
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
		return provider.Info{}, "", fmt.Errorf("model validation: %w", err)
	}
	return info, baseURL, nil
}

// requireRuntimeAPIKey rejects a provider that needs a key when none is
// available. A custom base URL, or a provider that authenticates some other
// way, is exempt.
func requireRuntimeAPIKey(info provider.Info, apiKey, baseURL string) error {
	if apiKey == "" && baseURL == "" && info.Provider != "gemini" && info.Provider != "ollama" && info.Provider != "azure" && info.Provider != "agentgateway" && !info.Ollama {
		envVar := providerEnvVar(info.Provider)
		return fmt.Errorf("no API key found for provider %q (set %s)", info.Provider, envVar)
	}
	return nil
}

// applyRuntimeOllamaEndpoint picks the Ollama daemon for the model, records it
// on info, and health-checks a local daemon before anything depends on it. It
// returns the base URL to use; non-Ollama models pass through untouched.
func applyRuntimeOllamaEndpoint(info *provider.Info, apiKey, baseURL string) (string, error) {
	if info.Ollama {
		// The model's tag decides the daemon, not whether OLLAMA_API_KEY
		// happens to be exported: a key set for some :cloud model used to send
		// locally pulled names like qwen3.8:27b-mlx to api.ollama.com, which
		// answers 404 for a model only this machine has.
		baseURL = provider.ResolveOllamaEndpoint(provider.OllamaRouting{
			Model:      info.Model,
			BaseURL:    baseURL,
			APIKey:     apiKey,
			ForceLocal: info.LocalOllama,
		})
		// Record the endpoint actually chosen so session metadata names the
		// backend instead of leaving the model name to be interpreted.
		info.BaseURL = baseURL
	}
	if info.Ollama && apiKey == "" && !provider.IsOllamaCloudEndpoint(baseURL) {
		if err := provider.CheckOllama(baseURL); err != nil {
			return "", fmt.Errorf("ollama health check: %w", err)
		}
	}
	return baseURL, nil
}

// resolveRuntimeContextWindow picks the context window to budget against: the
// catalog size, overridden by what a live Ollama daemon reports, overridden in
// turn by an explicit config value.
func resolveRuntimeContextWindow(ctx context.Context, cfg config.Config, info provider.Info, baseURL string) int64 {
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
	return ctxWindowSize
}

func buildRootRuntime(ctx context.Context, args []string) (rootRuntime, error) {
	cfg, err := loadRootConfig()
	if err != nil {
		return rootRuntime{}, err
	}

	// Resolve which session we are resuming before the model is picked: the
	// model restore below reads that session's metadata, and --continue has to
	// become a concrete ID for the lookup to happen at all.
	if err := resolveResumeSession(); err != nil {
		return rootRuntime{}, err
	}

	activeRole := resolveActiveRole()
	applyResumedModel(&cfg, activeRole)

	modelName, providerName, advisorModel, advisorMaxUses, advisorCaching, err := cfg.ResolveRole(activeRole)
	if err != nil {
		return rootRuntime{}, fmt.Errorf("resolving model role: %w", err)
	}

	mode := resolveMode()
	info, baseURL, err := resolveRuntimeModelForRole(cfg, modelName, providerName, activeRole)
	if err != nil {
		return rootRuntime{}, err
	}
	info.BaseURL = baseURL

	keys := config.APIKeys()
	apiKey := keys[info.Provider]
	if err := requireRuntimeAPIKey(info, apiKey, baseURL); err != nil {
		return rootRuntime{}, err
	}

	baseURL, err = applyRuntimeOllamaEndpoint(&info, apiKey, baseURL)
	if err != nil {
		return rootRuntime{}, err
	}

	llmOpts := &provider.LLMOptions{
		ExtraHeaders:   mergeExtraHeaders(cfg.ExtraHeaders, flagHeaders),
		AdvisorModel:   advisorModel,
		AdvisorMaxUses: advisorMaxUses,
		AdvisorCaching: advisorCaching,
	}
	applyTransportOptions(llmOpts, cfg, info)
	llm, err := provider.NewLLM(ctx, info, apiKey, baseURL, cfg.ThinkingLevel, llmOpts)
	if err != nil {
		return rootRuntime{}, fmt.Errorf("creating LLM provider: %w", err)
	}

	tokenTracker := guardrail.New(cfg.MaxDailyTokens)
	tokenTracker.SetContextWindowSize(resolveRuntimeContextWindow(ctx, cfg, info, baseURL))
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

// pprofOnce guards the pprof listener. PersistentPreRun fires once per command,
// but runRoot may also be reached directly in tests; starting twice would fail
// with "address already in use".
var pprofOnce sync.Once

// cpuProfileOnce guards the CPU profile writer. Like pprofOnce it exists so a
// command reached both through PersistentPreRun and directly in tests does not
// start two writers on the same file.
var cpuProfileOnce sync.Once

// startCPUProfile begins writing a runtime CPU profile to flagCPUProfile when
// set. The profile is the input PGO consumes, so it must cover a representative
// workload (see `make record-pgo`). It is stopped by stopCPUProfile, which
// runRoot defers; a command that never reaches runRoot (a subcommand with its
// own RunE) leaves the profile unwritten rather than corrupting it.
func startCPUProfile() {
	if flagCPUProfile == "" {
		return
	}
	cpuProfileOnce.Do(func() {
		f, err := os.Create(flagCPUProfile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "cpuprofile: create %s: %v\n", flagCPUProfile, err)
			return
		}
		if err := pprof.StartCPUProfile(f); err != nil {
			fmt.Fprintf(os.Stderr, "cpuprofile: start: %v\n", err)
			f.Close()
			return
		}
		fmt.Fprintf(os.Stderr, "cpuprofile: writing to %s\n", flagCPUProfile)
	})
}

// stopCPUProfile flushes and closes the CPU profile started by startCPUProfile.
// It is safe to call when no profile was started.
func stopCPUProfile() {
	pprof.StopCPUProfile()
}

// startPprofServer serves net/http/pprof on --pprof-port when --pprof is set to
// any non-empty value. Profiles are then collected over HTTP
// (http://localhost:<port>/debug/pprof), so no profile-specific setup is needed
// here — the value is only echoed back so the user can see what they asked for.
func startPprofServer() {
	if flagPprof == "" {
		return
	}
	pprofOnce.Do(func() {
		addr := ":" + flagPprofPort
		go func() {
			fmt.Printf("pprof server listening on %s (profile: %s)\n", addr, flagPprof)
			fmt.Println("collect with: go tool pprof http://localhost:" + flagPprofPort + "/debug/pprof/heap")
			if err := http.ListenAndServe(addr, nil); err != nil {
				fmt.Fprintf(os.Stderr, "pprof server error: %v\n", err)
			}
		}()
	})
}

func runRoot(cmd *cobra.Command, args []string) error {
	// --socket has a non-empty default, so its value alone cannot tell us
	// whether the caller asked for it. Record explicit use here so
	// dispatchMode can honor the pre-rename `--mode rpc --socket` spelling.
	flagSocketChanged = cmd.Flags().Changed("socket")

	// Load API keys from ~/.pi-go/.env (set by /login command).
	loadDotEnv()

	// Normally started by the root's PersistentPreRun; harmless if already up.
	startPprofServer()
	startCPUProfile()
	// Flush the CPU profile on the way out. runRoot is the only path that
	// reaches the agent loop, so deferring here (rather than in main) keeps the
	// profile covering exactly the work the process did.
	defer stopCPUProfile()

	runtime, err := buildRootRuntime(cmd.Context(), args)
	if err != nil {
		return err
	}

	if runtime.mode == "interactive" {
		// The update check runs inside runInteractive so its notice is
		// delivered after the TUI has claimed the notice sink. Started out
		// here it would race the sink installation and could still land on
		// os.Stderr, in the middle of the painted frame.
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

	go checkForUpdate(cmd.Context(), Version)
	config.NotifyReroutedLLMS(runtime.cfg)

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
	bashSup      *tools.BashSupervisor
}

func initNonInteractiveRuntime(ctx context.Context, cfg *config.Config, cwd, sandboxRoot, worktreeDir string) (*nonInteractiveRuntime, error) {
	sandbox, err := tools.NewSandbox(sandboxRoot, worktreeDir)
	if err != nil {
		return nil, fmt.Errorf("creating sandbox: %w", err)
	}

	if home, hErr := os.UserHomeDir(); hErr == nil {
		if aErr := sandbox.AddExtraDir(filepath.Join(home, ".pi-go")); aErr != nil {
			fmt.Fprintf(os.Stderr, "pi-go: warning: could not add ~/.pi-go to sandbox: %v\n", aErr)
		}
	}

	bashSup := tools.NewBashSupervisor()
	coreTools, err := tools.CoreTools(sandbox, tools.WithBashSupervisor(bashSup))
	if err != nil {
		_ = sandbox.Close()
		return nil, fmt.Errorf("creating core tools: %w", err)
	}
	bashCtlTools, err := tools.BashControlTools(bashSup)
	if err != nil {
		_ = sandbox.Close()
		return nil, fmt.Errorf("creating bash control tools: %w", err)
	}
	coreTools = append(coreTools, bashCtlTools...)

	repoRoot := detectGitRoot(ctx, cwd)
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

	bashSup.SetSink(func(execID, kind, content string) {
		agentEventCB(execID, tui.BashEventKind(kind), content)
	})

	return &nonInteractiveRuntime{
		sandbox:      sandbox,
		coreTools:    coreTools,
		orch:         orch,
		agentEventCh: agentEventCh,
		bashSup:      bashSup,
	}, nil
}

func (r *nonInteractiveRuntime) close() {
	if r == nil {
		return
	}
	// Backgrounded commands have no owner but this supervisor; leaving them
	// running past the run is a leaked process tree.
	if r.bashSup != nil {
		r.bashSup.KillAll()
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
	runtime, err := initNonInteractiveRuntime(parentCtx, &cfg, cwd, sandboxRoot, worktreeDir)
	if err != nil {
		return err
	}
	defer runtime.close()

	coreTools := runtime.coreTools
	orch := runtime.orch

	memStore, memWorker, closeMemory := setupMemory(parentCtx, cfg, orch)
	defer closeMemory()

	coreTools = appendNonInteractiveMemoryTools(coreTools, memStore)

	palaceTools, palaceContext, closePalace := setupPalace(cfg, memWorker)
	defer closePalace()
	coreTools = append(coreTools, palaceTools...)

	instruction := buildNonInteractiveInstruction(palaceContext)

	hooks := convertHooks(cfg.Hooks)
	beforeCBs := extension.BuildBeforeToolCallbacks(hooks)
	afterCBs := extension.BuildAfterToolCallbacks(hooks)

	tracingBefore, tracingAfter := extension.BuildTracingCallbacks()
	beforeCBs = append(beforeCBs, tracingBefore...)
	afterCBs = append(afterCBs, tracingAfter...)
	llmBefore, llmAfter := extension.BuildLLMTracingCallbacks(info.Provider)
	llmBefore = append(llmBefore, extension.BuildReadImageCallback(runtime.sandbox, info.Provider))

	lspMgr := lsp.NewManager(nil)
	defer lspMgr.Shutdown()
	// Dedup runs after the compactor so both calls are compared in their final,
	// post-compaction form.
	resultDeduper := tools.NewResultDeduper()
	afterCBs = append(afterCBs,
		lsp.BuildLSPAfterToolCallback(lspMgr),
		tools.BuildCompactorCallback(compactorConfigFrom(cfg), tools.NewCompactMetrics()),
		tools.BuildDedupCallback(resultDeduper))

	// memSessionID is only known once the session is created below; the
	// callback reads it at call time, so recording starts from that point.
	var memSessionID string
	if memWorker != nil {
		afterCBs = append(afterCBs, memoryObservationCallback(memWorker, cfg, cwd, &memSessionID))
	}

	// LSP tool declarations are billed on every request, and with no server
	// installed every call they enable fails — so the model pays tokens for
	// capability it cannot use. Gate on a server existing, then advertise only
	// as much surface as the mode asks for. The after-tool callback stays wired
	// either way; it is free when no server starts.
	coreTools, err = appendNonInteractiveLSPTools(coreTools, lspMgr)
	if err != nil {
		return err
	}

	allToolsets := buildToolsets(cfg)

	loadNonInteractiveSkills(mode)
	instruction += memoryInstructionContext(parentCtx, memStore, cfg, cwd)

	sessionsPath, sessionSvc, err := openSessionService()
	if err != nil {
		return err
	}

	// Gemini search grounding. Always on for the Gemini provider; kill
	// switch via PI_NO_GROUNDING=1 (propagates to subagent pi processes via
	// FilterEnv's PI_ prefix allowlist).
	//
	// APPEND — never replace. See the matching note in interactive.go: replacing
	// coreTools here strips every real tool and every MCP toolset, leaving the
	// model with nothing to call. The built-in search and function declarations
	// coexist fine.
	if gTool, ok := agent.GeminiGroundingTool(info.Provider); ok {
		coreTools = append(coreTools, gTool)
	}

	// Session logger. Created before the agent so it can capture the agent's
	// non-fatal diagnostics (e.g. unresolved instruction placeholders) in the
	// session log instead of leaking them to stderr. SessionStart is recorded
	// below once the session ID is resolved.
	sessionLog, err := logger.New()
	if err != nil {
		fmt.Fprintf(os.Stderr, "pi-go: warning: could not create session log: %v\n", err)
	}
	// --trace-http entries are dropped until this point, because the transport
	// is built well before the log file exists. In practice the only requests
	// that precede it are the ollama health check and model listing.
	httplog.SetSink(logger.HTTPSink(sessionLog))
	defer func() {
		httplog.SetSink(nil)
		_ = sessionLog.Close()
	}()

	ag, err := agent.New(agent.Config{
		Model:                llm,
		Tools:                coreTools,
		Toolsets:             allToolsets,
		Instruction:          instruction,
		SessionService:       sessionSvc,
		BeforeToolCallbacks:  beforeCBs,
		AfterToolCallbacks:   afterCBs,
		BeforeModelCallbacks: llmBefore,
		AfterModelCallbacks:  llmAfter,
		Logger:               sessionLog,
	})
	if err != nil {
		return fmt.Errorf("creating agent: %w", err)
	}

	ctx, stop := signal.NotifyContext(parentCtx, os.Interrupt)
	defer stop()

	sessionID, err := resolveSessionID(ctx, ag, sessionSvc)
	if err != nil {
		return err
	}

	// Two-stage auto-compaction: shed superseded tool results at the lower
	// threshold, summarize at the upper one. Installed as a pre-turn hook so it
	// only ever rewrites history between turns.
	if hook := buildAutoCompactHook(autoCompactDeps{
		SessionSvc:    sessionSvc,
		Tracker:       tokenTracker,
		Deduper:       resultDeduper,
		Cfg:           autoCompactConfigFrom(cfg),
		Log:           sessionLog,
		SummarizerLLM: llm,
		Notify:        func(msg string) { fmt.Fprintf(os.Stderr, "pi-go: %s\n", msg) },
	}); hook != nil {
		ag.SetPreTurnHook(hook)
	}

	// Capture ACP subagent events (claude, gemini) under the session dir.
	orch.SetACPLogPath(filepath.Join(sessionsPath, sessionID, "acp.jsonl"))

	armMemoryObservationSession(ctx, memStore, sessionID, cwd, &memSessionID)

	sessionLog.SessionStart(sessionID, llm.Name(), info.Provider, provider.BackendName(info, config.APIKeys()[info.Provider], info.BaseURL), info.BaseURL, mode)
	return dispatchMode(ctx, mode, prompt, ag, sessionID, sessionLog, llm.Name(), cfg, tokenTracker)
}

// appendNonInteractiveMemoryTools adds the memory tools when a store is
// configured. Memory is optional: a tool-construction failure is a warning and
// the run continues without them.
func appendNonInteractiveMemoryTools(coreTools []adktool.Tool, memStore memory.Store) []adktool.Tool {
	if memStore == nil {
		return coreTools
	}
	memTools, memErr := tools.MemoryTools(memStore)
	if memErr != nil {
		fmt.Fprintf(os.Stderr, "pi-go: warning: memory tools disabled: %v\n", memErr)
		return coreTools
	}
	if memTools != nil {
		coreTools = append(coreTools, memTools...)
	}
	return coreTools
}

// buildNonInteractiveInstruction assembles the system instruction: the built-in
// one unless --system replaces it, with the palace memory context appended.
func buildNonInteractiveInstruction(palaceContext string) string {
	instruction := agent.LoadInstruction(agent.SystemInstruction)
	if flagSystem != "" {
		instruction = flagSystem
	}
	if palaceContext != "" {
		instruction += "\n\n## Palace Memory Context\n\n" + palaceContext
	}
	return instruction
}

// appendNonInteractiveLSPTools adds LSP tools only when a language server is
// actually installed.
//
// LSP tool declarations are billed on every request, and with no server
// installed every call they enable fails — so the model pays tokens for
// capability it cannot use. Gate on a server existing, then advertise only
// as much surface as the mode asks for. The after-tool callback stays wired
// either way; it is free when no server starts.
func appendNonInteractiveLSPTools(coreTools []adktool.Tool, lspMgr *lsp.Manager) ([]adktool.Tool, error) {
	if !lspMgr.AnyAvailable() {
		return coreTools, nil
	}
	lspTools, lspErr := tools.LSPToolsFor(lspMgr, resolveLSPMode())
	if lspErr != nil {
		return nil, fmt.Errorf("creating LSP tools: %w", lspErr)
	}
	return append(coreTools, lspTools...), nil
}

// openSessionService opens the on-disk session store, returning both the
// directory it lives in and the service over it.
func openSessionService() (string, *pisession.FileService, error) {
	sessionsPath, err := sessionsDir()
	if err != nil {
		return "", nil, err
	}
	sessionSvc, err := pisession.NewFileService(sessionsPath)
	if err != nil {
		return "", nil, fmt.Errorf("creating session service: %w", err)
	}
	return sessionsPath, sessionSvc, nil
}

// armMemoryObservationSession opens the memory session row and publishes the
// session ID the observation callback records under. Until memSessionID is set
// the callback is inert, so this is what arms it.
func armMemoryObservationSession(ctx context.Context, memStore memory.Store, sessionID, project string, memSessionID *string) {
	if memStore == nil {
		return
	}
	*memSessionID = sessionID
	_ = memStore.CreateSession(ctx, &memory.Session{
		SessionID: sessionID,
		Project:   project,
		StartedAt: time.Now(),
		Status:    "active",
	})
}

// loadNonInteractiveSkills loads the skill set and reports what it found.
// Skills are optional: a load failure is a warning, not a fatal error.
func loadNonInteractiveSkills(mode string) {
	skills, err := extension.LoadSkills(extension.DefaultSkillDirs()...)
	if mode == "print" {
		fmt.Fprint(os.Stderr, formatPrintSkillLoad(len(skills), err))
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "pi-go: warning: skills disabled: %v\n", err)
	}
}

// memoryInstructionContext generates the recalled-memory block to append to the
// system instruction, or an empty string when there is no memory to add.
func memoryInstructionContext(ctx context.Context, store memory.Store, cfg config.Config, project string) string {
	if store == nil {
		return ""
	}
	budget := config.MemoryDefaults().TokenBudget
	if cfg.Memory != nil && cfg.Memory.TokenBudget > 0 {
		budget = cfg.Memory.TokenBudget
	}

	memContext, err := memory.NewContextGenerator(store, budget).Generate(ctx, project)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pi-go: warning: memory context generation failed: %v\n", err)
		return ""
	}
	if memContext == "" {
		return ""
	}
	return "\n\n" + memContext
}

// sessionsDir is the directory FileService keeps one subdirectory per session
// in. Startup reaches for it before any session service exists, so it cannot
// be asked of the service itself.
func sessionsDir() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("getting home dir: %w", err)
	}
	return filepath.Join(homeDir, ".pi-go", "sessions"), nil
}

// resolveResumeSession turns --continue into an explicit --session value, so
// everything downstream — the model restore, the agent, the TUI's transcript
// restore — reads one resolved session ID instead of handling two ways of
// asking for the same thing.
func resolveResumeSession() error {
	if !flagContinue {
		return nil
	}
	dir, err := sessionsDir()
	if err != nil {
		return err
	}
	sessionSvc, err := pisession.NewFileService(dir)
	if err != nil {
		return fmt.Errorf("creating session service: %w", err)
	}
	lastID := sessionSvc.LastSessionID(agent.AppName, agent.DefaultUserID)
	if lastID == "" {
		return fmt.Errorf("no previous session found to continue")
	}
	flagSession = lastID
	return nil
}

// applyResumedModel restores the model a resumed session last ran under.
//
// Sessions record their model in meta.json, but startup used to resolve the
// model from config alone: `pi --session <id>` continued a conversation held
// with one model under whatever the default role happened to point at, with
// nothing on screen admitting the swap. Since the whole transcript is replayed
// to the new model, the switch is silent and total.
//
// Explicit intent still wins. --model names a model outright, and --smol /
// --slow / --plan pick a role for a reason, so each leaves the session's
// recorded model alone.
func applyResumedModel(cfg *config.Config, activeRole string) {
	if flagSession == "" || flagModel != "" || activeRole != "default" || cfg.Roles == nil {
		return
	}
	dir, err := sessionsDir()
	if err != nil {
		return
	}
	name := pisession.SessionModel(dir, flagSession)
	if name == "" {
		return
	}
	rc := cfg.Roles["default"]
	if rc.Model == name {
		return
	}
	rc.Model = name
	// The configured provider belongs to the configured model. Carrying it
	// over would route the session's model to the wrong API — an anthropic
	// role serving a gpt-* model — so let it be re-detected from the name.
	rc.Provider = ""
	cfg.Roles["default"] = rc
}

// resolveSessionID picks the session to run in: an explicit --session, the most
// recent one under --continue, or a freshly created session.
func resolveSessionID(ctx context.Context, ag *agent.Agent, sessionSvc *pisession.FileService) (string, error) {
	// buildRootRuntime already resolved --continue into flagSession, but this
	// branch stays authoritative: --continue with nothing to continue is an
	// error, and must never fall through to opening a brand-new session.
	if flagContinue {
		lastID := sessionSvc.LastSessionID(agent.AppName, agent.DefaultUserID)
		if lastID == "" {
			return "", fmt.Errorf("no previous session found to continue")
		}
		fmt.Fprintf(os.Stderr, "pi-go: continuing session %s\n", lastID)
		return lastID, nil
	}
	if flagSession != "" {
		return flagSession, nil
	}

	sessionID, _, err := ag.CreateSession(ctx)
	if err != nil {
		return "", fmt.Errorf("creating session: %w", err)
	}
	return sessionID, nil
}

// dispatchMode runs the agent in the requested non-interactive mode. The
// server modes serve requests instead of a prompt; the others need one.
//
// Two server modes exist and they are not interchangeable:
//
//   - "socket": pi-go's own JSON-RPC 2.0 over a Unix socket, for editor/IDE
//     integration. This was spelled "rpc" before the rename.
//   - "rpc": the stdio NDJSON protocol that `pi-acp` drives, wire-compatible
//     with upstream pi's `--mode rpc`.
func dispatchMode(ctx context.Context, mode, prompt string, ag *agent.Agent, sessionID string, sessionLog *logger.Logger, modelName string, cfg config.Config, tokenTracker *guardrail.Tracker) error {
	// Pre-rename spelling: `--mode rpc --socket <path>` meant the Unix socket
	// server. Honor it with a warning rather than silently starting the
	// stdio server and leaving the caller's socket client hanging.
	if mode == "rpc" && flagSocketChanged {
		fmt.Fprintln(os.Stderr,
			"pi-go: `--mode rpc --socket` is deprecated and will be removed; use `--mode socket`.")
		mode = "socket"
	}
	if mode == "socket" {
		return jsonrpc.NewServer(jsonrpc.Config{
			Agent:      ag,
			SocketPath: flagSocket,
		}).Run(ctx)
	}
	if mode == "rpc" {
		return pirpc.NewServer(pirpc.Config{
			Agent:     ag,
			SessionID: sessionID,
			In:        os.Stdin,
			Out:       os.Stdout,
			Log:       sessionLog,
			Model:     modelName,
			ModelSwitcher: func(switchCtx context.Context, name, providerHint string) (adkmodel.LLM, string, string, error) {
				// buildSwitchedLLM seeds the provider from the default role
				// and then overrides detection with it, so a stale pin would
				// route e.g. an OpenAI model through Ollama. Substitute the
				// provider the ACP client named; when it names none, clearing
				// the pin lets pi-go detect from the model name.
				switchCfg := cfg
				switchCfg.Roles = maps.Clone(cfg.Roles)
				if switchCfg.Roles == nil {
					switchCfg.Roles = map[string]config.RoleConfig{}
				}
				rc := switchCfg.Roles["default"]
				rc.Provider = providerHint
				switchCfg.Roles["default"] = rc
				return buildSwitchedLLM(switchCtx, switchCfg, tokenTracker, name)
			},
		}).Run(ctx)
	}
	if prompt == "" {
		fmt.Fprintf(os.Stderr, "pi-go: no prompt provided (model: %s, mode: %s)\n", modelName, mode)
		return nil
	}
	if mode == "json" {
		return runJSON(ctx, ag, sessionID, prompt, sessionLog)
	}
	return runPrint(ctx, ag, sessionID, prompt, sessionLog)
}

// memoryEnabled reports whether the observation memory subsystem is on.
// It defaults to on: only an explicit false in config, or --memory-off,
// disables it.
func memoryEnabled(cfg config.Config) bool {
	return !flagMemoryOff && (cfg.Memory == nil || cfg.Memory.Enabled == nil || *cfg.Memory.Enabled)
}

// palaceIsEnabled reports whether the memory palace is on, with the same
// default-on semantics as memoryEnabled.
func palaceIsEnabled(cfg config.Config) bool {
	return !flagMemoryOff && (cfg.Palace == nil || cfg.Palace.Enabled == nil || *cfg.Palace.Enabled)
}

// setupMemory opens the observation store and starts its background worker.
// Memory is best-effort: every failure downgrades to "no memory" with a
// warning, and the returned closer is always safe to defer.
func setupMemory(ctx context.Context, cfg config.Config, orch *subagent.Orchestrator) (memory.Store, *memory.Worker, func()) {
	noop := func() {}
	if !memoryEnabled(cfg) {
		return nil, nil, noop
	}

	memCfg := deferredMemoryConfig(cfg)
	dbPath := memCfg.DBPath
	if dbPath == "" {
		if home, err := os.UserHomeDir(); err == nil {
			dbPath = filepath.Join(home, ".pi-go", "memory", "claude-mem.db")
		}
	}

	memDB, err := memory.OpenDB(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pi-go: warning: memory system disabled: %v\n", err)
		return nil, nil, noop
	}

	store := memory.NewSQLiteStore(memDB)
	worker := memory.NewWorker(store, memory.NewSubagentCompressor(orch), memCfg.MaxPending)
	worker.Start(ctx)

	return store, worker, func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = worker.Shutdown(shutdownCtx)
		_ = store.Close()
	}
}

// resolveLSPMode turns the --lsp flag into a mode, warning once on a value it
// does not recognize rather than silently picking one. A subagent inherits the
// parent's choice through the same flag on its command line, which is how an
// agent that needs the wide surface gets it without every session paying for it.
func resolveLSPMode() tools.LSPMode {
	mode, ok := tools.ParseLSPMode(flagLSP)
	if !ok {
		fmt.Fprintf(os.Stderr, "pi-go: warning: unknown --lsp value %q; using %q\n", flagLSP, mode)
	}
	return mode
}

// palaceHasContent reports whether the palace holds at least one drawer.
// A count error is treated as "no content": the tools would fail anyway, and
// the caller's job is to decide whether advertising them is worth the tokens.
func palaceHasContent(p *palace.Palace) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	status, err := p.Status(ctx)
	if err != nil {
		slog.Warn("palace: drawer count failed, not advertising palace tools", "error", err)
		return false
	}
	return status.DrawerCount > 0
}

// setupPalace opens the memory palace when one already exists on disk, and
// returns its tools plus the wake-up context to inject into the system prompt.
// A missing palace is not an error — it simply contributes nothing.
func setupPalace(cfg config.Config, memWorker *memory.Worker) ([]adktool.Tool, string, func()) {
	noop := func() {}
	if !palaceIsEnabled(cfg) {
		return nil, "", noop
	}
	palaceCfg := palaceConfigFromCLI(&cfg)
	if palaceCfg.DBPath == "" {
		return nil, "", noop
	}
	if _, err := os.Stat(palaceCfg.DBPath); err != nil {
		return nil, "", noop
	}

	p, err := palace.New(
		palace.WithDBPath(palaceCfg.DBPath),
		palace.WithModelPath(palaceCfg.ModelPath),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pi-go: warning: palace tools disabled: %v\n", err)
		return nil, "", noop
	}
	closePalace := func() { _ = p.Close() }

	// Eleven palace tool declarations cost ~1.6k tokens on every request. An
	// empty palace has nothing for them to find, so searching it is a wasted
	// call and the tokens buy nothing — the same trade the LSP gate makes. An
	// existing file is not evidence of content: `pi memory init` creates one
	// with zero drawers. Gate on drawers, not on the file.
	//
	// The palace is still opened when empty: the bridge below fills it, and the
	// tools appear on the next session once it has content.
	if !palaceHasContent(p) {
		if memWorker != nil {
			memWorker.OnAfterStore(palace.NewObservationBridge(p).ConvertAndStore)
		}
		return nil, "", closePalace
	}

	// A tool-building failure still leaves a usable palace for the bridge and
	// the wake-up context below, so it only costs the tools.
	palaceTools, err := palace.PalaceTools(p)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pi-go: warning: palace tools disabled: %v\n", err)
		palaceTools = nil
	}

	// Wire the observation bridge: auto-file observations as palace drawers.
	if memWorker != nil {
		memWorker.OnAfterStore(palace.NewObservationBridge(p).ConvertAndStore)
	}

	var wakeUpContext string
	if wakeUp, wErr := p.WakeUp(context.Background(), ""); wErr == nil {
		wakeUpContext = wakeUp
	}
	return palaceTools, wakeUpContext, closePalace
}

// compactorConfigFrom overlays the configured compactor settings on the
// defaults, ignoring zero values so an unset field keeps its default.
func compactorConfigFrom(cfg config.Config) tools.CompactorConfig {
	compactorCfg := tools.DefaultCompactorConfig()
	if cfg.Compactor == nil {
		return compactorCfg
	}
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
	return compactorCfg
}

// autoCompactConfigFrom resolves the two-stage auto-compaction settings,
// falling back to the session package's defaults for anything unset.
func autoCompactConfigFrom(cfg config.Config) pisession.AutoCompactConfig {
	acCfg := pisession.DefaultAutoCompactConfig()
	if cfg.AutoCompact == nil {
		return acCfg
	}
	if cfg.AutoCompact.Enabled != nil {
		acCfg.Enabled = *cfg.AutoCompact.Enabled
	}
	if cfg.AutoCompact.ShedPercent > 0 {
		acCfg.ShedPercent = cfg.AutoCompact.ShedPercent
	}
	if cfg.AutoCompact.SummarizePercent > 0 {
		acCfg.SummarizePercent = cfg.AutoCompact.SummarizePercent
	}
	if cfg.AutoCompact.KeepUserMessageTokens > 0 {
		acCfg.KeepUserMessageTokens = cfg.AutoCompact.KeepUserMessageTokens
	}
	if cfg.AutoCompact.KeepRecentEvents > 0 {
		acCfg.KeepRecentEvents = cfg.AutoCompact.KeepRecentEvents
	}
	return acCfg
}

// memoryObservationCallback records each successful tool call as a raw
// observation. sessionID is read through the pointer because the session is
// only created after the callbacks are wired.
func memoryObservationCallback(worker *memory.Worker, cfg config.Config, project string, sessionID *string) llmagent.AfterToolCallback {
	var excluded map[string]bool
	if cfg.Memory != nil && len(cfg.Memory.ExcludedTools) > 0 {
		excluded = make(map[string]bool, len(cfg.Memory.ExcludedTools))
		for _, name := range cfg.Memory.ExcludedTools {
			excluded[name] = true
		}
	}

	return func(_ adkagent.Context, t adktool.Tool, args, result map[string]any, toolErr error) (map[string]any, error) {
		if toolErr != nil || *sessionID == "" {
			return result, nil
		}
		name := t.Name()
		if excluded[name] {
			return result, nil
		}
		worker.Enqueue(memory.RawObservation{
			SessionID:  *sessionID,
			Project:    project,
			ToolName:   name,
			ToolInput:  args,
			ToolOutput: result,
			Timestamp:  time.Now(),
		})
		return result, nil
	}
}

// buildToolsets assembles the external toolsets — MCP servers and A2A agents —
// configured for this run.
func buildToolsets(cfg config.Config) []adktool.Toolset {
	var toolsets []adktool.Toolset
	if servers := buildMCPServerConfigs(cfg); len(servers) > 0 {
		mcpToolsets, _ := extension.BuildMCPToolsets(servers)
		toolsets = append(toolsets, mcpToolsets...)
	}
	if cfg.A2A != nil && len(cfg.A2A.Agents) > 0 {
		toolsets = append(toolsets, tools.NewA2AToolset(cfg.A2A))
	}
	if llms := cfg.LLMSSources(); llms != nil {
		toolsets = append(toolsets, tools.NewLLMSCachedToolset(llms))
	}
	return toolsets
}

// providerEnvVar delegates to the provider package so the CLI and the public
// pimodels package cannot drift on where a key comes from.
func providerEnvVar(p string) string {
	return provider.APIKeyEnvVar(p)
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
	// Log directories sort ascending by date, so walking backwards reaches the
	// most recent one first.
	for i := len(dateDirs) - 1; i >= 0; i-- {
		d := dateDirs[i]
		if !d.IsDir() {
			continue
		}
		if p, m := lastLoggedErrorInDateDir(filepath.Join(logRoot, d.Name())); m != "" {
			return p, m, nil
		}
	}
	return "", "", nil
}

// lastLoggedErrorInDateDir scans one date directory's session logs newest-first
// and returns the path and message of the first error entry found. A directory
// that cannot be listed yields no result rather than an error: this whole path
// is a best-effort diagnostic hint.
func lastLoggedErrorInDateDir(datePath string) (path, msg string) {
	files, listErr := os.ReadDir(datePath)
	if listErr != nil {
		return "", ""
	}
	for j := len(files) - 1; j >= 0; j-- {
		f := files[j]
		if f.IsDir() || !strings.HasPrefix(f.Name(), "session-") || !strings.HasSuffix(f.Name(), ".log") {
			continue
		}
		p := filepath.Join(datePath, f.Name())
		if m := lastLoggedErrorInLogFile(p); m != "" {
			return p, m
		}
	}
	return "", ""
}

// lastLoggedErrorInLogFile returns the content of the last non-empty "error"
// entry in one session log, or "" when it holds none or cannot be read.
func lastLoggedErrorInLogFile(logPath string) string {
	blob, readErr := os.ReadFile(logPath)
	if readErr != nil {
		return ""
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
			return entry.Content
		}
	}
	return ""
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

// derivePrintTitle extracts a short, single-line session title from a user
// prompt. Mirrors the TUI's deriveSessionTitle so /sessions lists look
// consistent regardless of which mode created the session.
func derivePrintTitle(prompt string) string {
	title := strings.TrimSpace(prompt)
	if title == "" {
		return ""
	}
	if i := strings.IndexByte(title, '\n'); i >= 0 {
		title = title[:i]
	}
	const max = 200
	if len(title) > max {
		title = title[:max-1] + "…"
	}
	return title
}

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
	ctx, span := otel.Tracer("pi-go").Start(ctx, "agent.prompt")
	defer span.End()
	span.SetAttributes(otel.AttributeInt("prompt.length", len(prompt)))

	log.UserMessage(prompt)
	// Auto-set the session title from the user prompt. Print mode is
	// non-interactive, so we don't emit OSC 0 — there may be no terminal, or
	// the terminal may be a script's stdout. The title is metadata for
	// /sessions listing and the meta.json file, both of which still benefit.
	if title := derivePrintTitle(prompt); title != "" {
		_ = ag.SetSessionTitle(sessionID, title)
	}
	retryCfg := agent.DefaultRetryConfig()
	// Say when a request is being re-sent, so a backoff pause is not mistaken
	// for a hung run. stderr keeps it out of the captured reply.
	ctx = retry.WithNotifier(ctx, func(a retry.Attempt) {
		fmt.Fprintln(os.Stderr, a.String())
	})
	// GroundingMetadata repeats on every chunk of the response it grounds;
	// report each search once.
	groundedSeen := map[string]bool{}
	// SSE delivers the reply as deltas and then once more as an aggregate;
	// without this the whole answer prints twice.
	var dedup agent.StreamDedup
	for ev, err := range agent.WithRetryContext(ctx, retryCfg, func() iter.Seq2[*session.Event, error] {
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
		if ev == nil {
			continue
		}
		printGroundingEvent(ev, groundedSeen, log)
		// Without this, a provider failure exits 0 having printed nothing.
		// See agent.EventError.
		if evErr := agent.EventError(ev); evErr != nil {
			log.Error(evErr.Error())
			return fmt.Errorf("agent run: %w", evErr)
		}
		if ev.Content == nil {
			continue
		}
		dedup.BeginEvent(ev)
		printEventParts(ev, &dedup, log)
	}
	fmt.Println()
	return nil
}

// printGroundingEvent reports a server-side Gemini search as a tool call.
//
// Gemini grounding runs server-side and emits no FunctionCall, so it would
// otherwise be invisible. GroundingMetadata repeats on every chunk of the
// response it grounds, so groundedSeen keeps each search to one report.
func printGroundingEvent(ev *session.Event, groundedSeen map[string]bool, log *logger.Logger) {
	gm := ev.GroundingMetadata
	if gm == nil || len(gm.WebSearchQueries) == 0 {
		return
	}
	key := agent.GroundingQueryKey(gm.WebSearchQueries)
	if groundedSeen[key] {
		return
	}
	groundedSeen[key] = true
	args := map[string]any{"query": agent.GroundingQuery(gm)}
	fmt.Fprint(os.Stderr, formatPrintToolCall(agent.GroundingToolName, args))
	log.ToolCall("grounding", agent.GroundingToolName, args)
	for _, src := range strings.Split(agent.GroundingSummary(gm), "\n") {
		fmt.Fprintf(os.Stderr, "%s   %s%s\n", printToolDimColor, src, printToolReset)
	}
	fmt.Fprint(os.Stderr, formatPrintToolDone(agent.GroundingToolName))
	log.ToolResult("grounding", agent.GroundingToolName, agent.GroundingSources(gm))
}

// printEventParts writes one event's parts: reply text to stdout, thinking and
// tool activity to stderr. The caller must have called dedup.BeginEvent for ev.
func printEventParts(ev *session.Event, dedup *agent.StreamDedup, log *logger.Logger) {
	for _, part := range ev.Content.Parts {
		if part.Text != "" && ev.Content.Role == "thinking" {
			fmt.Fprintf(os.Stderr, "\033[2m%s\033[0m", part.Text)
			log.Thinking(ev.Author, part.Text)
			continue
		}
		if part.Text != "" {
			if dedup.SkipText(ev) {
				continue
			}
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
	Error     string `json:"error,omitempty"`
}

// runJSON runs the agent and emits JSONL events to stdout.
// Events: message_start (once), text_delta (per text chunk), tool_call, tool_result, message_end (once).
func runJSON(ctx context.Context, ag *agent.Agent, sessionID, prompt string, log *logger.Logger) error {
	log.UserMessage(prompt)
	// Auto-set the session title for JSON mode too. The first jsonEvent
	// carries session_id, so the title is just metadata to keep meta.json
	// in sync with the user prompt — consumers can use it to label sessions.
	if title := derivePrintTitle(prompt); title != "" {
		_ = ag.SetSessionTitle(sessionID, title)
	}
	enc := json.NewEncoder(os.Stdout)
	started := false
	// SSE delivers the reply as deltas and then once more as an aggregate;
	// without this every text_delta is emitted twice.
	var dedup agent.StreamDedup

	retryCfg := agent.DefaultRetryConfig()
	for ev, err := range agent.WithRetryContext(ctx, retryCfg, func() iter.Seq2[*session.Event, error] {
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
		if ev == nil {
			continue
		}
		// Emit provider failures as an explicit `error` event so consumers can
		// tell a failed run from an empty one. See agent.EventError.
		if evErr := agent.EventError(ev); evErr != nil {
			log.Error(evErr.Error())
			_ = enc.Encode(jsonEvent{Type: "error", Agent: ev.Author, Error: evErr.Error()})
			return fmt.Errorf("agent run: %w", evErr)
		}
		if ev.Content == nil {
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

		dedup.BeginEvent(ev)
		encodeEventParts(enc, ev, &dedup, log)
	}
	if !started {
		const warn = "pi-go: warning: no assistant events received before message_end"
		fmt.Fprintln(os.Stderr, warn)
		log.Error(warn)
	}
	_ = enc.Encode(jsonEvent{Type: "message_end"})
	return nil
}

// encodeEventParts emits one event's parts as JSONL: thinking_delta,
// text_delta, tool_call and tool_result. The caller must have called
// dedup.BeginEvent for ev.
func encodeEventParts(enc *json.Encoder, ev *session.Event, dedup *agent.StreamDedup, log *logger.Logger) {
	for _, part := range ev.Content.Parts {
		if part.Text != "" && ev.Content.Role == "thinking" {
			_ = enc.Encode(jsonEvent{
				Type:  "thinking_delta",
				Agent: ev.Author,
				Delta: part.Text,
			})
			log.Thinking(ev.Author, part.Text)
			continue
		}
		if part.Text != "" {
			if dedup.SkipText(ev) {
				continue
			}
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
			respJSON, err := json.Marshal(part.FunctionResponse.Response)
			if err != nil {
				respJSON = []byte(fmt.Sprintf("%v", part.FunctionResponse.Response))
			}
			_ = enc.Encode(jsonEvent{
				Type:     "tool_result",
				Agent:    ev.Author,
				ToolName: part.FunctionResponse.Name,
				Content:  string(respJSON),
			})
			log.ToolResult(ev.Author, part.FunctionResponse.Name, string(respJSON))
		}
	}
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
		baseURLs := cfg.ResolveBaseURLs()
		baseURL = baseURLs[info.Provider]
	}
	if info.Ollama {
		baseURL = provider.ResolveOllamaEndpoint(provider.OllamaRouting{
			Model:      info.Model,
			BaseURL:    baseURL,
			APIKey:     apiKey,
			ForceLocal: info.LocalOllama,
		})
	}

	if info.Ollama && !provider.IsOllamaCloudEndpoint(baseURL) {
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

// applyTransportOptions layers the --insecure/--ca-cert/--trace-http flags over
// the transport settings from config. The flags are additive: none of them can
// turn a config-enabled setting back off, matching how --header behaves.
func applyTransportOptions(opts *provider.LLMOptions, cfg config.Config, info provider.Info) {
	opts.InsecureSkipTLS = cfg.InsecureSkipTLS || flagInsecure
	opts.CACertPath = cfg.CACertPath
	if flagCACert != "" {
		opts.CACertPath = flagCACert
	}
	opts.DisableSystemCAs = cfg.DisableSystemCAs

	opts.TraceHTTP = cfg.TraceHTTP || flagTraceHTTP
	// The transport reads this through httplog rather than from opts, so that
	// a client built before the flag was parsed still honors it. Setting it
	// here keeps the two in step for every path that builds an LLM.
	if opts.TraceHTTP {
		httplog.SetEnabled(true)
	}

	// Pacing is resolved here, alongside the other transport settings, because
	// it is installed the same way — as a RoundTripper by BuildTransport — and
	// because every path that builds an LLM already funnels through this
	// function. A provider added to the switch in NewLLM without a stop here
	// would silently send unpaced.
	opts.RateLimit = cfg.ResolveRateLimits(info.Provider, info.Model)
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

// gitCmdTimeout bounds every git subprocess call spawned during init so a
// stalled repo (blocked hook, lock contention, unreachable network mount)
// can never hang the init pipeline indefinitely.
const gitCmdTimeout = 5 * time.Second

// detectGitRoot returns the git repository root for the given directory,
// or empty string if not inside a git repo.
//
// Inside a linked worktree this resolves the *main* checkout, not the worktree
// — the value becomes PI_SANDBOX_ROOT for spawned subagents, and rooting that
// at a worktree makes every file-tool access to the rest of the repo fail.
// See internal/gitroot.
func detectGitRoot(ctx context.Context, dir string) string {
	return gitroot.Detect(ctx, dir)
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
