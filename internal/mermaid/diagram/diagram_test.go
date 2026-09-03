package diagram

import (
	"strings"
	"testing"

	"github.com/dimetron/pi-go/internal/mermaid/renderer"
)

func assertCanvasContains(t *testing.T, c *renderer.Canvas, substr string) {
	t.Helper()
	out := c.ToString()
	if !strings.Contains(out, substr) {
		t.Errorf("output missing %q\n---\n%s\n---", substr, out)
	}
}

func assertCanvasNotEmpty(t *testing.T, c *renderer.Canvas) {
	t.Helper()
	out := c.ToString()
	if strings.TrimSpace(out) == "" {
		t.Error("canvas is empty")
	}
}

// ── Sequence ────────────────────────────────────────────────────────────────

func TestSequenceBasic(t *testing.T) {
	c := RenderSequence("sequenceDiagram\n  Alice->>Bob: Hello\n  Bob-->>Alice: Hi", false)
	assertCanvasContains(t, c, "Alice")
	assertCanvasContains(t, c, "Bob")
	assertCanvasContains(t, c, "Hello")
}

// ── Class Diagram ───────────────────────────────────────────────────────────

func TestClassDiagramBasic(t *testing.T) {
	c := RenderClassDiagram("classDiagram\n  class Animal {\n    +int age\n    +makeSound()\n  }", false)
	assertCanvasContains(t, c, "Animal")
	assertCanvasContains(t, c, "+int age")
}

// ── ER Diagram ──────────────────────────────────────────────────────────────

func TestERDiagramBasic(t *testing.T) {
	c := RenderERDiagram("erDiagram\n  CUSTOMER ||--o{ ORDER : places", false)
	assertCanvasContains(t, c, "CUSTOMER")
	assertCanvasContains(t, c, "ORDER")
}

// ── Pie Chart ───────────────────────────────────────────────────────────────

func TestPieChartCircle(t *testing.T) {
	c := RenderPieChart("pie\n  \"A\" : 60\n  \"B\" : 40", false, true, nil)
	assertCanvasContains(t, c, "A")
	assertCanvasContains(t, c, "B")
	assertCanvasNotEmpty(t, c)
}

func TestPieChartBraille(t *testing.T) {
	c := RenderPieChart("pie\n  \"X\" : 70\n  \"Y\" : 30", false, false, nil)
	assertCanvasContains(t, c, "X")
	assertCanvasContains(t, c, "⣿") // braille solid pattern
}

func TestPieChartASCII(t *testing.T) {
	c := RenderPieChart("pie\n  \"Go\" : 50\n  \"Rust\" : 50", true, false, nil)
	assertCanvasContains(t, c, "Go")
	assertCanvasContains(t, c, "#") // ASCII fill char
}

func TestPieChartMonochromatic(t *testing.T) {
	theme := renderer.GetTheme("amber")
	c := RenderPieChart("pie\n  \"A\" : 60\n  \"B\" : 40", false, true, &theme)
	assertCanvasNotEmpty(t, c)
}

// ── Git Graph ───────────────────────────────────────────────────────────────

func TestGitGraphBasic(t *testing.T) {
	c := RenderGitGraph("gitGraph\n  commit id: \"A\"\n  commit id: \"B\"", false)
	assertCanvasContains(t, c, "A")
	assertCanvasContains(t, c, "B")
	assertCanvasContains(t, c, "●")
}

// ── Block Diagram ───────────────────────────────────────────────────────────

func TestBlockDiagramBasic(t *testing.T) {
	c := RenderBlockDiagram("block-beta\n  columns 2\n  A[\"Hello\"] B[\"World\"]", false)
	assertCanvasContains(t, c, "Hello")
	assertCanvasContains(t, c, "World")
}

// ── State Diagram ───────────────────────────────────────────────────────────

