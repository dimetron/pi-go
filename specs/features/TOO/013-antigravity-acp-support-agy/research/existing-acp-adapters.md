# Existing ACP Subagent Adapters

## Current ACP agent registry

- ACP-backed subagent names are hard-coded in `internal/subagent/orchestrator.go` in `acpAgentNames`.
- Current names in that map: `claude`, `gemini`, `cursor`, `copilot`.
- `isACPAgent(name string) bool` checks membership in that map.
- In `Orchestrator.Spawn`, ACP agents route through `dispatchACP(ctx, spawnOpts, agent.Name)` instead of the normal pi subprocess spawner.

## Dispatch flow

- `internal/subagent/spawner_acp.go` owns ACP dispatch.
- `dispatchACP(ctx, opts, agentName)`:
  - requires a non-empty prompt;
  - wraps the prompt using `acpPromptPreamble(agentName, opts.Prompt)`;
  - prepends `opts.Instruction` when present;
  - applies `ResolveTimeout(opts.Timeout)` and starts a context with an absolute timeout;
  - calls `startACPSessionFn(procCtx, agentName, prompt, opts)`;
  - returns a `*subagent.Process` whose events are fed by `pumpACPSession`.
- `acpPromptPreamble` asks ACP agents to emit `<Task Completed>!` when done and includes anti-hallucination verification rules.
- `pumpACPSession` translates shared ACP events into subagent process events:
  - `message` -> `text_delta`
  - `progress` / `tool` -> `tool_call`
  - `stderr` -> `stderr`
  - `error` -> `error`
- `pumpACPSession` watches streamed text for `<Task Completed>` and calls `sess.Cancel()` when seen, then coerces the final result to success and strips the sentinel.

## ACP runner selection

`startACPSession` switches on `agentName`:

- `claude` -> `internal/acp/client/claudecode.Runner`
- `gemini` -> `internal/acp/client/gemini.Runner`
- `cursor` -> `internal/acp/client/cursor.Runner`
- `copilot` -> `internal/acp/client/copilot.Runner`
- default -> `unknown ACP agent` error

The `SpawnOpts.Model` value is intentionally not forwarded to ACP agents. The comment says ACP CLIs have their own model namespaces/defaults and should be configured through their own CLI configuration.

## Shared ACP client runner

- `internal/acp/client/runner.go` contains `client.Runner`, which launches a local ACP-capable subprocess.
- It wires stdin/stdout to `github.com/coder/acp-go-sdk` via `acp.NewClientSideConnection(client, stdin, stdout)`.
- The client callbacks currently implement:
  - `RequestPermission` using pi-go's auto-approval policy;
  - `SessionUpdate` forwarding to the running session;
  - file and terminal methods as unimplemented / method-not-found.
- `client.Runner.StartCommand` accepts an already-created `*exec.Cmd` and shared `RunRequest`.

## RunningSession capabilities

- `internal/acp/client/session.go` documents two modes:
  1. subprocess mode via `newRunningSession` owning an `*exec.Cmd` and stdio pipes;
  2. in-process mode via `NewInProcessSession`, intended for a supplied `acp.ClientSideConnection` such as a `net.Pipe` and an in-process proxy.
- `RunningSession` exposes the interface used by the subagent dispatcher:
  - `Events() <-chan shared.Event`
  - `Done() <-chan struct{}`
  - `Cancel() error`
  - `Wait() shared.RunResult`
- Subprocess `Cancel()` kills the child process; in-process `Cancel()` invokes a supplied cancel hook.

## Per-adapter command patterns

- Claude Code:
  - package: `internal/acp/client/claudecode`
  - default command: `bunx -y @agentclientprotocol/claude-agent-acp@latest`
  - env override: `PI_ACP_CLAUDE_CMD`
- Gemini CLI:
  - package: `internal/acp/client/gemini`
  - binary: `gemini`
  - default args: `--acp`
  - env override: `PI_ACP_GEMINI_CMD`
- Cursor:
  - package: `internal/acp/client/cursor`
  - binary: `agent` or `cursor-agent`
  - default subcommand: `acp`
  - env override: `PI_ACP_CURSOR_CMD`
- GitHub Copilot:
  - package: `internal/acp/client/copilot`
  - binary: `copilot`
  - default args: `--acp --stdio`
  - env override: `PI_ACP_COPILOT_CMD`

All four adapter packages follow the same shape:

```go
type Runner struct {
    ClientInfo acp.Implementation
    Binary     string
    Logger     *slog.Logger
    ExtraEnv   []string
}

type RunRequest struct {
    Prompt    string
    SessionID string
    CWD       string
    Env       []string
    Command   []string // test override
    // optional CLI-specific fields
}

func (r Runner) Start(ctx context.Context, req RunRequest) (*client.RunningSession, error)
```

## Bundled agent definitions

- Bundled subagent markdown files live in `internal/subagent/bundled/` and are embedded by `internal/subagent/embed.go` via `//go:embed bundled/*.md`.
- Current bundled files include `claude.md`, `gemini.md`, `cursor.md`, and `copilot.md` plus non-ACP agents.
- `LoadBundledAgents` reads all embedded markdown files and sets `Source = "bundled"`.
- Tests currently assert there are 17 bundled agents in `internal/subagent/agents_test.go` and `internal/subagent/types_test.go`.
- Existing ACP bundled agent frontmatter uses `role: default`, `worktree: false`, and tool list `read, grep, find, tree, ls, bash, edit, write`.

## Relevant tests

- `internal/subagent/orchestrator_test.go` has `TestIsACPAgent` for ACP routing membership. It currently lists `claude`, `gemini`, and `cursor` as true; the production map also contains `copilot`.
- `internal/subagent/orchestrator_parallel_test.go` exercises mixed pi-backed and ACP-backed agents using a fake `startACPSessionFn`.
- `internal/subagent/spawner_acp_test.go` tests prompt wrapping, sentinel handling, start errors, instruction prefixing, and dispatch behavior with fake ACP sessions.
- Each existing ACP client adapter package has command-resolution tests for prompt validation, binary lookup, env overrides, and command overrides.
