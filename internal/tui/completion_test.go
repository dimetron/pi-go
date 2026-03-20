package tui

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/dimetron/pi-go/internal/extension"
)

func TestComplete_CommandCompletion(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected int    // expected number of matches
		first    string // first match should be this
	}{
		{"plan matches", "/pl", 1, "/plan"},
		{"run matches", "/ru", 1, "/run"},
		{"help matches", "/he", 1, "/help"},
		{"commit matches", "/co", 3, "/commit"}, // /commit, /compact, /context
		{"all commands", "/", 0, ""},            // "/" alone doesn't return completions (handled by showCommandList)
		{"no match", "/xyz", 0, ""},
		{"exact match", "/plan", 1, "/plan"},    // exact match with single candidate stays
		{"skill-like", "/skills", 1, "/skills"}, // /skills is a built-in command
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := Complete(tt.input, nil, "")
			if len(result.Candidates) != tt.expected {
				t.Errorf("expected %d matches, got %d: %v", tt.expected, len(result.Candidates), result.Candidates)
			}
			if tt.first != "" && len(result.Candidates) > 0 && result.Candidates[0].Text != tt.first {
				t.Errorf("expected first match %q, got %q", tt.first, result.Candidates[0].Text)
			}
		})
	}
}

func TestComplete_SkillCompletion(t *testing.T) {
	skills := []extension.Skill{
		{Name: "my-skill", Description: "Does something"},
		{Name: "my-other", Description: "Does another thing"},
		{Name: "other-skill", Description: "Different"},
	}

	tests := []struct {
		name     string
		input    string
		expected int
		first    string
	}{
		{"skill prefix", "/my-", 2, "/my-other"}, // alphabetically: my-other < my-skill
		{"skill full", "/my-skill", 1, "/my-skill"},
		{"skill case insensitive", "/MY-", 2, "/my-other"}, // alphabetically
		{"no match", "/nonexistent", 0, ""},
		{"partial", "/my-o", 1, "/my-other"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := Complete(tt.input, skills, "")
			if len(result.Candidates) != tt.expected {
				t.Errorf("expected %d matches, got %d: %v", tt.expected, len(result.Candidates), result.Candidates)
			}
			if tt.first != "" && len(result.Candidates) > 0 && result.Candidates[0].Text != tt.first {
				t.Errorf("expected first match %q, got %q", tt.first, result.Candidates[0].Text)
			}
		})
	}
}

func TestComplete_CycleSelection(t *testing.T) {
	result := Complete("/c", nil, "")
	if len(result.Candidates) < 2 {
		t.Fatalf("need at least 2 candidates, got %d", len(result.Candidates))
	}

	// Initial state
	if result.Selected != 0 {
		t.Errorf("expected selected 0, got %d", result.Selected)
	}

	// Cycle forward
	result.CycleSelection(1)
	if result.Selected != 1 {
		t.Errorf("expected selected 1, got %d", result.Selected)
	}

	// Cycle forward again (should be at 2)
	result.CycleSelection(1)
	if result.Selected != 2 {
		t.Errorf("expected selected 2, got %d", result.Selected)
	}

	// Cycle forward again (should be at 3)
	result.CycleSelection(1)
	if result.Selected != 3 {
		t.Errorf("expected selected 3, got %d", result.Selected)
	}

	// Cycle forward again (wrap around to 0)
	result.CycleSelection(1)
	if result.Selected != 0 {
		t.Errorf("expected selected 0 after wrap, got %d", result.Selected)
	}

	// Cycle backward
	result.CycleSelection(-1)
	if result.Selected != len(result.Candidates)-1 {
		t.Errorf("expected selected %d after backward cycle, got %d", len(result.Candidates)-1, result.Selected)
	}
}

func TestComplete_ApplySelection(t *testing.T) {
	result := Complete("/pl", nil, "")
	if len(result.Candidates) == 0 {
		t.Fatal("need at least 1 candidate")
	}

	applied := result.ApplySelection(0)
	if applied != result.Candidates[0].Text {
		t.Errorf("expected %q, got %q", result.Candidates[0].Text, applied)
	}

	// Invalid index
	applied = result.ApplySelection(999)
	if applied != "" {
		t.Errorf("expected empty string for invalid index, got %q", applied)
	}

	// Negative index
	applied = result.ApplySelection(-1)
	if applied != "" {
		t.Errorf("expected empty string for negative index, got %q", applied)
	}
}

