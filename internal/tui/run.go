package tui

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

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
	slices  []int // which checklist indices this agent handles
	done    bool  // true when events channel has closed
}

// runState tracks the state of a /run command execution.
type runState struct {
	specName    string
	promptMD    string
	gates       []Gate
	agentID     string
	phase       string // "running", "gating", "merging", "done", "failed"
	retries     int
	maxRetries  int
	events      <-chan subagent.Event // subagent event channel (single-agent mode)
	gateOutput  string                // formatted gate failure output (for retry prompts)
	gateResults []GateResult          // latest gate results (for summary report)
	startTime   time.Time             // when the run started
	checklist   []ChecklistStep       // parsed from plan.md
	parallel    []*parallelAgent      // parallel agents (nil for single-agent mode)
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

// --- Message types for /run streaming ---

// runAgentEventMsg wraps a subagent event for the TUI update loop.
type runAgentEventMsg struct {
	event   subagent.Event
	agentID string // which agent emitted this event (for parallel mode)
}

// runAgentDoneMsg signals that a subagent has finished (events channel closed).
type runAgentDoneMsg struct {
	agentID string // which agent finished (for parallel mode)
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

// buildRunPrompt constructs the augmented prompt for the task subagent.
// If the plan.md uses heading-only format (no checkboxes), it injects a
// checklist so the agent can mark steps as completed.
func buildRunPrompt(specName, promptMD string, checklist []ChecklistStep) string {
	var b strings.Builder
	b.WriteString(promptMD)
	b.WriteString("\n\n## Execution Instructions\n")
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
		specs, _ := listAvailableSpecs(m.cfg.WorkDir)
		msg := "Usage: `/run <spec-name> [--parallel]`\n\nExecutes a spec's PROMPT.md using an isolated task agent.\nUse `--parallel` to split independent slices across 2 agents."
		if len(specs) > 0 {
			msg += "\n\n" + formatAvailableRunSpecsTable(specs)
		}
		m.chatModel.Messages = append(m.chatModel.Messages, message{role: "assistant", content: msg})
		return m, nil
	}

	if m.cfg.Orchestrator == nil {
		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role:    "assistant",
			content: "Subagent system not available. Cannot run specs.",
		})
		return m, nil
	}

	// Parse args: spec-name [--parallel|-p]
	var specName string
	parallel := false
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
	if specName == "" {
		m.chatModel.Messages = append(m.chatModel.Messages, message{role: "assistant", content: "Missing spec name."})
		return m, nil
	}

	// Read PROMPT.md.
	promptMD, err := readPromptMD(m.cfg.WorkDir, specName)
	if err != nil {
		specs, _ := listAvailableSpecs(m.cfg.WorkDir)
		errMsg := fmt.Sprintf("Error: %v", err)
		if len(specs) > 0 {
			errMsg += "\n\n" + formatAvailableRunSpecsTable(specs)
		}
		m.chatModel.Messages = append(m.chatModel.Messages, message{role: "assistant", content: errMsg})
		return m, nil
	}

	// Parse gates.
	gates := parseGates(promptMD)

	// Parse plan.md checklist for sidebar display.
	checklist := parsePlanChecklist(m.cfg.WorkDir, specName)

	// Format gate info for display.
	gateInfo := "none"
	if len(gates) > 0 {
		names := make([]string, len(gates))
		for i, g := range gates {
			names[i] = g.Name
		}
		gateInfo = strings.Join(names, ", ")
	}

	// Parallel mode: split slices across 2 agents.
	if parallel && len(checklist) >= 2 {
		return m.handleRunParallel(specName, promptMD, gates, checklist, gateInfo)
	}

	// Single-agent mode.
	prompt := buildRunPrompt(specName, promptMD, checklist)

	events, agentID, err := m.cfg.Orchestrator.SpawnWithInput(m.ctx, subagent.AgentInput{
		Type:         "task",
		Prompt:       prompt,
		Worktree:     new(true),
		WorktreeName: runWorktreeName(specName, ""),
		SkipCleanup:  true,
	})
	if err != nil {
		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role:    "assistant",
			content: fmt.Sprintf("Failed to spawn task agent: %v", err),
		})
		return m, nil
	}

	m.run = &runState{
		specName:   specName,
		promptMD:   promptMD,
		gates:      gates,
		agentID:    agentID,
		phase:      "running",
		maxRetries: 10,
		events:     events,
		startTime:  time.Now(),
		checklist:  checklist,
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
	})
	if err != nil {
		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role:    "assistant",
			content: fmt.Sprintf("Failed to spawn agent 2 (agent 1 `%s` is running): %v", agentID1, err),
		})
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
		specName:   specName,
		promptMD:   promptMD,
		gates:      gates,
		agentID:    agentID1, // primary agent for fallback
		phase:      "running",
		maxRetries: 10,
		startTime:  time.Now(),
		checklist:  checklist,
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
	b.WriteString("\n\n## Execution Instructions (Parallel Mode)\n")
	b.WriteString("You are ONE OF TWO agents working on this spec in parallel.\n")
	b.WriteString("You are responsible for implementing ONLY these slices:\n\n")
	for i := from; i < to; i++ {
		fmt.Fprintf(&b, "- Slice %d: %s\n", i+1, checklist[i].Title)
	}
	b.WriteString("\nDo NOT implement slices assigned to the other agent.\n")
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

	case "tool_call":
		m.statusModel.ActiveTool = ev.Content
		m.statusModel.ToolStart = time.Now()
		m.chatModel.TraceLog = append(m.chatModel.TraceLog, traceEntry{
			time: time.Now(), kind: "tool_call", summary: fmt.Sprintf(">>> %s", ev.Content),
		})
		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role: "tool", tool: ev.Content,
		})

	case "tool_result":
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

	case "message_start":
		// New message from the agent — add an empty assistant placeholder.
		m.chatModel.Streaming = ""
		m.chatModel.Messages = append(m.chatModel.Messages, message{role: "assistant", content: ""})

	case "message_end":
		// Message completed — reset streaming accumulator for the next message.
		m.chatModel.Streaming = ""

	case "error":
		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role:    "assistant",
			content: fmt.Sprintf("Agent error: %s", ev.Error),
		})
		m.chatModel.TraceLog = append(m.chatModel.TraceLog, traceEntry{
			time: time.Now(), kind: "error", summary: "agent error", detail: ev.Error,
		})
	}

	// Keep consuming events from the subagent.
	return m, m.waitForRunEvents()
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

	m.chatModel.Messages = append(m.chatModel.Messages, message{
		role:    "assistant",
		content: "**All agents finished** — validating gates...",
	})

	// If no gates, skip directly to merge.
	if len(m.run.gates) == 0 {
		m.run.phase = "merging"
		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role:    "assistant",
			content: "No gates defined — proceeding to merge.",
		})
		return m, m.mergeWorktreeCmd()
	}

	// Run gate validation.
	m.run.phase = "gating"
	return m, m.runGatesCmd()
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

	// In parallel mode, use the first agent's worktree for gate validation.
	agentID := m.run.agentID
	if m.run.isParallel() {
		agentID = m.run.parallel[0].agentID
	}
	worktreePath := wm.PathFor(agentID)
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
		cmd := exec.CommandContext(ctx, "sh", "-c", gate.Command)
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
		// All gates passed — proceed to merge.
		m.run.phase = "merging"
		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role:    "assistant",
			content: "All gates passed — merging worktree branch...",
		})
		return m, m.mergeWorktreeCmd()
	}

	// Gates failed — attempt retry or give up.
	m.run.gateOutput = formatGateFailures(msg.results)

	if m.run.retries < m.run.maxRetries {
		// Retry: re-spawn agent in the same worktree with failure context.
		m.run.retries++
		m.run.phase = "retrying"

		wm := m.cfg.Orchestrator.Worktree()
		wtPath := ""
		if wm != nil {
			wtPath = wm.PathFor(m.run.agentID)
		}

		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role: "assistant",
			content: fmt.Sprintf("**Gate failed** — cycle %d/%d (retry %d) in worktree `%s`...",
				m.run.retries+1, m.run.maxRetries, m.run.retries, wtPath),
		})

		retryPrompt := buildRetryPrompt(m.run.specName, m.run.promptMD, m.run.gateOutput)

		// Spawn a new agent in the same worktree directory.
		events, agentID, err := m.cfg.Orchestrator.SpawnWithInput(m.ctx, subagent.AgentInput{
			Type:        "task",
			Prompt:      retryPrompt,
			WorkDir:     wtPath,
			SkipCleanup: true,
		})
		if err != nil {
			m.run.phase = "failed"
			m.chatModel.Messages = append(m.chatModel.Messages, message{
				role:    "assistant",
				content: fmt.Sprintf("Failed to spawn retry agent: %v", err),
			})
			return m, nil
		}

		m.run.agentID = agentID
		m.run.phase = "running"
		m.run.events = events

		// Add empty assistant message for streaming.
		m.chatModel.Messages = append(m.chatModel.Messages, message{role: "assistant", content: ""})
		m.chatModel.Streaming = ""
		m.chatModel.Thinking = ""
		m.running = true
		m.chatModel.Scroll = 0

		return m, waitForRunAgent(events, agentID)
	}

	// Retries exhausted.
	m.run.phase = "failed"

	wm := m.cfg.Orchestrator.Worktree()
	wtPath := ""
	if wm != nil {
		wtPath = wm.PathFor(m.run.agentID)
	}

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

	// Collect agent IDs to merge (parallel or single).
	var agentIDs []string
	if m.run.isParallel() {
		for _, pa := range m.run.parallel {
			agentIDs = append(agentIDs, pa.agentID)
		}
	} else {
		agentIDs = []string{m.run.agentID}
	}

	return func() tea.Msg {
		var allOutput strings.Builder
		for _, aid := range agentIDs {
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
			wm := m.cfg.Orchestrator.Worktree()
			if wm != nil {
				targetAgentID := m.run.agentID
				if msg.failedAgentID != "" {
					targetAgentID = msg.failedAgentID
				}
				wtPath = wm.PathFor(targetAgentID)
			}
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

	// Metadata.
	b.WriteString("## Metadata\n\n")
	b.WriteString("| Field | Value |\n")
	b.WriteString("|-------|-------|\n")
	fmt.Fprintf(&b, "| Spec | `%s` |\n", rs.specName)
	fmt.Fprintf(&b, "| Agent | `%s` |\n", rs.agentID)
	fmt.Fprintf(&b, "| Outcome | **%s** |\n", outcome)
	fmt.Fprintf(&b, "| Retries | %d / %d |\n", rs.retries, rs.maxRetries)
	if !rs.startTime.IsZero() {
		fmt.Fprintf(&b, "| Started | %s |\n", rs.startTime.Format(time.RFC3339))
		fmt.Fprintf(&b, "| Duration | %s |\n", time.Since(rs.startTime).Truncate(time.Second))
	}
	b.WriteString("\n")

	// Gate results.
	b.WriteString("## Gates\n\n")
	if len(rs.gateResults) == 0 && len(rs.gates) == 0 {
		b.WriteString("No gates defined.\n\n")
	} else if len(rs.gateResults) == 0 {
		b.WriteString("Gates were defined but not executed.\n\n")
		for _, g := range rs.gates {
			fmt.Fprintf(&b, "- **%s**: `%s`\n", g.Name, g.Command)
		}
		b.WriteString("\n")
	} else {
		allPassed := true
		for _, r := range rs.gateResults {
			status := "PASS"
			if !r.Passed {
				status = "FAIL"
				allPassed = false
			}
			fmt.Fprintf(&b, "- **%s** (`%s`): **%s**\n", r.Name, r.Command, status)
			if !r.Passed && r.Output != "" {
				out := strings.TrimSpace(r.Output)
				if len(out) > 1000 {
					out = out[:1000] + "\n...(truncated)"
				}
				fmt.Fprintf(&b, "  ```\n  %s\n  ```\n", out)
			}
		}
		b.WriteString("\n")
		if allPassed {
			b.WriteString("All gates **passed**.\n\n")
		} else {
			b.WriteString("Some gates **failed**.\n\n")
		}
	}

	// Outcome details.
	b.WriteString("## Result\n\n")
	switch outcome {
	case "completed":
		b.WriteString("All gates passed and changes were merged successfully.\n")
	case "gate_failed":
		fmt.Fprintf(&b, "Gate validation failed after %d retries. Worktree preserved for manual inspection.\n", rs.retries)
	case "merge_failed":
		b.WriteString("Gates passed but merge into the main branch failed. Worktree preserved for manual resolution.\n")
	default:
		fmt.Fprintf(&b, "Run ended with status: %s\n", outcome)
	}

	return b.String()
}

// buildRetryPrompt constructs the prompt for a retry agent after gate failure.
func buildRetryPrompt(specName, promptMD, gateOutput string) string {
	var b strings.Builder
	b.WriteString("The previous implementation attempt failed gate validation.\n\n")
	b.WriteString("## Gate Failures\n")
	b.WriteString(gateOutput)
	b.WriteString("\n## Original Task\n")
	b.WriteString(promptMD)
	b.WriteString("\n\n## Instructions\n")
	b.WriteString("Fix the issues identified by the gate failures. The failing commands were run in the worktree.\n")
	b.WriteString("Continue working in the current directory. Run the failing commands yourself to verify fixes.\n")
	b.WriteString("Update specs/")
	b.WriteString(specName)
	b.WriteString("/plan.md checklist as you complete steps.\n")
	return b.String()
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
	wtPath := wm.PathFor(m.run.agentID)
	if wtPath == "" {
		return
	}
	if updated := parsePlanChecklistFrom(wtPath, m.run.specName); len(updated) > 0 {
		m.run.checklist = updated
	}
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
			specs = append(specs, rel)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to walk specs directory: %w", err)
	}

	sort.Strings(specs)
	return specs, nil
}
