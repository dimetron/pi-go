package palace

import (
	"context"
	"strings"
	"testing"
)

func TestToolDiaryWrite_Success(t *testing.T) {
	t.Parallel()

	db, err := OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	store := NewSQLitePalaceStore(db)
	p := NewWithStore(store, nil)
	defer p.Close()

	out, err := palaceDiaryWriteHandler(context.Background(), p, DiaryWriteToolInput{
		AgentName: "pi",
		Entry:     "Completed auth migration review today.",
		Topic:     "session-notes",
	})
	if err != nil {
		t.Fatalf("palaceDiaryWriteHandler: %v", err)
	}

	if !strings.Contains(out.Content, "Diary entry written") {
		t.Errorf("expected success message, got: %s", out.Content)
	}
	if !strings.Contains(out.Content, "pi") {
		t.Error("expected agent name in output")
	}
	if !strings.Contains(out.Content, "session-notes") {
		t.Error("expected topic in output")
	}
}

func TestToolDiaryWrite_NoTopic(t *testing.T) {
	t.Parallel()

	db, err := OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	store := NewSQLitePalaceStore(db)
	p := NewWithStore(store, nil)
	defer p.Close()

	out, err := palaceDiaryWriteHandler(context.Background(), p, DiaryWriteToolInput{
		AgentName: "pi",
		Entry:     "Quick note.",
	})
	if err != nil {
		t.Fatalf("palaceDiaryWriteHandler: %v", err)
	}

	if !strings.Contains(out.Content, "Diary entry written") {
		t.Errorf("expected success, got: %s", out.Content)
	}
	// Should not mention topic.
	if strings.Contains(out.Content, "topic") {
		t.Error("unexpected topic in output when none given")
	}
}

func TestToolDiaryWrite_MissingAgentName(t *testing.T) {
	t.Parallel()

	db, err := OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	store := NewSQLitePalaceStore(db)
	p := NewWithStore(store, nil)
	defer p.Close()

	out, err := palaceDiaryWriteHandler(context.Background(), p, DiaryWriteToolInput{
		Entry: "some entry",
	})
	if err != nil {
		t.Fatalf("palaceDiaryWriteHandler: %v", err)
	}

	if !strings.Contains(out.Content, "Error") {
		t.Errorf("expected error, got: %s", out.Content)
	}
}

func TestToolDiaryWrite_MissingEntry(t *testing.T) {
	t.Parallel()

	db, err := OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	store := NewSQLitePalaceStore(db)
	p := NewWithStore(store, nil)
	defer p.Close()

	out, err := palaceDiaryWriteHandler(context.Background(), p, DiaryWriteToolInput{
		AgentName: "pi",
	})
	if err != nil {
		t.Fatalf("palaceDiaryWriteHandler: %v", err)
	}

	if !strings.Contains(out.Content, "Error") {
		t.Errorf("expected error, got: %s", out.Content)
	}
}

func TestToolDiaryRead_Success(t *testing.T) {
	t.Parallel()

	db, err := OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	store := NewSQLitePalaceStore(db)
	p := NewWithStore(store, nil)
	defer p.Close()
	ctx := context.Background()

	// Write two entries.
	_, _ = palaceDiaryWriteHandler(ctx, p, DiaryWriteToolInput{
		AgentName: "pi", Entry: "First entry.", Topic: "testing",
	})
	_, _ = palaceDiaryWriteHandler(ctx, p, DiaryWriteToolInput{
		AgentName: "pi", Entry: "Second entry.", Topic: "review",
	})

	out, err := palaceDiaryReadHandler(ctx, p, DiaryReadToolInput{
		AgentName: "pi",
	})
	if err != nil {
		t.Fatalf("palaceDiaryReadHandler: %v", err)
	}

	if !strings.Contains(out.Content, "Diary for") {
		t.Errorf("expected diary header, got: %s", out.Content)
	}
	if !strings.Contains(out.Content, "First entry.") {
		t.Error("expected first entry in output")
	}
	if !strings.Contains(out.Content, "Second entry.") {
		t.Error("expected second entry in output")
	}
	if !strings.Contains(out.Content, "[testing]") {
		t.Error("expected topic tag in output")
	}
}

