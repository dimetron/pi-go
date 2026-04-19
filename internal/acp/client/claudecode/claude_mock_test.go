package claudecode

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"

	shared "github.com/dimetron/pi-go/internal/acp"
)

func testSession() *RunningSession {
	return &RunningSession{
		events:     make(chan shared.Event, 32),
		done:       make(chan struct{}),
		stderr:     &stderrBuffer{},
		toolFilter: shared.NewToolCallTitleFilter(func(title string) {}),
	}
}

func TestRunningSessionEvents(t *testing.T) {
	session := &RunningSession{
		events: make(chan shared.Event, 32),
		done:   make(chan struct{}),
	}

	go func() {
		select {
		case session.events <- shared.Event{Type: shared.EventTypeMessage, Content: "test"}:
		default:
		}
	}()

	select {
	case ev := <-session.Events():
		if ev.Content != "test" {
			t.Errorf("expected 'test', got %q", ev.Content)
		}
	case <-time.After(time.Second):
		t.Error("timeout waiting for event")
	}
}

func TestRunningSessionEmit(t *testing.T) {
	session := &RunningSession{
		events: make(chan shared.Event, 1),
		done:   make(chan struct{}),
	}

	session.events <- shared.Event{Type: "full"}
	session.emit(shared.Event{Type: shared.EventTypeMessage, Content: "should not block"})

	select {
	case ev := <-session.events:
		if ev.Type != "full" {
			t.Errorf("expected 'full', got %q", ev.Type)
		}
	case <-time.After(time.Millisecond):
		t.Error("original event was dropped")
	}
}

func TestRunningSessionAppendResult(t *testing.T) {
	session := testSession()
	session.appendResult("Hello ")
	session.appendResult("World")

	session.mu.Lock()
	result := session.result.Result
	session.mu.Unlock()
	if result != "Hello World" {
		t.Errorf("expected 'Hello World', got %q", result)
	}
}

func TestRunningSessionFinish(t *testing.T) {
	session := testSession()
	session.finish(shared.RunResult{
		Status:    shared.StatusSuccess,
		Result:    "success",
		SessionID: "session-1",
	})

	session.mu.Lock()
	if session.result.Result != "success" {
		t.Errorf("expected 'success', got %q", session.result.Result)
	}
	if !session.finished {
		t.Error("expected finished to be true")
	}
	session.mu.Unlock()
}

func TestRunningSessionFinishWithError(t *testing.T) {
	session := testSession()
	session.finish(shared.RunResult{
		Status:    shared.StatusError,
		Error:     "test error",
		SessionID: "session-err",
	})

	session.mu.Lock()
	if session.result.Status != shared.StatusError {
		t.Errorf("expected StatusError, got %v", session.result.Status)
	}
	if session.result.Error != "test error" {
		t.Errorf("expected 'test error', got %q", session.result.Error)
	}
	session.mu.Unlock()
}

func TestRunningSessionFinishCapturesStderrOnEmptyError(t *testing.T) {
	session := testSession()
	session.stderr.Write([]byte("stderr content"))

	session.finish(shared.RunResult{
		Status: shared.StatusError,
		Error:  "",
	})

	session.mu.Lock()
	defer session.mu.Unlock()
	if session.result.Error == "" {
		t.Error("expected stderr to be captured in error")
	}
}

func TestRunningSessionHandleUpdateSessionID(t *testing.T) {
	session := testSession()

	notif := acp.SessionNotification{
		SessionId: acp.SessionId("my-session"),
	}
	session.handleUpdate(notif)

	session.mu.Lock()
	sid := session.result.SessionID
	cur := session.curSession
	session.mu.Unlock()
	if sid != "my-session" {
		t.Errorf("expected session ID 'my-session', got %q", sid)
	}
	if cur != "my-session" {
		t.Errorf("expected curSession 'my-session', got %q", cur)
	}
}

