package auth

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// TestAbsoluteVerificationURI pins every branch the inline conditional in
// DeviceFlow used to hold: empty, already-absolute (both schemes), relative
// against a good origin, and the three ways resolution can fail.
func TestAbsoluteVerificationURI(t *testing.T) {
	tests := []struct {
		name      string
		deviceURL string
		uri       string
		want      string
	}{
		{"empty is untouched", "https://console.example/device", "", ""},
		{"https absolute is untouched", "https://console.example/device", "https://elsewhere/x", "https://elsewhere/x"},
		{"http absolute is untouched", "https://console.example/device", "http://elsewhere/x", "http://elsewhere/x"},
		{"relative resolves against origin", "https://console.example/device/code", "/auth?code=ABCD", "https://console.example/auth?code=ABCD"},
		{"relative keeps http scheme", "http://console.example:8080/device", "/auth", "http://console.example:8080/auth"},
		{"unparseable device URL leaves uri alone", "://nonsense", "/auth", "/auth"},
		{"device URL without scheme leaves uri alone", "console.example/device", "/auth", "/auth"},
		{"device URL without host leaves uri alone", "file:///tmp/device", "/auth", "/auth"},
		// A scheme-relative URI is not "http://"-prefixed, so the original
		// treated it as relative too. Keep that.
		{"scheme-relative is treated as relative", "https://console.example/device", "//other/auth", "https://console.example//other/auth"},
		// Case matters: the original compared prefixes case-sensitively.
		{"uppercase scheme is treated as relative", "https://console.example/device", "HTTPS://elsewhere/x", "https://console.exampleHTTPS://elsewhere/x"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := absoluteVerificationURI(tt.deviceURL, tt.uri); got != tt.want {
				t.Errorf("absoluteVerificationURI(%q, %q) = %q, want %q", tt.deviceURL, tt.uri, got, tt.want)
			}
		})
	}
}

// TestPostDeviceCodeJSONBody checks the JSON branch sends the right content
// type and payload: client_id always, scope only when non-empty, and every
// extra param merged in.
func TestPostDeviceCodeJSONBody(t *testing.T) {
	tests := []struct {
		name        string
		scopes      []string
		extra       map[string]string
		wantPayload map[string]string
	}{
		{
			name:        "client id only",
			wantPayload: map[string]string{"client_id": "cid"},
		},
		{
			name:        "scopes are space joined",
			scopes:      []string{"read", "write"},
			wantPayload: map[string]string{"client_id": "cid", "scope": "read write"},
		},
		{
			name:        "empty scope key is omitted",
			scopes:      []string{},
			wantPayload: map[string]string{"client_id": "cid"},
		},
		{
			name:        "extra params are merged",
			scopes:      []string{"read"},
			extra:       map[string]string{"audience": "api"},
			wantPayload: map[string]string{"client_id": "cid", "scope": "read", "audience": "api"},
		},
		{
			name:        "extra params can override scope",
			scopes:      []string{"read"},
			extra:       map[string]string{"scope": "overridden"},
			wantPayload: map[string]string{"client_id": "cid", "scope": "overridden"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotPayload map[string]string
			var gotContentType string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotContentType = r.Header.Get("Content-Type")
				body, _ := io.ReadAll(r.Body)
				_ = json.Unmarshal(body, &gotPayload)
				w.WriteHeader(http.StatusOK)
			}))
			defer srv.Close()

			prov := Provider{
				Name:           "test",
				ClientID:       "cid",
				DeviceURL:      srv.URL,
				DeviceJSONBody: true,
				Scopes:         tt.scopes,
				ExtraParams:    tt.extra,
			}
			resp, err := postDeviceCodeRequest(context.Background(), prov)
			if err != nil {
				t.Fatalf("postDeviceCodeRequest: %v", err)
			}
			_ = resp.Body.Close()

			if gotContentType != "application/json" {
				t.Errorf("Content-Type = %q, want application/json", gotContentType)
			}
			if len(gotPayload) != len(tt.wantPayload) {
				t.Fatalf("payload = %v, want %v", gotPayload, tt.wantPayload)
			}
			for k, want := range tt.wantPayload {
				if gotPayload[k] != want {
					t.Errorf("payload[%q] = %q, want %q", k, gotPayload[k], want)
				}
			}
		})
	}
}

// TestPostDeviceCodeFormBody checks the form branch, which always sets scope
// (even empty) — unlike the JSON branch, which omits it. That asymmetry was in
// the original and must survive.
func TestPostDeviceCodeFormBody(t *testing.T) {
	var gotForm url.Values
	var gotContentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotContentType = r.Header.Get("Content-Type")
		_ = r.ParseForm()
		gotForm = r.PostForm
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	prov := Provider{
		Name:        "test",
		ClientID:    "cid",
		DeviceURL:   srv.URL,
		ExtraParams: map[string]string{"audience": "api"},
	}
	resp, err := postDeviceCodeRequest(context.Background(), prov)
	if err != nil {
		t.Fatalf("postDeviceCodeRequest: %v", err)
	}
	_ = resp.Body.Close()

	if !strings.HasPrefix(gotContentType, "application/x-www-form-urlencoded") {
		t.Errorf("Content-Type = %q, want form encoding", gotContentType)
	}
	if got := gotForm.Get("client_id"); got != "cid" {
		t.Errorf("client_id = %q, want cid", got)
	}
	if _, ok := gotForm["scope"]; !ok {
		t.Error("form has no scope key; the form branch always sets one, even empty")
	}
	if got := gotForm.Get("audience"); got != "api" {
		t.Errorf("audience = %q, want api", got)
	}
}

