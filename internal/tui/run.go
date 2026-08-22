package tui

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/dimetron/pi-go/internal/procs"
	"github.com/dimetron/pi-go/internal/subagent"

	tea "charm.land/bubbletea/v2"
)

// ChecklistStep represents a single step from the plan.md checklist.
type ChecklistStep struct {
	Title string
	Done  bool
}

// parallelAgent tracks a single agent within a parallel /run.
type parallelAgent struct {
	agentID string
	events  <-chan subagent.Event
	slices  []int  // which checklist indices this agent handles
	done    bool   // true when events channel has closed
	status  string // authoritative terminal status from run_done
}

// runState tracks the state of a /run command execution.
type runState struct {
	specName string
	promptMD string
	gates    []Gate
	agentID  string // the subagent currently streaming

	// worktreeAgentID is the agent that OWNS the worktree the run lives in.
	// It is not the same as agentID once a retry has happened: a retry agent
	// is spawned with WorkDir set to the existing worktree and never creates
	// one of its own, so gates, checklist refresh and the merge must all keep
	// asking the original owner. Looking them up by agentID after a retry
	// finds no worktree at all.
	worktreeAgentID string

	status      string // authoritative terminal status from the subagent
	phase       string // "running", "gating", "verifying", "retrying", "merging", "done", "failed"
	retries     int
	maxRetries  int
	events      <-chan subagent.Event // subagent event channel (single-agent mode)
	gateOutput  string                // formatted gate failure output (for retry prompts)
	gateResults []GateResult          // latest gate results (for summary report)
	startTime   time.Time             // when the run started
	checklist   []ChecklistStep       // parsed from plan.md
	parallel    []*parallelAgent      // parallel agents (nil for single-agent mode)

	// carried holds agents whose worktrees still hold unmerged work after the
	// run collapsed from parallel to a single resuming coordinator. Without
	// it, retrying a parallel run silently drops every worktree but the one
	// the retry resumed in.
	carried []mergeTarget

	// ownerBackup is the backup branch the worktree owner was given at spawn
	// time. It is recorded on collapse because the owner keeps its parallel
	// name: git cannot hold both "run/spec" and "run/spec/part-2" — one is a
	// ref, the other wants it to be a directory — so a collapsed run must not
	// fall back to the bare single-agent name while carrying part-N siblings.
	ownerBackup string
}

// mergeTarget names a worktree to merge and the backup branch to move onto it.
type mergeTarget struct {
	agentID string
	backup  string
}

// isParallel returns true if this run uses multiple parallel agents.
func (rs *runState) isParallel() bool {
	return len(rs.parallel) > 0
}

// allAgentsDone returns true when all parallel agents have finished.
func (rs *runState) allAgentsDone() bool {
	for _, pa := range rs.parallel {
		if !pa.done {
			return false
		}
	}
	return true
}

// collapseParallel ends parallel fan-in, moving every worktree that is not the
// run's own into carried so the merge still takes it. Dropping those worktrees
// is silent data loss: the second agent's slices live there and nowhere else.
func (rs *runState) collapseParallel() {
	for i, pa := range rs.parallel {
		backup := runBackupBranchName(rs.specName, fmt.Sprintf("part-%d", i+1))
		if pa.agentID == rs.worktreeAgentID {
			// The worktree the retry resumes in; merged as the owner. Keep the
			// name it was spawned with so it stays a sibling of the carried
			// refs rather than their parent directory.
			rs.ownerBackup = backup
			continue
		}
		rs.carried = append(rs.carried, mergeTarget{agentID: pa.agentID, backup: backup})
	}
	rs.parallel = nil
}

// mergeTargets lists every worktree the run must merge, each paired with the
// backup branch it was given at spawn time. It covers all three shapes a run
// can end in: a single agent, a live parallel fan-out, and a run that has
// collapsed to one coordinator but still carries the other agents' worktrees.
func (rs *runState) mergeTargets() []mergeTarget {
	var targets []mergeTarget
	if rs.isParallel() {
		for i, pa := range rs.parallel {
			targets = append(targets, mergeTarget{
				agentID: pa.agentID,
				backup:  runBackupBranchName(rs.specName, fmt.Sprintf("part-%d", i+1)),
			})
		}
	} else {
		backup := rs.ownerBackup
		if backup == "" {
			backup = runBackupBranchName(rs.specName, "")
		}
		targets = []mergeTarget{{agentID: rs.worktreeAgentID, backup: backup}}
	}
	// Worktrees left over from a collapsed parallel run still hold work.
	return append(targets, rs.carried...)
}

// --- Message types for /run streaming ---

// runAgentEventMsg wraps a subagent event for the TUI update loop.
type runAgentEventMsg struct {
	event   subagent.Event
	agentID string // which agent emitted this event (for parallel mode)
}

// runAgentDoneMsg signals that a subagent has finished (events channel closed).
type runAgentDoneMsg struct {
	agentID string // which agent finished (for parallel mode)
	status  string // authoritative terminal status from the subagent
}

// GateResult holds the result of running a single gate command.
type GateResult struct {
	Name    string
	Command string
	Passed  bool
	Output  string
}

// runGateResultMsg carries the result of running all gate commands.
type runGateResultMsg struct {
	results []GateResult
	passed  bool // true if all gates passed
}

// runMergeResultMsg carries the result of merging the worktree branch.
type runMergeResultMsg struct {
	output          string
	err             error
	failedAgentID   string
	preservedWTPath string
}

// coordinatorContract is the Coordinator → Worker → Verifier execution contract
// injected into every /run prompt. It is the reason a run survives long plans:
// the coordinator's own context stays small because each slice is implemented in
// a fresh worker context that is discarded once the slice lands.
const coordinatorContract = `
## Your Role: Coordinator

You are the **Coordinator**. You do NOT implement slices yourself — you delegate
each one to a worker subagent, verify it, and record progress. Implementing
slices inline is what makes runs die: the context grows with every file read and
every diff until the provider drops the stream mid-slice.

### Delegating a slice

Spawn ONE worker per slice with the ` + "`" + `subagent` + "`" + ` tool:

- ` + "`" + `{agent: "worker", task: "<self-contained brief>"}` + "`" + ` — the default.
- ` + "`" + `{agent: "quick-task", task: "..."}` + "`" + ` — a single-file mechanical change.
- ` + "`" + `{tasks: [{agent: "worker", task: "..."}, ...]}` + "`" + ` — several slices at once, ONLY
  when the plan marks them parallel-safe, they touch disjoint files, AND the
  ` + "`" + `subagent` + "`" + ` tool description says this process runs more than one subagent at a
  time. Batching past that number does not overlap anything: the extra tasks
  queue inside the same tool call and it takes proportionally longer, which is
  how a "parallel" batch turns into a timeout. When in doubt, dispatch one
  slice per call — sequential slices are the normal case.

**Never spawn ` + "`" + `task` + "`" + ` or ` + "`" + `designer` + "`" + `.** They are [worktree] agents: their edits go to
a nested worktree that is never merged back, so the slice silently produces
nothing. ` + "`" + `worker` + "`" + ` and ` + "`" + `quick-task` + "`" + ` edit the current directory, which is what you want.

Each worker brief must stand alone — the worker cannot see this conversation.
State the exact files, the change, the surrounding conventions, and the verify
command. Do NOT paste the whole plan into a worker brief; give it only its slice.

### After each slice

1. Run that slice's verify command yourself.
2. If it fails, send a fix brief to a new worker (up to 3 attempts per slice),
   including the exact error output.
3. Tick the checkbox in specs/%[1]s/plan.md: ` + "`" + `- [ ] Step N:` + "`" + ` → ` + "`" + `- [x] Step N:` + "`" + `.

Tick a box only after its verify command has actually passed. A checked box that
was never verified is worse than an unchecked one — it ends the run early.

### After the last slice: Verify

Spawn the Verifier and act on its verdict:

` + "`" + `{agent: "code-reviewer", task: "Check the working-tree diff against these Done Criteria: <paste the Done Criteria from the briefing above, or the Acceptance Criteria if absent>. For each criterion answer MET or NOT MET with the file and line that proves it. Flag any stub, TODO, or panic(\"not implemented\") left in the changed files. End your reply with exactly one line: VERDICT: PASS or VERDICT: FAIL."}` + "`" + `

- **VERDICT: FAIL** — dispatch fix workers for the NOT MET items, then verify again.
  Repeat up to 10 cycles.
- **VERDICT: PASS** — report the changed files and stop. Do not commit or merge;
  /run handles gates and the merge.
`

