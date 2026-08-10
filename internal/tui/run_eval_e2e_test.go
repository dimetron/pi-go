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
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/eval"
	"github.com/dimetron/pi-go/internal/provider"
	"github.com/dimetron/pi-go/internal/subagent"

	tea "charm.land/bubbletea/v2"
	llmmodel "google.golang.org/adk/v2/model"
	"google.golang.org/genai"
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

	// The run worker's worktree is named after the spec (runWorktreeName), so
	// every run of this spec reuses branch "eval-orchestrator". A run that was
	// killed, timed out or crashed exits before the merge flow cleans that
	// worktree up, leaving the branch registered in the shared .git at an
	// orphaned path — which makes WorktreeManager.Create refuse the next run.
	// Remove any such leftover before creating a fresh worktree.
	cleanupStaleRunWorktree(t, repoRoot)

	// --- isolate sessions + seed config for nested workers ------------------
	// Not t.TempDir(): the run's `go test` populates $HOME/go/pkg/mod, and the
	// module cache is written read-only. TempDir's cleanup cannot unlink those
	// files and fails the test *after* a successful measurement — a harness
	// that reports FAIL for a run it measured fine is worse than one that
	// leaves a temp dir behind. Clean it up permissively instead.
	home := evalHomeDir(t)
	t.Setenv("HOME", home)
	// Isolating HOME breaks git commit signing: the repo signs commits/tags
	// with the user's real key (GPG via 1Password), which is not reachable
	// from the temp HOME, so the run's `git merge --no-ff` fails at
	// "gpg: No secret key". The eval's commits are throwaway — they live in
	// the temp eval worktree, removed when the test ends — so skip signing
	// for every git call this run makes. Scoped to this process via
	// GIT_CONFIG_*; the shared repo config is untouched.
	t.Setenv("GIT_CONFIG_COUNT", "2")
	t.Setenv("GIT_CONFIG_KEY_0", "commit.gpgsign")
	t.Setenv("GIT_CONFIG_VALUE_0", "false")
	t.Setenv("GIT_CONFIG_KEY_1", "tag.gpgsign")
	t.Setenv("GIT_CONFIG_VALUE_1", "false")
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

	// --- temp worktree at the pinned base (primary checkout untouched) ------
	// Every run starts from the same commit so runs are comparable: a run
	// against a moving HEAD measures the repo's drift as much as /run's
	// behavior. PI_EVAL_BASE names the ref (default: the eval/base tag, or
	// HEAD when it has never been pinned).
	baseRef := resolveBaseRef(t, repoRoot)
	baseCommit := revParse(t, repoRoot, baseRef)
	wtPath, cleanupWT := addEvalWorktree(t, repoRoot, baseRef)
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
	var baselineRef string
	if ref := os.Getenv("PI_EVAL_BASELINE"); ref != "" {
		baselineRef = ref
		baselineCheck, baselinePass = diffBaseline(t, repoRoot, ref, producedDir, goldenFiles)
	}

	// --- save golden (tag + branch) ----------------------------------------
	if os.Getenv("PI_EVAL_SAVE_GOLDEN") == "1" && goldenPass && runPhase(final) == "done" {
		saveGoldenRefs(t, wtPath, final)
	}

	// --- report -------------------------------------------------------------
	finalPhase, retries, gateResults, failReason := runOutcome(final)
	report := &eval.RunReport{
		Metadata: eval.ReportMetadata{
			Spec:       evalSpecName,
			Mode:       mode,
			Model:      modelName(&cfg, evalModel),
			Binary:     bin,
			GitHead:    gitHead(t, repoRoot),
			BaseRef:    baseRef,
			BaseCommit: baseCommit,
			Timestamp:  time.Now(),
			Duration:   time.Since(start).Round(time.Second).String(),
		},
		Outcome: eval.RunOutcome{
			FinalPhase:    finalPhase,
			Retries:       retries,
			Reason:        failReason,
			GateResults:   gateResults,
			GoldenCheck:   goldenCheck,
			GoldenPass:    goldenPass,
			BaselineRef:   baselineRef,
			BaselineCheck: baselineCheck,
			BaselinePass:  baselinePass,
		},
		Trajectory:  traj,
		Concurrency: conc,
		Tools:       tools,
	}

	// --- LLM judge (advisory, graded from the assembled report) -------------
	// The judge grades the report it is handed, so it runs after the metrics
	// are computed and before the report is written. Its verdict is recorded,
	// never asserted on: a grader is not a stable enough signal to fail a run,
	// but it catches the qualitative regressions counters miss.
	if judgeModel := os.Getenv("PI_EVAL_JUDGE_MODEL"); judgeModel != "" {
		complete := judgeComplete(t, judgeModel)
		digest := eval.TrajectoryDigest(loaded, judgeDigestLimit())
		judgeCtx, judgeCancel := context.WithTimeout(context.Background(), 3*time.Minute)
		verdict := eval.Judge(judgeCtx, complete, judgeModel, report, digest)
		judgeCancel()
		report.Judge = &verdict
		if verdict.Error != "" {
			t.Logf("judge unavailable: %s", verdict.Error)
		}
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

// addEvalWorktree creates a temp worktree of repoRoot at baseRef and returns
// its path plus a cleanup func. Fails the test only if the add fails for a real
// reason; a dirty primary checkout is reported as a skip.
func addEvalWorktree(t *testing.T, repoRoot, baseRef string) (string, func()) {
	t.Helper()
	wtPath := filepath.Join(t.TempDir(), "eval-wt")
	branch := fmt.Sprintf("eval-run-%d", time.Now().UnixNano())
	cmd := exec.Command("git", "worktree", "add", "-b", branch, wtPath, baseRef)
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

// cleanupStaleRunWorktree removes any worktree a previous /run for this eval
// spec left registered in the shared .git. The worker's worktree is named
// after the spec (runWorktreeName), so every run of this spec uses the same
// branch. A run that is killed, times out or crashes exits before the merge
// flow cleans the worktree up, leaving the branch checked out at an orphaned
// path — which makes WorktreeManager.Create refuse the next run with "branch
// is already checked out at ...". Pruning it here keeps re-runs working after
// any interrupted run.
func cleanupStaleRunWorktree(t *testing.T, repoRoot string) {
	t.Helper()
	branch := runWorktreeName(evalSpecName, "")

	out, err := exec.Command("git", "-C", repoRoot, "worktree", "list", "--porcelain").Output()
	if err != nil {
		t.Logf("cleanup stale run worktree: list worktrees: %v", err)
		return
	}
	var wtPath string
	for _, line := range strings.Split(string(out), "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			wtPath = strings.TrimPrefix(line, "worktree ")
		case line == "branch refs/heads/"+branch && wtPath != "":
			if err := exec.Command("git", "-C", repoRoot, "worktree", "remove", "--force", wtPath).Run(); err != nil {
				t.Logf("cleanup stale run worktree: remove %s: %v", wtPath, err)
			} else {
				t.Logf("removed stale run worktree %s left by a previous run", wtPath)
			}
		}
	}
	_ = exec.Command("git", "-C", repoRoot, "worktree", "prune").Run()
	_ = exec.Command("git", "-C", repoRoot, "branch", "-D", branch).Run()
}

// evalHomeDir creates the isolated HOME for the run and registers a cleanup
// that chmods the tree writable before removing it. The run's nested `go test`
// writes a Go module cache under $HOME/go/pkg/mod with read-only files, which
// os.RemoveAll (and t.TempDir's cleanup) cannot unlink. A cleanup failure here
// must not fail an otherwise-good run, so removal errors are logged, not
// fatal.
func evalHomeDir(t *testing.T) string {
	t.Helper()
	home, err := os.MkdirTemp("", "pi-eval-home-")
	if err != nil {
		t.Fatalf("create eval HOME: %v", err)
	}
	t.Cleanup(func() {
		// Restore write permission on the way down; the module cache marks
		// both files and their directories read-only.
		_ = filepath.WalkDir(home, func(path string, d os.DirEntry, err error) error {
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
		if err := os.RemoveAll(home); err != nil {
			t.Logf("eval HOME %s left behind: %v", home, err)
		}
	})
	return home
}

// judgeDigestLimit caps how many tool calls per session reach the judge's
// prompt. A worker that thrashed can emit hundreds; the cap keeps one runaway
// session from crowding the others out of the context window.
func judgeDigestLimit() int {
	if v := os.Getenv("PI_EVAL_JUDGE_MAX_CALLS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return 60
}

// evalBaseRef is the tag naming the pinned starting point every eval run
// branches from. Pinning matters because a run against a moving HEAD measures
// the repository's drift as much as /run's behavior: two runs a week apart are
// only comparable if they started from the same code.
const evalBaseRef = "eval/base"

// resolveBaseRef picks the commit the eval worktree is created from:
// PI_EVAL_BASE when set, else the eval/base tag, else HEAD. With
// PI_EVAL_PIN_BASE=1 the tag is (re)created at HEAD first, which is how the
// baseline is established or deliberately moved.
func resolveBaseRef(t *testing.T, repoRoot string) string {
	t.Helper()

	if os.Getenv("PI_EVAL_PIN_BASE") == "1" {
		if out, err := exec.Command("git", "-C", repoRoot, "tag", "-f", evalBaseRef, "HEAD").CombinedOutput(); err != nil {
			t.Fatalf("pin base ref %s: %v\n%s", evalBaseRef, err, out)
		}
		t.Logf("pinned %s at %s", evalBaseRef, gitHead(t, repoRoot))
	}

	if ref := os.Getenv("PI_EVAL_BASE"); ref != "" {
		if revParse(t, repoRoot, ref) == "" {
			t.Fatalf("PI_EVAL_BASE=%s does not resolve to a commit", ref)
		}
		return ref
	}
	if revParse(t, repoRoot, evalBaseRef) != "" {
		return evalBaseRef
	}
	t.Logf("no %s tag yet — running against HEAD; pin it with PI_EVAL_PIN_BASE=1 to make runs comparable", evalBaseRef)
	return "HEAD"
}

// revParse resolves a ref to a full commit SHA, returning "" when it does not
// resolve.
func revParse(t *testing.T, repoRoot, ref string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", repoRoot, "rev-parse", "--verify", ref+"^{commit}").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// judgeComplete builds the single-shot LLM call the judge uses, resolving the
// judge model the same way the CLI resolves any model. Returns nil when no
// judge is configured or no API key is available for it, so the eval still
// produces its measured report without a grader.
func judgeComplete(t *testing.T, model string) eval.CompleteFunc {
	t.Helper()
	if model == "" {
		return nil
	}
	info, err := provider.Resolve(model)
	if err != nil {
		t.Logf("judge model %q unresolvable: %v", model, err)
		return nil
	}
	apiKey := config.APIKeys()[info.Provider]
	if apiKey == "" && !info.Ollama {
		t.Logf("judge model %q has no API key for provider %q — skipping judge", model, info.Provider)
		return nil
	}

	return func(ctx context.Context, system, user string) (string, error) {
		llm, err := provider.NewLLM(ctx, info, apiKey, "", "none", &provider.LLMOptions{})
		if err != nil {
			return "", fmt.Errorf("create judge llm: %w", err)
		}
		req := &llmmodel.LLMRequest{
			Contents: []*genai.Content{genai.NewContentFromText(user, genai.RoleUser)},
			Config: &genai.GenerateContentConfig{
				SystemInstruction: genai.NewContentFromText(system, genai.RoleUser),
			},
		}
		var reply strings.Builder
		for resp, err := range llm.GenerateContent(ctx, req, false) {
			if err != nil {
				return "", fmt.Errorf("judge llm: %w", err)
			}
			if resp.Content == nil {
				continue
			}
			for _, part := range resp.Content.Parts {
				if part.Text != "" && !part.Thought {
					reply.WriteString(part.Text)
				}
			}
		}
		if strings.TrimSpace(reply.String()) == "" {
			return "", fmt.Errorf("judge returned an empty reply")
		}
		return reply.String(), nil
	}
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

// runOutcome extracts the final phase, retry count, gate results and failure
// reason from the run state, so the report reflects the run even when it did
// not finish.
func runOutcome(m *model) (phase string, retries int, gates []eval.GateResult, reason string) {
	phase = "not_started"
	reason = runFailureReason(m)
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

// runFailureReason returns the terminal failure message from the run's chat
// log. Every failure path in the /run flow appends a distinctive assistant
// message ("**Merge failed**…", "**Verification failed**…", "Failed to spawn
// task agent: …") before stopping, so the last message matching one of those
// markers is the definitive cause. Returns "" when the run did not fail.
func runFailureReason(m *model) string {
	if m == nil {
		return ""
	}
	markers := []string{
		"**Merge failed**",
		"**Verification failed**",
		"**Gate validation failed**",
		"**Subagent exited with non-zero status**",
		"Failed to spawn retry agent",
		"Failed to spawn task agent",
		"Failed to spawn agent 1",
		"Failed to spawn agent 2",
		"Failed to create run backup branch for agent 1",
		"Failed to create run backup branch for agent 2",
	}
	for i := len(m.chatModel.Messages) - 1; i >= 0; i-- {
		msg := m.chatModel.Messages[i]
		if msg.role != "assistant" {
			continue
		}
		for _, marker := range markers {
			if !strings.Contains(msg.content, marker) {
				continue
			}
			reason := msg.content
			if len(reason) > 2000 {
				reason = reason[:2000] + "...(truncated)"
			}
			return reason
		}
	}
	return ""
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
