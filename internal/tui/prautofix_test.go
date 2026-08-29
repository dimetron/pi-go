package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"google.golang.org/adk/v2/session"

	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/sop"
	sopexec "github.com/dimetron/pi-go/internal/sop/exec"
	"github.com/dimetron/pi-go/internal/subagent"
)

// TestPRTargetAccepted pins which forms name a pull request. gh accepts a URL,
// a #123 and a bare number; anything else is a typo worth rejecting before a
// run starts rather than after gh fails.
func TestPRTargetAccepted(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{"https://github.com/dimetron/pi-go/pull/253", true},
		{"#253", true},
		{"253", true},
		{"", false},
		{"main", false},
		{"https://github.com/dimetron/pi-go/issues/253", false},
		{"https://example.com/dimetron/pi-go/pull/253", false},
		{"253; rm -rf /", false},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := prNumber.MatchString(tt.in); got != tt.want {
				t.Errorf("prNumber.MatchString(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

// TestPRAutofixStagePromptEmbedsInputs verifies the fix agent is handed the
// evidence rather than told where to find it.
func TestPRAutofixStagePromptEmbedsInputs(t *testing.T) {
	runDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(runDir, "failures.log"), []byte("undefined: Foo"), 0o644); err != nil {
		t.Fatalf("write input: %v", err)
	}

	stage := sop.Stage{
		ID:          "fix",
		Description: "Fix the failing checks in the working tree",
		Agent:       "worker",
		Inputs:      []string{"failures.log", "missing.json"},
	}
	got := prAutofixStagePrompt(stage, runDir)

	if !strings.Contains(got, stage.Description) {
		t.Error("prompt should carry the stage's own description")
	}
	if !strings.Contains(got, "undefined: Foo") {
		t.Error("prompt should embed the input's content, not just its name")
	}
	if !strings.Contains(got, "Do not commit, push") {
		t.Error("prompt should tell the agent that later stages own the commit")
	}
	// A declared input that no stage wrote is skipped rather than fatal: the
	// prompt is still useful with the evidence that does exist.
	if strings.Contains(got, "--- missing.json ---") {
		t.Error("a missing input should be skipped, not announced")
	}
}

// TestTruncateForPromptKeepsTail verifies an oversized log keeps its end,
// where a failing job says what actually went wrong.
func TestTruncateForPromptKeepsTail(t *testing.T) {
	body := strings.Repeat("noise\n", 20000) + "FINAL: the real error"
	got := truncateForPrompt(body)

	if len(got) > 25*1024 {
		t.Errorf("truncated length = %d, want it bounded", len(got))
	}
	if !strings.Contains(got, "FINAL: the real error") {
		t.Error("truncation dropped the tail, which is the part that matters")
	}
	if !strings.Contains(got, "truncated") {
		t.Error("truncation should say it happened")
	}
}

// TestTruncateForPromptLeavesShortInputAlone verifies a log that fits is passed
// through untouched.
func TestTruncateForPromptLeavesShortInputAlone(t *testing.T) {
	const short = "one line"
	if got := truncateForPrompt(short); got != short {
		t.Errorf("truncateForPrompt(%q) = %q, want it unchanged", short, got)
	}
}

// TestPRAutofixStageLineTrims verifies a long stage output is trimmed for the
// transcript. The whole log belongs in the artifact the fix agent reads; the
// chat gets a readable excerpt.
func TestPRAutofixStageLineTrims(t *testing.T) {
	out := strings.Repeat("line\n", 40)
	got := prAutofixStageLine("diagnose", out)

	if !strings.HasPrefix(got, "[diagnose] ") {
		t.Errorf("line = %q, want it to name its stage", got)
	}
	if n := strings.Count(got, "\n") + 1; n > 14 {
		t.Errorf("rendered %d lines, want a trimmed excerpt", n)
	}
	if !strings.Contains(got, "more lines") {
		t.Error("a trimmed excerpt should say how much it dropped")
	}
}

// TestPRAutofixStageLineKeepsShortOutput verifies output that already fits is
// shown whole.
func TestPRAutofixStageLineKeepsShortOutput(t *testing.T) {
	got := prAutofixStageLine("triage", "2 checks failing\n")
	if got != "[triage] 2 checks failing" {
		t.Errorf("line = %q, want the output kept whole", got)
	}
}

// TestPRAutofixSOPDrawsInSidebar verifies the mode's graph is the compiled SOP
// — the diagram and the executed workflow are the same definition, so they
// cannot drift.
func TestPRAutofixSOPDrawsInSidebar(t *testing.T) {
	def, err := sop.LoadEmbeddedDefinition("pr-autofix")
	if err != nil {
		t.Fatalf("LoadEmbeddedDefinition: %v", err)
	}
	compiled, err := sop.Compile(def, sop.DescribeFactory{})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	want := []string{"resolve", "watch", "triage", "diagnose", "fix", "gates", "commit", "push", "summary"}
	for _, id := range want {
		if !slices.Contains(compiled.Order, id) {
			t.Errorf("compiled order %v is missing stage %q", compiled.Order, id)
		}
	}
}

// TestPRAutofixStageStartExtractsCycle verifies the lifecycle event parser
// recovers both the stage id and the cycle number, so the narration can say
// "cycle 2" when the graph loops back.
func TestPRAutofixStageStartExtractsCycle(t *testing.T) {
	tests := []struct {
		name      string
		branch    string
		author    string
		wantStage string
		wantCycle int
		wantOK    bool
	}{
		{"first activation", "push", sopexec.LifecycleAuthor, "push", 1, true},
		{"second cycle", "watch#2", sopexec.LifecycleAuthor, "watch", 2, true},
		{"third cycle", "watch#3", sopexec.LifecycleAuthor, "watch", 3, true},
		{"not a lifecycle event", "push", "model", "", 0, false},
		// A branch whose suffix is not a number falls back to cycle 1 rather
		// than dropping the stage start.
		{"unparsable cycle", "watch#later", sopexec.LifecycleAuthor, "watch", 1, true},
		{"no branch at all", "", sopexec.LifecycleAuthor, "", 1, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ev := &session.Event{Author: tt.author, Branch: tt.branch}
			stage, cycle, ok := prAutofixStageStart(ev)
			if ok != tt.wantOK {
				t.Errorf("ok = %v, want %v", ok, tt.wantOK)
			}
			if stage != tt.wantStage {
				t.Errorf("stage = %q, want %q", stage, tt.wantStage)
			}
			if cycle != tt.wantCycle {
				t.Errorf("cycle = %d, want %d", cycle, tt.wantCycle)
			}
		})
	}
}

// TestPRAutofixStartLineNarratesStage verifies the start line names the stage,
// carries its description, and includes the cycle number on loops > 1.
func TestPRAutofixStartLineNarratesStage(t *testing.T) {
	if got := prAutofixStartLine("push", 1); !strings.Contains(got, "push") {
		t.Errorf("first cycle: %q should name the stage", got)
	}
	if got := prAutofixStartLine("push", 1); !strings.Contains(got, "Pushing to the PR branch") {
		t.Errorf("first cycle: %q should carry the stage's description", got)
	}
	if got := prAutofixStartLine("watch", 2); !strings.Contains(got, "cycle 2") {
		t.Errorf("second cycle: %q should say which cycle it is", got)
	}
	// An unknown stage falls back to its id rather than empty text.
	if got := prAutofixStartLine("unknown", 1); !strings.Contains(got, "unknown") {
		t.Errorf("unknown stage: %q should fall back to its id", got)
	}
}

// --- Command, run loop and message folding ---

// prAutofixModel builds the smallest model the mode needs: a transcript to
// append to and a working directory to resolve the SOP from.
func prAutofixModel(workDir string) *model {
	return &model{
		cfg:       Config{WorkDir: workDir},
		chatModel: ChatModel{Messages: make([]message, 0)},
	}
}

// lastAssistant returns the text of the last transcript line.
func lastAssistant(t *testing.T, m *model) string {
	t.Helper()
	if len(m.chatModel.Messages) == 0 {
		t.Fatal("no transcript lines were appended")
	}
	return m.chatModel.Messages[len(m.chatModel.Messages)-1].content
}

// writeSOPOverride installs a project SOP override under workDir, the path
// LoadDefinition prefers over the embedded copy.
func writeSOPOverride(t *testing.T, workDir, body string) {
	t.Helper()
	dir := filepath.Join(workDir, ".pi-go", "sops")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir sops: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pr-autofix.sop.yaml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write override: %v", err)
	}
}

