package session

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"google.golang.org/adk/v2/session"
)

// spendFileName is the append-only token ledger for one session.
// Compaction, /clear, and branch switches rewrite events.jsonl and drop the
// usage blocks on the events they remove. The ledger is not rewritten by
// those paths, so /cost can still price the tokens that were spent.
const spendFileName = "token-spend.jsonl"

// SpendCall is one model response's token counts.
//
// Input includes Cached. Output is the candidate count and may already
// include reasoning; Thoughts and Total are kept so the cost estimate can
// tell those cases apart. Dollars are not stored: a later price refresh
// reprices the same tokens.
//
// A backfill of a session that predates this file attributes every response
// that has no model of its own to whatever model the session meta records
// at backfill time. Responses appended after that are stamped with the
// model in effect when they were recorded.
type SpendCall struct {
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
	Input    int64  `json:"input_tokens"`
	Output   int64  `json:"output_tokens"`
	Cached   int64  `json:"cached_tokens,omitempty"`
	Thoughts int64  `json:"thoughts_tokens,omitempty"`
	Total    int64  `json:"total_tokens,omitempty"`
}

// SessionSpend returns the session's recorded responses, backfilling the
// ledger from events.jsonl the first time. Caller-facing: it takes the
// service lock.
func (s *FileService) SessionSpend(sessionID string) ([]SpendCall, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	sess, err := s.loadSessionUnchecked(sessionID)
	if err != nil {
		return nil, err
	}
	sessionDir := filepath.Join(s.baseDir, sessionID)
	s.ensureSpend(sessionDir, sess)
	return readSpend(sessionDir)
}

// noteAppendedSpend records the event that AppendEvent just persisted.
// When the ledger does not exist yet, every event already on the session is
// written once — including this one — so a resumed session is not missing
// the turns that happened before the file did. Caller holds s.mu.
func (s *FileService) noteAppendedSpend(sessionDir string, sess *fileSession, event *session.Event) {
	path := spendPath(sessionDir)
	if _, err := os.Stat(path); err == nil {
		call, ok := spendCallFromEvent(event, sess.meta.Provider, sess.meta.Model)
		if !ok {
			return
		}
		if err := appendSpendCall(path, call); err != nil {
			slog.Warn("spend: append failed", "session", sess.meta.ID, "error", err)
		}
		return
	} else if !os.IsNotExist(err) {
		slog.Warn("spend: stat failed", "session", sess.meta.ID, "error", err)
		return
	}
	if err := writeSpendFromEvents(sessionDir, sess); err != nil {
		slog.Warn("spend: backfill failed", "session", sess.meta.ID, "error", err)
	}
}

// ensureSpend creates the ledger from the events still on the session when
// the file is absent. Compaction and /clear call it before they drop events,
// so those tokens stay in the ledger. Caller holds s.mu.
func (s *FileService) ensureSpend(sessionDir string, sess *fileSession) {
	if _, err := os.Stat(spendPath(sessionDir)); err == nil {
		return
	} else if err != nil && !os.IsNotExist(err) {
		slog.Warn("spend: stat failed", "session", sess.meta.ID, "error", err)
		return
	}
	if err := writeSpendFromEvents(sessionDir, sess); err != nil {
		slog.Warn("spend: backfill failed", "session", sess.meta.ID, "error", err)
	}
}

func spendPath(sessionDir string) string {
	return filepath.Join(sessionDir, spendFileName)
}

func spendCallFromEvent(ev *session.Event, provider, model string) (SpendCall, bool) {
	if ev == nil || ev.UsageMetadata == nil {
		return SpendCall{}, false
	}
	u := ev.UsageMetadata
	if u.PromptTokenCount == 0 && u.CandidatesTokenCount == 0 &&
		u.CachedContentTokenCount == 0 && u.ThoughtsTokenCount == 0 &&
		u.TotalTokenCount == 0 {
		return SpendCall{}, false
	}
	if ev.ModelVersion != "" {
		model = ev.ModelVersion
	}
	return SpendCall{
		Provider: provider,
		Model:    model,
		Input:    int64(u.PromptTokenCount),
		Output:   int64(u.CandidatesTokenCount),
		Cached:   int64(u.CachedContentTokenCount),
		Thoughts: int64(u.ThoughtsTokenCount),
		Total:    int64(u.TotalTokenCount),
	}, true
}

func writeSpendFromEvents(sessionDir string, sess *fileSession) error {
	var buf bytes.Buffer
	for _, ev := range sess.events {
		call, ok := spendCallFromEvent(ev, sess.meta.Provider, sess.meta.Model)
		if !ok {
			continue
		}
		b, err := json.Marshal(call)
		if err != nil {
			return err
		}
		buf.Write(b)
		buf.WriteByte('\n')
	}
	if err := writeFileAtomic(spendPath(sessionDir), buf.Bytes(), 0o644); err != nil {
		return fmt.Errorf("writing token spend: %w", err)
	}
	return nil
}

func appendSpendCall(path string, call SpendCall) error {
	b, err := json.Marshal(call)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(b)
	return err
}

func readSpend(sessionDir string) ([]SpendCall, error) {
	f, err := os.Open(spendPath(sessionDir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var out []SpendCall
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var call SpendCall
		if err := json.Unmarshal(line, &call); err != nil {
			continue
		}
		out = append(out, call)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
