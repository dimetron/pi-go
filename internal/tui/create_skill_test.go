package tui

import (
	"context"
	"iter"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	adkmodel "google.golang.org/adk/v2/model"
	"google.golang.org/genai"

	"github.com/dimetron/pi-go/internal/agent"
	"github.com/dimetron/pi-go/internal/extension"
)

// skillCapturingLLM records the SystemInstruction it sees on each call, so a
// test can assert what the agent actually sent to the model after a slash
// activation rebuilt the system prompt.
type skillCapturingLLM struct {
	name     string
	response string
	mu       sync.Mutex
	lastSys  string
}

func (m *skillCapturingLLM) Name() string { return m.name }

func (m *skillCapturingLLM) GenerateContent(_ context.Context, req *adkmodel.LLMRequest, _ bool) iter.Seq2[*adkmodel.LLMResponse, error] {
	if req != nil && req.Config != nil && req.Config.SystemInstruction != nil {
		var sb strings.Builder
		for _, p := range req.Config.SystemInstruction.Parts {
			if p != nil && p.Text != "" {
				sb.WriteString(p.Text)
			}
		}
		m.mu.Lock()
		m.lastSys = sb.String()
		m.mu.Unlock()
	}
	return func(yield func(*adkmodel.LLMResponse, error) bool) {
		yield(&adkmodel.LLMResponse{
			Content: genai.NewContentFromText(m.response, genai.RoleModel),
		}, nil)
	}
}

func (m *skillCapturingLLM) capturedSystemInstruction() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastSys
}

// skillFixture writes a SKILL.md to a temp dir and returns the dir plus a
// skills slice containing only the freshly-created skill (bundled skills are
// filtered out so tests can target a specific skill by name).
func skillFixture(t *testing.T, name, body string) ([]extension.Skill, string) {
	t.Helper()
	dir := t.TempDir()
	skillDir := filepath.Join(dir, name)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\ndescription: test " + name + "\n---\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	all, err := extension.LoadSkillsWithOptions(extension.LoadOptions{AuditMode: extension.AuditSkip}, dir)
	if err != nil {
		t.Fatal(err)
	}
	var ours []extension.Skill
	for _, s := range all {
		if s.Name == name {
			ours = append(ours, s)
		}
	}
	if len(ours) != 1 {
		t.Fatalf("expected exactly 1 skill named %q, got %d (total %d)", name, len(ours), len(all))
	}
	return ours, dir
}

// TestPrepareSkillActivation_LoadsBodyAndRebuildsAgent is the core dynamic-
// activation test: a /<name> command must (a) load the body, (b) rebuild the
// agent so the system prompt contains the body, and (c) return the body+
// display for the caller.
func TestPrepareSkillActivation_LoadsBodyAndRebuildsAgent(t *testing.T) {
	const skillBody = "Always prefer stdlib. Be lazy. No new dependencies."
	skills, _ := skillFixture(t, "ponytail", skillBody)
	if len(skills) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(skills))
	}

	llm := &skillCapturingLLM{name: "test", response: "ok"}
	a, err := agent.New(agent.Config{Model: llm, Instruction: agent.SystemInstruction})
	if err != nil {
		t.Fatalf("agent.New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	m := &model{
		cfg: Config{
			Agent:   a,
			Skills:  skills,
			WorkDir: "/tmp",
		},
		ctx:    ctx,
		cancel: cancel,
	}

	body, display, ok := m.prepareSkillActivation(skills[0], []string{"simplify", "this"})
	if !ok {
		t.Fatal("prepareSkillActivation returned !ok on happy path")
	}
	if body != skillBody {
		t.Errorf("body = %q, want %q", body, skillBody)
	}
	if display != "/ponytail simplify this" {
		t.Errorf("display = %q, want %q", display, "/ponytail simplify this")
	}

	// Now drive the agent with a real prompt so the LLM is invoked and the
	// SystemInstruction is captured. The captured text must contain the
	// skill body and the active-skill header.
	sessionID, _, err := a.CreateSession(ctx)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	for ev, err := range a.Run(ctx, sessionID, "go") {
		_ = ev
		if err != nil {
			t.Fatalf("Run error: %v", err)
		}
	}
	sys := llm.capturedSystemInstruction()
	if !strings.Contains(sys, "# Active Skill: ponytail") {
		t.Errorf("system prompt missing active-skill header, got %q", sys)
	}
	if !strings.Contains(sys, skillBody) {
		t.Errorf("system prompt missing skill body, got %q", sys)
	}
}