// trivialSOP is a valid pr-autofix override whose stages are shell no-ops. It
// exercises the engine end to end without reaching for gh or git.
const trivialSOP = `sop: pr-autofix
version: 2
description: a no-op pr-autofix for tests
workspace:
  worktree: none
  merge_on: never
  cleanup: always
stages:
  - id: watch
    description: pretend to poll
    kind: function
    run: echo watched
    next: summary
  - id: summary
    description: pretend to summarize
    kind: function
    run: echo summarized
`

// TestPRAutofixCommandRejectsBadInput verifies the command explains itself
// rather than starting a run it cannot finish.
func TestPRAutofixCommandRejectsBadInput(t *testing.T) {
	t.Run("no argument shows usage", func(t *testing.T) {
		m := prAutofixModel(t.TempDir())
		if _, cmd := m.handlePRAutofixCommand(nil); cmd != nil {
			t.Error("usage must not start a run")
		}
		if got := lastAssistant(t, m); !strings.Contains(got, "/pr-autofix <pr>") {
			t.Errorf("message = %q, want the usage text", got)
		}
		if m.prAutofix != nil {
			t.Error("usage must not create run state")
		}
	})

	t.Run("a non-PR argument is refused", func(t *testing.T) {
		m := prAutofixModel(t.TempDir())
		if _, cmd := m.handlePRAutofixCommand([]string{"main"}); cmd != nil {
			t.Error("a bad target must not start a run")
		}
		if got := lastAssistant(t, m); !strings.Contains(got, "Not a pull request") {
			t.Errorf("message = %q, want a rejection naming the input", got)
		}
	})

	t.Run("a second run is refused while one is in flight", func(t *testing.T) {
		m := prAutofixModel(t.TempDir())
		m.prAutofix = &prAutofixState{pr: "253"}

		if _, cmd := m.handlePRAutofixCommand([]string{"254"}); cmd != nil {
			t.Error("a concurrent run must not be started")
		}
		if got := lastAssistant(t, m); !strings.Contains(got, "already in flight") {
			t.Errorf("message = %q, want a refusal", got)
		}
		if m.prAutofix.pr != "253" {
			t.Errorf("in-flight run was replaced: pr = %q", m.prAutofix.pr)
		}
	})

	t.Run("a finished run does not block a new one", func(t *testing.T) {
		m := prAutofixModel(t.TempDir())
		writeSOPOverride(t, m.cfg.WorkDir, trivialSOP)
		m.prAutofix = &prAutofixState{pr: "253", done: true}

		_, cmd := m.handlePRAutofixCommand([]string{"254"})
		if cmd == nil {
			t.Fatal("a finished run must not block the next one")
		}
		if m.prAutofix.pr != "254" {
			t.Errorf("pr = %q, want the new target", m.prAutofix.pr)
		}
		t.Cleanup(func() { os.RemoveAll(m.prAutofix.runDir) })
		drainPRAutofix(t, m, cmd)
	})
}

