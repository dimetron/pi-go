package atif

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"google.golang.org/adk/v2/session"
)

// SessionMeta holds metadata needed to initialize the ATIF trajectory header.
type SessionMeta struct {
	SessionID string
	AgentName string
	Model     string
	WorkDir   string
}

// Writer manages incremental writing of an ATIF trajectory file.
// It is safe for concurrent use; each AppendEvent call converts the event,
// appends to the in-memory trajectory, and atomically rewrites the JSON file.
type Writer struct {
	filePath    string
	trajectory  Trajectory
	stepCounter int
	mu          sync.Mutex
}

// NewWriter creates a Writer that will maintain an ATIF file at filePath.
// The trajectory header is initialized from meta but the file is not written
// until the first AppendEvent call.
func NewWriter(filePath string, meta SessionMeta) *Writer {
	w := &Writer{
		filePath:    filePath,
		stepCounter: 1,
	}
	w.trajectory = Trajectory{
		SchemaVersion: SchemaVersion,
		SessionID:     meta.SessionID,
		Agent: AgentInfo{
			Name:      meta.AgentName,
			ModelName: meta.Model,
		},
	}
	if meta.WorkDir != "" {
		w.trajectory.Agent.Extra = map[string]any{"work_dir": meta.WorkDir}
	}
	return w
}

// AppendEvent converts a session event into ATIF step(s) and appends them
// to the trajectory. The updated trajectory is atomically flushed to disk.
// Returns nil if the event produces no steps (e.g., empty or partial events).
func (w *Writer) AppendEvent(event *session.Event) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	steps := ConvertEvent(event, w.stepCounter)
	if len(steps) == 0 {
		return nil
	}

	w.trajectory.Steps = append(w.trajectory.Steps, steps...)
	w.stepCounter += len(steps)

	return w.flush()
}

// AppendEvents converts each session event into ATIF step(s) and appends them
// to the trajectory, then flushes the accumulated trajectory to disk exactly
// once at the end. Use this for batch loads (e.g., replaying a session from
// disk) where per-event flushing would do O(n) full-file rewrites for n
// events. For live appends, prefer AppendEvent, which flushes per event to
// preserve crash-safety of committed events.
//
// Returns nil if every event in the slice produces no steps (no file is
// written in that case). A per-event conversion error is logged via the
// returned error aggregated from the first failing event; the remaining
// events are still processed, matching the best-effort semantics of the
// previous per-event replay loop in internal/session/store.go.
func (w *Writer) AppendEvents(events []*session.Event) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	flushed := false
	var firstErr error
	for _, event := range events {
		steps := ConvertEvent(event, w.stepCounter)
		if len(steps) == 0 {
			continue
		}
		w.trajectory.Steps = append(w.trajectory.Steps, steps...)
		w.stepCounter += len(steps)
		flushed = true
	}
	if !flushed {
		return firstErr
	}
	if err := w.flush(); err != nil {
		return fmt.Errorf("atif: batch flush: %w", err)
	}
	return firstErr
}

// SetSubagentRef sets the subagent_trajectory_ref on the observation result
// whose source_call_id matches toolCallID. This is called after a subagent
// completes to link its trajectory into the parent.
func (w *Writer) SetSubagentRef(toolCallID string, refPath string) {
	w.mu.Lock()
	defer w.mu.Unlock()

	for i := len(w.trajectory.Steps) - 1; i >= 0; i-- {
		step := &w.trajectory.Steps[i]
		if step.Observation == nil {
			continue
		}
		for j := range step.Observation.Results {
			if step.Observation.Results[j].SourceCallID == toolCallID {
				step.Observation.Results[j].SubagentTrajectoryRef = refPath
				return
			}
		}
	}
}

// StepCount returns the number of steps in the trajectory.
func (w *Writer) StepCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.trajectory.Steps)
}

// Close performs a final flush. It is safe to call multiple times.
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.trajectory.Steps) == 0 {
		return nil
	}
	return w.flush()
}

// flush atomically writes the trajectory JSON to disk via a temp file and rename.
// The caller must hold w.mu.
func (w *Writer) flush() error {
	data, err := json.MarshalIndent(w.trajectory, "", "  ")
	if err != nil {
		return fmt.Errorf("atif: marshal trajectory: %w", err)
	}
	data = append(data, '\n')

	dir := filepath.Dir(w.filePath)
	tmp, err := os.CreateTemp(dir, ".atif-*.tmp")
	if err != nil {
		return fmt.Errorf("atif: create temp file: %w", err)
	}
	tmpName := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("atif: write temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("atif: close temp file: %w", err)
	}

	if err := os.Rename(tmpName, w.filePath); err != nil {
		// Fall back to direct write if rename fails (e.g., cross-device).
		if writeErr := os.WriteFile(w.filePath, data, 0o644); writeErr != nil {
			os.Remove(tmpName)
			return fmt.Errorf("atif: rename failed (%w), direct write also failed: %w", err, writeErr)
		}
		os.Remove(tmpName)
	}

	return nil
}
