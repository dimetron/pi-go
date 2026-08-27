package tui

import (
	"os"
	"path/filepath"
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
	} {
		if !strings.Contains(got, want) {
			t.Errorf("graph missing %q:\n%s", want, got)
		}
	}

	// The waterfall carries the order: each stage starts one column right of
	// the one before it, which is what replaced the drawn spine.
	if !strings.Contains(got, "  ✔ validate_spec") || !strings.Contains(got, "   ▶ slices") {
		t.Errorf("stages are not staggered:\n%s", got)
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
		"Plan",      // the section heading
		"2/7",       // the progress the checklist used to carry
		"✔ clarify", // the stages themselves
		"▶ research",
		"└ ✔ review", // a checkpoint, drawn under the stage it guards
		"✓→ prompt",  // a routed edge only the diagram can show
	} {
		if !strings.Contains(got, want) {
			t.Errorf("sidebar missing %q:\n%s", want, got)
		}
	}

	// The checklist and the diagram were two vocabularies for one thing. When
	// the diagram fits, it replaces the checklist rather than sitting under it.
	for _, gone := range []string{"[x] Requirements", "[ ] Outline"} {
		if strings.Contains(got, gone) {
			t.Errorf("the phase checklist should have been replaced by the diagram, found %q:\n%s", gone, got)
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
		t.Errorf("the checklist must survive a short panel:\n%s", got)
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

// The checklist is cached between agent events, so the cache must not be able
// to go stale silently — that would reproduce the frozen checklist the content
// check was meant to fix.
func TestPlanPhasesRefreshOnEvents(t *testing.T) {
	dir := t.TempDir()
	specDir := filepath.Join(dir, "specs", "demo")
	if err := os.MkdirAll(specDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(specDir, "rough-idea.md"),
		[]byte("# Rough Idea\n\nbuild it\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	m := &model{mode: "plan", planWorktreePath: dir, planTaskName: "demo"}

	before := m.currentPlanPhases()
	if !before[0].Done {
		t.Fatal("Idea should be done")
	}
	if before[3].Done {
		t.Fatal("Design should not be done yet")
	}

	// The agent writes design.md. Without an event the cache still answers.
	if err := os.WriteFile(filepath.Join(specDir, "design.md"),
		[]byte("# Design\n\nthe shape of it\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if m.currentPlanPhases()[3].Done {
		t.Error("cache should hold until an event invalidates it")
	}

	m.invalidatePlanPhases()
	if !m.currentPlanPhases()[3].Done {
		t.Error("Design should be done once the cache is invalidated")
	}
}

// A finished stage should not sit beside a review checkpoint that still reads
// as untouched, and a running stage has not reached its review yet.
func TestPlanStageStatusCarriesReviewCheckpoints(t *testing.T) {
	got := planStageStatus([]PlanPhase{
		{"Idea", true}, {"Requirements", true}, {"Research", false},
		{"Design", false}, {"Outline", false}, {"Plan", false}, {"Prompt", false},
	})

	if got["clarify"] != stageCompleted {
		t.Errorf("clarify = %v, want completed", got["clarify"])
	}
	if got["clarify.review"] != stageCompleted {
		t.Errorf("a completed stage's review = %v, want completed", got["clarify.review"])
	}
	if _, set := got["research.review"]; set {
		t.Error("a running stage's review should not be marked; it has not been reached")
	}
}