func TestStateDiagramParse(t *testing.T) {
	g := ParseStateDiagram("stateDiagram-v2\n  [*] --> Idle\n  Idle --> Done\n  Done --> [*]")
	if len(g.Nodes) < 2 {
		t.Errorf("expected at least 2 nodes, got %d", len(g.Nodes))
	}
}

// ── Gantt ───────────────────────────────────────────────────────────────────

func TestGanttBasic(t *testing.T) {
	c := RenderGantt("gantt\n  title Test\n  dateFormat YYYY-MM-DD\n  section S1\n    Task1 :a1, 2024-01-01, 7d", false, nil)
	assertCanvasContains(t, c, "Test")
	assertCanvasContains(t, c, "Task1")
}

func TestGanttNoTasks(t *testing.T) {
	c := RenderGantt("gantt\n  title Empty", false, nil)
	assertCanvasContains(t, c, "no tasks")
}

// ── Timeline ────────────────────────────────────────────────────────────────

func TestTimelineBasic(t *testing.T) {
	c := RenderTimeline("timeline\n  title History\n  2020 : Event A\n  2021 : Event B", false, nil)
	assertCanvasContains(t, c, "History")
	assertCanvasContains(t, c, "Event A")
	assertCanvasContains(t, c, "●")
}

func TestTimelineVerticalLayout(t *testing.T) {
	// Directly test vertical layout path with many events
	td := parseTimeline("timeline\n  title Computing\n  1940 : ENIAC\n  1950 : UNIVAC\n  1960 : Mainframes\n  1970 : Minicomputers : UNIX\n  1980 : PCs\n  1990 : Web\n  2000 : Cloud\n  2010 : Mobile\n  2020 : AI")
	c := renderTimelineVertical(td, false, nil)
	assertCanvasNotEmpty(t, c)
	assertCanvasContains(t, c, "Computing")
	assertCanvasContains(t, c, "ENIAC")
	assertCanvasContains(t, c, "UNIX")
	assertCanvasContains(t, c, "AI")
	// Vertical layout should have period labels stacked vertically
	out := c.ToString()
	lines := strings.Split(out, "\n")
	if len(lines) < 20 {
		t.Errorf("vertical layout should be tall, got %d lines", len(lines))
	}
}

func TestTimelineVerticalASCII(t *testing.T) {
	td := parseTimeline("timeline\n  2020 : Alpha\n  2021 : Beta\n  2022 : Release")
	c := renderTimelineVertical(td, true, nil)
	assertCanvasNotEmpty(t, c)
	assertCanvasContains(t, c, "Alpha")
	assertCanvasContains(t, c, "|")
	assertCanvasContains(t, c, "o")
}

// ── Kanban ──────────────────────────────────────────────────────────────────

func TestKanbanBasic(t *testing.T) {
	c := RenderKanban("kanban\n  col1[Todo]\n    t1[Task A]\n  col2[Done]\n    t2[Task B]", false, nil)
	assertCanvasContains(t, c, "Todo")
	assertCanvasContains(t, c, "Task A")
	assertCanvasContains(t, c, "Done")
}

func TestKanbanThemed(t *testing.T) {
	theme := renderer.GetTheme("blueprint")
	c := RenderKanban("kanban\n  col1[A]\n    t1[X]\n  col2[B]\n    t2[Y]", false, &theme)
	assertCanvasNotEmpty(t, c)
}

// ── Journey ─────────────────────────────────────────────────────────────────

func TestJourneyBasic(t *testing.T) {
	src := "journey\n    title My day\n    section Work\n        Tea: 5: Me\n        Code: 1: Me, Bot"
	c := RenderJourney(src, false, nil)
	assertCanvasContains(t, c, "My day")
	assertCanvasContains(t, c, "Work")
	assertCanvasContains(t, c, "Tea")
	assertCanvasContains(t, c, "Code")
	assertCanvasContains(t, c, "Me")
	assertCanvasContains(t, c, "Bot")
	assertCanvasContains(t, c, ":D")  // score 5 face
	assertCanvasContains(t, c, ":((") // score 1 face
}

