package webserver

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// Server holds HTTP handlers and dependencies.
type Server struct {
	pairingManager *PairingManager
	addr           string
	staticDir      string
	mux            *http.ServeMux
}

// Config holds server configuration.
type Config struct {
	Addr           string
	PairingTimeout time.Duration
	StaticDir      string
	Project        string
	Model          string
	BaseURL        string
	Headers        []string
	Insecure       bool
	Logger         *slog.Logger // if nil, a no-op logger is used
}

// DefaultAddr is the listen address used when Config.Addr is empty. It binds
// loopback only: this server hands whoever pairs with it a terminal running the
// agent, so exposing it to the network is a decision an operator makes
// explicitly with --addr (e.g. --addr 0.0.0.0:8765), never a default. 8765 is
// picked to stay clear of the 8080/3000 range every other local dev server
// squats on, so `pi serve` does not collide with whatever the project itself
// is running.
const DefaultAddr = "127.0.0.1:8765"

// NewServer creates a new web server with the given configuration.
func NewServer(cfg Config) *Server {
	if cfg.Addr == "" {
		cfg.Addr = DefaultAddr
	}
	if cfg.StaticDir == "" {
		cfg.StaticDir = "static"
	}
	if cfg.PairingTimeout == 0 {
		cfg.PairingTimeout = 5 * time.Minute
	}

	s := &Server{
		pairingManager: NewPairingManager(cfg.PairingTimeout),
		addr:           cfg.Addr,
		staticDir:      cfg.StaticDir,
		mux:            http.NewServeMux(),
	}

	s.setupRoutes()
	return s
}

// setupRoutes registers all HTTP routes.
func (s *Server) setupRoutes() {
	// Pairing endpoints
	s.mux.HandleFunc("GET /pair", s.handlePair)
	s.mux.HandleFunc("POST /api/pair", s.handleCreatePair)
	s.mux.HandleFunc("GET /api/status", s.handleStatus)

	// Terminal endpoints (protected)
	s.mux.HandleFunc("GET /", s.handleIndex)

	// WebSocket endpoint
	s.mux.HandleFunc("WS /ws/", s.handleWebSocket)

	// Static files
	s.mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.Dir(s.staticDir))))
}

// handlePair serves the pairing page with QR code.
func (s *Server) handlePair(w http.ResponseWriter, r *http.Request) {
	// Check if there's an existing token in query
	token := r.URL.Query().Get("token")

	// If we have a token, check if already approved
	if token != "" {
		status, _ := s.pairingManager.CheckStatus(token)
		if status == PairStatusApproved {
			// Redirect to index with token cookie
			http.SetCookie(w, &http.Cookie{
				Name:     "pi_token",
				Value:    token,
				Path:     "/",
				MaxAge:   3600 * 24, // 24 hours
				HttpOnly: true,
				SameSite: http.SameSiteStrictMode,
			})
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
	}

	// Serve the pair.html page
	pairPage := filepath.Join(s.staticDir, "pair.html")
	http.ServeFile(w, r, pairPage)
}

// handleCreatePair creates a new pairing code.
func (s *Server) handleCreatePair(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse request body
	var req struct {
		Project string `json:"project"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid request: %v", err), http.StatusBadRequest)
		return
	}

	// Use default project if not provided
	if req.Project == "" {
		req.Project = "."
	}

	// Create pairing. The token is intentionally dropped: this endpoint has no
	// authentication, so the response carries only the public code and QR.
	code, _, qrData, err := s.pairingManager.CreatePair(req.Project)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to create pair: %v", err), http.StatusInternalServerError)
		return
	}

	// Encode QR data as base64 for transport
	qrBase64 := base64.StdEncoding.EncodeToString(qrData)

	resp := PairResponse{
		Code: code,
		QR:   qrBase64,
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		http.Error(w, fmt.Sprintf("Failed to encode response: %v", err), http.StatusInternalServerError)
		return
	}
}

// handleStatus checks the status of a pairing token.
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" {
		http.Error(w, "Missing token parameter", http.StatusBadRequest)
		return
	}

	status, err := s.pairingManager.CheckStatus(token)
	if err != nil {
		// Token not found - could be expired or invalid
		if strings.Contains(err.Error(), "not found") || err.Error() == "token not found" {
			resp := StatusResponse{
				Status: PairStatusUnknown,
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	resp := StatusResponse{
		Status: status,
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		http.Error(w, fmt.Sprintf("Failed to encode response: %v", err), http.StatusInternalServerError)
		return
	}
}

// handleIndex serves the main terminal page (requires auth).
func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	// Check for token cookie
	token := ""
	if cookie, err := r.Cookie("pi_token"); err == nil {
		token = cookie.Value
	}

	// If no token, try query param
	if token == "" {
		token = r.URL.Query().Get("token")
	}

	// Validate token
	if token == "" || !s.pairingManager.IsApproved(token) {
		http.Redirect(w, r, "/pair", http.StatusSeeOther)
		return
	}

	// Serve the index.html page
	indexPage := filepath.Join(s.staticDir, "index.html")
	http.ServeFile(w, r, indexPage)
}

// handleWebSocket handles WebSocket connections for terminal.
func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
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

	// Validate token
	if !s.pairingManager.IsApproved(token) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Get project for this token
	project, err := s.pairingManager.GetProject(token)
	if err != nil {
		http.Error(w, "Failed to get project", http.StatusInternalServerError)
		return
	}

	// Upgrade to WebSocket
	upgrader := &websocket.Upgrader{CheckOrigin: checkSameOrigin}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		// Connection upgrade failed
		return
	}
	defer conn.Close()

	// Create PTY bridge for this session
	bridge := NewPtyBridge(project, "", "", nil, false, nil)
	defer bridge.Close()

	// Handle bidirectional I/O
	bridge.HandleWebSocket(conn, sessionID)
}

// Mux returns the HTTP serve mux.
func (s *Server) Mux() *http.ServeMux {
	return s.mux
}

// Addr returns the listen address.
func (s *Server) Addr() string {
	return s.addr
}

// PairingManager returns the pairing manager.
func (s *Server) PairingManager() *PairingManager {
	return s.pairingManager
}

// GetProject returns the project for a given token.
func (s *Server) GetProject(token string) (string, error) {
	return s.pairingManager.GetProject(token)
}