func TestRunningSessionHandleUpdateWithAgentMessage(t *testing.T) {
	session := testSession()

	update := acp.SessionUpdate{
		AgentMessageChunk: &acp.SessionUpdateAgentMessageChunk{Content: acp.TextBlock("hello world")},
	}

	notif := acp.SessionNotification{
		SessionId: acp.SessionId("s1"),
		Update:    update,
	}
	session.handleUpdate(notif)

	session.mu.Lock()
	sid := session.result.SessionID
	session.mu.Unlock()
	if sid != "s1" {
		t.Errorf("expected session ID 's1', got %q", sid)
	}
}

func TestRunningSessionHandleUpdateWithAgentThought(t *testing.T) {
	session := testSession()

	update := acp.SessionUpdate{
		AgentThoughtChunk: &acp.SessionUpdateAgentThoughtChunk{Content: acp.TextBlock("thinking...")},
	}

	notif := acp.SessionNotification{
		SessionId: acp.SessionId("s2"),
		Update:    update,
	}
	session.handleUpdate(notif)

	session.mu.Lock()
	sid := session.result.SessionID
	session.mu.Unlock()
	if sid != "s2" {
		t.Errorf("expected session ID 's2', got %q", sid)
	}
}

func TestRunningSessionHandleUpdateWithToolCall(t *testing.T) {
	session := testSession()

	update := acp.SessionUpdate{
		ToolCall: &acp.SessionUpdateToolCall{
			ToolCallId: acp.ToolCallId("tool-123"),
			Title:      "ReadFile: /path/to/file",
		},
	}

	notif := acp.SessionNotification{
		SessionId: acp.SessionId("s3"),
		Update:    update,
	}
	session.handleUpdate(notif)

	session.mu.Lock()
	cur := session.curSession
	session.mu.Unlock()
	if cur != "s3" {
		t.Errorf("expected curSession 's3', got %q", cur)
	}
}

func TestRunningSessionHandleUpdateWithToolCallUpdate(t *testing.T) {
	session := testSession()

	updateTitle := "WriteFile: completed"
	update := acp.SessionUpdate{
		ToolCallUpdate: &acp.SessionToolCallUpdate{
			ToolCallId: acp.ToolCallId("tool-456"),
			Title:      &updateTitle,
		},
	}

	notif := acp.SessionNotification{
		SessionId: acp.SessionId("s4"),
		Update:    update,
	}
	session.handleUpdate(notif)

	session.mu.Lock()
	cur := session.curSession
	session.mu.Unlock()
	if cur != "s4" {
		t.Errorf("expected curSession 's4', got %q", cur)
	}
}

func TestRunningSessionCancelAlreadyFinished(t *testing.T) {
	session := &RunningSession{
		finished: true,
		cmd:      nil,
	}

	if err := session.Cancel(); err != nil {
		t.Errorf("Cancel() on finished session returned error: %v", err)
	}
}

func TestRunningSessionCancelNoProcess(t *testing.T) {
	session := &RunningSession{
		finished: false,
		cmd:      &exec.Cmd{},
	}

	if err := session.Cancel(); err != nil {
		t.Errorf("Cancel() with no process returned error: %v", err)
	}
}

func TestRunningSessionWait(t *testing.T) {
	done := make(chan struct{})
	session := &RunningSession{
		events: make(chan shared.Event, 32),
		done:   done,
		result: shared.RunResult{Status: shared.StatusSuccess, Result: "wait-result"},
	}

	go func() {
		time.Sleep(10 * time.Millisecond)
		close(done)
	}()

	result := session.Wait()
	if result.Result != "wait-result" {
		t.Errorf("expected 'wait-result', got %q", result.Result)
	}
}