// TestPrepareSkillActivation_NoArgs exercises the display-string branch when
// the user types just `/ponytail` with no extra text.
func TestPrepareSkillActivation_NoArgs(t *testing.T) {
	skills, _ := skillFixture(t, "ponytail", "body")
	llm := &skillCapturingLLM{name: "test", response: "ok"}
	a, err := agent.New(agent.Config{Model: llm, Instruction: agent.SystemInstruction})
	if err != nil {
		t.Fatal(err)
	}
	m := &model{
		cfg: Config{Agent: a, Skills: skills},
		ctx: context.Background(),
	}
	_, display, ok := m.prepareSkillActivation(skills[0], nil)
	if !ok {
		t.Fatal("prepareSkillActivation returned !ok")
	}
	if display != "/ponytail" {
		t.Errorf("display = %q, want %q", display, "/ponytail")
	}
}

// TestPrepareSkillActivation_UnknownSkill verifies the error path: if the
// skill is not in the loaded slice, an error message is appended to the chat
// and the helper returns ok=false.
func TestPrepareSkillActivation_UnknownSkill(t *testing.T) {
	llm := &skillCapturingLLM{name: "test", response: "ok"}
	a, err := agent.New(agent.Config{Model: llm, Instruction: agent.SystemInstruction})
	if err != nil {
		t.Fatal(err)
	}
	m := &model{
		cfg: Config{
			Agent:  a,
			Skills: nil, // no skills loaded
		},
		ctx: context.Background(),
	}
	_, _, ok := m.prepareSkillActivation(extension.Skill{Name: "no-such-skill"}, nil)
	if ok {
		t.Error("expected ok=false when skill is missing")
	}
	if len(m.chatModel.Messages) != 1 {
		t.Fatalf("expected 1 error message, got %d", len(m.chatModel.Messages))
	}
	if !strings.Contains(m.chatModel.Messages[0].content, "Error loading skill") {
		t.Errorf("expected error message, got %q", m.chatModel.Messages[0].content)
	}
}

// TestHandleSkillCommand_UserMessageIsClean is the regression test for the
// old (pre-Plan A) behavior: the skill body used to be stuffed into the user
// prompt. It must NOT be — only `/<name> <args>` goes to the model as a
// user message; the body lives in the system prompt (asserted in the prior
// test).
func TestHandleSkillCommand_UserMessageIsClean(t *testing.T) {
	const skillBody = "secret ponytail wisdom that must never leak into user msgs"
	skills, _ := skillFixture(t, "ponytail", skillBody)
	llm := &skillCapturingLLM{name: "test", response: "ok"}
	a, err := agent.New(agent.Config{Model: llm, Instruction: agent.SystemInstruction})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := &model{
		cfg: Config{
			Agent:   a,
			Skills:  skills,
			WorkDir: "/tmp",
		},
		ctx:        ctx,
		cancel:     cancel,
		inputModel: NewInputModel(nil, nil, nil, ""),
		chatModel:  ChatModel{Messages: make([]message, 0)},
	}

	_, _ = m.handleSkillCommand(skills[0], []string{"simplify", "this"})

	// Find the user message (the last "user" role before any tool events).
	var userMsg *message
	for i := len(m.chatModel.Messages) - 1; i >= 0; i-- {
		if m.chatModel.Messages[i].role == "user" {
			userMsg = &m.chatModel.Messages[i]
			break
		}
	}
	if userMsg == nil {
		t.Fatal("no user message appended by handleSkillCommand")
	}
	if strings.Contains(userMsg.content, skillBody) {
		t.Errorf("user message contains skill body — regression: %q", userMsg.content)
	}
	if userMsg.content != "/ponytail simplify this" {
		t.Errorf("user message = %q, want %q", userMsg.content, "/ponytail simplify this")
	}
}