func TestJourneyScoreClamped(t *testing.T) {
	// Out-of-range and non-numeric scores fall back/clamp without panicking.
	c := RenderJourney("journey\n    section S\n        A: 99: X\n        B: nope: Y", false, nil)
	assertCanvasContains(t, c, ":D")  // 99 clamps to 5
	assertCanvasContains(t, c, ":-|") // "nope" -> default 3
}

func TestJourneyThemed(t *testing.T) {
	theme := renderer.GetTheme("blueprint")
	c := RenderJourney("journey\n    section S\n        A: 3: X", false, &theme)
	assertCanvasNotEmpty(t, c)
}

func TestJourneyEmpty(t *testing.T) {
	c := RenderJourney("journey", false, nil)
	assertCanvasContains(t, c, "no sections")
}

// ── Orientation ─────────────────────────────────────────────────────────────

func TestJourneyVerticalViaDirective(t *testing.T) {
	src := "journey\n    direction TB\n    title Day\n    section Work\n        Tea: 5: Me\n        Code: 2: Me, Bot"
	c := RenderJourney(src, false, nil)
	out := c.ToString()
	// Vertical layout is tall and narrow; horizontal is wide and short.
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) < 8 {
		t.Errorf("expected a tall vertical layout, got %d lines:\n%s", len(lines), out)
	}
	assertCanvasContains(t, c, "Tea")
	assertCanvasContains(t, c, "Code")
}

func TestJourneyHorizontalByDefault(t *testing.T) {
	// No directive, no override -> journey's natural default is horizontal.
	c := RenderJourney("journey\n    section S\n        A: 3: X\n        B: 4: Y", false, nil)
	lines := strings.Split(strings.TrimRight(c.ToString(), "\n"), "\n")
	if len(lines) > 9 {
		t.Errorf("expected a short horizontal layout, got %d lines", len(lines))
	}
}

func TestOrientationCLIOverridesDirective(t *testing.T) {
	src := "journey\n    direction TB\n    section S\n        A: 3: X"
	SetOrientationOverride("lr") // CLI says horizontal, directive says vertical
	defer SetOrientationOverride("")
	c := RenderJourney(src, false, nil)
	lines := strings.Split(strings.TrimRight(c.ToString(), "\n"), "\n")
	if len(lines) > 9 {
		t.Errorf("CLI --orientation lr should override 'direction TB'; got %d lines", len(lines))
	}
}

func TestResolveVerticalPrecedence(t *testing.T) {
	if resolveVertical("journey\n  section S", false) {
		t.Error("no directive/override should yield the default (false)")
	}
	if !resolveVertical("journey\n  direction TB\n  section S", false) {
		t.Error("'direction TB' directive should yield vertical")
	}
	if resolveVertical("journey\n  direction LR\n  section S", true) {
		t.Error("'direction LR' directive should yield horizontal even if default is vertical")
	}
	SetOrientationOverride("tb")
	defer SetOrientationOverride("")
	if !resolveVertical("journey\n  direction LR\n  section S", false) {
		t.Error("CLI override must win over the directive")
	}
}

func TestOrientationInvalidTokenIsNoop(t *testing.T) {
	if SetOrientationOverride("sideways") {
		t.Error("unrecognized token should report false")
	}
	defer SetOrientationOverride("")
	// Override not applied -> falls back to the supplied default.
	if resolveVertical("journey\n  section S", false) {
		t.Error("invalid override must not force vertical; expected default (false)")
	}
	if !resolveVertical("journey\n  section S", true) {
		t.Error("invalid override must not block the default (true)")
	}
}