// buildRunPrompt constructs the augmented prompt for the task subagent.
// If the plan.md uses heading-only format (no checkboxes), it injects a
// checklist so the agent can mark steps as completed.
func buildRunPrompt(specName, promptMD string, checklist []ChecklistStep) string {
	var b strings.Builder
	b.WriteString(promptMD)
	fmt.Fprintf(&b, coordinatorContract, specName)
	b.WriteString("\n## Execution Instructions\n")
	b.WriteString("- Follow the plan in specs/")
	b.WriteString(specName)
	b.WriteString("/plan.md step by step\n")
	b.WriteString("- Run tests after each step to verify correctness\n")
	b.WriteString("- Work in the current directory (worktree)\n")

	// If the plan has no checkboxes, inject a checklist and instruct the
	// agent to prepend one to plan.md on first edit.
	if len(checklist) > 0 && !checklistHasCheckboxes(checklist) {
		b.WriteString("- IMPORTANT: The plan.md has no progress checklist. As your FIRST action, prepend the following checklist to the top of plan.md:\n")
		b.WriteString("```\n## Progress\n\n")
		for i, step := range checklist {
			fmt.Fprintf(&b, "- [ ] Step %d: %s\n", i+1, step.Title)
		}
		b.WriteString("```\n")
	}
	b.WriteString("- After completing each step, update the plan.md checklist: change `- [ ] Step N:` to `- [x] Step N:`\n")

	return b.String()
}

// checklistHasCheckboxes returns true if any step was parsed from checkbox format.
// When the checklist was built from ### headings, all steps start as Done=false
// and we know there are no actual checkbox lines.
func checklistHasCheckboxes(steps []ChecklistStep) bool {
	for _, s := range steps {
		if s.Done {
			return true // at least one [x] was parsed → real checkboxes
		}
	}
	// All unchecked — could be real checkboxes or headings. Check titles for
	// heading-style format (no "Step N:" prefix typically present in checkboxes).
	// If any title starts with a heading keyword, it's likely from headings.
	for _, s := range steps {
		if strings.Contains(s.Title, "—") || strings.Contains(s.Title, "–") {
			return false // heading style: "PairingManager — Core Logic"
		}
	}
	return true // assume checkbox format
}

func runWorktreeName(specName string, suffix string) string {
	name := strings.Trim(specName, "/")
	name = strings.ReplaceAll(name, string(filepath.Separator), "-")
	name = strings.ReplaceAll(name, "/", "-")
	if suffix != "" {
		name += "-" + suffix
	}
	return name
}

func runBackupBranchName(specName, suffix string) string {
	name := strings.Trim(specName, "/")
	name = strings.ReplaceAll(name, string(filepath.Separator), "-")
	name = strings.ReplaceAll(name, "/", "-")
	if suffix != "" {
		// Dash, not slash: a nested ref (run/<spec>/part-1) collides with the
		// flat branch (run/<spec>) once both exist — git cannot create
		// refs/heads/run/<spec>/part-1 while refs/heads/run/<spec> is a file.
		name += "-" + suffix
	}
	return "run/" + name
}

func (m *model) createRunBackupBranch(agentID, branch string) error {
	if m.cfg.Orchestrator == nil || m.cfg.Orchestrator.Worktree() == nil {
		return fmt.Errorf("no worktree manager")
	}
	return m.cfg.Orchestrator.Worktree().CreateBackupBranch(agentID, branch)
}

func formatAvailableRunSpecsTable(specs []string) string {
	var b strings.Builder
	b.WriteString("**Available features:**\n\n")
	b.WriteString("| Feature | Run command |\n")
	b.WriteString("|---------|-------------|\n")
	for _, spec := range specs {
		feature := filepath.Base(spec)
		fmt.Fprintf(&b, "| `%s` | `/run %s` |\n", escapeMarkdownTableCell(feature), escapeMarkdownTableCell(spec))
	}
	return b.String()
}

func escapeMarkdownTableCell(s string) string {
	return strings.ReplaceAll(s, "|", `\|`)
}

// handleRunCommand handles the /run <spec-name> [--parallel] slash command.
func (m *model) handleRunCommand(args []string) (tea.Model, tea.Cmd) {
	if len(args) == 0 {
		m.showRunUsage()
		return m, nil
	}

	if m.cfg.Orchestrator == nil {
		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role:    "assistant",
			content: "Subagent system not available. Cannot run specs.",
		})
		return m, nil
	}

	specName, parallel := parseRunArgs(args)
	if specName == "" {
		m.chatModel.Messages = append(m.chatModel.Messages, message{role: "assistant", content: "Missing spec name."})
		return m, nil
	}

	// Read PROMPT.md.
	promptMD, err := readPromptMD(m.cfg.WorkDir, specName)
	if err != nil {
		m.showRunSpecError(err)
		return m, nil
	}

	// Parse gates.
	gates := parseGates(promptMD)

	// Parse plan.md checklist for sidebar display.
	checklist := parsePlanChecklist(m.cfg.WorkDir, specName)

	// Warn before spawning: a spec file past the read window is served to
	// workers one page at a time, so a worker sent to read its slice can act
	// on a partial plan. The SOP tells /plan to avoid this; this catches the
	// plans that arrive oversized anyway.
	if oversized := oversizedSpecFiles(m.cfg.WorkDir, specName); len(oversized) > 0 {
		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role:    "assistant",
			content: formatOversizedSpecWarning(oversized),
		})
	}

	gateInfo := formatRunGateInfo(gates)

	// Parallel mode: split slices across 2 agents.
	if parallel && len(checklist) >= 2 {
		return m.handleRunParallel(specName, promptMD, gates, checklist, gateInfo)
	}

	return m.startRunAgent(specName, promptMD, gates, checklist, gateInfo)
}

// showRunUsage appends the /run usage text, listing the specs that are
// available to run when there are any.
func (m *model) showRunUsage() {
	specs, _ := listAvailableSpecs(m.cfg.WorkDir)
	msg := "Usage: `/run <spec-name> [--parallel]`\n\nExecutes a spec's PROMPT.md using an isolated task agent.\nUse `--parallel` to split independent slices across 2 agents."
	if len(specs) > 0 {
		msg += "\n\n" + formatAvailableRunSpecsTable(specs)
	}
	m.chatModel.Messages = append(m.chatModel.Messages, message{role: "assistant", content: msg})
}

// showRunSpecError reports a failure to read the named spec, listing the specs
// that do exist so a typo is immediately visible.
func (m *model) showRunSpecError(err error) {
	specs, _ := listAvailableSpecs(m.cfg.WorkDir)
	errMsg := fmt.Sprintf("Error: %v", err)
	if len(specs) > 0 {
		errMsg += "\n\n" + formatAvailableRunSpecsTable(specs)
	}
	m.chatModel.Messages = append(m.chatModel.Messages, message{role: "assistant", content: errMsg})
}

// parseRunArgs splits /run arguments into the spec name and the parallel flag.
// The first non-flag argument wins; later ones are ignored.
func parseRunArgs(args []string) (specName string, parallel bool) {
	for _, arg := range args {
		switch arg {
		case "--parallel", "-p":
			parallel = true
		default:
			if specName == "" {
				specName = arg
			}
		}
	}
	return specName, parallel
}

// formatRunGateInfo renders gate names for the spawn announcement.
func formatRunGateInfo(gates []Gate) string {
	if len(gates) == 0 {
		return "none"
	}
	names := make([]string, len(gates))
	for i, g := range gates {
		names[i] = g.Name
	}
	return strings.Join(names, ", ")
}

