package tools

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/dimetron/pi-go/internal/subagent"
)

// Tests pinning the behavior of find.go, session_sweep.go and subagent.go
// across the cognitive-complexity flattening of findHandler,
// accumulateSweepFile, parallelModeHandler, chainModeHandler and
// singleModeHandler.
//
// Every "golden" literal below was captured by running these same inputs
// against the unmodified source at HEAD (extracted with `git archive` into a
// scratch tree) before any edit, so the tests prove the refactor is a no-op
// rather than proving the new code agrees with itself.

// cogATree builds the fixture tree the find goldens were captured against:
// a .gitignore, always-skipped directories, a hidden directory, and the two
// dot-directories find deliberately does NOT skip.
func cogATree(t *testing.T) *Sandbox {
	t.Helper()
	dir := t.TempDir()
	sb := testSandbox(t, dir)
	mk := func(rel, body string) {
		t.Helper()
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	mk(".gitignore", "ignored.go\nbuild/\n")
	mk("main.go", "package main")
	mk("readme.md", "# hi")
	mk("ignored.go", "package ignored")
	mk("build/out.go", "package build")
	mk("src/app.go", "package app")
	mk("src/app_test.go", "package app")
	mk("src/deep/nested/thing.go", "package nested")
	mk("node_modules/pkg/index.go", "package pkg")
	mk("vendor/v/v.go", "package v")
	mk("__pycache__/x.go", "package x")
	mk(".hidden/h.go", "package h")
	mk(".claude/agent.go", "package agent")
	mk(".pi-go/tasks/t.go", "package t")
	return sb
}

func TestCogAFindHandlerGolden(t *testing.T) {
	sb := cogATree(t)

	tests := []struct {
		name    string
		pattern string
		want    []string
	}{
		{
			// Bare name pattern: matched against the file name, so it reaches
			// every depth. .claude and .pi-go survive; .hidden, node_modules,
			// vendor, __pycache__ and the gitignored build/ and ignored.go do not.
			name:    "name pattern reaches every depth",
			pattern: "*.go",
			want: []string{
				".claude/agent.go", ".pi-go/tasks/t.go", "main.go",
				"src/app.go", "src/app_test.go", "src/deep/nested/thing.go",
			},
		},
		{
			name:    "leading doublestar is stripped and behaves the same",
			pattern: "**/*.go",
			want: []string{
				".claude/agent.go", ".pi-go/tasks/t.go", "main.go",
				"src/app.go", "src/app_test.go", "src/deep/nested/thing.go",
			},
		},
		{
			// Interior "**" takes the doublestar path: prefix must match, then
			// the suffix is matched against the base name.
			name:    "interior doublestar anchors on the prefix",
			pattern: "src/**/*.go",
			want:    []string{"src/app.go", "src/app_test.go", "src/deep/nested/thing.go"},
		},
		{
			name:    "suffix pattern",
			pattern: "**/*_test.go",
			want:    []string{"src/app_test.go"},
		},
		{
			// No "**": matches the path relative to the walk root, one segment
			// deep only — src/deep/nested/thing.go is not a hit.
			name:    "relative path pattern is not recursive",
			pattern: "src/*.go",
			want:    []string{"src/app.go", "src/app_test.go"},
		},
		{
			name:    "star matches every surviving file",
			pattern: "*",
			want: []string{
				".claude/agent.go", ".pi-go/tasks/t.go", "main.go", "readme.md",
				"src/app.go", "src/app_test.go", "src/deep/nested/thing.go",
			},
		},
		{
			// "**/" normalizes to "", and matchDoublestar's empty suffix
			// returns true — so an empty pattern matches everything.
			name:    "trailing-slash doublestar matches everything",
			pattern: "**/",
			want: []string{
				".claude/agent.go", ".pi-go/tasks/t.go", "main.go", "readme.md",
				"src/app.go", "src/app_test.go", "src/deep/nested/thing.go",
			},
		},
		{
			name:    "doublestar prefix that matches no directory",
			pattern: "nope/**/*.go",
			want:    nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := findHandler(sb, FindInput{Pattern: tt.pattern, Path: "."})
			if err != nil {
				t.Fatalf("findHandler(%q): %v", tt.pattern, err)
			}
			if !reflect.DeepEqual(out.Files, tt.want) {
				t.Errorf("files = %v, want %v", out.Files, tt.want)
			}
			if out.TotalFiles != len(tt.want) {
				t.Errorf("total_files = %d, want %d", out.TotalFiles, len(tt.want))
			}
			if out.Truncated {
				t.Error("truncated = true, want false")
			}
		})
	}
}

