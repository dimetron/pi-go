// Package session provides a file-based session.Service implementation
// that persists sessions as JSONL files on disk.
package session

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"google.golang.org/adk/session"
	"google.golang.org/genai"
)

// Meta holds session metadata persisted in meta.json.
type Meta struct {
	ID        string    `json:"id"`
	AppName   string    `json:"appName"`
	UserID    string    `json:"userID"`
	WorkDir   string    `json:"workDir,omitempty"`
	Model     string    `json:"model,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// FileService implements session.Service with file-based JSONL persistence.
// Sessions are stored in baseDir/<session-id>/ with meta.json and events.jsonl.
type FileService struct {
	baseDir string
	mu      sync.RWMutex
	// In-memory cache of sessions for fast access during a run.
	sessions map[string]*fileSession
}

// NewFileService creates a new file-based session service.
// baseDir is the directory where sessions are stored (e.g., ~/.pi-go/sessions).
func NewFileService(baseDir string) (*FileService, error) {
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating sessions dir: %w", err)
	}
	return &FileService{
		baseDir:  baseDir,
		sessions: make(map[string]*fileSession),
	}, nil
}

func (s *FileService) Create(_ context.Context, req *session.CreateRequest) (*session.CreateResponse, error) {
	if req.AppName == "" || req.UserID == "" {
		return nil, fmt.Errorf("app_name and user_id are required")
	}

	sessionID := req.SessionID
	if sessionID == "" {
		sessionID = uuid.NewString()
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Check if session already exists on disk or in cache.
	sessionDir := filepath.Join(s.baseDir, sessionID)
	if _, err := os.Stat(filepath.Join(sessionDir, "meta.json")); err == nil {
		return nil, fmt.Errorf("session %s already exists", sessionID)
	}

	// Create session directory and meta file.
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating session dir: %w", err)
	}

	now := time.Now()
	cwd, _ := os.Getwd()
	meta := Meta{
		ID:        sessionID,
		AppName:   req.AppName,
		UserID:    req.UserID,
		WorkDir:   cwd,
		CreatedAt: now,
		UpdatedAt: now,
	}

	if err := writeMeta(sessionDir, &meta); err != nil {
		return nil, err
	}

	// Create empty events file.
	eventsFile := filepath.Join(sessionDir, "events.jsonl")
	if err := os.WriteFile(eventsFile, nil, 0o644); err != nil {
		return nil, fmt.Errorf("creating events file: %w", err)
	}

	state := req.State
	if state == nil {
		state = make(map[string]any)
	}

	sess := &fileSession{
		meta:      meta,
		events:    nil,
		state:     state,
		updatedAt: now,
	}
	s.sessions[sessionID] = sess

	return &session.CreateResponse{
		Session: sess.snapshot(),
	}, nil
}

func (s *FileService) Get(_ context.Context, req *session.GetRequest) (*session.GetResponse, error) {
	if req.AppName == "" || req.UserID == "" || req.SessionID == "" {
		return nil, fmt.Errorf("app_name, user_id, session_id are required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	sess, err := s.loadSession(req.SessionID, req.AppName, req.UserID)
	if err != nil {
		return nil, err
	}

	snap := sess.snapshot()

	// Apply event filters.
	filtered := snap.events
	if req.NumRecentEvents > 0 {
		start := max(len(filtered)-req.NumRecentEvents, 0)
		filtered = filtered[start:]
	}
	if !req.After.IsZero() && len(filtered) > 0 {
		firstIdx := sort.Search(len(filtered), func(i int) bool {
			return !filtered[i].Timestamp.Before(req.After)
		})
		filtered = filtered[firstIdx:]
	}
	snap.events = filtered

	return &session.GetResponse{
		Session: snap,
	}, nil
}

func (s *FileService) List(_ context.Context, req *session.ListRequest) (*session.ListResponse, error) {
	if req.AppName == "" {
		return nil, fmt.Errorf("app_name is required")
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	entries, err := os.ReadDir(s.baseDir)
	if err != nil {
		return nil, fmt.Errorf("reading sessions dir: %w", err)
	}

	var sessions []session.Session
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		sessionDir := filepath.Join(s.baseDir, entry.Name())
		meta, err := readMeta(sessionDir)
		if err != nil {
			continue // Skip invalid sessions.
		}
		if meta.AppName != req.AppName {
			continue
		}
		if req.UserID != "" && meta.UserID != req.UserID {
			continue
		}
		// Return lightweight session without events.
		snap := &sessionSnapshot{
			id:        meta.ID,
			appName:   meta.AppName,
			userID:    meta.UserID,
			state:     make(map[string]any),
			events:    nil,
			updatedAt: meta.UpdatedAt,
		}
		sessions = append(sessions, snap)
	}

	return &session.ListResponse{
		Sessions: sessions,
	}, nil
}

func (s *FileService) Delete(_ context.Context, req *session.DeleteRequest) error {
	if req.AppName == "" || req.UserID == "" || req.SessionID == "" {
		return fmt.Errorf("app_name, user_id, session_id are required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.sessions, req.SessionID)

	sessionDir := filepath.Join(s.baseDir, req.SessionID)
	if err := os.RemoveAll(sessionDir); err != nil {
		return fmt.Errorf("deleting session dir: %w", err)
	}
	return nil
}

func (s *FileService) AppendEvent(_ context.Context, curSession session.Session, event *session.Event) error {
	if curSession == nil {
		return fmt.Errorf("session is nil")
	}
	if event == nil {
		return fmt.Errorf("event is nil")
	}
	if event.Partial {
		return nil
	}

	sessionID := curSession.ID()

	s.mu.Lock()
	defer s.mu.Unlock()

	sess, ok := s.sessions[sessionID]
	if !ok {
		return fmt.Errorf("session %s not found in cache", sessionID)
	}

	// Strip temp state keys from delta.
	if len(event.Actions.StateDelta) > 0 {
		filtered := make(map[string]any)
		for k, v := range event.Actions.StateDelta {
			if !strings.HasPrefix(k, session.KeyPrefixTemp) {
				filtered[k] = v
			}
		}
		event.Actions.StateDelta = filtered
	}

	// Update in-memory state.
	if event.Actions.StateDelta != nil {
		maps.Copy(sess.state, event.Actions.StateDelta)
	}

	sess.events = append(sess.events, event)
	sess.updatedAt = event.Timestamp
	sess.meta.UpdatedAt = event.Timestamp

	// Persist: append event to JSONL file.
	sessionDir := filepath.Join(s.baseDir, sessionID)
	if err := appendEventToFile(sessionDir, event); err != nil {
		return fmt.Errorf("persisting event: %w", err)
	}

	// Update meta.json with new timestamp.
	if err := writeMeta(sessionDir, &sess.meta); err != nil {
		return fmt.Errorf("updating meta: %w", err)
	}

	// Update branch head pointer.
	bs, err := loadBranches(sessionDir)
	if err == nil {
		if branch, ok := bs.Branches[bs.Active]; ok {
			branch.Head = len(sess.events) - 1
			bs.Branches[bs.Active] = branch
			saveBranches(sessionDir, bs) // best-effort
		}
	}

	return nil
}

// loadSession loads a session from disk or cache.
func (s *FileService) loadSession(sessionID, appName, userID string) (*fileSession, error) {
	if sess, ok := s.sessions[sessionID]; ok {
		return sess, nil
	}

	sessionDir := filepath.Join(s.baseDir, sessionID)
	meta, err := readMeta(sessionDir)
	if err != nil {
		return nil, fmt.Errorf("session %s not found", sessionID)
	}
	if meta.AppName != appName || meta.UserID != userID {
		return nil, fmt.Errorf("session %s not found for app=%s user=%s", sessionID, appName, userID)
	}

	events, err := readEvents(sessionDir)
	if err != nil {
		return nil, fmt.Errorf("reading events: %w", err)
	}

	sess := &fileSession{
		meta:      *meta,
		events:    events,
		state:     make(map[string]any),
		updatedAt: meta.UpdatedAt,
	}

	// Rebuild state from event deltas.
	for _, e := range events {
		if e.Actions.StateDelta != nil {
			maps.Copy(sess.state, e.Actions.StateDelta)
		}
	}

	s.sessions[sessionID] = sess
	return sess, nil
}

// LastSessionID returns the most recently updated session ID, or "" if none.
func (s *FileService) LastSessionID(appName, userID string) string {
	entries, err := os.ReadDir(s.baseDir)
	if err != nil {
		return ""
	}

	var latest string
	var latestTime time.Time

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		sessionDir := filepath.Join(s.baseDir, entry.Name())
		meta, err := readMeta(sessionDir)
		if err != nil {
			continue
		}
		if meta.AppName != appName || meta.UserID != userID {
			continue
		}
		if meta.UpdatedAt.After(latestTime) {
			latestTime = meta.UpdatedAt
			latest = meta.ID
		}
	}
	return latest
}

// fileSession holds session data in memory, backed by disk.
type fileSession struct {
	meta      Meta
	events    []*session.Event
	state     map[string]any
	updatedAt time.Time
}

func (s *fileSession) snapshot() *sessionSnapshot {
	eventsCopy := make([]*session.Event, len(s.events))
	copy(eventsCopy, s.events)
	return &sessionSnapshot{
		id:        s.meta.ID,
		appName:   s.meta.AppName,
		userID:    s.meta.UserID,
		state:     maps.Clone(s.state),
		events:    eventsCopy,
		updatedAt: s.updatedAt,
	}
}

// sessionSnapshot implements session.Session for returning to callers.
type sessionSnapshot struct {
	id        string
	appName   string
	userID    string
	state     map[string]any
	events    []*session.Event
	updatedAt time.Time
	mu        sync.RWMutex
}

func (s *sessionSnapshot) ID() string                { return s.id }
func (s *sessionSnapshot) AppName() string           { return s.appName }
func (s *sessionSnapshot) UserID() string            { return s.userID }
func (s *sessionSnapshot) LastUpdateTime() time.Time { return s.updatedAt }

func (s *sessionSnapshot) State() session.State {
	return &snapshotState{mu: &s.mu, state: s.state}
}

func (s *sessionSnapshot) Events() session.Events {
	return eventList(s.events)
}

// snapshotState implements session.State.
type snapshotState struct {
	mu    *sync.RWMutex
	state map[string]any
}

func (s *snapshotState) Get(key string) (any, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	val, ok := s.state[key]
	if !ok {
		return nil, session.ErrStateKeyNotExist
	}
	return val, nil
}

func (s *snapshotState) Set(key string, value any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state[key] = value
	return nil
}

func (s *snapshotState) All() iter.Seq2[string, any] {
	s.mu.RLock()
	stateCopy := maps.Clone(s.state)
	s.mu.RUnlock()
	return func(yield func(string, any) bool) {
		for k, v := range stateCopy {
			if !yield(k, v) {
				return
			}
		}
	}
}

// eventList implements session.Events.
type eventList []*session.Event

func (e eventList) All() iter.Seq[*session.Event] {
	return func(yield func(*session.Event) bool) {
		for _, event := range e {
			if !yield(event) {
				return
			}
		}
	}
}

func (e eventList) Len() int { return len(e) }

func (e eventList) At(i int) *session.Event {
	if i >= 0 && i < len(e) {
		return e[i]
	}
	return nil
}

// File I/O helpers.

func writeMeta(sessionDir string, meta *Meta) error {
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling meta: %w", err)
	}
	return os.WriteFile(filepath.Join(sessionDir, "meta.json"), data, 0o644)
}

func readMeta(sessionDir string) (*Meta, error) {
	data, err := os.ReadFile(filepath.Join(sessionDir, "meta.json"))
	if err != nil {
		return nil, err
	}
	var meta Meta
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, fmt.Errorf("unmarshaling meta: %w", err)
	}
	return &meta, nil
}

func appendEventToFile(sessionDir string, event *session.Event) error {
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshaling event: %w", err)
	}
	data = append(data, '\n')

	f, err := os.OpenFile(filepath.Join(sessionDir, "events.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("opening events file: %w", err)
	}
	defer f.Close()

	_, err = f.Write(data)
	return err
}

func readEvents(sessionDir string) ([]*session.Event, error) {
	data, err := os.ReadFile(filepath.Join(sessionDir, "events.jsonl"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	if len(data) == 0 {
		return nil, nil
	}

	var events []*session.Event
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var event session.Event
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			return nil, fmt.Errorf("unmarshaling event: %w", err)
		}
		events = append(events, &event)
	}
	return events, nil
}

// Summarizer is a function that takes a slice of events to be summarized
// and returns a summary text. This is typically backed by an LLM call.
type Summarizer func(events []*session.Event) (string, error)

// SimpleSummarizer is a basic summarizer that returns a placeholder summary.
// Useful for manual /compact invocations where no LLM is needed.
var SimpleSummarizer Summarizer = func(events []*session.Event) (string, error) {
	return fmt.Sprintf("[Compacted %d events]", len(events)), nil
}

// CompactConfig controls when and how compaction runs.
type CompactConfig struct {
	// MaxTokens is the approximate token threshold that triggers compaction.
	// Default: 100000.
	MaxTokens int

	// KeepRecent is the number of recent events to keep uncompacted.
	// Default: 10.
	KeepRecent int
}

// DefaultCompactConfig returns sensible default compaction settings.
func DefaultCompactConfig() CompactConfig {
	return CompactConfig{
		MaxTokens:  100000,
		KeepRecent: 10,
	}
}

// Compact checks if the session's events exceed the token threshold and,
// if so, summarizes older events using the provided summarizer function.
// The older events are replaced with a single summary event while recent
// events are preserved. The events file on disk is rewritten.
func (s *FileService) Compact(sessionID, appName, userID string, summarizer Summarizer, cfg CompactConfig) error {
	if summarizer == nil {
		return fmt.Errorf("summarizer is required")
	}
	if cfg.MaxTokens <= 0 {
		cfg.MaxTokens = 100000
	}
	if cfg.KeepRecent <= 0 {
		cfg.KeepRecent = 10
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	sess, err := s.loadSession(sessionID, appName, userID)
	if err != nil {
		return fmt.Errorf("loading session for compaction: %w", err)
	}

	totalTokens := estimateEventTokens(sess.events)
	if totalTokens <= cfg.MaxTokens {
		return nil // No compaction needed.
	}

	// Determine split point: compact events before keepRecent boundary.
	keepRecent := cfg.KeepRecent
	if keepRecent >= len(sess.events) {
		return nil // Not enough events to compact.
	}

	splitIdx := len(sess.events) - keepRecent
	toCompact := sess.events[:splitIdx]
	toKeep := sess.events[splitIdx:]

	// Call the summarizer.
	summary, err := summarizer(toCompact)
	if err != nil {
		return fmt.Errorf("summarizing events: %w", err)
	}

	// Create a summary event to replace the compacted events.
	summaryEvent := &session.Event{
		ID:        "compaction-summary",
		Timestamp: time.Now(),
		Author:    "system",
	}
	summaryEvent.Content = genai.NewContentFromText(
		fmt.Sprintf("[Session Summary]\n%s", summary),
		genai.RoleUser,
	)

	// Replace events: summary + recent events.
	newEvents := make([]*session.Event, 0, 1+len(toKeep))
	newEvents = append(newEvents, summaryEvent)
	newEvents = append(newEvents, toKeep...)

	sess.events = newEvents

	// Rewrite events file on disk.
	sessionDir := filepath.Join(s.baseDir, sessionID)
	if err := rewriteEvents(sessionDir, newEvents); err != nil {
		return fmt.Errorf("rewriting events after compaction: %w", err)
	}

	return nil
}

// EstimateTokens returns an approximate token count for a session's events.
// Uses a simple chars/4 heuristic.
func (s *FileService) EstimateTokens(sessionID, appName, userID string) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	sess, ok := s.sessions[sessionID]
	if !ok {
		return 0, fmt.Errorf("session %s not found in cache", sessionID)
	}
	_ = appName
	_ = userID
	return estimateEventTokens(sess.events), nil
}

// estimateEventTokens returns approximate token count for events using chars/4 heuristic.
func estimateEventTokens(events []*session.Event) int {
	total := 0
	for _, ev := range events {
		if ev.Content == nil {
			continue
		}
		for _, part := range ev.Content.Parts {
			if part.Text != "" {
				total += len(part.Text) / 4
			}
			if part.FunctionCall != nil {
				// Rough estimate for function call args.
				data, _ := json.Marshal(part.FunctionCall.Args)
				total += (len(part.FunctionCall.Name) + len(data)) / 4
			}
			if part.FunctionResponse != nil {
				data, _ := json.Marshal(part.FunctionResponse.Response)
				total += (len(part.FunctionResponse.Name) + len(data)) / 4
			}
		}
	}
	return total
}

// rewriteEvents overwrites the events.jsonl file with the given events.
func rewriteEvents(sessionDir string, events []*session.Event) error {
	eventsFile := filepath.Join(sessionDir, "events.jsonl")

	// Write to a temp file first, then rename for atomicity.
	tmpFile := eventsFile + ".tmp"
	f, err := os.Create(tmpFile)
	if err != nil {
		return fmt.Errorf("creating temp events file: %w", err)
	}

	enc := json.NewEncoder(f)
	for _, event := range events {
		if err := enc.Encode(event); err != nil {
			f.Close()
			os.Remove(tmpFile)
			return fmt.Errorf("encoding event: %w", err)
		}
	}

	if err := f.Close(); err != nil {
		os.Remove(tmpFile)
		return fmt.Errorf("closing temp events file: %w", err)
	}

	if err := os.Rename(tmpFile, eventsFile); err != nil {
		os.Remove(tmpFile)
		return fmt.Errorf("renaming temp events file: %w", err)
	}

	return nil
}

// Ensure FileService implements session.Service at compile time.
var _ session.Service = (*FileService)(nil)
