# Contributing to pi-go

First off, thank you for considering contributing to pi-go! It's a complex project involving LLMs, TUIs, and system-level sandboxing, so we appreciate your help in making it better.

## Getting Started

### Prerequisites
- **Go 1.27+**: Ensure you have the latest stable Go version installed.
- **LLM API Keys**: You'll need at least one API key (`ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, or `GEMINI_API_KEY`) to test the agent's functionality.
- **Git**: For version control.

### Setup
1. Clone the repository:
   ```bash
   git clone https://github.com/dimetron/pi-go.git
   cd pi-go
   ```
2. Build the binary:
   ```bash
   make install
   ```
3. Run the agent:
   ```bash
   pi --model minimax-m3:cloud
   ```

## Development Workflow

### Branching & Commits
- **Branches**: Use descriptive branch names (e.g., `feat/lsp-diagnostics`, `fix/session-compaction`).
- **Commits**: We use [Conventional Commits](https://www.conventionalcommits.org/).
  - `feat: ...` for new features.
  - `fix: ...` for bug fixes.
  - `docs: ...` for documentation changes.
  - `refactor: ...` for code changes that neither fix a bug nor add a feature.
  - `chore: ...` for maintenance tasks.

### Coding Guidelines
To keep the codebase maintainable, please follow these principles:
- **Surgical Edits**: Make the smallest change necessary. Avoid large-scale refactoring unless specifically requested or required for a feature.
- **Match Existing Patterns**: Use the same naming conventions, error handling, and structure as the surrounding code.
- **Error Handling**: Always wrap errors to provide context:
  ```go
  if err != nil {
      return fmt.Errorf("failed to load config: %w", err)
  }
  ```
- **No `init()` Functions**: Prefer explicit initialization to ensure predictability and testability.
- **Internal Packages**: All non-CLI code should reside under the `internal/` directory.

## Testing & Verification

Before submitting a Pull Request, please verify your changes:

### Build and Lint
```bash
make build  # Ensure the binary compiles
make lint   # Run go vet and linters
```

### Run Tests
- **Unit Tests**:
  ```bash
  make test
  ```
- **E2E Integration Tests**:
  ```bash
  make e2e
  ```

## Profiling

pi-go includes built-in pprof profiling support via the `--pprof` flag.

### Quick Start

```bash
# Start with memory (heap) profiling
pi --pprof mem --model glm-5.2:cloud

#PROMPT: explore repository but do not run tests and then check memory usage at go tool pprof http://localhost:6060/debug/pprof/heap

# Then analyze the profile
go tool pprof http://localhost:6060/debug/pprof/heap
```

### Available Profiles

| Profile | Flag Value | URL | Description |
|---------|-----------|-----|-------------|
| Heap/Memory | `mem` | `/debug/pprof/heap` | Memory allocations and live objects |
| CPU | `cpu` | `/debug/pprof/profile` | CPU usage (30s collection by default) |
| Goroutines | `goroutine` | `/debug/pprof/goroutine?debug=1` | All goroutines dump |
| Mutex | `mutex` | `/debug/pprof/mutex?debug=1` | Mutex contention |
| Block | `block` | `/debug/pprof/block?debug=1` | Blocking operations |
| Trace | `trace` | `/debug/pprof/trace` | Execution tracer |

### Custom Port

```bash
pi --pprof mem --pprof-port 9090 "your prompt"

pi --pprof mem --pprof-port 9090 --model minimax-m3:cloud

```

### Analyzing Profiles

```bash
# Heap profile (default for 'mem')
go tool pprof http://localhost:6060/debug/pprof/heap

# CPU profile (requires manual collection while profiling)
go tool pprof http://localhost:6060/debug/pprof/profile

# Goroutine dump (text)
curl http://localhost:6060/debug/pprof/goroutine?debug=1

# Execution trace (requires stopping trace separately)
go tool trace http://localhost:6060/debug/pprof/trace
```

### Notes

- CPU and trace profiles require manually starting/stopping collection via the respective endpoints
- The pprof server runs in a background goroutine alongside the main app
- Default port is `6060`; change with `--pprof-port`

## Contributing MCP Servers to the Example Config

The curated example MCP configuration lives at `.pi-go/mcp-example.json` (the
`.pi-go/` directory is gitignored, so the file is distributed as a reference and
copied into a user's `.pi-go/mcp.json`). It is a **vetted list, not a directory
of everything that exists**.

To keep it trustworthy and to avoid pointing users at abandoned or low-quality
servers, additions to the example config are limited to **well-known MCP servers
that meet a minimum bar for repository stars and credibility**. A submission
that does not meet the bar stays out of the example config — users can still add
the server to their own `.pi-go/mcp.json`.

### Minimum requirements

| Requirement | Bar |
|---|---|
| Repository | Public source for the server (the server's own repo, or the vendor's official repo for a hosted endpoint) |
| Stars | **≥ 100 GitHub stars** on the canonical repository |
| Credibility | Recognizable maintainer, vendor, or active project — not a personal throwaway, a fork with no activity, or a name-squat on someone else's product |
| Activity | Commits or releases within the last **12 months**; issues triaged rather than ignored |
| Safety | No obfuscated binaries, no post-install scripts that fetch and execute remote code, no requests for credentials beyond what the server actually needs |
| Usability | Installable from a documented, public method (`bunx`/`npx`/`uvx`, a release binary, or a documented remote URL) |

Stars are a floor, not a qualification on their own: a 100-star repo that has been
dormant for three years fails the activity and credibility requirements. Where the
canonical distribution is a hosted endpoint rather than a repository (for example
a vendor's own search MCP), judge credibility by the vendor and the endpoint's
documentation, and note the vendor in the PR description.

### What a submission must include

1. **One entry per PR**, added to `.pi-go/mcp-example.json` in the existing shape
   (`command`/`args` for local servers, `url` for remote ones).
2. **Evidence in the PR description**: the repository URL, current star count,
   date of the last release or commit, and the maintainer's identity.
3. **A stated use case**: what the server gives pi-go users that the existing
   entries do not.
4. **Secrets as environment references** (`${MY_API_KEY}`), never literal keys.
   An entry that hard-codes a credential is rejected outright.
5. **No disabled or duplicate entries** — if a server replaces an existing entry,
   propose the removal in the same PR and say why.

### What gets rejected

- New, unproven servers submitted for promotion or discoverability.
- Entries that only work with a paid plan, with no free tier or trial for testing.
- Servers that proxy another provider's API in violation of that provider's terms.
- Anything that requires disabling the sandbox or running with elevated privileges.

Maintainers apply this bar at their discretion; meeting every number above does
not guarantee inclusion, and a server just under the star threshold with strong
credibility (an established vendor or a recognized OSS maintainer) may still be
accepted with a note in the PR.

## Pull Request Process

1. **Self-Review**: Read through your changes. Ensure there are no debug prints (`fmt.Println`) or commented-out code.
2. **Verification**: Confirm that all tests pass and the binary builds.
3. **Documentation**: Update `README.md`, `ARCHITECTURE.md`, or other relevant docs if you've changed how the system works.
4. **PR Description**: Provide a clear description of *what* was changed and *why*. If the PR fixes an issue, reference it (e.g., `Fixes #123`).

## Reporting Issues

- **Bugs**: Use the GitHub Issue tracker. Include a minimal reproduction case and the LLM provider you were using.
- **Feature Requests**: Open an issue describing the desired behavior and the problem it solves.
- **Session Logs**: If you encounter a bug in the agent's behavior, providing a session log from `~/.pi-go/log/` is extremely helpful.

## License

By contributing to pi-go, you agree that your contributions will be licensed under the project's [LICENSE](LICENSE).

