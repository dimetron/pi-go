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
| `WithThinkingLevel` | Reasoning effort, validated — see below |
| `WithThinkingLevelString` | The same, from a string |
| `WithHeaders` | Extra headers for gateway routing or tenancy |
| `WithConnectTimeout` | Bounds connect only — not the request, which streams |
| `WithCACert` | Trust a PEM bundle alongside system roots |
| `WithInsecureTLS` | Disable verification — prefer `WithCACert` |
| `WithPromptCachingDisabled` | Turn off Anthropic cache breakpoints |
| `WithWebSearch` | Provider's built-in web search, where supported |
| `WithAdvisor` | Advisor model, where supported |
| `WithMaxOutputTokens` | Cap a reply, in tokens |
| `WithLegacyMaxTokens` | Send `max_tokens` — required for Ollama |
| `WithSystemCAsDisabled` | Trust only the `WithCACert` bundle |
| `WithTraceSink` | Capture every HTTP request and response this client makes |

Options apply in order, so a later one wins.

## Reasoning effort

`WithThinkingLevel` takes a `ThinkingLevel`, and there are five:

| Constant | Value | Meaning |
|---|---|---|
| `ThinkingNone` | `"none"` | Don't reason, where the model can be told |
| `ThinkingLow` | `"low"` | Cheapest level that still reasons |
| `ThinkingMedium` | `"medium"` | Balanced |
| `ThinkingHigh` | `"high"` | pi-go's own config default |
| `ThinkingMax` | `"max"` | Most reasoning the provider offers |

`ThinkingUnset` — the empty string, and the zero value — leaves the provider's
own default in force.

```go
m, err := pimodels.New(ctx, "claude-opus-4-7", pimodels.WithThinkingLevel(pimodels.ThinkingHigh))
```

A level that arrives as text — a flag, a config file, an environment variable —
should go through `WithThinkingLevelString` or `ParseThinkingLevel`, so it gets
the same check:

```go
level, err := pimodels.ParseThinkingLevel(os.Getenv("PI_THINKING"))
if err != nil {
    return err                          // names the accepted set
}
m, err := pimodels.New(ctx, name, pimodels.WithThinkingLevel(level))
```

An unrecognized level is an error from `New`, not a silent pass-through. That
is deliberate: every provider omits a level it does not recognize rather than
rejecting it, so an unchecked typo looks like a setting that was applied and
had no effect. `"xhigh"` and `"off"` are rejected for the same reason — some
providers honor those spellings and others drop them, so accepting either would
mean one provider quietly ignoring what you asked for. `ThinkingMax` and
`ThinkingNone` are the spellings that work everywhere.

### Which providers act on it

Choosing a level is a request, not a guarantee. Two things can weaken it:

- **Some providers ignore the level entirely.** OpenAI, Azure, Gemini and
  agentgateway never receive it, because their constructors do not take one.
  The option is still accepted and validated, so the same code works across
  providers; the model just runs at its own default.
- **Some collapse the range.** Anthropic maps low, medium and high onto a single
  adaptive-thinking config, and Mistral documents only two values (`high` and
  `none`), so it maps every active level to `high`.

Ollama, xAI and OpenRouter preserve the full range.

## Token limits on Ollama-backed endpoints

Both token options below apply to the **OpenAI-compatible** paths — openai,
azure, openrouter, opencode, xai and agentgateway — and are read by the Chat
Completions request builder.

Point an explicit endpoint at a gateway or proxy in front of Ollama, which is
where these matter:

```go
m, err := pimodels.New(ctx, "llama3",
    pimodels.WithBaseURL("http://127.0.0.1:4000/v1"),
    pimodels.WithLegacyMaxTokens(),      // send max_tokens, not max_completion_tokens
    pimodels.WithMaxOutputTokens(4096),  // cap the reply
)
```

`WithLegacyMaxTokens` is not a preference. An endpoint that understands only
`max_tokens` **ignores `max_completion_tokens` without an error**, so the model
runs unbounded instead of being rejected. The agentgateway provider sets this
itself for its known Ollama routes; set it when naming the endpoint yourself.

`WithMaxOutputTokens` is for a backend whose models stop below the default and
**reject** the request rather than clamping it.

### The native `ollama/` client is different

An `ollama/<name>` model name selects pi-go's native Ollama client, which speaks
Ollama's own API rather than the OpenAI wire. Neither option above affects it —
there is no `max_tokens` field to choose and nothing to cap in the request body.
Its output cap is the `PI_OLLAMA_NUM_PREDICT` environment variable, which
defaults to 16384 tokens; a value `<= 0` removes the cap.

To set a cap for Ollama from code rather than the environment, reach it through a
gateway instead:

```go
m, err := pimodels.New(ctx, "llama3",
    pimodels.WithBaseURL("http://127.0.0.1:4000/v1"),
    pimodels.WithMaxOutputTokens(4096),
)
```

## Trusting only one CA

`WithCACert` is additive by default: the bundle is trusted *alongside* the
system roots, which is what a TLS-intercepting proxy wants. When an endpoint
must instead be reachable through that CA **and nothing else**:

```go
m, err := pimodels.New(ctx, "claude-sonnet-5",
    pimodels.WithCACert("/etc/ssl/corp.pem"),
    pimodels.WithSystemCAsDisabled(),
)
```

This is the opposite of the proxy case. Use it only when a public root being
able to reach the endpoint would itself be the problem.

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
