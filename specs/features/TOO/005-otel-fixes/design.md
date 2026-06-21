# OTel Instrumentation Fixes — Design

## Current State

Three files have incomplete OpenTelemetry instrumentation:

| File                             | Issues                                                                |
|----------------------------------|-----------------------------------------------------------------------|
| `internal/otel/otel.go`          | Ignored resource errors, unimplemented console exporter, env var typo |
| `internal/acp/server/agent.go`   | Missing span status, context propagation bug, dotted attrs            |
| `internal/acp/server/session.go` | Missing span status on success/error paths                            |

## Desired End State

- All spans have explicit status: `Ok` for success, `Error` for failures
- Context properly propagates through goroutines for trace linkage
- Attribute names follow OpenTelemetry semantic conventions (snake_case)
- OTel provider handles errors gracefully with fallbacks
- Console exporter provides local debugging capability

## Architecture Overview

```
┌─────────────────────────────────────────────────────────────┐
│                    OTel Initialization                       │
│                  (internal/otel/otel.go)                    │
├─────────────────────────────────────────────────────────────┤
│  Resource creation with error handling                       │
│  ┌──────────┐    ┌──────────┐    ┌──────────┐             │
│  │   OTLP   │    │ Console  │    │   None   │             │
│  │ exporter │    │ exporter │    │ (no-op)  │             │
│  └──────────┘    └──────────┘    └──────────┘             │
└─────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────┐
│                   ACP Server Spans                          │
│           (internal/acp/server/agent.go)                   │
├─────────────────────────────────────────────────────────────┤
│  acp.Authenticate    ──→ SetStatus(Ok)                     │
│  acp.Initialize      ──→ SetStatus(Ok)                     │
│  acp.NewSession      ──→ SetStatus(Ok)                     │
│  acp.Prompt          ──→ SetStatus(Ok) or SetStatus(Error)  │
│                      │                                     │
│                      └── sendAvailableCommands(ctx, sid)   │
│                                └── Context propagated      │
└─────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────┐
│               Session Span (session.go)                     │
├─────────────────────────────────────────────────────────────┤
│  peer_disconnected  ──→ SetStatus(Ok)                      │
│  context_cancelled  ──→ SetStatus(Error)                   │
└─────────────────────────────────────────────────────────────┘
```

## Changes by File

### 1. `internal/otel/otel.go`

```go
// NEW IMPORTS
import (
    "go.opentelemetry.io/otel/codes"
    "go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
)

// In initProvider(), replace resource.New error handling:
res, err := resource.New(ctx,
    resource.WithAttributes(
        semconv.ServiceName(serviceName),
        semconv.ServiceVersion("dev"),
    ),
)
if err != nil {
    res = resource.Default()
}

// Add console exporter case:
case "console":
    exp, err := stdouttrace.New(stdouttrace.WithPrettyPrint())
    if err == nil {
        tp = sdktrace.NewTracerProvider(
            sdktrace.WithBatcher(exp),
            sdktrace.WithResource(res),
        )
    } else {
        tp = sdktrace.NewTracerProvider(sdktrace.WithResource(res))
    }
```

**Fix typo** (line 8): `EL_SERVICE_NAME` → `OTEL_SERVICE_NAME`

---

### 2. `internal/acp/server/agent.go`

```go
// NEW IMPORT
import "go.opentelemetry.io/otel/codes"

// MODIFY sendAvailableCommands signature:
func (a *Agent) sendAvailableCommands(ctx context.Context, sid string) {
    // ... existing code ...
    ctx, cancel := context.WithTimeout(ctx, 5*time.Second)  // Was: context.Background()
    defer cancel()
}

// UPDATE callers:
go a.sendAvailableCommands(ctx, sid)  // Lines 188, 224

// ADD SetStatus(codes.Ok) before returns:
func (a *Agent) Authenticate(...) {
    // ... existing code ...
    span.SetStatus(codes.Ok, "")
    return acp.AuthenticateResponse{}, nil
}

func (a *Agent) Initialize(...) {
    // ... existing code ...
    span.SetStatus(codes.Ok, "")
    return resp, nil
}

func (a *Agent) NewSession(...) {
    // ... existing code ...
    span.SetStatus(codes.Ok, "")
    return acp.NewSessionResponse{SessionId: acp.SessionId(sid)}, nil
}

// MODIFY Prompt error path (line 312):
if err != nil {
    span.RecordError(err)
    span.SetStatus(codes.Error, err.Error())  // ADD
    // ...
}

// MODIFY cancellation path (line 301):
if promptCtx.Err() != nil {
    span.SetAttributes(attribute.String("prompt.outcome", "cancelled"))
    span.SetStatus(codes.Error, "prompt cancelled")  // ADD
    // ...
}

// ADD SetStatus(codes.Ok) at end of Prompt success (before line 331):
    if l := a.acpLog(); l != nil {
        l.PromptEnd(ctx, sid, resp, nil)
    }
    span.SetStatus(codes.Ok, "")  // ADD
    return resp, nil
}

// FIX attribute names:
attribute.String("session.working_directory", params.Cwd)  // Was: session.cwd
attribute.Int("prompt.length", len(promptText))            // Was: prompt.len

// UPDATE IsRecording guards:
// Remove where safe; keep for RecordError in panic recovery
```

---

### 3. `internal/acp/server/session.go`

```go
// NEW IMPORT
import "go.opentelemetry.io/otel/codes"

// In Serve(), add status before returns:
case <-conn.Done():
    span.SetStatus(codes.Ok, "")
    logger.Log(...)
    return nil

case <-ctx.Done():
    span.SetStatus(codes.Error, ctx.Err().Error())
    logger.Log(...)
    return ctx.Err()
```

## Error Handling Strategy

| Scenario                        | Behavior                                         |
|---------------------------------|--------------------------------------------------|
| `resource.New()` fails          | Log internally, fallback to `resource.Default()` |
| Console exporter creation fails | Fall back to no-op TracerProvider                |
| `updater.Update()` fails        | Silently discard (existing behavior)             |

## Acceptance Criteria

### OTel Provider

- **Given** the application starts, **when** `OTEL_TRACES_EXPORTER=console` is set, **then** traces are printed to
  stdout in pretty format
- **Given** `resource.New()` fails, **then** the application continues with `resource.Default()` instead of panicking

### Span Status

- **Given** a successful ACP call, **when** the span ends, **then** span status is `Ok`
- **Given** an error occurs (session not found, handler error, panic), **when** the span ends, **then** span status is
  `Error` with descriptive message

### Context Propagation

- **Given** `sendAvailableCommands` is called, **when** it creates a timeout context, **then** the context inherits the
  parent span context for trace linkage

### Attribute Naming

- **Given** any span with session or prompt attributes, **when** attributes are set, **then** names use snake_case (
  e.g., `session.working_directory`, not `session.cwd`)

### Session Span

- **Given** `acp.Serve` receives peer disconnect, **when** the span ends, **then** status is `Ok`
- **Given** `acp.Serve` receives context cancellation, **when** the span ends, **then** status is `Error`

## Testing Strategy

1. **Unit tests** in `internal/otel/` and `internal/acp/server/` packages — verify behavior with mocks
2. **Build verification** — ensure all imports resolve, especially `stdouttrace`
3. **Manual testing** — set `OTEL_TRACES_EXPORTER=console` and trigger ACP operations to see output

## Dependencies

Add to go.mod:

```
go.opentelemetry.io/otel/exporters/stdout/stdouttrace v1.43.0
```