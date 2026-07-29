package cli

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strings"
	"testing"

	"github.com/dimetron/pi-go/internal/provider"
)

// captureWriter returns a pingWriter that accumulates everything written to it,
// plus a func to read the accumulated text back.
func captureWriter(t *testing.T) (pingWriter, func() string) {
	t.Helper()
	var sb strings.Builder
	w := pingWriter(func(format string, a ...any) {
		fmt.Fprintf(&sb, format, a...)
	})
	return w, sb.String
}

func TestMaskAPIKey(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		key  string
		want string
	}{
		{"long key masked", "sk-proj-abcdefghijklmnop", "sk-p...mnop"},
		{"exactly eight untouched", "12345678", "12345678"},
		{"nine gets masked", "123456789", "1234...6789"},
		{"short untouched", "abc", "abc"},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := maskAPIKey(tt.key); got != tt.want {
				t.Errorf("maskAPIKey(%q) = %q, want %q", tt.key, got, tt.want)
			}
		})
	}
}

func TestMaskAPIKeyNeverLeaksMiddle(t *testing.T) {
	t.Parallel()
	const secret = "sk-SUPERSECRETMIDDLE-tail"
	got := maskAPIKey(secret)
	if strings.Contains(got, "SUPERSECRETMIDDLE") {
		t.Errorf("maskAPIKey leaked the middle of the key: %q", got)
	}
}

