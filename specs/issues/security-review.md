# Security review: `pi serve` is an unauthenticated remote shell

## Summary

A security review of the repository on 2026-08-28 found one critical issue and a
set of supporting ones, all in the `pi serve` HTTP surface. **Anyone who can
reach TCP 8765 can obtain an interactive agent session — and therefore arbitrary
code execution as the user, with every provider API key inherited — in three
unauthenticated HTTP requests.**

Every claim below carries a `file:line` citation and was independently verified
against the source before being written down. Where something was *not*
reproduced, it says so.

The review also covered command injection, the `os.Root` sandbox, credential
redaction, TLS configuration, `pirpc`, `unsafe`, and deserialization. Those came
back clean and are recorded at the end, because a clean result is a result.

---

## CRITICAL — unauthenticated remote code execution

Four independently reasonable decisions compose into full compromise.

| Step | Location | What it does |
|---|---|---|
| Bind | `internal/webserver/handlers.go:41` | `DefaultAddr = ":8765"` — every interface, not loopback |
| Leak | `internal/webserver/server.go:153` | `handleCreatePair` accepts `GET` and `POST` with **no authentication** and returns the full `PairResponse` |
| Token in that response | `internal/webserver/pairing.go:218-220` | `PairResponse.Token` is serialized as `"token"` |
| Approve | `internal/webserver/server.go:210-249` | `POST /api/pair/submit {"code":…}` approves that token, **no authentication** |
| Execute | `internal/webserver/server.go:299-340` → `internal/webserver/pty.go:249` | `GET /ws/x?token=…` spawns `os.Executable()` in `cfg.Project` on a PTY |

### Attack

1. `GET /api/pair` → `{"code": …, "token": …}`
2. `POST /api/pair/submit` with that code → the token becomes approved
3. `GET /ws/anything?token=…` → interactive pi-go session

The session exposes the agent's `bash` tool, so step 3 is arbitrary code
execution as the invoking user. The child process is started with
`cmd.Env = append(os.Environ(), …)` (`internal/webserver/pty.go:251`), so it
inherits every provider API key in the environment.

`getOrCreateActivePair` (`internal/webserver/server.go:388-405`) **reuses** a
pending pair rather than minting a new one, so step 1 returns the same code the
operator saw printed at startup (`internal/cli/serve.go:106`). An attacker does
not need to race anybody.

Each leg is demonstrated by the repository's own tests:

- `internal/webserver/serverv2_test.go:270-283` — `GET /api/pair` returns the code to a request carrying no credentials
- `internal/webserver/serverv2_test.go:387-402` — `POST /api/pair/submit` approves with only the code in the body
- `internal/webserver/e2e_test.go:97-104` — the resulting token authenticates `/`

Only the *composition* of the three is inferred; each step is proven
individually. The chain was confirmed by reading code and tests, **not** by
starting a live network-listening server.

### Fix

1. Default `Addr` to `127.0.0.1:8765`. A remote-access server should be opt-in
   (`--addr 0.0.0.0:8765`), never the default.
2. Never return `Token` to an unauthenticated caller. The operator already
   receives it out-of-band via `BootstrapPair` / `printServeBanner`. The HTTP
   response needs only the `code`.
3. Rate-limit and lock out `/api/pair/submit`.

---

## HIGH — the 6-digit pairing code has no attempt limiting

`Approve` (`internal/webserver/pairing.go:154`) applies no attempt counter,
backoff, or lockout. Even with the token leak closed, a 6-digit code falls to
roughly 10⁶ requests.

**Fix:** cap attempts per pair and per source address; expire a pair after a
small number of failures.

---

## HIGH — the WebSocket accepts any Origin, and the cookie has no SameSite

```go
CheckOrigin: func(r *http.Request) bool {
    return true // Allow all origins for development
},
```

- `internal/webserver/ws.go:21-23`
- `internal/webserver/server.go:318-320`

Authentication is a cookie set at `internal/webserver/server.go:235-241` and
`:137-143` with `HttpOnly: true` but **no `SameSite` attribute**. Any page the
user visits while `pi serve` is running can open a WebSocket to it and drive the
terminal.

**Honest caveat:** modern browsers default to `SameSite=Lax`, which blunts the
cookie leg. That mitigation was **not** tested in a real browser. The missing
`CheckOrigin` is unambiguous in the code, and the `?token=` query fallback
(`internal/webserver/server.go:350-352`) bypasses cookie policy entirely.

**Fix:** validate `Origin` against the server's own host; set
`SameSite: http.SameSiteStrictMode` on `pi_token`.

---

## MEDIUM — session transcripts are world-readable

`internal/session/store.go` creates directories `0o755` (`:117`, `:170`, `:341`)
and `events.jsonl` / `meta.json` / `branches.json` `0o644` (`:194`, `:929`,
`:978`). `events.jsonl` holds the full turn stream — prompts, tool calls, and
tool results — so any file the agent read (a `.env`, a private key) sits in a
world-readable file.

The rest of the codebase gets this right, which makes it an inconsistency rather
than a policy: `internal/logger/logger.go:68,73`, `internal/auth/auth.go:917,921`
and `internal/config/config.go:729,732` all use `0o700`/`0o600`.

