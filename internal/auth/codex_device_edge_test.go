package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The interval encoding is the field most likely to change upstream, and a
// parse failure there must not be mistaken for a valid zero.
func TestCodexIntervalUnmarshal(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    codexInterval
		wantErr bool
	}{
		{name: "quoted number", input: `"5"`, want: 5},
		{name: "bare number", input: `9`, want: 9},
		{name: "empty string leaves the zero value", input: `""`},
		{name: "null leaves the zero value", input: `null`},
		{name: "whitespace only", input: `"  "`},
		{name: "not a number", input: `"soon"`, wantErr: true},
		{name: "float is rejected", input: `1.5`, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got codexInterval
			err := got.UnmarshalJSON([]byte(tt.input))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("UnmarshalJSON(%s) = nil error, want a parse failure", tt.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("UnmarshalJSON(%s) error = %v", tt.input, err)
			}
			if got != tt.want {
				t.Errorf("interval = %d, want %d", got, tt.want)
			}
		})
	}
}

// A server that cannot be reached at all has to be reported as a start
// failure, not as a session with empty fields.
func TestStartCodexDeviceFlow_UnreachableServer(t *testing.T) {
	prov := codexDeviceProvider("http://127.0.0.1:1")

	_, err := StartCodexDeviceFlow(context.Background(), prov)
	if err == nil {
		t.Fatal("StartCodexDeviceFlow() = nil error, want a transport failure")
	}
	if !strings.Contains(err.Error(), "requesting device user code") {
		t.Errorf("error = %q, want it to name the failed step", err)
	}
}