// startRunAgent spawns the single-agent /run worker and installs the run state
// the event loop drives from there.
func (m *model) startRunAgent(
	specName, promptMD string, gates []Gate, checklist []ChecklistStep, gateInfo string,
) (tea.Model, tea.Cmd) {
	prompt := buildRunPrompt(specName, promptMD, checklist)

	events, agentID, err := m.cfg.Orchestrator.SpawnWithInput(m.ctx, subagent.AgentInput{
		Type:         "task",
		Prompt:       prompt,
		Worktree:     new(true),
		WorktreeName: runWorktreeName(specName, ""),
		SkipCleanup:  true,
		Timeout:      int((60 * time.Minute) / time.Millisecond),
	})
	if err != nil {
		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role:    "assistant",
			content: fmt.Sprintf("Failed to spawn task agent: %v", err),
		})
		return m, nil
	}

	if err := m.createRunBackupBranch(agentID, runBackupBranchName(specName, "")); err != nil {
		m.chatModel.Messages = append(m.chatModel.Messages, message{role: "assistant", content: fmt.Sprintf("Failed to create run backup branch: %v", err)})
		return m, nil
	}

	m.run = &runState{
		specName: specName,
		promptMD: promptMD,
		gates:    gates,
		agentID:  agentID,
		// The spawning agent owns the worktree; retries resume inside it.
		worktreeAgentID: agentID,
		phase:           "running",
		maxRetries:      10,
		events:          events,
		startTime:       time.Now(),
		checklist:       checklist,
	}

	m.chatModel.Messages = append(m.chatModel.Messages, message{
		role: "assistant",
		content: fmt.Sprintf("**Running spec `%s`** [cycle 1/%d] — agent `%s` spawned in worktree\nGates: %s",
			specName, m.run.maxRetries, agentID, gateInfo),
	})

	m.chatModel.Messages = append(m.chatModel.Messages, message{role: "assistant", content: ""})
	m.chatModel.Streaming = ""
	m.chatModel.Thinking = ""
	m.running = true
	m.chatModel.Scroll = 0

	return m, waitForRunAgent(events, agentID)
}

// handleRunParallel spawns 2 agents, each handling a subset of the plan slices.
// Slices are split into first-half / second-half. Each agent gets its own worktree.
func (m *model) handleRunParallel(specName, promptMD string, gates []Gate, checklist []ChecklistStep, gateInfo string) (tea.Model, tea.Cmd) {
	mid := len(checklist) / 2

	// Build prompts for each agent with their assigned slices.
	prompt1 := buildParallelPrompt(specName, promptMD, checklist, 0, mid)
	prompt2 := buildParallelPrompt(specName, promptMD, checklist, mid, len(checklist))

	useWorktree := true

	// Spawn agent 1.
	events1, agentID1, err := m.cfg.Orchestrator.SpawnWithInput(m.ctx, subagent.AgentInput{
		Type:         "task",
		Prompt:       prompt1,
		Worktree:     &useWorktree,
		WorktreeName: runWorktreeName(specName, "part-1"),
		SkipCleanup:  true,
		Timeout:      int((60 * time.Minute) / time.Millisecond),
	})
	if err != nil {
		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role:    "assistant",
			content: fmt.Sprintf("Failed to spawn agent 1: %v", err),
		})
		return m, nil
	}

	// Spawn agent 2.
	events2, agentID2, err := m.cfg.Orchestrator.SpawnWithInput(m.ctx, subagent.AgentInput{
		Type:         "task",
		Prompt:       prompt2,
		Worktree:     &useWorktree,
		WorktreeName: runWorktreeName(specName, "part-2"),
		SkipCleanup:  true,
		Timeout:      int((60 * time.Minute) / time.Millisecond),
	})
	if err != nil {
		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role:    "assistant",
			content: fmt.Sprintf("Failed to spawn agent 2 (agent 1 `%s` is running): %v", agentID1, err),
		})
		return m, nil
	}

	if err := m.createRunBackupBranch(agentID1, runBackupBranchName(specName, "part-1")); err != nil {
		m.chatModel.Messages = append(m.chatModel.Messages, message{role: "assistant", content: fmt.Sprintf("Failed to create run backup branch for agent 1: %v", err)})
		return m, nil
	}
	if err := m.createRunBackupBranch(agentID2, runBackupBranchName(specName, "part-2")); err != nil {
		m.chatModel.Messages = append(m.chatModel.Messages, message{role: "assistant", content: fmt.Sprintf("Failed to create run backup branch for agent 2: %v", err)})
		return m, nil
	}

	// Build slice index arrays.
	slices1 := make([]int, mid)
	for i := range slices1 {
		slices1[i] = i
	}
	slices2 := make([]int, len(checklist)-mid)
	for i := range slices2 {
		slices2[i] = mid + i
	}

	m.run = &runState{
		specName: specName,
		promptMD: promptMD,
		gates:    gates,
		agentID:  agentID1, // primary agent for fallback
		// Gates and verification run in the first agent's worktree.
		worktreeAgentID: agentID1,
		phase:           "running",
		maxRetries:      10,
		startTime:       time.Now(),
		checklist:       checklist,
		parallel: []*parallelAgent{
			{agentID: agentID1, events: events1, slices: slices1},
			{agentID: agentID2, events: events2, slices: slices2},
		},
	}

	m.chatModel.Messages = append(m.chatModel.Messages, message{
		role: "assistant",
		content: fmt.Sprintf("**Running spec `%s` in parallel** [cycle 1/%d]\n"+
			"Agent 1: `%s` → slices 1–%d\n"+
			"Agent 2: `%s` → slices %d–%d\n"+
			"Gates: %s",
			specName, m.run.maxRetries,
			agentID1, mid,
			agentID2, mid+1, len(checklist),
			gateInfo),
	})

	m.chatModel.Messages = append(m.chatModel.Messages, message{role: "assistant", content: ""})
	m.chatModel.Streaming = ""
	m.chatModel.Thinking = ""
	m.running = true
	m.chatModel.Scroll = 0

	// Start consuming events from both agents via fan-in.
	return m, m.waitForParallelRunEvents()
}

// buildParallelPrompt builds a prompt for a parallel agent that handles
// a subset of the plan slices (from index `from` to `to`, exclusive).
func buildParallelPrompt(specName, promptMD string, checklist []ChecklistStep, from, to int) string {
	var b strings.Builder
	b.WriteString(promptMD)
	fmt.Fprintf(&b, coordinatorContract, specName)
	b.WriteString("\n## Execution Instructions (Parallel Mode)\n")
	b.WriteString("You are ONE OF TWO coordinators working on this spec in parallel.\n")
	b.WriteString("You are responsible for delegating ONLY these slices:\n\n")
	for i := from; i < to; i++ {
		fmt.Fprintf(&b, "- Slice %d: %s\n", i+1, checklist[i].Title)
	}
	b.WriteString("\nDo NOT implement slices assigned to the other coordinator.\n")
	b.WriteString("Verify only your own slices — the Verifier step covers your half.\n")
	b.WriteString("- Follow the plan in specs/")
	b.WriteString(specName)
	b.WriteString("/plan.md for details on your assigned slices\n")
	b.WriteString("- After completing each slice, update plan.md: change `- [ ] Step N:` to `- [x] Step N:`\n")
	b.WriteString("- Run tests after each slice to verify correctness\n")
	b.WriteString("- Work in the current directory (worktree)\n")
	return b.String()
}

// waitForRunAgent returns a tea.Cmd that reads the next event from the subagent channel.
func waitForRunAgent(events <-chan subagent.Event, agentID string) tea.Cmd {
	if events == nil {
		return nil
	}
	return func() tea.Msg {
		ev, ok := <-events
		if !ok {
			return runAgentDoneMsg{agentID: agentID}
		}
		if ev.Type == "run_done" {
			return runAgentDoneMsg{agentID: agentID, status: ev.Status}
		}
		return runAgentEventMsg{event: ev, agentID: agentID}
	}
}