func TestCogAFindHandlerDefaultPath(t *testing.T) {
	sb := cogATree(t)
	out, err := findHandler(sb, FindInput{Pattern: "*.md"})
	if err != nil {
		t.Fatalf("findHandler: %v", err)
	}
	if !reflect.DeepEqual(out.Files, []string{"readme.md"}) {
		t.Errorf("files = %v, want [readme.md] — an empty path must default to \".\"", out.Files)
	}
}

func TestCogAFindHandlerErrors(t *testing.T) {
	sb := cogATree(t)

	if _, err := findHandler(sb, FindInput{Pattern: ""}); err == nil {
		t.Error("empty pattern returned no error")
	} else if err.Error() != "pattern is required" {
		t.Errorf("error = %q, want %q", err.Error(), "pattern is required")
	}

	if _, err := findHandler(sb, FindInput{Pattern: "*.go", Path: "../escape"}); err == nil {
		t.Error("a path outside the sandbox returned no error")
	} else if !strings.Contains(err.Error(), "escapes sandbox root") {
		t.Errorf("error = %q, want a sandbox-escape error", err.Error())
	}
}

// TestCogAFindHandlerTruncates pins the cap: results stop at maxFindResults
// but the total keeps counting, and Truncated flips.
func TestCogAFindHandlerTruncates(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)
	const extra = 7
	for i := 0; i < maxFindResults+extra; i++ {
		name := filepath.Join(dir, fmt.Sprintf("f%04d.go", i))
		if err := os.WriteFile(name, []byte("x"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	out, err := findHandler(sb, FindInput{Pattern: "*.go", Path: "."})
	if err != nil {
		t.Fatalf("findHandler: %v", err)
	}
	if len(out.Files) != maxFindResults {
		t.Errorf("files = %d, want %d", len(out.Files), maxFindResults)
	}
	if out.TotalFiles != maxFindResults+extra {
		t.Errorf("total_files = %d, want %d", out.TotalFiles, maxFindResults+extra)
	}
	if !out.Truncated {
		t.Error("truncated = false, want true")
	}
	if out.Files[0] != "f0000.go" || out.Files[len(out.Files)-1] != "f0499.go" {
		t.Errorf("kept window = %s..%s, want f0000.go..f0499.go", out.Files[0], out.Files[len(out.Files)-1])
	}
}

// TestCogAFindWalkMatches exercises the three-way match ladder directly:
// name, then path relative to the root, then doublestar.
func TestCogAFindWalkMatches(t *testing.T) {
	tests := []struct {
		name        string
		root        string
		pattern     string
		filePattern string
		path        string
		base        string
		want        bool
	}{
		{"name match wins first", ".", "*.go", "*.go", "src/a.go", "a.go", true},
		{"relative path match", ".", "src/*.go", "src/*.go", "src/a.go", "a.go", true},
		{"doublestar fallback", ".", "src/**/*.go", "src/**/*.go", "src/deep/a.go", "a.go", true},
		{"doublestar prefix mismatch", ".", "other/**/*.go", "other/**/*.go", "src/deep/a.go", "a.go", false},
		{"no match at all", ".", "*.rs", "*.rs", "src/a.go", "a.go", false},
		{
			// filepath.Rel fails for an absolute root against a relative path;
			// the ladder stops there rather than falling through to doublestar.
			name: "unrelatable path stops the ladder", root: "/abs/root",
			pattern: "**/*.go", filePattern: "*.go", path: "relative/a.txt", base: "a.txt", want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := &findWalk{root: tt.root, pattern: tt.pattern, filePattern: tt.filePattern}
			if got := w.matches(tt.path, tt.base); got != tt.want {
				t.Errorf("matches(%q, %q) = %v, want %v", tt.path, tt.base, got, tt.want)
			}
		})
	}
}

// TestCogAFindWalkVisit pins the callback's four exits: walk error, directory
// pruning, gitignored file, and a recorded hit.
func TestCogAFindWalkVisit(t *testing.T) {
	patterns := []GitignorePattern{{pattern: "*.log"}}

	t.Run("walk error is swallowed", func(t *testing.T) {
		w := &findWalk{root: ".", pattern: "*", filePattern: "*"}
		if err := w.visit("x", fakeDirEntry{name: "x"}, os.ErrPermission); err != nil {
			t.Fatalf("visit returned %v, want nil", err)
		}
		if w.total != 0 {
			t.Errorf("total = %d, want 0", w.total)
		}
	})

	t.Run("always-skipped dir prunes", func(t *testing.T) {
		w := &findWalk{root: ".", pattern: "*", filePattern: "*"}
		got := w.visit("node_modules", fakeDirEntry{name: "node_modules", isDir: true}, nil)
		if !errors.Is(got, filepath.SkipDir) {
			t.Fatalf("visit = %v, want SkipDir", got)
		}
	})

	t.Run("gitignored dir prunes", func(t *testing.T) {
		w := &findWalk{root: ".", pattern: "*", filePattern: "*", patterns: []GitignorePattern{{pattern: "build", isDir: true}}}
		got := w.visit("build", fakeDirEntry{name: "build", isDir: true}, nil)
		if !errors.Is(got, filepath.SkipDir) {
			t.Fatalf("visit = %v, want SkipDir", got)
		}
	})

	t.Run("ordinary dir descends", func(t *testing.T) {
		w := &findWalk{root: ".", pattern: "*", filePattern: "*"}
		if got := w.visit("src", fakeDirEntry{name: "src", isDir: true}, nil); got != nil {
			t.Fatalf("visit = %v, want nil", got)
		}
	})

	t.Run("gitignored file is dropped", func(t *testing.T) {
		w := &findWalk{root: ".", pattern: "*", filePattern: "*", patterns: patterns}
		if got := w.visit("app.log", fakeDirEntry{name: "app.log"}, nil); got != nil {
			t.Fatalf("visit = %v, want nil", got)
		}
		if w.total != 0 {
			t.Errorf("total = %d, want 0 — a gitignored file must not be counted", w.total)
		}
	})

	t.Run("non-matching file is dropped", func(t *testing.T) {
		w := &findWalk{root: ".", pattern: "*.rs", filePattern: "*.rs"}
		_ = w.visit("main.go", fakeDirEntry{name: "main.go"}, nil)
		if w.total != 0 {
			t.Errorf("total = %d, want 0", w.total)
		}
	})

	t.Run("hit is counted and kept", func(t *testing.T) {
		w := &findWalk{root: ".", pattern: "*.go", filePattern: "*.go"}
		_ = w.visit("main.go", fakeDirEntry{name: "main.go"}, nil)
		if w.total != 1 || !reflect.DeepEqual(w.files, []string{"main.go"}) {
			t.Errorf("total = %d files = %v, want 1 [main.go]", w.total, w.files)
		}
	})

	t.Run("past the cap the total still climbs", func(t *testing.T) {
		w := &findWalk{root: ".", pattern: "*.go", filePattern: "*.go"}
		w.files = make([]string, maxFindResults)
		_ = w.visit("main.go", fakeDirEntry{name: "main.go"}, nil)
		if w.total != 1 {
			t.Errorf("total = %d, want 1", w.total)
		}
		if len(w.files) != maxFindResults {
			t.Errorf("files = %d, want %d — the cap must hold", len(w.files), maxFindResults)
		}
	})
}

// cogASweepLines is the fixture the sweep goldens were captured against: a
// tool call with usage, a malformed line, a part carrying both a call and a
// response, an oversize duplicate, two identical error responses, a
// loop-guard abort, and an unrelated error.
func cogASweepLines(big string) []string {
	return []string{
		`{"content":{"parts":[{"functionCall":{"name":"read"}}]},"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":20}}`,
		`not json at all`,
		`{"content":{"parts":[{"functionCall":{"name":"read"},"functionResponse":{"name":"read","response":{"content":"` + big + `"}}}]}}`,
		`{"content":{"parts":[{"functionResponse":{"name":"read","response":{"content":"` + big + `"}}}]}}`,
		`{"content":{"parts":[{"functionResponse":{"name":"grep","response":{"error":"boom"}}}]},"usageMetadata":{"promptTokenCount":7,"candidatesTokenCount":3}}`,
		`{"content":{"parts":[]},"errorMessage":"agent loop aborted: identical tool call \"read\" repeated 10 times"}`,
		`{"errorMessage":"provider timed out"}`,
		`{"content":{"parts":[{"functionResponse":{"name":"grep","response":{"error":"boom"}}}]}}`,
	}
}

// TestCogAAccumulateSweepFileGolden pins every counter accumulateSweepFile
// maintains, with the exact totals the pre-refactor code produced.
func TestCogAAccumulateSweepFileGolden(t *testing.T) {
	root := t.TempDir()
	big := strings.Repeat("z", oversizeChars+10)
	writeEvents(t, root, "cog-a", cogASweepLines(big)...)

	totals := newSweepTotals()
	accumulateSweepFile(totals, filepath.Join(root, "cog-a", "events.jsonl"))

	// Usage is summed across events; the malformed line contributes nothing.
	if totals.PromptTokens != 107 || totals.OutputTokens != 23 {
		t.Errorf("usage = %d/%d, want 107/23", totals.PromptTokens, totals.OutputTokens)
	}
	// Two 24024-byte read results plus two 16-byte grep results.
	if totals.ToolBytes != 48080 {
		t.Errorf("ToolBytes = %d, want 48080", totals.ToolBytes)
	}
	// The repeat of each body: one read (24024) and one grep (16).
	if totals.DupBytes != 24040 {
		t.Errorf("DupBytes = %d, want 24040", totals.DupBytes)
	}

	read := totals.tool("read")
	if read.Calls != 2 || read.Errors != 0 || read.Bytes != 48048 || read.Oversized != 48 {
		t.Errorf("read = %+v, want calls 2 errors 0 bytes 48048 oversized 48", *read)
	}
	grep := totals.tool("grep")
	if grep.Calls != 0 || grep.Errors != 2 || grep.Bytes != 32 || grep.Oversized != 0 {
		t.Errorf("grep = %+v, want calls 0 errors 2 bytes 32 oversized 0", *grep)
	}

	wantAborts := map[string]int{`identical tool call "read" repeated 10 times`: 1}
	if !reflect.DeepEqual(totals.Aborts, wantAborts) {
		t.Errorf("Aborts = %v, want %v — an unrelated error must not be counted", totals.Aborts, wantAborts)
	}
	if n := totalAborts(totals); n != 1 {
		t.Errorf("totalAborts = %d, want 1", n)
	}
}

// TestCogAAccumulateSweepEvent covers the per-event fold in isolation: the
// branches an event can skip.
func TestCogAAccumulateSweepEvent(t *testing.T) {
	t.Run("no usage, no abort, no parts", func(t *testing.T) {
		totals := newSweepTotals()
		accumulateSweepEvent(totals, map[string]bool{}, sweepEvent{ErrorMessage: "provider timed out"})
		if totals.PromptTokens != 0 || len(totals.Aborts) != 0 || len(totals.Tools) != 0 {
			t.Errorf("totals = %+v, want all zero", *totals)
		}
	})

	t.Run("call without response counts a call only", func(t *testing.T) {
		totals := newSweepTotals()
		ev := sweepEvent{}
		ev.Content.Parts = []sweepPart{{FunctionCall: &sweepFunctionCall{Name: "bash"}}}
		accumulateSweepEvent(totals, map[string]bool{}, ev)
		if totals.tool("bash").Calls != 1 {
			t.Errorf("calls = %d, want 1", totals.tool("bash").Calls)
		}
		if totals.ToolBytes != 0 {
			t.Errorf("ToolBytes = %d, want 0 — a call carries no result bytes", totals.ToolBytes)
		}
	})

	t.Run("usage and abort are both folded", func(t *testing.T) {
		totals := newSweepTotals()
		ev := sweepEvent{
			UsageMetadata: &sweepUsage{PromptTokenCount: 5, CandidatesTokenCount: 6},
			ErrorMessage:  "agent loop aborted: stuck",
		}
		accumulateSweepEvent(totals, map[string]bool{}, ev)
		if totals.PromptTokens != 5 || totals.OutputTokens != 6 {
			t.Errorf("usage = %d/%d, want 5/6", totals.PromptTokens, totals.OutputTokens)
		}
		if totals.Aborts["stuck"] != 1 {
			t.Errorf("Aborts = %v, want {stuck: 1}", totals.Aborts)
		}
	})
}

// TestCogAAccumulateSweepResponse pins the per-result accounting: volume, the
// error marker, the oversize excess, and first-seen versus duplicate.
func TestCogAAccumulateSweepResponse(t *testing.T) {
	oversize := `{"x":"` + strings.Repeat("z", oversizeChars) + `"}`

	tests := []struct {
		name          string
		body          string
		seenAlready   bool
		wantErrors    int
		wantOversized int
		wantDup       int
	}{
		{"plain result", `{"content":"ok"}`, false, 0, 0, 0},
		{"error marker counts an error", `{"error":"boom"}`, false, 1, 0, 0},
		{"oversize counts only the excess", oversize, false, 0, len(oversize) - oversizeChars, 0},
		{"first sighting is not a duplicate", `{"content":"ok"}`, false, 0, 0, 0},
		{"second sighting is a duplicate", `{"content":"ok"}`, true, 0, 0, len(`{"content":"ok"}`)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			totals := newSweepTotals()
			seen := map[string]bool{}
			if tt.seenAlready {
				seen[tt.body] = true
			}
			accumulateSweepResponse(totals, seen, &sweepFunctionResponse{Name: "read", Response: []byte(tt.body)})

			u := totals.tool("read")
			if u.Bytes != len(tt.body) || totals.ToolBytes != len(tt.body) {
				t.Errorf("bytes = %d/%d, want %d", u.Bytes, totals.ToolBytes, len(tt.body))
			}
			if u.Errors != tt.wantErrors {
				t.Errorf("errors = %d, want %d", u.Errors, tt.wantErrors)
			}
			if u.Oversized != tt.wantOversized {
				t.Errorf("oversized = %d, want %d", u.Oversized, tt.wantOversized)
			}
			if totals.DupBytes != tt.wantDup {
				t.Errorf("DupBytes = %d, want %d", totals.DupBytes, tt.wantDup)
			}
			if !seen[tt.body] {
				t.Error("body was not recorded as seen")
			}
		})
	}
}

