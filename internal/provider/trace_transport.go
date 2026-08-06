package provider

import (
	"bytes"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/dimetron/pi-go/internal/httplog"
)

// traceTransport records every request and response passing through it —
// method, URL, full headers and body — into internal/httplog, which fans them
// out to the session log and to OpenTelemetry span events.
//
// It is installed by BuildTransport as the *innermost* wrapper — below
// headerTransport — so it observes the request as it actually goes on the wire.
// Sitting above headerTransport would log a request missing every ExtraHeader
// the server went on to receive.
//
// Capture is re-checked per round trip rather than only at construction, so
// enabling the trace does not depend on having been decided before the LLM
// client was built.
type traceTransport struct{ base http.RoundTripper }

func (t *traceTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if !httplog.Enabled() {
		return t.base.RoundTrip(req)
	}

	ctx := req.Context()
	exchange := httplog.NextExchange()
	method, url := req.Method, req.URL.String()

	body, truncated := readRequestBody(req)
	httplog.Emit(ctx, httplog.Entry{
		Exchange:      exchange,
		Direction:     httplog.DirectionRequest,
		Method:        method,
		URL:           url,
		Headers:       httplog.Redact(req.Header),
		Body:          body,
		BodyTruncated: truncated,
	})

	start := time.Now()
	resp, err := t.base.RoundTrip(req)
	elapsed := time.Since(start)

	if err != nil || resp == nil {
		msg := ""
		if err != nil {
			msg = err.Error()
		}
		httplog.Emit(ctx, httplog.Entry{
			Exchange:  exchange,
			Direction: httplog.DirectionResponse,
			Method:    method,
			URL:       url,
			Duration:  elapsed,
			Err:       msg,
		})
		return resp, err
	}

	// Headers are emitted now, body when the stream ends. Waiting for the body
	// would withhold the status code of a streaming completion for the whole
	// duration of the turn, which is exactly when it is most wanted.
	httplog.Emit(ctx, httplog.Entry{
		Exchange:  exchange,
		Direction: httplog.DirectionResponse,
		Method:    method,
		URL:       url,
		Proto:     resp.Proto,
		Status:    resp.StatusCode,
		Headers:   httplog.Redact(resp.Header),
		Duration:  elapsed,
	})

	if resp.Body != nil {
		status, proto := resp.StatusCode, resp.Proto
		resp.Body = httplog.CaptureBody(resp.Body, func(b string, bodyTruncated bool) {
			if b == "" {
				return
			}
			httplog.Emit(ctx, httplog.Entry{
				Exchange:      exchange,
				Direction:     httplog.DirectionResponse,
				Method:        method,
				URL:           url,
				Proto:         proto,
				Status:        status,
				Body:          b,
				BodyTruncated: bodyTruncated,
			})
		})
	}

	return resp, err
}

// readRequestBody returns a copy of the outgoing body without disturbing the
// request the caller still owns.
//
// GetBody is preferred and is what the OpenAI and Anthropic SDKs set: it hands
// back an independent reader, so the original body is never consumed and a
// retry after a 429 still has something to send. Only when it is absent does
// this fall back to draining and replacing the body, which is safe for the
// single attempt but is why GetBody is tried first.
func readRequestBody(req *http.Request) (string, bool) {
	if req.Body == nil || req.Body == http.NoBody {
		return "", false
	}

	limit := httplog.MaxBody()

	if req.GetBody != nil {
		rc, err := req.GetBody()
		if err != nil || rc == nil {
			return "", false
		}
		defer func() { _ = rc.Close() }()
		return readLimited(rc, limit)
	}

	raw, err := io.ReadAll(io.LimitReader(req.Body, int64(limit)+1))
	if err != nil {
		// The body is now partially drained and cannot be put back intact.
		// Restore what was read so the request still carries as much as
		// possible rather than being silently emptied by the act of logging it.
		req.Body = io.NopCloser(io.MultiReader(bytes.NewReader(raw), req.Body))
		return "", false
	}
	req.Body = struct {
		io.Reader
		io.Closer
	}{
		Reader: io.MultiReader(bytes.NewReader(raw), req.Body),
		Closer: req.Body,
	}
	return capped(raw, limit)
}

func readLimited(r io.Reader, limit int) (string, bool) {
	raw, err := io.ReadAll(io.LimitReader(r, int64(limit)+1))
	if err != nil {
		return "", false
	}
	return capped(raw, limit)
}

// capped trims raw to limit, marking the result when it had to cut.
func capped(raw []byte, limit int) (string, bool) {
	if len(raw) > limit {
		return string(raw[:limit]) + "…(truncated at " + strconv.Itoa(limit) + " bytes)", true
	}
	return string(raw), false
}
