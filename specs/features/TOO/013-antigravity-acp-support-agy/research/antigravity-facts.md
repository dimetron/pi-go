# Antigravity / `agy` Facts and Blockers

## User-provided facts

- Desired user-facing subagent name: `antigravity`.
- The initial goal was to add a new ACP subagent adapter for Google Antigravity.
- The current blocker is that `agy` does not expose ACP options.
- User asked to research what can be done despite that blocker.

## Repository references

- The current source code has no implementation references to an Antigravity or `agy` ACP adapter.
- `TODO.md` contains: `rem gemini -> migrate to Antigravity 2.0`.
- `docs/DESIGN-COMPARISON.md` mentions Antigravity as a missing subscription/OAuth/provider area. That is an LLM-provider/authentication concern, separate from an ACP subagent adapter.
- Temporary/reference material under `tmp/` contains prior notes about an `agy` ACP support idea, but `tmp/` is not part of the live implementation.

## Local CLI observation

- `command -v agy` found `/Users/dimetron/.local/bin/agy` in this environment.
- `command -v antigravity` returned nothing.
- Attempts to run `agy --help`, `agy -h`, and `agy --version` in this non-interactive shell produced no usable help/version output through the tool runner.
- Because the CLI did not return help text here, the only reliable current blocker remains the user-provided fact: `agy` has no ACP options.

## ACP requirement mismatch

pi-go's existing ACP subagent integration expects the target agent process to speak ACP over stdin/stdout using `github.com/coder/acp-go-sdk` client-side connection flow:

1. pi-go starts an ACP-capable subprocess.
2. pi-go sends `initialize`.
3. pi-go sends `newSession`.
4. pi-go sends a prompt turn.
5. The subprocess emits ACP session updates and a prompt response.

Existing adapters are only thin command launchers around CLIs that already provide an ACP stdio mode:

- Claude: external ACP adapter package via `bunx ...claude-agent-acp...`
- Gemini: `gemini --acp`
- Cursor: `agent acp`
- Copilot: `copilot --acp --stdio`

If `agy` has only TUI or non-interactive prompt modes and no ACP stdio mode, it cannot be plugged into the current direct ACP adapter pattern as-is.

## Observable options from current architecture

These are factual integration shapes the current codebase can support or nearly support:

1. Direct ACP adapter, same as Gemini/Cursor/Copilot:
   - Requires an upstream `agy` command mode that speaks ACP over stdio.
   - Current blocker: `agy` has no such option.

2. Environment-overridden direct command:
   - Existing adapters support env vars like `PI_ACP_GEMINI_CMD` and parse a full command string.
   - This pattern would allow users to point pi-go at a future ACP-capable `agy` command without hardcoding final upstream argv.
   - It still requires the command to speak ACP.

3. In-process proxy around non-ACP `agy`:
   - `internal/acp/client/session.go` already documents in-process mode via `NewInProcessSession` and a supplied connection, suitable for an in-process proxy.
   - Such a proxy could expose ACP semantics to pi-go while internally launching a non-ACP `agy` prompt command.
   - This would not be the same as real ACP support from `agy`; it would be a compatibility shim limited by whatever non-ACP `agy` can do.

## Limitations of a non-ACP proxy

Without upstream ACP support, a proxy cannot objectively provide all behaviors of real ACP unless `agy` has equivalent non-ACP controls. Based on pi-go's ACP flow, likely hard requirements include:

- streaming assistant message chunks or equivalent stdout streaming;
- a way to submit a prompt non-interactively;
- process cancellation when pi-go cancels a session or sees the completion sentinel;
- meaningful exit status and stderr diagnostics;
- working-directory control;
- environment forwarding.

Features that may not map cleanly through a simple prompt-mode process:

- true ACP `session/update` event types such as tool calls and plan/progress metadata;
- mid-turn permission requests through ACP callbacks;
- session resumption by ACP session ID;
- terminal/file callback methods;
- exact `StopReason` values from ACP prompt responses.

## Build and test commands

Discovered from `Makefile`:

- `make build` runs:
  - `go build -ldflags "-X github.com/dimetron/pi-go/internal/cli.BuildTag=$(git rev-parse --short HEAD 2>/dev/null || echo local)" ./cmd/pi`
  - `go build ./cmd/pi-sandbox`
- `make test` delegates to `make test-unit`, which runs `go test ./...`.
- `make vet` runs `go vet ./...`.
- `make lint` runs `golangci-lint run ./...`.

For an Antigravity ACP/proxy adapter, likely targeted tests would include:

- `go test ./internal/acp/client/...`
- `go test ./internal/subagent/...`
- `go test ./internal/tools/...` if tool descriptions or exposed agent lists change.
