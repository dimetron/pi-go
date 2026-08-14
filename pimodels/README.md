# pimodels

Build LLM clients for the providers pi-go supports, from outside pi-go.

```go
import "github.com/dimetron/pi-go/pimodels"

m, err := pimodels.New(ctx, "gpt-5.6-luna")
```

`m` is an ADK `model.LLM`. Hand it to any ADK agent.

## What it does

Resolves a model name to a provider, finds the API key, and returns a client.
That is the whole remit — it knows nothing about agents, tools, sessions or
skills.

| Model name | Provider |
|---|---|
| `gpt-*` | openai |
| `claude-*` | anthropic |
| `gemini-*` | gemini |
| `grok-*` | xai |
| `mistral-*`, `magistral-*` | mistral |
| `ollama/<name>`, `*:cloud` | ollama |
| `azure/<deployment>` | azure |
| anything, with `WithBaseURL` | OpenAI-compatible |

## Isolation

This package and the agent package meet at ADK's `model.LLM`, not at each
other. Neither imports the other:

```go
m, err := pimodels.New(ctx, "gpt-5.6-luna")       // this package
a, err := piagent.New(ctx, piagent.WithModel(m))  // the agent package
```

That boundary is enforced by a test, not a convention — `isolation_test.go`
asserts the build graph and fails CI if this package ever reaches into the
agent, the tool set, or the CLI. A public package cannot afford for a change to
provider handling to become a breaking change to the agent API.

## API keys

`New` reads the key from the provider's environment variable:

| Provider | Variable |
|---|---|
| openai | `OPENAI_API_KEY` |
| anthropic | `ANTHROPIC_API_KEY` |
| gemini | `GEMINI_API_KEY` |
| azure | `AZURE_OPENAI_API_KEY` |
| everything else | `<PROVIDER>_API_KEY` |

`WithAPIKey` overrides it. A local Ollama needs neither.

Use `APIKeyEnvVar(provider)` to report a missing credential precisely instead of
letting the first request fail with a provider-specific auth error.

## Options

| Option | Purpose |
|---|---|
| `WithAPIKey` | Explicit credential |
| `WithBaseURL` | Gateway, proxy, or self-hosted endpoint |
| `WithThinkingLevel` | Reasoning effort where supported |
| `WithHeaders` | Extra headers for gateway routing or tenancy |
| `WithConnectTimeout` | Bounds connect only — not the request, which streams |
| `WithCACert` | Trust a PEM bundle alongside system roots |
| `WithInsecureTLS` | Disable verification — prefer `WithCACert` |
| `WithPromptCachingDisabled` | Turn off Anthropic cache breakpoints |
| `WithAdvisor` | Advisor model, where supported |

Options apply in order, so a later one wins.

## Without a model name

`FromConfig(ctx, role)` builds the model a `pi` session would use, reading
`~/.pi-go/config.json`. An empty role means `default`. This is the only function
here that touches pi-go's configuration; `New` is self-contained.

## Inspecting before connecting

```go
info, err := pimodels.Resolve("claude-sonnet-5")
// info.Provider == "anthropic"

pimodels.ContextWindow("gemini-3.7-flash")           // tokens, 0 if unknown
pimodels.ContextWindowFor("azure", "my-deployment")  // provider-aware
```

`Resolve` needs no credential and makes no request, so it is safe to run at
startup to validate configuration.

## Finding the provider from a model

Every model returned by `New` and `FromConfig` also reports its provider family,
so a consumer never needs its own model-name prefix table:

```go
if p, ok := m.(interface{ Provider() string }); ok {
    span.SetAttributes(attribute.String("gen_ai.provider.name", p.Provider()))
}
```

Assert the *shape*, not the named `ProviderNamer` type — that way the consumer
depends on ADK and a structural interface, not on this package. A model built
any other way will not satisfy it, so always handle the not-ok branch.

## A note on `Info.BaseURL`

`Resolve` fills `Info.BaseURL` whenever an explicit endpoint was given. pi-go's
own `provider.ResolveWithBaseURL` leaves that field empty and only the TUI fills
it in afterwards, by hand — so the same call inside pi-go answers less
completely than this one does. An embedder has no second place to look, so this
package completes it.