// handleRunAgentEvent processes a streaming event from the /run subagent.
func (m *model) handleRunAgentEvent(msg runAgentEventMsg) (tea.Model, tea.Cmd) {
	ev := msg.event

	// Feed the matrix rain widget so it visibly reacts to ACP subagent output.
	if ev.Content != "" {
		m.matrix.feed(ev.Content, m.mainWidth())
	} else if ev.Type != "" {
		m.matrix.feed(ev.Type, m.mainWidth())
	}

	switch ev.Type {
	case "text_delta":
		m.applyRunTextDelta(ev)

	case "tool_call":
		m.applyRunToolCall(ev)

	case "tool_result":
		m.applyRunToolResult(ev)

	case "message_start":
		// New message from the agent — add an empty assistant placeholder.
		m.chatModel.Streaming = ""
		m.chatModel.Messages = append(m.chatModel.Messages, message{role: "assistant", content: ""})

	case "message_end":
		// Message completed — reset streaming accumulator for the next message.
		m.chatModel.Streaming = ""

	case "error":
		m.applyRunError(ev)
	}

	// Keep consuming events from the subagent.
	return m, m.waitForRunEvents()
}

// applyRunTextDelta appends streamed text to the open assistant message and
// folds it into the trailing LLM trace entry.
func (m *model) applyRunTextDelta(ev subagent.Event) {
	m.chatModel.Streaming += ev.Content
	// Update the last assistant message with accumulated text.
	for i := len(m.chatModel.Messages) - 1; i >= 0; i-- {
		if m.chatModel.Messages[i].role == "assistant" {
			m.chatModel.Messages[i].content = m.chatModel.Streaming
			break
		}
	}
	m.chatModel.Scroll = 0
	// Trace.
	if len(m.chatModel.TraceLog) > 0 && m.chatModel.TraceLog[len(m.chatModel.TraceLog)-1].kind == "llm" {
		m.chatModel.TraceLog[len(m.chatModel.TraceLog)-1].detail = m.chatModel.Streaming
	} else {
		m.chatModel.TraceLog = append(m.chatModel.TraceLog, traceEntry{
			time: time.Now(), kind: "llm", summary: "agent response", detail: ev.Content,
		})
	}
}

// applyRunToolCall opens a tool card for a subagent tool invocation.
func (m *model) applyRunToolCall(ev subagent.Event) {
	m.statusModel.ActiveTool = ev.Content
	m.statusModel.ToolStart = time.Now()
	m.chatModel.TraceLog = append(m.chatModel.TraceLog, traceEntry{
		time: time.Now(), kind: "tool_call", summary: fmt.Sprintf(">>> %s", ev.Content),
	})
	// Compute a one-liner from the tool args so the chat card can render
	// the file path / command alongside the tool name — without this the
	// header would only show the bare tool name (e.g. "read") in gray,
	// while the parent path (see agent_loop.go) already fills toolIn.
	var toolIn string
	if args, ok := ev.ToolArgs.(map[string]any); ok {
		toolIn = toolCallSummary(ev.Content, args)
	}
	m.chatModel.Messages = append(m.chatModel.Messages, message{
		role: "tool", tool: ev.Content, toolIn: toolIn,
	})
}

// applyRunToolResult closes the open tool card and, after a write or edit,
// re-reads the plan checklist the agent may have ticked.
func (m *model) applyRunToolResult(ev subagent.Event) {
	prevTool := m.statusModel.ActiveTool
	m.statusModel.ActiveTool = ""
	m.chatModel.TraceLog = append(m.chatModel.TraceLog, traceEntry{
		time: time.Now(), kind: "tool_result", summary: "<<< result",
		detail: ev.Content,
	})
	// Update the last tool message with the result.
	for i := len(m.chatModel.Messages) - 1; i >= 0; i-- {
		if m.chatModel.Messages[i].role == "tool" && m.chatModel.Messages[i].content == "" {
			m.chatModel.Messages[i].content = toolResultSummary(ev.Content)
			break
		}
	}

	// Refresh checklist after write/edit operations — the agent may have
	// updated plan.md checkboxes. This is a cheap disk read.
	if m.run != nil && (prevTool == "write" || prevTool == "edit") {
		m.refreshRunChecklist()
	}
}

// applyRunError surfaces a subagent error in the transcript and the trace.
func (m *model) applyRunError(ev subagent.Event) {
	m.chatModel.Messages = append(m.chatModel.Messages, message{
		role:    "assistant",
		content: fmt.Sprintf("Agent error: %s", ev.Error),
	})
	m.chatModel.TraceLog = append(m.chatModel.TraceLog, traceEntry{
		time: time.Now(), kind: "error", summary: "agent error", detail: ev.Error,
	})
}

// handleRunAgentDone is called when a subagent events channel closes.
// It transitions to gate validation if gates are defined, or directly to merge.
func (m *model) handleRunAgentDone(msg runAgentDoneMsg) (tea.Model, tea.Cmd) {
	if m.run == nil {
		m.running = false
		return m, nil
	}

	// Parallel mode: mark the specific agent as done.
	if m.run.isParallel() {
		for _, pa := range m.run.parallel {
			if pa.agentID == msg.agentID {
				pa.done = true
				pa.status = msg.status
				break
			}
		}
		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role:    "assistant",
			content: fmt.Sprintf("**Agent `%s` finished.**", msg.agentID),
		})
		m.chatModel.Streaming = ""

		// If other agents are still running, keep consuming their events.
		if !m.run.allAgentsDone() {
			m.chatModel.Messages = append(m.chatModel.Messages, message{role: "assistant", content: ""})
			return m, m.waitForRunEvents()
		}
		// All parallel agents done — fall through to gate validation.
	}

	m.running = false
	m.statusModel.ActiveTool = ""
	m.chatModel.Streaming = ""
	m.chatModel.Thinking = ""
	m.run.status = msg.status

	// Verify that every subagent exited cleanly (status == "completed")
	// before moving on to gate validation. A non-clean exit is usually a
	// provider stream error (429/502/400) rather than a decision to stop —
	// the work so far is intact in the worktree, so resume there rather than
	// abandoning the whole run.
	if bad := m.failedAgentIDs(); len(bad) > 0 {
		m.refreshRunChecklist()
		if cmd := m.retryRun("agent exited with non-zero status",
			m.unfinishedSlicesContext()); cmd != nil {
			return m, cmd
		}
		m.run.phase = "failed"
		wtPaths := m.runWorktreePathsFor(bad)
		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role: "assistant",
			content: fmt.Sprintf(
				"**Subagent exited with non-zero status** for spec `%s` after %d retries — skipping gates and merge.\n"+
					"Inspect the worktree(s) below and re-run `/run %s` once the issue is fixed.\n%s",
				m.run.specName, m.run.maxRetries, m.run.specName, wtPaths),
		})
		if report, rerr := m.writeRunSummary("agent_failed"); rerr == nil {
			m.chatModel.Messages = append(m.chatModel.Messages, message{
				role:    "assistant",
				content: fmt.Sprintf("Summary report: `%s`", report),
			})
		}
		return m, nil
	}

	m.chatModel.Messages = append(m.chatModel.Messages, message{
		role:    "assistant",
		content: "**All agents finished** — validating gates...",
	})

	// If no gates, skip straight to completeness verification.
	if len(m.run.gates) == 0 {
		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role:    "assistant",
			content: "No gates defined — verifying plan completeness.",
		})
		return m.verifyRunComplete()
	}

	// Run gate validation.
	m.run.phase = "gating"
	return m, m.runGatesCmd()
}

// unfinishedSlices returns the checklist steps that are still unchecked,
// as 1-based "Step N: Title" strings.
func (rs *runState) unfinishedSlices() []string {
	var out []string
	for i, step := range rs.checklist {
		if !step.Done {
			out = append(out, fmt.Sprintf("Step %d: %s", i+1, step.Title))
		}
	}
	return out
}

// unfinishedSlicesContext renders the unchecked slices as a prompt fragment
// for the next cycle. Empty when the checklist is absent or fully ticked.
func (m *model) unfinishedSlicesContext() string {
	if m.run == nil {
		return ""
	}
	pending := m.run.unfinishedSlices()
	if len(pending) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "The previous cycle stopped with %d of %d slices unfinished:\n\n",
		len(pending), len(m.run.checklist))
	for _, p := range pending {
		fmt.Fprintf(&b, "- %s\n", p)
	}
	b.WriteString("\nWork already on disk is intact — inspect the worktree before redoing anything.\n")
	b.WriteString("Resume from the first unfinished slice.\n")
	return b.String()
}

