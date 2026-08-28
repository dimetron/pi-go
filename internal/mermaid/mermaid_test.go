package mermaid

import (
	"strings"
	"testing"
)

// ── Helpers ──────────────────────────────────────────────────────────────────

func assertContains(t *testing.T, output, substr string) {
	t.Helper()
	if !strings.Contains(output, substr) {
		t.Errorf("output missing %q\n---\n%s\n---", substr, output)
	}
}

func assertNotContains(t *testing.T, output, substr string) {
	t.Helper()
	if strings.Contains(output, substr) {
		t.Errorf("output should not contain %q\n---\n%s\n---", substr, output)
	}
}

func assertNonEmpty(t *testing.T, output string) {
	t.Helper()
	if strings.TrimSpace(output) == "" {
		t.Error("output is empty")
	}
}

func assertReasonableDimensions(t *testing.T, output string) {
	t.Helper()
	lines := strings.Split(output, "\n")
	if len(lines) > 200 {
		t.Errorf("output has %d lines (max 200)", len(lines))
	}
	for _, line := range lines {
		if len(line) > 500 {
			t.Errorf("line width %d exceeds 500", len(line))
			break
		}
	}
}

func assertValidUnicode(t *testing.T, output string) {
	t.Helper()
	if strings.ContainsRune(output, '\ufffd') {
		t.Error("output contains Unicode replacement character")
	}
}

// ── Flowchart tests ──────────────────────────────────────────────────────────

func TestFlowchartLR(t *testing.T) {
	out := Render("graph LR\n  A --> B")
	assertContains(t, out, "A")
	assertContains(t, out, "B")
	assertContains(t, out, "►")
}

func TestFlowchartTD(t *testing.T) {
	out := Render("graph TD\n  A --> B")
	assertContains(t, out, "A")
	assertContains(t, out, "B")
	assertContains(t, out, "▼")
}

func TestFlowchartBT(t *testing.T) {
	out := Render("graph BT\n  A --> B")
	assertContains(t, out, "A")
	assertContains(t, out, "B")
	assertContains(t, out, "▲")
}

func TestFlowchartRL(t *testing.T) {
	out := Render("graph RL\n  A --> B")
	assertContains(t, out, "A")
	assertContains(t, out, "B")
	assertContains(t, out, "◄")
}

func TestFlowchartChain(t *testing.T) {
	out := Render("graph LR\n  A --> B --> C --> D --> E")
	for _, label := range []string{"A", "B", "C", "D", "E"} {
		assertContains(t, out, label)
	}
}

func TestFlowchartSingleNode(t *testing.T) {
	out := Render("graph LR\n  A")
	assertContains(t, out, "A")
	assertNotContains(t, out, "►")
}

func TestFlowchartDiamond(t *testing.T) {
	out := Render("graph LR\n  A{Decision}")
	assertContains(t, out, "Decision")
	assertContains(t, out, "⟋") // chamfered diamond corners
}

func TestFlowchartRounded(t *testing.T) {
	out := Render("graph LR\n  A(Rounded)")
	assertContains(t, out, "Rounded")
	assertContains(t, out, "╭")
}

func TestFlowchartCircle(t *testing.T) {
	out := Render("graph LR\n  A((Circle))")
	assertContains(t, out, "Circle")
	assertContains(t, out, "◯")
}

func TestFlowchartEdgeLabels(t *testing.T) {
	out := Render("graph LR\n  A -->|Yes| B")
	assertContains(t, out, "A")
	assertContains(t, out, "B")
	assertContains(t, out, "Yes")
}

func TestFlowchartDottedEdge(t *testing.T) {
	out := Render("graph LR\n  A -.-> B")
	assertContains(t, out, "A")
	assertContains(t, out, "B")
	assertContains(t, out, "┄")
}

func TestFlowchartThickEdge(t *testing.T) {
	out := Render("graph LR\n  A ==> B")
	assertContains(t, out, "A")
	assertContains(t, out, "B")
	assertContains(t, out, "━")
}

