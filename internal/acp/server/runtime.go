package server

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"iter"

	acp "github.com/coder/acp-go-sdk"
	adksession "google.golang.org/adk/session"

	piagent "github.com/dimetron/pi-go/internal/agent"
	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/extension"
	"github.com/dimetron/pi-go/internal/guardrail"
	"github.com/dimetron/pi-go/internal/lsp"
	"github.com/dimetron/pi-go/internal/provider"
	"github.com/dimetron/pi-go/internal/subagent"
	"github.com/dimetron/pi-go/internal/tools"
)

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

// NewPromptHandler returns a real pi-backed ACP prompt handler.
func NewPromptHandler(rt RuntimeConfig) PromptHandler {
	loadConfig := rt.LoadConfig
	if loadConfig == nil {
		loadConfig = config.Load
	}
	return func(ctx context.Context, turn PromptTurn) (PromptResult, error) {
		cfg, err := loadConfig()
		if err != nil {
			return PromptResult{}, fmt.Errorf("loading config: %w", err)
		}
		if rt.Model != "" {
			if cfg.Roles == nil {
				cfg.Roles = map[string]config.RoleConfig{}
			}
			cfg.Roles["default"] = config.RoleConfig{Model: rt.Model}
		}

		modelName, providerName, advisorModel, advisorMaxUses, advisorCaching, err := cfg.ResolveRole("default")
		if err != nil {
			return PromptResult{}, fmt.Errorf("resolving model role: %w", err)
		}

		info, err := provider.Resolve(modelName)
		if err != nil {
			return PromptResult{}, fmt.Errorf("resolving model: %w", err)
		}
		if providerName != "" {
			info.Provider = providerName
		}
		if err := provider.ValidateModel(info); err != nil {
			return PromptResult{}, fmt.Errorf("model validation: %w", err)
		}

		apiKey := config.APIKeys()[info.Provider]
		if apiKey == "" && info.Provider != "gemini" && info.Provider != "ollama" && info.Provider != "azure" && !info.Ollama {
			return PromptResult{}, fmt.Errorf("no API key found for provider %q (set %s)", info.Provider, providerEnvVar(info.Provider))
		}

		baseURL := rt.BaseURL
		if baseURL == "" {
			baseURL = config.BaseURLs()[info.Provider]
		}
		if baseURL == "" && info.Ollama {
			baseURL = "http://localhost:11434"
		}
		if info.Ollama {
			if err := provider.CheckOllama(baseURL); err != nil {
				return PromptResult{}, fmt.Errorf("ollama health check: %w", err)
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
			return PromptResult{}, fmt.Errorf("creating LLM provider: %w", err)
		}
		tokenTracker := guardrail.New(cfg.MaxDailyTokens)
		tokenTracker.SetContextWindowSize(provider.ContextWindowSize(info.Model))
		llm = guardrail.WrapModel(llm, tokenTracker)

		cwd := turn.CWD
		if strings.TrimSpace(cwd) == "" {
			wd, err := os.Getwd()
			if err != nil {
				return PromptResult{}, fmt.Errorf("getting working directory: %w", err)
			}
			cwd = wd
		}
		sandboxRoot := cwd
		if rt.SandboxRootFunc != nil {
			sandboxRoot = rt.SandboxRootFunc(turn)
		}
		worktreeDir := os.Getenv("PI_WORKTREE_ROOT")

		sandbox, err := tools.NewSandbox(sandboxRoot, worktreeDir)
		if err != nil {
			return PromptResult{}, fmt.Errorf("creating sandbox: %w", err)
		}
		defer func() { _ = sandbox.Close() }()
		if home, hErr := os.UserHomeDir(); hErr == nil {
			_ = sandbox.AddExtraDir(filepath.Join(home, ".pi-go"))
		}

		coreTools, err := tools.CoreTools(sandbox)
		if err != nil {
			return PromptResult{}, fmt.Errorf("creating core tools: %w", err)
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
		orch.SetProviderOptions(rt.BaseURL, rt.Insecure, rt.Headers)
		defer orch.Shutdown()

		agentTools, err := tools.AgentTools(orch, func(agentID, eventType, content string) {})
		if err != nil {
			return PromptResult{}, fmt.Errorf("creating agent tools: %w", err)
		}
		coreTools = append(coreTools, agentTools...)

		hooks := convertHooks(cfg.Hooks)
		beforeCBs := extension.BuildBeforeToolCallbacks(hooks)
		afterCBs := extension.BuildAfterToolCallbacks(hooks)

		lspMgr := lsp.NewManager(nil)
		defer lspMgr.Shutdown()
		lspTools, err := tools.LSPTools(lspMgr)
		if err != nil {
			return PromptResult{}, fmt.Errorf("creating LSP tools: %w", err)
		}
		afterCBs = append(afterCBs, lsp.BuildLSPAfterToolCallback(lspMgr))
		coreTools = append(coreTools, lspTools...)

		instruction := rt.System
		if instruction == "" {
			instruction = piagent.LoadInstruction(piagent.SystemInstruction)
		}

		ag, err := piagent.New(piagent.Config{
			Model:               llm,
			Tools:               coreTools,
			Instruction:         instruction,
			BeforeToolCallbacks: beforeCBs,
			AfterToolCallbacks:  afterCBs,
		})
		if err != nil {
			return PromptResult{}, fmt.Errorf("creating agent: %w", err)
		}
		sessionID, err := ag.CreateSession(ctx)
		if err != nil {
			return PromptResult{}, fmt.Errorf("creating session: %w", err)
		}

		var final strings.Builder
		retryCfg := piagent.DefaultRetryConfig()
		for ev, err := range piagent.WithRetry(retryCfg, func() iter.Seq2[*adksession.Event, error] {
			return ag.RunStreaming(ctx, sessionID, turn.Prompt)
		}) {
			if err != nil {
				if ctx.Err() != nil {
					return PromptResult{FinalText: strings.TrimSpace(final.String()), StopReason: acp.StopReasonCancelled}, nil
				}
				return PromptResult{}, fmt.Errorf("agent run: %w", err)
			}
			if ev == nil || ev.Content == nil {
				continue
			}
			for _, part := range ev.Content.Parts {
				if part.Text == "" || ev.Content.Role == "user" || ev.Content.Role == "thinking" {
					continue
				}
				final.WriteString(part.Text)
				if turn.Updater != nil {
					if err := turn.Updater.Update(ctx, acp.UpdateAgentMessageText(part.Text)); err != nil {
						return PromptResult{}, fmt.Errorf("session update: %w", err)
					}
				}
			}
		}
		if ctx.Err() != nil {
			return PromptResult{FinalText: strings.TrimSpace(final.String()), StopReason: acp.StopReasonCancelled}, nil
		}
		return PromptResult{FinalText: strings.TrimSpace(final.String()), StopReason: acp.StopReasonEndTurn}, nil
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

func detectGitRoot(dir string) string {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
