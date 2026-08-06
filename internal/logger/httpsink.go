package logger

import (
	"github.com/dimetron/pi-go/internal/httplog"
)

// HTTPSink adapts a Logger to httplog's sink signature, so --trace-http entries
// land in the same JSONL session file as everything else.
//
// The dependency runs one way only — httplog knows nothing about Logger — which
// is what lets the provider transports emit traces without importing the
// logger they cannot reach at construction time.
//
// Returns nil for a nil Logger so the caller can install the result
// unconditionally; httplog.SetSink(nil) simply detaches.
func HTTPSink(l *Logger) func(httplog.Entry) {
	if l == nil {
		return nil
	}
	return func(e httplog.Entry) {
		entry := Entry{
			Type:      "http_" + e.Direction,
			Exchange:  e.Exchange,
			Method:    e.Method,
			URL:       e.URL,
			Proto:     e.Proto,
			Status:    e.Status,
			Headers:   e.Headers,
			Content:   e.Body,
			Truncated: e.BodyTruncated,
			DurationM: e.Duration.Milliseconds(),
		}
		if e.Err != "" {
			// A transport-level failure never produced a response, so there is
			// no status to report. Carrying it as the content of the response
			// entry keeps the exchange correlated with its request.
			entry.Content = "transport error: " + e.Err
		}
		l.Log(entry)
	}
}