func TestFlowchartSubgraphs(t *testing.T) {
	out := Render(`graph TB
    subgraph Frontend
        A[Web]
    end
    subgraph Backend
        B[API]
    end
    A --> B`)
	assertContains(t, out, "Frontend")
	assertContains(t, out, "Backend")
	assertContains(t, out, "Web")
	assertContains(t, out, "API")
}

func TestFlowchartASCII(t *testing.T) {
	out := Render("graph LR\n  A --> B", WithASCII())
	assertContains(t, out, "A")
	assertContains(t, out, "B")
	assertContains(t, out, ">")
	assertNotContains(t, out, "►")
	assertNotContains(t, out, "─")
	assertNotContains(t, out, "│")
}

func TestFlowchartBidirectional(t *testing.T) {
	out := Render("graph LR\n  A <--> B")
	assertContains(t, out, "◄")
	assertContains(t, out, "►")
}

func TestFlowchartSemicolonSyntax(t *testing.T) {
	out := Render("graph LR; A --> B; B --> C")
	assertContains(t, out, "A")
	assertContains(t, out, "B")
	assertContains(t, out, "C")
}

func TestFlowchartFrontmatterStripped(t *testing.T) {
	out := Render("---\ntitle: Test\n---\ngraph LR\n  A --> B")
	assertContains(t, out, "A")
	assertContains(t, out, "B")
}

func TestFlowchartDimensions(t *testing.T) {
	out := Render("graph TD\n  A --> B --> C\n  A --> D --> C")
	assertReasonableDimensions(t, out)
	assertValidUnicode(t, out)
}

// ── Sequence diagram tests ──────────────────────────────────────────────────

func TestSequenceBasic(t *testing.T) {
	out := Render(`sequenceDiagram
    Alice->>Bob: Hello
    Bob-->>Alice: Hi`)
	assertContains(t, out, "Alice")
	assertContains(t, out, "Bob")
	assertContains(t, out, "Hello")
	assertContains(t, out, "Hi")
	assertContains(t, out, "►")
}

func TestSequenceMultipleParticipants(t *testing.T) {
	out := Render(`sequenceDiagram
    participant A
    participant B
    participant C
    A->>B: msg1
    B->>C: msg2`)
	assertContains(t, out, "msg1")
	assertContains(t, out, "msg2")
}

func TestSequenceNotes(t *testing.T) {
	out := Render(`sequenceDiagram
    Alice->>Bob: Hello
    Note right of Alice: A note`)
	assertContains(t, out, "A note")
}

func TestSequenceLoopBlock(t *testing.T) {
	out := Render(`sequenceDiagram
    Alice->>Bob: Hello
    loop Every sec
        Bob->>Alice: Ping
    end`)
	assertContains(t, out, "loop")
}

// ── Class diagram tests ─────────────────────────────────────────────────────

func TestClassDiagramBasic(t *testing.T) {
	out := Render(`classDiagram
    Animal <|-- Duck
    Animal : +makeSound()
    Duck : +swim()`)
	assertContains(t, out, "Animal")
	assertContains(t, out, "Duck")
	assertContains(t, out, "+makeSound()")
	assertContains(t, out, "+swim()")
}

func TestClassDiagramAnnotation(t *testing.T) {
	out := Render(`classDiagram
    class Shape {
        <<interface>>
        +area() float
    }`)
	assertContains(t, out, "Shape")
	assertContains(t, out, "interface")
}

// ── ER diagram tests ────────────────────────────────────────────────────────

func TestERDiagramBasic(t *testing.T) {
	out := Render(`erDiagram
    CUSTOMER ||--o{ ORDER : places
    CUSTOMER {
        int id PK
        string name
    }`)
	assertContains(t, out, "CUSTOMER")
	assertContains(t, out, "ORDER")
	assertContains(t, out, "places")
}

// ── State diagram tests ─────────────────────────────────────────────────────

