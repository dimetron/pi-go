package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/dimetron/pi-go/internal/agent"
	pisession "github.com/dimetron/pi-go/internal/session"

	"google.golang.org/adk/session"
)

// --- agentMsg interface methods ---

func TestAgentMsgInterfaceMethods(t *testing.T) {
	// These are marker interface methods — just verify they satisfy the interface.
	var msgs []agentMsg
	msgs = append(msgs,
		agentTextMsg{text: "hello"},
		agentThinkingMsg{text: "thinking"},
		agentToolCallMsg{name: "bash", args: map[string]any{"cmd": "ls"}},
		agentToolResultMsg{name: "bash", content: "file.go"},
		agentDoneMsg{err: nil},
		agentSubEventMsg{agentID: "abc", kind: "text", content: "hi"},
	)
	for i, msg := range msgs {
		msg.agentMsg() // call the marker method
		if msg == nil {
			t.Errorf("msg[%d] is nil", i)
		}
	}
}

// --- waitForAgent tests (additional) ---

func TestWaitForAgent_NilCh(t *testing.T) {
	cmd := waitForAgent(nil)
	if cmd != nil {
		t.Error("expected nil cmd for nil channel")
	}
}

func TestWaitForAgent_ReceivesTextMsg(t *testing.T) {
	ch := make(chan agentMsg, 1)
	ch <- agentTextMsg{text: "hello"}

	cmd := waitForAgent(ch)
	if cmd == nil {
		t.Fatal("expected non-nil cmd")
	}
	msg := cmd()
	textMsg, ok := msg.(agentTextMsg)
	if !ok {
		t.Fatalf("expected agentTextMsg, got %T", msg)
	}
	if textMsg.text != "hello" {
		t.Errorf("expected 'hello', got %q", textMsg.text)
	}
}

// --- handleBranchCommand with real FileService ---

func TestHandleBranchCommand_NoArgsWithService(t *testing.T) {
	svc, err := pisession.NewFileService(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	m := &model{
		chatModel: ChatModel{Messages: make([]message, 0)},
		cfg: Config{
			SessionService: svc,
			SessionID:      "test-session",
		},
	}

	m.handleBranchCommand(nil)
	if len(m.chatModel.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(m.chatModel.Messages))
	}
	if !strings.Contains(m.chatModel.Messages[0].content, "Usage:") {
		t.Errorf("expected usage message, got %q", m.chatModel.Messages[0].content)
	}
}

func TestHandleBranchCommand_ListWithService(t *testing.T) {
	dir := t.TempDir()
	svc, err := pisession.NewFileService(dir)
	if err != nil {
		t.Fatal(err)
	}

	_, err = svc.Create(context.Background(), newCreateReq("test-sess"))
	if err != nil {
		t.Fatal(err)
	}

	m := &model{
		chatModel: ChatModel{Messages: make([]message, 0)},
		cfg: Config{
			SessionService: svc,
			SessionID:      "test-sess",
		},
	}

	m.handleBranchCommand([]string{"list"})
	if len(m.chatModel.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(m.chatModel.Messages))
	}
	if !strings.Contains(m.chatModel.Messages[0].content, "Branches") {
		t.Errorf("expected branch listing, got %q", m.chatModel.Messages[0].content)
	}
}

func TestHandleBranchCommand_CreateWithService(t *testing.T) {
	dir := t.TempDir()
	svc, err := pisession.NewFileService(dir)
	if err != nil {
		t.Fatal(err)
	}

	_, err = svc.Create(context.Background(), newCreateReq("test-sess"))
	if err != nil {
		t.Fatal(err)
	}

	m := &model{
		chatModel: ChatModel{Messages: make([]message, 0)},
		cfg: Config{
			SessionService: svc,
			SessionID:      "test-sess",
		},
	}

	m.handleBranchCommand([]string{"experiment"})
	if len(m.chatModel.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(m.chatModel.Messages))
	}
	if !strings.Contains(m.chatModel.Messages[0].content, "Created and switched") {
		t.Errorf("expected creation message, got %q", m.chatModel.Messages[0].content)
	}
}

func TestHandleBranchCommand_SwitchWithService(t *testing.T) {
	dir := t.TempDir()
	svc, err := pisession.NewFileService(dir)
	if err != nil {
		t.Fatal(err)
	}

	_, err = svc.Create(context.Background(), newCreateReq("test-sess"))
	if err != nil {
		t.Fatal(err)
	}

	err = svc.CreateBranch("test-sess", agent.AppName, agent.DefaultUserID, "feature")
	if err != nil {
		t.Fatal(err)
	}

	m := &model{
		chatModel: ChatModel{Messages: make([]message, 0)},
		cfg: Config{
			SessionService: svc,
			SessionID:      "test-sess",
		},
	}

	m.handleBranchCommand([]string{"switch", "feature"})
	if len(m.chatModel.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(m.chatModel.Messages))
	}
	if !strings.Contains(m.chatModel.Messages[0].content, "Switched to branch") {
		t.Errorf("expected switch message, got %q", m.chatModel.Messages[0].content)
	}
}