func TestComplete_SelectedCandidate(t *testing.T) {
	result := Complete("/c", nil, "")
	if len(result.Candidates) == 0 {
		t.Fatal("need at least 1 candidate")
	}

	// Test with selection
	selected := result.SelectedCandidate()
	if selected == nil {
		t.Error("expected non-nil selected candidate")
	}

	// Set to invalid selection
	result.Selected = 999
	selected = result.SelectedCandidate()
	if selected != nil {
		t.Error("expected nil for out-of-bounds selection")
	}
}

// --- Spec completion for /run ---

func TestComplete_RunSpecCompletion_AllSpecs(t *testing.T) {
	tmpDir := t.TempDir()
	setupTestSpecs(t, tmpDir, "alpha-feature", "beta-feature", "gamma-feature")

	result := Complete("/run ", nil, tmpDir)
	if len(result.Candidates) != 3 {
		t.Fatalf("expected 3 spec candidates for '/run ', got %d: %v", len(result.Candidates), result.Candidates)
	}

	// Should be sorted alphabetically.
	if result.Candidates[0].Text != "/run alpha-feature" {
		t.Errorf("first candidate = %q, want '/run alpha-feature'", result.Candidates[0].Text)
	}
	if result.Candidates[1].Text != "/run beta-feature" {
		t.Errorf("second candidate = %q, want '/run beta-feature'", result.Candidates[1].Text)
	}
	if result.Candidates[2].Text != "/run gamma-feature" {
		t.Errorf("third candidate = %q, want '/run gamma-feature'", result.Candidates[2].Text)
	}
}

func TestComplete_RunSpecCompletion_Partial(t *testing.T) {
	tmpDir := t.TempDir()
	setupTestSpecs(t, tmpDir, "alpha-feature", "alpha-other", "beta-feature")

	result := Complete("/run alpha", nil, tmpDir)
	if len(result.Candidates) != 2 {
		t.Fatalf("expected 2 candidates for '/run alpha', got %d", len(result.Candidates))
	}
	if result.Candidates[0].Text != "/run alpha-feature" {
		t.Errorf("first candidate = %q, want '/run alpha-feature'", result.Candidates[0].Text)
	}
	if result.Candidates[1].Text != "/run alpha-other" {
		t.Errorf("second candidate = %q, want '/run alpha-other'", result.Candidates[1].Text)
	}
}

func TestComplete_RunSpecCompletion_SingleMatch(t *testing.T) {
	tmpDir := t.TempDir()
	setupTestSpecs(t, tmpDir, "unique-spec", "other-spec")

	result := Complete("/run unique", nil, tmpDir)
	if len(result.Candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(result.Candidates))
	}
	if result.Candidates[0].Text != "/run unique-spec" {
		t.Errorf("candidate = %q, want '/run unique-spec'", result.Candidates[0].Text)
	}
}

func TestComplete_RunSpecCompletion_NoMatch(t *testing.T) {
	tmpDir := t.TempDir()
	setupTestSpecs(t, tmpDir, "alpha-feature")

	result := Complete("/run zzz", nil, tmpDir)
	if len(result.Candidates) != 0 {
		t.Errorf("expected 0 candidates for non-matching prefix, got %d", len(result.Candidates))
	}
}

func TestComplete_RunSpecCompletion_NoSpecs(t *testing.T) {
	tmpDir := t.TempDir()
	// No specs created.

	result := Complete("/run ", nil, tmpDir)
	if len(result.Candidates) != 0 {
		t.Errorf("expected 0 candidates with no specs, got %d", len(result.Candidates))
	}
}

