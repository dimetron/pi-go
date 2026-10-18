# MCP servers in the pi-go kagent harness

How a pi-go harness pod on Agent Substrate gets external MCP tools, worked
through end to end with the **remote GitHub MCP server** as the example.

The short version: MCP is not a harness feature. The `pi a2a` runtime loads the
same `.pi-go/config.json` / `.pi-go/mcp.json` an interactive `pi` does, so an
MCP server reaches a harness pod exactly the way it reaches your terminal —
through configuration. Everything else is about *where that configuration
lives* and *what it can authenticate to* inside the sandbox.

## The code path

| Step | Where |
|---|---|
| `pi a2a` starts, no `CWD` set | `internal/cli/a2a_server.go:88` → `ServeConfig{}` |
| Prompt turn arrives, `CWD` empty → process cwd | `internal/a2a/server/server.go:291` → `internal/acp/server/runtime.go:156` |
| Config load for that cwd | `loadSessionConfig` → `config.LoadFrom(cwd)` (`internal/config/config.go:390`) |
| MCP servers merged from `config.json` + `mcp.json` | `mergeStandaloneMCPServers`, `LoadMCPServersFrom` (`internal/config/config.go:402`) |
| `${VAR}` expansion in URLs and headers | `substituteEnvVarsFrom` (`internal/config/config.go:551`) — `.pi-go/.env`, then real env |
| Servers → ADK toolsets | `buildMCPToolsetsFromCfg` (`internal/acp/server/runtime.go:619`) → `extension.BuildMCPToolsets` (`internal/extension/mcp.go:75`) |
| Transport selection | `newMCPToolset` (`internal/extension/mcp.go:545`): `url` → Streamable HTTP, `command` → stdio subprocess |

The same wiring serves the TUI, one-shot `-p`, ACP and A2A, which is why an MCP
config that works locally also works in the pod.

## Config shape

Two accepted locations, both loaded on every session init:

1. `~/.pi-go/config.json` — `mcp.servers`, an array of objects.
2. `.pi-go/mcp.json` — Claude Desktop format, `mcpServers` keyed by name. A
   project file **replaces** the global one; servers from `config.json` win on a
   name collision.

Inside the harness image `HOME=/data` (the Substrate durable mount), so
`~/.pi-go/config.json` resolves to **`/data/.pi-go/config.json`** — and because
`/data` is checkpointed, a server configured there survives actor replacement.

Both shapes carry the same transport fields:

| Field | Meaning |
|---|---|
| `name` | server name; also the OAuth token cache key |
| `url` | Streamable HTTP endpoint (remote servers) |
| `headers` | custom HTTP headers for the `url` transport |
| `command` + `args` | stdio subprocess (local servers) |
| `oauth` | run the OAuth authorization-code flow on first connect |

## The GitHub example

GitHub's remote MCP server is a single endpoint — `https://api.githubcopilot.com/mcp/` —
speaking Streamable HTTP, authenticated with `Authorization: Bearer <token>`.
Behavioural toggles ride in headers, not in the URL: `X-MCP-Toolsets`,
`X-MCP-Tools`, `X-MCP-Readonly`, `X-MCP-Lockdown`, `X-MCP-Insiders`.

`/data/.pi-go/config.json` in the pod:

```json
{
  "mcp": {
    "servers": [
      {
        "name": "github",
        "url": "https://api.githubcopilot.com/mcp/",
        "headers": {
          "Authorization": "Bearer ${GITHUB_MCP_TOKEN}",
          "X-MCP-Toolsets": "repos,pull_requests,issues",
          "X-MCP-Readonly": "true"
        }
      }
    ]
  }
}
```

Or `.pi-go/mcp.json` (Claude Desktop shape, same fields):

```json
{
  "mcpServers": {
    "github": {
      "url": "https://api.githubcopilot.com/mcp/",
      "headers": {
        "Authorization": "Bearer ${GITHUB_MCP_TOKEN}",
        "X-MCP-Toolsets": "repos,pull_requests,issues",
        "X-MCP-Readonly": "true"
      }
    }
  }
}
```

Three details that matter in a pod:

