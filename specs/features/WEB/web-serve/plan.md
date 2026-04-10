# Implementation Plan: web-serve

## Vertical Slices

---

### Slice 1: PairingManager — Core Logic ✅

**What to implement:**
- `internal/webserver/pairing.go` — PairingManager type
  - `NewPairingManager(timeout time.Duration) *PairingManager`
  - `CreatePair(project string) (code, token string, qrData []byte, error)`
  - `CheckStatus(token string) (PairStatus, error)`
  - `Approve(code string) error`
  - `IsApproved(token string) bool`
  - `GetProject(token string) (string, error)`
- `internal/webserver/pairing_test.go` — unit tests

**Files:**
- ✅ Create: `internal/webserver/pairing.go`
- ✅ Create: `internal/webserver/pairing_test.go`

**Verification:**
```bash
go test ./internal/webserver/... -run Pairing
```

**Dependencies:** None

**Status:** ✅ COMPLETED — All tests pass (12 pairing tests)

---

### Slice 2: HTTP API Handlers — Pairing Endpoints ✅

**What to implement:**
- `internal/webserver/handlers.go` — HTTP handlers
  - `handlePair(w http.ResponseWriter, r *http.Request)` — serve pairing HTML page
  - `handleCreatePair(w http.ResponseWriter, r *http.Request)` — POST /api/pair
  - `handleStatus(w http.ResponseWriter, r *http.Request)` — GET /api/status
- `internal/webserver/handlers_test.go` — API tests
- `internal/webserver/static/pair.html` — minimal pairing page with QR

**Files:**
- ✅ Create: `internal/webserver/handlers.go`
- ✅ Create: `internal/webserver/handlers_test.go`
- ✅ Create: `internal/webserver/static/pair.html`

**Verification:**
```bash
go test ./internal/webserver/... -run Handler
```

**Dependencies:** Slice 1 (PairingManager)

**Status:** ✅ COMPLETED — All tests pass (7 handler tests)

---

### Slice 3: SessionManager — Per-Tab Sessions ✅

**What to implement:**
- `internal/webserver/session.go` — SessionManager type
  - `NewSessionManager() *SessionManager`
  - `CreateSession(project, token string) (*WebSession, error)`
  - `GetSession(sessionID string) (*WebSession, bool)`
  - `CloseSession(sessionID string) error`
  - `CleanupExpired()`

**Files:**
- ✅ Create: `internal/webserver/session.go`
- ✅ Create: `internal/webserver/session_test.go`

**Verification:**
```bash
go test ./internal/webserver/... -run Session
```

**Dependencies:** Slice 1 (PairingManager used for token validation)

**Status:** ✅ COMPLETED — All tests pass (7 session tests)

---

### Slice 4: PtyBridge — Terminal Process Management ✅

**What to implement:**
- `internal/webserver/pty.go` — PtyBridge type
  - `NewPtyBridge(project string) (*PtyBridge, error)`
  - `HandleWebSocket(conn *websocket.Conn)`
  - `Close() error`
- `internal/webserver/pty_test.go` — basic tests

**Files:**
- ✅ Create: `internal/webserver/pty.go`
- ✅ Create: `internal/webserver/pty_test.go`

**Verification:**
```bash
go test ./internal/webserver/... -run Pty
```

**Dependencies:** None (standalone)

**Status:** ✅ COMPLETED — All tests pass (3 PTY tests)

---

### Slice 5: WebSocket Handler — Terminal I/O

**What to implement:**
- `internal/webserver/ws.go` — WebSocket terminal handler
  - `handleTerminal(w http.ResponseWriter, r *http.Request)`
  - `handleAuth(next http.Handler) http.Handler` — middleware for token check
  - Message types: WSInput, WSOutput
- `internal/webserver/ws_test.go` — WebSocket tests

**Files:**
- Create: `internal/webserver/ws.go`
- Create: `internal/webserver/ws_test.go`

**Verification:**
```bash
go test ./internal/webserver/... -run WebSocket
```

**Dependencies:** Slice 3 (SessionManager), Slice 4 (PtyBridge)

---

### Slice 6: Static UI — Terminal Page

**What to implement:**
- `internal/webserver/static/index.html` — main terminal page
  - xterm.js loaded from CDN
  - WebSocket connection setup
  - Terminal input/output handling
  - Cookie-based token auth check
- `internal/webserver/static/style.css` — minimal styling
- Update `handlers.go` to serve static files

**Files:**
- Create: `internal/webserver/static/index.html`
- Create: `internal/webserver/static/style.css`
- Update: `internal/webserver/handlers.go`

**Verification:**
```bash
# Manual test: visit http://localhost:8080/
# Should show terminal or redirect to /pair
```

**Dependencies:** Slice 5 (WebSocket handler)

---

### Slice 7: Server — HTTP Server Setup

**What to implement:**
- `internal/webserver/server.go` — Server type
  - `NewServer(cfg Config) *Server`
  - `Start() error`
  - `Shutdown(ctx context.Context) error`
- `internal/webserver/server_test.go` — server tests

**Files:**
- Create: `internal/webserver/server.go`
- Create: `internal/webserver/server_test.go`

**Verification:**
```bash
go test ./internal/webserver/... -run Server
```

**Dependencies:** Slices 2, 3, 5, 6

---

### Slice 8: CLI Command — `pi serve`

**What to implement:**
- `cmd/pi/serve.go` — serve command
  - `serveCmd` cobra command
  - Flags: `--addr`, `--project`, `--pairing-timeout`
  - Main execution

**Files:**
- Create: `cmd/pi/serve.go`
- Update: `cmd/pi/root.go` — add serve subcommand

**Verification:**
```bash
go build ./cmd/pi && ./pi serve --help
```

**Dependencies:** Slice 7 (Server)

---

### Slice 9: Integration Tests — Full Flow

**What to implement:**
- `internal/webserver/e2e_test.go` — full pairing → terminal flow
  - Create pair, check pending status
  - Approve pair, check approved status
  - Connect WebSocket, send/receive data
  - Cleanup on disconnect

**Files:**
- Create: `internal/webserver/e2e_test.go`

**Verification:**
```bash
go test ./internal/webserver/... -run E2E
```

**Dependencies:** Slices 1-8

---

## File Structure

```
internal/webserver/
├── pairing.go
├── pairing_test.go
├── session.go
├── session_test.go
├── pty.go
├── pty_test.go
├── ws.go
├── ws_test.go
├── handlers.go
├── handlers_test.go
├── server.go
├── server_test.go
├── e2e_test.go
└── static/
    ├── index.html
    ├── style.css
    └── pair.html

cmd/pi/
├── root.go      (update)
└── serve.go    (create)
```

## Build & Test Commands

```bash
# Build
go build ./...

# Test all webserver
go test ./internal/webserver/...

# Test specific slice
go test ./internal/webserver/... -run Pairing
go test ./internal/webserver/... -run Session

# Run server
go run ./cmd/pi serve --addr :8080 --project /tmp/test
```
