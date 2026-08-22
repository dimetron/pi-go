//go:build e2e

// TestEvalTools runs the tool-coverage suite against a real pi binary: every
// scenario gets an isolated HOME and a seeded workspace, runs headlessly
// (`pi --mode print "<prompt>"`), and is graded from the trajectories it wrote
// plus the filesystem it left behind. The coverage matrix over the registered
// tool inventory and a JSON+Markdown report land in eval-reports/.
//
// Manually run — needs a built binary and an LLM API key. See
// internal/eval/eval.md ("Tool-coverage eval") and README.md here.
package scenarios

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/eval"
	"github.com/dimetron/pi-go/internal/lsp"
)

func TestEvalTools(t *testing.T) {
	if os.Getenv("PI_EVAL_TOOLS") != "1" {
		t.Skip("tool-coverage eval: set PI_EVAL_TOOLS=1 to run (make eval-tools)")
	}
	start := time.Now()

	repoRoot := findRepoRoot(t)
	bin := resolvePiBinary(t, repoRoot)
	model := os.Getenv("PI_EVAL_MODEL")
	if model == "" {
		model = config.Defaults().Roles["default"].Model
	}
	inv, err := eval.Inventory(t.TempDir())
	if err != nil {
		t.Fatalf("tool inventory: %v", err)
	}

	selected := os.Getenv("PI_EVAL_SCENARIO")
	suite := selectScenarios(Suite(), selected)
	if len(suite) == 0 {
		t.Fatalf("PI_EVAL_SCENARIO=%q matches no scenario (have: %s)", selected, strings.Join(scenarioNames(Suite()), ", "))
	}
	caps := hostCapabilities()
	defaultTimeout := scenarioTimeout()
	strict := os.Getenv("PI_EVAL_STRICT") == "1"
	serial := os.Getenv("PI_EVAL_SERIAL") == "1"

	var (
		mu       sync.Mutex
		results  = make(map[string]eval.ScenarioResult, len(suite))
		perScen  = make(map[string][]*eval.LoadedTrajectory, len(suite))
		allTrajs []*eval.LoadedTrajectory
	)
	// Scenario scratch dirs are cleaned up by the top-level test, not the
	// subtest: the aggregate step below still reads each session's
	// events.jsonl (token usage) after the subtests have finished.
	root := t
	t.Run("scenario", func(t *testing.T) {
		for _, s := range suite {
			t.Run(s.Name, func(t *testing.T) {
				if !serial {
					t.Parallel()
				}
				res, loaded := runScenario(t, root, bin, model, s, caps, defaultTimeout)
				mu.Lock()
				results[s.Name] = res
				perScen[s.Name] = loaded
				allTrajs = append(allTrajs, loaded...)
				mu.Unlock()
				reportScenario(t, res, strict)
			})
		}
	})

	// --- aggregate ----------------------------------------------------------
	ordered := make([]eval.ScenarioResult, 0, len(suite))
	for _, s := range suite {
		if r, ok := results[s.Name]; ok {
			ordered = append(ordered, r)
		}
	}
	report := &eval.ToolsReport{
		Metadata: eval.ToolsReportMetadata{
			Model:     model,
			Binary:    bin,
			GitHead:   gitHead(repoRoot),
			Timestamp: time.Now(),
			Duration:  time.Since(start).Round(time.Second).String(),
			Selected:  selected,
		},
		Scenarios: ordered,
		Coverage:  eval.ComputeCoverage(inv, Suite(), Exclusions, allTrajs),
		Tools:     eval.ComputeToolsMetrics(allTrajs),
		Tokens:    eval.ComputeTokenMetrics(allTrajs),
	}
	report.Tally()

	// --- LLM judge (advisory) -------------------------------------------------
	if judgeModel := os.Getenv("PI_EVAL_JUDGE_MODEL"); judgeModel != "" {
		complete, reason := eval.ProviderComplete(judgeModel)
		if complete == nil {
			t.Logf("judge unavailable: %s", reason)
		}
		digest := suiteDigest(suite, perScen, judgeDigestLimit())
		judgeCtx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		verdict := eval.JudgeTools(judgeCtx, complete, judgeModel, report, digest)
		cancel()
		report.Judge = &verdict
		if verdict.Error != "" {
			t.Logf("judge unavailable: %s", verdict.Error)
		}
	}

	outDir := os.Getenv("PI_EVAL_OUT")
	if outDir == "" {
		outDir = filepath.Join(repoRoot, "eval-reports")
	}
	jsonPath, mdPath, md, err := eval.WriteToolsReport(report, outDir)
	if err != nil {
		t.Fatalf("write report: %v", err)
	}
	fmt.Println(md)
	fmt.Printf("Report: %s\nJSON:   %s\n", mdPath, jsonPath)

	// --- sanity assertions (scenario outcomes are reported, not asserted,
	// unless PI_EVAL_STRICT=1) --------------------------------------------------
	if report.Metadata.Passed+report.Metadata.Failed+report.Metadata.Errored == 0 {
		t.Skip("every selected scenario was skipped (unmet requirements)")
	}
	if len(allTrajs) == 0 {
		t.Errorf("no trajectories parsed from any scenario")
	}
	if data, err := os.ReadFile(jsonPath); err != nil || !json.Valid(data) {
		t.Errorf("report JSON invalid or unreadable: %v", err)
	}
	if strict && len(report.Coverage.Gap) > 0 && selected == "" {
		t.Errorf("coverage gap: %s", strings.Join(report.Coverage.Gap, ", "))
	}
}

