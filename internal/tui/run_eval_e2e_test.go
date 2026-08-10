//go:build e2e

// TestEvalRun drives a real /run end-to-end against the current repository in
// a temp worktree and measures what actually happened: the ATIF trajectories,
// the orchestrator's subagent concurrency over time, and per-tool efficiency.
// It is a manually-run harness — see internal/eval/eval.md (make eval-run).
//
// It lives in package tui (not eval) because it drives the unexported /run
// handlers; internal/eval holds the pure metric/report logic.
package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/eval"
	"github.com/dimetron/pi-go/internal/subagent"

	tea "charm.land/bubbletea/v2"
)

const evalSpecName = "eval-orchestrator"

func TestEvalRun(t *testing.T) {
	if os.Getenv("PI_EVAL_RUN") != "1" {
		t.Skip("eval harness: set PI_EVAL_RUN=1 to run (make eval-run)")
	}

	start := time.Now()

	// --- inputs -------------------------------------------------------------
	repoRoot := findRepoRoot(t)
	specDir := filepath.Join(repoRoot, "specs", evalSpecName)
	if _, err := os.Stat(filepath.Join(specDir, "PROMPT.md")); err != nil {
		t.Fatalf("eval spec missing at %s (commit the spec before running): %v", specDir, err)
	}
	bin := resolvePiBinary(t, repoRoot)

	// --- isolate sessions + seed config for nested workers ------------------
	home := t.TempDir()
	t.Setenv("HOME", home)
	if cc := os.Getenv("PI_EVAL_CONCURRENCY"); cc != "" {
		t.Setenv("PI_SUBAGENT_CONCURRENCY", cc)
	}

	evalModel := os.Getenv("PI_EVAL_MODEL")
	cfg := config.Defaults()
	if evalModel != "" {
		def := cfg.Roles["default"]
		def.Model = evalModel
		cfg.Roles["default"] = def
	}
	writeHomeConfig(t, home, &cfg)

	// --- temp worktree of the current repo (primary checkout untouched) -----
	wtPath, cleanupWT := addEvalWorktree(t, repoRoot)
	defer cleanupWT()

	// --- orchestrator over the eval worktree --------------------------------
	agentConfigs, err := subagent.LoadBundledAgents()
	if err != nil {
		t.Fatalf("load bundled agents: %v", err)
	}
	orch := subagent.NewOrchestrator(&cfg, wtPath, agentConfigs)
	orch.SetPiBinary(bin)
	defer orch.Shutdown()

	// --- concurrency sampler ------------------------------------------------
	var (
		mu      sync.Mutex
		samples []eval.ConcurrencySample
	)
	stopSamp := make(chan struct{})
	var sampWG sync.WaitGroup
	sampWG.Add(1)
	go func() {
		defer sampWG.Done()
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stopSamp:
				return
			case <-ticker.C:
				agents := orch.List()
				running := 0
				ids := make([]string, 0, len(agents))
				for _, a := range agents {
					ids = append(ids, a.AgentID)
					if a.Status == "running" {
						running++
					}
				}
				mu.Lock()
				samples = append(samples, eval.ConcurrencySample{
					Timestamp: time.Now(),
					Running:   running,
					AgentIDs:  ids,
				})
				mu.Unlock()
			}
		}
	}()

	// --- drive /run ---------------------------------------------------------
	args := []string{evalSpecName}
	mode := "single"
	if os.Getenv("PI_EVAL_PARALLEL") == "1" {
		args = append(args, "--parallel")
		mode = "parallel"
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := &model{
		ctx: ctx,
		cfg: Config{
			WorkDir:      wtPath,
			Orchestrator: orch,
		},
		chatModel: ChatModel{Messages: make([]message, 0)},
	}

	timeout := 30 * time.Minute
	if v := os.Getenv("PI_EVAL_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			timeout = d
		}
	}

	resultCh := make(chan *model, 1)
	go func() {
		resultCh <- runEvalPump(m, args)
	}()

	var final *model
	select {
	case final = <-resultCh:
	case <-time.After(timeout):
		cancel()
		orch.Shutdown()
		final = m
		if final.run == nil {
			final.run = &runState{}
		}
		final.run.phase = "timeout"
	}

	close(stopSamp)
	sampWG.Wait()

	// --- metrics ------------------------------------------------------------
	sessionsDir := filepath.Join(home, ".pi-go", "sessions")
	loaded, loadErr := eval.LoadTrajectories(sessionsDir)
	if loadErr != nil {
		t.Logf("load trajectories: %v", loadErr)
	}
	traj := eval.ComputeTrajectoryMetrics(loaded)
	mu.Lock()
	conc := eval.ComputeConcurrencyMetrics(orch.Concurrency(), samples)
	mu.Unlock()
	tools := eval.ComputeToolsMetrics(loaded)

	// --- golden + baseline checks -------------------------------------------
	goldenFiles := []string{"go.mod", "add.go", "add_test.go"}
	producedDir := filepath.Join(wtPath, "specs", evalSpecName, "artifacts")
	goldenDir := filepath.Join(repoRoot, "specs", evalSpecName, "golden")
	goldenCheck, goldenPass := eval.DiffGolden(producedDir, goldenDir, goldenFiles)

	var baselineCheck []eval.GoldenFile
	baselinePass := true
	if ref := os.Getenv("PI_EVAL_BASELINE"); ref != "" {
		baselineCheck, baselinePass = diffBaseline(t, repoRoot, ref, producedDir, goldenFiles)
	}

	// --- save golden (tag + branch) ----------------------------------------
	if os.Getenv("PI_EVAL_SAVE_GOLDEN") == "1" && goldenPass && runPhase(final) == "done" {
		saveGoldenRefs(t, wtPath, final)
	}

	// --- report -------------------------------------------------------------
	finalPhase, retries, gateResults := runOutcome(final)
	report := &eval.RunReport{
		Metadata: eval.ReportMetadata{
			Spec:      evalSpecName,
			Mode:      mode,
			Model:     modelName(&cfg, evalModel),
			Binary:    bin,
			GitHead:   gitHead(t, repoRoot),
			Timestamp: time.Now(),
			Duration:  time.Since(start).Round(time.Second).String(),
		},
		Outcome: eval.RunOutcome{
			FinalPhase:    finalPhase,
			Retries:       retries,
			GateResults:   gateResults,
			GoldenCheck:   goldenCheck,
			GoldenPass:    goldenPass,
			BaselineCheck: baselineCheck,
			BaselinePass:  baselinePass,
		},
		Trajectory:  traj,
		Concurrency: conc,
		Tools:       tools,
	}

	outDir := os.Getenv("PI_EVAL_OUT")
	if outDir == "" {
		outDir = filepath.Join(repoRoot, "eval-reports")
	}
	jsonPath, mdPath, md, err := eval.WriteReport(report, outDir)
	if err != nil {
		t.Fatalf("write report: %v", err)
	}

	fmt.Println(md)
	fmt.Printf("Report: %s\nJSON:   %s\n", mdPath, jsonPath)

	// --- sanity assertions (outcome reported, not asserted) ------------------
	if len(loaded) == 0 {
		t.Errorf("no trajectories parsed under %s", sessionsDir)
	}
	if traj.TotalToolCalls == 0 {
		t.Errorf("no tool calls recorded across %d session(s)", len(loaded))
	}
	if conc.PoolBudget < 1 {
		t.Errorf("pool budget = %d, want >= 1", conc.PoolBudget)
	}
	if len(goldenCheck) == 0 {
		t.Errorf("golden check result not present")
	}
	if data, err := os.ReadFile(jsonPath); err != nil || !json.Valid(data) {
		t.Errorf("report JSON invalid or unreadable: %v", err)
	}
}

