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

// runState tracks the state of a /run command execution.
type runState struct {
	specName    string
	promptMD    string
	gates       []Gate
	agentID     string
	phase       string // "running", "gating", "merging", "done", "failed"
	retries     int
	maxRetries  int
	events      <-chan subagent.Event // subagent event channel
	gateOutput  string                // formatted gate failure output (for retry prompts)
	gateResults []GateResult          // latest gate results (for summary report)
	startTime   time.Time             // when the run started
}

// --- Message types for /run streaming ---

// runAgentEventMsg wraps a subagent event for the TUI update loop.
type runAgentEventMsg struct {
	event subagent.Event
}

// runAgentDoneMsg signals that the subagent has finished (events channel closed).
type runAgentDoneMsg struct{}

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
	output string
	err    error
}

// buildRunPrompt constructs the augmented prompt for the task subagent.
func buildRunPrompt(specName, promptMD string) string {
	var b strings.Builder
	b.WriteString(promptMD)
	b.WriteString("\n\n## Execution Instructions\n")
	b.WriteString("- Follow the plan in specs/")
	b.WriteString(specName)
	b.WriteString("/plan.md step by step\n")
	b.WriteString("- After completing each step, update the plan.md checklist: change `- [ ] Step N:` to `- [x] Step N:`\n")
	b.WriteString("- Run tests after each step to verify correctness\n")
	b.WriteString("- Work in the current directory (worktree)\n")
	return b.String()
}

// handleRunCommand handles the /run <spec-name> slash command.
func (m *model) handleRunCommand(args []string) (tea.Model, tea.Cmd) {
	if len(args) == 0 {
		specs, _ := listAvailableSpecs(m.cfg.WorkDir)
		msg := "Usage: `/run <spec-name>`\n\nExecutes a spec's PROMPT.md using an isolated task agent."
		if len(specs) > 0 {
			msg += "\n\n**Available specs:** " + strings.Join(specs, ", ")
		}
		m.messages = append(m.messages, message{role: "assistant", content: msg})
		return m, nil
	}

	if m.cfg.Orchestrator == nil {
		m.messages = append(m.messages, message{
			role:    "assistant",
			content: "Subagent system not available. Cannot run specs.",
		})
		return m, nil
	}

	specName := args[0]

	// Read PROMPT.md.
	promptMD, err := readPromptMD(m.cfg.WorkDir, specName)
	if err != nil {
		specs, _ := listAvailableSpecs(m.cfg.WorkDir)
		errMsg := fmt.Sprintf("Error: %v", err)
		if len(specs) > 0 {
			errMsg += "\n\n**Available specs:** " + strings.Join(specs, ", ")
		}
		m.messages = append(m.messages, message{role: "assistant", content: errMsg})
		return m, nil
	}

	// Parse gates.
	gates := parseGates(promptMD)

	// Build augmented prompt.
	prompt := buildRunPrompt(specName, promptMD)

	// Spawn task subagent with SkipCleanup so we can run gates before merge.
	useWorktree := true
	events, agentID, err := m.cfg.Orchestrator.Spawn(m.ctx, subagent.AgentInput{
		Type:        "task",
		Prompt:      prompt,
		Worktree:    &useWorktree,
		SkipCleanup: true,
	})
	if err != nil {
		m.messages = append(m.messages, message{
			role:    "assistant",
			content: fmt.Sprintf("Failed to spawn task agent: %v", err),
		})
		return m, nil
	}

	// Initialize run state.
	m.run = &runState{
		specName:   specName,
		promptMD:   promptMD,
		gates:      gates,
		agentID:    agentID,
		phase:      "running",
		maxRetries: 10,
		events:     events,
		startTime:  time.Now(),
	}

	// Show run start message.
	gateInfo := "none"
	if len(gates) > 0 {
		names := make([]string, len(gates))
		for i, g := range gates {
			names[i] = g.Name
		}
		gateInfo = strings.Join(names, ", ")
	}
	m.messages = append(m.messages, message{
		role: "assistant",
		content: fmt.Sprintf("**Running spec `%s`** [cycle 1/%d] — agent `%s` spawned in worktree\nGates: %s",
			specName, m.run.maxRetries, agentID, gateInfo),
	})

	// Add empty assistant message for streaming.
	m.messages = append(m.messages, message{role: "assistant", content: ""})
	m.streaming = ""
	m.thinking = ""
	m.running = true
	m.scroll = 0

	// Start consuming events from the subagent.
	return m, waitForRunAgent(events)
}