// reportScenario surfaces one scenario's outcome in the test log, failing the
// subtest only in strict mode.
func reportScenario(t *testing.T, res eval.ScenarioResult, strict bool) {
	t.Helper()
	switch res.Status {
	case eval.StatusSkip:
		t.Skip(res.Reason)
	case eval.StatusPass:
		t.Logf("PASS (%s, %d session(s), %d tool call(s))", res.Duration, res.Sessions, res.ToolCalls)
	default:
		msg := fmt.Sprintf("%s: %s", strings.ToUpper(res.Status), res.Reason)
		if res.Output != "" {
			msg += "\n--- pi output (tail) ---\n" + res.Output
		}
		if strict {
			t.Error(msg)
		} else {
			t.Log(msg)
		}
	}
}

// runScenario executes one scenario end to end and grades it. Scratch
// directories are registered for cleanup on root (the top-level test) so they
// outlive the subtest — the aggregate step reads them after it returns.
func runScenario(t, root *testing.T, bin, model string, s eval.Scenario, caps map[string]bool, defaultTimeout time.Duration) (eval.ScenarioResult, []*eval.LoadedTrajectory) {
	t.Helper()
	if missing := unmetRequirements(s, caps); missing != "" {
		return eval.ScenarioResult{Name: s.Name, Description: s.Description, Status: eval.StatusSkip, Reason: missing}, nil
	}
	started := time.Now()

	home := tempDir(root, "pi-eval-tools-home-")
	workDir := tempDir(root, "pi-eval-tools-ws-")
	ctx := context.Background()

	if err := eval.SeedWorkspace(s, workDir, home); err != nil {
		return errorResult(s, started, "seed workspace: "+err.Error()), nil
	}
	if err := eval.SeedMemory(ctx, home, workDir, s.Memory); err != nil {
		return errorResult(s, started, "seed memory: "+err.Error()), nil
	}
	cfg := eval.ScenarioConfig(s, model, os.Getenv("PI_EVAL_THINKING"))
	if err := writeHomeConfig(home, &cfg); err != nil {
		return errorResult(s, started, "seed config: "+err.Error()), nil
	}

	timeout := defaultTimeout
	if s.Timeout > timeout {
		timeout = s.Timeout
	}
	exitCode, output, runErr := runPi(ctx, bin, workDir, home, s, timeout)

	sessionsDir := filepath.Join(home, ".pi-go", "sessions")
	loaded, loadErr := eval.LoadTrajectories(sessionsDir)
	if loadErr != nil {
		t.Logf("%s: load trajectories: %v", s.Name, loadErr)
	}

	res := eval.EvaluateScenario(s, workDir, loaded)
	res.Duration = time.Since(started).Round(time.Second).String()
	res.ExitCode = exitCode
	res.Output = tail(output, 4000)
	// A process that timed out or exited non-zero is an error even when the
	// partial trajectory happens to satisfy every check: the grading still
	// shows per tool/check what was reached, but the scenario must not be
	// tallied as a pass.
	if runErr != nil {
		res.Status = eval.StatusError
		res.Reason = strings.TrimSuffix("pi: "+runErr.Error()+"; "+res.Reason, "; ")
	}
	return res, loaded
}

