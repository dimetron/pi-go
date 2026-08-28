# Research: Model Routing/Validation Bug Path

## 1. config's autoDetectProvider (internal/config/config.go)

- Config's own `modelPrefixes` (config.go:219-225): `claude→anthropic, gpt→openai,
  gpt-5→openai, gemini→gemini, grok→xai`. **Does NOT include `mistral` or `magistral`.**
- `autoDetectProvider` (config.go:257-286) order: azure/ → ollama/ → opencode/ →
  openrouter/ → :cloud/-cloud suffix → modelPrefixes map → `""`.
- `DefaultProvider` default (config.go:212): `"openai"`; default role model `gpt-5.6-sol`.
- `ResolveRole` (config.go:246-251):
  ```go
  prov = rc.Provider
  if prov == "" {
      prov = autoDetectProvider(rc.Model)
      if prov == "" { prov = c.DefaultProvider }
  }
  ```
  → a role with `Model: "mistral-large-latest"` and no explicit provider resolves
  to **"openai"**, not "mistral".

## 2. provider.Resolve (internal/provider/provider.go:349-403)

Prefix check order:
1. `ollama/` → strips prefix, Ollama:true, LocalOllama:true.
2. `azure/` → strips prefix.
3. `opencode/` → strips prefix.
4. `openrouter/` → strips prefix.
5. `:cloud`/`-cloud` suffix → keeps full name, Ollama:true.
6. `modelPrefixes` map loop (provider.go:384-389) — **does NOT strip; Model kept whole**.
   Provider's map (provider.go:182-190) **includes** `mistral`/`magistral` → mistral.
7. `OllamaModelPrefixes` loop (empty).
8. else error: `unknown model %q: cannot determine provider ...`.

**Confirmed: mistral does NOT strip.** `Resolve("mistral/codestral")` →
`Info{Provider:"mistral", Model:"mistral/codestral"}`. There is no special-case
strip for `mistral/` at all.

`ResolveWithBaseURL` (provider.go:323-345):
- ollama/ or azure/ → delegate Resolve; openrouter/ → delegate Resolve.
- **openai/ → strips prefix**, `{Provider:"openai", Model:<stripped>, Custom:true}`.
- otherwise Resolve; success && !Ollama → Custom:true; fail or Ollama →
  `{Provider:"openai", Model:modelName, Custom:true}`.

`ValidateModel` consequence: `mistral/codestral` → `KnownModels["mistral"]` has no
`mistral/codestral...` prefix → `unknown mistral model "mistral/codestral"`.

## 3. Provider override over auto-detection

**interactive.go `buildSwitchedLLM`** (interactive.go:993-997):
```go
providerName := ""
if rc, ok := cfg.Roles["default"]; ok && rc.Provider != "" { providerName = rc.Provider }
```
Only the default role's explicit Provider field, not auto-detect.

**`resolveSwitchedModel`** (interactive.go:1023-1065), override at 1034-1037:
```go
if providerName != "" {
    info.Provider = providerName
    info.Custom = baseURL != ""
}
```
then baseURL lookup, then `provider.ValidateModel(info)`.

**cli.go `resolveRuntimeModelForRole`** (cli.go:296-331), same override at 316-319.

## 4. Commit path (cli.go:1749-1802, buildCommitMsgFunc)

- `cfg.ResolveRole("commit")` fallback "default" → `provider.Resolve(commitModel)` →
  `if commitProvider != "" { info.Provider = commitProvider }` (1763-1765) →
  `provider.ValidateModel(info)` (1766-1768) — **error swallowed: `return nil`**.
- Uses `provider.Resolve` (not ResolveWithBaseURL); all errors swallowed.

## 5. Tests

- **No test references a `mistral/`-prefixed model name anywhere in internal/.**
- provider_test.go:247-250: bare `mistral-large-latest`/`codestral`/`pixtral`/`ministral`
  valid; no prefix.
- mistral_test.go:65-104: bare names; `ollama/mistral:7b` → ollama.
- config_test.go: **no mistral/magistral mentions at all**. TestResolveRole_AutoDetectProvider
  covers claude/gpt/azure/gemini/minimax/glm/opencode/openrouter — no mistral.
- The config/provider prefix-map divergence is **not covered by any test**.

## 6. Error strings / tests

- Emitters: provider.go:315 (ValidateModel), provider.go:402 (Resolve),
  opencode.go:68 (NewOpenCode-time).
- opencode_test.go:84-90 asserts exact "unknown OpenCode Go model".
- interactive_deferred_test.go:255-261 asserts wrapper prefixes "resolving model"/"model validation".
- **No test asserts exact `unknown mistral model %q` / `unknown openai model` strings.**

## Key synthesis

The two packages have **divergent prefix tables**: config lacks mistral,
provider has it. So:
- A role model `mistral-large-latest` resolves provider "openai" at config layer
  (DefaultProvider fallback), and the override forces `info.Provider` after
  ResolveWithBaseURL.
- A `mistral/...`-prefixed name routes to mistral with the prefix **kept** in
  `info.Model`, then ValidateModel rejects it (unless baseURL set → treated as
  custom openai).
- No test covers either divergence.
