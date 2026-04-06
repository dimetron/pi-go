package webserver

import (
	"net/http"
	"strings"

	"github.com/gorilla/websocket"
)

// WebSocketHandler handles WebSocket connections for terminal.
type WebSocketHandler struct {
	sessionManager *SessionManager
	upgrader       *websocket.Upgrader
}

// NewWebSocketHandler creates a new WebSocket handler.
func NewWebSocketHandler(sessionManager *SessionManager) *WebSocketHandler {
	return &WebSocketHandler{
		sessionManager: sessionManager,
		upgrader: &websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				return true // Allow all origins for development
			},
		},
	}
}

// HandleWebSocket handles a WebSocket connection.
func (wh *WebSocketHandler) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	// Extract session ID from path
	sessionID := strings.TrimPrefix(r.URL.Path, "/ws/")
	if sessionID == "" {
		http.Error(w, "Missing session ID", http.StatusBadRequest)
		return
	}

	// Token is passed as query parameter for WebSocket auth
	token := r.URL.Query().Get("token")
	if token == "" {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Create session (validation of token should be done by caller)
	session, err := wh.sessionManager.CreateSession("", token)
	if err != nil {
		http.Error(w, "Failed to create session", http.StatusInternalServerError)
		return
	}

	// Upgrade to WebSocket
	conn, err := wh.upgrader.Upgrade(w, r, nil)
	if err != nil {
		wh.sessionManager.CloseSession(session.ID)
		return
	}
	defer conn.Close()
	defer wh.sessionManager.CloseSession(session.ID)

	// Create PTY bridge
	bridge := NewPtyBridge(session.Project)
	defer bridge.Close()

	// Handle bidirectional I/O
	bridge.HandleWebSocket(conn, sessionID)
}
