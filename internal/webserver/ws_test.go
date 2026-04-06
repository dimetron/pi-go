package webserver

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWebSocketHandler_NewWebSocketHandler(t *testing.T) {
	sm := NewSessionManager()
	defer sm.Close()

	handler := NewWebSocketHandler(sm)
	if handler == nil {
		t.Fatal("NewWebSocketHandler should not return nil")
	}
	if handler.sessionManager != sm {
		t.Error("session manager should be set")
	}
	if handler.upgrader == nil {
		t.Error("upgrader should be set")
	}
}

func TestWebSocketHandler_HandleWebSocket_MissingSessionID(t *testing.T) {
	sm := NewSessionManager()
	defer sm.Close()

	handler := NewWebSocketHandler(sm)

	req := httptest.NewRequest(http.MethodGet, "/ws/", nil)
	w := httptest.NewRecorder()

	handler.HandleWebSocket(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
}

func TestWebSocketHandler_HandleWebSocket_MissingToken(t *testing.T) {
	sm := NewSessionManager()
	defer sm.Close()

	handler := NewWebSocketHandler(sm)

	req := httptest.NewRequest(http.MethodGet, "/ws/test-session", nil)
	w := httptest.NewRecorder()

	handler.HandleWebSocket(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", w.Code)
	}
}

func TestWebSocketHandler_HandleWebSocket_InvalidUpgrade(t *testing.T) {
	sm := NewSessionManager()
	defer sm.Close()

	handler := NewWebSocketHandler(sm)

	// This should not panic even with invalid request
	req := httptest.NewRequest(http.MethodGet, "/ws/test-session?token=test-token", nil)
	w := httptest.NewRecorder()

	// Call handler - it will try to upgrade but should handle gracefully
	handler.HandleWebSocket(w, req)

	// Without a proper WebSocket upgrade, the response code will be different
	// Just verify no panic occurs
}
