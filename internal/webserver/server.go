package webserver

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Server is the main web server.
type ServerV2 struct {
	httpServer *http.Server
	pairingMgr *PairingManager
	sessions   *SessionManager
	ptyPool    *PtyPool
	log        *slog.Logger
	cfg        Config
	listenAddr string // actual address after binding (useful for port 0)

	mu              sync.Mutex
	activePairCode  string
	activePairToken string
	activeProject   string
}

// NewServerV2 creates a new server with the given configuration.
func NewServerV2(cfg Config) *ServerV2 {
	if cfg.PairingTimeout == 0 {
		cfg.PairingTimeout = 5 * time.Minute
	}
	if cfg.Addr == "" {
		cfg.Addr = ":8080"
	}

	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	s := &ServerV2{
		pairingMgr: NewPairingManager(cfg.PairingTimeout),
		sessions:   NewSessionManager(),
		ptyPool:    NewPtyPool(logger),
		log:        logger,
		cfg:        cfg,
	}

	mux := http.NewServeMux()
	s.setupRoutes(mux)

	s.httpServer = &http.Server{
		Addr:        cfg.Addr,
		Handler:     s.loggingMiddleware(mux),
		ReadTimeout: 30 * time.Second,
		IdleTimeout: 120 * time.Second,
	}

	return s
}

// setupRoutes configures all HTTP routes.
func (s *ServerV2) setupRoutes(mux *http.ServeMux) {
	// Pairing endpoints
	mux.HandleFunc("GET /pair", s.handlePair)
	mux.HandleFunc("POST /api/pair", s.handleCreatePair)
	mux.HandleFunc("GET /api/pair", s.handleCreatePair)
	mux.HandleFunc("POST /api/pair/submit", s.handleSubmitPairCode)
	mux.HandleFunc("GET /api/status", s.handleStatus)

	// Terminal endpoints
	mux.HandleFunc("GET /", s.handleIndex)

	// WebSocket endpoint
	mux.HandleFunc("GET /ws/", s.handleWebSocket)

	// Static assets are embedded by default and can be overridden with cfg.StaticDir.
	mux.Handle("GET /static/", http.StripPrefix("/static/", staticAssetsFileServer(s.cfg.StaticDir)))
}

// Start starts the HTTP server.
func (s *ServerV2) Start() error {
	ln, err := net.Listen("tcp", s.cfg.Addr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}

	s.listenAddr = ln.Addr().String()

	go func() {
		if err := s.httpServer.Serve(ln); err != nil && err != http.ErrServerClosed {
			s.log.Error("http server error", "err", err)
		}
	}()

	return nil
}

// Shutdown gracefully shuts down the server.
func (s *ServerV2) Shutdown(ctx context.Context) error {
	s.ptyPool.CloseAll()
	s.sessions.Close()
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

	serveStaticPage(w, r, s.cfg.StaticDir, "pair.html")
}