// verifyRunComplete is the Verifier stage: gates only prove the tree builds,
// not that the plan was carried out. A green build over a half-implemented
// plan is the failure this catches — it re-reads plan.md from the worktree and
// refuses to merge while slices remain unchecked, looping instead.
func (m *model) verifyRunComplete() (tea.Model, tea.Cmd) {
	m.run.phase = "verifying"
	m.refreshRunChecklist()

	pending := m.run.unfinishedSlices()
	if len(pending) == 0 {
		done := len(m.run.checklist)
		msg := "**Verifier: PASS** — no plan checklist to verify."
		if done > 0 {
			msg = fmt.Sprintf("**Verifier: PASS** — all %d slices complete.", done)
		}
		m.chatModel.Messages = append(m.chatModel.Messages, message{role: "assistant", content: msg})
		m.run.phase = "merging"
		return m, m.mergeWorktreeCmd()
	}

	m.chatModel.Messages = append(m.chatModel.Messages, message{
		role: "assistant",
		content: fmt.Sprintf("**Verifier: FAIL** — %d of %d slices still unchecked in plan.md.",
			len(pending), len(m.run.checklist)),
	})

	if cmd := m.retryRun("plan incomplete", m.unfinishedSlicesContext()); cmd != nil {
		return m, cmd
	}

	// Cycles exhausted with work still outstanding — do not merge.
	m.run.phase = "failed"
	wtPath := m.runWorktreePath(m.run.worktreeAgentID)
	m.chatModel.Messages = append(m.chatModel.Messages, message{
		role: "assistant",
		content: fmt.Sprintf(
			"**Verification failed** for spec `%s` after %d retries — %d slices never completed.\n"+
				"Not merging. Worktree preserved at: `%s`",
			m.run.specName, m.run.maxRetries, len(pending), wtPath),
	})
	if report, err := m.writeRunSummary("verify_failed"); err == nil {
		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role:    "assistant",
			content: fmt.Sprintf("Summary report: `%s`", report),
		})
	}
	return m, nil
}

// runWorktreePath returns the worktree directory for an agent, or "" when
// there is no orchestrator or worktree manager to ask. Callers reach it from
// terminal-reporting paths that may run without either.
func (m *model) runWorktreePath(agentID string) string {
	if m.cfg.Orchestrator == nil {
		return ""
	}
	wm := m.cfg.Orchestrator.Worktree()
	if wm == nil {
		return ""
	}
	return wm.PathFor(agentID)
}

// retryRun spawns the next cycle in the same worktree, carrying `context`
// (unfinished slices, gate output) into the new coordinator's prompt.
// It returns nil when the retry budget is spent, leaving the caller to
// decide how to report the terminal failure.
func (m *model) retryRun(reason, extraContext string) tea.Cmd {
	if m.run == nil || m.run.retries >= m.run.maxRetries {
		return nil
	}
	if m.cfg.Orchestrator == nil {
		return nil
	}

	m.run.retries++
	m.run.phase = "retrying"

	wtPath := m.runWorktreePath(m.run.worktreeAgentID)

	m.chatModel.Messages = append(m.chatModel.Messages, message{
		role: "assistant",
		content: fmt.Sprintf("**%s** — cycle %d/%d (retry %d) in worktree `%s`...",
			reason, m.run.retries+1, m.run.maxRetries, m.run.retries, wtPath),
	})

	prompt := buildResumePrompt(m.run.specName, m.run.promptMD, reason, extraContext)

	events, agentID, err := m.cfg.Orchestrator.SpawnWithInput(m.ctx, subagent.AgentInput{
		Type:        "task",
		Prompt:      prompt,
		WorkDir:     wtPath,
		SkipCleanup: true,
		Timeout:     int((60 * time.Minute) / time.Millisecond),
	})
	if err != nil {
		m.run.phase = "failed"
		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role:    "assistant",
			content: fmt.Sprintf("Failed to spawn retry agent: %v", err),
		})
		return nil
	}

	// The new agent owns the run from here; parallel fan-in is over once we
	// fall back to a single resuming coordinator.
	m.run.collapseParallel()

	m.run.agentID = agentID
	m.run.events = events
	m.run.status = ""
	m.run.phase = "running"

	m.chatModel.Messages = append(m.chatModel.Messages, message{role: "assistant", content: ""})
	m.chatModel.Streaming = ""
	m.chatModel.Thinking = ""
	m.running = true
	m.chatModel.Scroll = 0

	return waitForRunAgent(events, agentID)
}

// buildResumePrompt constructs the prompt for a cycle that resumes an
// interrupted or incomplete run in an existing worktree.
func buildResumePrompt(specName, promptMD, reason, extraContext string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "The previous execution cycle did not finish: %s.\n\n", reason)
	if extraContext != "" {
		b.WriteString("## State\n")
		b.WriteString(extraContext)
		b.WriteString("\n")
	}
	b.WriteString("## Original Task\n")
	b.WriteString(promptMD)
	fmt.Fprintf(&b, coordinatorContract, specName)
	b.WriteString("\n## Resume Instructions\n")
	b.WriteString("- You are continuing in the SAME worktree as the previous cycle — its work is already on disk\n")
	b.WriteString("- Read specs/")
	b.WriteString(specName)
	b.WriteString("/plan.md first to see which slices are already ticked\n")
	b.WriteString("- Re-verify one completed slice before trusting the checklist; if a ticked slice does not actually work, untick it\n")
	b.WriteString("- Delegate the remaining slices to workers as described above\n")
	return b.String()
}

// failedAgentIDs returns the IDs of subagents (single or parallel) that
// did not exit cleanly. An agent is considered failed when its status in
// the orchestrator is anything other than "completed" or "running".
// When the orchestrator has no record of an agent yet (race between
// channel close and state update), it is treated as failed so the user
// is warned rather than silently merged.
func (m *model) failedAgentIDs() []string {
	if m.run == nil || m.cfg.Orchestrator == nil {
		return nil
	}
	if m.run.isParallel() {
		var bad []string
		for _, pa := range m.run.parallel {
			if m.resolvedAgentStatus(pa.status, pa.agentID) != "completed" {
				bad = append(bad, pa.agentID)
			}
		}
		return bad
	}
	if m.run.agentID == "" {
		return nil
	}
	if m.resolvedAgentStatus(m.run.status, m.run.agentID) != "completed" {
		return []string{m.run.agentID}
	}
	return nil
}

// resolvedAgentStatus returns status when the run already carries one, and
// otherwise asks the orchestrator for agentID's recorded status. An agent the
// orchestrator has no record of resolves to "", which every caller reads as
// "not completed" — see failedAgentIDs.
func (m *model) resolvedAgentStatus(status, agentID string) string {
	if status != "" {
		return status
	}
	if m.cfg.Orchestrator == nil {
		return ""
	}
	if st, ok := m.cfg.Orchestrator.Get(agentID); ok {
		return st.Status
	}
	return ""
}

// runWorktreePathsFor returns a markdown bullet list of worktree paths
// for the given agent IDs, one per line. Used by the post-run failure
// message so the user can find the preserved worktree(s).
func (m *model) runWorktreePathsFor(agentIDs []string) string {
	if m.cfg.Orchestrator == nil || len(agentIDs) == 0 {
		return ""
	}
	wm := m.cfg.Orchestrator.Worktree()
	if wm == nil {
		return ""
	}
	var b strings.Builder
	for _, aid := range agentIDs {
		if p := wm.PathFor(aid); p != "" {
			fmt.Fprintf(&b, "- `%s`\n", p)
		}
	}
	return b.String()
}

// runGatesCmd returns a tea.Cmd that runs each gate command sequentially in the worktree.
func (m *model) runGatesCmd() tea.Cmd {
	if m.run == nil || m.cfg.Orchestrator == nil {
		return nil
	}

	wm := m.cfg.Orchestrator.Worktree()
	if wm == nil {
		return func() tea.Msg {
			return runGateResultMsg{passed: true}
		}
	}

	// Gates run in the worktree the run owns — not the agent currently
	// streaming, which after a retry has no worktree of its own.
	worktreePath := wm.PathFor(m.run.worktreeAgentID)
	if worktreePath == "" {
		// No worktree path found — treat as pass (agent may not have used worktree).
		return func() tea.Msg {
			return runGateResultMsg{passed: true}
		}
	}

	gates := m.run.gates
	ctx := m.ctx

	return func() tea.Msg {
		return runGates(ctx, worktreePath, gates)
	}
}

