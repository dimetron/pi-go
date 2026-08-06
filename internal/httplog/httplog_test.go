package httplog

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
)

// reset returns httplog to its zero configuration. Package state is global, so
// every test that touches it must restore it or leak into the next one.
func reset(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		SetEnabled(false)
		SetSink(nil)
		SetMaxBody(0)
		SetOTelMaxBody(0)
	})
	SetEnabled(false)
	SetSink(nil)
	SetMaxBody(0)
	SetOTelMaxBody(0)
}

func TestEmitDroppedWhenDisabled(t *testing.T) {
	reset(t)
	var got int
	SetSink(func(Entry) { got++ })

	Emit(context.Background(), Entry{Direction: DirectionRequest})
	if got != 0 {
		t.Fatalf("sink called %d times while disabled, want 0", got)
	}

	SetEnabled(true)
	Emit(context.Background(), Entry{Direction: DirectionRequest})
	if got != 1 {
		t.Fatalf("sink called %d times while enabled, want 1", got)
	}
}

func TestEmitWithNoSinkDoesNotPanic(t *testing.T) {
	reset(t)
	SetEnabled(true)
	Emit(context.Background(), Entry{Direction: DirectionResponse, Status: 200})
}

func TestRedactMasksCredentialsAndCopies(t *testing.T) {
	reset(t)
	h := http.Header{}
	h.Set("Authorization", "Bearer sk-abcdefghijklmnop")
	h.Set("X-Api-Key", "short")
	h.Set("Content-Type", "application/json")
	h.Set("Set-Cookie", "session=deadbeefcafe")

	out := Redact(h)

	if got := out["Content-Type"][0]; got != "application/json" {
		t.Errorf("Content-Type = %q, want it passed through unchanged", got)
	}
	if got := out["Authorization"][0]; strings.Contains(got, "ijklmnop") {
		t.Errorf("Authorization = %q, still contains the secret tail", got)
	} else if !strings.HasPrefix(got, "Bearer s") {
		t.Errorf("Authorization = %q, want an identifying prefix retained", got)
	}
	if got := out["X-Api-Key"][0]; strings.Contains(got, "short") {
		t.Errorf("X-Api-Key = %q, short values must be masked entirely", got)
	}
	if got := out["Set-Cookie"][0]; strings.Contains(got, "deadbeefcafe") {
		t.Errorf("Set-Cookie = %q, response credentials must be masked too", got)
	}

	// The caller's header must be untouched — redacting in place would strip
	// the credentials off the request that is about to be sent.
	if h.Get("Authorization") != "Bearer sk-abcdefghijklmnop" {
		t.Errorf("Redact mutated the source header: %q", h.Get("Authorization"))
	}
}

func TestRedactNilHeader(t *testing.T) {
	if got := Redact(nil); got != nil {
		t.Errorf("Redact(nil) = %v, want nil", got)
	}
}

func TestCaptureBodyEmitsOnEOF(t *testing.T) {
	reset(t)
	var (
		mu        sync.Mutex
		body      string
		truncated bool
		calls     int
	)
	rc := CaptureBody(io.NopCloser(strings.NewReader("hello world")), func(b string, tr bool) {
		mu.Lock()
		defer mu.Unlock()
		body, truncated, calls = b, tr, calls+1
	})

	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(got) != "hello world" {
		t.Errorf("consumer read %q, want the body passed through intact", got)
	}
	if body != "hello world" || truncated {
		t.Errorf("captured %q truncated=%v, want %q false", body, truncated, "hello world")
	}

	// Close after EOF must not fire the callback a second time.
	if err := rc.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if calls != 1 {
		t.Errorf("done called %d times, want exactly 1", calls)
	}
}

func TestCaptureBodyEmitsOnCloseWithoutFullRead(t *testing.T) {
	reset(t)
	var body string
	var called bool
	rc := CaptureBody(io.NopCloser(strings.NewReader("abcdefghij")), func(b string, _ bool) {
		body, called = b, true
	})

	buf := make([]byte, 4)
	if _, err := rc.Read(buf); err != nil {
		t.Fatalf("Read: %v", err)
	}
	if called {
		t.Fatal("done fired mid-stream; it must wait for the end")
	}
	if err := rc.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if body != "abcd" {
		t.Errorf("captured %q, want the bytes actually read (%q)", body, "abcd")
	}
}

func TestCaptureBodyTruncatesAtCap(t *testing.T) {
	reset(t)
	SetMaxBody(10)
	var body string
	var truncated bool
	rc := CaptureBody(io.NopCloser(strings.NewReader(strings.Repeat("x", 100))), func(b string, tr bool) {
		body, truncated = b, tr
	})

	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(got) != 100 {
		t.Errorf("consumer got %d bytes, want all 100 — the cap must not truncate the stream itself", len(got))
	}
	if !truncated {
		t.Error("truncated = false, want true")
	}
	if !strings.HasPrefix(body, strings.Repeat("x", 10)) || !strings.Contains(body, "truncated") {
		t.Errorf("captured %q, want 10 bytes plus a truncation marker", body)
	}
}

// errReader fails after handing back its payload, standing in for a connection
// that drops mid-stream.
type errReader struct {
	data []byte
	done bool
}

func (e *errReader) Read(p []byte) (int, error) {
	if e.done {
		return 0, errors.New("connection reset")
	}
	e.done = true
	n := copy(p, e.data)
	return n, nil
}

func (e *errReader) Close() error { return nil }

func TestCaptureBodyEmitsOnReadError(t *testing.T) {
	reset(t)
	var body string
	var called bool
	rc := CaptureBody(&errReader{data: []byte("partial")}, func(b string, _ bool) {
		body, called = b, true
	})

	if _, err := io.ReadAll(rc); err == nil {
		t.Fatal("ReadAll succeeded, want the underlying error surfaced")
	}
	if !called {
		t.Fatal("done never fired; a broken stream must still log what it got")
	}
	if body != "partial" {
		t.Errorf("captured %q, want %q", body, "partial")
	}
}

func TestNextExchangeIsMonotonic(t *testing.T) {
	a, b := NextExchange(), NextExchange()
	if b <= a {
		t.Errorf("NextExchange returned %d then %d, want strictly increasing", a, b)
	}
}

func TestTruncate(t *testing.T) {
	if got, tr := truncate("abc", 10); got != "abc" || tr {
		t.Errorf("truncate under cap = %q,%v; want unchanged", got, tr)
	}
	got, tr := truncate("abcdef", 3)
	if !tr || !strings.HasPrefix(got, "abc") {
		t.Errorf("truncate over cap = %q,%v", got, tr)
	}
}

func TestSetMaxBodyRestoresDefaultOnNonPositive(t *testing.T) {
	reset(t)
	SetMaxBody(5)
	if MaxBody() != 5 {
		t.Fatalf("MaxBody() = %d, want 5", MaxBody())
	}
	SetMaxBody(0)
	if MaxBody() != DefaultMaxBody {
		t.Errorf("MaxBody() = %d after SetMaxBody(0), want the default %d", MaxBody(), DefaultMaxBody)
	}
}
