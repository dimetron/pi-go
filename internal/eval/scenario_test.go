package eval

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/dimetron/pi-go/internal/atif"
	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/memory"
)

func TestEvaluateScenario_ToolsAndChecks(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "out.txt"), []byte("hello from pi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	child := &atif.Trajectory{SessionID: "child", Steps: []atif.Step{
		callStep(1, "k1", "ripgrep", map[string]any{"pattern": "NEEDLE"}, map[string]any{"matches": []any{map[string]any{"file": "docs/notes.md"}}}),
	}}
	childPath := writeTraj(t, t.TempDir(), "child", child) // only the ref matters
	parent := &atif.Trajectory{SessionID: "parent", Steps: []atif.Step{
		callStep(1, "c1", "write", map[string]any{"path": "out.txt", "content": "hello from pi\n"}, map[string]any{"success": true}),
		callStep(2, "c2", "bash", map[string]any{"command": "wc -l x"}, map[string]any{"exit_code": 1, "stderr": "no such file"}),
		callStep(3, "c3", "bash", map[string]any{"command": "wc -l out.txt"}, map[string]any{"exit_code": 0, "stdout": "1"}),
		{
			StepID:    4,
			ToolCalls: []atif.ToolCall{{ToolCallID: "c4", FunctionName: "subagent", Arguments: map[string]any{"agent": "explore"}}},
			Observation: &atif.Observation{Results: []atif.ObservationResult{{
				SourceCallID:          "c4",
				Content:               map[string]any{"summary": "found docs/notes.md"},
				SubagentTrajectoryRef: childPath,
			}}},
		},
		callStep(5, "c5", "read", map[string]any{"path": "missing"}, map[string]any{"error": "not found"}),
	}}
	loaded := loadFixture(t, parent, child)

	s := Scenario{
		Name:  "synthetic",
		Tools: []string{"write", "bash", "subagent", "grep|ripgrep", "read", "edit"},
		Checks: []Check{
			{Kind: CheckFileExists, Path: "out.txt"},
			{Kind: CheckFileAbsent, Path: "nope.txt"},
			{Kind: CheckFileContains, Path: "out.txt", Text: "hello"},
			{Kind: CheckFileNotContains, Path: "out.txt", Text: "goodbye"},
			{Kind: CheckToolArgContains, Tool: "bash", Arg: "command", Text: "wc"},
			{Kind: CheckToolArgContains, Tool: "bash", Text: "out.txt"},
			{Kind: CheckToolResultContains, Tool: "bash", Text: `"stdout":"1"`},
			{Kind: CheckToolResultContains, Tool: "grep|ripgrep", Text: "notes.md"},
			{Kind: CheckSubagentSpawned},
			{Kind: CheckToolCalledAtLeast, Tool: "bash", N: 2},
			// failing ones
			{Kind: CheckFileContains, Path: "out.txt", Text: "absent-text"},
			{Kind: CheckToolArgContains, Tool: "write", Arg: "path", Text: "other.txt"},
			{Kind: CheckToolCalledAtLeast, Tool: "write", N: 3},
			{Kind: CheckFileExists, Path: "nope.txt"},
		},
	}
	res := EvaluateScenario(s, dir, loaded)

	if res.Status != StatusFail {
		t.Fatalf("status = %s, want fail", res.Status)
	}
	if res.Sessions != 2 || res.ToolCalls != 6 {
		t.Errorf("sessions=%d toolCalls=%d", res.Sessions, res.ToolCalls)
	}
	outcomes := make(map[string]ToolOutcome)
	for _, o := range res.Tools {
		outcomes[o.Tool] = o
	}
	if o := outcomes["bash"]; !o.OK || o.Calls != 2 || o.Errors != 1 {
		t.Errorf("bash outcome = %+v", o)
	}
	if o := outcomes["read"]; o.OK || o.Calls != 1 || o.Errors != 1 {
		t.Errorf("read outcome = %+v (all-error call must not be OK)", o)
	}
	if o := outcomes["edit"]; o.OK || o.Calls != 0 {
		t.Errorf("edit outcome = %+v", o)
	}
	if o := outcomes["grep|ripgrep"]; !o.OK {
		t.Errorf("grep alternatives outcome = %+v", o)
	}
	if !strings.Contains(res.Reason, "tool edit: never called") || !strings.Contains(res.Reason, "tool read: 1 call(s), all 1 result(s) look like errors") {
		t.Errorf("reason = %q", res.Reason)
	}

	passed := 0
	for i, c := range res.Checks {
		wantPass := i < 10
		if c.Passed != wantPass {
			t.Errorf("check %d %q: passed=%v detail=%q", i, c.Check, c.Passed, c.Detail)
		}
		if c.Passed {
			passed++
		}
	}
	if passed != 10 {
		t.Errorf("passed %d checks, want 10", passed)
	}
}