// runGates executes gate commands sequentially in the given directory.
func runGates(ctx context.Context, workDir string, gates []Gate) runGateResultMsg {
	var results []GateResult
	allPassed := true

	for _, gate := range gates {
		cmd := procs.CommandContext(ctx, "sh", "-c", gate.Command)
		cmd.Dir = workDir

		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr

		err := cmd.Run()
		passed := err == nil

		output := stdout.String()
		if stderr.Len() > 0 {
			if output != "" {
				output += "\n"
			}
			output += stderr.String()
		}

		results = append(results, GateResult{
			Name:    gate.Name,
			Command: gate.Command,
			Passed:  passed,
			Output:  output,
		})

		if !passed {
			allPassed = false
			break // Stop at first failure.
		}
	}

	return runGateResultMsg{results: results, passed: allPassed}
}

// handleRunGateResult processes gate validation results.
func (m *model) handleRunGateResult(msg runGateResultMsg) (tea.Model, tea.Cmd) {
	if m.run == nil {
		return m, nil
	}

	// Store gate results for the summary report.
	m.run.gateResults = msg.results

	// Build gate results summary.
	var summary strings.Builder
	summary.WriteString("**Gate Results:**\n")
	for _, r := range msg.results {
		status := "PASS"
		if !r.Passed {
			status = "FAIL"
		}
		fmt.Fprintf(&summary, "- **%s** (`%s`): %s\n", r.Name, r.Command, status)
		if !r.Passed && r.Output != "" {
			// Include truncated output for failed gates.
			out := r.Output
			if len(out) > 500 {
				out = out[:500] + "...(truncated)"
			}
			fmt.Fprintf(&summary, "  ```\n  %s\n  ```\n", strings.TrimSpace(out))
		}
	}

	m.chatModel.Messages = append(m.chatModel.Messages, message{
		role:    "assistant",
		content: summary.String(),
	})

	if msg.passed {
		// Gates prove the tree builds; the Verifier proves the plan was done.
		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role:    "assistant",
			content: "All gates passed — verifying plan completeness...",
		})
		return m.verifyRunComplete()
	}

	// Gates failed — attempt retry or give up.
	m.run.gateOutput = formatGateFailures(msg.results)

	if cmd := m.retryRun("Gate failed", m.run.gateOutput); cmd != nil {
		return m, cmd
	}

	// Retries exhausted.
	m.run.phase = "failed"

	wtPath := m.runWorktreePath(m.run.worktreeAgentID)

	m.chatModel.Messages = append(m.chatModel.Messages, message{
		role: "assistant",
		content: fmt.Sprintf("**Gate validation failed** for spec `%s` after %d retries.\nWorktree preserved at: `%s`\nInspect manually and fix the issues.",
			m.run.specName, m.run.maxRetries, wtPath),
	})

	// Write summary report for gate failure.
	if report, err := m.writeRunSummary("gate_failed"); err == nil {
		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role:    "assistant",
			content: fmt.Sprintf("Summary report: `%s`", report),
		})
	}

	return m, nil
}

// mergeWorktreeCmd returns a tea.Cmd that merges the worktree branch and cleans up.
// In parallel mode, it merges all agent worktrees sequentially.
func (m *model) mergeWorktreeCmd() tea.Cmd {
	if m.run == nil || m.cfg.Orchestrator == nil {
		return nil
	}

	wm := m.cfg.Orchestrator.Worktree()
	if wm == nil {
		return func() tea.Msg {
			return runMergeResultMsg{output: "no worktree manager"}
		}
	}

	// Collect agent IDs to merge (parallel or single), each with the backup
	// branch it was given at spawn time so the ref can be moved onto the work.
	targets := m.run.mergeTargets()

	specName := m.run.specName

	return func() tea.Msg {
		return mergeRunTargets(wm, targets, specName)
	}
}

// mergeRunTargets commits, backs up and merges each worktree in turn, stopping
// at the first failure so the offending worktree is preserved rather than
// cleaned up behind a half-finished merge. It is a package-level function, not
// a closure, so the whole sequence can be driven directly in tests.
func mergeRunTargets(wm *subagent.WorktreeManager, targets []mergeTarget, specName string) runMergeResultMsg {
	var allOutput strings.Builder
	for _, t := range targets {
		aid := t.agentID

		// Commit what the agent produced before anything can remove it.
		// The task agent is told its edits stay local to the worktree and
		// is never asked to commit, so without this the merge below has no
		// commits to take and Cleanup force-removes the only copy.
		if _, err := wm.CommitAll(aid, fmt.Sprintf("pi-go run %s (agent %s)", specName, aid)); err != nil {
			return runMergeResultMsg{
				output:          allOutput.String(),
				err:             fmt.Errorf("commit worktree for %s: %w", aid, err),
				failedAgentID:   aid,
				preservedWTPath: wm.PathFor(aid),
			}
		}
		// Move the backup ref onto the committed work. It was pointed at
		// the base commit at spawn time, when there was nothing to back up.
		if err := wm.CreateBackupBranch(aid, t.backup); err != nil {
			return runMergeResultMsg{
				output:          allOutput.String(),
				err:             fmt.Errorf("back up worktree for %s: %w", aid, err),
				failedAgentID:   aid,
				preservedWTPath: wm.PathFor(aid),
			}
		}

		out, err := wm.MergeBack(aid)
		if err != nil {
			return runMergeResultMsg{
				output:          allOutput.String() + out,
				err:             fmt.Errorf("merge %s: %w", aid, err),
				failedAgentID:   aid,
				preservedWTPath: wm.PathFor(aid),
			}
		}
		// Auto-cleanup on success: worktrees accumulate otherwise
		// and there's no UI to inspect them. Conflicts are handled
		// by the err branch above, which preserves the worktree.
		_ = wm.Cleanup(aid)
		if out != "" {
			fmt.Fprintf(&allOutput, "[%s] %s\n", aid, out)
		}
	}
	return runMergeResultMsg{output: allOutput.String()}
}

// handleRunMergeResult processes the merge result.
func (m *model) handleRunMergeResult(msg runMergeResultMsg) (tea.Model, tea.Cmd) {
	if m.run == nil {
		return m, nil
	}

	if msg.err != nil {
		m.run.phase = "failed"

		wtPath := msg.preservedWTPath
		if wtPath == "" {
			targetAgentID := m.run.worktreeAgentID
			if msg.failedAgentID != "" {
				targetAgentID = msg.failedAgentID
			}
			wtPath = m.runWorktreePath(targetAgentID)
		}

		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role: "assistant",
			content: fmt.Sprintf("**Merge failed** for spec `%s`: %v\nWorktree preserved at: `%s`",
				m.run.specName, msg.err, wtPath),
		})

		// Write summary report for merge failure.
		if report, err := m.writeRunSummary("merge_failed"); err == nil {
			m.chatModel.Messages = append(m.chatModel.Messages, message{
				role:    "assistant",
				content: fmt.Sprintf("Summary report: `%s`", report),
			})
		}
		return m, nil
	}

	m.run.phase = "done"

	// Mark all checklist items as completed on successful merge.
	for i := range m.run.checklist {
		m.run.checklist[i].Done = true
	}

	m.chatModel.Messages = append(m.chatModel.Messages, message{
		role:    "assistant",
		content: fmt.Sprintf("**Spec `%s` completed** — changes merged successfully.", m.run.specName),
	})

	// Write summary report.
	if report, err := m.writeRunSummary("completed"); err != nil {
		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role:    "assistant",
			content: fmt.Sprintf("Warning: failed to write summary report: %v", err),
		})
	} else {
		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role:    "assistant",
			content: fmt.Sprintf("Summary report: `%s`", report),
		})
	}

	return m, nil
}

// writeRunSummary writes a SUMMARY.md report to the spec directory.
// Returns the path to the written report, or an error.
func (m *model) writeRunSummary(outcome string) (string, error) {
	if m.run == nil {
		return "", fmt.Errorf("no run state")
	}
	report := buildRunSummaryReport(m.run, outcome)
	reportPath := filepath.Join(m.cfg.WorkDir, "specs", m.run.specName, "SUMMARY.md")
	if err := os.WriteFile(reportPath, []byte(report), 0o644); err != nil {
		return "", fmt.Errorf("writing summary: %w", err)
	}
	return reportPath, nil
}