// waitForRunAgent returns a tea.Cmd that reads the next event from the subagent channel.
func waitForRunAgent(events <-chan subagent.Event) tea.Cmd {
	if events == nil {
		return nil
	}
	return func() tea.Msg {
		ev, ok := <-events
		if !ok {
			return runAgentDoneMsg{}
		}
		return runAgentEventMsg{event: ev}
	}
}

// handleRunAgentEvent processes a streaming event from the /run subagent.
func (m *model) handleRunAgentEvent(msg runAgentEventMsg) (tea.Model, tea.Cmd) {
	ev := msg.event

	switch ev.Type {
	case "text_delta":
		m.streaming += ev.Content
		// Update the last assistant message with accumulated text.
		for i := len(m.messages) - 1; i >= 0; i-- {
			if m.messages[i].role == "assistant" {
				m.messages[i].content = m.streaming
				break
			}
		}
		m.scroll = 0
		// Trace.
		if len(m.traceLog) > 0 && m.traceLog[len(m.traceLog)-1].kind == "llm" {
			m.traceLog[len(m.traceLog)-1].detail = m.streaming
		} else {
			m.traceLog = append(m.traceLog, traceEntry{
				time: time.Now(), kind: "llm", summary: "agent response", detail: ev.Content,
			})
		}

	case "tool_call":
		m.activeTool = ev.Content
		m.toolStart = time.Now()
		m.traceLog = append(m.traceLog, traceEntry{
			time: time.Now(), kind: "tool_call", summary: fmt.Sprintf(">>> %s", ev.Content),
		})
		m.messages = append(m.messages, message{
			role: "tool", tool: ev.Content,
		})

	case "tool_result":
		m.activeTool = ""
		m.traceLog = append(m.traceLog, traceEntry{
			time: time.Now(), kind: "tool_result", summary: "<<< result",
			detail: ev.Content,
		})
		// Update the last tool message with the result.
		for i := len(m.messages) - 1; i >= 0; i-- {
			if m.messages[i].role == "tool" && m.messages[i].content == "" {
				m.messages[i].content = toolResultSummary(ev.Content)
				break
			}
		}

	case "message_start":
		// New message from the agent — add an empty assistant placeholder.
		m.streaming = ""
		m.messages = append(m.messages, message{role: "assistant", content: ""})

	case "message_end":
		// Message completed — reset streaming accumulator for the next message.
		m.streaming = ""

	case "error":
		m.messages = append(m.messages, message{
			role:    "assistant",
			content: fmt.Sprintf("Agent error: %s", ev.Error),
		})
		m.traceLog = append(m.traceLog, traceEntry{
			time: time.Now(), kind: "error", summary: "agent error", detail: ev.Error,
		})
	}

	// Keep consuming events from the subagent.
	return m, m.waitForRunEvents()
}

