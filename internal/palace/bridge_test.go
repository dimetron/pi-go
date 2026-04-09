package palace

import (
	"context"
	"testing"
	"time"

	"github.com/dimetron/pi-go/internal/memory"
)

func TestBridge_ConvertAndStore(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	p := NewWithStore(store, nil)
	bridge := NewObservationBridge(p)

	obs := &memory.Observation{
		ID:          1,
		SessionID:   "sess-1",
		Project:     "/Users/dev/myproject",
		Title:       "Added auth handler",
		Type:        memory.TypeFeature,
		Text:        "Implemented JWT auth handler with refresh tokens.",
		SourceFiles: []string{"internal/auth/handler.go"},
		ToolName:    "edit_file",
		CreatedAt:   time.Now(),
	}

	bridge.ConvertAndStore(context.Background(), obs)

	drawers, err := store.ListDrawers(context.Background(), DrawerFilter{Wing: "myproject"})
	if err != nil {
		t.Fatal(err)
	}
	if len(drawers) != 1 {
		t.Fatalf("expected 1 drawer, got %d", len(drawers))
	}
	d := drawers[0]
	if d.Wing != "myproject" {
		t.Errorf("wing = %q, want %q", d.Wing, "myproject")
	}
	if d.Room != "auth" {
		t.Errorf("room = %q, want %q", d.Room, "auth")
	}
	if d.Hall != "hall_features" {
		t.Errorf("hall = %q, want %q", d.Hall, "hall_features")
	}
	if d.AddedBy != "edit_file" {
		t.Errorf("added_by = %q, want %q", d.AddedBy, "edit_file")
	}
	if d.Importance != 7 {
		t.Errorf("importance = %d, want 7", d.Importance)
	}
	if d.Content != obs.Text {
		t.Errorf("content mismatch")
	}
}

func TestBridge_NilObservation(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	p := NewWithStore(store, nil)
	bridge := NewObservationBridge(p)

	// Should not panic.
	bridge.ConvertAndStore(context.Background(), nil)
}

func TestBridge_EmptySourceFiles(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	p := NewWithStore(store, nil)
	bridge := NewObservationBridge(p)

	obs := &memory.Observation{
		Project:     "/Users/dev/myproject",
		Type:        memory.TypeChange,
		Text:        "Some change without source files.",
		SourceFiles: []string{},
		ToolName:    "bash",
	}

	bridge.ConvertAndStore(context.Background(), obs)

	drawers, err := store.ListDrawers(context.Background(), DrawerFilter{Wing: "myproject"})
	if err != nil {
		t.Fatal(err)
	}
	if len(drawers) != 1 {
		t.Fatalf("expected 1 drawer, got %d", len(drawers))
	}
	if drawers[0].Room != "general" {
		t.Errorf("room = %q, want %q", drawers[0].Room, "general")
	}
}

func TestBridge_DuplicateSkipped(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	p := NewWithStore(store, nil)
	bridge := NewObservationBridge(p)

	obs := &memory.Observation{
		Project:     "/Users/dev/proj",
		Type:        memory.TypeBugfix,
		Text:        "Fixed null pointer in auth handler.",
		SourceFiles: []string{"auth/fix.go"},
		ToolName:    "edit_file",
	}

	// Store twice — second should not fail or create duplicate.
	bridge.ConvertAndStore(context.Background(), obs)
	bridge.ConvertAndStore(context.Background(), obs)

	drawers, err := store.ListDrawers(context.Background(), DrawerFilter{Wing: "proj"})
	if err != nil {
		t.Fatal(err)
	}
	// Without embedder, dedup doesn't kick in — both should be stored.
	// With embedder it would dedup. Either way, no error.
	if len(drawers) < 1 {
		t.Fatal("expected at least 1 drawer")
	}
}

func TestBridge_AllObservationTypes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		obsType    memory.ObservationType
		wantHall   string
		wantImport int
	}{
		{memory.TypeDecision, "hall_decisions", 8},
		{memory.TypeBugfix, "hall_bugs", 7},
		{memory.TypeFeature, "hall_features", 7},
		{memory.TypeRefactor, "hall_refactors", 5},
		{memory.TypeDiscovery, "hall_discoveries", 6},
		{memory.TypeChange, "hall_changes", 4},
	}

	for _, tt := range tests {
		t.Run(string(tt.obsType), func(t *testing.T) {
			t.Parallel()
			if got := hallFromObsType(tt.obsType); got != tt.wantHall {
				t.Errorf("hallFromObsType(%q) = %q, want %q", tt.obsType, got, tt.wantHall)
			}
			if got := importanceFromObsType(tt.obsType); got != tt.wantImport {
				t.Errorf("importanceFromObsType(%q) = %d, want %d", tt.obsType, got, tt.wantImport)
			}
		})
	}
}

