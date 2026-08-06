package logger

import (
	"strings"
	"testing"
	"time"

	"github.com/dimetron/pi-go/internal/httplog"
)

func TestHTTPSinkNilLogger(t *testing.T) {
	if HTTPSink(nil) != nil {
		t.Error("HTTPSink(nil) returned a non-nil sink; callers install it unconditionally")
	}
}

func TestHTTPSinkWritesRequestAndResponse(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	l, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	sink := HTTPSink(l)
	sink(httplog.Entry{
		Exchange:  7,
		Direction: httplog.DirectionRequest,
		Method:    "POST",
		URL:       "https://api.example.com/v1/messages",
		Headers:   map[string][]string{"Authorization": {"Bearer ab***(20 bytes)"}},
		Body:      `{"prompt":"hi"}`,
	})
	sink(httplog.Entry{
		Exchange:      7,
		Direction:     httplog.DirectionResponse,
		Method:        "POST",
		URL:           "https://api.example.com/v1/messages",
		Proto:         "HTTP/2.0",
		Status:        429,
		Headers:       map[string][]string{"Retry-After": {"30"}},
		Body:          `{"error":"rate_limit"}`,
		BodyTruncated: true,
		Duration:      1500 * time.Millisecond,
	})
	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	entries := readEntries(t, l.Path())
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2: %+v", len(entries), entries)
	}

	req := entries[0]
	if req.Type != "http_request" {
		t.Errorf("request type = %q, want http_request", req.Type)
	}
	if req.Exchange != 7 || req.Method != "POST" {
		t.Errorf("request = exchange %d %s, want 7 POST", req.Exchange, req.Method)
	}
	if req.Content != `{"prompt":"hi"}` {
		t.Errorf("request content = %q, want the body", req.Content)
	}
	if got := req.Headers["Authorization"][0]; !strings.Contains(got, "***") {
		t.Errorf("request Authorization = %q, want the masked value carried through", got)
	}

	resp := entries[1]
	if resp.Type != "http_response" {
		t.Errorf("response type = %q, want http_response", resp.Type)
	}
	if resp.Status != 429 || resp.Proto != "HTTP/2.0" {
		t.Errorf("response = %d %s, want 429 HTTP/2.0", resp.Status, resp.Proto)
	}
	if resp.DurationM != 1500 {
		t.Errorf("response duration_ms = %d, want 1500", resp.DurationM)
	}
	if !resp.Truncated {
		t.Error("response truncated = false, want true")
	}
	if resp.Exchange != req.Exchange {
		t.Errorf("response exchange = %d, want %d to match the request", resp.Exchange, req.Exchange)
	}
}

func TestHTTPSinkTransportError(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	l, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	HTTPSink(l)(httplog.Entry{
		Exchange:  3,
		Direction: httplog.DirectionResponse,
		Method:    "GET",
		URL:       "https://api.example.com/v1/models",
		Err:       "dial tcp: connection refused",
	})
	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	entries := readEntries(t, l.Path())
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	if !strings.Contains(entries[0].Content, "connection refused") {
		t.Errorf("content = %q, want the transport error", entries[0].Content)
	}
	if entries[0].Status != 0 {
		t.Errorf("status = %d, want 0 — no response arrived", entries[0].Status)
	}
}

// TestHTTPEntriesAreNotCoalesced guards the interaction with the streaming
// coalescer: only llm_text and thinking merge, so a run of HTTP entries must
// stay one record each.
func TestHTTPEntriesAreNotCoalesced(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	l, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	sink := HTTPSink(l)
	for i := range 3 {
		sink(httplog.Entry{
			Exchange:  uint64(i), //nolint:gosec // small loop index
			Direction: httplog.DirectionRequest,
			Method:    "POST",
			Body:      "body",
		})
	}
	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if entries := readEntries(t, l.Path()); len(entries) != 3 {
		t.Fatalf("got %d entries, want 3 separate records: %+v", len(entries), entries)
	}
}
