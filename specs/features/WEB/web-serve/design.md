# Design: web-serve

## Current State
- pi-go is a TUI-based coding agent
- No web server or remote access capability
- All interaction happens via local terminal

## Desired End State
- Web server on port 8080 serving a terminal UI
- Pair code authentication via QR code
- Full terminal emulator (xterm.js) in browser
- Each browser tab gets its own agent session
- Sessions are isolated (project path + running agent)

## Architecture Overview

```
┌─────────────────────────────────────────────────────────────────┐
│                        Web Browser                              │
│  ┌─────────────────────────────────────────────────────────┐    │
│  │                   Terminal UI (xterm.js)                 │    │
│  │  ┌──────────┐  WebSocket  ┌──────────┐  ┌──────────┐    │    │
│  │  │  xterm   │◄──────────►│  page    │◄►│  Pi-go   │    │    │
│  │  └──────────┘            └──────────┘  └──────────┘    │    │
│  └─────────────────────────────────────────────────────────┘    │
└─────────────────────────────────────────────────────────────────┘
           │                    │                    │
           │                    │                    │
           ▼                    ▼                    ▼
┌─────────────────────────────────────────────────────────────────┐
│                     Go Web Server (net/http)                    │
│  ┌─────────────────────────────────────────────────────────┐    │
│  │                   HTTP Handler Mux                       │    │
│  │  GET /           → Serve index.html (terminal UI)        │    │
│  │  GET /pair       → Serve pairing page + QR code          │    │
│  │  POST /api/pair  → Generate pair code, return QR data    │    │
│  │  GET /api/status → Check pair status (polling)           │    │
│  │  WS  /ws/:sessionID → Terminal WebSocket handler         │    │
│  └─────────────────────────────────────────────────────────┘    │
│                         │                                        │
│  ┌─────────────────────────────────────────────────────────┐    │
│  │              Pairing Manager                              │    │
│  │  - Generates random 6-digit codes                       │    │
│  │  - Stores pending codes with expiry                     │    │
│  │  - Tracks approved browser tokens                        │    │
│  └─────────────────────────────────────────────────────────┘    │
└─────────────────────────────────────────────────────────────────┘
           │                    │                    │
           ▼                    ▼                    ▼
┌─────────────────────────────────────────────────────────────────┐
│                     pi-go Agent Runtime                          │
│  ┌─────────────────────────────────────────────────────────┐    │
│  │              Session Manager (per-browser-tab)           │    │
│  │  - Each WS connection = new agent session               │    │
│  │  - Session = (project path, ADK session ID)             │    │
│  │  - Agent created per session with project tools         │    │
│  └─────────────────────────────────────────────────────────┘    │
│                         │                                        │
│  ┌─────────────────────────────────────────────────────────┐    │
│  │              PTY Bridge (go-term)                        │    │
│  │  - Spawns interactive pi-go process                     │    │
│  │  - Pipes terminal I/O through WebSocket                │    │
│  └─────────────────────────────────────────────────────────┘    │
└─────────────────────────────────────────────────────────────────┘
```

## Components and Interfaces

### 1. Web Server (`internal/webserver/`)
```go
// server.go
type Server struct {
    addr        string
    mux         *http.ServeMux
    pairingMgr  *PairingManager
    sessions    *SessionManager
}

func NewServer(addr string) *Server
func (s *Server) Start() error
func (s *Server) Shutdown(ctx context.Context) error
```

### 2. Pairing Manager (`internal/webserver/pairing.go`)
```go
type PairingManager struct {
    mu      sync.RWMutex
    pending map[string]*PendingPair // code → pending info
    approved map[string]*ApprovedPair // token → approved info
}

type PendingPair struct {
    Code      string
    CreatedAt time.Time
    ExpiresAt time.Time
    Project   string
    Token     string // browser token to approve
}

func (pm *PairingManager) CreatePair(project string) (code, token string, qrData []byte, error)
func (pm *PairingManager) CheckStatus(token string) (status string, error)
func (pm *PairingManager) Approve(code string) error
func (pm *PairingManager) IsApproved(token string) bool
func (pm *PairingManager) GetProject(token string) (string, error)
```

### 3. Session Manager (`internal/webserver/session.go`)
```go
type SessionManager struct {
    mu       sync.RWMutex
    sessions map[string]*WebSession // sessionID → session
}

type WebSession struct {
    ID        string
    Project   string
    Token     string
    CreatedAt time.Time
    Conn      *websocket.Conn
    // PTY process for terminal
    Pty       *os.Process
}

func (sm *SessionManager) CreateSession(project, token string) (*WebSession, error)
func (sm *SessionManager) GetSession(sessionID string) (*WebSession, bool)
func (sm *SessionManager) CloseSession(sessionID string) error
```