func TestStderrBuffer(t *testing.T) {
	buf := &stderrBuffer{}

	n, err := buf.Write([]byte("line 1\n"))
	if err != nil {
		t.Fatalf("Write error: %v", err)
	}
	if n != 7 {
		t.Errorf("expected 7 bytes written, got %d", n)
	}

	if str := buf.String(); str != "line 1\n" {
		t.Errorf("expected 'line 1\\n', got %q", str)
	}
}

func TestStderrBufferConcurrentWrite(t *testing.T) {
	buf := &stderrBuffer{}

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				fmt.Fprintf(buf, "line %d-%d\n", n, j)
			}
		}(i)
	}
	wg.Wait()

	s := buf.String()
	if s == "" {
		t.Error("buffer should have content after concurrent writes")
	}
}

func TestCloseStdinOnce(t *testing.T) {
	r, w := io.Pipe()
	session := &RunningSession{
		events:     make(chan shared.Event, 32),
		done:       make(chan struct{}),
		stdin:      w,
		closeStdin: sync.Once{},
	}

	session.closeStdinOnce()
	session.closeStdinOnce()

	buf := make([]byte, 1)
	_, err := r.Read(buf)
	if err == nil {
		t.Error("expected error reading from closed pipe")
	}
}

func TestAbsDirWithEmptyPath(t *testing.T) {
	result := absDir("")
	if result == "" {
		t.Error("absDir('') should return non-empty path")
	}
	if !filepath.IsAbs(result) {
		t.Errorf("absDir('') should return absolute path, got %q", result)
	}
}

func TestAbsDirWithRelativePath(t *testing.T) {
	result := absDir(".")
	if result == "" {
		t.Error("absDir('.') should return non-empty path")
	}
	if !filepath.IsAbs(result) {
		t.Errorf("absDir('.') should return absolute path, got %q", result)
	}
}

func TestAbsDirWithValidPath(t *testing.T) {
	tmpDir := t.TempDir()
	result := absDir(tmpDir)
	if result != tmpDir {
		t.Errorf("expected %q, got %q", tmpDir, result)
	}
}

func TestContentBlockTextWithNilBlock(t *testing.T) {
	block := acp.ContentBlock{}
	result := contentBlockText(block)
	if result != "" {
		t.Errorf("expected empty string, got %q", result)
	}
}

func TestStopReasonTextWithWhitespace(t *testing.T) {
	result := stopReasonText(acp.StopReason("   "))
	if result != "" {
		t.Errorf("expected empty string for whitespace, got %q", result)
	}
}

func TestFindBinaryWithAbsolutePath(t *testing.T) {
	tmpDir := t.TempDir()
	scriptPath := filepath.Join(tmpDir, "test-binary")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\necho hello"), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	path, err := findBinary([]string{scriptPath})
	if err != nil {
		t.Errorf("findBinary(%q) failed: %v", scriptPath, err)
	}
	if path != scriptPath {
		t.Errorf("expected %q, got %q", scriptPath, path)
	}
}

func TestFindBinaryWithRelativePath(t *testing.T) {
	tmpDir := t.TempDir()
	origDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(origDir)

	scriptPath := "./test-script"
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\necho hello"), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	path, err := findBinary([]string{scriptPath})
	if err != nil {
		t.Errorf("findBinary(%q) failed: %v", scriptPath, err)
	}
	if path == "" {
		t.Error("expected non-empty path")
	}
}

func TestFindBinaryWithEmptyPaths(t *testing.T) {
	_, err := findBinary([]string{"", ""})
	if err == nil {
		t.Error("expected error for all empty paths")
	}
}

func TestClientInfoWithWhitespaceOnlyName(t *testing.T) {
	runner := Runner{ClientInfo: acp.Implementation{Name: "   ", Version: "1.0"}}
	info := runner.clientInfo()
	if info.Name != "pi-go" {
		t.Errorf("expected default 'pi-go', got %q", info.Name)
	}
}

