package webserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHandler_HandleCreatePair(t *testing.T) {
	s := NewServer(Config{PairingTimeout: 5 * time.Minute, StaticDir: "."})

	reqBody := strings.NewReader(`{"project": "/tmp/test-project"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/pair", reqBody)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	s.handleCreatePair(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	// Decode into a map, not PairResponse: the point of the assertion is that
	// no "token" key reaches an unauthenticated caller, and a typed decode
	// would silently drop one if it came back.
	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	code, _ := resp["code"].(string)
	if len(code) != 6 {
		t.Errorf("expected 6-digit code, got %q", code)
	}
	if _, ok := resp["token"]; ok {
		t.Errorf("pair response leaked a token: %v", resp["token"])
	}
	if qr, _ := resp["qr"].(string); qr == "" {
		t.Error("qr should not be empty")
	}
}

func TestHandler_HandleStatus(t *testing.T) {
	s := NewServer(Config{PairingTimeout: 5 * time.Minute, StaticDir: "."})

	// Create a pair first
	code, token, _, err := s.pairingManager.CreatePair("/tmp/test-project")
	if err != nil {
		t.Fatalf("CreatePair failed: %v", err)
	}

	// Check pending status
	req := httptest.NewRequest(http.MethodGet, "/api/status?token="+token, nil)
	w := httptest.NewRecorder()
	s.handleStatus(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	var resp StatusResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Status != PairStatusPending {
		t.Errorf("expected pending, got %v", resp.Status)
	}

	// Approve the code
	_, err = s.pairingManager.Approve(code)
	if err != nil {
		t.Fatalf("Approve failed: %v", err)
	}

	// Check approved status
	req = httptest.NewRequest(http.MethodGet, "/api/status?token="+token, nil)
	w = httptest.NewRecorder()
	s.handleStatus(w, req)

	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Status != PairStatusApproved {
		t.Errorf("expected approved, got %v", resp.Status)
	}
}

func TestHandler_HandleStatus_MissingToken(t *testing.T) {
	s := NewServer(Config{PairingTimeout: 5 * time.Minute, StaticDir: "."})

	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	w := httptest.NewRecorder()
	s.handleStatus(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
}

func TestHandler_HandleStatus_UnknownToken(t *testing.T) {
	s := NewServer(Config{PairingTimeout: 5 * time.Minute, StaticDir: "."})

	req := httptest.NewRequest(http.MethodGet, "/api/status?token=unknown", nil)
	w := httptest.NewRecorder()
	s.handleStatus(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	var resp StatusResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Status != PairStatusUnknown {
		t.Errorf("expected unknown, got %v", resp.Status)
	}
}

func TestHandler_HandleIndex_RedirectWithoutToken(t *testing.T) {
	s := NewServer(Config{PairingTimeout: 5 * time.Minute, StaticDir: "."})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	s.handleIndex(w, req)

	if w.Code != http.StatusSeeOther {
		t.Errorf("expected redirect, got %d", w.Code)
	}

	location := w.Header().Get("Location")
	if location != "/pair" {
		t.Errorf("expected redirect to /pair, got %s", location)
	}
}

func TestHandler_HandleIndex_WithApprovedToken(t *testing.T) {
	s := NewServer(Config{PairingTimeout: 5 * time.Minute, StaticDir: "."})

	// Create and approve a pair
	code, token, _, err := s.pairingManager.CreatePair("/tmp/test-project")
	if err != nil {
		t.Fatalf("CreatePair failed: %v", err)
	}
	_, err = s.pairingManager.Approve(code)
	if err != nil {
		t.Fatalf("Approve failed: %v", err)
	}

	// Request with approved token cookie
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: "pi_token", Value: token})
	w := httptest.NewRecorder()
	s.handleIndex(w, req)

	// Should serve the file (404 if doesn't exist, but handler should proceed)
	if w.Code != http.StatusNotFound && w.Code != http.StatusOK {
		t.Errorf("expected OK or NotFound, got %d", w.Code)
	}
}

func TestHandler_HandleIndex_WithQueryToken(t *testing.T) {
	s := NewServer(Config{PairingTimeout: 5 * time.Minute, StaticDir: "."})

	// Create and approve a pair
	code, token, _, err := s.pairingManager.CreatePair("/tmp/test-project")
	if err != nil {
		t.Fatalf("CreatePair failed: %v", err)
	}
	_, err = s.pairingManager.Approve(code)
	if err != nil {
		t.Fatalf("Approve failed: %v", err)
	}

	// Request with token in query param
	req := httptest.NewRequest(http.MethodGet, "/?token="+token, nil)
	w := httptest.NewRecorder()
	s.handleIndex(w, req)

	// Should serve the file
	if w.Code != http.StatusNotFound && w.Code != http.StatusOK {
		t.Errorf("expected OK or NotFound, got %d", w.Code)
	}
}