// buildRunSummaryReport generates a markdown summary of a /run execution.
func buildRunSummaryReport(rs *runState, outcome string) string {
	var b strings.Builder

	b.WriteString("# Run Summary\n\n")
	writeRunSummaryMetadata(&b, rs, outcome)
	writeRunSummaryGates(&b, rs)

	// Unfinished slices, when the run stopped short of the plan.
	if pending := rs.unfinishedSlices(); len(pending) > 0 {
		b.WriteString("## Unfinished Slices\n\n")
		for _, p := range pending {
			fmt.Fprintf(&b, "- %s\n", p)
		}
		b.WriteString("\n")
	}

	writeRunSummaryResult(&b, rs, outcome)

	return b.String()
}

// writeRunSummaryMetadata writes the spec/agent/outcome table.
func writeRunSummaryMetadata(b *strings.Builder, rs *runState, outcome string) {
	b.WriteString("## Metadata\n\n")
	b.WriteString("| Field | Value |\n")
	b.WriteString("|-------|-------|\n")
	fmt.Fprintf(b, "| Spec | `%s` |\n", rs.specName)
	fmt.Fprintf(b, "| Agent | `%s` |\n", rs.agentID)
	fmt.Fprintf(b, "| Outcome | **%s** |\n", outcome)
	fmt.Fprintf(b, "| Retries | %d / %d |\n", rs.retries, rs.maxRetries)
	if n := len(rs.checklist); n > 0 {
		fmt.Fprintf(b, "| Slices | %d / %d complete |\n", n-len(rs.unfinishedSlices()), n)
	}
	if !rs.startTime.IsZero() {
		fmt.Fprintf(b, "| Started | %s |\n", rs.startTime.Format(time.RFC3339))
		fmt.Fprintf(b, "| Duration | %s |\n", time.Since(rs.startTime).Truncate(time.Second))
	}
	b.WriteString("\n")
}

// writeRunSummaryGates writes the gate section: nothing defined, defined but
// never executed, or the pass/fail roll-up with failure output.
func writeRunSummaryGates(b *strings.Builder, rs *runState) {
	b.WriteString("## Gates\n\n")
	if len(rs.gateResults) == 0 && len(rs.gates) == 0 {
		b.WriteString("No gates defined.\n\n")
		return
	}
	if len(rs.gateResults) == 0 {
		b.WriteString("Gates were defined but not executed.\n\n")
		for _, g := range rs.gates {
			fmt.Fprintf(b, "- **%s**: `%s`\n", g.Name, g.Command)
		}
		b.WriteString("\n")
		return
	}
	allPassed := true
	for _, r := range rs.gateResults {
		if !r.Passed {
			allPassed = false
		}
		writeRunSummaryGateResult(b, r)
	}
	b.WriteString("\n")
	if allPassed {
		b.WriteString("All gates **passed**.\n\n")
		return
	}
	b.WriteString("Some gates **failed**.\n\n")
}

// writeRunSummaryGateResult writes one gate's verdict, with its output block
// when it failed and produced any.
func writeRunSummaryGateResult(b *strings.Builder, r GateResult) {
	status := "PASS"
	if !r.Passed {
		status = "FAIL"
	}
	fmt.Fprintf(b, "- **%s** (`%s`): **%s**\n", r.Name, r.Command, status)
	if r.Passed || r.Output == "" {
		return
	}
	out := strings.TrimSpace(r.Output)
	if len(out) > 1000 {
		out = out[:1000] + "\n...(truncated)"
	}
	fmt.Fprintf(b, "  ```\n  %s\n  ```\n", out)
}

// writeRunSummaryResult explains the outcome in prose.
func writeRunSummaryResult(b *strings.Builder, rs *runState, outcome string) {
	b.WriteString("## Result\n\n")
	switch outcome {
	case "completed":
		b.WriteString("All gates passed, the plan checklist was complete, and changes were merged successfully.\n")
	case "gate_failed":
		fmt.Fprintf(b, "Gate validation failed after %d retries. Worktree preserved for manual inspection.\n", rs.retries)
	case "verify_failed":
		fmt.Fprintf(b, "Gates passed but the plan was still incomplete after %d retries — the run stopped short of the checklist. Nothing was merged; the worktree is preserved so the finished slices are not lost.\n", rs.retries)
	case "merge_failed":
		b.WriteString("Gates passed but merge into the main branch failed. Worktree preserved for manual resolution.\n")
	case "agent_failed":
		b.WriteString("Subagent process exited with non-zero status (or was killed) before gates could run. Worktree preserved for manual inspection — see chat for the failing agent IDs and their worktree paths.\n")
	default:
		fmt.Fprintf(b, "Run ended with status: %s\n", outcome)
	}
}

// formatGateFailures formats gate results into a string for retry prompts.
func formatGateFailures(results []GateResult) string {
	var b strings.Builder
	for _, r := range results {
		if !r.Passed {
			fmt.Fprintf(&b, "Gate `%s` (`%s`) FAILED:\n%s\n\n", r.Name, r.Command, r.Output)
		}
	}
	return b.String()
}

// refreshRunChecklist re-reads plan.md from the worktree and updates checklist state.
func (m *model) refreshRunChecklist() {
	if m.run == nil || m.cfg.Orchestrator == nil {
		return
	}
	wm := m.cfg.Orchestrator.Worktree()
	if wm == nil {
		return
	}
	if merged := checklistFromWorktrees(m.runChecklistPaths(wm), m.run.specName); len(merged) > 0 {
		m.run.checklist = merged
	}
}

// runChecklistPaths lists every worktree whose plan.md counts toward progress:
// the one the run owns, plus each parallel agent's own tree.
func (m *model) runChecklistPaths(wm *subagent.WorktreeManager) []string {
	paths := []string{wm.PathFor(m.run.worktreeAgentID)}
	for _, pa := range m.run.parallel {
		if p := wm.PathFor(pa.agentID); p != "" {
			paths = append(paths, p)
		}
	}
	return paths
}

// checklistFromWorktrees reads plan.md from each worktree and unions the
// results. In parallel mode each agent ticks only its OWN slices in its OWN
// worktree, so no single tree ever shows the plan complete; reading just one
// would leave the other agent's slices permanently unchecked and the Verifier
// permanently failing. A slice counts as done if any worktree records it done.
//
// Worktrees whose plan.md is missing, unreadable or a different length are
// skipped rather than allowed to overwrite a good view — a truncated or
// rewritten plan must not silently un-tick completed work.
func checklistFromWorktrees(paths []string, specName string) []ChecklistStep {
	var merged []ChecklistStep
	for _, wtPath := range paths {
		if wtPath == "" {
			continue
		}
		view := parsePlanChecklistFrom(wtPath, specName)
		if len(view) == 0 {
			continue
		}
		if merged == nil {
			merged = view
			continue
		}
		if len(view) != len(merged) {
			continue
		}
		for i := range view {
			if view[i].Done {
				merged[i].Done = true
			}
		}
	}
	return merged
}

// waitForRunEvents returns a tea.Cmd to consume the next event from the running subagent.
// It looks up the events channel via the orchestrator using the stored agent ID.
func (m *model) waitForRunEvents() tea.Cmd {
	if m.run == nil {
		return nil
	}

	// Parallel mode: wait on all active agent channels.
	if m.run.isParallel() {
		return m.waitForParallelRunEvents()
	}

	// Single-agent mode.
	if m.run.agentID == "" || m.run.events == nil {
		return nil
	}
	return waitForRunAgent(m.run.events, m.run.agentID)
}