func TestEvaluateScenario_PassAndUnknownCheck(t *testing.T) {
	loaded := loadFixture(t, &atif.Trajectory{SessionID: "s", Steps: []atif.Step{
		callStep(1, "c1", "ls", map[string]any{"path": "."}, map[string]any{"entries": []any{}}),
	}})
	res := EvaluateScenario(Scenario{Name: "ok", Tools: []string{"ls"}}, t.TempDir(), loaded)
	if res.Status != StatusPass || res.Reason != "" {
		t.Fatalf("res = %+v", res)
	}
	res = EvaluateScenario(Scenario{Name: "bad", Tools: []string{"ls"}, Checks: []Check{{Kind: "bogus"}}}, t.TempDir(), loaded)
	if res.Status != StatusFail || !strings.Contains(res.Reason, "unknown check kind") {
		t.Fatalf("res = %+v", res)
	}
	// Wasted call: the tool was asked for but never answered.
	wasted := loadFixture(t, &atif.Trajectory{SessionID: "w", Steps: []atif.Step{callStep(1, "c1", "ls", nil, nil)}})
	res = EvaluateScenario(Scenario{Name: "w", Tools: []string{"ls"}}, t.TempDir(), wasted)
	if res.Status != StatusFail || !strings.Contains(res.Reason, "no result") {
		t.Fatalf("res = %+v", res)
	}
}

func TestCheck_String(t *testing.T) {
	cases := map[string]Check{
		"file_exists a":                       {Kind: CheckFileExists, Path: "a"},
		`file_contains a "x"`:                 {Kind: CheckFileContains, Path: "a", Text: "x"},
		`tool_arg_contains bash.command "wc"`: {Kind: CheckToolArgContains, Tool: "bash", Arg: "command", Text: "wc"},
		`tool_arg_contains bash "wc"`:         {Kind: CheckToolArgContains, Tool: "bash", Text: "wc"},
		`tool_result_contains read "x"`:       {Kind: CheckToolResultContains, Tool: "read", Text: "x"},
		"tool_called_at_least bash 2":         {Kind: CheckToolCalledAtLeast, Tool: "bash", N: 2},
		"subagent_spawned":                    {Kind: CheckSubagentSpawned},
	}
	for want, c := range cases {
		if got := c.String(); got != want {
			t.Errorf("String() = %q, want %q", got, want)
		}
	}
}

