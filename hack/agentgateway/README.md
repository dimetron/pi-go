# agentgateway for pi-go

One [agentgateway](https://agentgateway.dev/docs/standalone/latest) in front of
every provider pi-go can talk to, and in front of the coding agents that speak a
native provider API — Claude Code, Codex, and Antigravity.

Why bother, when each of these can call its vendor directly: one place that
holds the keys, one request log across every agent and model, and one file to
edit when a model is retired or a provider needs failover.

## Layout

| File | What it is |
|---|---|
| `config.yaml` | **The config.** All pi-go providers under `<provider>/` prefixes, plus unprefixed routes for the coding agents. |
| `base-costs.json` | Cost data overlay. |
| `.env.example` | Every key the config reads. Copy to `.env`. |
| `docker-compose.yaml` | Runs `config.yaml` plus Postgres for the request log. |
| `run.sh` | Runs `config.yaml` from a local binary. |
| `migrate-sessions.py` | Replays pre-gateway pi-go session history into the request log. |
| `ollama-cloud.yaml`, `ollama-cloud-k8s.yaml`, `docker-compose.ollama-cloud.yaml` | The upstream 3-instance Ollama Cloud example, kept as-is. `config.yaml` now absorbs it — same three keys, plus the `ollama-deepseek` virtual models. |

Requires **agentgateway ≥ 1.5.0** (`modelCatalog` and the provider presets do
not exist in earlier releases).

## Start it

```bash
cp .env.example .env && $EDITOR .env
docker compose up -d
```

To reuse the keys pi-go already has instead of maintaining a second copy:

```bash
docker compose --env-file ../../.pi-go/.env up -d
```

Or from a local binary — `run.sh` finds it on `PATH`, at
`tmp/agentgateway/target/release/agentgateway`, or wherever `AGENTGATEWAY_BIN`
points:

```bash
set -a; . ../../.pi-go/.env; set +a
./run.sh
```

Either way: LLM endpoint on `http://localhost:4000`, UI on
`http://localhost:4000/ui`.

`AGENTGATEWAY_API_KEY` is the key *clients* send to the gateway, distinct from
every provider key above, which the gateway holds and spends upstream. The
config sets it under `llm.policies.apiKey` with `mode: strict`, so a request
without it is rejected; see [Client authentication](#client-authentication).

```bash
curl -s http://localhost:4000/v1/models -H "Authorization: Bearer $AGENTGATEWAY_API_KEY" | jq -r '.data[].id'
```

## Two naming schemes, on purpose

**Prefixed** — one per pi-go provider. The prefix is stripped before
forwarding, so the upstream sees its own model id. This is what pi-go's
`agentgateway` provider talks to.

| Send | Goes to | Key |
|---|---|---|
| `anthropic/<model>` | Anthropic | `ANTHROPIC_API_KEY` |
| `openai/<model>` | OpenAI | `OPENAI_API_KEY` |
| `gemini/<model>` | Gemini | `GEMINI_API_KEY` |
| `mistral/<model>` | Mistral | `MISTRAL_API_KEY` |
| `openrouter/<model>` | OpenRouter | `OPENROUTER_API_KEY` |
| `xai/<model>` | xAI | `XAI_API_KEY` |
| `ollama/<model>` | local Ollama | — |
| `ollama1/<model>`, `ollama2/`, `ollama3/` | Ollama Cloud, one account each | `OLLAMA_API_KEY_1/2/3` |
| `ollama-cloud/<model>` | Ollama Cloud account 1 | `OLLAMA_API_KEY_1` |
| `opencode/<model>` | OpenCode Zen | `OPENCODE_API_KEY` |
| `azure/<model>` | Azure OpenAI | commented out — see below |

`openrouter/` strips only its own leading segment, so
`openrouter/anthropic/claude-opus-5` reaches OpenRouter as
`anthropic/claude-opus-5`.

**Unprefixed** — `claude-*`, `gpt-*`, `o3*`, `gemini-*`, `grok-*`. A coding
agent sends its vendor's own model id and has nowhere to put a prefix, so these
pass the id through untouched.

**Virtual** — named entry points, so a client never has to name an instance or
a specific model. Repoint them here rather than reconfiguring every caller.

| Name | Routing |
|---|---|
| `ollama-deepseek-balanced` | DeepSeek weighted evenly across all three Ollama Cloud accounts |
| `ollama-deepseek` | DeepSeek on account 1, failing over to 2 then 3 |
| `ollama-gemma4`, `ollama-glm-flash`, `ollama-minimax` | Failover variants of those models across the three accounts, same pattern |
| `pi-default` | Claude Opus, failing over to GPT then Gemini |
| `pi-fast` | Ollama Cloud DeepSeek (account 1), failing over to `gpt-5.6-luna` |

### DeepSeek across three Ollama Cloud accounts

Three accounts, one key each, following the upstream `llm-ollama-cloud`
example. Address one directly when you care which account pays:

```bash
curl -s http://localhost:4000/v1/chat/completions -H 'content-type: application/json' \
  -H "Authorization: Bearer $AGENTGATEWAY_API_KEY" \
  -d '{"model":"ollama2/deepseek-v4-flash:0731-cloud",
       "messages":[{"role":"user","content":"say ok"}]}'
```

Or let the gateway choose — `ollama-deepseek-balanced` spreads load and rate
limits across all three, `ollama-deepseek` prefers account 1 and falls
through only when one is degraded:

```bash
curl -s http://localhost:4000/v1/chat/completions -H 'content-type: application/json' \
  -H "Authorization: Bearer $AGENTGATEWAY_API_KEY" \
  -d '{"model":"ollama-deepseek","messages":[{"role":"user","content":"say ok"}]}'
```

Set `OLLAMA_API_KEY_1`, `_2` and `_3` from
<https://ollama.com/settings/keys>. Unset keys are not fatal — that account
just 401s, and the failover variant routes around it.

### Azure is opt-in

Azure cannot ship enabled: the gateway refuses to start unless
`azureResourceName` is set, and there is no honest placeholder. It is the one
provider not in `config.yaml`. To add it, set `AZURE_OPENAI_RESOURCE_NAME` and
append:

```yaml
# under llm.providers
- name: azure
  provider: azure
  params:
    apiKey: ${AZURE_OPENAI_API_KEY:-}
    azureResourceName: ${AZURE_OPENAI_RESOURCE_NAME:-}
    azureApiVersion: ${AZURE_OPENAI_API_VERSION:-2024-10-21}

# under llm.models
- name: azure/*
  provider:
    reference: azure
  transformation:
    model: llmRequest.model.stripPrefix("azure/")
```

## config.yaml is machine-managed

The compose mount for `config.yaml` is deliberately **not** `:ro`, and
`storage.mode` defaults to `file`. Together that means the UI can save changes —
and that the gateway **rewrites the file and strips every comment** when it
does. That is why the config carries only a header and this README carries the
explanation.

| `AGENTGATEWAY_STORAGE_MODE` | UI saves go to | `config.yaml` |
|---|---|---|
| `file` (default) | `config.yaml` | rewritten, comments stripped |
| `hybrid` | the database, as an overlay on this file | left alone |
| `readOnly` | nowhere — UI edits refused | left alone; restore `:ro` too |

Pick `hybrid` if you want to keep an annotated, hand-authored config under
version control and still edit through the UI.

The container runs as uid 65532. On macOS (Docker Desktop, OrbStack) the bind
mount maps ownership and writes just work. On Linux the file must be writable by
that uid — `chmod 666 config.yaml` — or every UI save fails.

**Anything you create in the UI lands in this file**, including API keys under
`llm.policies.apiKey`. Those are real credentials in plaintext. Treat
`config.yaml` as a secret once you have used the UI: do not commit it, or switch
to `hybrid` so credentials live in the database instead.

## Database

`config.database.url` backs the request log, usage history, and the UI's request
views. Compose runs Postgres for it:

```bash
docker compose up -d          # starts agentgateway + postgres
docker compose exec postgres psql -U agentgateway -d agentgateway -c '\dt'
```

Tables: `request_logs` (one row per request — model, provider, status, tokens,
duration), `request_log_payloads`, `budget_usage`.

```sql
select gen_ai_provider_name, gen_ai_request_model, http_status,
       input_tokens, output_tokens
from request_logs order by completed_at desc limit 20;
```

The gateway reaches Postgres as the `postgres` service; compose overrides
`AGENTGATEWAY_DATABASE_URL` for that. The config default points at `localhost`,
which is what `./run.sh` needs. **Any URL that is not `postgres://` or
`postgresql://` is treated as a SQLite file**, so
`AGENTGATEWAY_DATABASE_URL=./agentgateway.db ./run.sh` needs no container at
all.

Postgres is not published to the host by default; uncomment the `ports` block in
`docker-compose.yaml` to reach it with `psql` directly. The `postgres-data`
volume persists across `docker compose down`, so request history survives
restarts — use `down -v` to discard it.

### Cost tracking

`request_logs.cost` is populated once the catalog knows the model:

```
 gen_ai_request_model | http_status | input_tokens | output_tokens |  cost
 claude-haiku-4-5     |         200 |            8 |             8 | 4.8e-05
```

If cost is NULL, the model is not in the catalog — refresh it (below).

## Wiring the clients

### Client authentication

The gateway authenticates clients itself — set in `config.yaml` under
`llm.policies.apiKey` as `mode: strict` with the key list. A request that
sends `Authorization: Bearer $AGENTGATEWAY_API_KEY` is accepted; anything else
gets 401 before it reaches a provider. The provider keys stay server-side:
the gateway swaps in the right upstream key per route.

Two things follow:

- **Clients never hold a real provider key.** The variable every client points
  at — pi-go's `AGENTGATEWAY_API_KEY`, Claude Code's `ANTHROPIC_AUTH_TOKEN`,
  Codex's `env_key` — can be any non-empty value only because the config was
  built with the gateway key in it. That value *is* the shared client secret.
- **The key lives in `config.yaml` in plaintext** and, with `storage.mode=file`,
  any key created in the UI lands there too — see
  [config.yaml is machine-managed](#configyaml-is-machine-managed).

Export it once and every client below reuses it:

```bash
export AGENTGATEWAY_API_KEY=agw_sk_...   # the value from llm.policies.apiKey
```

### pi-go

```bash
export AGENTGATEWAY_BASE_URL=http://localhost:4000
export AGENTGATEWAY_API_KEY=agw_sk_...   # must match llm.policies.apiKey
pi --model 'agentgateway/anthropic/claude-opus-5'
```

### Claude Code — Anthropic Messages, `/v1/messages`

```bash
export ANTHROPIC_BASE_URL=http://localhost:4000
export ANTHROPIC_AUTH_TOKEN=agw_sk_...    # must be non-empty AND match the gateway's client key
claude
```

Claude Code's model ids (`claude-opus-5`, …) match the `claude-*` route, and its
token counting hits `/v1/messages/count_tokens`, which the gateway also serves.
To send it somewhere other than Anthropic, set `ANTHROPIC_MODEL` to any route
the gateway exposes — `ANTHROPIC_MODEL=openrouter/moonshotai/kimi-k2` reaches
OpenRouter over the Anthropic wire format, because the gateway translates
between formats.

### Codex — OpenAI Responses, `/v1/responses`

In `~/.codex/config.toml`:

```toml
model_provider = "agentgateway"
model = "gpt-5.6"

[model_providers.agentgateway]
name = "agentgateway"
base_url = "http://localhost:4000/v1"
env_key = "AGENTGATEWAY_API_KEY"
wire_api = "responses"
```

`wire_api = "responses"` is the only value Codex supports, and `/v1/responses`
is one of the gateway's routes. `env_key` must name a variable set to the
gateway's client key (`AGENTGATEWAY_API_KEY` above) — mode is `strict`, so the
gateway rejects empty or wrong values.

### Antigravity — no supported hook, two surfaces ready

**Antigravity does not officially expose a custom base URL or BYOK setting for
its agent model.** Settings like `antigravity.openai-compatible.endpoint` circulate
in [community threads](https://discuss.ai.google.dev/t/how-to-properly-configure-custom-openai-compatible-models-in-antigravity-ide/168654)
but are not documented by Google, and reports of them working are mixed and
build-dependent. Nothing here can change that.

What this config does is make the gateway ready for whichever surface a given
build exposes:

- **Gemini native** — `:generateContent`, `:streamGenerateContent`,
  `:countTokens`, served against the `gemini-*` route.
- **OpenAI-compatible** — `/v1/models` and `/v1/chat/completions` at
  `http://localhost:4000/v1`, which is the shape an "Ollama" or
  "OpenAI-compatible provider" field expects.

If your build offers an OpenAI-compatible or Ollama endpoint field, point it at
`http://localhost:4000/v1` with the gateway's client key (`AGENTGATEWAY_API_KEY`
above). If it does not, the honest
answer today is a community patch such as
[antigravity-add-model](https://github.com/vahapogut/antigravity-add-model),
which injects a proxy into the Electron app — out of scope for this directory.

## Cost catalogs: base-costs.json and pi-aliases.json

Two `config.modelCatalog` sources, merged in order — later wins.

| File | Mount | Contents |
|---|---|---|
| `base-costs.json` | **read-write** | The full models.dev catalog, ~930 models across 19 providers. Machine-managed. |
| `pi-aliases.json` | read-only | 62 hand-maintained entries: pi-go's dotted aliases (`claude-sonnet-4.5`), older OpenAI ids (`o1-mini`, `gpt-5.1-codex`), and Ollama/OpenRouter prices the base catalog lacks. |

**`base-costs.json` is mounted writable on purpose.** The UI's refresh action —
`POST /api/costs/refresh-base` — fetches models.dev, **overwrites this file**,
and hot-reloads it without a restart. A read-only mount makes it fail.

```bash
curl -X POST http://localhost:4000/api/costs/refresh-base \
  -H "Authorization: Bearer $AGENTGATEWAY_API_KEY"
# {"providers":19,"models":930}
```

Because a refresh overwrites the whole file, **never hand-edit
`base-costs.json`** — your edits are lost on the next refresh. That is exactly
why the pi-go aliases live in a separate, read-only `pi-aliases.json` layered
after it. Startup should report the merged total (base + aliases, overlapping
ids counted once):

```
info llm::catalog  model catalog loaded  providers=20 models=977
```

### A configured catalog replaces the built-in one on v1.5.0

The binary embeds a models.dev catalog, and newer builds merge it *under* your
configured sources. **v1.5.0 does not** — configuring `modelCatalog` replaces it
outright. So a small hand-written overlay silently removes cost data for every
model it omits, which is why `base-costs.json` must hold the complete catalog
and must be populated by refresh rather than by hand.

Rates are USD per million tokens, as strings. Model ids match **exactly** — no
wildcards, so `"*"` matches nothing. The file takes only a `providers` key; a
`metadata` block is rejected on this version.

## Gotchas worth knowing

These each cost a debugging cycle when this config was built.

- **The whole config file is shell-expanded, comments included.** A bare
  `$NAME` for an unset variable aborts startup with
  `error looking key 'NAME' up`. Every key uses `${NAME:-}` so an unset provider
  degrades to a 401 on that one route instead of taking the gateway down — and
  no comment may contain a bare dollar-name.
- **`base-costs.json` takes only `providers`.** A `metadata` block is rejected
  (`unknown field 'metadata'`), and the failure is a warning at startup, not an
  error, so cost data silently goes missing.
- **The UI is on `:4000/ui`**, served by the gateway itself. The admin listener
  on `:15000` binds to localhost inside the container and is not the UI.
- **Gemini uses `provider: openai`, not `provider: gemini`.** The `gemini`
  preset presents the key as `Authorization: Bearer`, which
  generativelanguage.googleapis.com accepts only for OAuth2 tokens; an AI Studio
  API key gets a 401 that reads as a bad key but is not one. The provider points
  at Google's OpenAI-compatible endpoint instead. See `CLAUDE.md`.
- **`localhost` inside a container is the container.** The compose file points
  `OLLAMA_BASE_URL` at `host.docker.internal` and adds the `host-gateway` entry
  that Linux needs.

## Verify a change

`--validate-only` parses the config without binding ports:

```bash
docker run --rm -v "$PWD:/etc/agentgateway:ro" -w /etc/agentgateway \
  ghcr.io/agentgateway/agentgateway:v1.5.0 --validate-only -f config.yaml
```

Then check a route end to end — a 200 here means the prefix was stripped and the
upstream accepted the model:

```bash
curl -s http://localhost:4000/v1/messages -H 'content-type: application/json' \
  -H "Authorization: Bearer $AGENTGATEWAY_API_KEY" \
  -d '{"model":"anthropic/claude-haiku-4-5","max_tokens":16,
       "messages":[{"role":"user","content":"say ok"}]}' | jq -r '.model'
```

An upstream error (`invalid_request_error`, `no credits remaining`) still proves
the routing worked: the request reached the vendor. A gateway error looks
different — it names the model or the route.
