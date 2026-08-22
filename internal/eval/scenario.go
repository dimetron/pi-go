package eval

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/memory"
)

// Scenario is one tool-coverage eval case: a seeded workspace, a prompt that
// should make a competent agent reach for specific tools, and deterministic
// assertions over the resulting trajectory and filesystem.
//
// Scenarios are declarative data so the table (internal/eval/scenarios) reads
// as a spec and the runner (the e2e test in the same package) stays generic.
type Scenario struct {
	// Name identifies the scenario in reports and selects it with
	// PI_EVAL_SCENARIO / -run. Lowercase, dash-separated.
	Name string `json:"name"`
	// Description says what the scenario exercises, for the report.
	Description string `json:"description,omitempty"`
	// Tools the agent must call at least once, each with at least one
	// non-error result. An entry may list alternatives separated by "|"
	// ("grep|ripgrep"): any one of them satisfies the requirement, and the
	// scenario counts as covering all of them.
	Tools []string `json:"tools"`
	// Requires lists capabilities the host must provide: "lsp" (a language
	// server on PATH), "network" (outbound HTTPS), "vision" (a model that
	// accepts images). A scenario whose requirement is unmet is skipped, not
	// failed.
	Requires []string `json:"requires,omitempty"`
	// Files are fixture files written into the workdir before the run, keyed
	// by relative path.
	Files map[string]string `json:"files,omitempty"`
	// Git initializes a repository in the workdir and commits Files as the
	// initial commit, so git-* tools have history to look at.
	Git bool `json:"git,omitempty"`
	// Modified files are written after the initial commit (uncommitted
	// changes), so diffs are non-empty. Implies Git.
	Modified map[string]string `json:"modified,omitempty"`
	// Memory enables the observation memory system for the run and seeds
	// the store with the given observations, so mem-* tools have something
	// to find. Nil disables memory for the run (the default, because the
	// memory worker spawns compressor subagents after every tool call).
	Memory []MemorySeed `json:"memory,omitempty"`
	// LLMS sources to configure, which registers the fetch_docs tool.
	LLMS []config.LLMSSource `json:"llms,omitempty"`
	// Args are extra flags passed to the pi binary (e.g. --lsp full).
	Args []string `json:"args,omitempty"`
	// Prompt is the single user turn the agent runs.
	Prompt string `json:"prompt"`
	// Checks are the deterministic assertions evaluated after the run, on
	// top of the implicit "every target tool was called successfully".
	Checks []Check `json:"checks,omitempty"`
	// Timeout bounds one run of the scenario. Zero uses the runner default.
	Timeout time.Duration `json:"-"`
}

// MemorySeed is one observation written into the memory store before the run.
type MemorySeed struct {
	Title string `json:"title"`
	Text  string `json:"text"`
	Type  string `json:"type,omitempty"` // memory.ObservationType; default "discovery"
}

// CheckKind enumerates the assertion vocabulary.
type CheckKind string

const (
	// CheckFileExists: Path exists in the workdir.
	CheckFileExists CheckKind = "file_exists"
	// CheckFileAbsent: Path does not exist in the workdir.
	CheckFileAbsent CheckKind = "file_absent"
	// CheckFileContains: the file at Path contains Text.
	CheckFileContains CheckKind = "file_contains"
	// CheckFileNotContains: the file at Path does not contain Text.
	CheckFileNotContains CheckKind = "file_not_contains"
	// CheckToolArgContains: some call to Tool has an argument Arg whose
	// string form contains Text. Arg empty = any argument.
	CheckToolArgContains CheckKind = "tool_arg_contains"
	// CheckToolResultContains: some result of Tool contains Text (the
	// result is matched on its JSON/text rendering).
	CheckToolResultContains CheckKind = "tool_result_contains"
	// CheckSubagentSpawned: at least one observation links a nested
	// subagent trajectory, i.e. a child pi session really ran.
	CheckSubagentSpawned CheckKind = "subagent_spawned"
	// CheckToolCalledAtLeast: Tool was called at least N times.
	CheckToolCalledAtLeast CheckKind = "tool_called_at_least"
)

// Check is one deterministic assertion. Which fields matter depends on Kind.
type Check struct {
	Kind CheckKind `json:"kind"`
	Path string    `json:"path,omitempty"`
	Text string    `json:"text,omitempty"`
	Tool string    `json:"tool,omitempty"`
	Arg  string    `json:"arg,omitempty"`
	N    int       `json:"n,omitempty"`
}