// runEvalPump drives the /run handler chain to completion, mirroring what the
// bubbletea Update loop does for the run messages.
func runEvalPump(m *model, args []string) *model {
	var cmd tea.Cmd
	var cur tea.Model
	cur, cmd = m.handleRunCommand(args)
	for cmd != nil {
		msg := cmd()
		switch v := msg.(type) {
		case runAgentEventMsg:
			cur, cmd = m.handleRunAgentEvent(v)
		case runAgentDoneMsg:
			cur, cmd = m.handleRunAgentDone(v)
		case runGateResultMsg:
			cur, cmd = m.handleRunGateResult(v)
		case runMergeResultMsg:
			cur, cmd = m.handleRunMergeResult(v)
		default:
			cur, cmd = m, nil
		}
		m = cur.(*model)
	}
	return m
}

// --- helpers ----------------------------------------------------------------

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
	for _, cand := range []string{
		filepath.Join(repoRoot, "pi"),
		filepath.Join(gopathBin(), "pi"),
		"pi",
	} {
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

func writeHomeConfig(t *testing.T, home string, cfg *config.Config) {
	t.Helper()
	dir := filepath.Join(home, ".pi-go")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// addEvalWorktree creates a temp worktree of repoRoot at HEAD and returns its
// path plus a cleanup func. Fails the test only if the add fails for a real
// reason; a dirty primary checkout is reported as a skip.
func addEvalWorktree(t *testing.T, repoRoot string) (string, func()) {
	t.Helper()
	wtPath := filepath.Join(t.TempDir(), "eval-wt")
	branch := fmt.Sprintf("eval-run-%d", time.Now().UnixNano())
	cmd := exec.Command("git", "worktree", "add", "-b", branch, wtPath, "HEAD")
	cmd.Dir = repoRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("git worktree add failed (is the primary checkout dirty?): %v\n%s", err, out)
	}
	cleanup := func() {
		_ = exec.Command("git", "-C", repoRoot, "worktree", "remove", "--force", wtPath).Run()
		_ = exec.Command("git", "-C", repoRoot, "worktree", "prune").Run()
		_ = exec.Command("git", "-C", repoRoot, "branch", "-D", branch).Run()
	}
	return wtPath, cleanup
}

func diffBaseline(t *testing.T, repoRoot, ref, producedDir string, files []string) ([]eval.GoldenFile, bool) {
	t.Helper()
	baselineDir := t.TempDir()
	for _, name := range files {
		path := fmt.Sprintf("%s:specs/%s/artifacts/%s", ref, evalSpecName, name)
		out, err := exec.Command("git", "-C", repoRoot, "show", path).Output()
		if err != nil {
			t.Logf("baseline %s unreadable: %v", path, err)
			return nil, false
		}
		if err := os.WriteFile(filepath.Join(baselineDir, name), out, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return eval.DiffGolden(producedDir, baselineDir, files)
}

func saveGoldenRefs(t *testing.T, wtPath string, m *model) {
	t.Helper()
	ts := time.Now().Format("20060102-150405")
	if out, err := exec.Command("git", "-C", wtPath, "tag", "-f", "eval/golden-"+ts).CombinedOutput(); err != nil {
		t.Logf("save golden tag failed: %v\n%s", err, out)
	}
	if out, err := exec.Command("git", "-C", wtPath, "branch", "-f", "eval/golden").CombinedOutput(); err != nil {
		t.Logf("save golden branch failed: %v\n%s", err, out)
	}
	t.Logf("saved golden refs eval/golden-%s and eval/golden at %s", ts, gitHead(t, wtPath))
}

func runPhase(m *model) string {
	if m == nil || m.run == nil {
		return ""
	}
	return m.run.phase
}

// runOutcome extracts the final phase, retry count and gate results from the
// run state, so the report reflects the run even when it did not finish.
func runOutcome(m *model) (phase string, retries int, gates []eval.GateResult) {
	phase = "not_started"
	if m == nil || m.run == nil {
		return
	}
	phase = m.run.phase
	retries = m.run.retries
	for _, g := range m.run.gateResults {
		out := g.Output
		if len(out) > 2000 {
			out = out[:2000] + "...(truncated)"
		}
		gates = append(gates, eval.GateResult{Name: g.Name, Command: g.Command, Passed: g.Passed, Output: out})
	}
	return
}

func modelName(cfg *config.Config, envModel string) string {
	if envModel != "" {
		return envModel
	}
	if cfg != nil {
		if r, ok := cfg.Roles["default"]; ok {
			return r.Model
		}
	}
	return cfg.DefaultModel
}

func gitHead(t *testing.T, repoRoot string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", repoRoot, "rev-parse", "--short", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
