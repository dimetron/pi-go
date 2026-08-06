// Package httplog carries raw HTTP request/response traces from the provider
// transports to whichever sinks are active — the JSONL session log, OpenTelemetry
// span events, or both.
//
// It exists as its own package to break an ownership cycle. The provider
// transports are built during startup, before the session logger exists
// (internal/cli/cli.go builds the LLM at ~:251 and the logger at ~:568), so a
// transport cannot hold a logger reference at construction time. Instead the
// transport emits into this package unconditionally and the CLI installs a sink
// once the logger is ready — the same late-binding idiom as auth.SetDebugLogger.
//
// Nothing is captured unless SetEnabled(true) has been called: Enabled() is
// checked before a body is ever buffered, so the disabled path costs one atomic
// load per request.
package httplog

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// Direction distinguishes the two halves of an exchange.
const (
	DirectionRequest  = "request"
	DirectionResponse = "response"
)

// Entry is one captured half of an HTTP exchange. Requests and responses are
// emitted as separate entries rather than one combined record because a
// streaming response body is not complete until long after its headers arrive —
// a single record would have to withhold the request until the stream drained.
// Exchange ties the pair back together.
type Entry struct {
	// Exchange correlates the request with its response. Monotonic per process.
	Exchange uint64
	// Direction is DirectionRequest or DirectionResponse.
	Direction string
	// Method and URL are set on both directions so a response entry is
	// readable on its own.
	Method string
	URL    string
	// Proto and Status are set on responses only.
	Proto  string
	Status int
	// Headers is already redacted; see Redact.
	Headers map[string][]string
	// Body is the captured payload, truncated to MaxBody. Empty when the
	// message had no body.
	Body string
	// BodyTruncated reports that Body was cut at the cap.
	BodyTruncated bool
	// Duration is the time from request write to response headers. Set on
	// responses only.
	Duration time.Duration
	// Err is the transport-level failure, if the round trip never produced a
	// response. Set on responses only.
	Err string
}

// Default body caps. Requests carry the full conversation history, so a cap is
// not optional — a single turn late in a long session runs to hundreds of
// kilobytes, and the log is written on every request.
//
// The OTel cap is deliberately much smaller than the JSONL one. Span
// attributes travel through the OTLP exporter, which batches in memory and is
// bounded by the collector's own message-size limit; a megabyte-scale attribute
// silently drops the whole span at the collector rather than truncating it.
// The file sink has no such ceiling, so it keeps the larger cap.
const (
	DefaultMaxBody     = 1 << 20 // 1 MiB, JSONL sink
	DefaultOTelMaxBody = 8 << 10 // 8 KiB, span-event attributes
)

var (
	enabled     atomic.Bool
	maxBody     atomic.Int64
	otelMaxBody atomic.Int64

	sinkMu sync.RWMutex
	sink   func(Entry)

	exchangeCounter atomic.Uint64
)

func init() {
	maxBody.Store(DefaultMaxBody)
	otelMaxBody.Store(DefaultOTelMaxBody)
}

// SetEnabled turns capture on or off. Off by default; the CLI sets it from
// --debug.
func SetEnabled(v bool) { enabled.Store(v) }

// Enabled reports whether capture is on. Transports must check this before
// buffering anything.
func Enabled() bool { return enabled.Load() }

// SetMaxBody overrides the JSONL body cap. A value <= 0 restores the default.
func SetMaxBody(n int) {
	if n <= 0 {
		n = DefaultMaxBody
	}
	maxBody.Store(int64(n))
}

// MaxBody returns the current JSONL body cap.
func MaxBody() int { return int(maxBody.Load()) }

// SetOTelMaxBody overrides the span-attribute body cap. A value <= 0 restores
// the default.
func SetOTelMaxBody(n int) {
	if n <= 0 {
		n = DefaultOTelMaxBody
	}
	otelMaxBody.Store(int64(n))
}

// SetSink installs the destination for captured entries, replacing any previous
// one. Passing nil detaches the sink, which is what tests and shutdown want —
// entries emitted with no sink are dropped, not buffered.
//
// OTel span events are emitted independently of the sink, so tracing works even
// when no session log exists.
func SetSink(fn func(Entry)) {
	sinkMu.Lock()
	defer sinkMu.Unlock()
	sink = fn
}

// NextExchange returns a fresh correlation ID for a request/response pair.
func NextExchange() uint64 { return exchangeCounter.Add(1) }

// Emit delivers e to the installed sink and, when ctx carries a recording span,
// records it as a span event. It is safe to call with capture disabled or no
// sink installed.
//
// ctx is the request's context, so the span picked up is the agent turn the
// call belongs to (see internal/tui/agent_loop.go, which starts it).
func Emit(ctx context.Context, e Entry) {
	if !Enabled() {
		return
	}

	sinkMu.RLock()
	fn := sink
	sinkMu.RUnlock()
	if fn != nil {
		fn(e)
	}

	emitSpanEvent(ctx, e)
}