func TestSeedWorkspace_GitHistoryAndModifications(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	s := Scenario{
		Git:      true,
		Files:    map[string]string{"a.txt": "one\n", "sub/b.txt": "two\n"},
		Modified: map[string]string{"a.txt": "one\nmore\n"},
	}
	if err := SeedWorkspace(s, dir, t.TempDir()); err != nil {
		t.Fatalf("SeedWorkspace: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, "a.txt"))
	if string(got) != "one\nmore\n" {
		t.Errorf("a.txt = %q", got)
	}
	out, err := exec.Command("git", "-C", dir, "status", "--porcelain").Output()
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(out)) != "M a.txt" {
		t.Errorf("git status = %q, want only a.txt modified", out)
	}
	log, _ := exec.Command("git", "-C", dir, "log", "--format=%an %s").Output()
	if !strings.Contains(string(log), "pi-eval initial fixture commit") {
		t.Errorf("git log = %q", log)
	}
	// Commit must not be signed: no gpgsig header.
	raw, _ := exec.Command("git", "-C", dir, "cat-file", "commit", "HEAD").Output()
	if strings.Contains(string(raw), "gpgsig") {
		t.Error("fixture commit is signed; gitEnv should force signing off")
	}
}

func TestHomeEnv(t *testing.T) {
	home := t.TempDir()
	env := HomeEnv(home)
	if env[0] != "HOME="+home {
		t.Errorf("HomeEnv[0] = %q", env[0])
	}
	wantLen := 1
	if runtime.GOOS == "windows" {
		wantLen = 2
		if env[1] != "USERPROFILE="+home {
			t.Errorf("HomeEnv[1] = %q, want USERPROFILE (os.UserHomeDir reads it on Windows)", env[1])
		}
	}
	if len(env) != wantLen {
		t.Errorf("HomeEnv = %v, want %d entries", env, wantLen)
	}
	// gitEnv must carry the same redirection plus the signing/autocrlf overrides.
	git := strings.Join(gitEnv(home), "\n")
	for _, want := range []string{"HOME=" + home, "commit.gpgsign", "tag.gpgsign", "core.autocrlf", "GIT_CONFIG_VALUE_3=false"} {
		if !strings.Contains(git, want) {
			t.Errorf("gitEnv missing %q", want)
		}
	}
}

func TestSeedWorkspace_NoGit(t *testing.T) {
	dir := t.TempDir()
	if err := SeedWorkspace(Scenario{Files: map[string]string{"x/y.txt": "z"}}, dir, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		t.Error("unexpected .git")
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "x", "y.txt")); string(got) != "z" {
		t.Errorf("y.txt = %q", got)
	}
}

func TestSeedMemory_SearchableByTool(t *testing.T) {
	home := t.TempDir()
	ctx := context.Background()
	seeds := []MemorySeed{
		{Title: "Widget rotation bug", Text: "widget rotation was wrong because rows were swapped", Type: "bugfix"},
		{Title: "Cache decision", Text: "chose sqlite for the cache"},
	}
	if err := SeedMemory(ctx, home, "/tmp/proj", seeds); err != nil {
		t.Fatalf("SeedMemory: %v", err)
	}
	if err := SeedMemory(ctx, home, "/tmp/proj", nil); err != nil {
		t.Fatalf("SeedMemory(nil) = %v", err)
	}

	db, err := memory.OpenDB(filepath.Join(home, ".pi-go", "memory", "claude-mem.db"))
	if err != nil {
		t.Fatal(err)
	}
	store := memory.NewSQLiteStore(db)
	defer store.Close()
	res, err := store.Search(ctx, memory.SearchQuery{Query: "widget rotation", Limit: 10})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if res == nil || len(res.Rows) != 1 || !strings.Contains(res.Rows[0].Title, "rotation") {
		t.Fatalf("search result = %+v", res)
	}
	recent, err := store.RecentObservations(ctx, "/tmp/proj", 10)
	if err != nil || len(recent) != 2 {
		t.Fatalf("recent = %d, err=%v", len(recent), err)
	}
	if recent[0].Type != memory.TypeBugfix && recent[1].Type != memory.TypeBugfix {
		t.Errorf("bugfix type not preserved: %+v", recent)
	}
}