func TestClientInfoWithEmptyName(t *testing.T) {
	runner := Runner{ClientInfo: acp.Implementation{Name: "", Version: "1.0"}}
	info := runner.clientInfo()
	if info.Name != "pi-go" {
		t.Errorf("expected default 'pi-go' for empty name, got %q", info.Name)
	}
}

func TestNewRunningSession(t *testing.T) {
	cmd := exec.Command("echo", "test")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatalf("stderr pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start command: %v", err)
	}
	defer cmd.Process.Kill()

	session := newRunningSession(cmd, stdin, stderr)

	if session.events == nil {
		t.Error("events channel should be initialized")
	}
	if session.done == nil {
		t.Error("done channel should be initialized")
	}
	if session.stderr == nil {
		t.Error("stderr buffer should be initialized")
	}
	if session.toolFilter == nil {
		t.Error("tool filter should be initialized")
	}
}

func TestRunningSessionEmitToFullChannel(t *testing.T) {
	session := &RunningSession{
		events: make(chan shared.Event, 1),
		done:   make(chan struct{}),
	}

	for i := 0; i < cap(session.events); i++ {
		session.events <- shared.Event{Type: "event"}
	}

	session.emit(shared.Event{Type: shared.EventTypeMessage, Content: "dropped"})

	count := 0
	for {
		select {
		case <-session.events:
			count++
		default:
			goto done
		}
	}
done:
	if count != cap(session.events) {
		t.Errorf("expected %d events, got %d", cap(session.events), count)
	}
}

func TestStartWithEmptyPrompt(t *testing.T) {
	runner := Runner{}
	_, err := runner.Start(context.Background(), RunRequest{Prompt: ""})
	if err == nil {
		t.Error("expected error for empty prompt")
	}
}

func TestStartWithWhitespaceOnlyPrompt(t *testing.T) {
	runner := Runner{}
	_, err := runner.Start(context.Background(), RunRequest{Prompt: "   "})
	if err == nil {
		t.Error("expected error for whitespace-only prompt")
	}
}

func TestWaitProcessSuccess(t *testing.T) {
	cmd := exec.Command("true")
	stderr, _ := cmd.StderrPipe()
	if err := cmd.Start(); err != nil {
		t.Fatalf("start command: %v", err)
	}

	session := newRunningSession(cmd, nil, stderr)
	err := session.waitProcess()
	if err != nil {
		t.Errorf("waitProcess() error = %v", err)
	}
}

func TestWaitProcessErrorWithStderr(t *testing.T) {
	cmd := exec.Command("sh", "-c", "echo error >&2 && exit 1")
	stderr, _ := cmd.StderrPipe()
	if err := cmd.Start(); err != nil {
		t.Fatalf("start command: %v", err)
	}

	session := newRunningSession(cmd, nil, stderr)
	err := session.waitProcess()
	if err == nil {
		t.Error("expected error from waitProcess")
	}
	if !strings.Contains(err.Error(), "error") {
		t.Errorf("expected error to contain 'error', got %v", err)
	}
}

func TestWaitProcessErrorWithoutStderr(t *testing.T) {
	cmd := exec.Command("false")
	stderr, _ := cmd.StderrPipe()
	if err := cmd.Start(); err != nil {
		t.Fatalf("start command: %v", err)
	}

	session := newRunningSession(cmd, nil, stderr)
	err := session.waitProcess()
	if err == nil {
		t.Error("expected error from waitProcess")
	}
}

func TestStreamStderr(t *testing.T) {
	r, w := io.Pipe()
	session := &RunningSession{
		events:     make(chan shared.Event, 32),
		done:       make(chan struct{}),
		stderr:     &stderrBuffer{},
		toolFilter: shared.NewToolCallTitleFilter(func(title string) {}),
	}

	go session.streamStderr(r)
	go func() {
		w.Write([]byte("line 1\n"))
		w.Write([]byte("line 2\n"))
		time.Sleep(10 * time.Millisecond)
		w.Close()
	}()

	time.Sleep(50 * time.Millisecond)
	s := session.stderr.String()
	if !strings.Contains(s, "line 1") || !strings.Contains(s, "line 2") {
		t.Errorf("expected stderr to contain lines, got %q", s)
	}
}

