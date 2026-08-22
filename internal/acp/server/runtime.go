package server

import (
	"context"
	"fmt"
	"iter"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	acp "github.com/coder/acp-go-sdk"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/adk/v2/agent/llmagent"
	adkmodel "google.golang.org/adk/v2/model"
	adksession "google.golang.org/adk/v2/session"
	adktool "google.golang.org/adk/v2/tool"

	"github.com/dimetron/pi-go/internal/acp/server/adapter"
	piagent "github.com/dimetron/pi-go/internal/agent"
	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/extension"
	"github.com/dimetron/pi-go/internal/guardrail"
	"github.com/dimetron/pi-go/internal/lsp"
	"github.com/dimetron/pi-go/internal/otel"
	"github.com/dimetron/pi-go/internal/provider"
	"github.com/dimetron/pi-go/internal/subagent"
	"github.com/dimetron/pi-go/internal/tools"
)

// getwd wraps os.Getwd so tests can inject failures.
var getwd = os.Getwd

// RuntimeConfig controls how the ACP prompt handler resolves and builds the pi runtime.
type RuntimeConfig struct {
	Model           string
	BaseURL         string
	Headers         []string
	Insecure        bool
	System          string
	LoadConfig      func() (config.Config, error)
	SandboxRootFunc func(turn PromptTurn) string
}

// piSessionState caches the per-ACP-session pi runtime so all turns within one
// session share a single agent instance and its conversation history.
type piSessionState struct {
	mu          sync.Mutex // serializes turns, which share the agent and bash output sink
	agent       *piagent.Agent
	sessionID   string // ADK session ID — reused across turns for history continuity
	streamProxy *streamProxy
	bashSup     *tools.BashSupervisor
	cleanup     func()
}

// streamProxy implements extension.ToolCallReporter and forwards to the
// current turn's adapter.Stream. It is swapped at the start of each turn so
// tool-call events always reach the active ACP updater.
type streamProxy struct {
	mu sync.Mutex
	s  *adapter.Stream
}

func (p *streamProxy) swap(s *adapter.Stream) {
	p.mu.Lock()
	p.s = s
	p.mu.Unlock()
}

func (p *streamProxy) OnToolStart(ctx context.Context, name string, args map[string]any) (string, error) {
	p.mu.Lock()
	s := p.s
	p.mu.Unlock()
	if s == nil {
		return "", nil
	}
	return s.OnToolStart(ctx, name, args)
}

func (p *streamProxy) OnToolEnd(ctx context.Context, callID string, args map[string]any, result any, runErr error) error {
	p.mu.Lock()
	s := p.s
	p.mu.Unlock()
	if s == nil {
		return nil
	}
	return s.OnToolEnd(ctx, callID, args, result, runErr)
}

// beginTurnStream connects the session's shared callbacks to one active ACP
// turn. Call the returned function before releasing the session turn lock.
func (p *piSessionState) beginTurnStream(ctx context.Context, stream *adapter.Stream) func() {
	p.streamProxy.swap(stream)
	p.bashSup.SetSink(func(execID, kind, content string) {
		_ = stream.OnBashOutput(ctx, execID, kind, content)
	})
	return func() {
		p.bashSup.SetSink(nil)
		p.streamProxy.swap(nil)
	}
}

// NewPromptHandler returns a real pi-backed ACP prompt handler. It maintains a
// per-ACP-session cache so the pi agent (and its ADK session history) is reused
// across turns rather than re-created on every prompt.
func NewPromptHandler(rt RuntimeConfig) PromptHandler {
	var mu sync.Mutex
	sessions := map[string]*piSessionState{}

	return func(ctx context.Context, turn PromptTurn) (PromptResult, error) {
		mu.Lock()
		ps := sessions[turn.SessionID]
		mu.Unlock()

		if ps == nil {
			var err error
			ps, err = initPiSessionState(ctx, rt, turn)
			if err != nil {
				return PromptResult{}, err
			}
			mu.Lock()
			sessions[turn.SessionID] = ps
			mu.Unlock()
		}

		ps.mu.Lock()
		defer ps.mu.Unlock()

		// Fresh stream per turn so tool-call IDs and text are isolated.
		stream := adapter.New(turn.Updater)
		finishTurn := ps.beginTurnStream(ctx, stream)
		defer finishTurn()

		return runPromptTurn(ctx, turn, ps, stream)
	}
}