func TestScenarioConfig(t *testing.T) {
	cfg := ScenarioConfig(Scenario{}, "test-model", "")
	for _, role := range []string{"default", "smol", "slow", "plan"} {
		if cfg.Roles[role].Model != "test-model" {
			t.Errorf("role %s model = %q", role, cfg.Roles[role].Model)
		}
	}
	if cfg.Memory == nil || cfg.Memory.Enabled == nil || *cfg.Memory.Enabled {
		t.Error("memory should be disabled without seeds")
	}
	if cfg.Palace == nil || cfg.Palace.Enabled == nil || *cfg.Palace.Enabled {
		t.Error("palace should be disabled")
	}
	if cfg.LLMS != nil || cfg.Hooks != nil || cfg.MCP != nil || cfg.A2A != nil {
		t.Errorf("unexpected optional config: %+v", cfg)
	}
	if cfg.ThinkingLevel != "none" {
		t.Errorf("thinking = %q, want none by default", cfg.ThinkingLevel)
	}

	cfg = ScenarioConfig(Scenario{
		Memory: []MemorySeed{{Title: "t", Text: "x"}},
		LLMS:   []config.LLMSSource{{Name: "n", URL: "https://example.com/llms.txt"}},
	}, "", "medium")
	if cfg.ThinkingLevel != "medium" {
		t.Errorf("thinking = %q, want medium", cfg.ThinkingLevel)
	}
	if !*cfg.Memory.Enabled {
		t.Error("memory should be enabled with seeds")
	}
	if cfg.LLMS == nil || len(cfg.LLMS.Sources) != 1 {
		t.Error("llms sources not carried")
	}
	if cfg.Roles["default"].Model != config.Defaults().Roles["default"].Model {
		t.Error("empty model must keep the default")
	}
}

func TestToolsReport_RenderAndWrite(t *testing.T) {
	r := &ToolsReport{
		Metadata: ToolsReportMetadata{Model: "m", Binary: "/bin/pi", GitHead: "abcdef123456789", Timestamp: time.Date(2026, 8, 22, 10, 0, 0, 0, time.UTC), Duration: "1m", Selected: "grep"},
		Scenarios: []ScenarioResult{
			{Name: "explore", Status: StatusPass, Tools: []ToolOutcome{{Tool: "ls", OK: true}}, Sessions: 1, ToolCalls: 3, Duration: "10s",
				Checks: []CheckOutcome{{Check: "file_exists a", Passed: true}}},
			{Name: "git", Status: StatusFail, Reason: "tool git-hunk: never called | pipe", Tools: []ToolOutcome{{Tool: "git-hunk"}},
				Checks: []CheckOutcome{{Check: "x", Passed: false, Detail: "no | match"}}},
			{Name: "lsp", Status: StatusSkip, Reason: "requires lsp"},
			{Name: "boom", Status: StatusError, Reason: "pi: exit 1"},
		},
		Coverage: ComputeCoverage(
			[]ToolInfo{{Name: "ls", Group: "core"}, {Name: "git-hunk", Group: "core"}, {Name: "a2a", Group: "a2a"}},
			[]Scenario{{Name: "explore", Tools: []string{"ls"}}, {Name: "git", Tools: []string{"git-hunk"}}},
			[]Exclusion{{Tool: "a2a", Reason: "external"}},
			loadFixture(t, &atif.Trajectory{SessionID: "s", Steps: []atif.Step{callStep(1, "c1", "ls", nil, map[string]any{"ok": true})}}),
		),
		Tools:  ToolsMetrics{TotalCalls: 1, ByTool: map[string]ToolStats{"ls": {Calls: 1, Results: 1}}},
		Tokens: TokenMetrics{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15, CachedTokens: 2},
		Judge:  &JudgeVerdict{Model: "j", Verdict: "pass", Overall: 4, Scores: []JudgeScore{{Dimension: "tool_selection", Score: 4, Rationale: "fine"}}},
	}
	r.Tally()
	if r.Metadata.Passed != 1 || r.Metadata.Failed != 1 || r.Metadata.Skipped != 1 || r.Metadata.Errored != 1 {
		t.Errorf("tally = %+v", r.Metadata)
	}

	md := RenderToolsMarkdown(r)
	for _, want := range []string{
		"# Eval run: tool coverage",
		"**selected**: `grep`",
		"1 passed, 1 failed, 1 skipped, 1 errored",
		"| `explore` | PASS |",
		"| `git` | FAIL |",
		"| `lsp` | skip |",
		"| `boom` | ERROR |",
		`never called \| pipe`,
		"## Tool coverage",
		"**registered tools**: 3",
		"**gap**: `git-hunk`",
		"| `a2a` | a2a | excluded |",
		"excluded: external",
		"## LLM judge",
		"| tool_selection | 4/5 | fine |",
		"## Tools efficiency",
		"**cached tokens**: 2",
		"✗ `x` — no \\| match",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown missing %q\n%s", want, md)
		}
	}

	out := t.TempDir()
	jsonPath, mdPath, md2, err := WriteToolsReport(r, out)
	if err != nil {
		t.Fatal(err)
	}
	if md2 != md {
		t.Error("returned markdown differs from render")
	}
	if filepath.Base(jsonPath) != "eval-tools-20260822-100000.json" || filepath.Base(mdPath) != "eval-tools-20260822-100000.md" {
		t.Errorf("paths = %s, %s", jsonPath, mdPath)
	}
	data, err := os.ReadFile(jsonPath)
	if err != nil || !strings.Contains(string(data), `"coverage"`) {
		t.Errorf("json report: err=%v", err)
	}
}