func TestToolDiaryRead_Empty(t *testing.T) {
	t.Parallel()

	db, err := OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	store := NewSQLitePalaceStore(db)
	p := NewWithStore(store, nil)
	defer p.Close()

	out, err := palaceDiaryReadHandler(context.Background(), p, DiaryReadToolInput{
		AgentName: "unknown-agent",
	})
	if err != nil {
		t.Fatalf("palaceDiaryReadHandler: %v", err)
	}

	if !strings.Contains(out.Content, "No diary entries") {
		t.Errorf("expected empty message, got: %s", out.Content)
	}
}

func TestToolDiaryRead_MissingAgentName(t *testing.T) {
	t.Parallel()

	db, err := OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	store := NewSQLitePalaceStore(db)
	p := NewWithStore(store, nil)
	defer p.Close()

	out, err := palaceDiaryReadHandler(context.Background(), p, DiaryReadToolInput{})
	if err != nil {
		t.Fatalf("palaceDiaryReadHandler: %v", err)
	}

	if !strings.Contains(out.Content, "Error") {
		t.Errorf("expected error, got: %s", out.Content)
	}
}

func TestToolDiary_SeparateAgents(t *testing.T) {
	t.Parallel()

	db, err := OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	store := NewSQLitePalaceStore(db)
	p := NewWithStore(store, nil)
	defer p.Close()
	ctx := context.Background()

	// Write entries for two agents.
	_, _ = palaceDiaryWriteHandler(ctx, p, DiaryWriteToolInput{
		AgentName: "pi", Entry: "Pi's note.",
	})
	_, _ = palaceDiaryWriteHandler(ctx, p, DiaryWriteToolInput{
		AgentName: "ralph", Entry: "Ralph's note.",
	})

	// Read pi's diary.
	piOut, _ := palaceDiaryReadHandler(ctx, p, DiaryReadToolInput{AgentName: "pi"})
	if !strings.Contains(piOut.Content, "Pi's note.") {
		t.Error("expected pi's note in pi's diary")
	}
	if strings.Contains(piOut.Content, "Ralph's note.") {
		t.Error("pi's diary should not contain ralph's note")
	}

	// Read ralph's diary.
	ralphOut, _ := palaceDiaryReadHandler(ctx, p, DiaryReadToolInput{AgentName: "ralph"})
	if !strings.Contains(ralphOut.Content, "Ralph's note.") {
		t.Error("expected ralph's note in ralph's diary")
	}
	if strings.Contains(ralphOut.Content, "Pi's note.") {
		t.Error("ralph's diary should not contain pi's note")
	}
}

func TestToolDiaryWrite_SearchableAsDrawer(t *testing.T) {
	t.Parallel()

	db, err := OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	store := NewSQLitePalaceStore(db)
	p := NewWithStore(store, nil)
	defer p.Close()
	ctx := context.Background()

	_, _ = palaceDiaryWriteHandler(ctx, p, DiaryWriteToolInput{
		AgentName: "pi",
		Entry:     "Reviewed the authentication middleware for vulnerabilities.",
		Topic:     "security",
	})

	// Verify the drawer was created in agent_pi wing.
	drawers, err := p.ListDrawers(ctx, DrawerFilter{Wing: "agent_pi"})
	if err != nil {
		t.Fatalf("ListDrawers: %v", err)
	}
	if len(drawers) == 0 {
		t.Fatal("expected diary entry to be filed as a drawer")
	}
	if drawers[0].Hall != "hall_diary" {
		t.Errorf("hall = %q, want 'hall_diary'", drawers[0].Hall)
	}
	if drawers[0].Room != "security" {
		t.Errorf("room = %q, want 'security' (from topic)", drawers[0].Room)
	}
}