// TestPRAutofixCommandStartsRun verifies the accepted path: state is created,
// the transcript says what is happening, and the returned command feeds the
// run's messages back into the model until it finishes.
func TestPRAutofixCommandStartsRun(t *testing.T) {
	m := prAutofixModel(t.TempDir())
	writeSOPOverride(t, m.cfg.WorkDir, trivialSOP)

	_, cmd := m.handlePRAutofixCommand([]string{"https://github.com/dimetron/pi-go/pull/253"})
	if cmd == nil {
		t.Fatal("an accepted target must start a run")
	}
	if m.prAutofix == nil {
		t.Fatal("no run state was created")
	}
	if m.prAutofix.runDir == "" {
		t.Error("run state has no run directory for stage artifacts")
	}
	t.Cleanup(func() { os.RemoveAll(m.prAutofix.runDir) })
	if m.prAutofix.tracker == nil {
		t.Error("run state has no stage tracker, so the sidebar would show nothing")
	}

	drainPRAutofix(t, m, cmd)

	if !m.prAutofix.done {
		t.Error("run did not finish")
	}
	if m.prAutofix.err != nil {
		t.Errorf("run error = %v, want a clean finish", m.prAutofix.err)
	}
	transcript := allAssistant(m)
	if !strings.Contains(transcript, "Watching https://github.com/dimetron/pi-go/pull/253") {
		t.Error("transcript should announce what is being watched")
	}
	if !strings.Contains(transcript, "pr-autofix finished in") {
		t.Errorf("transcript should report the finish, got:\n%s", transcript)
	}
	if !strings.Contains(transcript, "watched") {
		t.Error("transcript should carry the stages' own output")
	}
}

