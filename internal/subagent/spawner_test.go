package subagent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// mockPiScript creates a temporary shell script that mimics pi --mode json output.
func mockPiScript(t *testing.T, script string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "mock-pi")
	err := os.WriteFile(path, []byte("#!/bin/bash\n"+script), 0o755)
	if err != nil {
		t.Fatalf("writing mock script: %v", err)
	}
	return path
}

func TestSpawner_BasicExecution(t *testing.T) {
	// Mock pi that emits a simple JSONL sequence.
	script := `
echo '{"type":"message_start","agent":"test","role":"model"}'
echo '{"type":"text_delta","delta":"Hello "}'
echo '{"type":"text_delta","delta":"world"}'
echo '{"type":"message_end"}'
`
	binary := mockPiScript(t, script)
	spawner := NewSpawner(binary)

	proc, err := spawner.Spawn(context.Background(), SpawnOpts{
		AgentID: "test-1",
		Prompt:  "say hello",
	})
	if err != nil {
		t.Fatalf("spawn failed: %v", err)
	}

	result, err := proc.Wait()
	if err != nil {
		t.Fatalf("wait failed: %v", err)
	}

	if result != "Hello world" {
		t.Errorf("expected 'Hello world', got %q", result)
	}
}

func TestSpawner_StreamingEvents(t *testing.T) {
	script := `
echo '{"type":"message_start","agent":"test","role":"model"}'
echo '{"type":"text_delta","delta":"chunk1"}'
echo '{"type":"tool_call","tool_name":"read","tool_input":{"file":"test.go"}}'
echo '{"type":"tool_result","tool_name":"read","content":"file contents"}'
echo '{"type":"text_delta","delta":"chunk2"}'
echo '{"type":"message_end"}'
`
	binary := mockPiScript(t, script)
	spawner := NewSpawner(binary)

	proc, err := spawner.Spawn(context.Background(), SpawnOpts{
		AgentID: "test-stream",
		Prompt:  "read a file",
	})
	if err != nil {
		t.Fatalf("spawn failed: %v", err)
	}

	var events []Event
	for ev := range proc.Events() {
		events = append(events, ev)
	}

	// Verify event sequence.
	expectedTypes := []string{"message_start", "text_delta", "tool_call", "tool_result", "text_delta", "message_end"}
	if len(events) != len(expectedTypes) {
		t.Fatalf("expected %d events, got %d: %+v", len(expectedTypes), len(events), events)
	}
	for i, et := range expectedTypes {
		if events[i].Type != et {
			t.Errorf("event %d: expected type %q, got %q", i, et, events[i].Type)
		}
	}

	// Verify specific content.
	if events[1].Content != "chunk1" {
		t.Errorf("event 1 content: expected 'chunk1', got %q", events[1].Content)
	}
	if events[2].Content != "read" {
		t.Errorf("event 2 content: expected 'read', got %q", events[2].Content)
	}

	result, err := proc.Wait()
	if err != nil {
		t.Fatalf("wait failed: %v", err)
	}
	if result != "chunk1chunk2" {
		t.Errorf("expected result 'chunk1chunk2', got %q", result)
	}
}

func TestSpawner_ProcessCrash(t *testing.T) {
	script := `
echo '{"type":"message_start"}'
echo '{"type":"text_delta","delta":"partial"}'
echo "something went wrong" >&2
exit 1
`
	binary := mockPiScript(t, script)
	spawner := NewSpawner(binary)

	proc, err := spawner.Spawn(context.Background(), SpawnOpts{
		AgentID: "test-crash",
		Prompt:  "crash test",
	})
	if err != nil {
		t.Fatalf("spawn failed: %v", err)
	}

	// Drain events.
	for range proc.Events() {
	}

	result, err := proc.Wait()
	if err == nil {
		t.Fatal("expected error from crashed process")
	}
	if !strings.Contains(err.Error(), "something went wrong") {
		t.Errorf("expected stderr in error, got: %v", err)
	}
	// Partial result should still be captured.
	if result != "partial" {
		t.Errorf("expected partial result 'partial', got %q", result)
	}
}

func TestSpawner_Cancel(t *testing.T) {
	// Script that runs forever.
	script := `
echo '{"type":"message_start"}'
echo '{"type":"text_delta","delta":"started"}'
sleep 60
`
	binary := mockPiScript(t, script)
	spawner := NewSpawner(binary)

	proc, err := spawner.Spawn(context.Background(), SpawnOpts{
		AgentID: "test-cancel",
		Prompt:  "long task",
	})
	if err != nil {
		t.Fatalf("spawn failed: %v", err)
	}

	// Give it time to start and emit initial events.
	time.Sleep(100 * time.Millisecond)

	// Cancel the process.
	proc.Cancel()

	_, err = proc.Wait()
	if err == nil {
		t.Fatal("expected error from cancelled process")
	}
}

func TestSpawner_ContextTimeout(t *testing.T) {
	script := `
echo '{"type":"message_start"}'
sleep 60
`
	binary := mockPiScript(t, script)
	spawner := NewSpawner(binary)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	proc, err := spawner.Spawn(ctx, SpawnOpts{
		AgentID: "test-timeout",
		Prompt:  "timeout task",
	})
	if err != nil {
		t.Fatalf("spawn failed: %v", err)
	}

	_, err = proc.Wait()
	if err == nil {
		t.Fatal("expected error from timed-out process")
	}
}

func TestSpawner_EmptyPrompt(t *testing.T) {
	spawner := NewSpawner("echo")

	_, err := spawner.Spawn(context.Background(), SpawnOpts{
		AgentID: "test-empty",
		Prompt:  "",
	})
	if err == nil {
		t.Fatal("expected error for empty prompt")
	}
	if !strings.Contains(err.Error(), "prompt is required") {
		t.Errorf("expected 'prompt is required' error, got: %v", err)
	}
}

func TestSpawner_WorkDir(t *testing.T) {
	// Script that prints the working directory.
	script := `
echo "{\"type\":\"text_delta\",\"delta\":\"$(pwd)\"}"
echo '{"type":"message_end"}'
`
	binary := mockPiScript(t, script)
	spawner := NewSpawner(binary)

	dir := t.TempDir()

	proc, err := spawner.Spawn(context.Background(), SpawnOpts{
		AgentID: "test-workdir",
		Prompt:  "check dir",
		WorkDir: dir,
	})
	if err != nil {
		t.Fatalf("spawn failed: %v", err)
	}

	result, err := proc.Wait()
	if err != nil {
		t.Fatalf("wait failed: %v", err)
	}

	// On macOS, /tmp resolves to /private/tmp.
	if !strings.Contains(result, filepath.Base(dir)) {
		t.Errorf("expected result to contain workdir base %q, got %q", filepath.Base(dir), result)
	}
}

func TestSpawner_DefaultBinary(t *testing.T) {
	s := NewSpawner("")
	if s.PiBinary != "pi" {
		t.Errorf("expected default binary 'pi', got %q", s.PiBinary)
	}

	s2 := NewSpawner("/usr/local/bin/pi")
	if s2.PiBinary != "/usr/local/bin/pi" {
		t.Errorf("expected custom binary, got %q", s2.PiBinary)
	}
}
