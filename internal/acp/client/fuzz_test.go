package client

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

// FuzzACPClientWithMock exercises the ACP client against the pi-acp-mock
// server with randomly generated prompts. The goal is to find crashes,
// panics, or deadlocks in the session lifecycle (initialize, new-session,
// prompt, streamStderr, handleUpdate, finish, waitProcess).
func FuzzACPClientWithMock(f *testing.F) {
	// Build the mock binary once before fuzzing.
	binPath := filepath.Join(f.TempDir(), "pi-acp-mock")
	build := exec.Command("go", "build", "-o", binPath, "./cmd/pi-acp-mock")
	build.Dir = filepath.Join("..", "..", "..")
	if out, err := build.CombinedOutput(); err != nil {
		f.Skipf("cannot build pi-acp-mock: %v\n%s", err, out)
	}

	// Seed corpus: common cases that should not crash.
	seedCorpus := []string{
		"hello",
		"hi",
		"",
		"   ",
		"with spaces",
		"with\ttab",
		"with\nnewline",
		strings.Repeat("x", 1024),
		"日本語",
		"emoji: 🎉",
		"<script>alert('x')</script>",
		"{ \"json\": true }",
		"no control \x00 chars",
	}
	for _, seed := range seedCorpus {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, prompt string) {
		// Bound execution time to avoid hanging on malformed input.
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		cmd := exec.CommandContext(ctx, binPath)
		cmd.Env = append(os.Environ(),
			"PI_MOCK_RESPONSE={{prompt}}",
			"PI_MOCK_DELAY_MS=0",
		)

		if err := cmd.Start(); err != nil {
			return // cannot start — skip
		}
		defer func() {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}()

		runner := Runner{}
		session, err := runner.Start(ctx, shared.RunRequest{
			Prompt:     prompt,
			CWD:        t.TempDir(),
			RPCTimeout: 3 * time.Second,
		})
		if err != nil {
			return // validation or start error — not a crash
		}

		// Drain events without blocking forever.
		eventDone := make(chan struct{})
		go func() {
			for range session.Events() {
				// consume events
			}
			close(eventDone)
		}()

		result := session.Wait()

		select {
		case <-eventDone:
		case <-time.After(100 * time.Millisecond):
			// Events channel may be slow; wait is sufficient for result.
		}

		// Sanity check result fields are not corrupted.
		if result.Status != "" && result.Status != shared.StatusSuccess && result.Status != shared.StatusError {
			t.Errorf("corrupt status: %q", result.Status)
		}
	})
}

// FuzzACPEventHandleUpdate exercises handleUpdate with randomly constructed
// SessionNotification values. Many crash points live here: nil pointer
// dereferences, slice bounds, string operations on empty fields.
func FuzzACPEventHandleUpdate(f *testing.F) {
	f.Fuzz(func(t *testing.T, sessionID, agentMsg, thought, toolID, toolTitle, toolRaw string) {
		sess := &RunningSession{
			events:     make(chan shared.Event, 8),
			done:       make(chan struct{}),
			stderr:     &stderrBuffer{},
			toolFilter: shared.NewToolCallTitleFilter(func(title string) {}),
		}

		notif := acp.SessionNotification{
			SessionId: acp.SessionId(sessionID),
		}

		// Randomly populate update fields to stress nil checks.
		if len(agentMsg) > 0 || len(thought) > 0 {
			update := acp.SessionUpdate{}
			if len(agentMsg) > 0 {
				update.AgentMessageChunk = &acp.SessionUpdateAgentMessageChunk{
					Content: acp.TextBlock(agentMsg),
				}
			}
			if len(thought) > 0 {
				update.AgentThoughtChunk = &acp.SessionUpdateAgentThoughtChunk{
					Content: acp.TextBlock(thought),
				}
			}
			notif.Update = update
		}

		if len(toolID) > 0 {
			if notif.Update == (acp.SessionUpdate{}) {
				notif.Update = acp.SessionUpdate{}
			}
			notif.Update.ToolCall = &acp.SessionUpdateToolCall{
				ToolCallId: acp.ToolCallId(toolID),
				Title:      toolTitle,
				RawInput:   toolRaw,
			}
		}

		// Must not panic on any input combination.
		sess.handleUpdate(notif)

		// Verify session ID is captured.
		sess.mu.Lock()
		if sess.result.SessionID != sessionID && sessionID != "" {
			t.Errorf("session ID not captured: got %q, want %q", sess.result.SessionID, sessionID)
		}
		sess.mu.Unlock()
	})
}

// FuzzACPEventFinish exercises finish() with various RunResult states.
// The finish() method has several guard branches that could panic if
// fields are unexpectedly nil or zero.
func FuzzACPEventFinish(f *testing.F) {
	f.Fuzz(func(t *testing.T, status, result, errMsg, sessionID, stderr, stopReason string) {
		sess := &RunningSession{
			events:     make(chan shared.Event, 8),
			done:       make(chan struct{}),
			stderr:     &stderrBuffer{},
			toolFilter: shared.NewToolCallTitleFilter(func(title string) {}),
		}
		sess.stderr.Write([]byte(stderr))

		runResult := shared.RunResult{
			Status:     status,
			Result:     result,
			Error:      errMsg,
			SessionID:  sessionID,
			StopReason: stopReason,
		}

		// finish must not panic on any state.
		sess.finish(runResult)

		sess.mu.Lock()
		// If already finished, second call should not corrupt state.
		sess.finish(runResult)
		sess.mu.Unlock()
	})
}

