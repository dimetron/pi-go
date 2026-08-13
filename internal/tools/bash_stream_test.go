package tools

import (
	"strings"
	"sync"
	"testing"
)

func TestStream_SinceIsIncremental(t *testing.T) {
	s := newStream(1024)

	if data, next, dropped := s.since(0); data != "" || next != 0 || dropped != 0 {
		t.Fatalf("empty stream: got (%q, %d, %d)", data, next, dropped)
	}

	s.Write([]byte("alpha\n"))
	data, next, dropped := s.since(0)
	if data != "alpha\n" || dropped != 0 {
		t.Fatalf("first read: got (%q, %d)", data, dropped)
	}

	// A read from the returned offset must see only what arrived afterwards.
	s.Write([]byte("beta\n"))
	data, next, dropped = s.since(next)
	if data != "beta\n" || dropped != 0 {
		t.Fatalf("second read: got (%q, %d)", data, dropped)
	}

	if data, _, _ := s.since(next); data != "" {
		t.Errorf("caught-up read should be empty, got %q", data)
	}
}

// TestStream_ReportsDroppedBytes is the honesty requirement. A bounded buffer
// must not hand a reader a later chunk as though it followed directly on from
// the offset it asked for — a slow reader of a noisy command would otherwise
// assemble a plausible, wrong transcript.
func TestStream_ReportsDroppedBytes(t *testing.T) {
	s := newStream(16)

	s.Write([]byte("0123456789"))
	s.Write([]byte("abcdefghij")) // total 20 bytes, 4 must age out

	data, next, dropped := s.since(0)
	if dropped != 4 {
		t.Errorf("dropped = %d, want 4", dropped)
	}
	if data != "456789abcdefghij" {
		t.Errorf("data = %q, want the retained tail", data)
	}
	if next != 20 {
		t.Errorf("next = %d, want 20 (absolute offset, not buffer length)", next)
	}
}

// TestStream_LenCountsEverythingEverWritten: Len drives the idle clock and the
// progress checks, so it must not shrink when the buffer trims.
func TestStream_LenCountsEverythingEverWritten(t *testing.T) {
	s := newStream(8)
	s.Write([]byte("aaaaaaaaaaaaaaaa")) // 16 bytes into an 8-byte buffer
	if got := s.Len(); got != 16 {
		t.Errorf("Len() = %d, want 16", got)
	}
	if got := len(s.String()); got != 8 {
		t.Errorf("retained tail = %d bytes, want 8", got)
	}
}

// TestStream_WaitWakesOnWrite covers the blocking read used by bash_wait, and
// specifically the check-then-wait ordering: a waiter that took the channel
// before a write must still be woken by it.
func TestStream_WaitWakesOnWrite(t *testing.T) {
	s := newStream(1024)

	ch := s.wait()
	select {
	case <-ch:
		t.Fatal("wait channel fired before any write")
	default:
	}

	go s.Write([]byte("x"))
	<-ch // blocks until the write lands; the test times out if it never does
}

func TestStream_ConcurrentWritesAreSerialized(t *testing.T) {
	s := newStream(1 << 20)

	var wg sync.WaitGroup
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 20 {
				s.Write([]byte("0123456789"))
			}
		}()
	}
	wg.Wait()

	const want = 50 * 20 * 10
	if got := s.Len(); got != want {
		t.Errorf("Len() = %d, want %d", got, want)
	}
	if got := strings.Count(s.String(), "0123456789"); got != want/10 {
		t.Errorf("found %d intact chunks, want %d — writes interleaved", got, want/10)
	}
}
