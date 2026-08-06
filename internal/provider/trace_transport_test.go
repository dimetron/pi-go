package provider

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/dimetron/pi-go/internal/httplog"
)

// stubTripper returns a canned response, recording the request body it actually
// received so a test can prove the trace did not eat it.
type stubTripper struct {
	resp     *http.Response
	err      error
	gotBody  string
	gotCalls int
}

func (s *stubTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	s.gotCalls++
	if req.Body != nil {
		b, _ := io.ReadAll(req.Body)
		s.gotBody = string(b)
	}
	if s.err != nil {
		return nil, s.err
	}
	return s.resp, nil
}

func jsonResponse(status int, body string) *http.Response {
	h := http.Header{}
	h.Set("Content-Type", "application/json")
	h.Set("Set-Cookie", "sess=supersecretvalue")
	return &http.Response{
		StatusCode: status,
		Proto:      "HTTP/2.0",
		Header:     h,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// collect turns tracing on with a recording sink for the duration of a test.
func collect(t *testing.T) *[]httplog.Entry {
	t.Helper()
	var mu sync.Mutex
	entries := make([]httplog.Entry, 0, 8)
	httplog.SetEnabled(true)
	httplog.SetSink(func(e httplog.Entry) {
		mu.Lock()
		defer mu.Unlock()
		entries = append(entries, e)
	})
	t.Cleanup(func() {
		httplog.SetEnabled(false)
		httplog.SetSink(nil)
		httplog.SetMaxBody(0)
	})
	return &entries
}

func TestTraceTransportCapturesRequestAndResponse(t *testing.T) {
	entries := collect(t)

	stub := &stubTripper{resp: jsonResponse(200, `{"ok":true}`)}
	tr := &traceTransport{base: stub}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		"https://api.example.com/v1/messages", strings.NewReader(`{"prompt":"hi"}`))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Authorization", "Bearer sk-secretsecret")

	resp, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()

	if string(body) != `{"ok":true}` {
		t.Errorf("consumer read %q, want the response body intact", body)
	}
	if stub.gotBody != `{"prompt":"hi"}` {
		t.Errorf("upstream received body %q, want it undisturbed by tracing", stub.gotBody)
	}

	e := *entries
	if len(e) != 3 {
		t.Fatalf("got %d entries, want 3 (request, response headers, response body): %+v", len(e), e)
	}

	reqEntry := e[0]
	if reqEntry.Direction != httplog.DirectionRequest {
		t.Errorf("entry 0 direction = %q, want request", reqEntry.Direction)
	}
	if reqEntry.Method != http.MethodPost || reqEntry.URL != "https://api.example.com/v1/messages" {
		t.Errorf("entry 0 = %s %s, want POST of the full URL", reqEntry.Method, reqEntry.URL)
	}
	if reqEntry.Body != `{"prompt":"hi"}` {
		t.Errorf("entry 0 body = %q, want the request payload", reqEntry.Body)
	}
	if got := reqEntry.Headers["Authorization"][0]; strings.Contains(got, "secretsecret") {
		t.Errorf("entry 0 leaked the credential: %q", got)
	}

	hdrEntry := e[1]
	if hdrEntry.Status != 200 || hdrEntry.Proto != "HTTP/2.0" {
		t.Errorf("entry 1 = %d %s, want 200 HTTP/2.0", hdrEntry.Status, hdrEntry.Proto)
	}
	if hdrEntry.Body != "" {
		t.Errorf("entry 1 body = %q, want headers only", hdrEntry.Body)
	}
	if got := hdrEntry.Headers["Set-Cookie"][0]; strings.Contains(got, "supersecretvalue") {
		t.Errorf("entry 1 leaked a response credential: %q", got)
	}

	bodyEntry := e[2]
	if bodyEntry.Body != `{"ok":true}` {
		t.Errorf("entry 2 body = %q, want the response payload", bodyEntry.Body)
	}

	for i, entry := range e {
		if entry.Exchange != e[0].Exchange {
			t.Errorf("entry %d exchange = %d, want all three to share %d", i, entry.Exchange, e[0].Exchange)
		}
	}
}

func TestTraceTransportRecordsTransportError(t *testing.T) {
	entries := collect(t)

	tr := &traceTransport{base: &stubTripper{err: errors.New("dial tcp: refused")}}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://api.example.com/v1/models", nil)

	if _, err := tr.RoundTrip(req); err == nil {
		t.Fatal("RoundTrip succeeded, want the transport error propagated")
	}

	e := *entries
	if len(e) != 2 {
		t.Fatalf("got %d entries, want 2 (request, failed response): %+v", len(e), e)
	}
	if e[1].Err == "" || !strings.Contains(e[1].Err, "refused") {
		t.Errorf("entry 1 Err = %q, want the dial failure", e[1].Err)
	}
	if e[1].Status != 0 {
		t.Errorf("entry 1 status = %d, want 0 — no response was received", e[1].Status)
	}
}

func TestTraceTransportDisabledIsPassthrough(t *testing.T) {
	httplog.SetEnabled(false)
	var got int
	httplog.SetSink(func(httplog.Entry) { got++ })
	t.Cleanup(func() { httplog.SetSink(nil) })

	stub := &stubTripper{resp: jsonResponse(200, "{}")}
	tr := &traceTransport{base: stub}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://api.example.com/", nil)

	if _, err := tr.RoundTrip(req); err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	if got != 0 {
		t.Errorf("sink called %d times with tracing off, want 0", got)
	}
	if stub.gotCalls != 1 {
		t.Errorf("base called %d times, want 1", stub.gotCalls)
	}
}

func TestReadRequestBodyPrefersGetBody(t *testing.T) {
	httplog.SetEnabled(true)
	t.Cleanup(func() { httplog.SetEnabled(false) })

	payload := `{"a":1}`
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, "https://x/", strings.NewReader(payload))
	if req.GetBody == nil {
		t.Fatal("precondition: NewRequest with a *strings.Reader should set GetBody")
	}

	body, truncated := readRequestBody(req)
	if body != payload || truncated {
		t.Errorf("readRequestBody = %q,%v; want %q,false", body, truncated, payload)
	}

	// GetBody hands back an independent reader, so the original must still be
	// fully readable — this is what keeps SDK retries working.
	remaining, _ := io.ReadAll(req.Body)
	if string(remaining) != payload {
		t.Errorf("original body left as %q, want %q", remaining, payload)
	}
}

