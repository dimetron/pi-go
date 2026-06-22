# Requirements

## Questions & Answers

# OpenTelemetry Instrumentation Fix Plan

**Status**: Draft  
**Files to modify**: `internal/acp/server/agent.go`, `internal/acp/server/session.go`, `internal/otel/otel.go`  
**Priority**: High — ensures correct span status reporting for observability

---

## Background

OpenTelemetry spans have a `Status` field that backend collectors (Jaeger, Tempo, etc.) use to determine
success/failure. The current implementation:

1. **Missing `SetStatus(codes.Ok, "")` on success paths** — spans end without explicit status, leaving status as "Unset"
   rather than "Ok"
2. **Context propagation issue** — `sendAvailableCommands` uses `context.Background()` instead of a parent context,
   breaking trace linkage
3. **Missing error status** — `RecordError()` alone doesn't set span status to "Error"; `SetStatus(codes.Error, msg)` is
   needed
4. **Dotted attribute names** — `session.cwd`, `prompt.len` use dots instead of underscores (violates semantic
   conventions)
5. **Ignored errors** — `resource.New()` errors are discarded; console exporter case has no implementation

---

## Phase 1: Core OTel Infrastructure (`internal/otel/otel.go`)

### Step 1.1 — Add `codes` import and handle `resource.New()` error

**File**: `internal/otel/otel.go`

**Changes**:

```go
import (
    // existing imports...
    "go.opentelemetry.io/otel/codes"
)

// In initProvider(), replace line 53:
//   res, _ := resource.New(ctx, ...)
//
// With:
//   res, err := resource.New(ctx,
func initProvider() {
    initOnce.Do(func() {
        // ... existing ctx setup ...

        res, resErr := resource.New(ctx,
            resource.WithAttributes(
                semconv.ServiceName(serviceName),
                semconv.ServiceVersion("dev"),
            ),
        )
        if resErr != nil {
            // Log but continue with minimal resource
            res = resource.Default()
        }

        var tp *sdktrace.TracerProvider
        // ... rest unchanged ...
    })
}
```

**Verify**: `go build ./internal/otel/... && go test ./internal/otel/...`  
**Depends on**: None  
**Risk**: Low — `resource.New` errors are non-fatal; `resource.Default()` provides safe fallback

---

### Step 1.2 — Implement console exporter case

**File**: `internal/otel/otel.go`

**Changes**:

```go
import (
    // Add new import:
    "go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
    // ... existing imports ...
)

func initProvider() {
    initOnce.Do(func() {
        // ... existing setup up to switch ...

        switch exporter {
        case "console":
            // Create a stdout trace exporter for local development/debugging
            exp, err := stdouttrace.New(
                stdouttrace.WithPrettyPrint(),
            )
            if err == nil {
                tp = sdktrace.NewTracerProvider(
                    sdktrace.WithBatcher(exp),
                    sdktrace.WithResource(res),
                )
            } else {
                tp = sdktrace.NewTracerProvider(sdktrace.WithResource(res))
            }
        // ... rest of switch unchanged ...
    })
}
```