**Fix:** `0o700` for directories and `0o600` for files throughout
`internal/session`.

---

## MEDIUM — RPC unix socket defaults into world-writable `/tmp`

`internal/cli/cli.go:185` defaults `--socket` to `/tmp/pi-go.sock`.
`internal/jsonrpc/rpc.go:99-102` calls `os.Remove` then `net.Listen("unix", …)`
with no umask handling and no `chmod`. The socket accepts `prompt`
(`internal/jsonrpc/rpc.go:161`) — arbitrary agent tool execution as the invoking
user.

Mitigating: reachable only when `--socket` is passed explicitly
(`internal/cli/cli.go:1020` gates on `flagSocketChanged`).

**Fix:** default under `$HOME/.pi-go/`; `chmod 0600` the socket after `Listen`.

---

## LOW — `pi upgrade` pipes a script to a shell with no integrity check

`internal/cli/upgrade.go:160`:

```go
exec.Command("sh", "-c", fmt.Sprintf("curl -fsSL %s | bash", scriptURL))
```

The Windows path (`internal/cli/upgrade.go:188`) does `http.Get` then
`powershell -ExecutionPolicy Bypass -File`.

`scriptURL` is a compile-time constant, so **there is no injection**. The gap is
that neither path verifies a checksum or signature — despite the repo carrying
`internal/attest` and depending on `sigstore-go`. Not verified: what the hosted
script does, or whether the release pipeline signs it.

---

## LOW — `--header` values land in child process argv

`internal/webserver/pty.go:233-235` appends `--header <value>` to the spawned
`pi` argv. An operator passing an `Authorization` header to `pi serve` would
expose it to any local user via `ps`. Not confirmed that anyone uses it this way.

---

## Categories that came back clean

Recorded because a clean result is worth having in writing.

- **Command injection.** 50 `exec.Command`/`CommandContext` sites across
  `internal/` and `cmd/`, all argv-form; no interpolation of model or user input
  into a shell string. Git plumbing places `--` before filenames
  (`internal/tools/git_diff.go:59`, `internal/tools/git_hunk.go:63`). The only
  `sh -c` is the constant-URL upgrade path above. `internal/tools/shell.go:73`
  runs model-authored scripts through `bash -c` **by design** — that is the bash
  tool, the product, not a defect.
- **Path traversal / the `os.Root` sandbox.** Consistently applied, no bypass
  found. Every accessor in `internal/tools/sandbox.go` routes through
  `resolveToRoot` (`:260`); `Resolve` (`:206`) and `resolveWorktreePath` (`:235`)
  reject `..` after `filepath.Clean`, and `os.Root` blocks symlink escape.
  `AddExtraDir` (`:103`) widens the boundary but has one caller
  (`internal/piagent/build.go:85`).
- **Credential leakage.** `internal/httplog/httplog.go:218-266` redacts
  `authorization`, `proxy-authorization`, `x-api-key`, `api-key`,
  `x-goog-api-key`, `cookie`, `set-cookie`, `chatgpt-account-id` and
  `openai-organization`, operating on a copy. Auth and login logs record only
  `key_present` and lengths. No path writes a key to a log, session file, error
  string, or OTel attribute. (The session-file *permissions* issue above is about
  what the agent read, not about keys.)
- **TLS.** Three `InsecureSkipVerify` sites, each behind an explicit user flag:
  `internal/provider/provider.go:95`, `internal/provider/list_models.go:36-44`,
  `internal/cli/ping.go:424`.
- **`pirpc`.** Stdio NDJSON only — no network listener, nothing to
  authenticate.
- **`unsafe`.** One use in the tree, a `TIOCGWINSZ` ioctl in
  `internal/mermaid/diagram/terminal.go:9,25`.
- **Deserialization.** No `gob`, no `xml`; one `yaml.Unmarshal` into a typed
  struct (`internal/palace/miner.go:248`).
- **Dependency CVEs.** `govulncheck` reports 8 advisories, all in
  `github.com/ollama/ollama@v0.32.15`, all `Fixed in: N/A` — and **none is
  reachable**. pi-go links only `api`, `auth`, `envconfig`, `format`,
  `internal/orderedmap`, `types/model` and `version`; every advisory describes
  server-side code. The advisories carry no `ecosystem_specific` symbol scoping,
  which is why govulncheck matches the whole module and emits an identical trace
  block for all eight. They will recur in every scan, so they deserve an explicit
  allowlist note in `check-cve` rather than re-triage.

## Scanner coverage

| Tool | Status |
|---|---|
| `govulncheck ./...` | ran — 8 advisories, 0 reachable |
| `go vet ./...` | ran — clean |
| `golangci-lint run ./...` | ran — 0 issues |
| `staticcheck` standalone | **unavailable** — toolchain version mismatch; golangci-lint's bundled copy did run |
| `gosec` | **not run** — not installed, and also absent from `.golangci.yml`, so this class is uncovered by CI too |
| `make check-cve` | **not run** — its first line is `go mod tidy -v`, which mutates tracked files |

## Fix order

1. The RCE chain and the Origin check — a remote shell on the LAN today.
2. Pairing-code attempt limiting.
3. Session file modes.
4. The rest.
