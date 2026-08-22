package piagent

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	adktool "google.golang.org/adk/v2/tool"

	"github.com/dimetron/pi-go/internal/agent"
	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/extension"
	"github.com/dimetron/pi-go/internal/lsp"
	"github.com/dimetron/pi-go/internal/memory"
	"github.com/dimetron/pi-go/internal/palace"
	"github.com/dimetron/pi-go/internal/subagent"
	"github.com/dimetron/pi-go/internal/tools"
)

// gitCmdTimeout bounds the repository-root lookup so a wedged git never
// stalls construction.
const gitCmdTimeout = 5 * time.Second

// palaceStatusTimeout bounds the drawer count that decides whether palace
// tools are worth advertising.
const palaceStatusTimeout = 2 * time.Second

// memoryShutdownTimeout bounds the memory worker's drain on Close.
const memoryShutdownTimeout = 5 * time.Second

// resolveWorkDir returns the absolute working directory for this agent.
func resolveWorkDir(dir string) (string, error) {
	if dir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("getting working directory: %w", err)
		}
		return cwd, nil
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("resolving working directory %q: %w", dir, err)
	}
	return abs, nil
}

// resolveSessionDir returns where sessions are persisted, defaulting to the
// directory the pi CLI uses so sessions are shared between the two.
func resolveSessionDir(dir string) (string, error) {
	if dir != "" {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving home directory: %w", err)
	}
	return filepath.Join(home, ".pi-go", "sessions"), nil
}

// detectGitRoot returns the repository root containing dir, or "" when dir is
// not in a repository. Subagent worktrees are created relative to it.
func detectGitRoot(ctx context.Context, dir string) string {
	ctx, cancel := context.WithTimeout(ctx, gitCmdTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--show-toplevel")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// buildSandbox roots the file tools at workDir and grants the extra
// directories, always including ~/.pi-go so config, skills and memory remain
// reachable.
func buildSandbox(workDir string, extra []string) (*tools.Sandbox, error) {
	sb, err := tools.NewSandbox(workDir)
	if err != nil {
		return nil, fmt.Errorf("creating sandbox: %w", err)
	}
	dirs := extra
	if home, hErr := os.UserHomeDir(); hErr == nil {
		dirs = append([]string{filepath.Join(home, ".pi-go")}, dirs...)
	}
	for _, dir := range dirs {
		if err := sb.AddExtraDir(dir); err != nil {
			_ = sb.Close()
			return nil, fmt.Errorf("adding sandbox dir %q: %w", dir, err)
		}
	}
	return sb, nil
}

// buildSubagents discovers the agent definitions visible from workDir and
// returns an orchestrator for them. Discovery failures degrade to the bundled
// set rather than failing construction: a malformed .pi-go/agents file should
// not stop the agent from running.
func buildSubagents(ctx context.Context, cfg *config.Config, workDir string) *subagent.Orchestrator {
	discovery, err := subagent.DiscoverAgents(workDir, subagent.ScopeBoth)
	if err != nil {
		slog.Warn("piagent: subagent discovery failed", "error", err)
	}
	var agentConfigs []subagent.AgentConfig
	if discovery != nil {
		agentConfigs = discovery.All
	}
	return subagent.NewOrchestrator(cfg, detectGitRoot(ctx, workDir), agentConfigs)
}

// buildToolsets assembles the external toolsets — MCP servers and A2A agents —
// declared in configuration.
func buildToolsets(cfg config.Config) []adktool.Toolset {
	var toolsets []adktool.Toolset
	if cfg.MCP != nil && len(cfg.MCP.Servers) > 0 {
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
		mcpToolsets, err := extension.BuildMCPToolsets(servers)
		if err != nil {
			slog.Warn("piagent: some MCP toolsets unavailable", "error", err)
		}
		toolsets = append(toolsets, mcpToolsets...)
	}
	if cfg.A2A != nil && len(cfg.A2A.Agents) > 0 {
		toolsets = append(toolsets, tools.NewA2AToolset(cfg.A2A))
	}
	if cfg.LLMS != nil && len(cfg.LLMS.Sources) > 0 {
		toolsets = append(toolsets, tools.NewLLMSCachedToolset(cfg.LLMS))
	}
	return toolsets
}

// buildInstruction assembles the system prompt: pi-go's built-in prompt (or
// the override), the project context files discovered from workDir, the skills
// menu when skills are on, and finally the embedder's own text.
func buildInstruction(o options, workDir string) string {
	base := o.instruction
	if base == "" {
		base = agent.SystemInstruction
	}
	parts := agent.LoadInstructionPartsFor(base, workDir)
	if !o.skillsEnabled {
		parts.Skills = ""
	}
	instruction := parts.String()
	if o.extraPrompt != "" {
		instruction += "\n\n" + o.extraPrompt
	}
	return instruction
}

// memoryDBPath resolves the observation store's location, honoring config and
// falling back to the path the CLI uses.
func memoryDBPath(cfg config.Config) string {
	if cfg.Memory != nil && cfg.Memory.DBPath != "" {
		return cfg.Memory.DBPath
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".pi-go", "memory", "claude-mem.db")
}

// maxPendingObservations resolves the worker's queue depth from config.
func maxPendingObservations(cfg config.Config) int {
	if cfg.Memory != nil && cfg.Memory.MaxPending > 0 {
		return cfg.Memory.MaxPending
	}
	return config.MemoryDefaults().MaxPending
}

// memoryTokenBudget resolves how much of the prompt recalled memory may use.
func memoryTokenBudget(cfg config.Config) int {
	if cfg.Memory != nil && cfg.Memory.TokenBudget > 0 {
		return cfg.Memory.TokenBudget
	}
	return config.MemoryDefaults().TokenBudget
}

// setupMemory opens the observation store and starts its worker. Memory is
// best-effort: any failure downgrades to "no memory" with a warning, and the
// returned closer is always safe to call.
func setupMemory(ctx context.Context, o options, cfg config.Config, orch *subagent.Orchestrator) (memory.Store, *memory.Worker, func()) {
	noop := func() {}
	if !o.memoryEnabled {
		return nil, nil, noop
	}
	db, err := memory.OpenDB(memoryDBPath(cfg))
	if err != nil {
		slog.Warn("piagent: memory disabled", "error", err)
		return nil, nil, noop
	}
	store := memory.NewSQLiteStore(db)
	worker := memory.NewWorker(store, memory.NewSubagentCompressor(orch), maxPendingObservations(cfg))
	worker.Start(ctx)
	return store, worker, func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), memoryShutdownTimeout)
		defer cancel()
		_ = worker.Shutdown(shutdownCtx)
		_ = store.Close()
	}
}

