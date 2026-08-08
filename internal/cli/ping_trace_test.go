package cli

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/dimetron/pi-go/internal/httplog"
)

// captureSink returns a pingWriter that accumulates into a builder, plus the
// builder, so a test can assert on the rendered transcript.
func captureSink() (pingWriter, *strings.Builder) {
	var b strings.Builder
	return func(format string, a ...any) {
		fmt.Fprintf(&b, format, a...)
	}, &b
}

func TestPingTraceSink_RendersRequestWithOutMarker(t *testing.T) {
	w, out := captureSink()

	pingTraceSink(w)(httplog.Entry{
		Exchange:  1,
		Direction: httplog.DirectionRequest,
		Method:    "POST",
		URL:       "https://api.anthropic.com/v1/messages",
		Headers:   map[string][]string{"Content-Type": {"application/json"}},
		Body:      `{"model":"claude"}`,
	})

	got := out.String()
	for _, want := range []string{
		"> [1] POST https://api.anthropic.com/v1/messages",
		"> [1] Content-Type: application/json",
		`> [1] body: {"model":"claude"}`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("trace missing %q\ngot:\n%s", want, got)
		}
	}
}

func TestPingTraceSink_RendersResponseWithInMarker(t *testing.T) {
	w, out := captureSink()

	pingTraceSink(w)(httplog.Entry{
		Exchange:  2,
		Direction: httplog.DirectionResponse,
		Proto:     "HTTP/2.0",
		Status:    200,
		Duration:  1234 * time.Millisecond,
		Headers:   map[string][]string{"Content-Type": {"text/event-stream"}},
	})

	got := out.String()
	for _, want := range []string{
		"< [2] HTTP/2.0 200 (1.234s)",
		"< [2] Content-Type: text/event-stream",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("trace missing %q\ngot:\n%s", want, got)
		}
	}
	if strings.Contains(got, "> [2]") {
		t.Errorf("response used the request marker:\n%s", got)
	}
}

func TestPingTraceSink_TransportErrorShortCircuits(t *testing.T) {
	w, out := captureSink()

	pingTraceSink(w)(httplog.Entry{
		Exchange:  3,
		Direction: httplog.DirectionResponse,
		Err:       "connection reset by peer",
		// Headers and body must not be printed once an error is reported.
		Headers: map[string][]string{"X-Should-Not": {"appear"}},
		Body:    "should not appear",
	})

	got := out.String()
	if !strings.Contains(got, "< [3] transport error: connection reset by peer") {
		t.Errorf("transport error not rendered:\n%s", got)
	}
	if strings.Contains(got, "X-Should-Not") || strings.Contains(got, "should not appear") {
		t.Errorf("an errored exchange printed headers or body:\n%s", got)
	}
}

func TestPingTraceSink_HeadersAreSortedForStableOutput(t *testing.T) {
	w, out := captureSink()

	pingTraceSink(w)(httplog.Entry{
		Exchange:  4,
		Direction: httplog.DirectionRequest,
		Method:    "GET",
		URL:       "https://example.com",
		Headers: map[string][]string{
			"Zeta":  {"z"},
			"Alpha": {"a"},
			"Mid":   {"m"},
		},
	})

	got := out.String()
	ai, mi, zi := strings.Index(got, "Alpha:"), strings.Index(got, "Mid:"), strings.Index(got, "Zeta:")
	if ai < 0 || mi < 0 || zi < 0 {
		t.Fatalf("not all headers rendered:\n%s", got)
	}
	if ai >= mi || mi >= zi {
		t.Errorf("headers not sorted (Alpha=%d Mid=%d Zeta=%d):\n%s", ai, mi, zi, got)
	}
}

func TestPingTraceSink_RepeatedHeaderValuesEachGetALine(t *testing.T) {
	w, out := captureSink()

	pingTraceSink(w)(httplog.Entry{
		Exchange:  5,
		Direction: httplog.DirectionRequest,
		Method:    "GET",
		URL:       "https://example.com",
		Headers:   map[string][]string{"Accept": {"text/event-stream", "application/json"}},
	})

	got := out.String()
	if n := strings.Count(got, "Accept:"); n != 2 {
		t.Errorf("got %d Accept lines, want one per value:\n%s", n, got)
	}
}

func TestPingTraceSink_ResponseWithoutStatusPrintsNoStatusLine(t *testing.T) {
	// Status 0 means the response line was never seen; only headers follow.
	w, out := captureSink()

	pingTraceSink(w)(httplog.Entry{
		Exchange:  6,
		Direction: httplog.DirectionResponse,
		Headers:   map[string][]string{"X-Trailer": {"1"}},
	})

	got := out.String()
	if strings.Contains(got, "HTTP/") {
		t.Errorf("a status-less response printed a status line:\n%s", got)
	}
	if !strings.Contains(got, "X-Trailer: 1") {
		t.Errorf("headers not rendered:\n%s", got)
	}
}

func TestPingTraceSink_EmptyEntryRendersNothingHarmful(t *testing.T) {
	w, out := captureSink()
	pingTraceSink(w)(httplog.Entry{})
	if got := out.String(); strings.TrimSpace(got) != "" {
		t.Errorf("empty entry rendered %q, want nothing", got)
	}
}
