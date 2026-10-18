package logger

import (
	"log"
	"os"
	"strings"
	"testing"
)

func newStdLogTestLogger(t *testing.T) *Logger {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	l, err := New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return l
}

func TestStdLog_HoldsThenForwardsInOrder(t *testing.T) {
	l := newStdLogTestLogger(t)
	s := RedirectStdLog()
	t.Cleanup(s.Restore)

	log.Printf("first")
	s.Attach(l)
	log.Printf("second")
	_ = l.Close()

	data, err := os.ReadFile(l.Path())
	if err != nil {
		t.Fatal(err)
	}
	out := string(data)
	i, j := strings.Index(out, "first"), strings.Index(out, "second")
	if i < 0 || j < 0 || i > j {
		t.Fatalf("want first before second in session log, got %q", out)
	}
}

func TestStdLog_NeverReachesStderr(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	prev := os.Stderr
	os.Stderr = w
	t.Cleanup(func() { os.Stderr = prev })
	log.SetOutput(os.Stderr)

	s := RedirectStdLog()
	log.Printf("orphan")
	s.Detach()
	log.Printf("late")
	s.Restore()
	_ = w.Close()

	buf := make([]byte, 64)
	n, _ := r.Read(buf)
	if n != 0 {
		t.Fatalf("stderr received %q", buf[:n])
	}
}

func TestStdLog_RestoreReinstatesOutput(t *testing.T) {
	var sb strings.Builder
	log.SetOutput(&sb)
	log.SetFlags(0)
	s := RedirectStdLog()
	s.Restore()
	log.Printf("back")
	if sb.String() != "back\n" {
		t.Fatalf("got %q", sb.String())
	}
}