func TestStateDiagramBasic(t *testing.T) {
	out := Render(`stateDiagram-v2
    [*] --> Idle
    Idle --> Active
    Active --> [*]`)
	assertContains(t, out, "Idle")
	assertContains(t, out, "Active")
	assertContains(t, out, "●")
}

// ── Pie chart tests ─────────────────────────────────────────────────────────

func TestPieChartBasic(t *testing.T) {
	out := Render(`pie title Pets
    "Dogs" : 45
    "Cats" : 30`)
	assertContains(t, out, "Dogs")
	assertContains(t, out, "Cats")
	assertContains(t, out, "⣿") // braille solid pattern (no-theme mode)
}

func TestPieChartShowData(t *testing.T) {
	out := Render(`pie showData
    "A" : 60
    "B" : 40`)
	assertContains(t, out, "(60)")
	assertContains(t, out, "(40)")
}

// ── Git graph tests ─────────────────────────────────────────────────────────

func TestGitGraphBasic(t *testing.T) {
	out := Render(`gitGraph
    commit id: "A"
    commit id: "B"
    branch dev
    checkout dev
    commit id: "C"`)
	assertContains(t, out, "main")
	assertContains(t, out, "dev")
	assertContains(t, out, "A")
	assertContains(t, out, "B")
	assertContains(t, out, "C")
	assertContains(t, out, "●")
}

func TestGitGraphMerge(t *testing.T) {
	out := Render(`gitGraph
    commit id: "A"
    branch dev
    checkout dev
    commit id: "B"
    checkout main
    merge dev id: "C"`)
	assertContains(t, out, "C")
	// Should have T-junctions, not crosses
	assertContains(t, out, "┬")
	assertContains(t, out, "┴")
}

func TestGitGraphTags(t *testing.T) {
	out := Render(`gitGraph
    commit id: "init" tag: "v1.0"`)
	assertContains(t, out, "[v1.0]")
}

func TestGitGraphTB(t *testing.T) {
	out := Render(`gitGraph TB:
    commit id: "A"
    commit id: "B"`)
	assertContains(t, out, "A")
	assertContains(t, out, "B")
	assertNonEmpty(t, out)
}

// ── Block diagram tests ─────────────────────────────────────────────────────

func TestBlockDiagramBasic(t *testing.T) {
	out := Render(`block-beta
    columns 2
    A["Hello"] B["World"]`)
	assertContains(t, out, "Hello")
	assertContains(t, out, "World")
}

func TestBlockDiagramLinks(t *testing.T) {
	out := Render(`block-beta
    columns 3
    A["In"] B["Mid"] C["Out"]
    A-->B
    B-->C`)
	assertContains(t, out, "In")
	assertContains(t, out, "Out")
}

// ── Treemap tests ───────────────────────────────────────────────────────────

func TestTreemapBasic(t *testing.T) {
	out := Render(`treemap-beta
    "Root"
        "Leaf A": 30
        "Leaf B": 20`)
	assertContains(t, out, "Root")
	assertContains(t, out, "Leaf A")
	assertContains(t, out, "Leaf B")
	assertContains(t, out, "┄") // dashed borders for section
}

// ── Error handling tests ────────────────────────────────────────────────────

func TestRenderEmpty(t *testing.T) {
	out := Render("")
	// Should not panic
	_ = out
}

func TestRenderGarbage(t *testing.T) {
	out := Render("this is not valid mermaid")
	// Should not panic, may produce something or be empty
	_ = out
}

func TestRenderReturnsString(t *testing.T) {
	out := Render("graph LR\n  A --> B")
	if out == "" {
		t.Error("expected non-empty output")
	}
}

// ── Option tests ────────────────────────────────────────────────────────────

func TestWithPadding(t *testing.T) {
	narrow := Render("graph LR\n  A --> B", WithPadding(2, 1))
	wide := Render("graph LR\n  A --> B", WithPadding(8, 4))
	// Wider vertical padding = taller output
	narrowLines := strings.Split(narrow, "\n")
	wideLines := strings.Split(wide, "\n")
	if len(wideLines) <= len(narrowLines) {
		t.Error("wider padding should produce taller output")
	}
}