// String renders the check for reports.
func (c Check) String() string {
	switch c.Kind {
	case CheckFileExists, CheckFileAbsent:
		return fmt.Sprintf("%s %s", c.Kind, c.Path)
	case CheckFileContains, CheckFileNotContains:
		return fmt.Sprintf("%s %s %q", c.Kind, c.Path, c.Text)
	case CheckToolArgContains:
		if c.Arg != "" {
			return fmt.Sprintf("%s %s.%s %q", c.Kind, c.Tool, c.Arg, c.Text)
		}
		return fmt.Sprintf("%s %s %q", c.Kind, c.Tool, c.Text)
	case CheckToolResultContains:
		return fmt.Sprintf("%s %s %q", c.Kind, c.Tool, c.Text)
	case CheckToolCalledAtLeast:
		return fmt.Sprintf("%s %s %d", c.Kind, c.Tool, c.N)
	default:
		return string(c.Kind)
	}
}

// --- Results ---

// ToolOutcome is what the trajectories show for one target tool.
type ToolOutcome struct {
	Tool    string `json:"tool"`
	Calls   int    `json:"calls"`
	Results int    `json:"results"`
	Errors  int    `json:"errors"`
	Wasted  int    `json:"wasted"`
	OK      bool   `json:"ok"`
}

// CheckOutcome is the result of one Check.
type CheckOutcome struct {
	Check  string `json:"check"`
	Passed bool   `json:"passed"`
	Detail string `json:"detail,omitempty"`
}

// ScenarioResult is the graded outcome of one scenario run.
type ScenarioResult struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// Status is "pass", "fail", "skip" or "error" (the run itself broke).
	Status   string         `json:"status"`
	Reason   string         `json:"reason,omitempty"`
	Duration string         `json:"duration,omitempty"`
	Tools    []ToolOutcome  `json:"tools,omitempty"`
	Checks   []CheckOutcome `json:"checks,omitempty"`
	Sessions int            `json:"sessions"`
	// ToolCalls is the total across every session the run produced,
	// including subagents.
	ToolCalls int `json:"tool_calls"`
	// ExitCode of the pi process, -1 when it did not exit cleanly.
	ExitCode int    `json:"exit_code"`
	Output   string `json:"output,omitempty"`
}

// Scenario result statuses.
const (
	StatusPass  = "pass"
	StatusFail  = "fail"
	StatusSkip  = "skip"
	StatusError = "error"
)

// EvaluateScenario grades a finished run: every target tool called with at
// least one non-error result, and every Check satisfied. workDir is the
// scenario's workspace after the run; loaded are the trajectories the run
// wrote (root session plus any subagents).
func EvaluateScenario(s Scenario, workDir string, loaded []*LoadedTrajectory) ScenarioResult {
	res := ScenarioResult{Name: s.Name, Description: s.Description, Status: StatusPass}
	stats := callStatsByTool(loaded)
	var failures []string

	for _, target := range s.Tools {
		out := targetOutcome(target, stats)
		res.Tools = append(res.Tools, out)
		if !out.OK {
			failures = append(failures, describeToolFailure(out))
		}
	}
	for _, c := range s.Checks {
		out := evaluateCheck(c, workDir, loaded)
		res.Checks = append(res.Checks, out)
		if !out.Passed {
			failures = append(failures, out.Check+": "+out.Detail)
		}
	}

	res.Sessions = len(loaded)
	for _, lt := range loaded {
		if lt != nil && lt.Traj != nil {
			res.ToolCalls += countToolCalls(lt.Traj)
		}
	}
	if len(failures) > 0 {
		res.Status = StatusFail
		res.Reason = strings.Join(failures, "; ")
	}
	return res
}

// targetOutcome folds the alternatives of one target ("grep|ripgrep") into a
// single outcome under the target's label.
func targetOutcome(target string, stats map[string]toolCallStats) ToolOutcome {
	out := ToolOutcome{Tool: target}
	for _, name := range strings.Split(target, "|") {
		st := stats[strings.TrimSpace(name)]
		out.Calls += st.calls
		out.Results += st.results
		out.Errors += st.errors
		out.Wasted += st.wasted
	}
	out.OK = out.Results-out.Errors > 0
	return out
}

func describeToolFailure(out ToolOutcome) string {
	switch {
	case out.Calls == 0:
		return fmt.Sprintf("tool %s: never called", out.Tool)
	case out.Results == 0:
		return fmt.Sprintf("tool %s: %d call(s), no result", out.Tool, out.Calls)
	default:
		return fmt.Sprintf("tool %s: %d call(s), all %d result(s) look like errors", out.Tool, out.Calls, out.Errors)
	}
}