// drainPRAutofix runs the command loop the TUI would run, folding every message
// back into the model until the run reports done.
func drainPRAutofix(t *testing.T, m *model, cmd tea.Cmd) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for cmd != nil {
		if time.Now().After(deadline) {
			t.Fatal("pr-autofix run did not finish in time")
		}
		msg := cmd()
		next, ok := msg.(prAutofixChan)
		if !ok {
			// The channel closed; the terminal message is not a chan message.
			return
		}
		_, cmd = m.handlePRAutofixMsg(next)
	}
}

// allAssistant joins the transcript for substring assertions.
func allAssistant(m *model) string {
	var b strings.Builder
	for _, msg := range m.chatModel.Messages {
		b.WriteString(msg.content)
		b.WriteString("\n")
	}
	return b.String()
}

// TestPRAutofixRunReportsLoadFailure verifies an override that does not compile
// stops the run and says so, rather than falling back to the embedded SOP.
func TestPRAutofixRunReportsLoadFailure(t *testing.T) {
	workDir := t.TempDir()
	writeSOPOverride(t, workDir, "sop: pr-autofix\nversion: 2\nstages: []\n")

	m := prAutofixModel(workDir)
	ch := make(chan prAutofixMsg, 8)
	go m.runPRAutofix(ch, "253", t.TempDir())

	var last prAutofixMsg
	for msg := range ch {
		last = msg
	}
	if !last.done {
		t.Fatal("run did not report done")
	}
	if last.err == nil {
		t.Fatal("an override with no stages must fail the run, not schedule nothing")
	}
}

// TestPRAutofixRunEmitsStagesAndFinishes verifies a walk of the SOP reports
// each stage's output and ends with a clean done message.
func TestPRAutofixRunEmitsStagesAndFinishes(t *testing.T) {
	workDir := t.TempDir()
	writeSOPOverride(t, workDir, trivialSOP)

	m := prAutofixModel(workDir)
	// The run is drained concurrently rather than after the fact: a buffered
	// channel sized to today's event count would deadlock the moment the
	// engine emitted one more.
	ch := make(chan prAutofixMsg, 8)
	go m.runPRAutofix(ch, "253", t.TempDir())

	var outputs []string
	var events int
	var last prAutofixMsg
	for msg := range ch {
		if msg.output != "" {
			outputs = append(outputs, msg.stage+":"+strings.TrimSpace(msg.output))
		}
		if msg.event != nil {
			events++
		}
		last = msg
	}

	if !last.done || last.err != nil {
		t.Fatalf("final message = %+v, want a clean done", last)
	}
	if events == 0 {
		t.Error("no engine events reached the TUI, so the sidebar would never update")
	}
	if !slices.Contains(outputs, "watch:watched") {
		t.Errorf("stage output = %v, want the watch stage's own output", outputs)
	}
}