// handleCreatePair creates a new pairing code.
func (s *ServerV2) handleCreatePair(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	project := s.cfg.Project
	if project == "" {
		project = "."
	}

	if r.Method == http.MethodPost {
		var req struct {
			Project string `json:"project"`
		}
		if err := decodeJSON(r, &req); err != nil {
			http.Error(w, fmt.Sprintf("Invalid request: %v", err), http.StatusBadRequest)
			return
		}
		if req.Project != "" {
			project = req.Project
		}
	}

	origin, host := requestOriginAndHost(r)
	pairURL := origin + "/pair"
	resp, err := s.getOrCreateActivePair(project, host, pairURL)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to create pair: %v", err), http.StatusInternalServerError)
		return
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

// handleSubmitPairCode approves the active pair when the submitted code matches.
func (s *ServerV2) handleSubmitPairCode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	code, err := parseSubmittedCode(r)
	if err != nil {
		respondPairSubmitError(w, r, fmt.Sprintf("Invalid request: %v", err))
		return
	}
	if code == "" {
		respondPairSubmitError(w, r, "Missing pair code")
		return
	}
	s.log.Info("pair code submitted", "code", code, "remote", r.RemoteAddr)

	token, err := s.approveActivePairCode(code)
	if err != nil {
		s.log.Warn("pair code rejected", "code", code, "err", err)
		respondPairSubmitError(w, r, err.Error())
		return
	}
	s.log.Info("pair code approved", "code", code)

	http.SetCookie(w, &http.Cookie{
		Name:     "pi_token",
		Value:    token,
		Path:     "/",
		MaxAge:   3600 * 24,
		HttpOnly: true,
	})

	if isJSONRequest(r) {
		writeJSON(w, StatusResponse{Status: PairStatusApproved})
		return
	}

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func parseSubmittedCode(r *http.Request) (string, error) {
	if isJSONRequest(r) {
		var req struct {
			Code string `json:"code"`
		}
		if err := decodeJSON(r, &req); err != nil {
			return "", err
		}
		return strings.TrimSpace(req.Code), nil
	}

	if err := r.ParseForm(); err != nil {
		return "", err
	}
	return strings.TrimSpace(r.FormValue("code")), nil
}

func isJSONRequest(r *http.Request) bool {
	ct := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type")))
	accept := strings.ToLower(strings.TrimSpace(r.Header.Get("Accept")))
	return strings.Contains(ct, "application/json") || strings.Contains(accept, "application/json")
}

func respondPairSubmitError(w http.ResponseWriter, r *http.Request, message string) {
	if isJSONRequest(r) {
		http.Error(w, message, http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/pair?error="+url.QueryEscape(message), http.StatusSeeOther)
}

// handleIndex serves the main terminal page.
func (s *ServerV2) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	// Pairing disabled - serve index directly
	serveStaticPage(w, r, s.cfg.StaticDir, "index.html")
}

// handleWebSocket handles WebSocket connections for terminal.
func (s *ServerV2) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	sessionID := strings.TrimPrefix(r.URL.Path, "/ws/")
	if sessionID == "" {
		http.Error(w, "Missing session ID", http.StatusBadRequest)
		return
	}

	project := s.cfg.Project
	if project == "" {
		project = "."
	}

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

	// Get existing PTY or create a new one for this session.
	bridge, err := s.ptyPool.GetOrCreate(sessionID, project, s.cfg.Model)
	if err != nil {
		s.log.Error("pty create failed", "session", sessionID, "err", err)
		msg := WSMessage{Type: "error", Data: err.Error()}
		conn.WriteJSON(msg)
		return
	}

	s.log.Info("ws attached", "session", sessionID, "reconnect", bridge.Alive())
	bridge.AttachWebSocket(conn, sessionID)
	s.log.Info("ws detached", "session", sessionID, "pty_alive", bridge.Alive())
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

func requestOriginAndHost(r *http.Request) (origin, host string) {
	host = strings.TrimSpace(r.Header.Get("X-Forwarded-Host"))
	if host == "" {
		host = strings.TrimSpace(r.Host)
	}
	if host == "" {
		host = "pi-go"
	}

	scheme := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto"))
	if scheme == "" {
		if r.TLS != nil {
			scheme = "https"
		} else {
			scheme = "http"
		}
	}

	return scheme + "://" + host, host
}

func (s *ServerV2) getOrCreateActivePair(project, host, pairURL string) (PairResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.activePairCode != "" && s.activePairToken != "" {
		status, err := s.pairingMgr.CheckStatus(s.activePairToken)
		if err == nil && status == PairStatusPending {
			qrData, err := BuildPairQRCode(s.activePairCode, s.activePairToken, host, pairURL)
			if err != nil {
				return PairResponse{}, fmt.Errorf("building QR image: %w", err)
			}
			return PairResponse{
				Code:  s.activePairCode,
				Token: s.activePairToken,
				QR:    base64.StdEncoding.EncodeToString(qrData),
			}, nil
		}
	}

	code, token, qrData, err := s.pairingMgr.CreatePairWithContext(project, host, pairURL)
	if err != nil {
		return PairResponse{}, err
	}
	s.activePairCode = code
	s.activePairToken = token
	s.activeProject = project

	return PairResponse{
		Code:  code,
		Token: token,
		QR:    base64.StdEncoding.EncodeToString(qrData),
	}, nil
}

func (s *ServerV2) approveActivePairCode(code string) (string, error) {
	approvedToken, err := s.pairingMgr.Approve(code)
	if err != nil {
		return "", fmt.Errorf("approving pair code: %w", err)
	}

	// Keep tracked active pair aligned with the most recently approved code.
	s.mu.Lock()
	s.activePairCode = code
	s.activePairToken = approvedToken
	s.mu.Unlock()

	return approvedToken, nil
}

// BootstrapPair creates (or reuses) an active pair for CLI-first flow.
func (s *ServerV2) BootstrapPair(project string) (string, string, error) {
	if strings.TrimSpace(project) == "" {
		project = "."
	}

	resp, err := s.getOrCreateActivePair(project, "pi-go", "")
	if err != nil {
		return "", "", err
	}
	return resp.Code, resp.Token, nil
}

// Addr returns the actual listen address (reflects the bound port when using :0).
func (s *ServerV2) Addr() string {
	if s.listenAddr != "" {
		return s.listenAddr
	}
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

// responseCapture wraps http.ResponseWriter to capture the status code.
// It forwards Hijack/Flush so WebSocket upgrades work through the middleware.
type responseCapture struct {
	http.ResponseWriter
	status int
}

func (rc *responseCapture) WriteHeader(code int) {
	rc.status = code
	rc.ResponseWriter.WriteHeader(code)
}

func (rc *responseCapture) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := rc.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, fmt.Errorf("underlying ResponseWriter does not implement http.Hijacker")
}

func (rc *responseCapture) Flush() {
	if f, ok := rc.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// loggingMiddleware logs every HTTP request and highlights errors.
func (s *ServerV2) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rc := &responseCapture{ResponseWriter: w, status: http.StatusOK}

		next.ServeHTTP(rc, r)

		dur := time.Since(start)
		attrs := []slog.Attr{
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", rc.status),
			slog.Duration("dur", dur),
			slog.String("remote", r.RemoteAddr),
		}

		if rc.status >= 500 {
			s.log.LogAttrs(r.Context(), slog.LevelError, "request", attrs...)
		} else if rc.status >= 400 {
			s.log.LogAttrs(r.Context(), slog.LevelWarn, "request", attrs...)
		} else {
			s.log.LogAttrs(r.Context(), slog.LevelInfo, "request", attrs...)
		}
	})
}