// --- subagent pipeline ---

// TestCogASubagentPipelineLimits pins the over-limit output for both
// multi-agent modes, including the two differently-worded messages.
func TestCogASubagentPipelineLimits(t *testing.T) {
	orch := noRoleOrchestrator(t, "explore")

	t.Run("parallel", func(t *testing.T) {
		tasks := make([]TaskItem, maxParallelTasks+1)
		for i := range tasks {
			tasks[i] = TaskItem{Agent: "explore", Task: "t"}
		}
		out, err := subagentHandler(nil, orch, SubagentInput{Tasks: tasks}, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if out.Mode != "parallel" {
			t.Errorf("mode = %q, want parallel", out.Mode)
		}
		if out.Summary != "too many parallel tasks: 9 (max 8)" {
			t.Errorf("summary = %q", out.Summary)
		}
		if len(out.Results) != 1 {
			t.Fatalf("results = %d, want 1", len(out.Results))
		}
		r := out.Results[0]
		if r.Agent != "parallel" || r.Status != "failed" {
			t.Errorf("result = %+v, want agent parallel status failed", r)
		}
		if r.Error != "too many parallel tasks: 9 exceeds maximum of 8" {
			t.Errorf("error = %q", r.Error)
		}
		if r.Duration == "" {
			t.Error("result carries no duration")
		}
	})

	t.Run("chain", func(t *testing.T) {
		steps := make([]ChainItem, maxChainSteps+1)
		for i := range steps {
			steps[i] = ChainItem{Agent: "explore", Task: "t"}
		}
		out, err := subagentHandler(nil, orch, SubagentInput{Chain: steps}, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if out.Mode != "chain" {
			t.Errorf("mode = %q, want chain", out.Mode)
		}
		if out.Summary != "too many chain steps: 9 (max 8)" {
			t.Errorf("summary = %q", out.Summary)
		}
		if len(out.Results) != 1 {
			t.Fatalf("results = %d, want 1", len(out.Results))
		}
		r := out.Results[0]
		if r.Agent != "chain" || r.Status != "failed" {
			t.Errorf("result = %+v, want agent chain status failed", r)
		}
		if r.Error != "too many chain steps: 9 exceeds maximum of 8" {
			t.Errorf("error = %q", r.Error)
		}
	})
}

// TestCogASubagentUnknownAgentStopsBeforeSpawning pins that validation runs
// over the whole batch up front: an unknown agent in the second slot fails
// the call, and the report names that agent, not the first.
func TestCogASubagentUnknownAgentStopsBeforeSpawning(t *testing.T) {
	orch := noRoleOrchestrator(t, "explore", "review")

	check := func(t *testing.T, out SubagentOutput, wantMode string) {
		t.Helper()
		if out.Mode != wantMode {
			t.Errorf("mode = %q, want %q", out.Mode, wantMode)
		}
		if out.Summary != `validation failed: unknown agent "ghost"` {
			t.Errorf("summary = %q", out.Summary)
		}
		if len(out.Results) != 1 {
			t.Fatalf("results = %d, want 1 — validation reports the offender only", len(out.Results))
		}
		r := out.Results[0]
		if r.Agent != "ghost" || r.Status != "failed" {
			t.Errorf("result = %+v, want agent ghost status failed", r)
		}
		// The available-agent list is map-ordered, so pin the prefix only.
		if !strings.HasPrefix(r.Error, `unknown agent "ghost"`) {
			t.Errorf("error = %q, want it to start with `unknown agent \"ghost\"`", r.Error)
		}
	}

	t.Run("parallel", func(t *testing.T) {
		out, err := subagentHandler(nil, orch, SubagentInput{Tasks: []TaskItem{
			{Agent: "explore", Task: "a"}, {Agent: "ghost", Task: "b"},
		}}, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		check(t, out, "parallel")
	})

	t.Run("chain", func(t *testing.T) {
		out, err := subagentHandler(nil, orch, SubagentInput{Chain: []ChainItem{
			{Agent: "explore", Task: "a"}, {Agent: "ghost", Task: "b"},
		}}, nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		check(t, out, "chain")
	})
}

// TestCogACheckSubagentPipelineAccepts pins the pass-through: a call within
// the limit whose agents all exist reports ok and an empty output.
func TestCogACheckSubagentPipelineAccepts(t *testing.T) {
	orch := noRoleOrchestrator(t, "explore")
	spec := subagentPipelineSpec{Mode: "parallel", Noun: "parallel tasks", Limit: maxParallelTasks}

	out, ok := checkSubagentPipeline(orch, spec, []subagentTask{{Agent: "explore", Task: "a"}}, time.Now())
	if !ok {
		t.Fatalf("ok = false, want true (out = %+v)", out)
	}
	if !reflect.DeepEqual(out, SubagentOutput{}) {
		t.Errorf("out = %+v, want the zero output", out)
	}

	// An empty batch is under the limit and names no agent: also fine.
	if _, ok := checkSubagentPipeline(orch, spec, nil, time.Now()); !ok {
		t.Error("an empty batch was rejected")
	}
}

// TestCogASubagentSpawnFailureGolden pins how each mode reports a spawn that
// never starts: parallel runs every task and reports each one, chain stops at
// the step that failed.
func TestCogASubagentSpawnFailureGolden(t *testing.T) {
	t.Run("parallel reports every task", func(t *testing.T) {
		orch := noRoleOrchestrator(t, "explore", "review")
		out, err := subagentHandler(nil, orch, SubagentInput{Tasks: []TaskItem{
			{Agent: "explore", Task: "a"}, {Agent: "review", Task: "b"},
		}}, nil)
		if err != nil {
			t.Fatalf("a spawn failure must be reported, not returned: %v", err)
		}
		if len(out.Results) != 2 {
			t.Fatalf("results = %d, want 2", len(out.Results))
		}
		for i, want := range []string{"explore", "review"} {
			r := out.Results[i]
			if r.Agent != want {
				t.Errorf("results[%d].Agent = %q, want %q — order follows the input", i, r.Agent, want)
			}
			if r.Status != "failed" || r.Error == "" {
				t.Errorf("results[%d] = %+v, want a failed result carrying an error", i, r)
			}
			if r.AgentID != "" {
				t.Errorf("results[%d].AgentID = %q, want empty — nothing spawned", i, r.AgentID)
			}
		}
		if !strings.HasPrefix(out.Summary, "parallel: 0/2 completed, 2 failed in ") {
			t.Errorf("summary = %q", out.Summary)
		}
	})

	t.Run("chain stops at the failed step", func(t *testing.T) {
		orch := noRoleOrchestrator(t, "explore", "review")
		out, err := subagentHandler(nil, orch, SubagentInput{Chain: []ChainItem{
			{Agent: "explore", Task: "a"}, {Agent: "review", Task: "b {previous}"},
		}}, nil)
		if err != nil {
			t.Fatalf("a spawn failure must be reported, not returned: %v", err)
		}
		if len(out.Results) != 1 {
			t.Fatalf("results = %d, want 1 — the chain must not run step 2", len(out.Results))
		}
		r := out.Results[0]
		if r.Agent != "explore" || r.Status != "failed" || r.Error == "" {
			t.Errorf("result = %+v, want a failed explore result carrying an error", r)
		}
		if !strings.HasPrefix(out.Summary, "chain: stopped at step 1/2 in ") {
			t.Errorf("summary = %q", out.Summary)
		}
	})
}

// cogAEventChan feeds a closed channel of events, standing in for a live
// agent so the forwarding loops can be exercised without spawning one.
func cogAEventChan(evs ...subagent.Event) <-chan subagent.Event {
	ch := make(chan subagent.Event, len(evs))
	for _, ev := range evs {
		ch <- ev
	}
	close(ch)
	return ch
}

// TestCogAForwardSubagentEvents covers the loop parallel and chain share:
// text accumulation, the error path with its content fallback, session-id
// capture, and the event metadata stamped on every forwarded event.
func TestCogAForwardSubagentEvents(t *testing.T) {
	meta := subagentStepMeta{PipelineID: "pipe-1", Mode: "parallel", Step: 2, Total: 3}

	t.Run("text is accumulated and metadata stamped", func(t *testing.T) {
		var seen []SubagentEvent
		text, status, errMsg, sessID := forwardSubagentEvents(
			cogAEventChan(
				subagent.Event{Type: "message_start", SessionID: "sess-1"},
				subagent.Event{Type: "text_delta", Content: "hello "},
				subagent.Event{Type: "text_delta", Content: "world"},
			),
			func(ev SubagentEvent) { seen = append(seen, ev) },
			"agent-7", meta,
		)
		if text != "hello world" {
			t.Errorf("text = %q, want %q", text, "hello world")
		}
		if status != "completed" || errMsg != "" {
			t.Errorf("status/err = %q/%q, want completed/empty", status, errMsg)
		}
		if sessID != "sess-1" {
			t.Errorf("sessID = %q, want sess-1", sessID)
		}
		if len(seen) != 3 {
			t.Fatalf("forwarded %d events, want 3", len(seen))
		}
		for i, ev := range seen {
			if ev.AgentID != "agent-7" || ev.PipelineID != "pipe-1" || ev.Mode != "parallel" || ev.Step != 2 || ev.Total != 3 {
				t.Errorf("event %d = %+v, want the step metadata stamped on it", i, ev)
			}
		}
	})

	t.Run("error sets the status and fills empty content", func(t *testing.T) {
		var seen []SubagentEvent
		_, status, errMsg, _ := forwardSubagentEvents(
			cogAEventChan(subagent.Event{Type: "error", Error: "boom"}),
			func(ev SubagentEvent) { seen = append(seen, ev) },
			"agent-7", meta,
		)
		if status != "failed" || errMsg != "boom" {
			t.Errorf("status/err = %q/%q, want failed/boom", status, errMsg)
		}
		if len(seen) != 1 || seen[0].Content != "boom" {
			t.Errorf("forwarded = %+v, want the error text as content", seen)
		}
	})

	t.Run("error keeps its own content when it has some", func(t *testing.T) {
		var seen []SubagentEvent
		forwardSubagentEvents(
			cogAEventChan(subagent.Event{Type: "error", Content: "detail", Error: "boom"}),
			func(ev SubagentEvent) { seen = append(seen, ev) },
			"agent-7", meta,
		)
		if len(seen) != 1 || seen[0].Content != "detail" {
			t.Errorf("forwarded = %+v, want content left alone", seen)
		}
	})

	t.Run("an empty session id does not overwrite", func(t *testing.T) {
		_, _, _, sessID := forwardSubagentEvents(
			cogAEventChan(
				subagent.Event{Type: "message_start", SessionID: "sess-1"},
				subagent.Event{Type: "message_start"},
			),
			nil, "agent-7", meta,
		)
		if sessID != "sess-1" {
			t.Errorf("sessID = %q, want sess-1", sessID)
		}
	})

	t.Run("run_done is forwarded but does not change the status", func(t *testing.T) {
		// Parallel and chain report "completed" for a timed-out run; only
		// single mode reads the terminal status. Pinned so a later merge of
		// the two loops cannot change it silently.
		var seen []SubagentEvent
		_, status, _, _ := forwardSubagentEvents(
			cogAEventChan(subagent.Event{Type: "run_done", Status: "timeout"}),
			func(ev SubagentEvent) { seen = append(seen, ev) },
			"agent-7", meta,
		)
		if status != "completed" {
			t.Errorf("status = %q, want completed", status)
		}
		if len(seen) != 1 || seen[0].Kind != "run_done" {
			t.Errorf("forwarded = %+v, want the run_done event passed through", seen)
		}
	})

	t.Run("a nil callback is safe", func(t *testing.T) {
		text, status, _, _ := forwardSubagentEvents(
			cogAEventChan(subagent.Event{Type: "text_delta", Content: "hi"}), nil, "agent-7", meta)
		if text != "hi" || status != "completed" {
			t.Errorf("text/status = %q/%q, want hi/completed", text, status)
		}
	})
}

// TestCogAForwardSingleModeEvents covers single mode's extra arm: run_done
// promotes the status to "timeout", and a later error still wins because the
// stream is read in order.
func TestCogAForwardSingleModeEvents(t *testing.T) {
	t.Run("run_done timeout becomes the status", func(t *testing.T) {
		var seen []SubagentEvent
		text, status, errMsg, sessID := forwardSingleModeEvents(
			cogAEventChan(
				subagent.Event{Type: "message_start", SessionID: "sub-1"},
				subagent.Event{Type: "text_delta", Content: "partial"},
				subagent.Event{Type: "run_done", Status: "timeout"},
			),
			func(ev SubagentEvent) { seen = append(seen, ev) },
			"agent-1", "pipe-9",
		)
		if status != "timeout" {
			t.Errorf("status = %q, want timeout", status)
		}
		if text != "partial" || errMsg != "" || sessID != "sub-1" {
			t.Errorf("text/err/sess = %q/%q/%q", text, errMsg, sessID)
		}
		for i, ev := range seen {
			if ev.Mode != "single" || ev.Step != 1 || ev.Total != 1 || ev.PipelineID != "pipe-9" {
				t.Errorf("event %d = %+v, want single-mode metadata", i, ev)
			}
		}
	})

	t.Run("a non-timeout run_done leaves the status alone", func(t *testing.T) {
		_, status, _, _ := forwardSingleModeEvents(
			cogAEventChan(subagent.Event{Type: "run_done", Status: "completed"}), nil, "agent-1", "pipe-9")
		if status != "completed" {
			t.Errorf("status = %q, want completed", status)
		}
	})

	t.Run("the last terminal event in the stream wins", func(t *testing.T) {
		_, status, errMsg, _ := forwardSingleModeEvents(
			cogAEventChan(
				subagent.Event{Type: "run_done", Status: "timeout"},
				subagent.Event{Type: "error", Error: "crashed"},
			), nil, "agent-1", "pipe-9")
		if status != "failed" || errMsg != "crashed" {
			t.Errorf("status/err = %q/%q, want failed/crashed", status, errMsg)
		}
	})

	t.Run("error content falls back to the error text", func(t *testing.T) {
		var seen []SubagentEvent
		forwardSingleModeEvents(
			cogAEventChan(subagent.Event{Type: "error", Error: "boom"}),
			func(ev SubagentEvent) { seen = append(seen, ev) }, "agent-1", "pipe-9")
		if len(seen) != 1 || seen[0].Content != "boom" {
			t.Errorf("forwarded = %+v, want the error text as content", seen)
		}
	})
}

// TestCogASubagentTaskConversion pins that the two input shapes convert to the
// shared task without reordering or losing a field.
func TestCogASubagentTaskConversion(t *testing.T) {
	if got := subagentTask(TaskItem{Agent: "a", Task: "t"}); got != (subagentTask{Agent: "a", Task: "t"}) {
		t.Errorf("from TaskItem = %+v", got)
	}
	if got := subagentTask(ChainItem{Agent: "b", Task: "u"}); got != (subagentTask{Agent: "b", Task: "u"}) {
		t.Errorf("from ChainItem = %+v", got)
	}
}
