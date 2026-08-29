package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/runner"
	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"

	"github.com/dimetron/pi-go/internal/sop"
	sopexec "github.com/dimetron/pi-go/internal/sop/exec"
	"github.com/dimetron/pi-go/internal/subagent"
)

// prAutofixState tracks one /pr-autofix run.
//
// Unlike /plan and /run, nothing here mirrors the workflow: the SOP is the
// workflow, the engine walks it, and this struct only holds what the TUI has
// to draw. Stage status comes from stageTracker, which reads the engine's own
// event stream rather than guessing from artifacts on disk.
type prAutofixState struct {
	pr      string // the PR as the user gave it, passed to gh
	runDir  string // where stage artifacts land
	tracker *stageTracker
	started time.Time

	done bool
	err  error
}

// prAutofixMsg carries one step of a run back to the TUI.
type prAutofixMsg struct {
	event  *session.Event // engine event; drives stage status
	stage  string         // stage that produced Output, for transcript lines
	output string
	done   bool
	err    error
}

// prNumber matches the forms a PR is named by: a full URL, a #123, or a bare
// number. gh accepts all three, so this only has to recognize them well enough
// to reject something that is plainly not a PR.
var prNumber = regexp.MustCompile(`^(https://github\.com/[^/]+/[^/]+/pull/\d+|#?\d+)$`)

// handlePRAutofixCommand starts a /pr-autofix run.
func (m *model) handlePRAutofixCommand(args []string) (tea.Model, tea.Cmd) {
	if len(args) == 0 {
		m.showPRAutofixUsage()
		return m, nil
	}
	target := strings.TrimSpace(args[0])
	if !prNumber.MatchString(target) {
		m.appendAssistant(fmt.Sprintf("Not a pull request: %q. Pass a PR URL, #123, or 123.", target))
		return m, nil
	}
	if m.prAutofix != nil && !m.prAutofix.done {
		m.appendAssistant("A /pr-autofix run is already in flight. Wait for it to finish, or restart the session.")
		return m, nil
	}

	runDir, err := os.MkdirTemp("", "pi-pr-autofix-")
	if err != nil {
		m.appendAssistant(fmt.Sprintf("Cannot create a run directory: %v", err))
		return m, nil
	}

	m.prAutofix = &prAutofixState{
		pr:      target,
		runDir:  runDir,
		tracker: newStageTracker(),
		started: time.Now(),
	}
	m.appendAssistant(fmt.Sprintf("Watching %s. Fixing until green, up to 5 push cycles.", target))

	ch := make(chan prAutofixMsg, 64)
	go m.runPRAutofix(ch, target, runDir)
	return m, waitForPRAutofix(ch)
}

// runPRAutofix walks the pr-autofix SOP to completion, reporting every engine
// event and every stage's output on ch.
//
// It runs the SOP the user's overrides resolve to rather than the embedded copy
// — a run is the one place an override is meant to take effect — while the
// sidebar keeps drawing the embedded graph, so the diagram cannot drift from
// what LoadEmbeddedDefinition compiles.
func (m *model) runPRAutofix(ch chan<- prAutofixMsg, target, runDir string) {
	defer close(ch)

	def, err := sop.LoadDefinition(m.cfg.WorkDir, "pr-autofix")
	if err != nil {
		ch <- prAutofixMsg{done: true, err: err}
		return
	}

	shell := sopexec.ShellRunner{
		Dir:    m.cfg.WorkDir,
		RunDir: runDir,
		Env: []string{
			"PI_PR=" + target,
			"PI_FIX_MESSAGE=" + prAutofixCommitMessage,
		},
		Agent: m.prAutofixAgentRunner(runDir),
		Observe: func(stage, out string) {
			ch <- prAutofixMsg{stage: stage, output: out}
		},
	}

	ag, _, err := sopexec.Agent(def, sopexec.NewFactory(shell))
	if err != nil {
		ch <- prAutofixMsg{done: true, err: err}
		return
	}
	r, err := runner.New(runner.Config{
		AppName:           "pi-pr-autofix",
		Agent:             ag,
		SessionService:    session.InMemoryService(),
		AutoCreateSession: true,
	})
	if err != nil {
		ch <- prAutofixMsg{done: true, err: err}
		return
	}

	var runErr error
	for ev, err := range r.Run(context.Background(), "pi", "pr-autofix-"+filepath.Base(runDir),
		genai.NewContentFromText(target, genai.RoleUser), agent.RunConfig{}) {
		if err != nil {
			runErr = err
			break
		}
		ch <- prAutofixMsg{event: ev}
	}
	ch <- prAutofixMsg{done: true, err: runErr}
}

