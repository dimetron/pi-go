package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// codexDeviceProvider points a codex-shaped provider at a test server.
func codexDeviceProvider(base string) Provider {
	return Provider{
		Name:              "codex",
		EnvVar:            "OPENAI_API_KEY",
		TokenURL:          base + "/oauth/token",
		ClientID:          "app_test",
		CodexDeviceAuth:   true,
		DeviceURL:         base + "/api/accounts/deviceauth",
		DeviceVerifyURL:   base + "/codex/device",
		DeviceRedirectURI: base + "/deviceauth/callback",
		TokenToKey:        func(tok *TokenResponse) string { return tok.AccessToken },
	}
}

func TestStartCodexDeviceFlow(t *testing.T) {
	tests := []struct {
		name         string
		status       int
		body         string
		wantErr      string
		wantUserCode string
		wantInterval time.Duration
	}{
		{
			// auth.openai.com sends interval as a string, which is the only
			// form the codex CLI parses.
			name:         "interval as string",
			status:       http.StatusOK,
			body:         `{"device_auth_id":"dev-1","user_code":"ABCD-EFGH","interval":"3"}`,
			wantUserCode: "ABCD-EFGH",
			wantInterval: 3 * time.Second,
		},
		{
			name:         "interval as number",
			status:       http.StatusOK,
			body:         `{"device_auth_id":"dev-1","user_code":"ABCD-EFGH","interval":7}`,
			wantUserCode: "ABCD-EFGH",
			wantInterval: 7 * time.Second,
		},
		{
			name:         "missing interval falls back to default",
			status:       http.StatusOK,
			body:         `{"device_auth_id":"dev-1","user_code":"ABCD-EFGH"}`,
			wantUserCode: "ABCD-EFGH",
			wantInterval: codexDeviceDefaultInterval,
		},
		{
			name:         "usercode alias is accepted",
			status:       http.StatusOK,
			body:         `{"device_auth_id":"dev-1","usercode":"WXYZ-1234","interval":"5"}`,
			wantUserCode: "WXYZ-1234",
			wantInterval: 5 * time.Second,
		},
		{
			name:    "404 reports device auth unavailable",
			status:  http.StatusNotFound,
			body:    `{"error":"not_found"}`,
			wantErr: "not enabled",
		},
		{
			name:    "incomplete response is rejected",
			status:  http.StatusOK,
			body:    `{"interval":"5"}`,
			wantErr: "missing device_auth_id",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !strings.HasSuffix(r.URL.Path, "/deviceauth/usercode") {
					t.Errorf("unexpected path %s", r.URL.Path)
				}
				if ct := r.Header.Get("Content-Type"); ct != "application/json" {
					t.Errorf("Content-Type = %q, want application/json", ct)
				}
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()

			sess, err := StartCodexDeviceFlow(t.Context(), codexDeviceProvider(srv.URL))

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("error = %v, want it to contain %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if sess.UserCode != tt.wantUserCode {
				t.Errorf("UserCode = %q, want %q", sess.UserCode, tt.wantUserCode)
			}
			if sess.interval != tt.wantInterval {
				t.Errorf("interval = %v, want %v", sess.interval, tt.wantInterval)
			}
		})
	}
}

// TestCompleteCodexDeviceFlow_PollsThenExchanges covers the whole approval
// path: 403 means "not approved yet", and the verifier the server hands back
// must be what reaches the token endpoint.
func TestCompleteCodexDeviceFlow_PollsThenExchanges(t *testing.T) {
	var polls atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/deviceauth/usercode"):
			_, _ = w.Write([]byte(`{"device_auth_id":"dev-1","user_code":"ABCD-EFGH","interval":"1"}`))

		case strings.HasSuffix(r.URL.Path, "/deviceauth/token"):
			var req map[string]string
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Errorf("decoding poll body: %v", err)
			}
			if req["device_auth_id"] != "dev-1" || req["user_code"] != "ABCD-EFGH" {
				t.Errorf("poll body = %v, want device_auth_id and user_code from the session", req)
			}
			// Withhold approval on the first poll so the 403 path is exercised.
			if polls.Add(1) < 2 {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			_, _ = w.Write([]byte(`{"authorization_code":"auth-code","code_challenge":"chal","code_verifier":"verifier-xyz"}`))

		case strings.HasSuffix(r.URL.Path, "/oauth/token"):
			if err := r.ParseForm(); err != nil {
				t.Errorf("parsing exchange form: %v", err)
			}
			if got := r.PostForm.Get("code_verifier"); got != "verifier-xyz" {
				t.Errorf("code_verifier = %q, want the server-supplied verifier", got)
			}
			if got := r.PostForm.Get("code"); got != "auth-code" {
				t.Errorf("code = %q, want auth-code", got)
			}
			if got := r.PostForm.Get("grant_type"); got != "authorization_code" {
				t.Errorf("grant_type = %q, want authorization_code", got)
			}
			if !strings.HasSuffix(r.PostForm.Get("redirect_uri"), "/deviceauth/callback") {
				t.Errorf("redirect_uri = %q, want the deviceauth callback", r.PostForm.Get("redirect_uri"))
			}
			_, _ = w.Write([]byte(`{"access_token":"tok-123","token_type":"Bearer"}`))

		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	prov := codexDeviceProvider(srv.URL)

	sess, err := StartCodexDeviceFlow(t.Context(), prov)
	if err != nil {
		t.Fatalf("StartCodexDeviceFlow: %v", err)
	}

	result, err := CompleteCodexDeviceFlow(t.Context(), prov, sess)
	if err != nil {
		t.Fatalf("CompleteCodexDeviceFlow: %v", err)
	}
	if result.Err != nil {
		t.Fatalf("result.Err = %v", result.Err)
	}
	if result.APIKey != "tok-123" {
		t.Errorf("APIKey = %q, want tok-123", result.APIKey)
	}
	if result.EnvVar != "OPENAI_API_KEY" {
		t.Errorf("EnvVar = %q, want OPENAI_API_KEY", result.EnvVar)
	}
	if got := polls.Load(); got != 2 {
		t.Errorf("polled %d times, want 2 (one 403 then success)", got)
	}
}

// TestCompleteCodexDeviceFlow_ServerError checks that an unexpected status
// stops the loop instead of being read as "keep waiting".
func TestCompleteCodexDeviceFlow_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/deviceauth/usercode") {
			_, _ = w.Write([]byte(`{"device_auth_id":"dev-1","user_code":"ABCD-EFGH","interval":"1"}`))
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"boom"}`))
	}))
	defer srv.Close()

	prov := codexDeviceProvider(srv.URL)

	sess, err := StartCodexDeviceFlow(t.Context(), prov)
	if err != nil {
		t.Fatalf("StartCodexDeviceFlow: %v", err)
	}

	result, err := CompleteCodexDeviceFlow(t.Context(), prov, sess)
	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	if result.Err == nil {
		t.Fatal("expected result.Err for a 500 response")
	}
	if !strings.Contains(result.Err.Error(), "500") {
		t.Errorf("result.Err = %v, want it to mention the status", result.Err)
	}
}

func TestStartCodexDeviceFlow_UnsupportedProvider(t *testing.T) {
	t.Parallel()

	_, err := StartCodexDeviceFlow(t.Context(), Provider{Name: "anthropic"})
	if err == nil {
		t.Fatal("expected an error for a provider without codex device auth")
	}
	if !strings.Contains(err.Error(), "does not support") {
		t.Errorf("error = %v, want it to say the provider is unsupported", err)
	}
}