func TestReadRequestBodyWithoutGetBodyRestoresStream(t *testing.T) {
	httplog.SetEnabled(true)
	t.Cleanup(func() { httplog.SetEnabled(false) })

	payload := "raw-stream-payload"
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, "https://x/", nil)
	req.Body = io.NopCloser(bytes.NewReader([]byte(payload)))
	req.GetBody = nil

	body, _ := readRequestBody(req)
	if body != payload {
		t.Errorf("readRequestBody = %q, want %q", body, payload)
	}
	remaining, _ := io.ReadAll(req.Body)
	if string(remaining) != payload {
		t.Errorf("body left as %q, want it replayable as %q", remaining, payload)
	}
}

func TestReadRequestBodyNil(t *testing.T) {
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://x/", nil)
	if body, tr := readRequestBody(req); body != "" || tr {
		t.Errorf("readRequestBody on a bodyless request = %q,%v; want empty", body, tr)
	}
}

func TestReadRequestBodyTruncates(t *testing.T) {
	httplog.SetEnabled(true)
	httplog.SetMaxBody(8)
	t.Cleanup(func() {
		httplog.SetEnabled(false)
		httplog.SetMaxBody(0)
	})

	payload := strings.Repeat("y", 50)
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, "https://x/", strings.NewReader(payload))

	body, truncated := readRequestBody(req)
	if !truncated {
		t.Error("truncated = false, want true")
	}
	if !strings.HasPrefix(body, strings.Repeat("y", 8)) || !strings.Contains(body, "truncated") {
		t.Errorf("body = %q, want 8 bytes plus a marker", body)
	}
}

func TestBuildTransportInstallsTraceBeneathHeaders(t *testing.T) {
	// With no other customization, tracing alone must still produce a
	// transport — the nil return that means "leave the SDK's client alone"
	// would skip the trace entirely.
	tr, err := BuildTransport(&LLMOptions{TraceHTTP: true})
	if err != nil {
		t.Fatalf("BuildTransport: %v", err)
	}
	tt, ok := tr.(*traceTransport)
	if !ok {
		t.Fatalf("BuildTransport returned %T, want *traceTransport", tr)
	}
	if tt.base != http.DefaultTransport {
		t.Errorf("trace base = %T, want http.DefaultTransport", tt.base)
	}

	// With ExtraHeaders, headerTransport must be on the outside so the trace
	// below it records the headers it injected.
	tr, err = BuildTransport(&LLMOptions{TraceHTTP: true, ExtraHeaders: map[string]string{"X-Team": "pi"}})
	if err != nil {
		t.Fatalf("BuildTransport: %v", err)
	}
	ht, ok := tr.(*headerTransport)
	if !ok {
		t.Fatalf("BuildTransport returned %T, want *headerTransport outermost", tr)
	}
	if _, ok := ht.base.(*traceTransport); !ok {
		t.Errorf("headerTransport wraps %T, want *traceTransport directly beneath it", ht.base)
	}
}

func TestBuildTransportWithoutTraceUnchanged(t *testing.T) {
	tr, err := BuildTransport(&LLMOptions{})
	if err != nil {
		t.Fatalf("BuildTransport: %v", err)
	}
	if tr != nil {
		t.Errorf("BuildTransport with no options = %T, want nil so the SDK client is left alone", tr)
	}
	if tr, err = BuildTransport(nil); err != nil || tr != nil {
		t.Errorf("BuildTransport(nil) = %v,%v; want nil,nil", tr, err)
	}
}

// TestTraceTransportSeesInjectedHeaders is the end-to-end case the layering in
// BuildTransport exists for: a header added by headerTransport must appear in
// the trace, because diagnosing a gateway that rejects a request depends on
// seeing the headers it actually got.
func TestTraceTransportSeesInjectedHeaders(t *testing.T) {
	entries := collect(t)

	stub := &stubTripper{resp: jsonResponse(200, "{}")}
	tr, err := BuildTransport(&LLMOptions{TraceHTTP: true, ExtraHeaders: map[string]string{"X-Team": "pi"}})
	if err != nil {
		t.Fatalf("BuildTransport: %v", err)
	}
	// Swap the real network transport underneath the trace for the stub.
	tr.(*headerTransport).base.(*traceTransport).base = stub

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://x/", nil)
	resp, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	_ = resp.Body.Close()

	got := (*entries)[0].Headers["X-Team"]
	if len(got) == 0 || got[0] != "pi" {
		t.Errorf("trace headers X-Team = %v, want [pi] — the injected header must be visible", got)
	}
}
