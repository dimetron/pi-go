package webserver

import (
	"testing"
	"time"
)

func TestSessionManager_CreateSession(t *testing.T) {
	sm := NewSessionManager()
	defer sm.Close()

	session, err := sm.CreateSession("/tmp/test-project", "test-token")
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}

	if session.ID == "" {
		t.Error("session ID should not be empty")
	}
	if session.Project != "/tmp/test-project" {
		t.Errorf("expected project /tmp/test-project, got %q", session.Project)
	}
	if session.Token != "test-token" {
		t.Errorf("expected token test-token, got %q", session.Token)
	}
	if session.Closed {
		t.Error("session should not be closed")
	}
}

func TestSessionManager_GetSession(t *testing.T) {
	sm := NewSessionManager()
	defer sm.Close()

	// Create a session
	session, err := sm.CreateSession("/tmp/test-project", "test-token")
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}

	// Get the session
	retrieved, ok := sm.GetSession(session.ID)
	if !ok {
		t.Error("GetSession should return true for existing session")
	}
	if retrieved.ID != session.ID {
		t.Errorf("expected session ID %q, got %q", session.ID, retrieved.ID)
	}

	// Get non-existent session
	_, ok = sm.GetSession("non-existent")
	if ok {
		t.Error("GetSession should return false for non-existent session")
	}
}

func TestSessionManager_GetSession_Closed(t *testing.T) {
	sm := NewSessionManager()
	defer sm.Close()

	// Create and close a session
	session, err := sm.CreateSession("/tmp/test-project", "test-token")
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}

	err = sm.CloseSession(session.ID)
	if err != nil {
		t.Fatalf("CloseSession failed: %v", err)
	}

	// Get closed session
	_, ok := sm.GetSession(session.ID)
	if ok {
		t.Error("GetSession should return false for closed session")
	}
}

func TestSessionManager_CloseSession(t *testing.T) {
	sm := NewSessionManager()
	defer sm.Close()

	// Create a session
	session, err := sm.CreateSession("/tmp/test-project", "test-token")
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}

	// Close the session
	err = sm.CloseSession(session.ID)
	if err != nil {
		t.Fatalf("CloseSession failed: %v", err)
	}

	// Verify session is closed
	if !session.Closed {
		t.Error("session should be marked as closed")
	}
	if session.ClosedAt.IsZero() {
		t.Error("session ClosedAt should be set")
	}

	// Closing again should fail
	err = sm.CloseSession(session.ID)
	if err == nil {
		t.Error("CloseSession should fail for already closed session")
	}
}

func TestSessionManager_CloseSession_NotFound(t *testing.T) {
	sm := NewSessionManager()
	defer sm.Close()

	err := sm.CloseSession("non-existent")
	if err == nil {
		t.Error("CloseSession should fail for non-existent session")
	}
}

func TestSessionManager_SessionCount(t *testing.T) {
	sm := NewSessionManager()
	defer sm.Close()

	// Initially empty
	if count := sm.SessionCount(); count != 0 {
		t.Errorf("expected 0 sessions, got %d", count)
	}

	// Create sessions
	session1, _ := sm.CreateSession("/tmp/project1", "token1")
	session2, _ := sm.CreateSession("/tmp/project2", "token2")

	if count := sm.SessionCount(); count != 2 {
		t.Errorf("expected 2 sessions, got %d", count)
	}

	// Close one
	sm.CloseSession(session1.ID)

	if count := sm.SessionCount(); count != 1 {
		t.Errorf("expected 1 session, got %d", count)
	}

	// Close the other
	sm.CloseSession(session2.ID)

	if count := sm.SessionCount(); count != 0 {
		t.Errorf("expected 0 sessions, got %d", count)
	}
}

func TestSessionManager_MultipleSessions(t *testing.T) {
	sm := NewSessionManager()
	defer sm.Close()

	// Create multiple sessions
	sessions := make([]*WebSession, 5)
	for i := 0; i < 5; i++ {
		session, err := sm.CreateSession("/tmp/project", "token")
		if err != nil {
			t.Fatalf("CreateSession %d failed: %v", i, err)
		}
		sessions[i] = session
	}

	if count := sm.SessionCount(); count != 5 {
		t.Errorf("expected 5 sessions, got %d", count)
	}

	// Verify each session is retrievable
	for i, session := range sessions {
		retrieved, ok := sm.GetSession(session.ID)
		if !ok {
			t.Errorf("session %d should be retrievable", i)
		}
		if retrieved.ID != session.ID {
			t.Errorf("session %d ID mismatch", i)
		}
	}

	// Close all
	for _, session := range sessions {
		sm.CloseSession(session.ID)
	}

	if count := sm.SessionCount(); count != 0 {
		t.Errorf("expected 0 sessions after closing all, got %d", count)
	}
}

func TestWebSession_Fields(t *testing.T) {
	sm := NewSessionManager()
	defer sm.Close()

	project := "/tmp/test-project"
	token := "test-token-123"

	session, err := sm.CreateSession(project, token)
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}

	// Check CreatedAt is recent
	if time.Since(session.CreatedAt) > time.Second {
		t.Error("CreatedAt should be recent")
	}

	// Check ClosedAt is zero
	if !session.ClosedAt.IsZero() {
		t.Error("ClosedAt should be zero for new session")
	}
}
