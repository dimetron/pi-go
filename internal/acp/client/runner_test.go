package client

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	shared "github.com/dimetron/pi-go/internal/acp"
)

func TestRunnerStartRejectsInvalidRequest(t *testing.T) {
	runner := Runner{}
	_, err := runner.Start(context.Background(), shared.RunRequest{})
	if err == nil {
		t.Fatal("expected validation error")
	}
}

func TestRunnerCompletesPromptTurn(t *testing.T) {
	runner := Runner{}
	cmd := []string{os.Args[0], "-test.run=TestACPClientHelperProcess", "--"}
	t.Setenv("GO_WANT_HELPER_PROCESS", "1")
	session, err := runner.Start(context.Background(), shared.RunRequest{
		Command: cmd,
		Prompt:  "hello acp",
		CWD:     t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	var messages []shared.Event
	for event := range session.Events() {
		if event.Type == shared.EventTypeMessage {
			messages = append(messages, event)
		}
	}

	result := session.Wait()
	if result.Status != shared.StatusSuccess {
		t.Fatalf("status = %q, want %q (error=%q)", result.Status, shared.StatusSuccess, result.Error)
	}
	if got := strings.TrimSpace(result.Result); got != "echo: hello acp" {
		t.Fatalf("result = %q, want %q", got, "echo: hello acp")
	}
	if result.SessionID != "session-1" {
		t.Fatalf("session id = %q, want session-1", result.SessionID)
	}
	if len(messages) != 1 {
		t.Fatalf("message events len = %d, want 1", len(messages))
	}
	if strings.TrimSpace(messages[0].Content) != "echo: hello acp" {
		t.Fatalf("unexpected event: %+v", messages[0])
	}
}

func TestRunningSessionCancel(t *testing.T) {
	// Any process that stays alive until it is killed will do. Re-exec the test
	// binary as the ACP helper agent rather than a #!/bin/sh script, which
	// Windows cannot run: the helper blocks reading stdio just the same.
	t.Setenv("GO_WANT_HELPER_PROCESS", "1")
	cmd := exec.Command(os.Args[0], "-test.run=TestACPClientHelperProcess", "--")
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatalf("stderr pipe: %v", err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start command: %v", err)
	}

	session := newRunningSession(cmd, stdin, stderr)
	if err := session.Cancel(); err != nil {
		t.Fatalf("Cancel() error = %v", err)
	}
	if err := session.waitProcess(); err == nil {
		t.Fatal("expected process wait error after cancel")
	}
}

func TestACPClientHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	if err := runHelperACPAgent(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}