func TestComplete_RunSpecCycling(t *testing.T) {
	tmpDir := t.TempDir()
	setupTestSpecs(t, tmpDir, "alpha", "beta", "gamma")

	result := Complete("/run ", nil, tmpDir)
	if len(result.Candidates) != 3 {
		t.Fatalf("expected 3 candidates, got %d", len(result.Candidates))
	}

	// Cycle forward through all specs and wrap around.
	if result.Selected != 0 {
		t.Errorf("initial selection = %d, want 0", result.Selected)
	}

	result.CycleSelection(1)
	if result.Selected != 1 {
		t.Errorf("after cycle(1) = %d, want 1", result.Selected)
	}
	if result.Candidates[result.Selected].Text != "/run beta" {
		t.Errorf("selected = %q, want '/run beta'", result.Candidates[result.Selected].Text)
	}

	result.CycleSelection(1)
	if result.Selected != 2 {
		t.Errorf("after cycle(1) = %d, want 2", result.Selected)
	}

	// Wrap around.
	result.CycleSelection(1)
	if result.Selected != 0 {
		t.Errorf("after wrap = %d, want 0", result.Selected)
	}
	if result.Candidates[result.Selected].Text != "/run alpha" {
		t.Errorf("wrapped to = %q, want '/run alpha'", result.Candidates[result.Selected].Text)
	}

	// Cycle backward wraps to end.
	result.CycleSelection(-1)
	if result.Selected != 2 {
		t.Errorf("backward wrap = %d, want 2", result.Selected)
	}
	if result.Candidates[result.Selected].Text != "/run gamma" {
		t.Errorf("backward = %q, want '/run gamma'", result.Candidates[result.Selected].Text)
	}
}

func TestComplete_RunSpecApplySelection(t *testing.T) {
	tmpDir := t.TempDir()
	setupTestSpecs(t, tmpDir, "alpha", "beta")

	result := Complete("/run ", nil, tmpDir)
	if len(result.Candidates) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(result.Candidates))
	}

	applied := result.ApplySelection(0)
	if applied != "/run alpha" {
		t.Errorf("apply(0) = %q, want '/run alpha'", applied)
	}

	applied = result.ApplySelection(1)
	if applied != "/run beta" {
		t.Errorf("apply(1) = %q, want '/run beta'", applied)
	}
}

func TestComplete_RunSpecHasDescription(t *testing.T) {
	tmpDir := t.TempDir()
	setupTestSpecs(t, tmpDir, "my-feature")

	result := Complete("/run ", nil, tmpDir)
	if len(result.Candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(result.Candidates))
	}
	if result.Candidates[0].Description == "" {
		t.Error("spec candidate should have a description")
	}
	if result.Candidates[0].Type != CompletionTypeSpec {
		t.Errorf("type = %d, want CompletionTypeSpec", result.Candidates[0].Type)
	}
}

// --- Spec completion for /plan ---

func TestComplete_PlanSpecCompletion(t *testing.T) {
	tmpDir := t.TempDir()
	setupTestSpecs(t, tmpDir, "existing-plan", "another-plan")

	result := Complete("/plan ", nil, tmpDir)
	if len(result.Candidates) != 2 {
		t.Fatalf("expected 2 candidates for '/plan ', got %d", len(result.Candidates))
	}
	if result.Candidates[0].Text != "/plan another-plan" {
		t.Errorf("first = %q, want '/plan another-plan'", result.Candidates[0].Text)
	}
}

// --- File @mention completion ---

func TestCompleteMention_MatchingFiles(t *testing.T) {
	tmpDir := t.TempDir()
	setupTestFiles(t, tmpDir, "src/main.go", "src/model.go", "internal/tui/input.go", "README.md")

	result := CompleteMention("src/", tmpDir)
	if len(result.Candidates) != 2 {
		t.Fatalf("expected 2 candidates for 'src/', got %d: %v", len(result.Candidates), result.Candidates)
	}
	if result.Candidates[0].Text != "src/main.go" {
		t.Errorf("first = %q, want 'src/main.go'", result.Candidates[0].Text)
	}
}

func TestCompleteMention_EmptyPrefix(t *testing.T) {
	tmpDir := t.TempDir()
	setupTestFiles(t, tmpDir, "a.go", "b.go", "c.go")

	result := CompleteMention("", tmpDir)
	if len(result.Candidates) != 3 {
		t.Fatalf("expected 3 candidates for empty prefix, got %d", len(result.Candidates))
	}
}

func TestCompleteMention_NoMatch(t *testing.T) {
	tmpDir := t.TempDir()
	setupTestFiles(t, tmpDir, "src/main.go")

	result := CompleteMention("zzz", tmpDir)
	if len(result.Candidates) != 0 {
		t.Errorf("expected 0 candidates, got %d", len(result.Candidates))
	}
}

