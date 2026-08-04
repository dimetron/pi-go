// Package session provides a file-based session.Service implementation
// that persists sessions as JSONL files on disk.
package session

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"iter"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"

	"github.com/dimetron/pi-go/internal/atif"
)

// PlanContext holds the /plan session context for resume.
type PlanContext struct {
	TaskName  string `json:"taskName,omitempty"`
	RoughIdea string `json:"roughIdea,omitempty"`
	SpecDir   string `json:"specDir,omitempty"`
	Phase     string `json:"phase,omitempty"`
}

// UnknownModel is the placeholder written to meta.Model when the caller did
// not supply one. meta.json always carries a non-empty "model" field so log
// consumers (CLI status, /sessions list, the ATIF trajectory) never have to
// branch on absence.
const UnknownModel = "unknown"

// MaxSessionTitle caps the persisted session title. Anything longer is
// truncated so meta.json stays small and terminal titles (which use this value
// via OSC 0) fit on a single line.
const MaxSessionTitle = 200

// Meta holds session metadata persisted in meta.json.
type Meta struct {
	ID          string       `json:"id"`
	AppName     string       `json:"appName"`
	UserID      string       `json:"userID"`
	WorkDir     string       `json:"workDir,omitempty"`
	Model       string       `json:"model"`
	Title       string       `json:"title,omitempty"`
	CreatedAt   time.Time    `json:"createdAt"`
	UpdatedAt   time.Time    `json:"updatedAt"`
	PlanContext *PlanContext `json:"planContext,omitempty"`
}

// maxCachedSessions is the maximum number of sessions kept in the in-memory
// cache. When exceeded, the least recently updated sessions are evicted; they
// can be reloaded from disk on demand via loadSession.
const maxCachedSessions = 20

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

// GenerateSessionID creates a time-sortable session ID.
// Format: yymmdd-hhmm-xxxxx-xxxxx (23 chars total).
// The yymmdd-hhmm prefix makes IDs naturally sortable by creation time.
func GenerateSessionID() string {
	now := time.Now()
	prefix := fmt.Sprintf("%02d%02d%02d-%02d%02d",
		now.Year()%100, // 2-digit year
		now.Month(),
		now.Day(),
		now.Hour(),
		now.Minute(),
	)
	b := make([]byte, 5) // 5 bytes = 10 hex chars, split into 5+5
	if _, err := rand.Read(b); err != nil {
		// Fallback when crypto/rand fails: encode second + nanosecond
		// so repeated calls within the same minute still yield unique IDs.
		sec := now.Second()
		nano := now.Nanosecond()
		return fmt.Sprintf("%s-%05d-%05d", prefix, sec*1000+nano/1_000_000, nano%100_000)
	}
	hexStr := hex.EncodeToString(b)
	return prefix + "-" + hexStr[:5] + "-" + hexStr[5:]
}

func (s *FileService) Create(_ context.Context, req *session.CreateRequest) (*session.CreateResponse, error) {
	if req.AppName == "" || req.UserID == "" {
		return nil, fmt.Errorf("app_name and user_id are required")
	}

	sessionID := req.SessionID
	if sessionID == "" {
		sessionID = GenerateSessionID()
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
		Model:     UnknownModel,
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
		atifWriter: atif.NewWriter(
			filepath.Join(sessionDir, "trajectory.atif.json"),
			atif.SessionMeta{
				SessionID: sessionID,
				AgentName: meta.AppName,
				Model:     meta.Model,
				WorkDir:   meta.WorkDir,
			},
		),
	}
	s.sessions[sessionID] = sess
	s.evictCachedSessionsLocked()

	return &session.CreateResponse{
		Session: sess.live(),
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

	live := sess.live()

	// If no filters requested, return the live session directly.
	// The live session reflects events appended after Get() returns,
	// which is required by the ADK runner's ContentsRequestProcessor.
	if req.NumRecentEvents == 0 && req.After.IsZero() {
		return &session.GetResponse{Session: live}, nil
	}

	// Apply event filters — return a filtered snapshot (not live).
	sess.mu.RLock()
	filtered := make([]*session.Event, len(sess.events))
	copy(filtered, sess.events)
	sess.mu.RUnlock()

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

	return &session.GetResponse{
		Session: &filteredSession{
			fs:     sess,
			events: filtered,
		},
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
		lightFS := &fileSession{
			meta:      *meta,
			events:    nil,
			state:     make(map[string]any),
			updatedAt: meta.UpdatedAt,
		}
		sessions = append(sessions, lightFS.live())
	}

	return &session.ListResponse{
		Sessions: sessions,
	}, nil
}

// Archive moves the session directory under baseDir to archiveDir/yyyy/mm/dd/
// using the current time. It removes the session from the in-memory cache.
func (s *FileService) Archive(_ context.Context, req *session.DeleteRequest) error {
	if req.AppName == "" || req.UserID == "" || req.SessionID == "" {
		return fmt.Errorf("app_name, user_id, session_id are required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.sessions, req.SessionID)

	srcDir := filepath.Join(s.baseDir, req.SessionID)
	if _, err := os.Stat(srcDir); os.IsNotExist(err) {
		return nil // Already gone, nothing to archive
	}

	// Build archive path: archiveDir/YYYY/MM/DD/<sessionID>/
	now := time.Now()
	archiveDir := filepath.Join(s.baseDir, "archive", fmt.Sprintf("%04d", now.Year()),
		fmt.Sprintf("%02d", now.Month()), fmt.Sprintf("%02d", now.Day()))
	dstDir := filepath.Join(archiveDir, req.SessionID)

	// Create archive directory structure.
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return fmt.Errorf("creating archive dir: %w", err)
	}

	// Move session files to archive.
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return fmt.Errorf("reading session dir: %w", err)
	}
	for _, entry := range entries {
		src := filepath.Join(srcDir, entry.Name())
		dst := filepath.Join(dstDir, entry.Name())
		if err := os.Rename(src, dst); err != nil {
			return fmt.Errorf("moving %s to archive: %w", entry.Name(), err)
		}
	}

	// Remove now-empty session source directory.
	if err := os.Remove(srcDir); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing empty session dir: %w", err)
	}

	return nil
}