// initPiSessionState initializes the pi runtime for a new ACP session.
// Resources (sandbox, LSP, orchestrator) are owned by the returned state and
// released via its cleanup function when the ACP server exits.
func initPiSessionState(ctx context.Context, rt RuntimeConfig, turn PromptTurn) (*piSessionState, error) {
	cwd := turn.CWD
	if strings.TrimSpace(cwd) == "" {
		wd, err := getwd()
		if err != nil {
			return nil, fmt.Errorf("getting working directory: %w", err)
		}
		cwd = wd
	}

	ctx, span := otel.Tracer("acp-server").Start(ctx, "acp.InitSession")
	span.SetAttributes(
		attribute.String("session.id", turn.SessionID),
		attribute.String("session.cwd", cwd),
	)
	defer span.End()

	cfg, err := loadSessionConfig(rt, cwd)
	if err != nil {
		return nil, err
	}

	llm, err := buildSessionLLM(ctx, rt, cfg)
	if err != nil {
		return nil, err
	}

	res, err := buildSessionResources(rt, cfg, turn, cwd, span)
	if err != nil {
		return nil, err
	}

	instruction := rt.System
	if instruction == "" {
		instruction = piagent.LoadInstruction(piagent.SystemInstruction)
	}

	ag, err := piagent.New(piagent.Config{
		Model:                llm,
		Tools:                res.coreTools,
		Toolsets:             buildMCPToolsetsFromCfg(cfg),
		Instruction:          instruction,
		BeforeToolCallbacks:  res.beforeCBs,
		AfterToolCallbacks:   res.afterCBs,
		BeforeModelCallbacks: res.beforeModelCBs,
	})
	if err != nil {
		res.cleanup()
		return nil, fmt.Errorf("creating agent: %w", err)
	}

	sessionID, _, err := ag.CreateSession(ctx)
	if err != nil {
		res.cleanup()
		return nil, fmt.Errorf("creating session: %w", err)
	}

	return &piSessionState{
		agent:       ag,
		sessionID:   sessionID,
		streamProxy: res.proxy,
		bashSup:     res.bashSup,
		cleanup:     res.cleanup,
	}, nil
}

// loadSessionConfig loads the pi config for the session's working directory and
// applies the runtime's model override.
func loadSessionConfig(rt RuntimeConfig, cwd string) (config.Config, error) {
	loadConfig := rt.LoadConfig
	if loadConfig == nil {
		loadConfig = func() (config.Config, error) { return config.LoadFrom(cwd) }
	}
	cfg, err := loadConfig()
	if err != nil {
		return config.Config{}, fmt.Errorf("loading config: %w", err)
	}
	if rt.Model != "" {
		if cfg.Roles == nil {
			cfg.Roles = map[string]config.RoleConfig{}
		}
		cfg.Roles["default"] = config.RoleConfig{Model: rt.Model}
	}
	return cfg, nil
}

// buildSessionLLM resolves the default role to a provider and returns a
// token-guarded LLM for it.
func buildSessionLLM(ctx context.Context, rt RuntimeConfig, cfg config.Config) (adkmodel.LLM, error) {
	modelName, providerName, advisorModel, advisorMaxUses, advisorCaching, err := cfg.ResolveRole("default")
	if err != nil {
		return nil, fmt.Errorf("resolving model role: %w", err)
	}

	info, baseURL, err := resolveSessionProvider(cfg, rt.BaseURL, modelName, providerName)
	if err != nil {
		return nil, err
	}

	apiKey := config.APIKeys()[info.Provider]
	// Providers that authenticate some other way — a local Ollama, an Azure
	// deployment URL, gemini's ADC — are allowed through without a key.
	if apiKey == "" && baseURL == "" && !info.Ollama &&
		info.Provider != "gemini" && info.Provider != "ollama" && info.Provider != "azure" {
		return nil, fmt.Errorf("no API key found for provider %q (set %s)", info.Provider, providerEnvVar(info.Provider))
	}

	if info.Ollama {
		baseURL = provider.ResolveOllamaEndpoint(info.Model, baseURL)
	}
	if info.Ollama && apiKey == "" && !provider.IsOllamaCloudEndpoint(baseURL) {
		if err := provider.CheckOllama(baseURL); err != nil {
			return nil, fmt.Errorf("ollama health check: %w", err)
		}
	}

	llm, err := provider.NewLLM(ctx, info, apiKey, baseURL, cfg.ThinkingLevel, &provider.LLMOptions{
		ExtraHeaders:    mergeExtraHeaders(cfg.ExtraHeaders, rt.Headers),
		InsecureSkipTLS: cfg.InsecureSkipTLS || rt.Insecure,
		AdvisorModel:    advisorModel,
		AdvisorMaxUses:  advisorMaxUses,
		AdvisorCaching:  advisorCaching,
	})
	if err != nil {
		return nil, fmt.Errorf("creating LLM provider: %w", err)
	}

	tokenTracker := guardrail.New(cfg.MaxDailyTokens)
	tokenTracker.SetContextWindowSize(sessionContextWindow(ctx, info, baseURL))
	return guardrail.WrapModel(llm, tokenTracker), nil
}

