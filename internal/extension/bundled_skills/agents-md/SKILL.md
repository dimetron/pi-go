---
name: agents-md
description: Generate AGENTS.md files for Go, Rust, TypeScript, and Java projects. Use this skill whenever the user asks to create, scaffold, bootstrap, update, or review an AGENTS.md (or agent-instructions, CLAUDE.md, repo guide for agents) file in a codebase. Also trigger when the user says "add AGENTS.md", "make this repo agent-friendly", "document build/test commands for Claude Code", or uploads a repo and asks Claude Code to learn its conventions. Produces a spec-loose, Claude-Code-optimized AGENTS.md with a shared core section plus short language-specific appendices for the stacks present in the repo.
---

# AGENTS.md Generator

## Purpose

AGENTS.md is a top-level Markdown file that tells coding agents (Claude Code, Cursor, etc.) how to work inside a repository: how to build, test, lint, where code lives, what conventions to follow, and what NOT to touch. A good AGENTS.md turns a cold-start agent into a productive one in a single read.

This skill generates such files. It is **loosely inspired by the agents.md open standard** but optimized for Claude Code workflows.

## When to use

- User asks to create / bootstrap / scaffold AGENTS.md (or CLAUDE.md, AGENT.md)
- User hands Claude a repo and asks it to "learn the conventions" or "make it agent-friendly"
- User wants to update an existing AGENTS.md after a stack change
- User asks for a template they can fill in manually

## Workflow

1. **Detect languages present.** Look for these marker files:
   - Go → `go.mod`, `go.sum`
   - Rust → `Cargo.toml`, `Cargo.lock`
   - TypeScript → `package.json` + `tsconfig.json` (check for `pnpm-lock.yaml` / `yarn.lock` / `package-lock.json` / `bun.lockb`)
   - Java → `pom.xml` (Maven) or `build.gradle[.kts]` / `settings.gradle[.kts]` (Gradle)

   If multiple, it's a polyglot repo — include an appendix for each. If none detected, ask the user which stacks to target.

2. **Inspect before writing.** Run the actual build/test tool to confirm commands work, read 1–2 representative source files to pick up naming style, check `.golangci.yml`, `clippy.toml`, `eslint.config.*`, `checkstyle.xml` for lint config. Never invent commands that weren't verified.

3. **Assemble from the template.** Use the structure in `template.md` (same directory). Fill the **Shared Core** always, then append only the language sections that apply.

4. **Keep it short.** Target ~150–300 lines total. AGENTS.md is read on every agent session — brevity matters more than completeness. Link out to deeper docs rather than inline them.

5. **Place at repo root** as `AGENTS.md`. If the repo already has `CLAUDE.md`, ask whether to replace, merge, or keep both (CLAUDE.md can be a symlink or short pointer to AGENTS.md).

## Structure (Shared Core — always include)

```
# AGENTS.md

> One-paragraph repo description: what it is, who uses it, what stage.

## Quick start
- Setup: <single command or 2-3 lines>
- Build: <command>
- Test: <command>
- Run locally: <command>

## Repository layout
<tree of top-level dirs with one-line purpose each>

## Conventions
- <code style, naming, formatting tool>
- <commit message format>
- <branch / PR rules>

## Do not touch
- <generated files, vendored deps, migrations, etc.>

## Testing policy
- <what must have tests, coverage expectations>
- <how to run a single test>

## Gotchas
- <known footguns, non-obvious env vars, flaky tests>
```

## Language Appendices (include only the ones that apply)

### Go appendix

```
## Go

- Toolchain: Go <version from go.mod>
- Module: <module path>
- Build: `go build ./...`
- Test: `go test ./...` — single test: `go test ./pkg/foo -run TestName -v`
- Race: `go test -race ./...`
- Lint: `golangci-lint run` (config: `.golangci.yml`)
- Format: `gofmt -s -w .` and `goimports -w .`
- Deps: `go mod tidy` after any import change
- Errors: wrap with `fmt.Errorf("...: %w", err)`; use `errors.Is/As`
- Logging: `log/slog` (structured, no `fmt.Println` in library code)
- Context: first arg is always `ctx context.Context`
```

### Rust appendix

```
## Rust

- Toolchain: pinned in `rust-toolchain.toml` (<channel>)
- Build: `cargo build` — release: `cargo build --release`
- Test: `cargo test` — single: `cargo test <name> -- --nocapture`
- Lint: `cargo clippy --all-targets --all-features -- -D warnings`
- Format: `cargo fmt --all`
- Deps: `cargo update -p <crate>` for targeted bumps; avoid blanket `cargo update`
- Errors: `thiserror` for libraries, `anyhow` for binaries
- Unsafe: must be justified in a `// SAFETY:` comment
- MSRV: <version> — do not use features beyond this
```

### TypeScript appendix

```
## TypeScript

- Package manager: <pnpm|npm|yarn|bun> — do not mix
- Node: <version from .nvmrc or package.json engines>
- Install: `<pm> install`
- Build: `<pm> run build`
- Test: `<pm> test` — single: `<pm> test -- <pattern>` (vitest/jest)
- Lint: `<pm> run lint` (eslint + <config>)
- Typecheck: `<pm> run typecheck` or `tsc --noEmit`
- Format: `<pm> run format` (prettier)
- Strict mode: `tsconfig.json` has `"strict": true` — do not disable per-file
- Imports: use path aliases from `tsconfig.json`, not deep relative paths
```

### Java appendix

```
## Java

- JDK: <version> (use SDKMAN or Temurin)
- Build tool: <Maven | Gradle>
- Build: `./mvnw clean install` or `./gradlew build`
- Test: `./mvnw test` or `./gradlew test` — single: `-Dtest=ClassName#method` / `--tests "ClassName.method"`
- Lint: `./gradlew check` (checkstyle + spotbugs) or `mvn verify`
- Format: `./gradlew spotlessApply` or `mvn spotless:apply`
- Style: Google Java Format, 4-space indent, no wildcard imports
- Null-safety: annotate with `@Nullable` / `@NonNull` (JSpecify)
- Logging: SLF4J — never `System.out.println` in production code
```

## Writing rules

- **Verify, don't guess.** Every command in the file must have been run successfully (or read from an existing script). Hallucinated `make test` targets are the #1 failure mode.
- **Imperative voice.** "Run `go test ./...`" not "You can run tests with...".
- **Links over prose.** For anything longer than 3 lines, link to `docs/` instead of inlining.
- **No marketing.** Skip the "welcome to our amazing project" opener. Start with what the agent needs.
- **Stable commands only.** If a script is in flux, note it: `# unstable — check Makefile`.
- **Secrets & env.** List required env vars by name; never include values. Point to `.env.example`.

## Example output

See `example-AGENTS.md` in this skill directory for a filled-in polyglot (Go + TypeScript) example.

## Updating an existing AGENTS.md

1. Read the current file end-to-end.
2. Diff against what's actually true now (run the commands, check the tree).
3. Propose a patch, don't rewrite wholesale — preserve the user's voice and project-specific gotchas.