// memoryContext renders the recalled-memory block for the system prompt, or ""
// when there is nothing to recall.
func memoryContext(ctx context.Context, store memory.Store, cfg config.Config, project string) string {
	if store == nil {
		return ""
	}
	out, err := memory.NewContextGenerator(store, memoryTokenBudget(cfg)).Generate(ctx, project)
	if err != nil {
		slog.Warn("piagent: memory context generation failed", "error", err)
		return ""
	}
	if out == "" {
		return ""
	}
	return "\n\n" + out
}

// palacePaths resolves the palace database and embedding model locations.
func palacePaths(cfg config.Config) (dbPath, modelPath string) {
	if cfg.Palace != nil {
		dbPath, modelPath = cfg.Palace.DBPath, cfg.Palace.ModelPath
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return dbPath, modelPath
	}
	if dbPath == "" {
		dbPath = filepath.Join(home, ".pi-go", "palace.db")
	}
	if modelPath == "" {
		modelPath = filepath.Join(home, ".pi-go", "models", "KnightsAnalytics_all-MiniLM-L6-v2")
	}
	return dbPath, modelPath
}

// palaceHasContent reports whether the palace holds at least one drawer. A
// count error counts as "no content": the tools would fail anyway, and the
// question is only whether advertising them is worth the tokens.
func palaceHasContent(p *palace.Palace) bool {
	ctx, cancel := context.WithTimeout(context.Background(), palaceStatusTimeout)
	defer cancel()
	status, err := p.Status(ctx)
	if err != nil {
		slog.Warn("piagent: palace drawer count failed, not advertising palace tools", "error", err)
		return false
	}
	return status.DrawerCount > 0
}

