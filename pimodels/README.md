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
| azure | `AZUREOPENAI_API_KEY` |
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
| `WithTraceSink` | Capture every HTTP request and response this client makes |

Options apply in order, so a later one wins.

## Tracing the traffic

`WithTraceSink` hands you each request and response this client sends:

```go
m, err := pimodels.New(ctx, "claude-sonnet-5",
    pimodels.WithTraceSink(func(e pimodels.TraceEntry) {
        audit.Log(e.Exchange, e.Method, e.URL, e.Status, e.Body)
    }))
```

`TraceEntry` is an alias for the type pi-go's own `--trace-http` writes, so
request and response arrive as **separate** entries tied together by
`Exchange` — a streaming body is not complete when its headers are, so one
combined record could not be emitted until the stream drained.

The sink belongs to the client, not the process:

- Two clients with different sinks trace independently.
- Installing one does not displace pi-go's `--trace-http` sink, or anything else
  in the host process that is tracing.

That last point is why this is an option rather than a `SetSink`-style call:
the global sink is a **single slot** that `SetSink` replaces, so a public
library claiming it would clobber its host.

**Capture is on as soon as a sink is set** — there is no separate flag, because a
trace with nowhere to go is not a useful state. Note this is the *opposite*
arrangement from pi-go's own `TraceHTTP` setting, which captures nothing until
`httplog.SetEnabled(true)` **and** a global sink are both in place. Do not
assume the two behave alike.

Two things to know before wiring one up:

- **Entries arrive synchronously** on the request goroutine, so a slow sink
  delays the request. Queue and return rather than writing inline.
- **Bodies are cleartext.** Credentials are masked (`Bearer s***(32 bytes)`),
  but prompts, completions and tool output are not. That is the point of a
  trace and also the reason it is opt-in.

`TraceMaxBody()` reports the byte cap applied before an entry reaches the sink;
a body cut at the cap sets `BodyTruncated`.

## Without a model name

`FromConfig(ctx, role)` builds the model a `pi` session would use, reading
`~/.pi-go/config.json`. An empty role means `default`. This is the only function
here that touches pi-go's configuration; `New` is self-contained.

## Inspecting before connecting

```go
info, err := pimodels.Resolve("claude-sonnet-5")
// info.Provider == "anthropic"

m, err := pimodels.NewFromInfo(ctx, info) // build exactly that resolved pair

pimodels.ContextWindow("gemini-3.7-flash")           // tokens, 0 if unknown
pimodels.ContextWindowFor("azure", "my-deployment")  // provider-aware
```

`Resolve` needs no credential and makes no request, so it is safe to run at
startup to validate configuration. If you want to build the inspected model
later, pass the returned `Info` to `NewFromInfo`; `Info.Model` is the provider's
stripped model/deployment name, so feeding it back to `New` reparses it as a new
user-facing model name.

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
