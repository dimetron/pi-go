package validate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimetron/pi-go/internal/sop/specdoc"
)

func TestParseRule(t *testing.T) {
	tests := []struct {
		spec     string
		wantName string
		check    func(*testing.T, Args)
	}{
		{"non_empty", "non_empty", func(t *testing.T, a Args) {
			if len(a.Positional) != 0 || len(a.List) != 0 {
				t.Errorf("bare rule got args: %+v", a)
			}
		}},
		{"max_lines(2000)", "max_lines", func(t *testing.T, a Args) {
			if got := a.Int("max", 0); got != 2000 {
				t.Errorf("Int = %d, want 2000", got)
			}
		}},
		{"max_lines(max: 500)", "max_lines", func(t *testing.T, a Args) {
			if got := a.Int("max", 0); got != 500 {
				t.Errorf("Int = %d, want 500", got)
			}
		}},
		{`has_headings(["Objective", "Gates"])`, "has_headings", func(t *testing.T, a Args) {
			if got := a.Items(); len(got) != 2 || got[0] != "Objective" || got[1] != "Gates" {
				t.Errorf("Items = %v", got)
			}
		}},
		{"slice_budget(max_files: 10, max_changed_lines: 400)", "slice_budget", func(t *testing.T, a Args) {
			if got := a.Int("max_files", 0); got != 10 {
				t.Errorf("max_files = %d, want 10", got)
			}
			if got := a.Int("max_changed_lines", 0); got != 400 {
				t.Errorf("max_changed_lines = %d, want 400", got)
			}
		}},
		{"done_criteria(min: 3, no_placeholders: true)", "done_criteria", func(t *testing.T, a Args) {
			if !a.Bool("no_placeholders", false) {
				t.Error("no_placeholders = false, want true")
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.spec, func(t *testing.T) {
			name, args, err := ParseRule(tt.spec)
			if err != nil {
				t.Fatal(err)
			}
			if name != tt.wantName {
				t.Fatalf("name = %q, want %q", name, tt.wantName)
			}
			tt.check(t, args)
		})
	}
}

func TestParseRuleMalformed(t *testing.T) {
	if _, _, err := ParseRule("Not A Rule!"); err == nil {
		t.Error("ParseRule accepted malformed input")
	}
}

func TestApplyUnknownRuleIsAnError(t *testing.T) {
	got := Apply("no_such_rule", Target{Artifact: "plan.md", Content: "x"})
	if got.OK() {
		t.Fatal("unknown rule did not produce a blocking finding")
	}
	if !strings.Contains(got[0].Message, "unknown validator") {
		t.Errorf("message = %q", got[0].Message)
	}
}

func TestRuleNonEmpty(t *testing.T) {
	if got := Apply("non_empty", Target{Artifact: "plan.md", Content: "  \n\t "}); got.OK() {
		t.Error("whitespace-only content passed non_empty")
	}
	if got := Apply("non_empty", Target{Artifact: "plan.md", Content: "# Plan"}); !got.OK() {
		t.Errorf("real content failed non_empty: %v", got)
	}
}

func TestRuleMaxLines(t *testing.T) {
	long := strings.Repeat("line\n", 2500)
	got := Apply("max_lines(max: 2000)", Target{Artifact: "plan.md", Content: long})
	if got.OK() {
		t.Fatal("a 2500-line plan passed max_lines(2000)")
	}
	if !strings.Contains(got[0].Fix, "2000 lines per call") {
		t.Errorf("fix does not explain the read window: %q", got[0].Fix)
	}
}

func TestRuleHasHeadings(t *testing.T) {
	content := "# T\n\n## Objective\nx\n\n## Gates\ny\n"
	if got := Apply(`has_headings(["Objective","Gates"])`, Target{Content: content}); !got.OK() {
		t.Errorf("present headings reported missing: %v", got)
	}
	got := Apply(`has_headings(["Objective","Done Criteria"])`, Target{Content: content})
	if got.OK() {
		t.Fatal("missing heading was not reported")
	}
	if !strings.Contains(got[0].Message, "Done Criteria") {
		t.Errorf("message = %q", got[0].Message)
	}
}

func TestRuleSlicesAreCheckboxes(t *testing.T) {
	headingOnly := "# Plan\n\n### Slice 1: A\n\n### Slice 2: B\n"
	if got := Apply("slices_are_checkboxes", Target{Content: headingOnly}); got.OK() {
		t.Error("a heading-only plan passed slices_are_checkboxes")
	}
	withBoxes := "# Plan\n\n- [ ] Step 1: A\n- [x] Step 2: B\n"
	if got := Apply("slices_are_checkboxes", Target{Content: withBoxes}); !got.OK() {
		t.Errorf("a checkbox plan failed: %v", got)
	}
	if got := Apply("slices_are_checkboxes", Target{Content: "# Plan\n\ntext\n"}); got.OK() {
		t.Error("a plan with no slices passed")
	}
}

func TestRuleSliceCountUpperBound(t *testing.T) {
	var b strings.Builder
	b.WriteString("# Plan\n\n")
	for i := 1; i <= 50; i++ {
		b.WriteString("- [ ] Step 1: thing\n")
	}
	got := Apply("slice_count(min: 1, max: 25)", Target{Artifact: "plan.md", Content: b.String()})
	if got.OK() {
		t.Fatal("a 50-slice plan passed slice_count(max: 25)")
	}
	if !strings.Contains(got[0].Message, "50 slices") {
		t.Errorf("message = %q", got[0].Message)
	}
}

const promptWithSlices = "# T\n\n## Implementation Slices\n\n" +
	"1. **A** — do a, files: `x.go`, verify: `go test ./...`, parallel-safe: no\n" +
	"2. **B** — do b\n"

func TestRuleEverySliceHas(t *testing.T) {
	got := Apply(`every_slice_has(["files","verify","parallel_safe"])`,
		Target{Artifact: "PROMPT.md", Content: promptWithSlices})
	if got.OK() {
		t.Fatal("a slice missing files/verify/parallel-safe passed")
	}
	if len(got) != 1 {
		t.Fatalf("got %d findings, want 1 (only slice 2 is incomplete): %v", len(got), got)
	}
	for _, want := range []string{"files", "verify", "parallel-safe"} {
		if !strings.Contains(got[0].Message, want) {
			t.Errorf("message %q does not name missing %q", got[0].Message, want)
		}
	}
}

func TestRuleSliceBudget(t *testing.T) {
	var files []string
	for i := 0; i < 14; i++ {
		files = append(files, "`f"+string(rune('a'+i))+".go`")
	}
	content := "## Implementation Slices\n\n1. **Big** — files: " + strings.Join(files, ", ") + ", verify: `go build ./...`\n"
	got := Apply("slice_budget(max_files: 10)", Target{Artifact: "PROMPT.md", Content: content})
	if got.OK() {
		t.Fatal("a 14-file slice passed a 10-file budget")
	}
}

func TestRuleGatesAreExecutable(t *testing.T) {
	t.Run("placeholder", func(t *testing.T) {
		content := "## Gates\n- **build**: `<build command discovered during research>`\n"
		got := Apply("gates_are_executable", Target{Artifact: "PROMPT.md", Content: content})
		if got.OK() {
			t.Fatal("a template placeholder gate passed")
		}
		if !strings.Contains(got[0].Message, "template placeholder") {
			t.Errorf("message = %q", got[0].Message)
		}
	})
	t.Run("missing binary", func(t *testing.T) {
		content := "## Gates\n- **build**: `definitely-not-a-real-binary-xyz build`\n"
		got := Apply("gates_are_executable", Target{Artifact: "PROMPT.md", Content: content})
		if got.OK() {
			t.Fatal("a gate running a nonexistent binary passed")
		}
	})
	t.Run("real binary", func(t *testing.T) {
		content := "## Gates\n- **build**: `go build ./...`\n"
		if got := Apply("gates_are_executable", Target{Artifact: "PROMPT.md", Content: content}); !got.OK() {
			t.Errorf("`go build` reported unrunnable: %v", got)
		}
	})
	t.Run("no gates", func(t *testing.T) {
		if got := Apply("gates_are_executable", Target{Artifact: "PROMPT.md", Content: "# T\n"}); got.OK() {
			t.Error("a PROMPT.md with no gates passed")
		}
	})
}

func TestRuleGatesMakeTargetWarnsOnly(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "Makefile"), []byte("build:\n\techo hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	content := "## Gates\n- **test**: `make no-such-target`\n"
	got := Apply("gates_are_executable", Target{Artifact: "PROMPT.md", Content: content, RepoRoot: repo})
	if !got.OK() {
		t.Errorf("an unknown make target should warn, not block: %v", got)
	}
	if len(got) != 1 || got[0].Severity != SeverityWarn {
		t.Errorf("want one warning, got %v", got)
	}
}

func TestRuleDoneCriteria(t *testing.T) {
	t.Run("too few", func(t *testing.T) {
		content := "## Done Criteria\n- [ ] one thing\n"
		got := Apply("done_criteria(min: 3)", Target{Artifact: "PROMPT.md", Content: content})
		if got.OK() {
			t.Fatal("1 criterion passed a minimum of 3")
		}
	})
	t.Run("placeholders", func(t *testing.T) {
		content := "## Done Criteria\n- [ ] <observable outcome>\n- [ ] <observable outcome>\n- [ ] real thing works\n"
		got := Apply("done_criteria(min: 3, no_placeholders: true)", Target{Artifact: "PROMPT.md", Content: content})
		if got.OK() {
			t.Fatal("template placeholders passed")
		}
		if len(got) != 2 {
			t.Errorf("got %d findings, want 2 placeholders: %v", len(got), got)
		}
	})
	t.Run("valid", func(t *testing.T) {
		content := "## Done Criteria\n- [ ] a works\n- [ ] b works\n- [ ] c works\n"
		if got := Apply("done_criteria(min: 3)", Target{Content: content}); !got.OK() {
			t.Errorf("valid criteria failed: %v", got)
		}
	})
}

func TestRuleGivenWhenThen(t *testing.T) {
	wrapped := "## Acceptance Criteria\n\n### Area\n\n- Given no goal set, when `/goal x`,\n  then chat shows the goal.\n"
	if got := Apply("acceptance_criteria_are_given_when_then", Target{Content: wrapped}); !got.OK() {
		t.Errorf("a criterion wrapped across lines was rejected: %v", got)
	}
	prose := "## Acceptance Criteria\n\n- It should work nicely.\n"
	if got := Apply("acceptance_criteria_are_given_when_then", Target{Content: prose}); got.OK() {
		t.Error("prose criteria passed the Given/When/Then rule")
	}
}

func TestRuleReferencesExist(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "specs", "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "specs", "x", "plan.md"), []byte("p"), 0o644); err != nil {
		t.Fatal(err)
	}
	content := "## Reference\n- Plan: `specs/x/plan.md`\n- Design: `specs/x/design.md`\n"
	got := Apply("references_exist", Target{Artifact: "PROMPT.md", Content: content, RepoRoot: repo})
	if got.OK() {
		t.Fatal("a Reference to a nonexistent path passed")
	}
	if len(got) != 1 || !strings.Contains(got[0].Message, "design.md") {
		t.Errorf("findings = %v", got)
	}
}

func TestRulePlanMatchesPrompt(t *testing.T) {
	spec := &specdoc.Spec{Files: map[string]string{
		specdoc.Plan:   "- [ ] Step 1: Routing fix\n- [ ] Step 2: Model catalog\n- [ ] Step 3: Thinking cache\n",
		specdoc.Prompt: promptWithSlices,
	}}
	got := Apply("plan_slices_match_prompt_slices", Target{Artifact: "PROMPT.md", Spec: spec})
	if got.OK() {
		t.Fatal("a 3-slice plan against a 2-slice PROMPT passed")
	}
	if !strings.Contains(got.Errors()[0].Message, "3 slices") {
		t.Errorf("message = %q", got.Errors()[0].Message)
	}
}

func TestRulePlanMatchesPromptAcceptsRephrasedTitles(t *testing.T) {
	spec := &specdoc.Spec{Files: map[string]string{
		specdoc.Plan: "- [ ] Step 1: Routing fix for provider\n- [ ] Step 2: Model catalog entries\n",
		specdoc.Prompt: "## Implementation Slices\n\n" +
			"1. **Routing fix** — provider routing, files: `a.go`, verify: `go build ./...`, parallel-safe: no\n" +
			"2. **Model catalog** — catalog entries, files: `b.go`, verify: `go build ./...`, parallel-safe: yes\n",
	}}
	if got := Apply("plan_slices_match_prompt_slices", Target{Artifact: "PROMPT.md", Spec: spec}); !got.OK() {
		t.Errorf("rephrased but matching titles were rejected: %v", got)
	}
}

func TestRuleNoSolutionLanguageWarnsOnly(t *testing.T) {
	content := "# Findings\n\nThe router dispatches by prefix.\n\nWe should replace it with a map.\n"
	got := Apply("no_solution_language", Target{Artifact: "research/a.md", Content: content})
	if !got.OK() {
		t.Error("solution language should warn, not block")
	}
	if len(got) != 1 || got[0].Severity != SeverityWarn {
		t.Errorf("want one warning, got %v", got)
	}
}

func TestFindingsFormatPutsErrorsFirst(t *testing.T) {
	fs := Findings{
		{Artifact: "a.md", Rule: "r1", Severity: SeverityWarn, Message: "warn thing"},
		{Artifact: "b.md", Rule: "r2", Severity: SeverityError, Message: "error thing", Fix: "do x"},
	}
	out := fs.Format()
	if strings.Index(out, "error thing") > strings.Index(out, "warn thing") {
		t.Errorf("errors not listed first:\n%s", out)
	}
	if !strings.Contains(out, "fix: do x") {
		t.Errorf("fix not rendered:\n%s", out)
	}
}

func TestFindingsOK(t *testing.T) {
	if !(Findings{{Severity: SeverityWarn}}).OK() {
		t.Error("warnings should not block")
	}
	if (Findings{{Severity: SeverityError}}).OK() {
		t.Error("errors should block")
	}
	if !(Findings{}).OK() {
		t.Error("empty findings should be OK")
	}
}