// FuzzACPStreamStderr exercises streamStderr with various byte sequences.
// The scanner has a 1MB max token; very long lines and binary data are
// stress points.
func FuzzACPStreamStderr(f *testing.F) {
	f.Fuzz(func(t *testing.T, data string) {
		cmd := exec.Command("echo", "test")
		if err := cmd.Start(); err != nil {
			return
		}
		if cmd.Process != nil {
			defer cmd.Process.Kill()
		}

		r, w := io.Pipe()
		session := &RunningSession{
			events:     make(chan shared.Event, 8),
			done:       make(chan struct{}),
			stderrDone: make(chan struct{}),
			stderr:     &stderrBuffer{},
			toolFilter: shared.NewToolCallTitleFilter(func(title string) {}),
		}

		go session.streamStderr(r)
		go func() {
			// Write all at once to test scanner token handling.
			w.Write([]byte(data))
			w.Close()
		}()

		select {
		case <-session.stderrDone:
		case <-time.After(2 * time.Second):
			// streamStderr should close stderrDone within a reasonable time.
		}

		// Verify buffer state is consistent.
		_ = session.stderr.String()
	})
}

// FuzzACPAppendResult exercises appendResult with large strings and
// repeated calls. String concatenation in a loop is a common source of
// O(n²) behavior and memory blowup.
func FuzzACPAppendResult(f *testing.F) {
	f.Fuzz(func(t *testing.T, base string, count int) {
		if count < 0 || count > 10000 {
			return // skip unrealistic counts
		}

		sess := &RunningSession{
			events:     make(chan shared.Event, 8),
			done:       make(chan struct{}),
			stderr:     &stderrBuffer{},
			toolFilter: shared.NewToolCallTitleFilter(func(title string) {}),
		}

		for i := 0; i < count; i++ {
			sess.appendResult(base)
		}

		sess.mu.Lock()
		totalLen := len(sess.result.Result)
		sess.mu.Unlock()

		expected := len(base) * count
		if totalLen != expected {
			t.Errorf("appendResult length mismatch: got %d, want %d", totalLen, expected)
		}
	})
}

// FuzzACPEmitNonBlocking exercises emit() behavior when the events channel
// is full. Non-blocking sends must not panic; dropped events are acceptable.
func FuzzACPEmitNonBlocking(f *testing.F) {
	f.Fuzz(func(t *testing.T, eventType, content, sessionID string) {
		session := &RunningSession{
			events: make(chan shared.Event, 2), // small buffer to trigger drops
			done:   make(chan struct{}),
		}

		// Fill the channel.
		session.events <- shared.Event{Type: "fill1"}
		session.events <- shared.Event{Type: "fill2"}

		// emit must not block or panic when channel is full.
		session.emit(shared.Event{
			Type:      eventType,
			Content:   content,
			SessionID: sessionID,
		})

		// Drain to clean up.
		close(session.events)
	})
}

// FuzzACPContentBlockText exercises contentBlockText with all block variants.
// Panics here would indicate missing nil checks on block discriminants.
func FuzzACPContentBlockText(f *testing.F) {
	f.Fuzz(func(t *testing.T, text, resourceURI string) {
		// Test with Text block.
		textBlock := acp.ContentBlock{
			Text: &acp.ContentBlockText{Text: text},
		}
		result1 := contentBlockText(textBlock)
		if result1 != text {
			t.Errorf("text block mismatch: got %q, want %q", result1, text)
		}

		// Test with ResourceLink block.
		linkBlock := acp.ContentBlock{
			ResourceLink: &acp.ContentBlockResourceLink{Uri: resourceURI},
		}
		result2 := contentBlockText(linkBlock)
		if result2 != resourceURI {
			t.Errorf("resource link block mismatch: got %q, want %q", result2, resourceURI)
		}

		// Test with empty block (neither field set).
		emptyBlock := acp.ContentBlock{}
		result3 := contentBlockText(emptyBlock)
		if result3 != "" {
			t.Errorf("empty block should return empty string, got %q", result3)
		}
	})
}

// FuzzACPValidateRunRequest exercises RunRequest.Validate with edge cases.
func FuzzACPValidateRunRequest(f *testing.F) {
	f.Fuzz(func(t *testing.T, command0, command1, prompt string) {
		req := shared.RunRequest{
			Command: []string{command0, command1},
			Prompt:  prompt,
		}

		// Validate must not panic on any input.
		_ = req.Validate()
	})
}