func errorResult(s eval.Scenario, started time.Time, reason string) eval.ScenarioResult {
	return eval.ScenarioResult{
		Name:        s.Name,
		Description: s.Description,
		Status:      eval.StatusError,
		Reason:      reason,
		Duration:    time.Since(started).Round(time.Second).String(),
		ExitCode:    -1,
	}
}

// runPi runs `pi --mode print [args] "<prompt>"` in workDir with HOME pointed
// at the scenario's isolated home. Returns the exit code (-1 when the process
// did not exit on its own), the combined output, and an error describing a
// non-zero exit or timeout.
func runPi(ctx context.Context, bin, workDir, home string, s eval.Scenario, timeout time.Duration) (int, string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	args := append([]string{"--mode", "print"}, s.Args...)
	args = append(args, s.Prompt)
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = workDir
	cmd.Env = piEnv(home)
	cmd.WaitDelay = 10 * time.Second
	out, err := cmd.CombinedOutput()

	switch {
	case ctx.Err() != nil:
		return -1, string(out), fmt.Errorf("timed out after %s", timeout)
	case err != nil:
		code := -1
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		}
		return code, string(out), fmt.Errorf("exit %d: %v", code, err)
	default:
		return 0, string(out), nil
	}
}

// piEnv is the environment a scenario's pi process runs with: the caller's
// environment (API keys, PATH, go toolchain) with the home directory
// redirected to the isolated home (HOME, plus USERPROFILE on Windows — see
// eval.HomeEnv), git signing forced off for anything the agent commits, and
// the sandbox-root override dropped so the workspace is the sandbox.
func piEnv(home string) []string {
	var env []string
	for _, kv := range os.Environ() {
		key, _, _ := strings.Cut(kv, "=")
		switch key {
		case "HOME", "USERPROFILE", "PI_SANDBOX_ROOT", "GIT_CONFIG_COUNT":
			continue
		}
		if strings.HasPrefix(key, "GIT_CONFIG_KEY_") || strings.HasPrefix(key, "GIT_CONFIG_VALUE_") {
			continue
		}
		env = append(env, kv)
	}
	env = append(env, eval.HomeEnv(home)...)
	return append(env,
		"GIT_AUTHOR_NAME=pi-eval", "GIT_AUTHOR_EMAIL=pi-eval@example.invalid",
		"GIT_COMMITTER_NAME=pi-eval", "GIT_COMMITTER_EMAIL=pi-eval@example.invalid",
		"GIT_CONFIG_COUNT=2",
		"GIT_CONFIG_KEY_0=commit.gpgsign", "GIT_CONFIG_VALUE_0=false",
		"GIT_CONFIG_KEY_1=tag.gpgsign", "GIT_CONFIG_VALUE_1=false",
	)
}

// --- requirements -------------------------------------------------------------

// hostCapabilities probes what this host can offer the scenarios that declare
// Requires. Vision is assumed unless opted out (it cannot be probed without a
// model call); LSP is probed for real; network is opted out with
// PI_EVAL_OFFLINE=1 and otherwise probed per scenario against the URLs it
// needs (see unmetRequirements), because a host can reach the LLM provider and
// still be firewalled off from arbitrary documentation hosts.
func hostCapabilities() map[string]bool {
	return map[string]bool{
		"lsp":     lsp.NewManager(nil).AnyAvailable(),
		"network": os.Getenv("PI_EVAL_OFFLINE") != "1",
		"vision":  os.Getenv("PI_EVAL_NO_VISION") != "1",
	}
}

func unmetRequirements(s eval.Scenario, caps map[string]bool) string {
	var missing []string
	for _, r := range s.Requires {
		if !caps[r] {
			missing = append(missing, r)
			continue
		}
		if r == "network" {
			if reason := probeNetwork(s); reason != "" {
				return "requires network: " + reason
			}
		}
	}
	if len(missing) == 0 {
		return ""
	}
	return "requires " + strings.Join(missing, ", ") + " (unavailable on this host)"
}

// probeNetwork fetches each llms source URL the scenario configures and
// returns a reason when any is unreachable from this host, so a firewalled
// environment records a skip with the cause rather than a tool failure.
// Results are cached per URL across scenarios.
func probeNetwork(s eval.Scenario) string {
	for _, src := range s.LLMS {
		if reason := probeURL(src.URL); reason != "" {
			return reason
		}
	}
	return ""
}

