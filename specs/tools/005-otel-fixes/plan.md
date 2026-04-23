# OTel Instrumentation Fixes — Implementation Plan

## Overview

Fix OpenTelemetry instrumentation in three files to ensure proper span status reporting, context propagation, and
semantic convention compliance.

---

## Slices

### Slice 1: Fix OTel Provider (`internal/otel/otel.go`)

- [ ] **Add `codes` and `stdouttrace` imports**
  ```go
  "go.opentelemetry.io/otel/codes"
  stdouttrace "go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
  ```
- [ ] **Fix env var comment typo** (line 8): `EL_SERVICE_NAME` → `OTEL_SERVICE_NAME`
- [ ] **Handle `resource.New()` error** with fallback to `resource.Default()`:
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
- [ ] **Implement console exporter case** using `stdouttrace`:
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

### Slice 2: Fix Agent Span Status (`internal/acp/server/agent.go`) — Part 1

- [ ] **Add `codes` import**
  ```go
  "go.opentelemetry.io/otel/codes"
  ```
- [ ] **Add `span.SetStatus(codes.Ok, "")`** to success paths:
    - Authenticate (after defer span.End(), before return at line 135)
    - Initialize (after defer span.End(), before return at line 155)
    - NewSession (after defer span.End(), before return at line 193)
- [ ] **Fix context propagation**: change `sendAvailableCommands(sid string)` to
  `sendAvailableCommands(ctx context.Context, sid string)`
- [ ] **Update callers** at lines 188 and 224 to pass `ctx`

**Verify**: `go build ./internal/acp/server/...`

---

### Slice 3: Fix Agent Span Status (`internal/acp/server/agent.go`) — Part 2

- [ ] **Add `span.SetStatus(codes.Error, msg)`** after `RecordError()` at:
    - Line 218 (session not found): `span.SetStatus(codes.Error, "session not found")`
    - Line 273 (panic in handler): `span.SetStatus(codes.Error, fmt.Sprintf("handler panicked: %v", r))`
    - Line 281 (panic after defer): `span.SetStatus(codes.Error, "handler panicked")`
    - Line 313 (handler error): `span.SetStatus(codes.Error, err.Error())`
- [ ] **Add `span.SetStatus(codes.Error, "prompt cancelled")`** in cancellation path (before line 310)
- [ ] **Add `span.SetStatus(codes.Ok, "")`** at end of Prompt success path (before line 331)

**Verify**: `go build ./internal/acp/server/...`

---

### Slice 4: Normalize Agent Attributes (`internal/acp/server/agent.go`)

- [ ] **Fix dotted attribute names**:
    - Line 163: `session.cwd` → `session.working_directory`
    - Line 209: `prompt.len` → `prompt.length`
- [ ] **Remove redundant `IsRecording()` guards** (keep for panic recovery only):
    - Lines 162-164 (NewSession cwd)
    - Lines 190-192 (NewSession id)
    - Lines 205-211 (Prompt attributes)
    - Lines 293-299 (Prompt success attrs)
    - Lines 301-307 (cancellation attrs)
- [ ] **Remove redundant `prompt.error` attribute** from success path (line 297); keep only on error

**Verify**: `go build ./internal/acp/server/... && go test ./internal/acp/server/...`

---

### Slice 5: Fix Session Span Status (`internal/acp/server/session.go`)

- [ ] **Add `codes` import**
  ```go
  "go.opentelemetry.io/otel/codes"
  ```
- [ ] **Add `span.SetStatus(codes.Ok, "")`** before return nil (peer disconnect, line 70)
- [ ] **Add `span.SetStatus(codes.Error, ctx.Err().Error())`** before return ctx.Err() (context cancelled, line 74)

**Verify**: `go build ./internal/acp/server/...`

---

### Slice 6: Final Verification

- [ ] **Run full build**: `go build ./...`
- [ ] **Run tests**: `go test ./internal/otel/... ./internal/acp/server/...`
- [ ] **Add stdouttrace to go.mod** if not present:
  `go get go.opentelemetry.io/otel/exporters/stdout/stdouttrace@v1.43.0`

**Verify**: All commands pass with exit code 0

---

## Dependencies

- Slice 2 depends on Slice 1 (context changes require provider to be stable)
- Slice 3 depends on Slice 2 (SetStatus needs codes import)
- Slice 4 depends on Slice 3 (attribute changes depend on prior fixes)
- Slice 5 is independent of Slices 2-4 (can be done in parallel)
- Slice 6 depends on all previous slices

---

## Gates

- **build**: `go build ./...`
- **test**: `go test ./internal/otel/... ./internal/acp/server/...`
- **vet**: `go vet ./internal/otel/... ./internal/acp/server/...`

---

## Files Modified

| File                             | Slices  |
|----------------------------------|---------|
| `internal/otel/otel.go`          | 1       |
| `internal/acp/server/agent.go`   | 2, 3, 4 |
| `internal/acp/server/session.go` | 5       |

---

## Risk Mitigation

1. **Console exporter build failure**: If `stdouttrace` is unavailable, fallback to no-op provider with warning log
2. **Context change breaks flow**: Test with actual ACP connection before deploying
3. **IsRecording removal overhead**: OTel SDK handles no-op spans efficiently; overhead is negligible