func TestStreamStderrEmptyLines(t *testing.T) {
	r, w := io.Pipe()
	session := &RunningSession{
		events:     make(chan shared.Event, 32),
		done:       make(chan struct{}),
		stderr:     &stderrBuffer{},
		toolFilter: shared.NewToolCallTitleFilter(func(title string) {}),
	}

	go session.streamStderr(r)
	go func() {
		w.Write([]byte("\n\n\n"))
		time.Sleep(10 * time.Millisecond)
		w.Close()
	}()

	time.Sleep(50 * time.Millisecond)
	s := session.stderr.String()
	if !strings.Contains(s, "\n\n\n") {
		t.Logf("stderr captured: %q", s)
	}
}

func TestStreamStderrNonASCII(t *testing.T) {
	r, w := io.Pipe()
	session := &RunningSession{
		events:     make(chan shared.Event, 32),
		done:       make(chan struct{}),
		stderr:     &stderrBuffer{},
		toolFilter: shared.NewToolCallTitleFilter(func(title string) {}),
	}

	go session.streamStderr(r)
	go func() {
		w.Write([]byte("你好\nこんにちは\n"))
		time.Sleep(10 * time.Millisecond)
		w.Close()
	}()

	time.Sleep(50 * time.Millisecond)
	s := session.stderr.String()
	if !strings.Contains(s, "你好") || !strings.Contains(s, "こんにちは") {
		t.Errorf("expected stderr to contain non-ASCII text, got %q", s)
	}
}

func TestStreamStderrLargeBuffer(t *testing.T) {
	r, w := io.Pipe()
	session := &RunningSession{
		events:     make(chan shared.Event, 32),
		done:       make(chan struct{}),
		stderr:     &stderrBuffer{},
		toolFilter: shared.NewToolCallTitleFilter(func(title string) {}),
	}

	go session.streamStderr(r)
	go func() {
		content := strings.Repeat("x", 10000) + "\n"
		w.Write([]byte(content))
		time.Sleep(10 * time.Millisecond)
		w.Close()
	}()

	time.Sleep(50 * time.Millisecond)
	s := session.stderr.String()
	if len(s) < 10000 {
		t.Errorf("expected at least 10000 chars, got %d", len(s))
	}
}

// TestRunWithValidCommand tests the runner with a valid command.
func TestRunWithValidCommand(t *testing.T) {
	runner := Runner{}
	session, err := runner.Start(context.Background(), RunRequest{
		Prompt:  "test",
		Command: []string{"true"},
	})
	if err != nil {
		t.Fatalf("Start() failed: %v", err)
	}

	result := session.Wait()
	// true exits successfully but doesn't speak ACP, so we might get an error.
	t.Logf("result: status=%s, result=%q, error=%q", result.Status, result.Result, result.Error)
}

// TestRunWithFalseCommand tests the runner with a command that fails.
func TestRunWithFalseCommand(t *testing.T) {
	runner := Runner{}
	session, err := runner.Start(context.Background(), RunRequest{
		Prompt:  "test",
		Command: []string{"false"},
	})
	if err != nil {
		t.Fatalf("Start() failed: %v", err)
	}

	result := session.Wait()
	// false exits with error.
	if result.Status == "" {
		t.Error("expected status to be set")
	}
}

// TestRunWithCatCommand tests the runner with cat (reads stdin).
func TestRunWithCatCommand(t *testing.T) {
	runner := Runner{}
	session, err := runner.Start(context.Background(), RunRequest{
		Prompt:  "test",
		Command: []string{"cat"},
	})
	if err != nil {
		t.Fatalf("Start() failed: %v", err)
	}

	result := session.Wait()
	t.Logf("result: status=%s, result=%q, error=%q", result.Status, result.Result, result.Error)
}

