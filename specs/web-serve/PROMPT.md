# web-serve

## Objective
Add a web server to pi-go that exposes a terminal running the pi-go agent via a browser. Users authenticate by scanning a QR code or entering a pair code in the pi-go mobile app. Each browser tab gets its own isolated agent session.

## Key Requirements
1. **Pairing Flow** — Server generates 6-digit code + QR; mobile app approves; browser gains access
2. **Full Terminal** — xterm.js in browser connects via WebSocket to pi-go process
3. **Session Isolation** — Each browser tab = separate session; session = (project path + agent session)
4. **Standard Library** — Use `net/http` for HTTP, `github.com/gorilla/websocket` for WebSocket

## Acceptance Criteria
### Pairing
- Given `pi serve` is running, when user visits `/pair`, then QR code and code are displayed
- Given a pending code, when mobile approves it, then status changes to "approved"
- Given an expired code (5min), when checking status, then "expired" is returned

### Terminal
- Given approved token cookie, when visiting `/`, then terminal emulator appears
- Given terminal connected, when typing commands, then they execute in pi-go
- Given browser resizes, when window changes size, then terminal dimensions update

### Sessions
- Given browser tab connects, when session established, then new agent session created
- Given multiple tabs open, when each connects, then each gets isolated session
- Given disconnect, when browser closes, then resources are cleaned up

## Implementation Slices
1. **PairingManager** — `pairing.go`: code generation, approval, expiry; verify: `go test ... -run Pairing`
2. **HTTP API** — `handlers.go` + `static/pair.html`: POST /api/pair, GET /api/status; verify: `go test ... -run Handler`
3. **SessionManager** — `session.go`: per-tab sessions; verify: `go test ... -run Session`
4. **PtyBridge** — `pty.go`: PTY process for terminal; verify: `go test ... -run Pty`
5. **WebSocket Handler** — `ws.go`: bidirectional terminal I/O; verify: `go test ... -run WebSocket`
6. **Static UI** — `static/index.html`, `style.css`: xterm.js terminal; verify: `go build ...`
7. **Server** — `server.go`: HTTP mux + routing; verify: `go test ... -run Server`
8. **CLI Command** — `cmd/pi/serve.go`: `pi serve` command; verify: `go build ./cmd/pi`
9. **Integration** — `e2e_test.go`: full pairing → terminal flow; verify: `go test ... -run E2E`

## Gates
- **build**: `go build ./...`
- **test**: `go test ./internal/webserver/...`
- **vet**: `go vet ./internal/webserver/...`

## Reference
- Design: `specs/web-serve/design.md`
- Outline: `specs/web-serve/outline.md`
- Plan: `specs/web-serve/plan.md`
- Requirements: `specs/web-serve/requirements.md`
- Research: `specs/web-serve/research/`

## Constraints
- Use `net/http` (standard library) for HTTP server
- Use `github.com/gorilla/websocket` (already in go.mod as indirect) for WebSocket
- Default port: 8080
- Pairing code expiry: 5 minutes
- Each session: project path + ADK session ID

## File Structure
```
internal/webserver/
├── pairing.go          # Slice 1
├── pairing_test.go     # Slice 1
├── session.go          # Slice 3
├── session_test.go     # Slice 3
├── pty.go              # Slice 4
├── pty_test.go         # Slice 4
├── ws.go               # Slice 5
├── ws_test.go          # Slice 5
├── handlers.go         # Slice 2, 6
├── handlers_test.go    # Slice 2
├── server.go           # Slice 7
├── server_test.go      # Slice 7
├── e2e_test.go         # Slice 9
└── static/
    ├── index.html      # Slice 6
    ├── style.css       # Slice 6
    └── pair.html       # Slice 2

cmd/pi/
├── root.go             # Slice 8 (update)
└── serve.go            # Slice 8
```

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| GET | `/` | Terminal UI (auth required) |
| GET | `/pair` | Pairing page with QR |
| POST | `/api/pair` | Create pairing code |
| GET | `/api/status?token=x` | Check pairing status |
| WS | `/ws/:sessionID` | Terminal WebSocket |