func TestDirectionScopedToTopLevel(t *testing.T) {
	// `direction LR` nested in a subgraph governs that subgraph only — it
	// must not flip the whole diagram (Mermaid semantics).
	nested := "flowchart TB\n  subgraph G\n    direction LR\n    A --> B\n  end\n  C --> A"
	if !resolveVertical(nested, true) {
		t.Error("nested 'direction LR' must not override whole-diagram default")
	}
	// A top-level `direction` does govern the whole diagram.
	if resolveVertical("stateDiagram-v2\ndirection LR\n[*] --> A", true) {
		t.Error("top-level 'direction LR' should yield horizontal")
	}
	if !resolveVertical("journey\ndirection TB\nsection S", false) {
		t.Error("top-level 'direction TB' should yield vertical")
	}
}

func TestJourneyLeadingBlankAndComment(t *testing.T) {
	// Leading blank/comment lines before the header must not break parsing.
	for _, src := range []string{
		"\n\njourney\n    section S\n        Tea: 5: Me",
		"%% a note\njourney\n    section S\n        Tea: 5: Me",
	} {
		c := RenderJourney(src, false, nil)
		assertCanvasContains(t, c, "Tea")
	}
}

func TestPacketLeadingBlank(t *testing.T) {
	c := RenderPacket("\npacket-beta\n    0-7: \"A\"", false, nil)
	assertCanvasContains(t, c, "A")
}

// ── Packet ──────────────────────────────────────────────────────────────────

func TestPacketBasic(t *testing.T) {
	src := "packet-beta\n    0-15: \"Source Port\"\n    16-31: \"Destination Port\""
	c := RenderPacket(src, false, nil)
	assertCanvasContains(t, c, "Source Port")
	assertCanvasContains(t, c, "Destination Port")
	assertCanvasContains(t, c, "0")
	assertCanvasContains(t, c, "31")
}

func TestPacketAutoIncrement(t *testing.T) {
	// +N fields chain from the previous field's end bit.
	c := RenderPacket("packet-beta\n    +16: \"A\"\n    +16: \"B\"", false, nil)
	assertCanvasContains(t, c, "A")
	assertCanvasContains(t, c, "B")
	assertCanvasContains(t, c, "31") // 0..15 then 16..31
}

func TestPacketTruncationLegend(t *testing.T) {
	// A label too wide for a 1-bit field is truncated and listed in a legend.
	c := RenderPacket("packet-beta\n    0: \"VeryLongFieldName\"", false, nil)
	assertCanvasContains(t, c, "VeryLongFieldName [0]")
}

func TestPacketEmpty(t *testing.T) {
	c := RenderPacket("packet-beta", false, nil)
	assertCanvasContains(t, c, "no fields")
}

// ── Mindmap ─────────────────────────────────────────────────────────────────

func TestMindmapBasic(t *testing.T) {
	c := RenderMindmap("mindmap\n  root((Root))\n    Child1\n    Child2", false)
	assertCanvasContains(t, c, "Root")
	assertCanvasContains(t, c, "Child1")
	assertCanvasContains(t, c, "Child2")
}

// ── Quadrant ────────────────────────────────────────────────────────────────

func TestQuadrantBasic(t *testing.T) {
	c := RenderQuadrantChart("quadrantChart\n  title Test\n  x-axis A --> B\n  y-axis C --> D\n  Point1: [0.5, 0.5]", false, nil)
	assertCanvasContains(t, c, "Test")
	assertCanvasContains(t, c, "Point1")
	assertCanvasContains(t, c, "●")
}

// ── XY Chart ────────────────────────────────────────────────────────────────

func TestXYChartBasic(t *testing.T) {
	c := RenderXYChart("xychart-beta\n  title Rev\n  x-axis [a, b]\n  bar [10, 20]", false, nil)
	assertCanvasContains(t, c, "Rev")
	assertCanvasContains(t, c, "▓") // bar fill char
}

// ── Treemap ─────────────────────────────────────────────────────────────────

func TestTreemapBasic(t *testing.T) {
	c := RenderTreemap("treemap-beta\n  \"Section\"\n    \"Item\": 100", false, nil)
	assertCanvasContains(t, c, "Section")
	assertCanvasContains(t, c, "Item")
	assertCanvasContains(t, c, "100")
}