// TestPostDeviceCodeRequestErrors covers the failure paths of both branches.
// Every one must carry the same "device code request: " prefix the original
// wrapped them with, whichever branch produced it.
func TestPostDeviceCodeRequestErrors(t *testing.T) {
	tests := []struct {
		name string
		prov Provider
	}{
		{
			name: "json branch: unparseable device URL fails at NewRequest",
			prov: Provider{Name: "t", ClientID: "c", DeviceURL: "://nonsense", DeviceJSONBody: true},
		},
		{
			name: "json branch: unsupported scheme fails at Do",
			prov: Provider{Name: "t", ClientID: "c", DeviceURL: "ftp://example.invalid/device", DeviceJSONBody: true},
		},
		{
			name: "form branch: unparseable device URL fails at PostForm",
			prov: Provider{Name: "t", ClientID: "c", DeviceURL: "://nonsense"},
		},
		{
			name: "form branch: unsupported scheme fails at PostForm",
			prov: Provider{Name: "t", ClientID: "c", DeviceURL: "ftp://example.invalid/device"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := postDeviceCodeRequest(context.Background(), tt.prov)
			if err == nil {
				_ = resp.Body.Close()
				t.Fatal("expected an error, got nil")
			}
			if resp != nil {
				t.Error("expected a nil response alongside the error")
			}
			if !strings.HasPrefix(err.Error(), "device code request: ") {
				t.Errorf("error = %q, want the shared %q prefix", err, "device code request: ")
			}
		})
	}
}

// TestDeviceFlowNoDeviceURL is the one guard DeviceFlow keeps inline.
func TestDeviceFlowNoDeviceURL(t *testing.T) {
	_, err := DeviceFlow(context.Background(), Provider{Name: "acme"})
	if err == nil {
		t.Fatal("expected an error for a provider without a device endpoint")
	}
	if want := "provider acme does not support device code flow"; err.Error() != want {
		t.Errorf("error = %q, want %q", err, want)
	}
}

// TestDeviceFlowResponseHandling walks the response-side branches: HTTP
// failure, malformed JSON, the interval default, and URI resolution.
func TestDeviceFlowResponseHandling(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		body       string
		jsonBody   bool
		wantErr    string // substring; empty means success expected
		wantIntvl  int
		wantExpiry int
		// wantComplete is checked with the server URL's origin substituted for
		// the literal "ORIGIN", since httptest picks the port.
		wantComplete string
	}{
		{
			name:    "non-200 reports status and body",
			status:  http.StatusBadRequest,
			body:    `{"error":"invalid_client"}`,
			wantErr: "device code request failed (400)",
		},
		{
			name:    "500 also reports",
			status:  http.StatusInternalServerError,
			body:    "boom",
			wantErr: "device code request failed (500)",
		},
		{
			name:    "malformed JSON is reported as a parse failure",
			status:  http.StatusOK,
			body:    "not json",
			wantErr: "parsing device code response",
		},
		{
			name:      "absent interval defaults to 5",
			status:    http.StatusOK,
			body:      `{"device_code":"dc","user_code":"UC","verification_uri":"https://v/","expires_in":600}`,
			wantIntvl: 5, wantExpiry: 600,
		},
		{
			name:      "explicit interval is kept",
			status:    http.StatusOK,
			body:      `{"device_code":"dc","user_code":"UC","interval":9,"expires_in":600}`,
			wantIntvl: 9, wantExpiry: 600,
		},
		{
			name:      "relative verification_uri_complete is resolved",
			status:    http.StatusOK,
			body:      `{"device_code":"dc","interval":3,"verification_uri_complete":"/auth?code=UC"}`,
			wantIntvl: 3, wantComplete: "ORIGIN/auth?code=UC",
		},
		{
			name:      "absolute verification_uri_complete is left alone",
			status:    http.StatusOK,
			body:      `{"device_code":"dc","interval":3,"verification_uri_complete":"https://one.click/x"}`,
			wantIntvl: 3, wantComplete: "https://one.click/x",
		},
		{
			name:      "json body branch reaches the same response handling",
			status:    http.StatusOK,
			body:      `{"device_code":"dc","interval":7}`,
			jsonBody:  true,
			wantIntvl: 7,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = io.WriteString(w, tt.body)
			}))
			defer srv.Close()

			prov := Provider{Name: "t", ClientID: "c", DeviceURL: srv.URL, DeviceJSONBody: tt.jsonBody}
			dcr, err := DeviceFlow(context.Background(), prov)

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected an error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("error = %q, want it to contain %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("DeviceFlow: %v", err)
			}
			if dcr.Interval != tt.wantIntvl {
				t.Errorf("Interval = %d, want %d", dcr.Interval, tt.wantIntvl)
			}
			if dcr.ExpiresIn != tt.wantExpiry {
				t.Errorf("ExpiresIn = %d, want %d", dcr.ExpiresIn, tt.wantExpiry)
			}
			want := strings.ReplaceAll(tt.wantComplete, "ORIGIN", srv.URL)
			if dcr.VerificationURIComplete != want {
				t.Errorf("VerificationURIComplete = %q, want %q", dcr.VerificationURIComplete, want)
			}
		})
	}
}

// TestDeviceFlowRequestErrorIsWrapped checks that a transport failure reaches
// the caller through DeviceFlow with the same wrapping as before the split.
func TestDeviceFlowRequestErrorIsWrapped(t *testing.T) {
	_, err := DeviceFlow(context.Background(), Provider{
		Name:      "t",
		ClientID:  "c",
		DeviceURL: "ftp://example.invalid/device",
	})
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.HasPrefix(err.Error(), "device code request: ") {
		t.Errorf("error = %q, want the %q prefix", err, "device code request: ")
	}
}