// A 200 whose body is not the expected JSON must fail rather than yield a
// session with an empty device id that would poll forever.
func TestStartCodexDeviceFlow_MalformedBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{not json`))
	}))
	defer srv.Close()

	_, err := StartCodexDeviceFlow(context.Background(), codexDeviceProvider(srv.URL))
	if err == nil {
		t.Fatal("StartCodexDeviceFlow() = nil error, want a parse failure")
	}
	if !strings.Contains(err.Error(), "parsing device user code response") {
		t.Errorf("error = %q, want it to name the parse step", err)
	}
}

// A 404 means the auth server does not route /deviceauth at all and gets its
// own message; any other non-200 has to report the server's own reason so the
// user can tell a misconfiguration from an outage.
func TestStartCodexDeviceFlow_ServerRejection(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{
			name:   "not routed",
			status: http.StatusNotFound,
			body:   `{"error":"no such route"}`,
			want:   "device auth is not enabled",
		},
		{
			name:   "server error reports the status",
			status: http.StatusInternalServerError,
			body:   `{"error":"upstream unavailable"}`,
			want:   "500",
		},
		{
			name:   "unauthorized reports the status",
			status: http.StatusUnauthorized,
			body:   `{"error":"bad client"}`,
			want:   "401",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()

			_, err := StartCodexDeviceFlow(context.Background(), codexDeviceProvider(srv.URL))
			if err == nil {
				t.Fatalf("StartCodexDeviceFlow() = nil error, want a rejection for status %d", tt.status)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %q, want it to contain %q", err, tt.want)
			}
		})
	}
}

func TestCompleteCodexDeviceFlow_NilSession(t *testing.T) {
	_, err := CompleteCodexDeviceFlow(context.Background(), codexDeviceProvider("http://127.0.0.1:1"), nil)
	if err == nil {
		t.Fatal("CompleteCodexDeviceFlow(nil) = nil error, want a refusal")
	}
}

// An approval that arrives without the verifier cannot be exchanged, so it has
// to fail here rather than at the token endpoint with a confusing message.
func TestCompleteCodexDeviceFlow_ApprovalMissingFields(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/usercode") {
			_, _ = w.Write([]byte(`{"device_auth_id":"dev-1","user_code":"ABCD-EFGH","interval":"1"}`))
			return
		}
		// Approved, but with no code_verifier.
		_, _ = w.Write([]byte(`{"authorization_code":"auth-1"}`))
	}))
	defer srv.Close()

	prov := codexDeviceProvider(srv.URL)
	sess, err := StartCodexDeviceFlow(context.Background(), prov)
	if err != nil {
		t.Fatalf("StartCodexDeviceFlow() error = %v", err)
	}

	result, err := CompleteCodexDeviceFlow(context.Background(), prov, sess)
	if err != nil {
		t.Fatalf("CompleteCodexDeviceFlow() error = %v, want the failure in Result.Err", err)
	}
	if result.Err == nil {
		t.Fatal("Result.Err = nil, want the incomplete approval reported")
	}
	if !strings.Contains(result.Err.Error(), "code_verifier") {
		t.Errorf("Result.Err = %v, want it to name the missing field", result.Err)
	}
}

// An approved code whose token exchange is rejected must surface that, not the
// approval that already succeeded.
func TestCompleteCodexDeviceFlow_TokenExchangeRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/usercode"):
			_, _ = w.Write([]byte(`{"device_auth_id":"dev-1","user_code":"ABCD-EFGH","interval":"1"}`))
		case strings.HasSuffix(r.URL.Path, "/deviceauth"), strings.Contains(r.URL.Path, "deviceauth/token"):
			_, _ = w.Write([]byte(`{"authorization_code":"auth-1","code_verifier":"verifier-1"}`))
		default: // /oauth/token
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
		}
	}))
	defer srv.Close()

	prov := codexDeviceProvider(srv.URL)
	sess, err := StartCodexDeviceFlow(context.Background(), prov)
	if err != nil {
		t.Fatalf("StartCodexDeviceFlow() error = %v", err)
	}

	result, err := CompleteCodexDeviceFlow(context.Background(), prov, sess)
	if err != nil {
		t.Fatalf("CompleteCodexDeviceFlow() error = %v, want the failure in Result.Err", err)
	}
	if result.Err == nil {
		t.Fatal("Result.Err = nil, want the rejected exchange reported")
	}
	if !strings.Contains(result.Err.Error(), "device code exchange") {
		t.Errorf("Result.Err = %v, want it to name the exchange step", result.Err)
	}
}

// An approval body that is not JSON must fail the poll rather than be treated
// as a pending response and retried until the deadline.
func TestPollCodexApproval_MalformedApproval(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/usercode") {
			_, _ = w.Write([]byte(`{"device_auth_id":"dev-1","user_code":"ABCD-EFGH","interval":"1"}`))
			return
		}
		_, _ = w.Write([]byte(`{oops`))
	}))
	defer srv.Close()

	prov := codexDeviceProvider(srv.URL)
	sess, err := StartCodexDeviceFlow(context.Background(), prov)
	if err != nil {
		t.Fatalf("StartCodexDeviceFlow() error = %v", err)
	}

	_, err = pollCodexApproval(context.Background(), prov, sess)
	if err == nil {
		t.Fatal("pollCodexApproval() = nil error, want a parse failure")
	}
	if !strings.Contains(err.Error(), "parsing device authorization response") {
		t.Errorf("error = %q, want it to name the parse step", err)
	}
}

// The poll loop must stop when its context is canceled — a user who hits Esc
// should not leave a poller running against the auth server.
func TestPollCodexApproval_StopsOnCanceledContext(t *testing.T) {
	polled := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/usercode") {
			_, _ = w.Write([]byte(`{"device_auth_id":"dev-1","user_code":"ABCD-EFGH","interval":"1"}`))
			return
		}
		select {
		case polled <- struct{}{}:
		default:
		}
		w.WriteHeader(http.StatusForbidden) // never approved
	}))
	defer srv.Close()

	prov := codexDeviceProvider(srv.URL)
	sess, err := StartCodexDeviceFlow(context.Background(), prov)
	if err != nil {
		t.Fatalf("StartCodexDeviceFlow() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-polled
		cancel()
	}()

	done := make(chan error, 1)
	go func() {
		_, pollErr := pollCodexApproval(ctx, prov, sess)
		done <- pollErr
	}()

	select {
	case pollErr := <-done:
		if pollErr == nil {
			t.Fatal("pollCodexApproval() = nil error, want the cancellation reported")
		}
	case <-time.After(15 * time.Second):
		t.Fatal("pollCodexApproval did not stop after its context was canceled")
	}
}

// A poll that loses the server mid-flight is a transport failure, not a
// pending approval.
func TestPollCodexApproval_TransportFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"device_auth_id":"dev-1","user_code":"ABCD-EFGH","interval":"1"}`))
	}))
	prov := codexDeviceProvider(srv.URL)
	sess, err := StartCodexDeviceFlow(context.Background(), prov)
	if err != nil {
		t.Fatalf("StartCodexDeviceFlow() error = %v", err)
	}
	srv.Close() // the poll now has nothing to talk to

	_, err = pollCodexApproval(context.Background(), prov, sess)
	if err == nil {
		t.Fatal("pollCodexApproval() = nil error, want a transport failure")
	}
	if !strings.Contains(err.Error(), "polling device authorization") {
		t.Errorf("error = %q, want it to name the polling step", err)
	}
}

func TestPostJSON_RejectsUnencodablePayload(t *testing.T) {
	_, _, err := postJSON(context.Background(), "http://127.0.0.1:1", make(chan int))
	if err == nil {
		t.Fatal("postJSON() = nil error, want the encoding failure")
	}
	if !strings.Contains(err.Error(), "encoding request") {
		t.Errorf("error = %q, want it to name the encoding step", err)
	}
}

func TestPostJSON_RejectsInvalidEndpoint(t *testing.T) {
	// A control character cannot appear in a request URL.
	_, _, err := postJSON(context.Background(), "http://exa\x7fmple.invalid/", map[string]string{})
	if err == nil {
		t.Fatal("postJSON() = nil error, want the request to be rejected")
	}
}
