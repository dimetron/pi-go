package diagram

import "testing"

// ── findClearMidY tests ──────────────────────────────────────────

func TestFindClearMidY_NoObstacles(t *testing.T) {
	src := &erBoxInfo{x: 5, y: 0, width: 10, height: 3}
	tgt := &erBoxInfo{x: 5, y: 10, width: 10, height: 3}
	boxes := map[string]*erBoxInfo{"src": src, "tgt": tgt}

	mid := findClearMidY(3, 10, 10, 10, src, tgt, boxes)
	expected := (3 + 10) / 2
	if mid != expected {
		t.Errorf("no obstacles: expected midpoint %d, got %d", expected, mid)
	}
}

func TestFindClearMidY_SingleObstacle(t *testing.T) {
	src := &erBoxInfo{x: 0, y: 0, width: 10, height: 3}
	tgt := &erBoxInfo{x: 20, y: 14, width: 10, height: 3}
	obstacle := &erBoxInfo{x: 5, y: 6, width: 12, height: 4} // y=6..9
	boxes := map[string]*erBoxInfo{"src": src, "tgt": tgt, "obs": obstacle}

	mid := findClearMidY(3, 14, 5, 25, src, tgt, boxes)

	// Should avoid the obstacle (y=6..9)
	if mid >= obstacle.y && mid < obstacle.y+obstacle.height {
		t.Errorf("routed through obstacle: midY=%d, obstacle y=%d..%d",
			mid, obstacle.y, obstacle.y+obstacle.height)
	}
}

func TestFindClearMidY_AllObstructed(t *testing.T) {
	src := &erBoxInfo{x: 0, y: 0, width: 10, height: 3}
	tgt := &erBoxInfo{x: 20, y: 10, width: 10, height: 3}
	// Obstacle fills the entire gap
	obstacle := &erBoxInfo{x: 5, y: 3, width: 12, height: 7} // y=3..9
	boxes := map[string]*erBoxInfo{"src": src, "tgt": tgt, "obs": obstacle}

	mid := findClearMidY(3, 10, 5, 25, src, tgt, boxes)

	// Should route outside the obstacle — either above or below
	if mid >= obstacle.y && mid < obstacle.y+obstacle.height {
		t.Errorf("fallback still hit obstacle: midY=%d, obstacle y=%d..%d",
			mid, obstacle.y, obstacle.y+obstacle.height)
	}
}

// ── erTBPorts tests ──────────────────────────────────────────────

func TestERTBPorts_VerticallyAligned(t *testing.T) {
	top := &erBoxInfo{x: 10, y: 0, width: 10, height: 3}
	bot := &erBoxInfo{x: 10, y: 7, width: 10, height: 3}

	topX, topY, botX, botY := erTBPorts(top, bot)

	// Should exit from center-bottom of top, enter center-top of bot
	expectTopX := top.x + top.width/2
	if topX != expectTopX {
		t.Errorf("topX: expected %d, got %d", expectTopX, topX)
	}
	if topY != top.y+top.height {
		t.Errorf("topY: expected %d (bottom edge), got %d", top.y+top.height, topY)
	}
	if botX != expectTopX {
		t.Errorf("botX: expected %d, got %d", expectTopX, botX)
	}
	if botY != bot.y {
		t.Errorf("botY: expected %d (top edge), got %d", bot.y, botY)
	}
}

func TestERTBPorts_OffsetRight(t *testing.T) {
	top := &erBoxInfo{x: 0, y: 0, width: 10, height: 3}
	bot := &erBoxInfo{x: 20, y: 7, width: 10, height: 3}

	topX, topY, botX, botY := erTBPorts(top, bot)

	// Should use side edges: right of top, left of bot
	if topX != top.x+top.width {
		t.Errorf("topX: expected %d (right edge), got %d", top.x+top.width, topX)
	}
	if topY != top.y+top.height/2 {
		t.Errorf("topY: expected %d (center), got %d", top.y+top.height/2, topY)
	}
	if botX != bot.x {
		t.Errorf("botX: expected %d (left edge), got %d", bot.x, botX)
	}
	if botY != bot.y+bot.height/2 {
		t.Errorf("botY: expected %d (center), got %d", bot.y+bot.height/2, botY)
	}
}

func TestERTBPorts_OffsetLeft(t *testing.T) {
	top := &erBoxInfo{x: 20, y: 0, width: 10, height: 3}
	bot := &erBoxInfo{x: 0, y: 7, width: 10, height: 3}

	topX, topY, botX, botY := erTBPorts(top, bot)

	// Should use side edges: left of top, right of bot
	if topX != top.x {
		t.Errorf("topX: expected %d (left edge), got %d", top.x, topX)
	}
	if botX != bot.x+bot.width {
		t.Errorf("botX: expected %d (right edge), got %d", bot.x+bot.width, botX)
	}
	_ = topY
	_ = botY
}

func TestERTBPorts_OverlappingX(t *testing.T) {
	top := &erBoxInfo{x: 5, y: 0, width: 20, height: 3}
	bot := &erBoxInfo{x: 10, y: 7, width: 20, height: 3}

	topX, topY, botX, botY := erTBPorts(top, bot)

	// Overlapping but not aligned — should use center-bottom/top
	if topY != top.y+top.height {
		t.Errorf("topY: expected %d (bottom edge), got %d", top.y+top.height, topY)
	}
	if botY != bot.y {
		t.Errorf("botY: expected %d (top edge), got %d", bot.y, botY)
	}
	_ = topX
	_ = botX
}

// ── Integration: RenderERDiagram smoke tests ─────────────────────

func TestRenderER_TwoEntities(t *testing.T) {
	source := `erDiagram
    CUSTOMER ||--o{ ORDER : places`
	canvas := RenderERDiagram(source, false)
	if canvas == nil {
		t.Fatal("nil canvas")
	}
	out := canvas.ToString()
	if out == "" {
		t.Error("empty output")
	}
	// Both entity names should appear in full
	if !containsText(out, "CUSTOMER") {
		t.Error("missing CUSTOMER label")
	}
	if !containsText(out, "ORDER") {
		t.Error("missing ORDER label")
	}
}

func TestRenderER_FourEntities_LabelsIntact(t *testing.T) {
	source := `erDiagram
    USER ||--o{ ORDER : places
    ORDER ||--|{ LINE_ITEM : contains
    PRODUCT ||--o{ LINE_ITEM : includes
    ORDER ||--|| PAYMENT : "paid by"`
	canvas := RenderERDiagram(source, false)
	if canvas == nil {
		t.Fatal("nil canvas")
	}
	out := canvas.ToString()
	for _, name := range []string{"USER", "ORDER", "LINE_ITEM", "PRODUCT", "PAYMENT"} {
		if !containsText(out, name) {
			t.Errorf("label %q truncated or missing in output", name)
		}
	}
}

func containsText(canvas, text string) bool {
	// Check if text appears in the canvas with possible surrounding spaces
	for _, line := range splitLines(canvas) {
		cleaned := ""
		for _, r := range line {
			if r >= 32 && r < 127 {
				cleaned += string(r)
			}
		}
		if len(cleaned) > 0 && contains(cleaned, text) {
			return true
		}
	}
	return false
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && findSubstring(s, substr)
}

func findSubstring(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
