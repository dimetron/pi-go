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
  provider:
    custom:
      providerOverride: gcp.gemini
      formats:
      - type: completions
  params:
    baseUrl: ${GEMINI_BASE_URL:-https://generativelanguage.googleapis.com/v1beta/openai}
    apiKey: ${GEMINI_API_KEY:-}
```

**`providerOverride: gcp.gemini` is load-bearing, not decoration.** A plain
`provider: openai` with the same `baseUrl` authenticates identically and returns
200s — but it reports `gen_ai.provider.name=openai`, so the cost catalog looks
the model up under `openai`, finds no `gemini-*` there, and every Gemini request
logs with **no `agw.ai.usage.cost.total` at all**. Nothing errors; the cost
column in the UI is simply blank. The override keeps the provider identity that
`base-costs.json` keys on (`providers."gcp.gemini"`) while the request still
goes out in OpenAI wire format with Bearer auth.

Whenever you retarget a provider's `baseUrl`, check a log line for
`agw.ai.usage.cost.total` — silent loss of cost attribution is the failure mode
this config invites.

Do not "fix" this back to `provider: gemini` — **including on the strength of
the upstream docs**, which prescribe exactly that:

```yaml
# https://agentgateway.dev/docs/standalone/latest/llm/providers/gemini/
llm:
  models:
  - name: "*"
    provider: gemini
    params:
      apiKey: "$GEMINI_API_KEY"
```

That is the config this directory started from, and it 401s on v1.5.0 —
the latest release as of 2026-09-03, so there is no version to upgrade into.
The provider reference elsewhere notes `auth.gcp uses Application Default
Credentials`, which fits what the wire shows: the preset is built around a GCP
OAuth token, and hands an AI Studio API key to the same code path. If a later
release sends `x-goog-api-key`, re-run the probes below before switching back.

Vertex AI is a different story — it genuinely wants an OAuth2 token — and is not
configured here.

### Gemini 3.x tool calls need their thought_signature echoed back

A one-shot Gemini 3 request works; the *second* turn of a tool-using
conversation fails with a 400 that has nothing to do with the gateway:

```
Function call is missing a thought_signature in functionCall parts. This is
required for tools to work correctly... position 2.
https://ai.google.dev/gemini-api/docs/thought-signatures
```

Gemini 3 returns an opaque signature on each tool call, inside a non-standard
field an OpenAI-shaped client will happily drop:

```json
"tool_calls": [{"id": "...", "type": "function", "function": {...},
                "extra_content": {"google": {"thought_signature": "Ep4BCpsB..."}}}]
```

It must come back verbatim on the assistant message that replays that tool call.
Measured, same conversation both ways:

| Model | Signature returned | Replay without it | Replay with it |
|---|---|---|---|
| `gemini-3.8-flash` | yes | **400** | 200 |
| `gemini-2.5-flash` | no | 200 | 200 |

This is a **client** obligation, not something the gateway can paper over — it
forwards what it is given, and it was verified to forward `extra_content` in
both directions. A client that strips unknown fields from `tool_calls` cannot
hold a multi-turn tool conversation with any Gemini 3 model; the 2.5 line is
unaffected and is the fallback while a client is being fixed.

pi-go itself carries the signature through
`internal/provider/openai_thought_signature.go`, parking it on
`genai.Part.ThoughtSignature` between the response and the next request. If a
Gemini 3 tool loop starts 400ing again, look there before looking at this
config.

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
| `missing a thought_signature` (400) | Not auth at all — see the Gemini 3.x section above |

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