// TestWaitForPRAutofixReadsOneMessage verifies the command reads exactly one
// message and pairs it with its channel, and that a closed channel ends the
// loop rather than blocking it.
func TestWaitForPRAutofixReadsOneMessage(t *testing.T) {
	t.Run("delivers a message with its channel", func(t *testing.T) {
		ch := make(chan prAutofixMsg, 1)
		ch <- prAutofixMsg{stage: "watch", output: "polling"}

		got, ok := waitForPRAutofix(ch)().(prAutofixChan)
		if !ok {
			t.Fatal("want a prAutofixChan so Update can queue the next read")
		}
		if got.msg.stage != "watch" || got.ch != ch {
			t.Errorf("got %+v, want the message paired with its own channel", got.msg)
		}
	})

	t.Run("a closed channel ends the run", func(t *testing.T) {
		ch := make(chan prAutofixMsg)
		close(ch)

		got, ok := waitForPRAutofix(ch)().(prAutofixMsg)
		if !ok || !got.done {
			t.Errorf("got %#v, want a done message so the loop stops", got)
		}
	})
}

// TestHandlePRAutofixMsgFolds verifies each kind of run message lands in the
// model: stage starts narrate, output goes to the transcript, and done settles
// the state either way.
func TestHandlePRAutofixMsgFolds(t *testing.T) {
	newState := func(m *model) {
		m.prAutofix = &prAutofixState{pr: "253", tracker: newStageTracker(), started: time.Now()}
	}

	t.Run("a message with no run state is dropped", func(t *testing.T) {
		m := prAutofixModel(t.TempDir())
		if _, cmd := m.handlePRAutofixMsg(prAutofixChan{}); cmd != nil {
			t.Error("a message arriving after the state is gone must not requeue")
		}
	})

	t.Run("a stage start narrates", func(t *testing.T) {
		m := prAutofixModel(t.TempDir())
		newState(m)
		ch := make(chan prAutofixMsg, 1)
		ev := &session.Event{Author: sopexec.LifecycleAuthor, Branch: "watch#2"}

		_, cmd := m.handlePRAutofixMsg(prAutofixChan{msg: prAutofixMsg{event: ev}, ch: ch})
		if cmd == nil {
			t.Error("a non-final message must queue the next read")
		}
		if got := lastAssistant(t, m); !strings.Contains(got, "cycle 2") {
			t.Errorf("narration = %q, want the cycle number", got)
		}
	})

	t.Run("stage output reaches the transcript", func(t *testing.T) {
		m := prAutofixModel(t.TempDir())
		newState(m)
		ch := make(chan prAutofixMsg, 1)

		m.handlePRAutofixMsg(prAutofixChan{msg: prAutofixMsg{stage: "triage", output: "2 failing"}, ch: ch})
		if got := lastAssistant(t, m); got != "[triage] 2 failing" {
			t.Errorf("transcript line = %q", got)
		}
	})

	t.Run("done settles the run", func(t *testing.T) {
		m := prAutofixModel(t.TempDir())
		newState(m)

		_, cmd := m.handlePRAutofixMsg(prAutofixChan{msg: prAutofixMsg{done: true}})
		if cmd != nil {
			t.Error("a finished run must not queue another read")
		}
		if !m.prAutofix.done || m.prAutofix.err != nil {
			t.Errorf("state = %+v, want a clean finish", m.prAutofix)
		}
		if got := lastAssistant(t, m); !strings.Contains(got, "finished in") {
			t.Errorf("message = %q, want the finish line", got)
		}
	})

	t.Run("a failed run reports its error", func(t *testing.T) {
		m := prAutofixModel(t.TempDir())
		newState(m)

		_, cmd := m.handlePRAutofixMsg(prAutofixChan{msg: prAutofixMsg{done: true, err: errors.New("gh exploded")}})
		if cmd != nil {
			t.Error("a failed run must not queue another read")
		}
		if m.prAutofix.err == nil {
			t.Error("the error was dropped from the state")
		}
		if got := lastAssistant(t, m); !strings.Contains(got, "gh exploded") {
			t.Errorf("message = %q, want the error surfaced", got)
		}
	})
}