// resolveSessionProvider resolves the model to a provider, and settles on the
// base URL to reach it at — the runtime flag first, then the configured one.
func resolveSessionProvider(cfg config.Config, flagBaseURL, modelName, providerName string) (provider.Info, string, error) {
	baseURL := flagBaseURL
	if baseURL == "" && providerName != "" {
		baseURL = cfg.ResolveBaseURLs()[providerName]
	}

	info, err := provider.ResolveWithBaseURL(modelName, baseURL)
	if err != nil {
		return provider.Info{}, "", fmt.Errorf("resolving model: %w", err)
	}
	if providerName != "" {
		info.Provider = providerName
		info.Custom = baseURL != ""
	}
	// The role named no provider, so the configured URL could only be found
	// once the model itself resolved one.
	if baseURL == "" {
		baseURL = cfg.ResolveBaseURLs()[info.Provider]
		if baseURL != "" {
			info.Custom = true
		}
	}
	if err := provider.ValidateModel(info); err != nil {
		return provider.Info{}, "", fmt.Errorf("model validation: %w", err)
	}
	return info, baseURL, nil
}

// sessionContextWindow returns the model's context window, preferring the size
// a running Ollama reports over the static table.
func sessionContextWindow(ctx context.Context, info provider.Info, baseURL string) int64 {
	if info.Ollama {
		if n := provider.OllamaContextWindowSize(ctx, baseURL, info.Model); n > 0 {
			return n
		}
	}
	return provider.ContextWindowSizeFor(info.Provider, info.Model)
}

// sessionResources holds the tools and callbacks a session runs with, plus the
// cleanup that releases the sandbox, orchestrator and LSP manager behind them.
type sessionResources struct {
	coreTools      []adktool.Tool
	beforeCBs      []llmagent.BeforeToolCallback
	afterCBs       []llmagent.AfterToolCallback
	beforeModelCBs []llmagent.BeforeModelCallback
	proxy          *streamProxy
	bashSup        *tools.BashSupervisor
	cleanup        func()
}

// buildSessionResources opens the sandbox and starts the subagent orchestrator
// and LSP manager, assembling the tool set and callbacks around them. On
// failure it releases whatever it had already opened.
func buildSessionResources(rt RuntimeConfig, cfg config.Config, turn PromptTurn, cwd string, span trace.Span) (*sessionResources, error) {
	sandboxRoot := cwd
	if rt.SandboxRootFunc != nil {
		sandboxRoot = rt.SandboxRootFunc(turn)
	}

	sandbox, err := tools.NewSandbox(sandboxRoot, os.Getenv("PI_WORKTREE_ROOT"))
	if err != nil {
		return nil, fmt.Errorf("creating sandbox: %w", err)
	}
	span.SetAttributes(attribute.String("session.sandbox_root", sandbox.Dir()))
	if home, hErr := os.UserHomeDir(); hErr == nil {
		_ = sandbox.AddExtraDir(filepath.Join(home, ".pi-go"))
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

	orch := newSessionOrchestrator(rt, cfg, cwd)
	lspMgr := lsp.NewManager(nil)
	cleanup := func() {
		// Backgrounded commands outlive the turn by design, so the session
		// teardown is the only thing that will ever reap them.
		bashSup.KillAll()
		lspMgr.Shutdown()
		orch.Shutdown()
		_ = sandbox.Close()
	}

	agentTools, err := tools.AgentTools(orch, func(agentID, eventType, content string) {})
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("creating agent tools: %w", err)
	}
	coreTools = append(coreTools, agentTools...)

	lspTools, err := tools.LSPTools(lspMgr)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("creating LSP tools: %w", err)
	}
	coreTools = append(coreTools, lspTools...)

	hooks := convertHooks(cfg.Hooks)
	beforeCBs := extension.BuildBeforeToolCallbacks(hooks)
	afterCBs := extension.BuildAfterToolCallbacks(hooks)

	// The streamProxy is set to the current turn's stream before each RunStreaming
	// call, so tool-call events reach the active ACP peer even though the agent
	// instance is shared across turns.
	proxy := &streamProxy{}
	toolBefore, toolAfter := extension.BuildToolCallCallbacks(proxy)
	beforeCBs = append(beforeCBs, toolBefore...)
	afterCBs = append(afterCBs, toolAfter...)
	afterCBs = append(afterCBs, lsp.BuildLSPAfterToolCallback(lspMgr))

	// Inject image bytes (screenshots) as visible InlineData parts for the model.
	beforeModelCBs := []llmagent.BeforeModelCallback{
		extension.BuildReadImageCallback(sandbox),
	}

	return &sessionResources{
		coreTools:      coreTools,
		beforeCBs:      beforeCBs,
		afterCBs:       afterCBs,
		beforeModelCBs: beforeModelCBs,
		proxy:          proxy,
		bashSup:        bashSup,
		cleanup:        cleanup,
	}, nil
}