### 4. PTY Bridge (`internal/webserver/pty.go`)
```go
type PtyBridge struct {
    project  string
    stdin    *os.File
    stdout   *os.File
    stderr   *os.File
    process  *os.Process
}

func NewPtyBridge(project string) (*PtyBridge, error)
func (pb *PtyBridge) HandleWebSocket(conn *websocket.Conn)
func (pb *PtyBridge) Close() error
```

### 5. CLI Command
```go
// cmd/pi/serve.go
var serveCmd = &cobra.Command{
    Use:   "serve",
    Short: "Start web server for remote terminal access",
    RunE: func(cmd *cobra.Command, args []string) error {
        // Parse --project flag
        // Start web server
        // Block until shutdown
    },
}
```

## Data Models

### Pairing State
```go
type PairStatus string
const (
    PairStatusPending    PairStatus = "pending"
    PairStatusApproved   PairStatus = "approved"
    PairStatusExpired    PairStatus = "expired"
)
```

### WebSocket Messages
```go
// Client → Server
type WSInput struct {
    Type string `json:"type"` // "input", "resize"
    Data string `json:"data"` // for input: keystrokes; for resize: "W×H"
}

// Server → Client
type WSOutput struct {
    Type string `json:"type"` // "output", "close"
    Data string `json:"data"` // terminal output
}
```

## Patterns to Follow

1. **Error handling**: Use `fmt.Errorf("operation: %w", err)` pattern
2. **Shutdown**: Context with cancellation, graceful shutdown with timeout
3. **HTTP handlers**: Standard `http.Handler` interface
4. **WebSocket**: gorilla/websocket pattern with ping/pong
5. **Logging**: Use existing `internal/logger` package
6. **Configuration**: Add to `internal/config/config.go`

## Error Handling Strategy

1. **Pairing errors**: Return JSON with `{"error": "message"}`, HTTP 400/404/408
2. **WebSocket errors**: Log and close connection gracefully
3. **Session errors**: Clean up resources, notify client, close WebSocket
4. **Agent errors**: Display in terminal output, allow reconnect

## HTTP API

### Endpoints

| Method | Path | Description |
|--------|------|-------------|
| GET | `/` | Serve terminal UI (requires approved token cookie) |
| GET | `/pair` | Pairing page with QR code |
| POST | `/api/pair` | Create new pairing code |
| GET | `/api/status?token=xxx` | Check pairing status (polling) |
| WS | `/ws/:sessionID` | Terminal WebSocket |

### Request/Response Examples

```bash
# Create pairing
curl -X POST http://localhost:8080/api/pair \
  -H "Content-Type: application/json" \
  -d '{"project": "/path/to/project"}'
# Response: {"token": "abc123", "code": "456789", "qr": "base64png..."}

# Check status
curl http://localhost:8080/api/status?token=abc123
# Response: {"status": "pending"} or {"status": "approved", "sessionID": "xyz..."}
```

## Static Files Structure

```
internal/webserver/static/
├── index.html      # Main terminal UI
├── style.css      # Styles
├── app.js         # WebSocket client + xterm.js integration
└── xterm.js       # Terminal emulator library
```

## CLI Flags

```bash
pi serve [flags]

Flags:
  --addr string      Listen address (default ":8080")
  --project string   Default project path (default: current directory)
  --pairing-timeout duration  Pairing code expiry (default: 5m)
```

## Acceptance Criteria

### Pairing Flow
- Given a user starts `pi serve`, when they visit `/pair`, then a QR code and 6-digit code are displayed
- Given a pairing code exists, when the user enters the code in pi-go mobile and approves, then the status changes to "approved"
- Given a pairing code is expired (5min), when the user checks status, then they get "expired" status

### Terminal Access
- Given a browser has an approved token cookie, when they visit `/`, then a terminal emulator appears
- Given a terminal is connected, when user types a command, then it is sent to pi-go and response displayed
- Given a terminal is connected, when the browser window resizes, then the terminal dimensions update

### Session Management
- Given a browser tab connects, when the session is established, then a new agent session is created
- Given a session exists, when the browser disconnects, then resources are cleaned up
- Given multiple tabs are open, when each connects, then each gets its own isolated session

### Security
- Given a browser without an approved token, when they visit `/`, then they are redirected to `/pair`
- Given an invalid token, when they check status, then they get an error response

## Testing Strategy

1. **Pairing manager tests**: Test code generation, approval, expiry, lookup
2. **Session manager tests**: Test session creation, lookup, cleanup
3. **WebSocket handler tests**: Integration tests with test server
4. **E2E tests**: Full pairing → terminal flow with headless browser

## Implementation Order

1. **Pairing manager** - core logic for code generation and approval
2. **HTTP handlers** - serve HTML, API endpoints
3. **WebSocket handler** - PTY bridge
4. **Session manager** - per-tab sessions
5. **Static files** - HTML/CSS/JS with xterm.js
6. **CLI integration** - `pi serve` command
7. **Integration tests** - full flow testing
