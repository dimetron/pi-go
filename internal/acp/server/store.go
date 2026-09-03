package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"time"

	adksession "google.golang.org/adk/v2/session"

	piagent "github.com/dimetron/pi-go/internal/agent"
	pisession "github.com/dimetron/pi-go/internal/session"
)

// SessionSummary describes one persisted session, as reported by session/list.
type SessionSummary struct {
	ID        string
	Cwd       string
	Title     string
	UpdatedAt time.Time
}

// SessionStore is the persistence seam behind session/load, session/resume
// and session/list. Ids are the ACP session ids the client knows; the store
// maps them to whatever it keeps on disk (see StoreSessionID). A nil store
// leaves the agent with in-memory sessions only: every id is accepted on
// load and resume because there is nothing to check it against, and
// session/list is not advertised.
type SessionStore interface {
	// Exists reports whether a transcript is persisted for the session.
	Exists(ctx context.Context, sessionID string) bool
	// Replay re-emits the persisted transcript through updater as ACP
	// session updates, in order. session/load calls it before responding.
	Replay(ctx context.Context, sessionID string, updater SessionUpdater) error
	// List returns every persisted session, newest first.
	List(ctx context.Context) ([]SessionSummary, error)
}

// sessionIDPattern accepts ids that are safe directory names: pi's own
// sess_… ids, the UUIDs kagent assigns as A2A context ids, and the
// yymmdd-hhmm-… ids the CLI generates.
var sessionIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// StoreSessionID maps an ACP session id to the id its transcript is persisted
// under. Safe ids pass through unchanged, so the on-disk session carries the
// id the client knows and `pi --session <id>` can reopen it. Anything else —
// path separators, a leading dot, control characters, an over-long string —
// is replaced by a stable hash, so a hostile id cannot name a path outside
// the sessions directory and the same id still resolves to the same transcript.
func StoreSessionID(id string) string {
	if sessionIDPattern.MatchString(id) {
		return id
	}
	sum := sha256.Sum256([]byte(id))
	return "acp-" + hex.EncodeToString(sum[:16])
}

// FileSessionStore is a SessionStore over pi's on-disk session store — the
// same JSONL directory the CLI writes to, so a transcript an editor started
// over ACP is resumable from the terminal and vice versa.
type FileSessionStore struct {
	svc *pisession.FileService
}

var _ SessionStore = (*FileSessionStore)(nil)

// NewFileSessionStore wraps svc as a SessionStore.
func NewFileSessionStore(svc *pisession.FileService) *FileSessionStore {
	return &FileSessionStore{svc: svc}
}

// Exists implements SessionStore.
func (s *FileSessionStore) Exists(ctx context.Context, sessionID string) bool {
	_, err := s.get(ctx, sessionID)
	return err == nil
}

// Replay implements SessionStore.
func (s *FileSessionStore) Replay(ctx context.Context, sessionID string, updater SessionUpdater) error {
	if updater == nil {
		return nil
	}
	sess, err := s.get(ctx, sessionID)
	if err != nil {
		return err
	}
	return replayEvents(ctx, sess.Events().All(), updater)
}

// List implements SessionStore.
func (s *FileSessionStore) List(_ context.Context) ([]SessionSummary, error) {
	metas, err := s.svc.ListMeta(piagent.AppName, piagent.DefaultUserID)
	if err != nil {
		return nil, fmt.Errorf("listing sessions: %w", err)
	}
	out := make([]SessionSummary, 0, len(metas))
	for _, m := range metas {
		out = append(out, SessionSummary{
			ID:        m.ID,
			Cwd:       m.WorkDir,
			Title:     m.Title,
			UpdatedAt: m.UpdatedAt,
		})
	}
	return out, nil
}

func (s *FileSessionStore) get(ctx context.Context, sessionID string) (adksession.Session, error) {
	resp, err := s.svc.Get(ctx, &adksession.GetRequest{
		AppName:   piagent.AppName,
		UserID:    piagent.DefaultUserID,
		SessionID: StoreSessionID(sessionID),
	})
	if err != nil {
		return nil, fmt.Errorf("session %s: %w", sessionID, err)
	}
	return resp.Session, nil
}