// TestRunningSessionRunWithStdoutCapture tests session with captured stdout.
func TestRunningSessionRunWithStdoutCapture(t *testing.T) {
	// Create a simple script that outputs JSON.
	scriptDir := t.TempDir()
	scriptPath := filepath.Join(scriptDir, "echo_json.sh")
	script := `#!/bin/bash
echo '{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":1,"agentInfo":{"name":"test","version":"1.0"}}}'
echo '{"jsonrpc":"2.0","id":2,"result":{"sessionId":"test-session"}}}'
echo '{"jsonrpc":"2.0","id":3,"result":{"stopReason":"end_turn"}}}'
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	cmd := exec.Command("/bin/bash", scriptPath)
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Fatalf("start command: %v", err)
	}
	defer cmd.Process.Kill()

	client := &mockClientForSession{}
	conn := acp.NewClientSideConnection(client, stdin, stdout)

	session := &RunningSession{
		cmd:        cmd,
		stdin:      stdin,
		stderr:     &stderrBuffer{},
		conn:       conn,
		events:     make(chan shared.Event, 32),
		done:       make(chan struct{}),
		toolFilter: shared.NewToolCallTitleFilter(func(title string) {}),
	}

	// Run the session.
	go session.run(RunRequest{Prompt: "test"}, acp.Implementation{Name: "test", Version: "1.0"})

	// Wait for completion.
	select {
	case <-session.done:
	case <-time.After(5 * time.Second):
		t.Error("session did not complete in time")
	}

	session.mu.Lock()
	result := session.result
	session.mu.Unlock()

	t.Logf("session completed with status=%s, result=%q, error=%q", result.Status, result.Result, result.Error)
}

// mockClientForSession implements acp.Client for testing.
type mockClientForSession struct{}

func (m *mockClientForSession) ReadTextFile(ctx context.Context, req acp.ReadTextFileRequest) (acp.ReadTextFileResponse, error) {
	return acp.ReadTextFileResponse{}, fmt.Errorf("not implemented")
}

func (m *mockClientForSession) WriteTextFile(ctx context.Context, req acp.WriteTextFileRequest) (acp.WriteTextFileResponse, error) {
	return acp.WriteTextFileResponse{}, fmt.Errorf("not implemented")
}

func (m *mockClientForSession) RequestPermission(ctx context.Context, req acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	return acp.RequestPermissionResponse{}, fmt.Errorf("not implemented")
}

func (m *mockClientForSession) SessionUpdate(ctx context.Context, params acp.SessionNotification) error {
	return nil
}

func (m *mockClientForSession) CreateTerminal(context.Context, acp.CreateTerminalRequest) (acp.CreateTerminalResponse, error) {
	return acp.CreateTerminalResponse{}, fmt.Errorf("not implemented")
}

func (m *mockClientForSession) KillTerminal(context.Context, acp.KillTerminalRequest) (acp.KillTerminalResponse, error) {
	return acp.KillTerminalResponse{}, fmt.Errorf("not implemented")
}

func (m *mockClientForSession) TerminalOutput(context.Context, acp.TerminalOutputRequest) (acp.TerminalOutputResponse, error) {
	return acp.TerminalOutputResponse{}, fmt.Errorf("not implemented")
}

func (m *mockClientForSession) ReleaseTerminal(context.Context, acp.ReleaseTerminalRequest) (acp.ReleaseTerminalResponse, error) {
	return acp.ReleaseTerminalResponse{}, fmt.Errorf("not implemented")
}

func (m *mockClientForSession) WaitForTerminalExit(context.Context, acp.WaitForTerminalExitRequest) (acp.WaitForTerminalExitResponse, error) {
	return acp.WaitForTerminalExitResponse{}, fmt.Errorf("not implemented")
}