// TestFormatSkillsReport_ShadowedAndOrphanSkills covers the case the old flat
// list could not express: a skill on disk that the model never sees. Both
// causes have to be distinguishable, because they need different fixes.
func TestFormatSkillsReport_ShadowedAndOrphanSkills(t *testing.T) {
	userDir := t.TempDir()
	projDir := t.TempDir()

	write := func(dir, name, body string) string {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		f := filepath.Join(p, "SKILL.md")
		if err := os.WriteFile(f, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return f
	}

	// Same name in both directories: the project copy wins, the user copy is
	// shadowed. "orphan" is on disk but never made it into the loaded set.
	write(userDir, "dup", "shadowed body")
	winner := write(projDir, "dup", "winning body")
	write(projDir, "orphan", "never loaded")

	skills := []extension.Skill{{
		Name:        "dup",
		Description: "a | pipe and a very long description that must be truncated so it cannot set the column width",
		BodyPath:    winner,
		Source:      "project",
	}}

	got := formatSkillsReport(skills, []string{userDir, projDir})

	for _, want := range []string{
		"Loaded skills (1)",
		"Discovered but not active (2)",
		"shadowed by",
		"not loaded",
		"orphan",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("report missing %q:\n%s", want, got)
		}
	}
	// A raw pipe in a description would break the row into extra columns.
	if strings.Contains(got, "a | pipe") {
		t.Errorf("description pipe not escaped:\n%s", got)
	}
	// The winning file must not also be listed as inactive.
	if strings.Count(got, "shadowed by") != 1 {
		t.Errorf("want exactly one shadowed row:\n%s", got)
	}
}

func TestTableCell_TruncatesAndFlattens(t *testing.T) {
	if got := tableCell("one\n  two\tthree", 40); got != "one two three" {
		t.Errorf("tableCell = %q, want %q", got, "one two three")
	}
	long := strings.Repeat("x", 200)
	got := tableCell(long, 20)
	if len([]rune(got)) != 20 {
		t.Errorf("tableCell len = %d, want 20 (%q)", len([]rune(got)), got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("truncated cell should end in an ellipsis: %q", got)
	}
}

// TestFormatSkillsReport_FrontmatterNameOverride pins the case where a
// SKILL.md declares `name:` differently from its directory: the file must
// still be classified as the winner, not as "not loaded". The loader keys
// winners by their frontmatter name; the directory walker keys its scan by
// the directory name. The report must bridge the two by absolute path.
func TestFormatSkillsReport_FrontmatterNameOverride(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "review")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(sub, "SKILL.md")
	body := "---\nname: reviewed-thing\ndescription: alt name\n---\nbody\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	skills := []extension.Skill{{Name: "reviewed-thing", BodyPath: path, Source: "user"}}
	got := formatSkillsReport(skills, []string{dir})
	if !strings.Contains(got, "Loaded skills (1)") {
		t.Errorf("expected winner in loaded table, got:\n%s", got)
	}
	if strings.Contains(got, "not loaded") || strings.Contains(got, "Discovered but not active") {
		t.Errorf("winning file wrongly classified as inactive:\n%s", got)
	}
}

func TestShortSkillDir(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if got := shortSkillDir(filepath.Join(cwd, ".pi-go", "skills")); got != ".pi-go/skills" {
		t.Errorf("in-project dir = %q, want %q", got, ".pi-go/skills")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	if got := shortSkillDir(filepath.Join(home, ".pi-go", "skills")); got != "~/.pi-go/skills" {
		t.Errorf("home dir = %q, want %q", got, "~/.pi-go/skills")
	}
	// Neither: left absolute rather than turned into a ../../.. crawl.
	if got := shortSkillDir("/opt/skills"); got != "/opt/skills" {
		t.Errorf("outside dir = %q, want %q", got, "/opt/skills")
	}
}
