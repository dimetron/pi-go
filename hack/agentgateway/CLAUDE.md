# CLAUDE.md — hack/agentgateway

Guidance for coding agents changing the agentgateway deployment in this
directory. `README.md` is the reference for *what* is here (layout, naming
schemes, catalogs, client wiring); this file is the rules for *how* to change
it safely, and the failures worth recognizing on sight.

The repo-root `CLAUDE.md` still applies — worktrees, signed commits, no
`--no-verify`.

## The pieces

| File | Role |
|---|---|
| `config.yaml` | The gateway config. Machine-managed — see below. |
| `.env` / `.env.example` | Provider keys and the gateway's own API key. `.env` is gitignored. |
| `docker-compose.yaml` | The normal way to run it, plus Postgres for the request log. |
| `run.sh` | Runs a local `agentgateway` binary against `config.yaml` instead. |
| `base-costs.json` | Generated cost catalog, refreshed from models.dev by the UI. |
| `pi-aliases.json` | Hand-maintained catalog, layered after `base-costs.json`. |

Endpoint `http://localhost:4000`, UI `http://localhost:4000/ui`.

## Rule 1: config.yaml is machine-managed — comments do not survive

`storage.mode` is `file` and the compose mount is deliberately writable, so the
gateway **rewrites `config.yaml` and strips every comment** the first time
anyone saves from the UI.

**Do not explain a decision in a `config.yaml` comment.** It will vanish, and
its disappearance is silent. Put the explanation in this file or in `README.md`
and keep the config itself to the header line.

The same mechanism makes `config.yaml` a secret once the UI has been used:
anything created there, including `llm.policies.apiKey` entries, is written back
in plaintext. Switch `AGENTGATEWAY_STORAGE_MODE` to `hybrid` if you want an
annotated hand-authored config and UI edits both.

## Rule 2: the whole file is shell-expanded, comments included

A bare `$NAME` for an unset variable aborts startup with
`error looking key 'NAME' up` — and it does that even inside a comment. Every
reference uses `${NAME:-}` so an unset key degrades to a 401 on that one route
rather than taking the whole gateway down.

`AGENTGATEWAY_API_KEY` is the exception: `docker-compose.yaml` uses `:?` so
compose refuses to start with a readable message. An empty value there would
register an empty key that `mode: strict` then happily accepts.

## Rule 3: adding a provider is two edits, plus two more to make it reachable

1. `llm.providers` in `config.yaml` — the credential and endpoint.
2. `llm.models` in `config.yaml` — the route. Prefixed (`foo/*` with a
   `stripPrefix` transformation) and/or bare-glob (`foo-*`).
3. `docker-compose.yaml` `environment:` — **a variable the config reads but
   compose does not pass through does not exist in the container.** This is the
   easiest step to forget; it looks like a bad key rather than a missing one.
4. `.env.example` — so the next person knows the variable exists.

## Gemini must go through the OpenAI-compatible endpoint

`provider: gemini` is **wrong for an AI Studio API key** and is the one preset
here that does not do what its name suggests. It sends the credential as
`Authorization: Bearer <key>` to `generativelanguage.googleapis.com`, which
accepts that header only for an OAuth2 access token. Google answers:

```
401 {"type":"authentication_error",
     "message":"Request had invalid authentication credentials. Expected OAuth 2
     access token, login cookie or other valid authentication credential...",
     "code":"UNAUTHENTICATED"}
```

The key itself is fine — the native API just wants it in `x-goog-api-key`.
Google's OpenAI-compatible surface *does* accept a Bearer API key, so the
provider is wired to that:

```yaml
- name: gemini
  provider: openai
  params:
    baseUrl: ${GEMINI_BASE_URL:-https://generativelanguage.googleapis.com/v1beta/openai}
    apiKey: ${GEMINI_API_KEY:-}
```

Do not "fix" this back to `provider: gemini`. If a future agentgateway release
sends `x-goog-api-key`, verify with the probes below before switching.

