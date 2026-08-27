package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"

	"github.com/dimetron/pi-go/internal/sop"
)

// compileEmbedded gives a test the real compiled graph. The diagram is only
// worth testing against the SOPs that ship, not a fixture that can drift.
func compileEmbedded(t *testing.T, name string) *sop.Compiled {
	t.Helper()
	def, err := sop.LoadEmbeddedDefinition(name)
	if err != nil {
		t.Fatalf("LoadEmbeddedDefinition(%q): %v", name, err)
	}
	c, err := sop.Compile(def, sop.DescribeFactory{})
	if err != nil {
		t.Fatalf("Compile(%q): %v", name, err)
	}
	return c
}

func TestSidebarGraphLines_Run(t *testing.T) {
	c := compileEmbedded(t, "run")
	got := plain(sidebarGraphLines(c.Order, c.GraphEdges(), map[string]stageStatus{
		"validate_spec": stageCompleted,
		"slices":        stageRunning,
	}, 20, testSidebarStyles()))

	for _, want := range []string{
		"✔ validate_spec", // completed
		"▶ slices",        // running
		"○ gates",         // not started
		"✗→ repair",       // FAIL branch
		"✓→ merge",        // PASS branch
		"│",               // spine connector
	} {
		if !strings.Contains(got, want) {
			t.Errorf("graph missing %q:\n%s", want, got)
		}
	}
}

// repair loops back to verify. The loop is the reason the diagram exists at
// all — a flat list cannot show it.
func TestSidebarGraphLines_ShowsLoopBack(t *testing.T) {
	c := compileEmbedded(t, "run")
	got := plain(sidebarGraphLines(c.Order, c.GraphEdges(), nil, 20, testSidebarStyles()))
	if !strings.Contains(got, "↺→ verify") {
		t.Errorf("loop-back edge not drawn:\n%s", got)
	}
}

// A review checkpoint is a node of its own in the compiled graph, but it reads
// as part of the stage it guards.
func TestSidebarGraphLines_ReviewIsAChild(t *testing.T) {
	c := compileEmbedded(t, "plan")
	got := plain(sidebarGraphLines(c.Order, c.GraphEdges(), map[string]stageStatus{
		"clarify.review": stageWaiting,
	}, 20, testSidebarStyles()))

	if !strings.Contains(got, "└ ⏸ review") {
		t.Errorf("waiting review not drawn as a child:\n%s", got)
	}
	if strings.Contains(got, "clarify.review") {
		t.Errorf("review node drawn as a top-level stage:\n%s", got)
	}
}

// The sidebar is a fixed 23 columns. A line that overflows would break the
// frame, which the render-integrity tests pin globally — catch it here first.
func TestSidebarGraphLines_FitsSidebarWidth(t *testing.T) {
	const innerW = SidebarWidth - 3
	for _, name := range []string{"run", "plan"} {
		t.Run(name, func(t *testing.T) {
			c := compileEmbedded(t, name)
			for _, line := range sidebarGraphLines(c.Order, c.GraphEdges(), nil, innerW, testSidebarStyles()) {
				if w := runewidth.StringWidth(ansi.Strip(line)); w > SidebarWidth {
					t.Errorf("line %q is %d cells wide, sidebar is %d", ansi.Strip(line), w, SidebarWidth)
				}
			}
		})
	}
}

func TestSidebarGraphLines_EmptyIsHidden(t *testing.T) {
	if got := sidebarGraphLines(nil, nil, nil, 20, testSidebarStyles()); got != nil {
		t.Errorf("expected no lines for an empty graph, got %d", len(got))
	}
}

// The diagram has to actually reach the panel: it was implemented and tested in
// isolation for a while without being wired into RenderSidebar at all.
func TestRenderSidebar_DrawsTheGraphUnderThePlanList(t *testing.T) {
	c := compileEmbedded(t, "plan")
	phases := []PlanPhase{
		{"Idea", true}, {"Requirements", true}, {"Research", false},
		{"Design", false}, {"Outline", false}, {"Plan", false}, {"Prompt", false},
	}
	in := SidebarRenderInput{
		Width: SidebarWidth, Height: 44, Mode: "plan", PlanPhases: phases,
		Graph: &SOPGraph{Order: c.Order, Edges: c.GraphEdges(), Status: planStageStatus(phases)},
	}

	got := ansi.Strip(RenderSidebar(in))
	for _, want := range []string{
		"[x] Requirements", // the list
		"▶ Research",       // the list's current phase
		"✔ clarify",        // the diagram, projected from the same phases
		"▶ research",
		"└─✓→ prompt", // a routed edge only the diagram can show
	} {
		if !strings.Contains(got, want) {
			t.Errorf("sidebar missing %q:\n%s", want, got)
		}
	}
}

// A short panel keeps the list and drops the diagram whole. sidebarFrame clips
// from the bottom, so the alternative is half a tree with no sign of the rest.
func TestRenderSidebar_DropsTheGraphWhenItCannotFit(t *testing.T) {
	c := compileEmbedded(t, "plan")
	phases := []PlanPhase{{"Idea", true}, {"Requirements", false}}
	in := SidebarRenderInput{
		Width: SidebarWidth, Height: 20, Mode: "plan", PlanPhases: phases,
		Graph: &SOPGraph{Order: c.Order, Edges: c.GraphEdges(), Status: planStageStatus(phases)},
	}

	got := ansi.Strip(RenderSidebar(in))
	if !strings.Contains(got, "[x] Idea") {
		t.Errorf("the stage list must survive a short panel:\n%s", got)
	}
	if strings.Contains(got, "clarify") {
		t.Errorf("the diagram should have been dropped whole:\n%s", got)
	}
}

// The run diagram follows the imperative phase until the engine drives it.
func TestRunStageStatusProjectsThePhase(t *testing.T) {
	c := compileEmbedded(t, "run")
	got := runStageStatus(c.Order, "gating")

	if got["validate_spec"] != stageCompleted || got["slices"] != stageCompleted {
		t.Errorf("stages before the current one should be complete: %v", got)
	}
	if got["gates"] != stageRunning {
		t.Errorf("gates status = %v, want running", got["gates"])
	}
	if _, set := got["merge"]; set {
		t.Errorf("a later stage should be untouched: %v", got)
	}
}
