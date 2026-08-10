package tools

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestBash_OversizedOutputReportsWhatItLost is the guard against a silent
// truncated transcript.
//
// The stream buffers keep the most recent 256KB and discard everything before
// it (bash_stream.go). That is the right end to keep — a command's verdict is
// at the bottom — but finish() reported nothing about the discard, so output
// that began mid-stream was indistinguishable from output that began at the
// beginning. The model then reasons about "the output" as if it saw all of it.
func TestBash_OversizedOutputReportsWhatItLost(t *testing.T) {
	const verdict = "FAIL github.com/dimetron/pi-go/internal/tools [build failed]"

	sup := NewBashSupervisor()
	t.Cleanup(sup.KillAll)

	// Well over one buffer's worth, then the line that actually matters.
	script := `for i in $(seq 1 20000); do echo "ok   github.com/dimetron/pi-go/internal/pkg$i	0.01s"; done; echo "` + verdict + `"`

	out, err := sup.Run(context.Background(), runRequest{
		dir:     t.TempDir(),
		command: script,
		timeout: 60 * time.Second,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(out.Stdout) > maxOutputBytes {
		t.Errorf("stdout is %d bytes, over the %d cap", len(out.Stdout), maxOutputBytes)
	}
	// The tail is the part worth keeping, and the buffer already keeps it.
	if !strings.Contains(out.Stdout, verdict) {
		tail := out.Stdout[max(0, len(out.Stdout)-200):]
		t.Errorf("the failing last line was dropped; output ends with %q", tail)
	}
	// The head is genuinely gone — and that must be said out loud.
	if !strings.Contains(out.Note, "discarded to stay within the output buffer") {
		t.Errorf("Note does not disclose the discarded head: %q", out.Note)
	}
	if !strings.Contains(out.Note, "starts mid-stream") {
		t.Errorf("Note does not warn that the transcript is partial: %q", out.Note)
	}
}

// TestBash_SmallOutputCarriesNoNote keeps the common case quiet: a command that
// fits the buffer must not gain explanatory text it does not need.
func TestBash_SmallOutputCarriesNoNote(t *testing.T) {
	sup := NewBashSupervisor()
	t.Cleanup(sup.KillAll)

	out, err := sup.Run(context.Background(), runRequest{
		dir:     t.TempDir(),
		command: "echo hello",
		timeout: 10 * time.Second,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.TrimSpace(out.Stdout) != "hello" {
		t.Errorf("stdout = %q, want %q", out.Stdout, "hello")
	}
	if out.Note != "" {
		t.Errorf("Note should be empty for output that fits: %q", out.Note)
	}
}

// TestStream_DroppedBytesCountsTheDiscardedPrefix pins the primitive the note
// is built on.
func TestStream_DroppedBytesCountsTheDiscardedPrefix(t *testing.T) {
	s := newStream(10)

	if got := s.droppedBytes(); got != 0 {
		t.Errorf("fresh stream reports %d dropped, want 0", got)
	}

	s.Write([]byte("0123456789")) // exactly fills it
	if got := s.droppedBytes(); got != 0 {
		t.Errorf("full-but-not-over stream reports %d dropped, want 0", got)
	}

	s.Write([]byte("abcde")) // pushes 5 bytes out of the front
	if got := s.droppedBytes(); got != 5 {
		t.Errorf("droppedBytes = %d, want 5", got)
	}
	if got := s.String(); got != "56789abcde" {
		t.Errorf("retained tail = %q, want %q", got, "56789abcde")
	}
}
