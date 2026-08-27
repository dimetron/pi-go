package specdoc

import (
	"os"
	"path/filepath"
	"testing"
)

const samplePrompt = "# Mistral Provider\n" +
	"\n" +
	"## Objective\n" +
	"Add a Mistral provider.\n" +
	"\n" +
	"## Implementation Slices\n" +
	"\n" +
	"1. **Slice 1 — Routing fix** — repair provider routing, files: `internal/provider/route.go`, `internal/provider/route_test.go`,\n" +
	"   verify: `go test ./internal/provider/...`, parallel-safe: no\n" +
	"\n" +
	"2. **Slice 2 — Model catalog** — add the catalog entries.\n" +
	"   files: `internal/provider/catalog.go`\n" +
	"   verify: `go build ./...`\n" +
	"   parallel-safe: yes\n" +
	"\n" +
	"## Done Criteria\n" +
	"- [ ] `pi --provider mistral` streams a response\n" +
	"- [ ] No slice is left as a stub\n" +
	"\n" +
	"## Gates\n" +
	"- **build**: `make build`\n" +
	"- **test**: `make test`\n" +
	"- vet: `make vet`\n" +
	"\n" +
	"## Reference\n" +
	"- Plan: `specs/x/plan.md`\n" +
	"- Design: `specs/x/design.md`\n"

func TestParseGates(t *testing.T) {
	gates := ParseGates(samplePrompt)
	if len(gates) != 3 {
		t.Fatalf("got %d gates, want 3: %+v", len(gates), gates)
	}
	want := map[string]string{"build": "make build", "test": "make test", "vet": "make vet"}
	for _, g := range gates {
		if want[g.Name] != g.Command {
			t.Errorf("gate %q command = %q, want %q", g.Name, g.Command, want[g.Name])
		}
		if g.Line == 0 {
			t.Errorf("gate %q has no line number", g.Name)
		}
	}
}

func TestParseGatesStopsAtNextSection(t *testing.T) {
	// The Reference section also matches the gate pattern; it must not leak in.
	for _, g := range ParseGates(samplePrompt) {
		if g.Name == "Plan" || g.Name == "Design" {
			t.Errorf("Reference entry %q leaked into gates", g.Name)
		}
	}
}

func TestParsePromptSlices(t *testing.T) {
	slices := ParsePromptSlices(samplePrompt)
	if len(slices) != 2 {
		t.Fatalf("got %d slices, want 2: %+v", len(slices), slices)
	}

	s1 := slices[0]
	if s1.Title != "Slice 1 — Routing fix" {
		t.Errorf("slice 1 title = %q", s1.Title)
	}
	if len(s1.Files) != 2 {
		t.Errorf("slice 1 files = %v, want 2 entries", s1.Files)
	}
	if s1.Verify != "go test ./internal/provider/..." {
		t.Errorf("slice 1 verify = %q", s1.Verify)
	}
	if !s1.HasParallel || s1.ParallelSafe {
		t.Errorf("slice 1 parallel-safe = (%v,%v), want stated=true safe=false", s1.HasParallel, s1.ParallelSafe)
	}

	// Slice 2's detail wraps across lines; it must still be read whole.
	s2 := slices[1]
	if s2.Verify != "go build ./..." {
		t.Errorf("slice 2 verify = %q, want the wrapped value", s2.Verify)
	}
	if !s2.ParallelSafe {
		t.Errorf("slice 2 parallel-safe = false, want true")
	}
}

func TestDoneCriteria(t *testing.T) {
	got := DoneCriteria(samplePrompt)
	if len(got) != 2 {
		t.Fatalf("got %d criteria, want 2: %v", len(got), got)
	}
}

func TestReferences(t *testing.T) {
	got := References(samplePrompt)
	if len(got) != 2 {
		t.Fatalf("got %d references, want 2: %v", len(got), got)
	}
}

func TestParsePlanSlicesCheckboxes(t *testing.T) {
	plan := "# Plan\n\n## Progress\n\n" +
		"- [x] Step 1: Model Roles — Config\n" +
		"- [ ] Step 2: Git Tools — git-overview\n" +
		"- [ ] Slice 3: Subagent pool\n"
	got := ParsePlanSlices(plan)
	if len(got) != 3 {
		t.Fatalf("got %d slices, want 3", len(got))
	}
	if !got[0].Done || got[1].Done {
		t.Errorf("done flags wrong: %v %v", got[0].Done, got[1].Done)
	}
	if got[0].Title != "Model Roles — Config" {
		t.Errorf("title = %q, want the Step prefix stripped", got[0].Title)
	}
	if got[2].Title != "Subagent pool" {
		t.Errorf("title = %q, want the Slice prefix stripped", got[2].Title)
	}
}

func TestParsePlanSlicesFallsBackToHeadings(t *testing.T) {
	plan := "# Plan\n\n### Slice 1: PairingManager\n\ntext\n\n### Slice 2: Wire it up\n"
	got := ParsePlanSlices(plan)
	if len(got) != 2 {
		t.Fatalf("got %d slices, want 2", len(got))
	}
	if got[0].Title != "PairingManager" {
		t.Errorf("title = %q", got[0].Title)
	}
}

func TestSectionStopsAtSameDepth(t *testing.T) {
	md := "## A\nalpha\n### A1\nnested\n## B\nbeta\n"
	got := Section(md, "A")
	if want := "alpha\n### A1\nnested"; got != want {
		t.Errorf("Section(A) = %q, want %q", got, want)
	}
}

func TestHasHeading(t *testing.T) {
	if !HasHeading(samplePrompt, "done criteria") {
		t.Error("HasHeading should match case-insensitively")
	}
	if HasHeading(samplePrompt, "Execution Model") {
		t.Error("HasHeading matched a heading that is absent")
	}
}

func TestLoadReadsPresentArtifactsOnly(t *testing.T) {
	work := t.TempDir()
	dir := filepath.Join(work, "specs", "features", "x")
	if err := os.MkdirAll(filepath.Join(dir, "research"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(rel, body string) {
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("plan.md", "# Plan\n")
	write("PROMPT.md", samplePrompt)
	write(filepath.Join("research", "a.md"), "notes\n")

	spec, err := Load(work, "features/x")
	if err != nil {
		t.Fatal(err)
	}
	if !spec.Has(Plan) || !spec.Has(Prompt) {
		t.Error("present artifacts not loaded")
	}
	if spec.Has(Design) {
		t.Error("absent artifact reported as present")
	}
	if len(spec.Research) != 1 {
		t.Errorf("research = %v, want 1 entry", spec.Research)
	}
}

func TestLoadMissingSpec(t *testing.T) {
	if _, err := Load(t.TempDir(), "nope"); err == nil {
		t.Error("Load(missing) = nil error, want an error")
	}
}

func TestCountLines(t *testing.T) {
	for _, tt := range []struct {
		in   string
		want int
	}{{"", 0}, {"a", 1}, {"a\n", 1}, {"a\nb", 2}, {"a\nb\n", 2}} {
		if got := CountLines(tt.in); got != tt.want {
			t.Errorf("CountLines(%q) = %d, want %d", tt.in, got, tt.want)
		}
	}
}
