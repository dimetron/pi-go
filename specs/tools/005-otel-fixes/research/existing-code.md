# OTel Fixes Research Findings

## Files Under Investigation

### 1. `internal/otel/otel.go` (139 lines)

**Issues confirmed:**

- **Line 53**: `res, _ := resource.New(ctx, ...)` — error is silently discarded
- **Lines 63-64**: console case falls through with TODO comment:
  ```go
  case "console":
      // TODO: console exporter – for now fall through to no-op.
  ```
- **Line 8**: env var comment has typo — `EL_SERVICE_NAME` missing 'OT' prefix

**Dependencies:** `go.opentelemetry.io/otel v1.43.0`, SDK v1.43.0, trace v1.43.0

**Missing dependency:** `stdouttrace` exporter not in go.mod

---

### 2. `internal/acp/server/agent.go` (488 lines)

**Issues confirmed:**

#### Missing `codes` import

- No `go.opentelemetry.io/otel/codes` import at top of file

#### Missing `SetStatus(codes.Ok, "")` on success paths:

1. **Authenticate** (lines 129-136): Returns success but no span status set
2. **Initialize** (lines 141-156): Returns success but no span status set
3. **NewSession** (lines 159-194): Returns success but no span status set
4. **Prompt** success path (lines 319-331): Returns success but no span status set

#### Missing `SetStatus(codes.Error, msg)` after `RecordError()`:

1. **Line 218**: Session not found — `RecordError` without `SetStatus`
2. **Lines 272-274**: Panic recovery — `RecordError` without `SetStatus`
3. **Lines 280-282**: Panic after defer check — `RecordError` without `SetStatus`
4. **Line 313**: Handler error — `RecordError` without `SetStatus`

#### Dotted attribute names violating semantic conventions:

- **Line 163**: `session.cwd` → should be `session.working_directory`
- **Line 209**: `prompt.len` → should be `prompt.length`

#### Context propagation bug:

- **Line 421**: `sendAvailableCommands(sid string)` — no context parameter
- **Line 456**: `ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)` — breaks trace linkage
- **Line 188**: `go a.sendAvailableCommands(sid)` — no context passed
- **Line 224**: `go a.sendAvailableCommands(sid)` — no context passed

#### Cancellation path status:

- **Lines 301-310**: Returns `StopReasonCancelled` but no error status set on span (cancellation is an error condition,
  not success)

---

### 3. `internal/acp/server/session.go` (77 lines)

**Issues confirmed:**

- No `codes` import
- **Lines 67-70**: Peer disconnected — no span status set (should be `codes.Ok`)
- **Lines 71-74**: Context cancelled — returns error but no span status set (should be `codes.Error`)

---

## Build Commands

```bash
# Build individual packages
go build ./internal/otel/...
go build ./internal/acp/server/...

# Test individual packages
go test ./internal/otel/...
go test ./internal/acp/server/...

# Full build and test
go build ./...
go test ./...
```

## Risk Assessment

1. **Console exporter**: `stdouttrace` not in dependencies. Will need to add
   `go.opentelemetry.io/otel/exporters/stdout/stdouttrace` to go.mod or use alternative approach.

2. **Context change in goroutines**: Modifying `sendAvailableCommands` signature is safe but requires updating 2 call
   sites.

3. **Removing IsRecording guards**: Safe since OTel SDK handles no-op spans efficiently. All modified methods are safe
   to call on nil/recording spans.