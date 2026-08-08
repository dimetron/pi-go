package tools

import (
	"sync"
)

// streamCap bounds how much of one output channel is retained. A command that
// prints a gigabyte still runs to completion; only its tail is kept, because
// the tail is what the model is given and what the user is shown.
const streamCap = maxOutputBytes

// stream is the retained tail of one output channel of a running command,
// addressed by absolute byte offset.
//
// Absolute offsets are what make incremental reads honest under a bounded
// buffer. A reader that asks for "everything after offset N" can be told that
// bytes it never saw were discarded, instead of silently receiving a later
// chunk as though it followed directly on from N. Without that distinction a
// slow reader of a noisy command would be handed a plausible, wrong transcript.
type stream struct {
	mu    sync.Mutex
	data  []byte // retained tail, at most cap bytes
	base  int64  // absolute offset of data[0]
	end   int64  // absolute offset one past the last byte written
	limit int
	// notify is closed and replaced on every write, so any number of readers
	// can wait for "something changed" without a per-reader channel.
	notify chan struct{}
}

func newStream(limit int) *stream {
	if limit <= 0 {
		limit = streamCap
	}
	return &stream{limit: limit, notify: make(chan struct{})}
}

// Write appends p to the tail, discarding the oldest bytes past the limit.
// It never returns an error: dropping old output is the designed behavior, not
// a failure, and returning one would make the copying goroutine tear down a
// command that is running perfectly well.
func (s *stream) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	s.mu.Lock()
	s.data = append(s.data, p...)
	s.end += int64(len(p))
	if over := len(s.data) - s.limit; over > 0 {
		s.data = s.data[over:]
		s.base += int64(over)
	}
	// Wake every waiter by closing the current notify channel and installing a
	// fresh one for the next write.
	close(s.notify)
	s.notify = make(chan struct{})
	s.mu.Unlock()
	return len(p), nil
}

// since returns the retained bytes at or after off, the offset to pass next
// time, and how many bytes between off and the returned data were dropped
// because they aged out of the buffer.
func (s *stream) since(off int64) (data string, next int64, dropped int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if off < s.base {
		dropped = s.base - off
		off = s.base
	}
	if off >= s.end {
		return "", s.end, dropped
	}
	return string(s.data[off-s.base:]), s.end, dropped
}

// String returns the whole retained tail.
func (s *stream) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return string(s.data)
}

// Len reports the total number of bytes ever written, including bytes since
// discarded. Callers use it to detect progress, so it must not shrink when the
// buffer trims.
func (s *stream) Len() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.end
}

// wait returns a channel closed on the next write. Taking it under the same
// lock that publishes writes is what makes the check-then-wait sequence safe:
// a reader that saw offset N and then waits on this channel cannot miss a write
// that lands in between.
func (s *stream) wait() <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.notify
}
