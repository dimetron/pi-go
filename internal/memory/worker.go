package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// Compressor transforms raw tool observations into structured observations.
type Compressor interface {
	CompressObservation(ctx context.Context, raw RawObservation) (*Observation, error)
}

// Worker is a background goroutine that drains raw observations,
// applies privacy filtering, compresses them, and stores the results.
type Worker struct {
	store      Store
	compressor Compressor
	obsChan    chan RawObservation
	done       chan struct{}
	wg         sync.WaitGroup
}

// NewWorker creates a Worker with the given buffer size for the observation channel.
func NewWorker(store Store, compressor Compressor, bufSize int) *Worker {
	if bufSize <= 0 {
		bufSize = 100
	}
	return &Worker{
		store:      store,
		compressor: compressor,
		obsChan:    make(chan RawObservation, bufSize),
		done:       make(chan struct{}),
	}
}

// Enqueue sends a raw observation to the worker. Non-blocking: drops and logs if full.
func (w *Worker) Enqueue(obs RawObservation) {
	select {
	case w.obsChan <- obs:
	default:
		slog.Warn("memory: observation channel full, dropping observation",
			"tool", obs.ToolName,
			"session", obs.SessionID,
		)
	}
}

// Start begins processing observations in a background goroutine.
func (w *Worker) Start(ctx context.Context) {
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		for raw := range w.obsChan {
			w.processOne(ctx, raw)
		}
		close(w.done)
	}()
}

// Shutdown closes the observation channel and waits for all pending
// observations to be processed, with a timeout.
func (w *Worker) Shutdown(ctx context.Context) error {
	close(w.obsChan)

	// Wait for drain or context timeout
	select {
	case <-w.done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("memory: shutdown timed out: %w", ctx.Err())
	}
}

// processOne handles a single raw observation: privacy filter, compress, store.
func (w *Worker) processOne(ctx context.Context, raw RawObservation) {
	// Apply privacy filtering
	raw.ToolInput = StripPrivateFromMap(raw.ToolInput)
	raw.ToolOutput = StripPrivateFromMap(raw.ToolOutput)

	// Compress via subagent (or mock)
	obs, err := w.compressor.CompressObservation(ctx, raw)
	if err != nil {
		slog.Warn("memory: compression failed, storing fallback",
			"tool", raw.ToolName,
			"error", err,
		)
		obs = w.fallbackObservation(raw)
	}

	// Store the observation
	if err := w.store.InsertObservation(ctx, obs); err != nil {
		slog.Error("memory: failed to store observation",
			"tool", raw.ToolName,
			"error", err,
		)
	}
}

// fallbackObservation creates a minimal observation when compression fails.
func (w *Worker) fallbackObservation(raw RawObservation) *Observation {
	text := truncateFallbackText(raw)
	return &Observation{
		SessionID:   raw.SessionID,
		Project:     raw.Project,
		Title:       raw.ToolName + " (uncompressed)",
		Type:        TypeChange,
		Text:        text,
		SourceFiles: extractSourceFiles(raw.ToolInput),
		ToolName:    raw.ToolName,
		CreatedAt:   raw.Timestamp,
	}
}

// truncateFallbackText produces a truncated JSON summary of the raw observation.
func truncateFallbackText(raw RawObservation) string {
	const maxLen = 4096
	data := map[string]any{
		"tool_input":  raw.ToolInput,
		"tool_output": raw.ToolOutput,
	}
	b, err := json.Marshal(data)
	if err != nil {
		return fmt.Sprintf("tool=%s (marshal error)", raw.ToolName)
	}
	s := string(b)
	if len(s) > maxLen {
		s = s[:maxLen] + "...(truncated)"
	}
	return s
}

// extractSourceFiles attempts to find file paths in tool input.
func extractSourceFiles(input map[string]any) []string {
	var files []string
	for _, key := range []string{"file_path", "path", "file", "filename"} {
		if v, ok := input[key]; ok {
			if s, ok := v.(string); ok && s != "" {
				files = append(files, s)
			}
		}
	}
	if files == nil {
		files = []string{}
	}
	return files
}

// BuildMemoryCallback creates an AfterToolCallback function that enqueues observations.
func BuildMemoryCallback(w *Worker, sessionID, project string) func(toolName string, toolInput, toolOutput map[string]any) {
	return func(toolName string, toolInput, toolOutput map[string]any) {
		w.Enqueue(RawObservation{
			SessionID:  sessionID,
			Project:    project,
			ToolName:   toolName,
			ToolInput:  toolInput,
			ToolOutput: toolOutput,
			Timestamp:  time.Now(),
		})
	}
}
