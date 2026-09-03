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
	"unicode/utf8"

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
	ID      string `json:"id"`
	AppName string `json:"appName"`
	UserID  string `json:"userID"`
	WorkDir string `json:"workDir,omitempty"`

	// Model is the model name as configured. Provider is recorded alongside it
	// because the same name means different things to different backends —
	// "qwen2.5:latest" via ollama and via a hosted gateway are not the same
	// model, and a transcript that records only the name cannot tell them apart
	// after the fact.
	Model    string `json:"model"`
	Provider string `json:"provider,omitempty"`
	BaseURL  string `json:"baseURL,omitempty"`

	Title     string    `json:"title,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`

	// Host is a snapshot of the machine when the session began. It is here so a
	// process that later dies with nothing but "signal: killed" can be checked
	// against the resources it had, instead of guessed at.
	Host *HostEnv `json:"host,omitempty"`

	PlanContext *PlanContext `json:"planContext,omitempty"`

	// Agent records this session's place in a /run or /plan agent tree. It is
	// absent for ordinary interactive sessions.
	Agent *AgentContext `json:"agentContext,omitempty"`
}

// AgentContext records where a session sits in an agent tree.
//
// Reconstructing a run previously meant grouping sessions by workDir and
// inferring roles from title prefixes; sessions in numerically-named worktrees
// (.pi-go/tasks/763098722000) could not be attributed to a spec by any recorded
// field at all, and three such worktrees are still on disk with nothing to say
// what they were for. Every question that investigation had to answer by
// inference is a field here.
type AgentContext struct {
	AgentID   string `json:"agentID,omitempty"`   // the orchestrator's ID for this agent
	AgentType string `json:"agentType,omitempty"` // task | worker | quick-task | code-reviewer | explore
	ParentID  string `json:"parentSessionID,omitempty"`
	RunID     string `json:"runID,omitempty"` // groups every session of one /run
	SpecName  string `json:"specName,omitempty"`
	Slice     int    `json:"slice,omitempty"`
	Cycle     int    `json:"cycle,omitempty"` // /run retry index
	Worktree  string `json:"worktree,omitempty"`
	Branch    string `json:"branch,omitempty"`
	Status    string `json:"status,omitempty"` // terminal status, written when the run ends
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
	host := captureHostEnv(sessionDir)
	meta := Meta{
		ID:        sessionID,
		AppName:   req.AppName,
		UserID:    req.UserID,
		WorkDir:   cwd,
		Model:     UnknownModel,
		CreatedAt: now,
		UpdatedAt: now,
		Host:      &host,
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

// ListMeta returns the metadata of every session under baseDir for the given
// app, newest first. It reads only meta.json, never events, so listing a large
// store stays cheap. An empty userID matches every user. Directories without
// readable metadata are skipped.
func (s *FileService) ListMeta(appName, userID string) ([]Meta, error) {
	if appName == "" {
		return nil, fmt.Errorf("app_name is required")
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	entries, err := os.ReadDir(s.baseDir)
	if err != nil {
		return nil, fmt.Errorf("reading sessions dir: %w", err)
	}

	metas := make([]Meta, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		meta, err := readMeta(filepath.Join(s.baseDir, entry.Name()))
		if err != nil || meta.AppName != appName {
			continue
		}
		if userID != "" && meta.UserID != userID {
			continue
		}
		metas = append(metas, *meta)
	}
	sort.Slice(metas, func(i, j int) bool {
		return metas[i].UpdatedAt.After(metas[j].UpdatedAt)
	})
	return metas, nil
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
		// Lazy-load from disk, like SetSessionTitle: a resumed session is not
		// in the cache until the runner first reads it, and recording the model
		// it is resuming under has to happen before that.
		loaded, err := s.loadSessionUnchecked(sessionID)
		if err != nil {
			return err
		}
		sess = loaded
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()
	sess.meta.Model = modelName
	return writeMeta(filepath.Join(s.baseDir, sessionID), &sess.meta)
}

// SetSessionProvider records which backend served the model, and the base URL
// when one was configured.
//
// The model name alone is ambiguous: the same string routed to ollama, to a
// hosted gateway, or to a vendor API is three different models with three
// different failure modes. Recording the name without the backend leaves a
// transcript that cannot be reproduced. Both fields are optional — an empty
// value clears rather than writes a blank string, so meta.json stays quiet when
// there is nothing to say.
func (s *FileService) SetSessionProvider(sessionID, provider, baseURL string) error {
	provider = strings.TrimSpace(provider)
	baseURL = strings.TrimSpace(baseURL)

	s.mu.Lock()
	defer s.mu.Unlock()

	sess, ok := s.sessions[sessionID]
	if !ok {
		loaded, err := s.loadSessionUnchecked(sessionID)
		if err != nil {
			return err
		}
		sess = loaded
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()
	sess.meta.Provider = provider
	sess.meta.BaseURL = baseURL
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

// SetSessionWorkDir records the working directory a session runs in and
// persists it to meta.json. Create captures the process's own cwd, which is
// right for the CLI but not for a server that hosts sessions for directories
// it was not started in — an ACP editor's project, an A2A actor's workspace.
func (s *FileService) SetSessionWorkDir(sessionID, dir string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	sess, ok := s.sessions[sessionID]
	if !ok {
		loaded, err := s.loadSessionUnchecked(sessionID)
		if err != nil {
			return err
		}
		sess = loaded
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()
	sess.meta.WorkDir = dir
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
	return truncateTitle(b.String())
}

// truncateTitle caps a title at MaxSessionTitle bytes without splitting a
// rune. A plain byte slice cuts multi-byte characters in half, and the
// fragment is stored in meta.json and written to the terminal inside an OSC 0
// sequence — where it shows up as U+FFFD. Half of recent session titles
// carried one before this backed the cut up to a boundary.
func truncateTitle(title string) string {
	if len(title) <= MaxSessionTitle {
		return title
	}
	cut := MaxSessionTitle
	for cut > 0 && !utf8.RuneStart(title[cut]) {
		cut--
	}
	return title[:cut]
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

// UpdateAgentContext records a session's place in an agent tree.
// Pass nil to clear it.
func (s *FileService) UpdateAgentContext(sessionID string, ctx *AgentContext) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	sess, ok := s.sessions[sessionID]
	if !ok {
		return fmt.Errorf("session not found: %s", sessionID)
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()
	sess.meta.Agent = ctx
	sessionDir := filepath.Join(s.baseDir, sessionID)
	return writeMeta(sessionDir, &sess.meta)
}

// GetAgentContext returns the agent context for a session, or nil if unset.
func (s *FileService) GetAgentContext(sessionID string) (*AgentContext, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	sess, ok := s.sessions[sessionID]
	if !ok {
		return nil, fmt.Errorf("session not found: %s", sessionID)
	}
	sess.mu.RLock()
	defer sess.mu.RUnlock()
	return sess.meta.Agent, nil
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

// writeFileAtomic writes data to path via a temp file and rename, so a reader
// (including rsync copying the sessions tree) never observes a partially
// written file. The temp file lives in the same directory as the target so the
// rename is atomic on the same filesystem.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".pi-*.tmp")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("writing temp file: %w", err)
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("closing temp file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("renaming temp file: %w", err)
	}
	return nil
}

func writeMeta(sessionDir string, meta *Meta) error {
	if strings.TrimSpace(meta.Model) == "" {
		meta.Model = UnknownModel
	}
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling meta: %w", err)
	}
	return writeFileAtomic(filepath.Join(sessionDir, "meta.json"), data, 0o644)
}

// SessionBackend returns the provider and endpoint recorded for a session.
// It returns false when the session metadata is unavailable or predates backend
// recording.
func SessionBackend(baseDir, sessionID string) (provider, baseURL string, ok bool) {
	meta, err := readMeta(filepath.Join(baseDir, sessionID))
	if err != nil || meta == nil {
		return "", "", false
	}
	provider = strings.TrimSpace(meta.Provider)
	baseURL = strings.TrimSpace(meta.BaseURL)
	return provider, baseURL, provider != "" || baseURL != ""
}

// SessionModel returns the model recorded in a session's metadata, or an empty
// string when the session is unknown or its model was never recorded.
//
// Deliberately not a FileService method: startup needs this before any session
// is loaded, and going through the cache would parse the whole events.jsonl
// (and rebuild the ATIF trajectory) to read a single metadata field.
func SessionModel(baseDir, sessionID string) string {
	meta, err := readMeta(filepath.Join(baseDir, sessionID))
	if err != nil || meta.Model == UnknownModel {
		return ""
	}
	return strings.TrimSpace(meta.Model)
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
