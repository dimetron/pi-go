package atif

import (
	"encoding/json"
	"path/filepath"

	"google.golang.org/adk/session"
)

// LinkSubagentTrajectories inspects a session event for subagent tool responses.
// When a FunctionResponse from the "subagent" tool contains result entries with
// session_id fields, it computes relative paths to the subagent trajectory files
// and calls SetSubagentRef to link them in the parent trajectory.
//
// parentSessionDir is the directory of the parent session (e.g. ~/.pi-go/sessions/<id>).
// sessionsBaseDir is the root sessions directory (e.g. ~/.pi-go/sessions).
func (w *Writer) LinkSubagentTrajectories(event *session.Event, parentSessionDir, sessionsBaseDir string) {
	if event == nil || event.Content == nil {
		return
	}

	for _, part := range event.Content.Parts {
		fr := part.FunctionResponse
		if fr == nil || fr.Name != "subagent" {
			continue
		}

		sourceCallID := fr.ID
		if sourceCallID == "" {
			sourceCallID = fr.Name
		}

		sessionIDs := extractSubagentSessionIDs(fr.Response)
		for _, subSessionID := range sessionIDs {
			if subSessionID == "" {
				continue
			}
			subTrajectory := filepath.Join(sessionsBaseDir, subSessionID, "trajectory.atif.json")
			relPath, err := filepath.Rel(parentSessionDir, subTrajectory)
			if err != nil {
				continue
			}
			w.SetSubagentRef(sourceCallID, relPath)
			// Flush after linking.
			w.mu.Lock()
			_ = w.flush()
			w.mu.Unlock()
			// For single-mode calls, one link per FunctionResponse is sufficient.
			// For parallel/chain, we link to the first result's trajectory.
			return
		}
	}
}

// extractSubagentSessionIDs extracts session_id values from a subagent tool response.
// The response is expected to be a map matching the SubagentOutput structure:
// {"results": [{"session_id": "..."}, ...]}
func extractSubagentSessionIDs(response any) []string {
	// The ADK serializes tool return values as map[string]any.
	data, err := json.Marshal(response)
	if err != nil {
		return nil
	}

	var output struct {
		Results []struct {
			SessionID string `json:"session_id"`
		} `json:"results"`
	}
	if err := json.Unmarshal(data, &output); err != nil {
		return nil
	}

	var ids []string
	for _, r := range output.Results {
		if r.SessionID != "" {
			ids = append(ids, r.SessionID)
		}
	}
	return ids
}