var (
	probeMu    sync.Mutex
	probeCache = map[string]string{}
)

func probeURL(url string) string {
	probeMu.Lock()
	defer probeMu.Unlock()
	if reason, ok := probeCache[url]; ok {
		return reason
	}
	client := &http.Client{Timeout: 15 * time.Second}
	reason := ""
	resp, err := client.Get(url)
	switch {
	case err != nil:
		reason = fmt.Sprintf("GET %s: %v", url, err)
	case resp.StatusCode >= 400:
		reason = fmt.Sprintf("GET %s: HTTP %d", url, resp.StatusCode)
	}
	if resp != nil {
		_ = resp.Body.Close()
	}
	probeCache[url] = reason
	return reason
}

// --- selection ------------------------------------------------------------------

func selectScenarios(all []eval.Scenario, filter string) []eval.Scenario {
	if strings.TrimSpace(filter) == "" {
		return all
	}
	want := make(map[string]bool)
	for _, n := range strings.Split(filter, ",") {
		want[strings.TrimSpace(n)] = true
	}
	var out []eval.Scenario
	for _, s := range all {
		if want[s.Name] {
			out = append(out, s)
		}
	}
	return out
}

func scenarioNames(all []eval.Scenario) []string {
	names := make([]string, 0, len(all))
	for _, s := range all {
		names = append(names, s.Name)
	}
	return names
}

// suiteDigest concatenates the per-scenario tool-call timelines for the judge,
// in suite order and labelled by scenario.
func suiteDigest(suite []eval.Scenario, perScen map[string][]*eval.LoadedTrajectory, maxCalls int) string {
	var b strings.Builder
	for _, s := range suite {
		loaded := perScen[s.Name]
		if len(loaded) == 0 {
			continue
		}
		fmt.Fprintf(&b, "## scenario %s\n\n", s.Name)
		b.WriteString(eval.TrajectoryDigest(loaded, maxCalls))
	}
	return b.String()
}

// --- environment helpers ---------------------------------------------------------

func findRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find repository root (no .git)")
		}
		dir = parent
	}
}

func resolvePiBinary(t *testing.T, repoRoot string) string {
	t.Helper()
	if p := os.Getenv("PI_BINARY"); p != "" {
		if !filepath.IsAbs(p) {
			p = filepath.Join(repoRoot, p)
		}
		if _, err := os.Stat(p); err == nil {
			return p
		}
		t.Skipf("PI_BINARY=%s not found", p)
	}
	for _, cand := range []string{filepath.Join(repoRoot, "pi"), filepath.Join(gopathBin(), "pi"), "pi"} {
		if p, err := exec.LookPath(cand); err == nil {
			return p
		}
	}
	t.Skip("no pi binary found: set PI_BINARY, run make build, or make install")
	return ""
}

func gopathBin() string {
	if gp := os.Getenv("GOPATH"); gp != "" {
		return filepath.Join(gp, "bin")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "go", "bin")
}

func writeHomeConfig(home string, cfg *config.Config) error {
	dir := filepath.Join(home, ".pi-go")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "config.json"), data, 0o644)
}

// tempDir creates a scratch directory with permissive cleanup: the run may
// leave read-only files behind (a Go module cache under $HOME/go, gopls
// caches), which os.RemoveAll cannot unlink without a chmod pass first. A
// cleanup failure is logged, never fatal — a harness that fails a run it
// measured fine is worse than one that leaves a temp dir behind. t.Cleanup is
// safe to call from a parallel subtest on the parent: it only appends.
func tempDir(t *testing.T, prefix string) string {
	t.Helper()
	dir, err := os.MkdirTemp("", prefix)
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	t.Cleanup(func() {
		_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			mode := os.FileMode(0o600)
			if d.IsDir() {
				mode = 0o700
			}
			_ = os.Chmod(path, mode)
			return nil
		})
		if err := os.RemoveAll(dir); err != nil {
			t.Logf("temp dir %s left behind: %v", dir, err)
		}
	})
	return dir
}

func scenarioTimeout() time.Duration {
	if v := os.Getenv("PI_EVAL_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return 5 * time.Minute
}

func judgeDigestLimit() int {
	if v := os.Getenv("PI_EVAL_JUDGE_MAX_CALLS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return 40
}

func gitHead(repoRoot string) string {
	out, err := exec.Command("git", "-C", repoRoot, "rev-parse", "--short", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}