// evaluateCheck runs one assertion against the workspace and trajectories.
func evaluateCheck(c Check, workDir string, loaded []*LoadedTrajectory) CheckOutcome {
	out := CheckOutcome{Check: c.String()}
	switch c.Kind {
	case CheckFileExists:
		out.Passed, out.Detail = fileExists(filepath.Join(workDir, c.Path))
	case CheckFileAbsent:
		exists, _ := fileExists(filepath.Join(workDir, c.Path))
		out.Passed = !exists
		if exists {
			out.Detail = "file exists"
		}
	case CheckFileContains:
		out.Passed, out.Detail = fileContains(filepath.Join(workDir, c.Path), c.Text, true)
	case CheckFileNotContains:
		out.Passed, out.Detail = fileContains(filepath.Join(workDir, c.Path), c.Text, false)
	case CheckToolArgContains:
		out.Passed, out.Detail = toolArgContains(loaded, c.Tool, c.Arg, c.Text)
	case CheckToolResultContains:
		out.Passed, out.Detail = toolResultContains(loaded, c.Tool, c.Text)
	case CheckSubagentSpawned:
		out.Passed, out.Detail = subagentSpawned(loaded)
	case CheckToolCalledAtLeast:
		n := callsTo(loaded, c.Tool)
		out.Passed = n >= c.N
		out.Detail = fmt.Sprintf("called %d time(s)", n)
	default:
		out.Detail = "unknown check kind"
	}
	return out
}

func fileExists(path string) (bool, string) {
	if _, err := os.Stat(path); err != nil {
		return false, err.Error()
	}
	return true, ""
}

func fileContains(path, text string, want bool) (bool, string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err.Error()
	}
	has := strings.Contains(string(data), text)
	if has == want {
		return true, ""
	}
	if want {
		return false, fmt.Sprintf("text not found in %d bytes", len(data))
	}
	return false, "text still present"
}

// recordedCall is one tool call with its (optional) observation result, as
// the checks see it.
type recordedCall struct {
	fn          string
	args        map[string]any
	result      any
	hasResult   bool
	subagentRef string
}

// callsOf collects every call to tool across the trajectories, paired with its
// observation. tool "" collects every call.
func callsOf(loaded []*LoadedTrajectory, tool string) []recordedCall {
	var out []recordedCall
	for _, lt := range loaded {
		if lt == nil || lt.Traj == nil {
			continue
		}
		results := resultsBySourceCall(lt)
		for _, step := range lt.Traj.Steps {
			for _, tc := range step.ToolCalls {
				if tool != "" && !toolMatches(tool, tc.FunctionName) {
					continue
				}
				rc := recordedCall{fn: tc.FunctionName, args: tc.Arguments}
				if res, ok := results[tc.ToolCallID]; ok {
					rc.result, rc.hasResult, rc.subagentRef = res.Content, true, res.SubagentTrajectoryRef
				}
				out = append(out, rc)
			}
		}
	}
	return out
}

// toolMatches honors "a|b" alternatives in a check's tool field.
func toolMatches(target, name string) bool {
	for _, alt := range strings.Split(target, "|") {
		if strings.TrimSpace(alt) == name {
			return true
		}
	}
	return false
}

func callsTo(loaded []*LoadedTrajectory, tool string) int {
	return len(callsOf(loaded, tool))
}

func toolArgContains(loaded []*LoadedTrajectory, tool, arg, text string) (bool, string) {
	calls := callsOf(loaded, tool)
	for _, rc := range calls {
		for k, v := range rc.args {
			if arg != "" && k != arg {
				continue
			}
			if strings.Contains(contentText(v), text) {
				return true, ""
			}
		}
	}
	return false, fmt.Sprintf("no match across %d call(s)", len(calls))
}

func toolResultContains(loaded []*LoadedTrajectory, tool, text string) (bool, string) {
	calls := callsOf(loaded, tool)
	for _, rc := range calls {
		if rc.hasResult && strings.Contains(contentText(rc.result), text) {
			return true, ""
		}
	}
	return false, fmt.Sprintf("no match across %d call(s)", len(calls))
}

func subagentSpawned(loaded []*LoadedTrajectory) (bool, string) {
	for _, rc := range callsOf(loaded, "") {
		if rc.subagentRef != "" {
			return true, ""
		}
	}
	if len(loaded) > 1 {
		return true, fmt.Sprintf("%d sessions present (no explicit subagent ref)", len(loaded))
	}
	return false, "no observation references a subagent trajectory"
}

// --- Workspace seeding ---

