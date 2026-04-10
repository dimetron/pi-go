# Structure Outline: web-serve

## High-Level Phases

### Phase 1: Pairing System
- PairingManager type with code generation, approval, expiry
- HTTP endpoints: POST /api/pair, GET /api/status
- Pairing HTML page with QR code display

### Phase 2: Session Management
- SessionManager type for per-browser-tab sessions
- Session = (project path, ADK session ID, WebSocket connection)
- Creation, lookup, cleanup methods

### Phase 3: WebSocket Terminal Bridge
- PtyBridge type for PTY process management
- WebSocket handler for terminal I/O
- Bidirectional message handling (input, resize, output)

### Phase 4: Static UI
- index.html with embedded terminal
- xterm.js integration
- WebSocket client logic
- Pairing page with QR display

### Phase 5: CLI Integration
- `pi serve` command
- Flags: --addr, --project, --pairing-timeout
- Server startup and graceful shutdown

### Phase 6: Integration
- Auth middleware (token cookie check)
- Routing (redirect to /pair if not authenticated)
- E2E tests

---

## Key Types (Header File)

```go
package webserver

// Config
type Config struct {
    Addr           string
    Project        string
    PairingTimeout time.Duration
}

// Pairing
type PairingManager struct { ... }
type PairStatus string

// Sessions
type SessionManager struct { ... }
type WebSession struct { ... }

// PTY
type PtyBridge struct { ... }

// Server
type Server struct { ... }
```

---

## Dependencies

| Phase | Depends On |
|-------|------------|
| Session Management | Pairing System |
| WebSocket Bridge | Session Management |
| Static UI | None (self-contained) |
| CLI Integration | Phases 1-4 |
| Integration | Phases 1-5 |

---

## Testing Per Phase

1. **Pairing**: Unit tests for PairingManager
2. **Sessions**: Unit tests for SessionManager
3. **Bridge**: Integration tests with mock WebSocket
4. **UI**: Manual browser testing
5. **CLI**: Command execution tests
6. **Integration**: Full pairing → terminal E2E test