// handleRunAgentDone is called when the subagent events channel closes.
// It transitions to gate validation if gates are defined, or directly to merge.
func (m *model) handleRunAgentDone() (tea.Model, tea.Cmd) {
	m.running = false
	m.activeTool = ""
	m.streaming = ""
	m.thinking = ""

	if m.run == nil {
		return m, nil
	}

	m.messages = append(m.messages, message{
		role:    "assistant",
		content: fmt.Sprintf("**Agent `%s` finished** — validating gates...", m.run.agentID),
	})

	// If no gates, skip directly to merge.
	if len(m.run.gates) == 0 {
		m.run.phase = "merging"
		m.messages = append(m.messages, message{
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

	worktreePath := wm.PathFor(m.run.agentID)
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
		summary.WriteString(fmt.Sprintf("- **%s** (`%s`): %s\n", r.Name, r.Command, status))
		if !r.Passed && r.Output != "" {
			// Include truncated output for failed gates.
			out := r.Output
			if len(out) > 500 {
				out = out[:500] + "...(truncated)"
			}
			summary.WriteString(fmt.Sprintf("  ```\n  %s\n  ```\n", strings.TrimSpace(out)))
		}
	}

	m.messages = append(m.messages, message{
		role:    "assistant",
		content: summary.String(),
	})

	if msg.passed {
		// All gates passed — proceed to merge.
		m.run.phase = "merging"
		m.messages = append(m.messages, message{
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

		m.messages = append(m.messages, message{
			role: "assistant",
			content: fmt.Sprintf("**Gate failed** — cycle %d/%d (retry %d) in worktree `%s`...",
				m.run.retries+1, m.run.maxRetries, m.run.retries, wtPath),
		})

		retryPrompt := buildRetryPrompt(m.run.specName, m.run.promptMD, m.run.gateOutput)

		// Spawn a new agent in the same worktree directory.
		events, agentID, err := m.cfg.Orchestrator.Spawn(m.ctx, subagent.AgentInput{
			Type:        "task",
			Prompt:      retryPrompt,
			WorkDir:     wtPath,
			SkipCleanup: true,
		})
		if err != nil {
			m.run.phase = "failed"
			m.messages = append(m.messages, message{
				role:    "assistant",
				content: fmt.Sprintf("Failed to spawn retry agent: %v", err),
			})
			return m, nil
		}

		m.run.agentID = agentID
		m.run.phase = "running"
		m.run.events = events

		// Add empty assistant message for streaming.
		m.messages = append(m.messages, message{role: "assistant", content: ""})
		m.streaming = ""
		m.thinking = ""
		m.running = true
		m.scroll = 0

		return m, waitForRunAgent(events)
	}

	// Retries exhausted.
	m.run.phase = "failed"

	wm := m.cfg.Orchestrator.Worktree()
	wtPath := ""
	if wm != nil {
		wtPath = wm.PathFor(m.run.agentID)
	}

	m.messages = append(m.messages, message{
		role: "assistant",
		content: fmt.Sprintf("**Gate validation failed** for spec `%s` after %d retries.\nWorktree preserved at: `%s`\nInspect manually and fix the issues.",
			m.run.specName, m.run.maxRetries, wtPath),
	})

	// Write summary report for gate failure.
	if report, err := m.writeRunSummary("gate_failed"); err == nil {
		m.messages = append(m.messages, message{
			role:    "assistant",
			content: fmt.Sprintf("Summary report: `%s`", report),
		})
	}

	return m, nil
}

// mergeWorktreeCmd returns a tea.Cmd that merges the worktree branch and cleans up.
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

	agentID := m.run.agentID
	return func() tea.Msg {
		out, err := wm.MergeBack(agentID)
		if err != nil {
			return runMergeResultMsg{output: out, err: err}
		}
		// Cleanup worktree after successful merge.
		_ = wm.Cleanup(agentID)
		return runMergeResultMsg{output: out}
	}
}

// handleRunMergeResult processes the merge result.
func (m *model) handleRunMergeResult(msg runMergeResultMsg) (tea.Model, tea.Cmd) {
	if m.run == nil {
		return m, nil
	}

	if msg.err != nil {
		m.run.phase = "failed"

		wm := m.cfg.Orchestrator.Worktree()
		wtPath := ""
		if wm != nil {
			wtPath = wm.PathFor(m.run.agentID)
		}

		m.messages = append(m.messages, message{
			role: "assistant",
			content: fmt.Sprintf("**Merge failed** for spec `%s`: %v\nWorktree preserved at: `%s`",
				m.run.specName, msg.err, wtPath),
		})

		// Write summary report for merge failure.
		if report, err := m.writeRunSummary("merge_failed"); err == nil {
			m.messages = append(m.messages, message{
				role:    "assistant",
				content: fmt.Sprintf("Summary report: `%s`", report),
			})
		}
		return m, nil
	}

	m.run.phase = "done"
	m.messages = append(m.messages, message{
		role:    "assistant",
		content: fmt.Sprintf("**Spec `%s` completed** — changes merged successfully.", m.run.specName),
	})

	// Write summary report.
	if report, err := m.writeRunSummary("completed"); err != nil {
		m.messages = append(m.messages, message{
			role:    "assistant",
			content: fmt.Sprintf("Warning: failed to write summary report: %v", err),
		})
	} else {
		m.messages = append(m.messages, message{
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
			b.WriteString(fmt.Sprintf("Gate `%s` (`%s`) FAILED:\n%s\n\n", r.Name, r.Command, r.Output))
		}
	}
	return b.String()
}

// waitForRunEvents returns a tea.Cmd to consume the next event from the running subagent.
// It looks up the events channel via the orchestrator using the stored agent ID.
func (m *model) waitForRunEvents() tea.Cmd {
	if m.run == nil || m.run.agentID == "" {
		return nil
	}
	// We reuse the orchestrator's event channel. Since Spawn() already returned
	// the channel and we passed it to the initial waitForRunAgent, we need to
	// keep a reference. Store it on runState.
	if m.run.events == nil {
		return nil
	}
	return waitForRunAgent(m.run.events)
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

// listAvailableSpecs scans the specs/ directory for subdirectories containing PROMPT.md.
// Returns a sorted list of spec names.
func listAvailableSpecs(workDir string) ([]string, error) {
	specsDir := filepath.Join(workDir, "specs")

	entries, err := os.ReadDir(specsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read specs directory: %w", err)
	}

	var specs []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		promptPath := filepath.Join(specsDir, entry.Name(), "PROMPT.md")
		if _, err := os.Stat(promptPath); err == nil {
			specs = append(specs, entry.Name())
		}
	}

	sort.Strings(specs)
	return specs, nil
}
