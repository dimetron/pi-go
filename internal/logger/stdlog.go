package logger

import (
	"io"
	"log"
	"os"
	"strings"
	"sync"
)

// maxHeldStdLogLines bounds what is held between RedirectStdLog and Attach, so
// a chatty dependency cannot grow memory while the session log is still being
// created.
const maxHeldStdLogLines = 64

// StdLog diverts the standard library's default logger away from the terminal.
//
// Dependencies such as adk call log.Printf, which writes to stderr. On the
// TUI's alternate screen that output is painted into the frame and stays there
// until the next full repaint. The session log does not exist yet when the TUI
// starts, so lines are held until Attach hands them a destination.
type StdLog struct {
	mu   sync.Mutex
	dst  *Logger
	shut bool // Detach ran: discard instead of holding
	held []string
	prev io.Writer
	flag int
}

// RedirectStdLog points log's default output at the returned StdLog. It must
// run before anything that can log, which means before the TUI takes the
// screen. Call Restore to put the previous output back.
func RedirectStdLog() *StdLog {
	s := &StdLog{prev: log.Writer(), flag: log.Flags()}
	log.SetOutput(s)
	// The session log timestamps every entry itself.
	log.SetFlags(0)
	return s
}

// Write implements io.Writer for the standard logger.
func (s *StdLog) Write(p []byte) (int, error) {
	line := strings.TrimRight(string(p), "\r\n")
	s.mu.Lock()
	dst := s.dst
	if dst == nil && !s.shut && len(s.held) < maxHeldStdLogLines {
		s.held = append(s.held, line)
	}
	s.mu.Unlock()
	// Logged outside the lock: Logger takes its own.
	if dst != nil {
		dst.Info(line)
	}
	return len(p), nil
}

// Attach sends held and later lines to l. A nil l is ignored.
func (s *StdLog) Attach(l *Logger) {
	if s == nil || l == nil {
		return
	}
	s.mu.Lock()
	held := s.held
	s.held = nil
	s.dst = l
	s.shut = false
	s.mu.Unlock()
	for _, line := range held {
		l.Info(line)
	}
}

// Detach stops forwarding, so a late line cannot reach a closed Logger. Lines
// after Detach are discarded rather than returned to the terminal.
func (s *StdLog) Detach() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.dst = nil
	s.held = nil
	s.shut = true
	s.mu.Unlock()
}

// Restore reinstates the logger output and flags that were in place before
// RedirectStdLog.
func (s *StdLog) Restore() {
	if s == nil {
		return
	}
	s.Detach()
	if s.prev == nil {
		s.prev = os.Stderr
	}
	log.SetOutput(s.prev)
	log.SetFlags(s.flag)
}