// Delete archives the session (moves to archive/yyyy/mm/dd/) instead of deleting.
func (s *FileService) Delete(ctx context.Context, req *session.DeleteRequest) error {
	return s.Archive(ctx, req)
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

	// Write ATIF trajectory (non-fatal).
	if sess.atifWriter != nil {
		if atifErr := sess.atifWriter.AppendEvent(event); atifErr != nil {
			slog.Warn("atif: failed to append event", "session", sessionID, "error", atifErr)
		}
		// Link subagent trajectories if this event contains subagent tool responses.
		sess.atifWriter.LinkSubagentTrajectories(event, sessionDir, s.baseDir)
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
			_ = saveBranches(sessionDir, bs) // best-effort
		}
	}

	return nil
}

// evictCachedSessionsLocked removes the least recently updated sessions from
// the in-memory cache when it exceeds maxCachedSessions. Caller must hold s.mu.
func (s *FileService) evictCachedSessionsLocked() {
	if len(s.sessions) <= maxCachedSessions {
		return
	}
	// Build a list of (id, updatedAt) and sort ascending (oldest first).
	type entry struct {
		id string
		ts time.Time
	}
	entries := make([]entry, 0, len(s.sessions))
	for id, sess := range s.sessions {
		entries = append(entries, entry{id, sess.updatedAt})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].ts.Before(entries[j].ts)
	})
	toRemove := len(s.sessions) - maxCachedSessions
	for i := 0; i < toRemove && i < len(entries); i++ {
		delete(s.sessions, entries[i].id)
	}
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

	return s.loadSessionFromDisk(sessionID, meta)
}

// loadSessionUnchecked is like loadSession but skips the app/user identity
// check. Use this for metadata-only operations (SetSessionTitle, GetSessionTitle)
// where the caller doesn't know which app/user created the session. Caller
// must hold s.mu.
func (s *FileService) loadSessionUnchecked(sessionID string) (*fileSession, error) {
	if sess, ok := s.sessions[sessionID]; ok {
		return sess, nil
	}
	sessionDir := filepath.Join(s.baseDir, sessionID)
	meta, err := readMeta(sessionDir)
	if err != nil {
		return nil, fmt.Errorf("session %s not found", sessionID)
	}
	// Skip app/user verification — for SetSessionTitle/GetSessionTitle the
	// session ID is the only key we need.
	return s.loadSessionFromDisk(sessionID, meta)
}

// loadSessionFromDisk builds a fileSession in the cache from already-read
// meta. Used by loadSession and loadSessionUnchecked so they share the event
// replay + ATIF writer setup. Caller must hold s.mu.
func (s *FileService) loadSessionFromDisk(sessionID string, meta *Meta) (*fileSession, error) {
	sessionDir := filepath.Join(s.baseDir, sessionID)
	events, err := readEvents(sessionDir)
	if err != nil {
		return nil, fmt.Errorf("reading events: %w", err)
	}
	sess := &fileSession{
		meta:      *meta,
		events:    events,
		state:     make(map[string]any),
		updatedAt: meta.UpdatedAt,
		atifWriter: atif.NewWriter(
			filepath.Join(sessionDir, "trajectory.atif.json"),
			atif.SessionMeta{
				SessionID: sessionID,
				AgentName: meta.AppName,
				Model:     meta.Model,
				WorkDir:   meta.WorkDir,
			},
		),
	}
	// Restore the in-memory state by replaying each event's StateDelta, then
	// rebuild the ATIF trajectory in one shot. Both passes used to be inside a
	// single per-event loop; the state merge stays per-event (it mutates
	// sess.state), but the ATIF write is now batched via AppendEvents to avoid
	// O(n) full-file rewrites under s.mu on a cache miss.
	for _, e := range events {
		if e.Actions.StateDelta != nil {
			maps.Copy(sess.state, e.Actions.StateDelta)
		}
	}
	if sess.atifWriter != nil {
		if err := sess.atifWriter.AppendEvents(events); err != nil {
			slog.Warn("atif: failed to rebuild trajectory on load", "session", sessionID, "error", err)
		}
	}
	s.sessions[sessionID] = sess
	s.evictCachedSessionsLocked()
	return sess, nil
}