func TestCompleteMention_FuzzyMatch(t *testing.T) {
	tmpDir := t.TempDir()
	setupTestFiles(t, tmpDir, "src/main.go", "src/model.go", "internal/tui/input.go")

	result := CompleteMention("smo", tmpDir) // fuzzy: s->src/, m->m, o->odel
	found := false
	for _, c := range result.Candidates {
		if c.Text == "src/model.go" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected fuzzy match for 'src/model.go' in %v", result.Candidates)
	}
}

func TestCompleteMention_SkipsHiddenDirs(t *testing.T) {
	tmpDir := t.TempDir()
	setupTestFiles(t, tmpDir, ".git/config", "src/main.go")

	result := CompleteMention("", tmpDir)
	for _, c := range result.Candidates {
		if c.Text == ".git/config" {
			t.Error("should not include files in hidden directories")
		}
	}
}

func TestCompleteMention_MaxResults(t *testing.T) {
	tmpDir := t.TempDir()
	files := make([]string, 30)
	for i := range files {
		files[i] = fmt.Sprintf("file%02d.go", i)
	}
	setupTestFiles(t, tmpDir, files...)

	result := CompleteMention("", tmpDir)
	if len(result.Candidates) > 20 {
		t.Errorf("expected at most 20 candidates, got %d", len(result.Candidates))
	}
}

func TestFindMentionAtCursor(t *testing.T) {
	tests := []struct {
		name      string
		text      string
		cursor    int
		wantStart int
		wantPfx   string
	}{
		{"at end", "fix @src/ma", 11, 4, "src/ma"},
		{"mid text", "fix @src/main.go and more", 16, 4, "src/main.go"},
		{"no mention", "fix this bug", 12, -1, ""},
		{"just @", "hello @", 7, 6, ""},
		{"@ at start", "@main", 5, 0, "main"},
		{"space before cursor", "fix @src/ file", 9, 4, "src/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start, pfx := findMentionAtCursor(tt.text, tt.cursor)
			if start != tt.wantStart {
				t.Errorf("start = %d, want %d", start, tt.wantStart)
			}
			if pfx != tt.wantPfx {
				t.Errorf("prefix = %q, want %q", pfx, tt.wantPfx)
			}
		})
	}
}

func TestExtractMentions(t *testing.T) {
	tests := []struct {
		name string
		text string
		want []string
	}{
		{"single mention", "fix @src/main.go please", []string{"src/main.go"}},
		{"multiple mentions", "look at @src/a.go and @src/b.go", []string{"src/a.go", "src/b.go"}},
		{"no mentions", "fix this bug", nil},
		{"mention at end", "check @README.md", []string{"README.md"}},
		{"mention at start", "@main.go is broken", []string{"main.go"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractMentions(tt.text)
			if len(got) != len(tt.want) {
				t.Fatalf("len = %d, want %d: %v", len(got), len(tt.want), got)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestFuzzyMatchPath(t *testing.T) {
	tests := []struct {
		path, query string
		want        bool
	}{
		{"src/main.go", "sma", true},
		{"src/model.go", "smo", true},
		{"internal/tui/input.go", "iti", true},
		{"src/main.go", "zzz", false},
		{"a.go", "a", true},
	}
	for _, tt := range tests {
		t.Run(tt.path+"_"+tt.query, func(t *testing.T) {
			if got := fuzzyMatchPath(tt.path, tt.query); got != tt.want {
				t.Errorf("fuzzyMatchPath(%q, %q) = %v, want %v", tt.path, tt.query, got, tt.want)
			}
		})
	}
}

// --- Helpers ---

func setupTestFiles(t *testing.T, workDir string, paths ...string) {
	t.Helper()
	for _, p := range paths {
		full := workDir + "/" + p
		dir := full[:strings.LastIndex(full, "/")]
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("// "+p), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// --- Helper ---

func setupTestSpecs(t *testing.T, workDir string, names ...string) {
	t.Helper()
	for _, name := range names {
		specDir := workDir + "/specs/" + name
		if err := os.MkdirAll(specDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(specDir+"/PROMPT.md", []byte("# "+name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