// newSessionOrchestrator starts the subagent orchestrator for cwd. Agent
// discovery is best-effort: a failure just means no custom agents.
func newSessionOrchestrator(rt RuntimeConfig, cfg config.Config, cwd string) *subagent.Orchestrator {
	discovery, err := subagent.DiscoverAgents(cwd, subagent.ScopeBoth)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pi-go: warning: agent discovery failed: %v\n", err)
	}
	var agentConfigs []subagent.AgentConfig
	if discovery != nil {
		agentConfigs = discovery.All
	}

	orch := subagent.NewOrchestrator(&cfg, detectGitRoot(cwd), agentConfigs)
	orch.SetProviderOptions(rt.BaseURL, rt.Insecure, rt.Headers)
	return orch
}

// runPromptTurn runs one prompt turn against the cached pi session.
func runPromptTurn(ctx context.Context, turn PromptTurn, ps *piSessionState, stream *adapter.Stream) (PromptResult, error) {
	retryCfg := piagent.DefaultRetryConfig()
	for ev, err := range piagent.WithRetry(retryCfg, func() iter.Seq2[*adksession.Event, error] {
		return ps.agent.RunStreaming(ctx, ps.sessionID, turn.Prompt)
	}) {
		if err != nil {
			if ctx.Err() != nil {
				return PromptResult{FinalText: stream.Final(), StopReason: acp.StopReasonCancelled}, nil
			}
			return PromptResult{}, fmt.Errorf("agent run: %w", err)
		}
		// Without this a provider failure ends the turn with StopReasonEndTurn
		// and empty text, and the ACP client shows nothing. See EventError.
		if evErr := piagent.EventError(ev); evErr != nil {
			return PromptResult{}, fmt.Errorf("agent run: %w", evErr)
		}
		if err := stream.OnEvent(ctx, ev); err != nil {
			return PromptResult{}, fmt.Errorf("stream event: %w", err)
		}
	}
	if ctx.Err() != nil {
		return PromptResult{FinalText: stream.Final(), StopReason: acp.StopReasonCancelled}, nil
	}
	return PromptResult{FinalText: stream.Final(), StopReason: acp.StopReasonEndTurn}, nil
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

// buildMCPToolsetsFromCfg converts cfg.MCP servers into resilient ADK toolsets
// so the ACP server exposes the same MCP tools that the TUI/interactive paths
// already use. A nil return means no MCP servers are configured.
func buildMCPToolsetsFromCfg(cfg config.Config) []adktool.Toolset {
	if cfg.MCP == nil || len(cfg.MCP.Servers) == 0 {
		return nil
	}
	servers := make([]extension.MCPServerConfig, len(cfg.MCP.Servers))
	for i, s := range cfg.MCP.Servers {
		servers[i] = extension.MCPServerConfig{
			Name:    s.Name,
			Command: s.Command,
			Args:    s.Args,
			URL:     s.URL,
			Headers: s.Headers,
		}
	}
	ts, _ := extension.BuildMCPToolsets(servers)
	return ts
}

func detectGitRoot(dir string) string {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