Vertex AI is a different story — it genuinely wants an OAuth2 token — and is not
configured here.

## Diagnosing a 401: three probes, in order

A 401 from `localhost:4000` can come from the gateway or from the vendor, and
they look alike in a client. Separate them:

**1. Is it the gateway's own auth?** A gateway rejection says so in plain text
and never mentions the vendor:

```
api key authentication failure: no API Key found
```

Only `Authorization: Bearer` is accepted — `x-api-key` and `api-key` are not.

**2. Does the key work at all, outside the gateway?**

```bash
set -a; . ./.env; set +a
curl -s "https://generativelanguage.googleapis.com/v1beta/models?pageSize=1" \
  -H "x-goog-api-key: $GEMINI_API_KEY"
```

A 200 here with a 401 through the gateway means the gateway is presenting the
key wrongly, not that the key is bad.

**3. Which upstream, and what status?** The request log names both. Note that
`config.yaml` adds `llm.prompt`/`llm.completion` to the log fields, so every
line carries a full prompt and the log is enormous — never `docker logs` it
unfiltered:

```bash
docker logs agentgateway --since 5m 2>&1 | grep -o 'endpoint=[^ ]*' | sort | uniq -c
docker logs agentgateway --since 5m 2>&1 | grep generativelanguage | tail -1 | cut -c1-800
```

Google's own errors distinguish the cases precisely, which makes them worth
reading rather than skimming:

| Google says | Means |
|---|---|
| `Expected OAuth 2 access token, login cookie...` (401) | An `Authorization` header was sent where an API key belongs |
| `Method doesn't allow unregistered callers` (403) | No credential reached the API at all — an empty `${VAR:-}` |
| `API key ... used with other authentication credentials` (401) | Both `x-goog-api-key` and `Authorization` were sent |
| `API key not valid` (400) | The key really is wrong |

Two more environment traps that read as auth failures but are not: the container
is distroless, so `docker exec agentgateway env` fails with
`"env": executable file not found` — use
`docker inspect agentgateway --format '{{range .Config.Env}}{{println .}}{{end}}'`
instead. And a key edited in `.env` after `docker compose up` is **not** in the
running container; compose reads the env file at create time.

## Verify a change

Validate first — it parses the config without binding ports:

```bash
docker run --rm -v "$PWD:/etc/agentgateway:ro" -w /etc/agentgateway \
  ghcr.io/agentgateway/agentgateway:v1.5.0 --validate-only -f config.yaml
```

Then restart and exercise the route end to end. Editing `config.yaml` alone is
not enough when the change touches `docker-compose.yaml`'s `environment:` —
`docker compose up -d` recreates the container, `docker restart` does not pick
up new variables.

```bash
docker compose up -d          # after an environment: change
docker restart agentgateway   # config.yaml-only change

set -a; . ./.env; set +a
curl -s http://localhost:4000/v1/chat/completions \
  -H "Authorization: Bearer $AGENTGATEWAY_API_KEY" -H 'content-type: application/json' \
  -d '{"model":"gemini-2.5-flash","max_tokens":16,
       "messages":[{"role":"user","content":"say ok"}]}'
```

Check **both** naming schemes when you touch a provider — the prefixed route and
the bare glob resolve through different `llm.models` entries and can break
independently:

```bash
for M in gemini-2.5-flash gemini/gemini-2.5-flash; do ... done
```

A vendor-level error (`no credits remaining`, an unknown model) still proves
routing worked: the request reached the vendor. A gateway error names the model
or the route instead.

## Do not commit

- `.env` — real keys (gitignored, keep it that way).
- `config.yaml` **if the UI has written keys into it** — check
  `llm.policies.apiKey` before staging.
- Recordings of the UI. The repo-root rule applies: GIFs and screen captures go
  on a release, and `.githooks/check-large-files` enforces a 1 MiB cap.
