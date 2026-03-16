package tui

import (
	"fmt"
	"os"
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
	specName   string
	promptMD   string
	gates      []Gate
	agentID    string
	phase      string // "running", "gating", "merging", "done", "failed"
	retries    int
	maxRetries int
	events     <-chan subagent.Event // subagent event channel
}

// --- Message types for /run streaming ---

// runAgentEventMsg wraps a subagent event for the TUI update loop.
type runAgentEventMsg struct {
	event subagent.Event
}

// runAgentDoneMsg signals that the subagent has finished (events channel closed).
type runAgentDoneMsg struct{}

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

	// Spawn task subagent.
	useWorktree := true
	events, agentID, err := m.cfg.Orchestrator.Spawn(m.ctx, subagent.AgentInput{
		Type:     "task",
		Prompt:   prompt,
		Worktree: &useWorktree,
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
		maxRetries: 3,
		events:     events,
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
		content: fmt.Sprintf("**Running spec `%s`** — agent `%s` spawned in worktree\nGates: %s",
			specName, agentID, gateInfo),
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
func (m *model) handleRunAgentDone() (tea.Model, tea.Cmd) {
	m.running = false
	m.activeTool = ""
	m.streaming = ""
	m.thinking = ""

	if m.run != nil {
		m.run.phase = "done"
		m.messages = append(m.messages, message{
			role:    "assistant",
			content: fmt.Sprintf("**Spec `%s` completed** — agent `%s` finished.", m.run.specName, m.run.agentID),
		})
		// TODO: Step 6 will add gate validation here instead of marking done.
	}

	return m, nil
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
