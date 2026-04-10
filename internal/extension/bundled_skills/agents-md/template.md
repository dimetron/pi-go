# AGENTS.md

> {ONE_PARAGRAPH_DESCRIPTION}

## Quick start

```bash
{SETUP_COMMAND}
{BUILD_COMMAND}
{TEST_COMMAND}
{RUN_COMMAND}
```

## Repository layout

```
{TREE}
```

## Conventions

- {STYLE}
- {COMMIT_FORMAT}
- {PR_RULES}

## Do not touch

- {GENERATED_PATHS}
- {VENDORED_DEPS}

## Testing policy

- {COVERAGE_RULE}
- Run a single test: `{SINGLE_TEST_CMD}`

## Gotchas

- {GOTCHAS}

---

<!-- Append only the language sections that apply -->

## Go

- Toolchain: Go {GO_VERSION}
- Module: `{MODULE_PATH}`
- Build: `go build ./...`
- Test: `go test ./...` — single: `go test ./{PKG} -run {TEST_NAME} -v`
- Race: `go test -race ./...`
- Lint: `golangci-lint run`
- Format: `gofmt -s -w . && goimports -w .`
- Errors: wrap with `fmt.Errorf("...: %w", err)`
- Logging: `log/slog`
- Context: `ctx context.Context` always first arg

## Rust

- Toolchain: `rust-toolchain.toml` ({CHANNEL})
- Build: `cargo build` / `cargo build --release`
- Test: `cargo test` — single: `cargo test {NAME} -- --nocapture`
- Lint: `cargo clippy --all-targets --all-features -- -D warnings`
- Format: `cargo fmt --all`
- Errors: `thiserror` (lib) / `anyhow` (bin)
- MSRV: {MSRV}

## TypeScript

- Package manager: {PM}
- Node: {NODE_VERSION}
- Install: `{PM} install`
- Build: `{PM} run build`
- Test: `{PM} test` — single: `{PM} test -- {PATTERN}`
- Lint: `{PM} run lint`
- Typecheck: `{PM} run typecheck`
- Format: `{PM} run format`
- `tsconfig.json` is strict — do not relax per-file

## Java

- JDK: {JDK_VERSION}
- Build tool: {BUILD_TOOL}
- Build: `{BUILD_CMD}`
- Test: `{TEST_CMD}` — single: `{SINGLE_TEST_CMD}`
- Lint: `{LINT_CMD}`
- Format: `{FORMAT_CMD}`
- Style: Google Java Format, no wildcard imports
- Logging: SLF4J