// gitEnv returns an environment for fixture git commands that cannot touch the
// user's identity or signing setup: HOME is the eval's isolated home, the
// author is synthetic, and commit/tag signing is forced off.
func gitEnv(home string) []string {
	return append(os.Environ(),
		"HOME="+home,
		"GIT_AUTHOR_NAME=pi-eval",
		"GIT_AUTHOR_EMAIL=pi-eval@example.invalid",
		"GIT_COMMITTER_NAME=pi-eval",
		"GIT_COMMITTER_EMAIL=pi-eval@example.invalid",
		"GIT_CONFIG_COUNT=3",
		"GIT_CONFIG_KEY_0=commit.gpgsign", "GIT_CONFIG_VALUE_0=false",
		"GIT_CONFIG_KEY_1=tag.gpgsign", "GIT_CONFIG_VALUE_1=false",
		"GIT_CONFIG_KEY_2=init.defaultBranch", "GIT_CONFIG_VALUE_2=main",
	)
}

// SeedWorkspace writes the scenario's fixture files into workDir, creating the
// git history and uncommitted modifications it asks for. home is the isolated
// HOME the run uses, so git never reads the user's global config.
func SeedWorkspace(s Scenario, workDir, home string) error {
	if err := writeFiles(workDir, s.Files); err != nil {
		return err
	}
	if !s.Git && len(s.Modified) == 0 {
		return nil
	}
	env := gitEnv(home)
	for _, args := range [][]string{
		{"init", "-q"},
		{"add", "-A"},
		{"commit", "-q", "-m", "initial fixture commit", "--allow-empty"},
	} {
		if err := runGit(workDir, env, args...); err != nil {
			return err
		}
	}
	return writeFiles(workDir, s.Modified)
}

func writeFiles(root string, files map[string]string) error {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return fmt.Errorf("seed %s: %w", name, err)
		}
		if err := os.WriteFile(path, []byte(files[name]), 0o644); err != nil {
			return fmt.Errorf("seed %s: %w", name, err)
		}
	}
	return nil
}

func runGit(dir string, env []string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git %s: %w\n%s", strings.Join(args, " "), err, out)
	}
	return nil
}

// SeedMemory creates the observation store the run will open (the default
// path under home) and inserts the seeds under a synthetic session, so the
// mem-* tools have something to return.
func SeedMemory(ctx context.Context, home, project string, seeds []MemorySeed) error {
	if len(seeds) == 0 {
		return nil
	}
	dbPath := filepath.Join(home, ".pi-go", "memory", "claude-mem.db")
	db, err := memory.OpenDB(dbPath)
	if err != nil {
		return fmt.Errorf("seed memory: %w", err)
	}
	store := memory.NewSQLiteStore(db)
	defer func() { _ = store.Close() }()

	now := time.Now().Add(-time.Hour)
	sess := &memory.Session{
		SessionID:  "eval-seed-session",
		Project:    project,
		UserPrompt: "eval seed",
		StartedAt:  now,
		Status:     "completed",
	}
	if err := store.CreateSession(ctx, sess); err != nil {
		return fmt.Errorf("seed memory session: %w", err)
	}
	for i, seed := range seeds {
		typ := memory.ObservationType(seed.Type)
		if typ == "" {
			typ = memory.TypeDiscovery
		}
		obs := &memory.Observation{
			SessionID:    sess.SessionID,
			Project:      project,
			Title:        seed.Title,
			Type:         typ,
			Text:         seed.Text,
			ToolName:     "eval",
			PromptNumber: 1,
			CreatedAt:    now.Add(time.Duration(i) * time.Minute),
		}
		if err := store.InsertObservation(ctx, obs); err != nil {
			return fmt.Errorf("seed memory observation %q: %w", seed.Title, err)
		}
	}
	return nil
}

// ScenarioConfig derives the config.json a scenario's run is seeded with:
// the eval model on every role, the given thinking level ("none" when empty —
// cheapest, and the only level every model accepts), memory off unless the
// scenario seeds it, llms sources when it asks for them, and no hooks, MCP
// servers or A2A agents.
func ScenarioConfig(s Scenario, model, thinking string) config.Config {
	cfg := config.Defaults()
	cfg.ThinkingLevel = thinking
	if cfg.ThinkingLevel == "" {
		cfg.ThinkingLevel = "none"
	}
	if model != "" {
		for _, role := range []string{"default", "smol", "slow", "plan"} {
			rc := cfg.Roles[role]
			rc.Model = model
			cfg.Roles[role] = rc
		}
	}
	cfg.Memory = &config.MemoryConfig{Enabled: boolPtr(len(s.Memory) > 0)}
	cfg.Palace = &config.PalaceConfig{Enabled: boolPtr(false)}
	cfg.Hooks = nil
	cfg.MCP = nil
	cfg.A2A = nil
	if len(s.LLMS) > 0 {
		cfg.LLMS = &config.LLMSConfig{Sources: s.LLMS}
	}
	return cfg
}

func boolPtr(b bool) *bool { return &b }