// setupPalace opens the memory palace when one already exists on disk and
// returns its tools plus the wake-up context for the system prompt.
//
// Eleven palace tool declarations cost ~1.6k tokens on every request, and an
// empty palace has nothing for them to find. An existing file is not evidence
// of content — `pi memory init` creates one with zero drawers — so the gate is
// on drawers. The palace is still opened when empty: the observation bridge
// fills it, and the tools appear on a later run.
func setupPalace(o options, cfg config.Config, worker *memory.Worker) ([]adktool.Tool, string, func()) {
	noop := func() {}
	if !o.palaceEnabled {
		return nil, "", noop
	}
	dbPath, modelPath := palacePaths(cfg)
	if dbPath == "" {
		return nil, "", noop
	}
	if _, err := os.Stat(dbPath); err != nil {
		return nil, "", noop
	}

	p, err := palace.New(palace.WithDBPath(dbPath), palace.WithModelPath(modelPath))
	if err != nil {
		slog.Warn("piagent: palace disabled", "error", err)
		return nil, "", noop
	}
	closePalace := func() { _ = p.Close() }

	if worker != nil {
		worker.OnAfterStore(palace.NewObservationBridge(p).ConvertAndStore)
	}
	if !palaceHasContent(p) {
		return nil, "", closePalace
	}

	// A tool-building failure still leaves a usable palace for the bridge and
	// the wake-up context, so it only costs the tools.
	palaceTools, err := palace.PalaceTools(p)
	if err != nil {
		slog.Warn("piagent: palace tools disabled", "error", err)
		palaceTools = nil
	}
	var wakeUp string
	if text, wErr := p.WakeUp(context.Background(), ""); wErr == nil {
		wakeUp = text
	}
	return palaceTools, wakeUp, closePalace
}

// setupLSP starts a manager and returns the tools to advertise. The manager is
// returned regardless so the after-tool hook stays wired: it costs nothing when
// no server starts.
//
// LSP declarations are billed on every request, and with no server installed
// every call they enable fails — the model would pay tokens for capability it
// cannot use. So gate on a server existing, then advertise only as much surface
// as the mode asks for.
func setupLSP(mode LSPMode) (*lsp.Manager, []adktool.Tool, error) {
	mgr := lsp.NewManager(nil)
	if mode == LSPOff || !mgr.AnyAvailable() {
		return mgr, nil, nil
	}
	parsed, ok := tools.ParseLSPMode(string(mode))
	if !ok {
		slog.Warn("piagent: unknown LSP mode, using minimal", "mode", string(mode))
	}
	lspTools, err := tools.LSPToolsFor(mgr, parsed)
	if err != nil {
		mgr.Shutdown()
		return nil, nil, fmt.Errorf("creating LSP tools: %w", err)
	}
	return mgr, lspTools, nil
}

// compactorConfig overlays configured compactor settings on the defaults,
// ignoring zero values so an unset field keeps its default.
func compactorConfig(cfg config.Config) tools.CompactorConfig {
	out := tools.DefaultCompactorConfig()
	if cfg.Compactor == nil {
		return out
	}
	if cfg.Compactor.Enabled != nil {
		out.Enabled = *cfg.Compactor.Enabled
	}
	if cfg.Compactor.SourceCodeFiltering != "" {
		out.SourceCodeFiltering = cfg.Compactor.SourceCodeFiltering
	}
	if cfg.Compactor.MaxChars > 0 {
		out.MaxChars = cfg.Compactor.MaxChars
	}
	if cfg.Compactor.MaxLines > 0 {
		out.MaxLines = cfg.Compactor.MaxLines
	}
	return out
}

// convertHooks maps configured tool hooks onto the extension package's shape.
func convertHooks(cfgHooks []config.HookConfig) []extension.HookConfig {
	out := make([]extension.HookConfig, len(cfgHooks))
	for i, h := range cfgHooks {
		out[i] = extension.HookConfig{
			Event:   h.Event,
			Command: h.Command,
			Tools:   h.Tools,
			Timeout: h.Timeout,
		}
	}
	return out
}