func TestPingPort(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{"explicit port wins", "https://example.com:8443/v1", "8443"},
		{"https default", "https://api.anthropic.com/v1/messages", "443"},
		{"http default", "http://localhost/v1", "80"},
		{"explicit http port", "http://localhost:11434/api", "11434"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			u, err := url.Parse(tt.raw)
			if err != nil {
				t.Fatalf("parsing %q: %v", tt.raw, err)
			}
			if got := pingPort(u); got != tt.want {
				t.Errorf("pingPort(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

func TestSetPingAuthHeaders(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		provider    string
		apiKey      string
		wantHeaders map[string]string
		wantQuery   string
	}{
		{
			name:        "anthropic sets key and version",
			provider:    "anthropic",
			apiKey:      "sk-ant-123",
			wantHeaders: map[string]string{"X-Api-Key": "sk-ant-123", "Anthropic-Version": "2023-06-01"},
		},
		{
			name:        "openai uses bearer",
			provider:    "openai",
			apiKey:      "sk-openai",
			wantHeaders: map[string]string{"Authorization": "Bearer sk-openai"},
		},
		{
			name:        "azure uses api-key header",
			provider:    "azure",
			apiKey:      "azure-key",
			wantHeaders: map[string]string{"Api-Key": "azure-key"},
		},
		{
			name:      "gemini puts key in query",
			provider:  "gemini",
			apiKey:    "gem-key",
			wantQuery: "key=gem-key",
		},
		{
			name:     "empty key sets nothing",
			provider: "anthropic",
			apiKey:   "",
		},
		{
			name:     "unknown provider sets nothing",
			provider: "mistral",
			apiKey:   "some-key",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			req, err := http.NewRequest(http.MethodGet, "https://example.com/v1/models", nil)
			if err != nil {
				t.Fatalf("building request: %v", err)
			}
			setPingAuthHeaders(req, tt.provider, tt.apiKey)

			for k, want := range tt.wantHeaders {
				if got := req.Header.Get(k); got != want {
					t.Errorf("header %s = %q, want %q", k, got, want)
				}
			}
			if tt.wantQuery != "" && req.URL.RawQuery != tt.wantQuery {
				t.Errorf("query = %q, want %q", req.URL.RawQuery, tt.wantQuery)
			}
			if tt.wantHeaders == nil && tt.wantQuery == "" {
				if len(req.Header) != 0 {
					t.Errorf("expected no headers, got %v", req.Header)
				}
				if req.URL.RawQuery != "" {
					t.Errorf("expected no query, got %q", req.URL.RawQuery)
				}
			}
		})
	}
}

func TestDumpPingRequestMasksCredentials(t *testing.T) {
	t.Parallel()
	req, err := http.NewRequest(http.MethodGet, "https://example.com/v1/models", nil)
	if err != nil {
		t.Fatalf("building request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer sk-super-secret-token-value")
	req.Header.Set("X-Api-Key", "sk-another-secret-value")
	req.Header.Set("User-Agent", "pi-go/test")

	w, read := captureWriter(t)
	dumpPingRequest(w, req)
	out := read()

	for _, secret := range []string{"sk-super-secret-token-value", "sk-another-secret-value"} {
		if strings.Contains(out, secret) {
			t.Errorf("dumpPingRequest leaked credential %q in:\n%s", secret, out)
		}
	}
	if !strings.Contains(out, "pi-go/test") {
		t.Error("expected non-credential headers to be shown verbatim")
	}
	if !strings.Contains(out, "Host: example.com") {
		t.Errorf("expected Host line, got:\n%s", out)
	}
}

// tlsConnectionStateStub is a zero handshake state, enough for the trace
// callback which ignores its argument.
func tlsConnectionStateStub() tls.ConnectionState { return tls.ConnectionState{} }

// pingVerdictTarget builds a pingTarget for verdict tests.
func pingVerdictTarget(providerName string) *pingTarget {
	return &pingTarget{
		info:    provider.Info{Provider: providerName, Model: "test-model"},
		baseURL: "https://example.com",
		opts:    &provider.LLMOptions{},
	}
}

func TestReportHTTPVerdict(t *testing.T) {
	// Not parallel: the azure cases read the package-level flagURL.
	tests := []struct {
		name     string
		provider string
		status   int
		flagURL  string
		wantOK   bool
		wantText string
	}{
		{"200 is alive", "anthropic", http.StatusOK, "", true, "Endpoint reachable"},
		{"204 is alive", "anthropic", http.StatusNoContent, "", true, "Endpoint reachable"},
		{"401 is dead", "anthropic", http.StatusUnauthorized, "", false, "Authentication failed"},
		{"403 is dead", "openai", http.StatusForbidden, "", false, "Authentication failed"},
		{"404 is dead for non-azure", "openai", http.StatusNotFound, "", false, "Endpoint not found"},
		{"404 is alive for azure", "azure", http.StatusNotFound, "", true, "continuing with model ping"},
		{"405 is alive", "openai", http.StatusMethodNotAllowed, "", true, "requires POST"},
		{"422 is dead without custom url", "azure", http.StatusUnprocessableEntity, "", false, "Unexpected status"},
		{"422 is alive with custom url", "azure", http.StatusUnprocessableEntity, "https://proxy.example", true, "continuing with model ping"},
		{"401 is alive for custom azure", "azure", http.StatusUnauthorized, "https://proxy.example", true, "continuing with model ping"},
		{"429 is alive", "openai", http.StatusTooManyRequests, "", true, "Rate limited"},
		{"500 is dead", "openai", http.StatusInternalServerError, "", false, "Server error"},
		{"503 is dead", "openai", http.StatusServiceUnavailable, "", false, "Server error"},
		{"302 is unexpected", "openai", http.StatusFound, "", false, "Unexpected status"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			origURL := flagURL
			flagURL = tt.flagURL
			t.Cleanup(func() { flagURL = origURL })

			target := pingVerdictTarget(tt.provider)
			resp := &http.Response{StatusCode: tt.status, Status: http.StatusText(tt.status)}

			w, read := captureWriter(t)
			got := target.reportHTTPVerdict(w, resp)

			if got != tt.wantOK {
				t.Errorf("reportHTTPVerdict(status %d, provider %s) = %v, want %v",
					tt.status, tt.provider, got, tt.wantOK)
			}
			if out := read(); !strings.Contains(out, tt.wantText) {
				t.Errorf("expected %q in output, got:\n%s", tt.wantText, out)
			}
		})
	}
}

func TestReportReply(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		reply      string
		isPingPong bool
		want       string
	}{
		{"ping-pong match", "prompt-prompt", true, "Prompt:Prompt OK"},
		{"ping-pong match is case-insensitive", "PROMPT", true, "Prompt:Prompt OK"},
		{"ping-pong mismatch still alive", "hello there", true, "response unexpected"},
		{"custom prompt is alive", "4", false, "is ALIVE"},
		{"custom prompt skips ping-pong wording", "4", false, "is ALIVE"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			target := pingVerdictTarget("openai")
			w, read := captureWriter(t)
			target.reportReply(w, tt.reply, tt.isPingPong)

			out := read()
			if !strings.Contains(out, tt.want) {
				t.Errorf("expected %q in output, got:\n%s", tt.want, out)
			}
			if !strings.Contains(out, "Model replied") {
				t.Errorf("expected the reply to be echoed, got:\n%s", out)
			}
		})
	}
}