- **Use `${VAR}` with `headers`, not `requestInit`.** The VS Code / Copilot
  docs show `"requestInit": {"headers": {...}}`; pi-go reads a top-level
  `headers` map and has no `requestInit` field. A copied VS Code snippet parses
  as a server with nil headers — verified: such a config loads with
  `headers=map[string]string(nil)` — and then fails against GitHub with
  `missing required Authorization header`. It fails closed, but it fails.
- **Prefer an explicit `Authorization` header over `oauth: true`.** A token
  header is the only auth that works headless. `oauth: true` installs an
  authorization-code handler whose browser step is refused when no user is
  present (`mcpOAuthCodeFetcher`, `internal/extension/mcp.go:761`) — the server
  is then reported and skipped. A token cached from an earlier interactive run
  *is* reused headless, but you cannot produce one from inside the pod.
- **Where the token comes from.** `substituteEnvVarsFrom` reads `.pi-go/.env`
  under the nearest project dir, then the real environment. In the pod the
  process cwd is `/data`, so `/data/.pi-go/.env` works; a Kubernetes Secret
  mounted as an env var works with no file at all.

## Verification

Run against the live endpoint with a real token (`gh auth token`, used as
`GH_TOK`), through pi-go's own client — not curl:

```
toolsets built: 1
Tools() in 899ms, err=<nil>
toolset "github" exposes 16 tools
  - get_commit
  - get_file_contents
  - get_latest_release
  - get_release_by_tag
  - get_tag
  - list_branches
  … and 10 more
status: github connected=true failed=false tools=16 (connected)
```

16 tools for `X-MCP-Toolsets: repos,pull_requests` with `X-MCP-Readonly: true`.
Unset `X-MCP-Readonly` for write tools; an unknown toolset name is silently
ignored by GitHub, so a typo shows up as a missing tool rather than an error.

Endpoint behaviour, unauthenticated and authenticated:

```
POST /mcp/ with no Authorization         → HTTP 401  "missing required Authorization header"
POST /mcp/ with "Bearer not-a-real-token" → HTTP 400  "Authorization header is badly formatted"
POST /mcp/ initialize with a real token   → HTTP 200  event: message {jsonrpc,result:{capabilities…}}
```

## Deploying the config to the pod

`specs/kagent/manifests.yaml.tmpl` has an env block on the `Harness` CR
(`render-manifests.sh` fills it from `--model` / `--base-url`), but **no**
`/data/.pi-go` config yet. Three ways to land the MCP config, least invasive
first:

1. **Env var + a file in the durable dir.** Mount `/data/.pi-go/config.json`
   from a ConfigMap and set `GITHUB_MCP_TOKEN` from a Secret:

   ```yaml
   spec:
     workload:
       image: ${PI_GO_IMAGE}
     env:
       - name: GITHUB_MCP_TOKEN
         valueFrom:
           secretKeyRef: {name: github-mcp, key: token}
   ```

   `config.json` itself goes in `/data/.pi-go/`, which a Substrate snapshot
   carries across actor replacement — the same reason sessions live there.

2. **`/data/.pi-go/.env` for the token**, config in `config.json` unchanged.
   Keeps the token out of the CR and out of git; still needs the file in the
   durable dir.

3. **Bake a default config into the image.** Only if every pod should share one;
   a token cannot be baked, so this covers URL/toolsets/readonly only and the
   `Authorization` header must still come from env.

Whichever path you take, the pod needs egress to `api.githubcopilot.com:443` —
the Substrate sandbox blocks `apt-get` at runtime, and the same network policy
governs the MCP call. If MCP tools vanish with no error in the transcript, check
the session log before the config: `BuildMCPToolsets` reports a failed server
through `notice.Notifyf` and skips it rather than failing the run.

## Notes

- **Do not rely on `gh` for MCP.** The image ships `gh` (and `gcx`), but the
  GitHub MCP server is a separate HTTP service; `gh` contributes a token, not a
  transport.
- **Tool count is context, and it is not free.** 16 tools from `repos` alone.
  `X-MCP-Toolsets` is the lever; the GitHub server's tool schemas change without
  a versioning contract, so a prompt built around specific tool names can drift.
- **One server, one connection.** Each configured server is dialed on first
  `Tools()` with a 30s budget (`mcpConnectTimeout`); a hung server is closed and
  skipped for that session.
