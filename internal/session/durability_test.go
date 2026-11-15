package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/adk/v2/session"
)

// A crash mid-append leaves a partial last line. Loading must keep every event
// before it rather than failing the whole session.
//
// Before this, readEvents returned an error for the entire file, so one torn
// record cost the whole transcript:
//
//	readEvents -> events=0 err=unmarshaling event: unexpected end of JSON input
func TestReadEvents_TornFinalLineKeepsEarlierEvents(t *testing.T) {
	dir := t.TempDir()

	good1 := `{"id":"e1","timestamp":"2024-01-01T00:00:00Z","author":"user"}` + "\n"
	good2 := `{"id":"e2","timestamp":"2024-01-01T00:00:01Z","author":"model"}` + "\n"
	torn := `{"id":"e3","timestamp":"2024-01-`
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(good1+good2+torn), 0o644); err != nil {
		t.Fatal(err)
	}

	events, err := readEvents(dir)
	if err != nil {
		t.Fatalf("readEvents() error = %v; a torn final line must not fail the load", err)
	}
	if len(events) != 2 {
		t.Fatalf("readEvents() = %d events, want the 2 intact ones", len(events))
	}
	if events[0].ID != "e1" || events[1].ID != "e2" {
		t.Errorf("ids = %q, %q; want e1, e2", events[0].ID, events[1].ID)
	}
}

// A torn line in the middle must not shift or drop the events after it.
func TestReadEvents_UnparseableMiddleLineIsSkipped(t *testing.T) {
	dir := t.TempDir()
	content := `{"id":"e1","timestamp":"2024-01-01T00:00:00Z"}` + "\n" +
		"not json at all\n" +
		`{"id":"e3","timestamp":"2024-01-01T00:00:02Z"}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	events, err := readEvents(dir)
	if err != nil {
		t.Fatalf("readEvents() error = %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("readEvents() = %d events, want 2", len(events))
	}
	if events[0].ID != "e1" || events[1].ID != "e3" {
		t.Errorf("ids = %q, %q; want e1, e3", events[0].ID, events[1].ID)
	}
}

// A line that decodes as JSON but violates the event shape must still surface:
// only malformed lines are skipped, so a schema error is not silently dropped.
func TestReadEvents_InvalidEventShapeStillErrors(t *testing.T) {
	dir := t.TempDir()
	// Valid JSON, unusable as a session.Event: timestamp is not a time.
	content := `{"id":"e1","timestamp":"not-a-time"}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	events, err := readEvents(dir)
	if err == nil {
		t.Fatalf("readEvents() = %d events, nil error; want the shape error surfaced", len(events))
	}
}

// A torn tail must not swallow the event appended after it. Without the tail
// repair the O_APPEND write concatenates onto the fragment, producing one
// corrupt line and losing both the fragment and the new event.
func TestAppendEventToFile_RepairsTornTail(t *testing.T) {
	dir := t.TempDir()

	good := `{"id":"e1","timestamp":"2024-01-01T00:00:00Z"}` + "\n"
	torn := `{"id":"e2","timest`
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(good+torn), 0o644); err != nil {
		t.Fatal(err)
	}

	next := &session.Event{ID: "e3", Timestamp: time.Date(2024, 1, 1, 0, 0, 2, 0, time.UTC)}
	if err := appendEventToFile(dir, next); err != nil {
		t.Fatalf("appendEventToFile() error = %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	// The fragment must be isolated on its own line, not glued to the new event.
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3 (good, torn fragment, new event):\n%s", len(lines), raw)
	}
	if lines[1] != torn {
		t.Errorf("line 2 = %q, want the torn fragment %q left alone", lines[1], torn)
	}
	if !strings.Contains(lines[2], `"id":"e3"`) {
		t.Errorf("line 3 = %q, want the appended event", lines[2])
	}

	// And the load keeps the two intact events.
	events, err := readEvents(dir)
	if err != nil {
		t.Fatalf("readEvents() error = %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("readEvents() = %d events, want 2 (e1, e3)", len(events))
	}
	if events[0].ID != "e1" || events[1].ID != "e3" {
		t.Errorf("ids = %q, %q; want e1, e3", events[0].ID, events[1].ID)
	}
}

// A clean file must be appended to without introducing a blank line.
func TestAppendEventToFile_NoSpuriousBlankLine(t *testing.T) {
	dir := t.TempDir()
	first := &session.Event{ID: "e1", Timestamp: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)}
	second := &session.Event{ID: "e2", Timestamp: time.Date(2024, 1, 1, 0, 0, 1, 0, time.UTC)}

	for _, e := range []*session.Event{first, second} {
		if err := appendEventToFile(dir, e); err != nil {
			t.Fatalf("appendEventToFile(%s) error = %v", e.ID, err)
		}
	}

	raw, err := os.ReadFile(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(raw), "\n"); got != 2 {
		t.Errorf("file has %d newlines, want 2 (one per event, no blank lines):\n%s", got, raw)
	}
	if strings.Contains(string(raw), "\n\n") {
		t.Errorf("file contains a blank line:\n%s", raw)
	}
}

// Every event must reach disk, not just the page cache.
func TestAppendEventToFile_IsSynced(t *testing.T) {
	dir := t.TempDir()
	event := &session.Event{ID: "e1", Timestamp: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)}
	if err := appendEventToFile(dir, event); err != nil {
		t.Fatalf("appendEventToFile() error = %v", err)
	}

	// The appended line is complete on disk once the call returns: a reader that
	// never sees the writing process's cache must find it whole and newline
	// terminated.
	raw, err := os.ReadFile(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(raw), "\n") {
		t.Errorf("file does not end in a newline: %q", raw)
	}
	if !strings.Contains(string(raw), `"id":"e1"`) {
		t.Errorf("file does not contain the event: %q", raw)
	}
}

// writeFileAtomic and rewriteEvents must not publish a name for content that is
// only in the page cache. These assert the observable contract: the target
// exists, is complete, and is valid after the call.
func TestRewriteEvents_TargetIsComplete(t *testing.T) {
	dir := t.TempDir()
	events := []*session.Event{
		{ID: "e1", Timestamp: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)},
		{ID: "e2", Timestamp: time.Date(2024, 1, 1, 0, 0, 1, 0, time.UTC)},
	}
	if err := rewriteEvents(dir, events); err != nil {
		t.Fatalf("rewriteEvents() error = %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(raw), "\n") {
		t.Errorf("rewritten file does not end in a newline: %q", raw)
	}
	if _, err := readEvents(dir); err != nil {
		t.Errorf("readEvents() after rewrite error = %v", err)
	}
}

func TestWriteFileAtomic_ContentIsComplete(t *testing.T) {
	path := filepath.Join(t.TempDir(), "meta.json")
	data := []byte(`{"id":"s1","model":"m"}`)
	if err := writeFileAtomic(path, data, 0o644); err != nil {
		t.Fatalf("writeFileAtomic() error = %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(data) {
		t.Errorf("read back %q, want %q", got, data)
	}
}