func TestJudgeTools(t *testing.T) {
	r := &ToolsReport{
		Metadata:  ToolsReportMetadata{Model: "m", Passed: 1, Failed: 1},
		Scenarios: []ScenarioResult{{Name: "explore", Status: StatusPass, Sessions: 1, ToolCalls: 2}, {Name: "git", Status: StatusFail, Reason: "tool git-hunk: never called"}},
		Coverage:  Coverage{Total: 3, OK: 1, Gap: []string{"git-hunk", "write"}},
		Tools:     ToolsMetrics{TotalCalls: 2, ByTool: map[string]ToolStats{"ls": {Calls: 2, Results: 2}}},
	}
	prompt := BuildToolsJudgePrompt(r, "## scenario explore\n1. ls() -> 10 bytes\n")
	for _, want := range []string{"Tool-coverage suite under review", "- explore: pass", "- git: fail — tool git-hunk: never called",
		"gap: git-hunk, write", "| ls | 2 |", "## Tool-call timelines", "1. ls()"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q\n%s", want, prompt)
		}
	}
	if got := BuildToolsJudgePrompt(nil, ""); !strings.Contains(got, "Tool-coverage suite under review") {
		t.Errorf("nil report prompt = %q", got)
	}

	ctx := context.Background()
	if v := JudgeTools(ctx, nil, "m", r, ""); v.Error != "no judge model configured" {
		t.Errorf("nil complete: %+v", v)
	}
	failing := func(context.Context, string, string) (string, error) { return "", errors.New("transport down") }
	if v := JudgeTools(ctx, failing, "m", r, ""); v.Error != "transport down" {
		t.Errorf("failing complete: %+v", v)
	}
	garbage := func(context.Context, string, string) (string, error) { return "no json here", nil }
	if v := JudgeTools(ctx, garbage, "m", r, ""); v.Error == "" {
		t.Errorf("garbage reply should error: %+v", v)
	}
	var gotSystem, gotUser string
	ok := func(_ context.Context, system, user string) (string, error) {
		gotSystem, gotUser = system, user
		return `{"scores":[{"dimension":"outcome_correctness","score":4,"rationale":"r"},{"dimension":"tool_selection","score":5,"rationale":"r"}],"verdict":"pass","summary":"s"}`, nil
	}
	v := JudgeTools(ctx, ok, "judge-model", r, "digest-text")
	if v.Error != "" || v.Model != "judge-model" || v.Verdict != "pass" || v.Overall != 4.5 {
		t.Errorf("verdict = %+v", v)
	}
	if !strings.Contains(gotSystem, "tool_selection") || !strings.Contains(gotUser, "digest-text") {
		t.Error("judge did not receive the tools prompt and digest")
	}
}

func TestProviderComplete_Unavailable(t *testing.T) {
	if fn, reason := ProviderComplete(""); fn != nil || reason == "" {
		t.Errorf("empty model: fn=%v reason=%q", fn != nil, reason)
	}
	if fn, reason := ProviderComplete("no-such-provider/definitely-not-a-model"); fn != nil || reason == "" {
		// Either unresolvable or no key — both must come back as a reason, not a panic.
		t.Errorf("bogus model: fn=%v reason=%q", fn != nil, reason)
	}
}
