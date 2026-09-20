package memory

import "context"

// NoopCompressor records an observation without calling a model.
//
// It exists because the per-observation model call is the expensive part of the
// memory pipeline, not the recording. The default compressor spawns a
// `pi --mode json` child process per tool call (see SubagentCompressor), which
// costs a process, a session directory, a database open, a log file and an
// uncached provider round trip — all before the model has been asked anything.
// A session of forty tool calls therefore pays forty of those, serially, on a
// worker whose queue the shutdown path only waits five seconds for.
//
// What the model call actually buys is a title, a type classification and a
// narrative sentence. None of that is required for an observation to be
// searchable: the tool name, the file paths and the (truncated) tool output are
// all readable from the raw event. So this compressor writes exactly the record
// the failure path already writes, and the session-level summary — one model
// call at the end, over the whole session — supplies the narrative instead.
//
// The trade is deliberate: per-tool rows stay, so mem-search, mem-timeline,
// mem-get and the recalled-context block keep working; per-tool inference goes.
type NoopCompressor struct{}

// NewNoopCompressor returns a Compressor that stores the fallback observation
// and never calls a model.
func NewNoopCompressor() *NoopCompressor {
	return &NoopCompressor{}
}

// CompressObservation builds the fallback observation for raw.
//
// It never returns an error: there is nothing that can fail when no model is
// involved, and returning one would only make the worker log a compression
// failure for a path that succeeded.
func (c *NoopCompressor) CompressObservation(_ context.Context, raw RawObservation) (*Observation, error) {
	return FallbackObservation(raw), nil
}

// FallbackObservation builds the model-free observation for a raw tool event.
//
// Shared by the worker's failure path and NoopCompressor so the two cannot
// drift: a compression failure and a deliberately uncompressed record should
// produce the same shape, since both are what a reader sees when no model
// interpretation is available.
func FallbackObservation(raw RawObservation) *Observation {
	return &Observation{
		SessionID:   raw.SessionID,
		Project:     raw.Project,
		Title:       raw.ToolName + " (uncompressed)",
		Type:        TypeChange,
		Text:        truncateFallbackText(raw),
		SourceFiles: extractSourceFiles(raw.ToolInput),
		ToolName:    raw.ToolName,
		CreatedAt:   raw.Timestamp,
	}
}