// prAutofixAgentRunner bridges an `agent:` stage to the subagent orchestrator.
//
// It blocks until the subagent finishes, because a SOP stage is finished when
// its work is: returning early would let the graph advance to the local gates
// while the fix was still being written.
func (m *model) prAutofixAgentRunner(runDir string) sopexec.StageRunner {
	orch := m.cfg.Orchestrator
	workDir := m.cfg.WorkDir

	return sopexec.RunnerFunc(func(ctx context.Context, req sopexec.StageRequest) (sopexec.StageOutcome, error) {
		if orch == nil {
			return sopexec.StageOutcome{}, fmt.Errorf("stage %q needs the subagent system, which is not available", req.Stage.ID)
		}
		cfg, err := orch.LookupAgent(req.Stage.AgentName())
		if err != nil {
			return sopexec.StageOutcome{}, fmt.Errorf("stage %q: %w", req.Stage.ID, err)
		}

		events, _, err := orch.Spawn(ctx, subagent.SpawnInput{
			Agent:   cfg,
			Prompt:  prAutofixStagePrompt(req.Stage, runDir),
			WorkDir: workDir,
		})
		if err != nil {
			return sopexec.StageOutcome{}, fmt.Errorf("stage %q: spawning %s: %w", req.Stage.ID, cfg.Name, err)
		}

		var failed bool
		var text strings.Builder
		for ev := range events {
			switch ev.Type {
			case "text_delta":
				text.WriteString(ev.Content)
			case "error":
				failed = true
				text.WriteString("\n" + ev.Error)
			}
		}
		if failed {
			return sopexec.StageOutcome{Route: sop.VerdictFail, Output: text.String()}, nil
		}
		return sopexec.StageOutcome{Route: sop.VerdictPass, Output: text.String()}, nil
	})
}

// prAutofixStagePrompt builds an agent stage's task from the SOP: its own
// description, plus the artifacts the stage declared as inputs.
//
// Reading the inputs here rather than naming them in the prompt is deliberate.
// A stage that says "read failures.log" is a stage that depends on the agent
// choosing to; pasting the evidence in removes the choice.
func prAutofixStagePrompt(stage sop.Stage, runDir string) string {
	var b strings.Builder
	b.WriteString(stage.Description)
	b.WriteString("\n\nThe failing checks are below, taken from the jobs' own logs.\n")
	b.WriteString("Fix them in the working tree. Do not commit, push, or touch the PR — ")
	b.WriteString("later stages do that once the local gates pass.\n")

	for _, name := range stage.Inputs {
		content, err := os.ReadFile(filepath.Join(runDir, name))
		if err != nil {
			continue
		}
		fmt.Fprintf(&b, "\n--- %s ---\n%s\n", name, truncateForPrompt(string(content)))
	}
	return b.String()
}

// truncateForPrompt keeps a log's tail: a failing job says what went wrong at
// the end, and the head is setup noise.
func truncateForPrompt(s string) string {
	const maxBytes = 24 * 1024
	if len(s) <= maxBytes {
		return s
	}
	return "… truncated …\n" + s[len(s)-maxBytes:]
}

// prAutofixCommitMessage is what a fix cycle commits under. It says which
// machinery produced the commit without naming the session that drove it.
const prAutofixCommitMessage = "fix: repair failing CI checks\n\n" +
	"Applied by /pr-autofix from the failing jobs' logs, verified against the\n" +
	"local gates (build, vet, test, lint) before pushing."

// waitForPRAutofix reads one message from the run.
func waitForPRAutofix(ch chan prAutofixMsg) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-ch
		if !ok {
			return prAutofixMsg{done: true}
		}
		return prAutofixChan{msg: msg, ch: ch}
	}
}

// prAutofixChan pairs a message with the channel it came from, so Update can
// queue the next read without holding the channel on the model.
type prAutofixChan struct {
	msg prAutofixMsg
	ch  chan prAutofixMsg
}

// handlePRAutofixMsg folds one run message into the model.
func (m *model) handlePRAutofixMsg(in prAutofixChan) (tea.Model, tea.Cmd) {
	st := m.prAutofix
	if st == nil {
		return m, nil
	}

	switch {
	case in.msg.event != nil:
		st.tracker.observe(in.msg.event)
	case in.msg.output != "":
		m.appendAssistant(prAutofixStageLine(in.msg.stage, in.msg.output))
	}

	if in.msg.done {
		st.done = true
		st.err = in.msg.err
		if in.msg.err != nil {
			st.tracker.fail()
			m.appendAssistant(fmt.Sprintf("pr-autofix stopped: %v", in.msg.err))
		} else {
			st.tracker.finish()
			m.appendAssistant(fmt.Sprintf("pr-autofix finished in %s.", time.Since(st.started).Round(time.Second)))
		}
		return m, nil
	}
	return m, waitForPRAutofix(in.ch)
}

// prAutofixStageLine renders one stage's output for the transcript, trimmed:
// a failing job log runs to hundreds of lines, and the whole of it belongs in
// the artifact the fix agent reads, not in the chat.
func prAutofixStageLine(stage, output string) string {
	const maxLines = 12
	lines := strings.Split(strings.TrimRight(output, "\n"), "\n")
	if len(lines) > maxLines {
		omitted := len(lines) - maxLines
		lines = append(lines[:maxLines], fmt.Sprintf("… %d more lines", omitted))
	}
	return "[" + stage + "] " + strings.Join(lines, "\n")
}

// showPRAutofixUsage explains the command.
func (m *model) showPRAutofixUsage() {
	m.appendAssistant(strings.Join([]string{
		"Usage: /pr-autofix <pr>",
		"",
		"Watches a GitHub PR's checks and fixes them until it is green:",
		"poll → triage → pull the failing job logs → fix → local gates → commit → push, and back to poll.",
		"",
		"The PR's branch must be the one checked out here, and the run pushes to it.",
		"Commits are signed, so each cycle waits on the signing prompt.",
		"",
		"  /pr-autofix 253",
		"  /pr-autofix https://github.com/dimetron/pi-go/pull/253",
	}, "\n"))
}

// appendAssistant adds one assistant line to the transcript. The TUI owns the
// terminal, so this is the only way this mode says anything.
func (m *model) appendAssistant(text string) {
	m.chatModel.Messages = append(m.chatModel.Messages, message{role: "assistant", content: text})
}
