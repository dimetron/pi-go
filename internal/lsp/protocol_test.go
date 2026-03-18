package lsp

import (
	"encoding/json"
	"testing"
)

func TestDiagnosticSeverityString(t *testing.T) {
	tests := []struct {
		severity int
		want     string
	}{
		{SeverityError, "error"},
		{SeverityWarning, "warning"},
		{SeverityInformation, "info"},
		{SeverityHint, "hint"},
		{0, "unknown"},
		{99, "unknown"},
	}
	for _, tt := range tests {
		d := &Diagnostic{Severity: tt.severity}
		got := d.SeverityString()
		if got != tt.want {
			t.Errorf("SeverityString() for severity %d = %q, want %q", tt.severity, got, tt.want)
		}
	}
}

func TestResponseError_Error(t *testing.T) {
	e := &ResponseError{Code: -32600, Message: "invalid request"}
	if e.Error() != "invalid request" {
		t.Errorf("Error() = %q, want %q", e.Error(), "invalid request")
	}
}

func TestParseLocations_Null(t *testing.T) {
	locs, err := parseLocations(json.RawMessage("null"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if locs != nil {
		t.Errorf("expected nil for null input, got %v", locs)
	}
}

func TestParseLocations_Empty(t *testing.T) {
	locs, err := parseLocations(json.RawMessage(""))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if locs != nil {
		t.Errorf("expected nil for empty input, got %v", locs)
	}
}

func TestParseLocations_Array(t *testing.T) {
	raw := json.RawMessage(`[{"uri":"file:///test.go","range":{"start":{"line":0,"character":0},"end":{"line":0,"character":5}}}]`)
	locs, err := parseLocations(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(locs) != 1 {
		t.Fatalf("expected 1 location, got %d", len(locs))
	}
	if locs[0].URI != "file:///test.go" {
		t.Errorf("URI = %q, want file:///test.go", locs[0].URI)
	}
}

func TestParseLocations_SingleObject(t *testing.T) {
	raw := json.RawMessage(`{"uri":"file:///single.go","range":{"start":{"line":5,"character":10},"end":{"line":5,"character":15}}}`)
	locs, err := parseLocations(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(locs) != 1 {
		t.Fatalf("expected 1 location, got %d", len(locs))
	}
	if locs[0].URI != "file:///single.go" {
		t.Errorf("URI = %q", locs[0].URI)
	}
	if locs[0].Range.Start.Line != 5 {
		t.Errorf("start line = %d, want 5", locs[0].Range.Start.Line)
	}
}

func TestParseLocations_InvalidJSON(t *testing.T) {
	raw := json.RawMessage(`{invalid json}`)
	_, err := parseLocations(raw)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestPathToURI(t *testing.T) {
	got := pathToURI("/tmp/test.go")
	if got != "file:///tmp/test.go" {
		t.Errorf("pathToURI(/tmp/test.go) = %q, want file:///tmp/test.go", got)
	}
}

func TestEditBefore_SameLine(t *testing.T) {
	a := TextEdit{Range: Range{Start: Position{Line: 5, Character: 10}}}
	b := TextEdit{Range: Range{Start: Position{Line: 5, Character: 5}}}

	if !editBefore(a, b) {
		t.Error("expected a (char 10) before b (char 5) in reverse order")
	}
	if editBefore(b, a) {
		t.Error("expected b (char 5) NOT before a (char 10)")
	}
}