// waitForParallelRunEvents returns a tea.Cmd that listens on all active
// parallel agent event channels simultaneously using a select-style fan-in.
func (m *model) waitForParallelRunEvents() tea.Cmd {
	// Collect active agents.
	var active []*parallelAgent
	for _, pa := range m.run.parallel {
		if !pa.done && pa.events != nil {
			active = append(active, pa)
		}
	}
	if len(active) == 0 {
		return nil
	}

	// Fan-in: start one goroutine per active agent, first result wins.
	type result struct {
		msg tea.Msg
	}
	ch := make(chan result, len(active))
	for _, pa := range active {
		go func(pa *parallelAgent) {
			ev, ok := <-pa.events
			if !ok {
				ch <- result{msg: runAgentDoneMsg{agentID: pa.agentID}}
			} else if ev.Type == "run_done" {
				ch <- result{msg: runAgentDoneMsg{agentID: pa.agentID, status: ev.Status}}
			} else {
				ch <- result{msg: runAgentEventMsg{event: ev, agentID: pa.agentID}}
			}
		}(pa)
	}
	return func() tea.Msg {
		r := <-ch
		return r.msg
	}
}

// Gate represents a validation command parsed from the ## Gates section of PROMPT.md.
type Gate struct {
	Name    string
	Command string
}

// parseGates extracts gate entries from the ## Gates section of a PROMPT.md.
// Supports formats:
//   - **name**: `command`
//   - name: `command`
//
// Returns an empty slice if no Gates section is found.
func parseGates(promptMD string) []Gate {
	lines := strings.Split(promptMD, "\n")

	// Find the ## Gates section.
	inGates := false
	var gates []Gate

	// Match: - **name**: `command` or - name: `command`
	gateRe := regexp.MustCompile(`^-\s+\*{0,2}([^*:]+?)\*{0,2}\s*:\s*` + "`" + `([^` + "`" + `]+)` + "`")

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "## Gates") {
			inGates = true
			continue
		}

		// Stop at the next heading.
		if inGates && strings.HasPrefix(trimmed, "## ") {
			break
		}

		if !inGates {
			continue
		}

		matches := gateRe.FindStringSubmatch(trimmed)
		if matches != nil {
			gates = append(gates, Gate{
				Name:    strings.TrimSpace(matches[1]),
				Command: strings.TrimSpace(matches[2]),
			})
		}
	}

	return gates
}

// readWindowLines mirrors the read tool's default line window
// (defaultReadLimit in internal/tools/read.go). A spec file longer than this
// is returned a page at a time, so a worker reading it may see only part.
const readWindowLines = 2000

// windowedSpecFiles are the spec documents a worker reads for slice detail.
// PROMPT.md is excluded: /run loads it with os.ReadFile and embeds it whole in
// the coordinator prompt, so the read window never applies to it.
var windowedSpecFiles = []string{"plan.md", "design.md"}

// oversizedSpecFile names a spec document that exceeds the read window.
type oversizedSpecFile struct {
	Name  string
	Lines int
}

// oversizedSpecFiles returns the spec documents that exceed the read window,
// in the order listed by windowedSpecFiles. Missing or unreadable files are
// skipped — a spec without a design.md is normal, not an error.
func oversizedSpecFiles(workDir, specName string) []oversizedSpecFile {
	var out []oversizedSpecFile
	for _, name := range windowedSpecFiles {
		content, err := os.ReadFile(filepath.Join(workDir, "specs", specName, name))
		if err != nil {
			continue
		}
		if n := countLines(content); n > readWindowLines {
			out = append(out, oversizedSpecFile{Name: name, Lines: n})
		}
	}
	return out
}

// countLines counts the lines in a file, not counting a trailing newline as a
// line of its own — the same off-by-one the read tool was corrected for.
func countLines(content []byte) int {
	if len(content) == 0 {
		return 0
	}
	n := bytes.Count(content, []byte("\n"))
	if !bytes.HasSuffix(content, []byte("\n")) {
		n++
	}
	return n
}

// formatOversizedSpecWarning explains what the length costs and what to do,
// rather than only reporting the number.
func formatOversizedSpecWarning(files []oversizedSpecFile) string {
	var b strings.Builder
	b.WriteString("**Spec exceeds the read window** — workers will page through it:\n\n")
	for _, f := range files {
		fmt.Fprintf(&b, "- `%s`: %d lines (window is %d)\n", f.Name, f.Lines, readWindowLines)
	}
	b.WriteString("\nA worker sent to read its slice may act on a partial view. ")
	b.WriteString("Prefer splitting the feature into sequential specs over shrinking the prose — ")
	b.WriteString("a plan too long to read in one call is also too long for one Coordinator.\n")
	b.WriteString("Running anyway.\n")
	return b.String()
}

// readPromptMD reads the PROMPT.md file from a spec directory.
func readPromptMD(workDir, specName string) (string, error) {
	promptPath := filepath.Join(workDir, "specs", specName, "PROMPT.md")
	content, err := os.ReadFile(promptPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("PROMPT.md not found at %s — has the /plan session completed?", promptPath)
		}
		return "", fmt.Errorf("failed to read PROMPT.md: %w", err)
	}
	return string(content), nil
}

// parsePlanChecklist reads plan.md from the spec directory and extracts checklist steps.
// It looks for lines matching "### Slice N: Title" patterns.
func parsePlanChecklist(workDir, specName string) []ChecklistStep {
	planPath := filepath.Join(workDir, "specs", specName, "plan.md")
	content, err := os.ReadFile(planPath)
	if err != nil {
		return nil
	}
	return extractChecklist(string(content))
}

// parsePlanChecklistFrom reads plan.md from an arbitrary directory (e.g. worktree).
func parsePlanChecklistFrom(dir, specName string) []ChecklistStep {
	planPath := filepath.Join(dir, "specs", specName, "plan.md")
	content, err := os.ReadFile(planPath)
	if err != nil {
		return nil
	}
	return extractChecklist(string(content))
}

// sliceHeadingRe matches "### Slice N: Title" headings in plan.md.
var sliceHeadingRe = regexp.MustCompile(`^###\s+(?:Slice\s+(\d+):?\s*)?(.+)`)

// checkboxRe matches "- [ ] Step N:" or "- [x] Step N:" lines.
var checkboxRe = regexp.MustCompile(`^-\s+\[([ xX])\]\s+(.+)`)

// extractChecklist parses plan.md content to extract steps.
// It first looks for "- [ ] / - [x]" checkbox lines. If none found,
// falls back to "### Slice N: Title" headings.
func extractChecklist(content string) []ChecklistStep {
	lines := strings.Split(content, "\n")

	// Try checkbox format first.
	var steps []ChecklistStep
	for _, line := range lines {
		m := checkboxRe.FindStringSubmatch(strings.TrimSpace(line))
		if m != nil {
			title := m[2]
			// Truncate long titles
			if len(title) > 40 {
				title = title[:37] + "..."
			}
			steps = append(steps, ChecklistStep{
				Title: title,
				Done:  m[1] == "x" || m[1] == "X",
			})
		}
	}
	if len(steps) > 0 {
		return steps
	}

	// Fallback: extract from ### Slice headings.
	for _, line := range lines {
		m := sliceHeadingRe.FindStringSubmatch(strings.TrimSpace(line))
		if m != nil {
			title := strings.TrimSpace(m[2])
			if len(title) > 40 {
				title = title[:37] + "..."
			}
			steps = append(steps, ChecklistStep{
				Title: title,
				Done:  false,
			})
		}
	}
	return steps
}

// listAvailableSpecs recursively scans the specs/ directory for subdirectories
// containing PROMPT.md. Returns a sorted list of spec names (relative paths
// from specs/, e.g. "skills/skills-audit" or "simple-ollama-test").
func listAvailableSpecs(workDir string) ([]string, error) {
	specsDir := filepath.Join(workDir, "specs")

	if _, err := os.Stat(specsDir); os.IsNotExist(err) {
		return nil, nil
	}

	var specs []string
	err := filepath.WalkDir(specsDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable dirs
		}
		if !d.IsDir() {
			return nil
		}
		promptPath := filepath.Join(path, "PROMPT.md")
		if _, statErr := os.Stat(promptPath); statErr == nil {
			rel, _ := filepath.Rel(specsDir, path)
			// A spec name is an identifier the user types back into
			// "/run <spec-name>", not a path to display: keep it slash-form on
			// every OS. filepath.Join accepts it either way.
			specs = append(specs, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to walk specs directory: %w", err)
	}

	sort.Strings(specs)
	return specs, nil
}
