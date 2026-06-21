# OTel Instrumentation Fixes

Fix OpenTelemetry instrumentation in three files to ensure proper span status reporting, context propagation, and
semantic convention compliance.

## Context

The pi-go codebase has incomplete OpenTelemetry instrumentation:

- Span status is not set on success/error paths
- Context is not properly propagated to async operations
- Attribute names don't follow semantic conventions
- Console exporter is stubbed out

## Instructions

Work through the slices in order. After each slice, verify with `go build` before proceeding.

### Slice 1: Fix OTel Provider

Edit `internal/otel/otel.go`:

1. Add imports:
   ```go
   "go.opentelemetry.io/otel/codes"
   stdouttrace "go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
   ```

2. Fix comment typo on line 8: `EL_SERVICE_NAME` → `OTEL_SERVICE_NAME`

3. Replace `resource.New()` call (lines 53-58) to handle error:
   ```go
   res, err := resource.New(ctx,
       resource.WithAttributes(
           semconv.ServiceName(serviceName),
           semconv.ServiceVersion("dev"),
       ),
   )
   if err != nil {
       res = resource.Default()
   }
   ```

4. Replace the console exporter case (lines 63-64):
   ```go
   case "console":
       exp, err := stdouttrace.New(
           stdouttrace.WithPrettyPrint(),
       )
       if err == nil {
           tp = sdktrace.NewTracerProvider(
               sdktrace.WithBatcher(exp),
               sdktrace.WithResource(res),
           )
       }
   ```

**Verify**: `go build ./internal/otel/...`

---

### Slice 2: Fix Agent Span Status (Part 1)

Edit `internal/acp/server/agent.go`:

1. Add import: `"go.opentelemetry.io/otel/codes"`

2. Add `span.SetStatus(codes.Ok, "")` to success paths:
    - Authenticate: after `defer span.End()`, before `return acp.AuthenticateResponse{}`
    - Initialize: after `defer span.End()`, before `return acp.InitializeResponse{...}`
    - NewSession: after `defer span.End()`, before `return acp.NewSessionResponse{...}`

3. Change `sendAvailableCommands` signature to accept context:
   ```go
   func (a *Agent) sendAvailableCommands(ctx context.Context, sid string)
   ```

4. Update line 188 and 224 to pass `ctx`:
   ```go
   go a.sendAvailableCommands(ctx, sid)
   ```

**Verify**: `go build ./internal/acp/server/...`

---

### Slice 3: Fix Agent Span Status (Part 2)

Continue in `internal/acp/server/agent.go`:

1. Add `span.SetStatus(codes.Error, msg)` after `RecordError()` in error paths:
    - Line 218: `span.SetStatus(codes.Error, "session not found")`
    - Line 273: `span.SetStatus(codes.Error, fmt.Sprintf("handler panicked: %v", r))`
    - Line 281: `span.SetStatus(codes.Error, "handler panicked")`
    - Line 313: `span.SetStatus(codes.Error, err.Error())`

2. Add `span.SetStatus(codes.Error, "prompt cancelled")` in cancellation path (before line 310)

3. Add `span.SetStatus(codes.Ok, "")` at end of Prompt success path (before line 331)

**Verify**: `go build ./internal/acp/server/...`

---

### Slice 4: Normalize Agent Attributes

Continue in `internal/acp/server/agent.go`:

1. Fix dotted attribute names:
    - Line 163: `session.cwd` → `session.working_directory`
    - Line 209: `prompt.len` → `prompt.length`

2. Remove redundant `IsRecording()` guards throughout (keep only in panic recovery)

3. Remove redundant `prompt.error` attribute from success path (keep only on error)

**Verify**: `go build ./internal/acp/server/... && go test ./internal/acp/server/...`

---

### Slice 5: Fix Session Span Status

Edit `internal/acp/server/session.go`:

1. Add import: `"go.opentelemetry.io/otel/codes"`

2. Add `span.SetStatus(codes.Ok, "")` before `return nil` (peer disconnect, line 70)

3. Add `span.SetStatus(codes.Error, ctx.Err().Error())` before `return ctx.Err()` (context cancelled, line 74)

**Verify**: `go build ./internal/acp/server/...`

---

### Slice 6: Final Verification

```bash
go build ./...
go test ./internal/otel/... ./internal/acp/server/...
go get go.opentelemetry.io/otel/exporters/stdout/stdouttrace@v1.43.0
```

All commands must pass.

---

## Gates

- `go build ./...`
- `go test ./internal/otel/... ./internal/acp/server/...`
- `go vet ./internal/otel/... ./internal/acp/server/...`

## Files Modified

| File                             | Slices  |
|----------------------------------|---------|
| `internal/otel/otel.go`          | 1       |
| `internal/acp/server/agent.go`   | 2, 3, 4 |
| `internal/acp/server/session.go` | 5       |