func TestReportReplyCustomPromptNeverClaimsPingPong(t *testing.T) {
	t.Parallel()
	target := pingVerdictTarget("openai")
	w, read := captureWriter(t)
	// A custom-prompt reply that happens to contain "prompt" must not be
	// reported as a Prompt:Prompt result.
	target.reportReply(w, "your prompt was unclear", false)

	if out := read(); strings.Contains(out, "Prompt:Prompt OK") {
		t.Errorf("custom prompt run reported a Prompt:Prompt result:\n%s", out)
	}
}

func TestPrintHeader(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		apiKey   string
		wantText string
	}{
		{"missing key is called out", "", "(not set)"},
		{"present key is masked", "sk-abcdefghijklmnop", "sk-a...mnop"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			target := pingVerdictTarget("openai")
			target.apiKey = tt.apiKey

			w, read := captureWriter(t)
			target.printHeader(w)

			out := read()
			if !strings.Contains(out, tt.wantText) {
				t.Errorf("expected %q in header, got:\n%s", tt.wantText, out)
			}
			if !strings.Contains(out, "test-model") {
				t.Errorf("expected the model name in the header, got:\n%s", out)
			}
			if tt.apiKey != "" && strings.Contains(out, tt.apiKey) {
				t.Errorf("header leaked the raw key:\n%s", out)
			}
		})
	}
}

func TestDumpPingResponse(t *testing.T) {
	t.Parallel()
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}

	w, read := captureWriter(t)
	dumpPingResponse(w, resp, 0)

	out := read()
	for _, want := range []string{"HTTP/1.1 200 OK", "Content-Type: application/json", "Total HTTP time"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output, got:\n%s", want, out)
		}
	}
}

func TestPingClientTraceDoesNotPanic(t *testing.T) {
	t.Parallel()
	w, read := captureWriter(t)
	trace := pingClientTrace(w)

	// GotFirstResponseByte before GotConn must not report a bogus TTFB.
	trace.GotFirstResponseByte()
	if strings.Contains(read(), "TTFB") {
		t.Error("TTFB reported without a connection")
	}

	// A failed connect reports nothing; a successful one does.
	trace.ConnectStart("tcp", "example.com:443")
	trace.ConnectDone("tcp", "example.com:443", errors.New("dial failed"))
	if strings.Contains(read(), "TCP connected") {
		t.Error("failed connect should not report success")
	}
	trace.ConnectDone("tcp", "example.com:443", nil)
	if !strings.Contains(read(), "TCP connected") {
		t.Error("successful connect should be reported")
	}

	trace.GotConn(httptrace.GotConnInfo{})
	trace.GotFirstResponseByte()
	if !strings.Contains(read(), "TTFB") {
		t.Error("TTFB should be reported once a connection exists")
	}

	// The TLS callbacks must be safe to call in either order too.
	trace.TLSHandshakeStart()
	trace.TLSHandshakeDone(tlsConnectionStateStub(), nil)
	if !strings.Contains(read(), "TLS done") {
		t.Error("TLS handshake completion should be reported")
	}
}