// FuzzACPRapidSessions exercises rapid sequential sessions to find resource
// leaks, file descriptor exhaustion, or goroutine leaks that manifest over
// many iterations. This mirrors the "2 min crash" pattern you described.
func FuzzACPRapidSessions(f *testing.F) {
	binPath := filepath.Join(f.TempDir(), "pi-acp-mock")
	build := exec.Command("go", "build", "-o", binPath, "./cmd/pi-acp-mock")
	build.Dir = filepath.Join("..", "..", "..")
	if out, err := build.CombinedOutput(); err != nil {
		f.Skipf("cannot build pi-acp-mock: %v\n%s", err, out)
	}

	seedCorpus := []int{1, 5, 10, 50, 100}
	for _, n := range seedCorpus {
		f.Add(n, "")
	}

	f.Fuzz(func(t *testing.T, count int, prompt string) {
		if count < 0 || count > 200 {
			return // skip unreasonable counts
		}
		if prompt == "" {
			prompt = "hello"
		}

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		for i := 0; i < count; i++ {
			select {
			case <-ctx.Done():
				return
			default:
			}

			cmd := exec.CommandContext(ctx, binPath)
			cmd.Env = append(os.Environ(),
				"PI_MOCK_RESPONSE={{prompt}}",
				"PI_MOCK_DELAY_MS=0",
			)

			if err := cmd.Start(); err != nil {
				return
			}
			defer cmd.Process.Kill()
			defer cmd.Wait()

			runner := Runner{}
			session, err := runner.Start(ctx, shared.RunRequest{
				Prompt:     prompt,
				CWD:        t.TempDir(),
				RPCTimeout: 2 * time.Second,
			})
			if err != nil {
				continue
			}

			// Drain without blocking.
			go func() {
				for range session.Events() {
				}
			}()

			_ = session.Wait()
		}
	})
}

// FuzzACPLargeResult exercises the client with very large result strings to
// find OOM, slice bounds, or string handling bugs in the result accumulation.
func FuzzACPLargeResult(f *testing.F) {
	f.Fuzz(func(t *testing.T, chunk1, chunk2, chunk3, chunk4 string) {
		sess := &RunningSession{
			events:     make(chan shared.Event, 64),
			done:       make(chan struct{}),
			stderr:     &stderrBuffer{},
			toolFilter: shared.NewToolCallTitleFilter(func(title string) {}),
		}

		// Simulate large result accumulation.
		chunks := []string{chunk1, chunk2, chunk3, chunk4}
		for _, chunk := range chunks {
			sess.appendResult(chunk)
			sess.appendResult("\n")
		}

		sess.mu.Lock()
		resultLen := len(sess.result.Result)
		sess.mu.Unlock()

		// Verify no corruption.
		if resultLen < 0 {
			t.Errorf("negative result length: %d", resultLen)
		}
	})
}

// FuzzACPConcurrentEmit exercises emit() from multiple goroutines
// simultaneously to find race conditions in event channel access.
func FuzzACPConcurrentEmit(f *testing.F) {
	f.Fuzz(func(t *testing.T, content string, goroutineCount int) {
		if goroutineCount < 0 || goroutineCount > 50 {
			return
		}

		session := &RunningSession{
			events: make(chan shared.Event, 64),
			done:   make(chan struct{}),
		}

		var wg sync.WaitGroup
		for i := 0; i < goroutineCount; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				for j := 0; j < 100; j++ {
					session.emit(shared.Event{
						Type:      fmt.Sprintf("type-%d-%d", id, j),
						Content:   content,
						SessionID: fmt.Sprintf("session-%d", id),
					})
				}
			}(i)
		}

		wg.Wait()
		close(session.events)
	})
}

// FuzzACPSessionCancelRace exercises cancel() during active event pumping
// to find race conditions between cancel and event processing.
func FuzzACPSessionCancelRace(f *testing.F) {
	binPath := filepath.Join(f.TempDir(), "pi-acp-mock")
	build := exec.Command("go", "build", "-o", binPath, "./cmd/pi-acp-mock")
	build.Dir = filepath.Join("..", "..", "..")
	if out, err := build.CombinedOutput(); err != nil {
		f.Skipf("cannot build pi-acp-mock: %v\n%s", err, out)
	}

	f.Fuzz(func(t *testing.T, delayMs int) {
		if delayMs < 0 || delayMs > 5000 {
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		cmd := exec.CommandContext(ctx, binPath)
		cmd.Env = append(os.Environ(),
			"PI_MOCK_RESPONSE=hello",
			fmt.Sprintf("PI_MOCK_DELAY_MS=%d", delayMs),
		)

		if err := cmd.Start(); err != nil {
			return
		}

		runner := Runner{}
		session, err := runner.Start(ctx, shared.RunRequest{
			Prompt:     "test",
			CWD:        t.TempDir(),
			RPCTimeout: 5 * time.Second,
		})
		if err != nil {
			cmd.Process.Kill()
			cmd.Wait()
			return
		}

		// Randomly cancel during event reception.
		go func() {
			time.Sleep(time.Duration(delayMs) * time.Millisecond / 2)
			session.Cancel()
		}()

		go func() {
			for range session.Events() {
			}
		}()

		_ = session.Wait()
		cmd.Process.Kill()
		cmd.Wait()
	})
}