func TestHandleBranchCommand_SwitchNoNameWithService(t *testing.T) {
	dir := t.TempDir()
	svc, err := pisession.NewFileService(dir)
	if err != nil {
		t.Fatal(err)
	}

	m := &model{
		chatModel: ChatModel{Messages: make([]message, 0)},
		cfg: Config{
			SessionService: svc,
			SessionID:      "test-sess",
		},
	}

	m.handleBranchCommand([]string{"switch"})
	if len(m.chatModel.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(m.chatModel.Messages))
	}
	if !strings.Contains(m.chatModel.Messages[0].content, "Usage:") {
		t.Errorf("expected usage message, got %q", m.chatModel.Messages[0].content)
	}
}

func TestHandleBranchCommand_SwitchNonExistentBranch(t *testing.T) {
	dir := t.TempDir()
	svc, err := pisession.NewFileService(dir)
	if err != nil {
		t.Fatal(err)
	}

	_, err = svc.Create(context.Background(), newCreateReq("test-sess"))
	if err != nil {
		t.Fatal(err)
	}

	m := &model{
		chatModel: ChatModel{Messages: make([]message, 0)},
		cfg: Config{
			SessionService: svc,
			SessionID:      "test-sess",
		},
	}

	m.handleBranchCommand([]string{"switch", "nonexistent"})
	if len(m.chatModel.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(m.chatModel.Messages))
	}
	if !strings.Contains(m.chatModel.Messages[0].content, "Error") {
		t.Errorf("expected error message, got %q", m.chatModel.Messages[0].content)
	}
}

func TestHandleBranchCommand_ListError(t *testing.T) {
	dir := t.TempDir()
	svc, err := pisession.NewFileService(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Don't create the session — listing branches will error.
	m := &model{
		chatModel: ChatModel{Messages: make([]message, 0)},
		cfg: Config{
			SessionService: svc,
			SessionID:      "nonexistent-sess",
		},
	}

	m.handleBranchCommand([]string{"list"})
	if len(m.chatModel.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(m.chatModel.Messages))
	}
	if !strings.Contains(m.chatModel.Messages[0].content, "Error") {
		t.Errorf("expected error message, got %q", m.chatModel.Messages[0].content)
	}
}

func TestHandleBranchCommand_CreateError(t *testing.T) {
	dir := t.TempDir()
	svc, err := pisession.NewFileService(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Don't create session — creating branch will error.
	m := &model{
		chatModel: ChatModel{Messages: make([]message, 0)},
		cfg: Config{
			SessionService: svc,
			SessionID:      "nonexistent-sess",
		},
	}

	m.handleBranchCommand([]string{"my-branch"})
	if len(m.chatModel.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(m.chatModel.Messages))
	}
	if !strings.Contains(m.chatModel.Messages[0].content, "Error") {
		t.Errorf("expected error message, got %q", m.chatModel.Messages[0].content)
	}
}

// --- handleCompactCommand with real FileService ---

func TestHandleCompactCommand_WithService(t *testing.T) {
	dir := t.TempDir()
	svc, err := pisession.NewFileService(dir)
	if err != nil {
		t.Fatal(err)
	}

	_, err = svc.Create(context.Background(), newCreateReq("test-sess"))
	if err != nil {
		t.Fatal(err)
	}

	m := &model{
		chatModel: ChatModel{Messages: make([]message, 0)},
		cfg: Config{
			SessionService: svc,
			SessionID:      "test-sess",
		},
	}

	m.handleCompactCommand()
	if len(m.chatModel.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(m.chatModel.Messages))
	}
	// Should succeed (may compact empty session) or report error.
	content := m.chatModel.Messages[0].content
	if content == "" {
		t.Error("expected non-empty message")
	}
}

// --- cancelAgent tests ---

func TestCancelAgent_NoChannel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	m := &model{
		ctx:    ctx,
		cancel: cancel,
		chatModel: ChatModel{
			Streaming: "partial",
			Thinking:  "hmm",
		},
		statusModel: StatusModel{
			ActiveTool:  "bash",
			ActiveTools: map[string]time.Time{"bash": time.Now()},
		},
		running: true,
	}

	m.cancelAgent()

	if m.running {
		t.Error("expected running to be false")
	}
	if m.statusModel.ActiveTool != "" {
		t.Error("expected ActiveTool to be cleared")
	}
	if m.statusModel.ActiveTools != nil {
		t.Error("expected ActiveTools to be nil")
	}
	if m.chatModel.Streaming != "" {
		t.Error("expected Streaming to be cleared")
	}
	if m.chatModel.Thinking != "" {
		t.Error("expected Thinking to be cleared")
	}
}

func TestCancelAgent_WithChannel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan agentMsg, 1)
	m := &model{
		ctx:       ctx,
		cancel:    cancel,
		agentCh:   ch,
		chatModel: ChatModel{},
		running:   true,
	}

	close(ch)
	m.cancelAgent()

	if m.agentCh != nil {
		t.Error("expected agentCh to be nil after cancel")
	}
}

// --- helper ---

func newCreateReq(sessionID string) *session.CreateRequest {
	return &session.CreateRequest{
		SessionID: sessionID,
		AppName:   agent.AppName,
		UserID:    agent.DefaultUserID,
	}
}
