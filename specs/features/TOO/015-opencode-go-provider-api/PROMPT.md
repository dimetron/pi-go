# OpenCode Go Provider

## Objective
Add OpenCode Go (https://opencode.ai/go) as a new `opencode` LLM provider in pi-go. OpenCode Go
is a low-cost subscription provider serving curated open coding models over HTTPS at
`https://opencode.ai/zen/go/v1` using an `OPENCODE_API_KEY`. Each model must route to its native
endpoint family: OpenAI chat/completions, OpenAI responses, or Anthropic messages.

## Key Requirements
1. **New `opencode` provider** — selectable via `opencode/<model-id>` (e.g. `opencode/kimi-k3`).
   The `opencode/` prefix is stripped before sending; delegates receive the bare model ID.
2. **Per-model endpoint routing** — a hardcoded catalog maps each model to
   `"chat"` (OpenAI chat/completions), `"responses"` (OpenAI responses), or `"messages"`
   (Anthropic). Reuse existing `NewOpenAI` and `NewAnthropic` constructors (no duplicated
   request/streaming/tool logic).
3. **Auth** — OpenAI endpoints use `Authorization: Bearer`; Anthropic `messages` uses
   `x-api-key`. Base URL default `https://opencode.ai/zen/go/v1`, overridable via
   `OPENCODE_BASE_URL` (and config.json `baseURLs.opencode`).
4. **Config & listing** — `OPENCODE_API_KEY` in `config.APIKeys()`; `OPENCODE_BASE_URL` in
   `config.BaseURLs()`; `autoDetectProvider` recognizes `opencode/`; `pi model list opencode`
   fetches models at runtime from `/v1/models`.

## Acceptance Criteria
### Provider construction & routing
- Given provider `opencode` and model `kimi-k3`, when `NewOpenCode` is called, then it returns
  an OpenAI chat-completions-backed `model.LLM`.
- Given model `gpt-5.6-luna`, then it returns an OpenAI responses-backed `model.LLM`.
- Given model `minimax-m3`, then it returns an Anthropic messages-backed `model.LLM`.
- Given an unknown `opencode/<id>`, then `NewOpenCode` returns an error.

### Generation & auth
- Given `opencode/kimi-k3`, when `GenerateContent` runs, then a request hits
  `https://opencode.ai/zen/go/v1/chat/completions` with `Authorization: Bearer`.
- Given `opencode/minimax-m3`, then a request hits `https://opencode.ai/zen/go/v1/messages`
  with `x-api-key`.
- Given a request with tools, when streamed, then tool calls round-trip through the delegate.

### Config & listing
- Given `OPENCODE_API_KEY` set, then `config.APIKeys()["opencode"]` returns it.
- Given `OPENCODE_BASE_URL` set, then `config.BaseURLs()["opencode"]` returns it and wins over
  config.json `baseURLs.opencode`.
- Given `pi model list opencode`, then it lists models from the runtime `/models` endpoint.

## Implementation Slices
1. **Catalog + constructor** — `internal/provider/opencode.go`: `opencodeDefaultBaseURL`,
   `opencodeGoModelCatalog`, `opencodeAnthropicBaseURL`, `NewOpenCode` (routes to
   `NewOpenAI`/`NewAnthropic`). Unit tests for routing/base-URL/unknown-model.
   verify: `go test ./internal/provider/ -run OpenCode && go build ./...`
2. **Provider registry** — `internal/provider/provider.go`: `opencode/` prefix in `Resolve`,
   `NewLLM` case. Tests.
   verify: `go test ./internal/provider/ -run 'OpenCode|Resolve' && go build ./...`
3. **Config** — `internal/config/config.go`: `APIKeys()`/`BaseURLs()` opencode entries,
   `autoDetectProvider` prefix. Tests.
   verify: `go test ./internal/config/... && go build ./...`
4. **Model listing** — `internal/provider/list_models.go`: `ListModels` case,
   `listOpenCodeModels`, `fetchJSON` Bearer case, `providerDefaultBaseURL`. Tests.
   verify: `go test ./internal/provider/ -run 'ListModels|OpenCode' && go build ./...`
5. **CLI** — `internal/cli/model.go`: `allProviders` + switch case for `opencode`. Tests.
   verify: `go test ./internal/cli/ -run ModelList && go build ./...`
6. **End-to-end** — guarded E2E test (skip when `OPENCODE_API_KEY` unset) for a chat and a
   messages model. Run full gates.
   verify: `go test ./... && go vet ./... && golangci-lint run ./...`

## Gates
- **build**: `go build ./...`
- **test**: `go test ./internal/provider/... ./internal/config/... ./internal/cli/...`
- **vet**: `go vet ./...`
- **lint**: `golangci-lint run ./...`

## Reference
- Design: `specs/features/TOO/015-opencode-go-provider-api/design.md`
- Outline: `specs/features/TOO/015-opencode-go-provider-api/outline.md`
- Plan: `specs/features/TOO/015-opencode-go-provider-api/plan.md`
- Requirements: `specs/features/TOO/015-opencode-go-provider-api/requirements.md`
- Research: `specs/features/TOO/015-opencode-go-provider-api/research/`

## Constraints
- **Never modify source in the spec dir** — only the files listed above in `internal/`.
- `opencode/` prefix only (not `opencode-go/`).
- Anthropic messages endpoint needs `x-api-key`; pass base URL `https://opencode.ai/zen/go`
  (strip trailing `/v1`) to `NewAnthropic` because its SDK appends `v1/messages`.
- OpenAI-family delegates use base `https://opencode.ai/zen/go/v1` (SDK paths are
  `chat/completions` and `responses` without `/v1`).
- `NewOpenAI`'s `codexBackend` detection must stay disabled — always pass a non-empty base URL.
- `gpt-5.6-luna` is already in `modelNeedsResponses`, so `NewOpenAI` auto-routes it to responses;
  catalog `"chat"` models auto-route to chat/completions. Rely on this (matches the catalog).
- Do not add a wrapper `model.LLM` type; `NewOpenCode` returns the delegate directly.
