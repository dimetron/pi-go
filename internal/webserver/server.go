package webserver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// Server is the main web server.
type ServerV2 struct {
	httpServer *http.Server
	pairingMgr *PairingManager
	sessions   *SessionManager
	cfg        Config
}

// NewServerV2 creates a new server with the given configuration.
func NewServerV2(cfg Config) *ServerV2 {
	if cfg.PairingTimeout == 0 {
		cfg.PairingTimeout = 5 * time.Minute
	}
	if cfg.Addr == "" {
		cfg.Addr = ":8080"
	}
	if cfg.StaticDir == "" {
		cfg.StaticDir = "."
	}

	s := &ServerV2{
		pairingMgr: NewPairingManager(cfg.PairingTimeout),
		sessions:   NewSessionManager(),
		cfg:        cfg,
	}

	mux := http.NewServeMux()
	s.setupRoutes(mux)

	s.httpServer = &http.Server{
		Addr:         cfg.Addr,
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	return s
}

// setupRoutes configures all HTTP routes.
func (s *ServerV2) setupRoutes(mux *http.ServeMux) {
	// Pairing endpoints
	mux.HandleFunc("GET /pair", s.handlePair)
	mux.HandleFunc("POST /api/pair", s.handleCreatePair)
	mux.HandleFunc("GET /api/status", s.handleStatus)

	// Terminal endpoints
	mux.HandleFunc("GET /", s.handleIndex)

	// WebSocket endpoint
	mux.HandleFunc("GET /ws/", s.handleWebSocket)

	// Static files - serve from the static directory
	staticDir := filepath.Join(s.cfg.StaticDir, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.Dir(staticDir))))
}

// Start starts the HTTP server.
func (s *ServerV2) Start() error {
	ln, err := net.Listen("tcp", s.cfg.Addr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}

	go func() {
		if err := s.httpServer.Serve(ln); err != nil && err != http.ErrServerClosed {
			// Log error (in production, use proper logging)
			fmt.Printf("server error: %v\n", err)
		}
	}()

	return nil
}

// Shutdown gracefully shuts down the server.
func (s *ServerV2) Shutdown(ctx context.Context) error {
	// Close session manager
	s.sessions.Close()

	// Shutdown HTTP server
	return s.httpServer.Shutdown(ctx)
}

// handlePair serves the pairing page.
func (s *ServerV2) handlePair(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token != "" {
		status, _ := s.pairingMgr.CheckStatus(token)
		if status == PairStatusApproved {
			http.SetCookie(w, &http.Cookie{
				Name:     "pi_token",
				Value:    token,
				Path:     "/",
				MaxAge:   3600 * 24,
				HttpOnly: true,
			})
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
	}

	// Serve pair.html from static directory
	pairPage := filepath.Join(s.cfg.StaticDir, "static", "pair.html")
	http.ServeFile(w, r, pairPage)
}

// handleCreatePair creates a new pairing code.
func (s *ServerV2) handleCreatePair(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Project string `json:"project"`
	}
	if err := decodeJSON(r, &req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid request: %v", err), http.StatusBadRequest)
		return
	}

	if req.Project == "" {
		req.Project = "."
	}

	code, token, qrData, err := s.pairingMgr.CreatePair(req.Project)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to create pair: %v", err), http.StatusInternalServerError)
		return
	}

	resp := PairResponse{
		Code:  code,
		Token: token,
		QR:    base64.StdEncoding.EncodeToString(qrData),
	}

	writeJSON(w, resp)
}

// handleStatus checks the status of a pairing token.
func (s *ServerV2) handleStatus(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" {
		http.Error(w, "Missing token parameter", http.StatusBadRequest)
		return
	}

	status, err := s.pairingMgr.CheckStatus(token)
	if err != nil {
		if strings.Contains(err.Error(), "not found") || err.Error() == "token not found" {
			writeJSON(w, StatusResponse{Status: PairStatusUnknown})
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, StatusResponse{Status: status})
}

// handleIndex serves the main terminal page.
func (s *ServerV2) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	token := getToken(r)
	if token == "" || !s.pairingMgr.IsApproved(token) {
		http.Redirect(w, r, "/pair", http.StatusSeeOther)
		return
	}

	indexPage := filepath.Join(s.cfg.StaticDir, "static", "index.html")
	http.ServeFile(w, r, indexPage)
}

// handleWebSocket handles WebSocket connections for terminal.
func (s *ServerV2) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	sessionID := strings.TrimPrefix(r.URL.Path, "/ws/")
	if sessionID == "" {
		http.Error(w, "Missing session ID", http.StatusBadRequest)
		return
	}

	token := r.URL.Query().Get("token")
	if token == "" {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	if !s.pairingMgr.IsApproved(token) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	project, err := s.pairingMgr.GetProject(token)
	if err != nil {
		http.Error(w, "Failed to get project", http.StatusInternalServerError)
		return
	}

	// Create session
	session, err := s.sessions.CreateSession(project, token)
	if err != nil {
		http.Error(w, "Failed to create session", http.StatusInternalServerError)
		return
	}
	defer s.sessions.CloseSession(session.ID)

	// Upgrade to WebSocket
	upgrader := &websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	// Create PTY bridge
	bridge := NewPtyBridge(project)
	defer bridge.Close()

	// Handle bidirectional I/O
	bridge.HandleWebSocket(conn, sessionID)
}

// getToken extracts the token from cookie or query parameter.
func getToken(r *http.Request) string {
	if cookie, err := r.Cookie("pi_token"); err == nil {
		return cookie.Value
	}
	return r.URL.Query().Get("token")
}

// decodeJSON decodes JSON from request body.
func decodeJSON(r *http.Request, v interface{}) error {
	return json.NewDecoder(r.Body).Decode(v)
}

// writeJSON writes JSON response.
func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

// Addr returns the listen address.
func (s *ServerV2) Addr() string {
	return s.cfg.Addr
}

// PairingManager returns the pairing manager.
func (s *ServerV2) PairingManager() *PairingManager {
	return s.pairingMgr
}

// SessionManager returns the session manager.
func (s *ServerV2) SessionManager() *SessionManager {
	return s.sessions
}