**Verify**: `go build ./internal/otel/...`  
**Depends on**: 1.1 (to ensure import doesn't conflict)  
**Risk**: Medium — need to verify `stdouttrace` package is available (it's in the otel SDK). If build fails, fallback to
no-op with warning.

---

### Step 1.3 — Update env var comments to use `OTEL_*` prefix

**File**: `internal/otel/otel.go`

**Changes** (lines 8-10 in current file):

```go
// The following env vars are consumed:
//
//	  OTEL_SERVICE_NAME        defaults to "pi-go"
//	  OTEL_EXPORTER_OTLP_ENDPOINT  collector endpoint (e.g. https://collector:4317)
```

**Verify**: `go build ./internal/otel/...`  
**Depends on**: None  
**Risk**: None — comments only

---

## Phase 2: Server Instrumentation (`internal/acp/server/agent.go`)

### Step 2.1 — Import `codes` package

**File**: `internal/acp/server/agent.go`

**Changes**: Add to imports:

```go
import (
    // ... existing imports ...
    "go.opentelemetry.io/otel/codes"
)
```

**Verify**: `go build ./internal/acp/server/...`  
**Depends on**: None

---

### Step 2.2 — Add `SetStatus(codes.Ok, "")` on success paths

**File**: `internal/acp/server/agent.go`

**Changes**:

1. **`Authenticate` (line 130)**: Add before `return`:
   ```go
   func (a *Agent) Authenticate(ctx context.Context, req acp.AuthenticateRequest) (acp.AuthenticateResponse, error) {
       ctx, span := otel.Tracer("acp-server").Start(ctx, "acp.Authenticate")
       defer span.End()
       if l := a.acpLog(); l != nil {
           l.Authenticate(ctx, req)
       }
       span.SetStatus(codes.Ok, "")   // <-- ADD
       return acp.AuthenticateResponse{}, nil
   }
   ```

2. **`Initialize` (line 142)**: Add before `return`:
   ```go
       span.SetStatus(codes.Ok, "")   // <-- ADD
       return acp.InitializeResponse{...}
   ```

3. **`NewSession` (line 160)**: Add before `return`:
   ```go
       // Line 193 - before final return
       span.SetStatus(codes.Ok, "")
       return acp.NewSessionResponse{SessionId: acp.SessionId(sid)}, nil
   ```

4. **`Prompt` success path (line 318-331)**: Add before final `return`:
   ```go
       if l := a.acpLog(); l != nil {
           l.PromptEnd(ctx, sid, resp, nil)
       }
       span.SetStatus(codes.Ok, "")   // <-- ADD
       return resp, nil
   ```

**Verify**: `go build ./internal/acp/server/...`  
**Depends on**: 2.1

---

### Step 2.3 — Fix context propagation in `sendAvailableCommands`

**File**: `internal/acp/server/agent.go`

**Changes** (lines 421-466): Change the function signature and context creation:

```go
// Current (line 421):
func (a *Agent) sendAvailableCommands(sid string) {

// Change to accept context:
func (a *Agent) sendAvailableCommands(ctx context.Context, sid string) {

    // ... existing lookup code ...

    // Line 456: Replace context.Background() with passed ctx
    // Current:
    //   ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    // Changed:
    ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
    defer cancel()

    // ... rest unchanged ...
}

// Update callers (lines 188, 224):
// Line 188 in NewSession:
go a.sendAvailableCommands(ctx, sid)

// Line 224 in Prompt:
go a.sendAvailableCommands(ctx, sid)
```

**Verify**: `go build ./internal/acp/server/...`  
**Depends on**: None  
**Risk**: Medium — goroutine now inherits parent span context, enabling proper trace linkage

---

### Step 2.4 — Fix span status in cancellation path

**File**: `internal/acp/server/agent.go`

**Changes** (around line 301-311):

```go
if promptCtx.Err() != nil {
    if span.IsRecording() {
        span.SetAttributes(attribute.String("prompt.outcome", "cancelled"))
    }
    span.SetStatus(codes.Error, "prompt cancelled")  // <-- ADD: cancellation is Error, not Ok
    resp := acp.PromptResponse{StopReason: acp.StopReasonCancelled}
    if params.MessageId != nil {
        mid := *params.MessageId
        resp.UserMessageId = &mid
    }
    return resp, nil
}
```

**Verify**: `go build ./internal/acp/server/...`  
**Depends on**: 2.1, 2.2

---

### Step 2.5 — Add `SetStatus(codes.Error, msg)` after `RecordError()`

**File**: `internal/acp/server/agent.go`

**Changes**:

1. **Session not found error** (lines 218-219):
   ```go
   if !ok {
       span.RecordError(fmt.Errorf("session %s not found", sid))
       span.SetStatus(codes.Error, "session not found")  // <-- ADD
       return acp.PromptResponse{}, fmt.Errorf("session %s not found", sid)
   }
   ```

2. **Panic recovery** (lines 272-274):
   ```go
   if span.IsRecording() {
       span.RecordError(fmt.Errorf("%v", r))
       span.SetStatus(codes.Error, "handler panicked")  // <-- ADD
   }
   ```

3. **Panicked after defer** (lines 280-282):
   ```go
   if panicked.Load() {
       span.RecordError(fmt.Errorf("handler panicked"))
       span.SetStatus(codes.Error, "handler panicked")  // <-- ADD
       return acp.PromptResponse{}, fmt.Errorf("handler panicked")
   }
   ```

4. **Handler error** (lines 312-317):
   ```go
   if err != nil {
       span.RecordError(err)
       span.SetStatus(codes.Error, err.Error())  // <-- ADD
       if l := a.acpLog(); l != nil {
           l.PromptHandlerError(ctx, sid, err)
       }
       return acp.PromptResponse{}, err
   }
   ```

**Verify**: `go build ./internal/acp/server/...`  
**Depends on**: 2.1, 2.2

---

### Step 2.6 — Normalize attribute naming to use underscores

**File**: `internal/acp/server/agent.go`

**Changes**:

1. **Line 163**: `session.cwd` → `session.working_directory`
   ```go
   span.SetAttributes(attribute.String("session.working_directory", params.Cwd))
   ```

2. **Line 209**: `prompt.len` → `prompt.length`
   ```go
   attribute.Int("prompt.length", len(promptText)),
   ```

3. **Line 296**: `prompt.final_text_len` — already uses underscores, no change needed

4. **Line 297**: `prompt.error` — already uses underscores, no change needed

5. **Line 303**: `prompt.outcome` — already uses underscores, no change needed

6. **Lines 191, 208**: `session.id` — already uses underscore, no change needed

**Verify**: `go build ./internal/acp/server/... && go test ./internal/acp/server/...`  
**Depends on**: None

---

### Step 2.7 — Remove redundant error recording

**File**: `internal/acp/server/agent.go`

**Changes**:

In the error path (lines 312-317), the `prompt.error` attribute is set at line 297 in the success attributes block.
However, this is inside `if span.IsRecording()` which runs unconditionally (all code paths reach it). The `prompt.error`
attribute with an empty string for success is redundant — we should only set it when there's an actual error.

**Current code (lines 293-298)**:

```go
if span.IsRecording() {
    span.SetAttributes(
        attribute.String("prompt.stop_reason", string(result.StopReason)),
        attribute.Int("prompt.final_text_len", len(result.FinalText)),
        attribute.String("prompt.error", errStr(err)),  // <-- Redundant on success
    )
}
```

**Change**: Keep `prompt.error` only when `err != nil`. Move it to the error handling block:

```go
if span.IsRecording() {
    span.SetAttributes(
        attribute.String("prompt.stop_reason", string(result.StopReason)),
        attribute.Int("prompt.final_text_len", len(result.FinalText)),
    )
}
```

And in the error path (lines 312-317):

```go
if err != nil {
    span.RecordError(err)
    span.SetStatus(codes.Error, err.Error())
    if span.IsRecording() {
        span.SetAttributes(attribute.String("prompt.error", errStr(err)))  // <-- Only on error
    }
    // ...
}
```

**Note**: The `RecordError()` call already captures the error details; setting it as an attribute is redundant.

**Verify**: `go build ./internal/acp/server/... && go test ./internal/acp/server/...`  
**Depends on**: 2.5

---

### Step 2.8 — Remove unnecessary `span.IsRecording()` guards

**File**: `internal/acp/server/agent.go`

**Rationale**: `SetAttributes()`, `SetStatus()`, `RecordError()` are safe to call on no-op spans. The `IsRecording()`
guard adds noise and is unnecessary.

**Changes**:

1. **Lines 162-164** (NewSession):
   ```go
   // Current:
   if span.IsRecording() {
       span.SetAttributes(attribute.String("session.working_directory", params.Cwd))
   }
   
   // Changed to:
   span.SetAttributes(attribute.String("session.working_directory", params.Cwd))
   ```

2. **Lines 190-192** (NewSession):
   ```go
   // Remove the IsRecording guard around session.id attribute
   span.SetAttributes(attribute.String("session.id", sid))
   ```

3. **Lines 205-211** (Prompt):
   ```go
   // Current:
   if span.IsRecording() {
       promptText := extractPromptText(params.Prompt)
       span.SetAttributes(
           attribute.String("session.id", string(params.SessionId)),
           attribute.Int("prompt.length", len(promptText)),
       )
   }
   
   // Changed to:
   promptText := extractPromptText(params.Prompt)
   span.SetAttributes(
       attribute.String("session.id", string(params.SessionId)),
       attribute.Int("prompt.length", len(promptText)),
   )
   ```

4. **Lines 293-299** (Prompt success attributes):
   ```go
   // Remove IsRecording guard around prompt attributes
   span.SetAttributes(
       attribute.String("prompt.stop_reason", string(result.StopReason)),
       attribute.Int("prompt.final_text_len", len(result.FinalText)),
   )
   ```

5. **Lines 301-304** (cancellation path):
   ```go
   // Remove IsRecording guard
   span.SetAttributes(attribute.String("prompt.outcome", "cancelled"))
   ```

**Exception**: Keep `IsRecording()` guard for `RecordError()` in panic recovery (line 272-274) — this is actually useful
to avoid overhead when tracing is disabled.

**Verify**: `go build ./internal/acp/server/... && go test ./internal/acp/server/...`  
**Depends on**: 2.6, 2.7

---

## Phase 3: Session Instrumentation (`internal/acp/server/session.go`)

### Step 3.1 — Import `codes` and add status on success path

**File**: `internal/acp/server/session.go`

**Changes**:

1. Add import:
   ```go
   import (
       // ... existing imports ...
       "go.opentelemetry.io/otel/codes"
   )
   ```

2. Add span status for success paths (line 52 area):

   The `acp.Serve` function has two success exit paths:
    - **Peer disconnected** (line 68-70): This is a normal exit, should be `codes.Ok`
    - **Context cancelled** (line 71-74): This returns an error, so status is set to Error

   ```go
   // Line 68-70, add before return nil:
   span.SetStatus(codes.Ok, "")
   logger.Log(ctx, slog.LevelInfo, "acp-server: peer disconnected", "uptime", time.Since(start))
   return nil
   ```

   The context cancelled path already returns `ctx.Err()`, so span status should be set to Error:
   ```go
   case <-ctx.Done():
       span.SetAttributes(attribute.String("exit.reason", "context_cancelled"))
       span.SetStatus(codes.Error, ctx.Err().Error())  // <-- ADD
       logger.Log(ctx, slog.LevelInfo, "acp-server: context canceled", "uptime", time.Since(start))
       return ctx.Err()
   ```

**Verify**: `go build ./internal/acp/server/...`  
**Depends on**: None

---

## Phase 4: Final Verification

### Step 4.1 — Run all affected tests

**Verify**: Full test suite for modified packages:

```bash
go test ./internal/otel/... ./internal/acp/server/...
```

### Step 4.2 — Build entire project

**Verify**:

```bash
go build ./...
```

### Step 4.3 — Lint check

**Verify**:

```bash
golangci-lint run ./internal/otel/... ./internal/acp/server/...
```

---

## Summary Table

| Step    | File       | Change                               | Priority | Depends   |
|---------|------------|--------------------------------------|----------|-----------|
| 1.1     | otel.go    | Handle `resource.New()` error        | High     | -         |
| 1.2     | otel.go    | Implement console exporter           | Medium   | 1.1       |
| 1.3     | otel.go    | Fix env var comments                 | Low      | -         |
| 2.1     | agent.go   | Import `codes`                       | High     | -         |
| 2.2     | agent.go   | Add `SetStatus(codes.Ok)` on success | High     | 2.1       |
| 2.3     | agent.go   | Fix context propagation              | High     | -         |
| 2.4     | agent.go   | Fix cancellation status              | High     | 2.1       |
| 2.5     | agent.go   | Add error status after RecordError   | High     | 2.1       |
| 2.6     | agent.go   | Normalize attribute names            | Medium   | -         |
| 2.7     | agent.go   | Remove redundant error attrs         | Medium   | 2.5       |
| 2.8     | agent.go   | Remove IsRecording guards            | Low      | 2.6, 2.7  |
| 3.1     | session.go | Add span status on success           | Medium   | -         |
| 4.1-4.3 | all        | Final verification                   | High     | All above |

---

## Risk Assessment

| Risk                                    | Likelihood | Impact | Mitigation                                     |
|-----------------------------------------|------------|--------|------------------------------------------------|
| Console exporter import fails           | Low        | Low    | Fallback to no-op if `stdouttrace` unavailable |
| Context change in goroutine breaks flow | Medium     | Medium | Test with actual ACP connection tests          |
| Removing IsRecording causes overhead    | Low        | Low    | OTel SDK handles no-op spans efficiently       |

---

## Notes

- The `codes` package is from `go.opentelemetry.io/otel/codes` (v1.43.0 in go.mod)
- `stdouttrace` exporter is part of `go.opentelemetry.io/otel/exporters/stdout/stdouttrace`
- All changes are backward compatible — span behavior is enhanced, not changed
- After implementation, verify traces in Jaeger/Tempo show correct `Ok`/`Error` status