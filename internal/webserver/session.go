package webserver

import (
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

// WebSession represents an isolated browser tab session.
type WebSession struct {
	ID        string
	Project   string
	Token     string
	CreatedAt time.Time
	ClosedAt  time.Time
	Closed    bool
}

// SessionManager manages per-tab sessions.
type SessionManager struct {
	mu       sync.RWMutex
	sessions map[string]*WebSession
	closed   chan struct{}
}

// NewSessionManager creates a new session manager.
func NewSessionManager() *SessionManager {
	sm := &SessionManager{
		sessions: make(map[string]*WebSession),
		closed:   make(chan struct{}),
	}

	// Start cleanup goroutine
	go sm.cleanupLoop()

	return sm
}

// CreateSession creates a new session for a browser tab.
func (sm *SessionManager) CreateSession(project, token string) (*WebSession, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	// Check if token is approved
	// This should be validated by the caller

	sessionID := uuid.New().String()
	session := &WebSession{
		ID:        sessionID,
		Project:   project,
		Token:     token,
		CreatedAt: time.Now(),
	}

	sm.sessions[sessionID] = session
	return session, nil
}

// GetSession retrieves a session by ID.
func (sm *SessionManager) GetSession(sessionID string) (*WebSession, bool) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	session, ok := sm.sessions[sessionID]
	if !ok {
		return nil, false
	}

	// Check if session is closed
	if session.Closed {
		return nil, false
	}

	return session, true
}

// CloseSession marks a session as closed.
func (sm *SessionManager) CloseSession(sessionID string) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session, ok := sm.sessions[sessionID]
	if !ok {
		return fmt.Errorf("session not found")
	}

	if session.Closed {
		return fmt.Errorf("session already closed")
	}

	session.Closed = true
	session.ClosedAt = time.Now()

	// Remove from active sessions
	delete(sm.sessions, sessionID)

	return nil
}

// CleanupExpired removes expired sessions.
// Deprecated: Use cleanupLoop instead.
func (sm *SessionManager) CleanupExpired() {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	now := time.Now()
	for id, session := range sm.sessions {
		if session.Closed || now.Sub(session.CreatedAt) > 24*time.Hour {
			delete(sm.sessions, id)
		}
	}
}

// cleanupLoop periodically cleans up expired sessions.
func (sm *SessionManager) cleanupLoop() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-sm.closed:
			return
		case <-ticker.C:
			sm.CleanupExpired()
		}
	}
}

// Close stops the session manager and cleans up.
func (sm *SessionManager) Close() {
	close(sm.closed)
}

// SessionCount returns the number of active sessions.
func (sm *SessionManager) SessionCount() int {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	count := 0
	for _, session := range sm.sessions {
		if !session.Closed {
			count++
		}
	}
	return count
}
