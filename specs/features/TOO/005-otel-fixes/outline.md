# OTel Fixes — Implementation Outline

## Phases

### Phase 1: OTel Provider Fixes

1. Fix env var comment typo in otel.go
2. Handle `resource.New()` error with fallback
3. Implement console exporter case

### Phase 2: Agent Span Status Fixes

1. Import `codes` package
2. Add `SetStatus(codes.Ok)` to success paths (Authenticate, Initialize, NewSession, Prompt)
3. Add `SetStatus(codes.Error)` after `RecordError()` calls
4. Fix context propagation in `sendAvailableCommands`
5. Normalize attribute names to snake_case
6. Clean up redundant `IsRecording()` guards

### Phase 3: Session Span Fixes

1. Import `codes` package
2. Add `SetStatus(codes.Ok)` for peer disconnect
3. Add `SetStatus(codes.Error)` for context cancellation

### Phase 4: Verification

- Build and test all modified packages

---

## Order of Changes

```
Phase 1 (otel.go)
    ↓
Phase 2 (agent.go) — depends on Phase 1 for context propagation
    ↓
Phase 3 (session.go) — independent, can parallelize
    ↓
Phase 4 (verification)
```

---

## Key Type Signatures

```go
// NEW: sendAvailableCommands signature change
func (a *Agent) sendAvailableCommands(ctx context.Context, sid string)

// NEW: console exporter creation
exp, err := stdouttrace.New(stdouttrace.WithPrettyPrint())

// Pattern: SetStatus calls
span.SetStatus(codes.Ok, "")
span.SetStatus(codes.Error, "descriptive message")
```

---

## Verification Commands

```bash
go build ./internal/otel/...
go build ./internal/acp/server/...
go test ./internal/otel/... ./internal/acp/server/...
```

---

## New Dependency

```go
go.opentelemetry.io/otel/exporters/stdout/stdouttrace v1.43.0
```

---

## Risk Notes

- Console exporter: verify `stdouttrace` package exists in OTel SDK
- Context change: update 2 call sites (lines 188, 224)
- IsRecording guards: safe to remove, SDK handles no-op spans