// ATIFWriter returns the ATIF writer for the given session, or nil if not found.
func (s *FileService) ATIFWriter(sessionID string) *atif.Writer {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if sess, ok := s.sessions[sessionID]; ok {
		return sess.atifWriter
	}
	return nil
}

// SetSessionModel records the model name used by the agent for the given
// session and persists it to meta.json. Empty model names are normalized to
// UnknownModel so meta.json's "model" field is never blank.
func (s *FileService) SetSessionModel(sessionID, modelName string) error {
	modelName = strings.TrimSpace(modelName)
	if modelName == "" {
		modelName = UnknownModel
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	sess, ok := s.sessions[sessionID]
	if !ok {
		return fmt.Errorf("session not found: %s", sessionID)
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()
	sess.meta.Model = modelName
	return writeMeta(filepath.Join(s.baseDir, sessionID), &sess.meta)
}

// SetSessionTitle records a human-readable title for the given session and
// persists it to meta.json. The title is trimmed, truncated to MaxSessionTitle,
// and stripped of control characters so it can be embedded in terminal escape
// sequences (OSC 0) without breaking the surrounding output stream. An empty
// title clears the field.
func (s *FileService) SetSessionTitle(sessionID, title string) error {
	title = sanitizeSessionTitle(title)
	s.mu.Lock()
	defer s.mu.Unlock()

	sess, ok := s.sessions[sessionID]
	if !ok {
		// Lazy-load from disk so a freshly reopened service can still update
		// titles for sessions it hasn't seen yet. Skip the app/user check
		// because SetSessionTitle is keyed by ID alone.
		loaded, err := s.loadSessionUnchecked(sessionID)
		if err != nil {
			return err
		}
		sess = loaded
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()
	sess.meta.Title = title
	return writeMeta(filepath.Join(s.baseDir, sessionID), &sess.meta)
}

// sanitizeSessionTitle trims surrounding whitespace, replaces ASCII control
// characters (including CR, LF, TAB) with spaces, and drops ESC (0x1B) and
// DEL (0x7F) entirely. This neutralizes OSC injection attempts — a stray
// "title" can't break out of the OSC 0 envelope because the envelope-opener
// (ESC) and the terminator (BEL) are removed before the title reaches
// stdout. The result is capped at MaxSessionTitle. Empty input returns "".
func sanitizeSessionTitle(title string) string {
	title = strings.TrimSpace(title)
	if title == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(title))
	for _, r := range title {
		if r == '\n' || r == '\r' || r == '\t' {
			b.WriteByte(' ')
			continue
		}
		// Strip C0 control characters (0x00–0x1F) and DEL (0x7F). Bell (0x07)
		// is the OSC terminator — it must not appear inside the title payload.
		if r < 0x20 || r == 0x7F {
			continue
		}
		// ESC (0x1B) opens a new escape sequence, which would let a stray
		// "title" inject arbitrary ANSI/OSC into the terminal. Drop it.
		b.WriteRune(r)
	}
	title = b.String()
	if len(title) > MaxSessionTitle {
		title = title[:MaxSessionTitle]
	}
	return title
}

// GetSessionTitle returns the current title for the given session, or "" if
// the session has no title set. Returns an error for unknown sessions.
func (s *FileService) GetSessionTitle(sessionID string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	sess, ok := s.sessions[sessionID]
	if !ok {
		// Lazy-load so a freshly reopened service can still report titles
		// for sessions persisted by a previous run. Skip the app/user check
		// because GetSessionTitle is keyed by ID alone.
		loaded, err := s.loadSessionUnchecked(sessionID)
		if err != nil {
			return "", err
		}
		sess = loaded
	}
	sess.mu.RLock()
	defer sess.mu.RUnlock()
	return sess.meta.Title, nil
}

// UpdatePlanContext updates the plan session context in the session metadata.
// Pass nil to clear the context.
func (s *FileService) UpdatePlanContext(sessionID string, ctx *PlanContext) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	sess, ok := s.sessions[sessionID]
	if !ok {
		return fmt.Errorf("session not found: %s", sessionID)
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()
	sess.meta.PlanContext = ctx
	sessionDir := filepath.Join(s.baseDir, sessionID)
	return writeMeta(sessionDir, &sess.meta)
}

// GetPlanContext returns the plan context for the given session, or nil if not found.
func (s *FileService) GetPlanContext(sessionID string) (*PlanContext, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	sess, ok := s.sessions[sessionID]
	if !ok {
		return nil, fmt.Errorf("session not found: %s", sessionID)
	}
	sess.mu.RLock()
	defer sess.mu.RUnlock()
	return sess.meta.PlanContext, nil
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
	mu         sync.RWMutex
	meta       Meta
	events     []*session.Event
	state      map[string]any
	updatedAt  time.Time
	atifWriter *atif.Writer
}

func (s *fileSession) live() *liveSession {
	return &liveSession{fs: s}
}

// liveSession implements session.Session as a live view of the underlying
// fileSession. Events and state are read through the shared reference so
// that mutations (e.g. AppendEvent) are immediately visible to the ADK
// runner's ContentsRequestProcessor.
type liveSession struct {
	fs *fileSession
}

func (s *liveSession) ID() string      { return s.fs.meta.ID }
func (s *liveSession) AppName() string { return s.fs.meta.AppName }
func (s *liveSession) UserID() string  { return s.fs.meta.UserID }
func (s *liveSession) LastUpdateTime() time.Time {
	s.fs.mu.RLock()
	defer s.fs.mu.RUnlock()
	return s.fs.updatedAt
}

func (s *liveSession) State() session.State {
	return &liveState{fs: s.fs}
}

func (s *liveSession) Events() session.Events {
	s.fs.mu.RLock()
	eventsCopy := make([]*session.Event, len(s.fs.events))
	copy(eventsCopy, s.fs.events)
	s.fs.mu.RUnlock()
	return eventList(eventsCopy)
}

// filteredSession wraps a fileSession but returns a pre-filtered events list.
// Used when Get() is called with NumRecentEvents or After filters.
type filteredSession struct {
	fs     *fileSession
	events []*session.Event
}

func (s *filteredSession) ID() string                { return s.fs.meta.ID }
func (s *filteredSession) AppName() string           { return s.fs.meta.AppName }
func (s *filteredSession) UserID() string            { return s.fs.meta.UserID }
func (s *filteredSession) LastUpdateTime() time.Time { return s.fs.updatedAt }
func (s *filteredSession) State() session.State      { return &liveState{fs: s.fs} }
func (s *filteredSession) Events() session.Events    { return eventList(s.events) }

// liveState implements session.State backed by the fileSession's state map.
type liveState struct {
	fs *fileSession
}

func (s *liveState) Get(key string) (any, error) {
	s.fs.mu.RLock()
	defer s.fs.mu.RUnlock()
	val, ok := s.fs.state[key]
	if !ok {
		return nil, session.ErrStateKeyNotExist
	}
	return val, nil
}

func (s *liveState) Set(key string, value any) error {
	s.fs.mu.Lock()
	defer s.fs.mu.Unlock()
	s.fs.state[key] = value
	return nil
}

func (s *liveState) All() iter.Seq2[string, any] {
	s.fs.mu.RLock()
	stateCopy := maps.Clone(s.fs.state)
	s.fs.mu.RUnlock()
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
	if strings.TrimSpace(meta.Model) == "" {
		meta.Model = UnknownModel
	}
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

// ClearEvents drops every event from a session, in memory and on disk, so the
// next LLM request starts from an empty context window.
//
// This is what the TUI's /clear needs and what Compact cannot provide:
// compaction replaces history with a summary (which is still history) and
// returns early when the session is under its token threshold, so on a short
// session it is a no-op. Clearing is unconditional.
//
// Everything that is not conversation is preserved: session state, meta
// (title, model, timestamps), plan context and branches all survive. The user
// cleared the conversation, not the session.
//
// The ATIF writer is replaced with a fresh one so the trajectory numbering
// restarts alongside the events rather than continuing over a history that no
// longer exists.
func (s *FileService) ClearEvents(sessionID, appName, userID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	sess, err := s.loadSession(sessionID, appName, userID)
	if err != nil {
		return fmt.Errorf("loading session for clear: %w", err)
	}

	sess.events = nil

	sessionDir := filepath.Join(s.baseDir, sessionID)
	if err := rewriteEvents(sessionDir, nil); err != nil {
		return fmt.Errorf("rewriting events after clear: %w", err)
	}

	sess.atifWriter = atif.NewWriter(
		filepath.Join(sessionDir, "trajectory.atif.json"),
		atif.SessionMeta{
			SessionID: sessionID,
			AgentName: sess.meta.AppName,
			Model:     sess.meta.Model,
			WorkDir:   sess.meta.WorkDir,
		},
	)

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