func TestWithSharpEdges(t *testing.T) {
	out := Render("graph TD\n  A --> B --> C\n  A --> D --> C", WithSharpEdges())
	// Sharp edges: routing uses ┌└ instead of ╭╰
	assertContains(t, out, "└")
	assertNotContains(t, out, "╰")
}

// ── Diagram type detection tests ────────────────────────────────────────────

func TestDetectFlowchart(t *testing.T) {
	if dt := detectDiagramType("graph LR\n  A-->B"); dt != "flowchart" {
		t.Errorf("expected flowchart, got %s", dt)
	}
	if dt := detectDiagramType("flowchart TD\n  A-->B"); dt != "flowchart" {
		t.Errorf("expected flowchart, got %s", dt)
	}
}

func TestDetectSequence(t *testing.T) {
	if dt := detectDiagramType("sequenceDiagram\n  A->>B: hi"); dt != "sequence" {
		t.Errorf("expected sequence, got %s", dt)
	}
}

func TestDetectClass(t *testing.T) {
	if dt := detectDiagramType("classDiagram\n  A <|-- B"); dt != "class" {
		t.Errorf("expected class, got %s", dt)
	}
}

func TestDetectER(t *testing.T) {
	if dt := detectDiagramType("erDiagram\n  A ||--o{ B : has"); dt != "er" {
		t.Errorf("expected er, got %s", dt)
	}
}

func TestDetectState(t *testing.T) {
	if dt := detectDiagramType("stateDiagram-v2\n  [*] --> A"); dt != "state" {
		t.Errorf("expected state, got %s", dt)
	}
}

func TestDetectPie(t *testing.T) {
	if dt := detectDiagramType("pie\n  \"A\": 50"); dt != "pie" {
		t.Errorf("expected pie, got %s", dt)
	}
}

func TestDetectGitGraph(t *testing.T) {
	if dt := detectDiagramType("gitGraph\n  commit"); dt != "gitgraph" {
		t.Errorf("expected gitgraph, got %s", dt)
	}
}

func TestDetectTreemap(t *testing.T) {
	if dt := detectDiagramType("treemap-beta\n  \"A\": 10"); dt != "treemap" {
		t.Errorf("expected treemap, got %s", dt)
	}
}

func TestDetectJourney(t *testing.T) {
	if dt := detectDiagramType("journey\n  title Day\n  section S\n    A: 3: X"); dt != "journey" {
		t.Errorf("expected journey, got %s", dt)
	}
}

func TestDetectPacket(t *testing.T) {
	if dt := detectDiagramType("packet-beta\n  0-7: \"A\""); dt != "packet" {
		t.Errorf("expected packet, got %s", dt)
	}
	if dt := detectDiagramType("packet\n  0-7: \"A\""); dt != "packet" {
		t.Errorf("expected packet (no -beta), got %s", dt)
	}
}

func TestRenderJourneyDispatch(t *testing.T) {
	out := Render("journey\n  title Day\n  section Work\n    Tea: 5: Me")
	if !strings.Contains(out, "Tea") || !strings.Contains(out, "Work") {
		t.Errorf("journey did not dispatch/render:\n%s", out)
	}
}

func TestRenderPacketDispatch(t *testing.T) {
	out := Render("packet-beta\n  0-15: \"Src\"\n  16-31: \"Dst\"")
	if !strings.Contains(out, "Src") || !strings.Contains(out, "Dst") {
		t.Errorf("packet did not dispatch/render:\n%s", out)
	}
}

func TestStripFrontmatter(t *testing.T) {
	input := "---\ntitle: Test\n---\ngraph LR\n  A --> B"
	result := stripFrontmatter(input)
	if strings.Contains(result, "---") {
		t.Error("frontmatter not stripped")
	}
	if !strings.Contains(result, "graph LR") {
		t.Error("content after frontmatter missing")
	}
}