// emitSpanEvent records the entry on the active span. A non-recording span
// (the no-op tracer, when OTel is not configured) short-circuits here, which is
// what makes the OTel half of this "if enabled" without a config lookup.
func emitSpanEvent(ctx context.Context, e Entry) {
	span := trace.SpanFromContext(ctx)
	if !span.IsRecording() {
		return
	}

	attrs := []attribute.KeyValue{
		attribute.Int64("http.exchange", int64(e.Exchange)), //nolint:gosec // counter, not user input
		attribute.String("http.direction", e.Direction),
	}
	if e.Method != "" {
		attrs = append(attrs, attribute.String("http.request.method", e.Method))
	}
	if e.URL != "" {
		attrs = append(attrs, attribute.String("url.full", e.URL))
	}
	if e.Status != 0 {
		attrs = append(attrs, attribute.Int("http.response.status_code", e.Status))
	}
	if e.Duration > 0 {
		attrs = append(attrs, attribute.Int64("http.duration_ms", e.Duration.Milliseconds()))
	}
	if e.Err != "" {
		attrs = append(attrs, attribute.String("error.message", e.Err))
	}

	// Headers go on as one attribute per field rather than a single blob so a
	// backend can filter on, say, retry-after without parsing.
	prefix := "http.request.header."
	if e.Direction == DirectionResponse {
		prefix = "http.response.header."
	}
	for k, vs := range e.Headers {
		attrs = append(attrs, attribute.String(prefix+strings.ToLower(k), strings.Join(vs, ", ")))
	}

	if e.Body != "" {
		body, truncated := truncate(e.Body, int(otelMaxBody.Load()))
		attrs = append(attrs, attribute.String(prefix+"..body", body))
		if truncated || e.BodyTruncated {
			attrs = append(attrs, attribute.Bool("http.body.truncated", true))
		}
	}

	span.AddEvent("http."+e.Direction, trace.WithAttributes(attrs...))
}

// redactedHeaders are replaced with a fingerprint rather than dropped, so a log
// still shows that a credential was sent and whether it changed between calls.
//
// Response headers are in here too: openai-organization and set-cookie identify
// the account, and a session log is routinely pasted into a bug report.
var redactedHeaders = map[string]bool{
	"authorization":       true,
	"proxy-authorization": true,
	"x-api-key":           true,
	"api-key":             true,
	"x-goog-api-key":      true,
	"cookie":              true,
	"set-cookie":          true,
	"chatgpt-account-id":  true,
	"openai-organization": true,
}

// Redact copies h, replacing credential values with a prefix and a length so
// the entry stays diagnostically useful without carrying the secret.
//
// The copy matters: http.Header is a live map owned by the request, and
// redacting in place would strip the caller's own credentials.
func Redact(h http.Header) map[string][]string {
	if len(h) == 0 {
		return nil
	}
	out := make(map[string][]string, len(h))
	for k, vs := range h {
		if !redactedHeaders[strings.ToLower(k)] {
			out[k] = append([]string(nil), vs...)
			continue
		}
		masked := make([]string, len(vs))
		for i, v := range vs {
			masked[i] = mask(v)
		}
		out[k] = masked
	}
	return out
}

// mask reduces a credential to an identifiable but unusable stub.
func mask(v string) string {
	if v == "" {
		return ""
	}
	const keep = 8
	if len(v) <= keep {
		return "***(" + strconv.Itoa(len(v)) + " bytes)"
	}
	return v[:keep] + "***(" + strconv.Itoa(len(v)) + " bytes)"
}

// truncate cuts s to n bytes, reporting whether it did.
func truncate(s string, n int) (string, bool) {
	if n <= 0 || len(s) <= n {
		return s, false
	}
	return s[:n] + fmt.Sprintf("…(truncated, %d bytes total)", len(s)), true
}

// CaptureBody buffers up to MaxBody bytes of rc as it is read by the real
// consumer, then calls done exactly once when the stream ends — at EOF, at a
// read error, or at Close, whichever comes first.
//
// It exists because a streaming response cannot be logged the way an ordinary
// one can. io.ReadAll on an SSE body blocks until the model finishes its turn,
// which for a long completion means the SDK sees no tokens for minutes and the
// TUI renders nothing; for a body that never terminates it deadlocks outright.
// Buffering alongside the consumer keeps the stream flowing at its own pace and
// defers the log entry to the point where there is something complete to write.
func CaptureBody(rc io.ReadCloser, done func(body string, truncated bool)) io.ReadCloser {
	return &bodyCapture{rc: rc, limit: MaxBody(), done: done}
}

type bodyCapture struct {
	rc        io.ReadCloser
	buf       bytes.Buffer
	limit     int
	truncated bool
	once      sync.Once
	done      func(string, bool)
}

func (b *bodyCapture) Read(p []byte) (int, error) {
	n, err := b.rc.Read(p)
	if n > 0 {
		b.record(p[:n])
	}
	if err != nil {
		// Includes io.EOF. Finishing here rather than waiting for Close means
		// the entry lands as soon as the stream ends, even for a consumer that
		// leaks the body.
		b.finish()
	}
	return n, err
}

func (b *bodyCapture) Close() error {
	b.finish()
	return b.rc.Close()
}

func (b *bodyCapture) record(p []byte) {
	room := b.limit - b.buf.Len()
	if room <= 0 {
		b.truncated = true
		return
	}
	if len(p) > room {
		b.buf.Write(p[:room])
		b.truncated = true
		return
	}
	b.buf.Write(p)
}

func (b *bodyCapture) finish() {
	b.once.Do(func() {
		body := b.buf.String()
		if b.truncated {
			body += fmt.Sprintf("…(truncated at %d bytes)", b.limit)
		}
		b.done(body, b.truncated)
	})
}
