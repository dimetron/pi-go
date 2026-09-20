package memory

import (
	"context"
	"testing"
	"time"

	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/subagent"
)

// The default compressor must not call a model. This is the point of the
// change: per-tool inference spawned a child process and an uncached provider
// round trip for every tool call in a session.
func TestNoopCompressor_CallsNoModel(t *testing.T) {
	raw := RawObservation{
		SessionID:  "sess-1",
		Project:    "/proj",
		ToolName:   "read",
		ToolInput:  map[string]any{"file_path": "/proj/main.go"},
		ToolOutput: map[string]any{"content": "package main"},
		Timestamp:  time.Now(),
	}

	obs, err := NewNoopCompressor().CompressObservation(context.Background(), raw)
	if err != nil {
		t.Fatalf("CompressObservation: %v", err)
	}
	if obs == nil {
		t.Fatal("expected an observation, got nil")
	}

	// The record must still be usable: mem-search, mem-timeline and the
	// recalled-context block all read these fields, so a model-free record
	// that dropped them would break retrieval rather than just lose prose.
	if obs.SessionID != raw.SessionID {
		t.Errorf("SessionID = %q, want %q", obs.SessionID, raw.SessionID)
	}
	if obs.Project != raw.Project {
		t.Errorf("Project = %q, want %q", obs.Project, raw.Project)
	}
	if obs.ToolName != "read" {
		t.Errorf("ToolName = %q, want %q", obs.ToolName, "read")
	}
	if len(obs.SourceFiles) != 1 || obs.SourceFiles[0] != "/proj/main.go" {
		t.Errorf("SourceFiles = %v, want [/proj/main.go]", obs.SourceFiles)
	}
	if obs.Text == "" {
		t.Error("Text is empty; the tool output must still be recorded")
	}
	if !contains(obs.Text, "package main") {
		t.Errorf("Text does not carry the tool output: %q", obs.Text)
	}
	if obs.CreatedAt.IsZero() {
		t.Error("CreatedAt is zero; the observation would be undatable")
	}
}

// The failure path and the deliberate no-model path must agree, or a reader
// cannot tell "compression broke" from "compression is off by design".
func TestNoopCompressor_MatchesFallbackObservation(t *testing.T) {
	raw := RawObservation{
		SessionID:  "sess-1",
		Project:    "/proj",
		ToolName:   "bash",
		ToolInput:  map[string]any{"command": "ls"},
		ToolOutput: map[string]any{"stdout": "a\nb"},
		Timestamp:  time.Now(),
	}

	fromNoop, err := NewNoopCompressor().CompressObservation(context.Background(), raw)
	if err != nil {
		t.Fatalf("CompressObservation: %v", err)
	}
	fromFallback := FallbackObservation(raw)

	if fromNoop.Title != fromFallback.Title {
		t.Errorf("Title = %q, want %q", fromNoop.Title, fromFallback.Title)
	}
	if fromNoop.Type != fromFallback.Type {
		t.Errorf("Type = %q, want %q", fromNoop.Type, fromFallback.Type)
	}
	if fromNoop.Text != fromFallback.Text {
		t.Errorf("Text differs between the two paths:\n noop:     %q\n fallback: %q",
			fromNoop.Text, fromFallback.Text)
	}
}

// The worker's own failure path must produce the same record the shared builder
// does, so the two cannot drift apart in a future edit.
func TestWorkerFallbackObservation_UsesSharedBuilder(t *testing.T) {
	w := NewWorker(newMockStore(), NewNoopCompressor(), 1)
	raw := RawObservation{
		SessionID:  "sess-1",
		Project:    "/proj",
		ToolName:   "grep",
		ToolInput:  map[string]any{"pattern": "foo"},
		ToolOutput: map[string]any{"matches": []any{}},
		Timestamp:  time.Now(),
	}

	got := w.fallbackObservation(raw)
	want := FallbackObservation(raw)

	if got.Title != want.Title || got.Text != want.Text || got.Type != want.Type {
		t.Errorf("worker fallback diverged from FallbackObservation:\n got: %+v\nwant: %+v", got, want)
	}
}

// A nil orchestrator must not panic and must not silently disable recording:
// the subagent compressor needs an orchestrator to spawn, so the model-free
// compressor is the only correct fallback.
func TestNewCompressor_Selection(t *testing.T) {
	tests := []struct {
		name     string
		compName string
		orch     *subagent.Orchestrator
		wantType string
	}{
		{name: "none", compName: "none", wantType: "noop"},
		{name: "unknown name degrades to noop", compName: "", wantType: "noop"},
		{name: "subagent without orchestrator degrades to noop", compName: "subagent", wantType: "noop"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NewCompressor(tt.compName, tt.orch)
			if _, ok := got.(*NoopCompressor); !ok {
				t.Errorf("got %T, want *NoopCompressor", got)
			}
		})
	}
}

// The subagent compressor is still selectable — the knob must actually work,
// otherwise "none" is not a choice but a removal.
func TestNewCompressor_SelectsSubagentCompressor(t *testing.T) {
	cfg := config.Config{}
	orch := subagent.NewOrchestrator(&cfg, "", nil)
	t.Cleanup(orch.Shutdown)

	got := NewCompressor("subagent", orch)
	if _, ok := got.(*SubagentCompressor); !ok {
		t.Errorf("got %T, want *SubagentCompressor", got)
	}
}