// TestPRAutofixAgentRunnerRefusesWithoutOrchestrator verifies an agent stage
// fails loudly when the subagent system is missing. Passing silently would let
// the SOP advance to the gates with no fix written.
func TestPRAutofixAgentRunnerRefusesWithoutOrchestrator(t *testing.T) {
	m := prAutofixModel(t.TempDir())
	runner := m.prAutofixAgentRunner(t.TempDir())

	_, err := runner.RunStage(context.Background(), sopexec.StageRequest{
		Stage: sop.Stage{ID: "fix", Agent: "worker"},
	})
	if err == nil {
		t.Fatal("an agent stage with no orchestrator must fail")
	}
	if !strings.Contains(err.Error(), "fix") {
		t.Errorf("error %q should name the stage it could not run", err)
	}
}

// TestPRAutofixAgentRunnerRejectsUnknownAgent verifies a stage naming an agent
// the orchestrator does not have fails on lookup rather than at spawn.
func TestPRAutofixAgentRunnerRejectsUnknownAgent(t *testing.T) {
	m := prAutofixModel(t.TempDir())
	m.cfg.Orchestrator = subagent.NewOrchestrator(&config.Config{}, "", nil)
	runner := m.prAutofixAgentRunner(t.TempDir())

	_, err := runner.RunStage(context.Background(), sopexec.StageRequest{
		Stage: sop.Stage{ID: "fix", Agent: "no-such-agent"},
	})
	if err == nil {
		t.Fatal("an unknown agent must fail the stage")
	}
	if !strings.Contains(err.Error(), "fix") {
		t.Errorf("error %q should name the stage", err)
	}
}

// TestPRAutofixAgentRunnerReportsSpawnFailure verifies a stage whose agent is
// known but cannot be started fails the run rather than reporting a fix that
// was never written.
func TestPRAutofixAgentRunnerReportsSpawnFailure(t *testing.T) {
	orch := subagent.NewOrchestrator(&config.Config{}, "", nil)
	orch.RegisterAgents([]subagent.AgentConfig{{Name: "worker", Description: "test agent"}})
	// A shut-down orchestrator refuses to spawn, which is the cheapest way to
	// reach the failure branch without starting a real subagent.
	orch.Shutdown()

	m := prAutofixModel(t.TempDir())
	m.cfg.Orchestrator = orch

	_, err := m.prAutofixAgentRunner(t.TempDir()).RunStage(context.Background(), sopexec.StageRequest{
		Stage: sop.Stage{ID: "fix", Agent: "worker"},
	})
	if err == nil {
		t.Fatal("a spawn that fails must fail the stage")
	}
	if !strings.Contains(err.Error(), "fix") || !strings.Contains(err.Error(), "worker") {
		t.Errorf("error %q should name the stage and the agent", err)
	}
}

// TestPRAutofixMessageRoutesThroughUpdate verifies a run message reaches the
// handler through the TUI's own dispatch, not just by direct call.
func TestPRAutofixMessageRoutesThroughUpdate(t *testing.T) {
	m := prAutofixModel(t.TempDir())
	m.prAutofix = &prAutofixState{pr: "253", tracker: newStageTracker(), started: time.Now()}

	_, _, handled := m.updateRunWorkflow(prAutofixChan{msg: prAutofixMsg{done: true}})
	if !handled {
		t.Fatal("updateRunWorkflow must claim prAutofixChan, or run messages never arrive")
	}
	if !m.prAutofix.done {
		t.Error("the message was claimed but not folded into the state")
	}
}

// TestPRAutofixDrawsItsGraphInTheSidebar verifies an in-flight run puts the
// compiled pr-autofix graph on the sidebar, with the tracker's statuses.
func TestPRAutofixDrawsItsGraphInTheSidebar(t *testing.T) {
	m := prAutofixModel(t.TempDir())

	if in := m.sidebarRenderInput(30, 20); in.Graph != nil {
		t.Fatal("no run should mean no graph")
	}

	m.prAutofix = &prAutofixState{pr: "253", tracker: newStageTracker(), started: time.Now()}
	in := m.sidebarRenderInput(30, 20)
	if in.Graph == nil {
		t.Fatal("an in-flight run should draw its graph")
	}
	if !slices.Contains(in.Graph.Order, "watch") {
		t.Errorf("graph order = %v, want the pr-autofix stages", in.Graph.Order)
	}
}
