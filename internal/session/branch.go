// Package session — branch.go implements session branching.
// Branches allow creating divergent conversation paths from a fork point.
package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"google.golang.org/adk/session"
)

// BranchInfo describes a named branch within a session.
type BranchInfo struct {
	Name      string  `json:"name"`
	Head      int     `json:"head"`                // index of the last event in this branch
	Parent    *string `json:"parent"`              // parent branch name, nil for main
	ForkPoint int     `json:"forkPoint,omitempty"` // event index where this branch forked from parent
}

// branchState holds all branches for a session, persisted as branches.json.
type branchState struct {
	Active   string                `json:"active"`
	Branches map[string]BranchInfo `json:"branches"`
}

// CreateBranch creates a new branch from the current position in the session.
// The new branch starts with a copy of events up to the current head of the active branch.
func (s *FileService) CreateBranch(sessionID, appName, userID, branchName string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	sess, err := s.loadSession(sessionID, appName, userID)
	if err != nil {
		return fmt.Errorf("loading session: %w", err)
	}

	sessionDir := filepath.Join(s.baseDir, sessionID)
	bs, err := loadBranches(sessionDir)
	if err != nil {
		return err
	}

	if _, exists := bs.Branches[branchName]; exists {
		return fmt.Errorf("branch %q already exists", branchName)
	}

	// Fork point is the current head of the active branch.
	activeBranch := bs.Branches[bs.Active]
	forkPoint := activeBranch.Head // Create branch directory and copy events up to fork point.
	branchDir := filepath.Join(sessionDir, "branches", branchName)
	if err := os.MkdirAll(branchDir, 0o755); err != nil {
		return fmt.Errorf("creating branch dir: %w", err)
	}

	// Copy events up to fork point (inclusive) into the branch events file.
	branchEvents := sess.events
	if forkPoint+1 < len(branchEvents) {
		branchEvents = branchEvents[:forkPoint+1]
	}
	if err := rewriteEvents(branchDir, branchEvents); err != nil {
		return fmt.Errorf("writing branch events: %w", err)
	}

	// Register the branch.
	bs.Branches[branchName] = BranchInfo{
		Name:      branchName,
		Head:      forkPoint,
		Parent:    new(bs.Active),
		ForkPoint: forkPoint,
	}

	return saveBranches(sessionDir, bs)
}

// SwitchBranch switches the active branch for a session.
// The session's in-memory events are replaced with the branch's events.
func (s *FileService) SwitchBranch(sessionID, appName, userID, branchName string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	sess, err := s.loadSession(sessionID, appName, userID)
	if err != nil {
		return fmt.Errorf("loading session: %w", err)
	}

	sessionDir := filepath.Join(s.baseDir, sessionID)
	bs, err := loadBranches(sessionDir)
	if err != nil {
		return err
	}

	if _, exists := bs.Branches[branchName]; !exists {
		return fmt.Errorf("branch %q does not exist", branchName)
	}

	if branchName == bs.Active {
		return nil // Already on this branch.
	}

	// Save current branch's events before switching.
	if err := s.saveBranchEvents(sessionDir, bs.Active, sess); err != nil {
		return err
	}

	// Load the target branch's events.
	events, err := s.loadBranchEvents(sessionDir, branchName)
	if err != nil {
		return fmt.Errorf("loading branch events: %w", err)
	}

	// Update in-memory session.
	sess.events = events

	// Also write to main events.jsonl so ADK sees the right events.
	if err := rewriteEvents(sessionDir, events); err != nil {
		return fmt.Errorf("writing session events: %w", err)
	}

	// Update active branch.
	bs.Active = branchName
	return saveBranches(sessionDir, bs)
}

// ListBranches returns all branches for a session.
func (s *FileService) ListBranches(sessionID, appName, userID string) ([]BranchInfo, string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	sessionDir := filepath.Join(s.baseDir, sessionID)

	// Validate session exists.
	if _, err := readMeta(sessionDir); err != nil {
		return nil, "", fmt.Errorf("session %s not found", sessionID)
	}

	bs, err := loadBranches(sessionDir)
	if err != nil {
		return nil, "", err
	}

	branches := make([]BranchInfo, 0, len(bs.Branches))
	for _, b := range bs.Branches {
		branches = append(branches, b)
	}
	return branches, bs.Active, nil
}

// ActiveBranch returns the name of the currently active branch.
func (s *FileService) ActiveBranch(sessionID string) string {
	sessionDir := filepath.Join(s.baseDir, sessionID)
	bs, err := loadBranches(sessionDir)
	if err != nil {
		return "main"
	}
	return bs.Active
}

// saveBranchEvents saves the current in-memory events to the branch's events file.
func (s *FileService) saveBranchEvents(sessionDir, branchName string, sess *fileSession) error {
	branchDir := filepath.Join(sessionDir, "branches", branchName)
	if err := os.MkdirAll(branchDir, 0o755); err != nil {
		return fmt.Errorf("creating branch dir: %w", err)
	}
	return rewriteEvents(branchDir, sess.events)
}

// loadBranchEvents loads events from a branch's events file.
func (s *FileService) loadBranchEvents(sessionDir, branchName string) ([]*session.Event, error) {
	branchDir := filepath.Join(sessionDir, "branches", branchName)
	// Try branch dir first; fall back to root events.jsonl for main if no branch dir exists.
	if _, err := os.Stat(filepath.Join(branchDir, "events.jsonl")); err == nil {
		return readEvents(branchDir)
	}
	if branchName == "main" {
		return readEvents(sessionDir)
	}
	return nil, fmt.Errorf("branch %q events not found", branchName)
}

// loadBranches reads branches.json, returning a default state if it doesn't exist.
func loadBranches(sessionDir string) (*branchState, error) {
	data, err := os.ReadFile(filepath.Join(sessionDir, "branches.json"))
	if err != nil {
		if os.IsNotExist(err) {
			// Default: single "main" branch.
			return &branchState{
				Active: "main",
				Branches: map[string]BranchInfo{
					"main": {Name: "main", Head: 0, Parent: nil, ForkPoint: 0},
				},
			}, nil
		}
		return nil, fmt.Errorf("reading branches.json: %w", err)
	}
	var bs branchState
	if err := json.Unmarshal(data, &bs); err != nil {
		return nil, fmt.Errorf("unmarshaling branches.json: %w", err)
	}
	return &bs, nil
}

// saveBranches writes branches.json to disk.
func saveBranches(sessionDir string, bs *branchState) error {
	data, err := json.MarshalIndent(bs, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling branches: %w", err)
	}
	return os.WriteFile(filepath.Join(sessionDir, "branches.json"), data, 0o644)
}
