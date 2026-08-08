package httplog

import (
	"context"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

// recordSpan runs fn with a live recording span in the context and returns the
// finished span, so tests can assert on the events emitted onto it.
func recordSpan(t *testing.T, fn func(ctx context.Context)) sdktrace.ReadOnlySpan {
	t.Helper()

	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
	t.Cleanup(func() {
		if err := tp.Shutdown(context.Background()); err != nil {
			t.Errorf("shutting down tracer provider: %v", err)
		}
	})

	ctx, span := tp.Tracer("httplog-test").Start(context.Background(), "exchange")
	fn(ctx)
	span.End()

	spans := exp.GetSpans().Snapshots()
	if len(spans) != 1 {
		t.Fatalf("got %d spans, want 1", len(spans))
	}
	return spans[0]
}

// attrsOf flattens the first event's attributes into a lookup map.
func attrsOf(t *testing.T, span sdktrace.ReadOnlySpan) (string, map[string]attribute.Value) {
	t.Helper()

	events := span.Events()
	if len(events) != 1 {
		t.Fatalf("got %d span events, want 1", len(events))
	}
	out := make(map[string]attribute.Value, len(events[0].Attributes))
	for _, kv := range events[0].Attributes {
		out[string(kv.Key)] = kv.Value
	}
	return events[0].Name, out
}

func TestEmitSpanEvent_NonRecordingSpanIsANoOp(t *testing.T) {
	// The no-op tracer is what runs when OTel is not configured. Reaching this
	// path must cost nothing and must not panic.
	ctx := trace.ContextWithSpan(context.Background(), trace.SpanFromContext(context.Background()))
	emitSpanEvent(ctx, Entry{Exchange: 1, Direction: DirectionRequest})
}

func TestEmitSpanEvent_RequestAttributes(t *testing.T) {
	span := recordSpan(t, func(ctx context.Context) {
		emitSpanEvent(ctx, Entry{
			Exchange:  7,
			Direction: DirectionRequest,
			Method:    "POST",
			URL:       "https://api.example.com/v1/messages",
			Headers:   map[string][]string{"Content-Type": {"application/json"}},
			Body:      `{"model":"test"}`,
		})
	})

	name, attrs := attrsOf(t, span)
	if name != "http.request" {
		t.Errorf("event name = %q, want %q", name, "http.request")
	}

	want := map[string]string{
		"http.exchange":                    "7",
		"http.direction":                   "request",
		"http.request.method":              "POST",
		"url.full":                         "https://api.example.com/v1/messages",
		"http.request.header.content-type": "application/json",
		"http.request.header...body":       `{"model":"test"}`,
	}
	for k, v := range want {
		got, ok := attrs[k]
		if !ok {
			t.Errorf("missing attribute %q", k)
			continue
		}
		if got.String() != v {
			t.Errorf("attribute %q = %q, want %q", k, got.String(), v)
		}
	}

	// Response-only fields must not appear on a request entry.
	for _, k := range []string{"http.response.status_code", "http.duration_ms", "error.message"} {
		if _, ok := attrs[k]; ok {
			t.Errorf("request entry carried response-only attribute %q", k)
		}
	}
}

func TestEmitSpanEvent_ResponseAttributesUseResponsePrefix(t *testing.T) {
	span := recordSpan(t, func(ctx context.Context) {
		emitSpanEvent(ctx, Entry{
			Exchange:  8,
			Direction: DirectionResponse,
			Status:    429,
			Duration:  1500 * time.Millisecond,
			Err:       "rate limited",
			Headers:   map[string][]string{"Retry-After": {"30"}},
			Body:      "slow down",
		})
	})

	name, attrs := attrsOf(t, span)
	if name != "http.response" {
		t.Errorf("event name = %q, want %q", name, "http.response")
	}

	want := map[string]string{
		"http.response.status_code":        "429",
		"http.duration_ms":                 "1500",
		"error.message":                    "rate limited",
		"http.response.header.retry-after": "30",
		"http.response.header...body":      "slow down",
	}
	for k, v := range want {
		got, ok := attrs[k]
		if !ok {
			t.Errorf("missing attribute %q", k)
			continue
		}
		if got.String() != v {
			t.Errorf("attribute %q = %q, want %q", k, got.String(), v)
		}
	}
}

func TestEmitSpanEvent_JoinsRepeatedHeaderValues(t *testing.T) {
	span := recordSpan(t, func(ctx context.Context) {
		emitSpanEvent(ctx, Entry{
			Direction: DirectionRequest,
			Headers:   map[string][]string{"Accept": {"text/event-stream", "application/json"}},
		})
	})

	_, attrs := attrsOf(t, span)
	got, ok := attrs["http.request.header.accept"]
	if !ok {
		t.Fatal("missing joined accept header")
	}
	if got.String() != "text/event-stream, application/json" {
		t.Errorf("accept = %q, want the values joined", got.String())
	}
}

func TestEmitSpanEvent_TruncatesOversizedBodyAndFlagsIt(t *testing.T) {
	t.Cleanup(func() { SetOTelMaxBody(0) }) // restore the default
	SetOTelMaxBody(16)

	span := recordSpan(t, func(ctx context.Context) {
		emitSpanEvent(ctx, Entry{
			Direction: DirectionRequest,
			Body:      strings.Repeat("x", 500),
		})
	})

	_, attrs := attrsOf(t, span)
	body, ok := attrs["http.request.header...body"]
	if !ok {
		t.Fatal("missing body attribute")
	}
	if len(body.String()) > 64 {
		t.Errorf("body not truncated: %d bytes", len(body.String()))
	}
	flag, ok := attrs["http.body.truncated"]
	if !ok || flag.String() != "true" {
		t.Errorf("truncation flag = %v (present=%v), want true", flag.String(), ok)
	}
}

func TestEmitSpanEvent_PropagatesPreTruncatedBodyFlag(t *testing.T) {
	// The body was already cut by the capture layer, so emitSpanEvent's own
	// truncate is a no-op — the flag must still be set.
	span := recordSpan(t, func(ctx context.Context) {
		emitSpanEvent(ctx, Entry{
			Direction:     DirectionRequest,
			Body:          "short",
			BodyTruncated: true,
		})
	})

	_, attrs := attrsOf(t, span)
	flag, ok := attrs["http.body.truncated"]
	if !ok || flag.String() != "true" {
		t.Errorf("truncation flag = %v (present=%v), want true", flag.String(), ok)
	}
}

func TestEmitSpanEvent_OmitsEmptyOptionalFields(t *testing.T) {
	span := recordSpan(t, func(ctx context.Context) {
		emitSpanEvent(ctx, Entry{Exchange: 1, Direction: DirectionRequest})
	})

	_, attrs := attrsOf(t, span)
	for _, k := range []string{
		"http.request.method", "url.full", "http.response.status_code",
		"http.duration_ms", "error.message", "http.request.header...body",
	} {
		if _, ok := attrs[k]; ok {
			t.Errorf("empty field emitted attribute %q", k)
		}
	}
	// The two always-present fields are still there.
	if _, ok := attrs["http.exchange"]; !ok {
		t.Error("missing http.exchange")
	}
	if _, ok := attrs["http.direction"]; !ok {
		t.Error("missing http.direction")
	}
}

func TestMask(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty stays empty", "", ""},
		{"short values reveal nothing", "abc", "***(3 bytes)"},
		{"exactly at the keep length reveals nothing", "12345678", "***(8 bytes)"},
		{"longer values keep an identifying prefix", "sk-ant-secret-value", "sk-ant-s***(19 bytes)"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := mask(tc.in); got != tc.want {
				t.Errorf("mask(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