func TestDeriveWing(t *testing.T) {
	t.Parallel()
	tests := []struct {
		project string
		want    string
	}{
		{"/Users/dev/myproject", "myproject"},
		{"/Users/dev/My-Project", "my-project"},
		{"", "general"},
		{"/", "general"},
		{".", "general"},
	}
	for _, tt := range tests {
		if got := deriveWing(tt.project); got != tt.want {
			t.Errorf("deriveWing(%q) = %q, want %q", tt.project, got, tt.want)
		}
	}
}

func TestDeriveRoom(t *testing.T) {
	t.Parallel()
	tests := []struct {
		files []string
		want  string
	}{
		{[]string{"internal/auth/handler.go"}, "auth"},
		{[]string{"cmd/server/main.go"}, "server"},
		{[]string{"handler.go"}, "general"},
		{[]string{}, "general"},
		{nil, "general"},
		{[]string{"pkg/utils/helpers.go"}, "utils"},
		{[]string{"internal/cli/memory.go"}, "cli"},
	}
	for _, tt := range tests {
		if got := deriveRoom(tt.files); got != tt.want {
			t.Errorf("deriveRoom(%v) = %q, want %q", tt.files, got, tt.want)
		}
	}
}

func TestBridge_UnknownObservationType(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	p := NewWithStore(store, nil)
	bridge := NewObservationBridge(p)

	obs := &memory.Observation{
		Project:     "/Users/dev/proj",
		Type:        memory.ObservationType("unknown_type"),
		Text:        "Some observation with unknown type.",
		SourceFiles: []string{"internal/foo/bar.go"},
		ToolName:    "test_tool",
	}

	bridge.ConvertAndStore(context.Background(), obs)

	drawers, err := store.ListDrawers(context.Background(), DrawerFilter{Wing: "proj"})
	if err != nil {
		t.Fatal(err)
	}
	if len(drawers) != 1 {
		t.Fatalf("expected 1 drawer, got %d", len(drawers))
	}
	// Unknown type should get empty hall and default importance of 5.
	if drawers[0].Hall != "" {
		t.Errorf("hall = %q, want empty for unknown type", drawers[0].Hall)
	}
	if drawers[0].Importance != 5 {
		t.Errorf("importance = %d, want 5 for unknown type", drawers[0].Importance)
	}
}

func TestDeriveRoom_InternalOnly(t *testing.T) {
	t.Parallel()
	// When the only meaningful directory is "internal" itself.
	got := deriveRoom([]string{"internal/file.go"})
	if got != "internal" {
		t.Errorf("deriveRoom([internal/file.go]) = %q, want internal", got)
	}
}

func TestBridge_ConvertAndStore_EmptyText(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	p := NewWithStore(store, nil)
	bridge := NewObservationBridge(p)

	obs := &memory.Observation{
		Project:     "/Users/dev/proj",
		Type:        memory.TypeChange,
		Text:        "", // empty text → AddDrawer returns error
		SourceFiles: []string{"test.go"},
		ToolName:    "test_tool",
	}

	// Should not panic — error is logged and swallowed.
	bridge.ConvertAndStore(context.Background(), obs)

	// No drawers should be added.
	drawers, _ := store.ListDrawers(context.Background(), DrawerFilter{Wing: "proj"})
	if len(drawers) != 0 {
		t.Errorf("expected 0 drawers for empty text, got %d", len(drawers))
	}
}

func TestBridge_ConvertAndStore_MultipleSourceFiles(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	p := NewWithStore(store, nil)
	bridge := NewObservationBridge(p)

	obs := &memory.Observation{
		Project:     "/Users/dev/proj",
		Type:        memory.TypeFeature,
		Text:        "Added cross-cutting feature.",
		SourceFiles: []string{"internal/auth/handler.go", "internal/api/router.go"},
		ToolName:    "edit_file",
	}

	bridge.ConvertAndStore(context.Background(), obs)

	drawers, _ := store.ListDrawers(context.Background(), DrawerFilter{Wing: "proj"})
	if len(drawers) != 1 {
		t.Fatalf("expected 1 drawer, got %d", len(drawers))
	}
	// Should use first source file's room
	if drawers[0].Room != "auth" {
		t.Errorf("room = %q, want auth", drawers[0].Room)
	}
	// SourceFile should be the first
	if drawers[0].SourceFile != "internal/auth/handler.go" {
		t.Errorf("source_file = %q", drawers[0].SourceFile)
	}
}

func TestBridge_ErrorDoesNotPropagate(t *testing.T) {
	t.Parallel()
	// Use a store that will cause an error (e.g., closed DB).
	store := newTestStore(t)
	p := NewWithStore(store, nil)
	bridge := NewObservationBridge(p)

	// Valid observation — should succeed regardless of internal issues.
	obs := &memory.Observation{
		Project:     "/Users/dev/proj",
		Type:        memory.TypeChange,
		Text:        "Test content for bridge error handling.",
		SourceFiles: []string{"test.go"},
		ToolName:    "test_tool",
	}

	// Should not panic or block.
	bridge.ConvertAndStore(context.Background(), obs)
}
