package cli

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/dimetron/pi-go/internal/auth"
)

// newCodexDeviceServer stands in for auth.openai.com's device endpoints.
// pollsBeforeApproval answers that many polls with 403 ("not approved yet"),
// which is the only pending signal the protocol has.
func newCodexDeviceServer(t *testing.T, pollsBeforeApproval int) *httptest.Server {
	t.Helper()
	polls := 0
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/deviceauth/usercode":
			fmt.Fprint(w, `{"device_auth_id":"dev-1","user_code":"ABCD-9876","interval":"1"}`)
		case "/deviceauth/token":
			polls++
			if polls <= pollsBeforeApproval {
				w.WriteHeader(http.StatusForbidden)
				fmt.Fprint(w, `{"error":"pending"}`)
				return
			}
			fmt.Fprint(w, `{"authorization_code":"auth-1","code_verifier":"verifier-1"}`)
		case "/oauth/token":
			fmt.Fprint(w, `{"access_token":"codex-cli-token","token_type":"bearer"}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func codexDeviceProvider(srvURL string) auth.Provider {
	return auth.Provider{
		Name:              "codex",
		EnvVar:            "OPENAI_API_KEY",
		DeviceURL:         srvURL + "/deviceauth",
		TokenURL:          srvURL + "/oauth/token",
		ClientID:          "pi-go-cli",
		CodexDeviceAuth:   true,
		DeviceVerifyURL:   "https://auth.openai.com/codex/device",
		DeviceRedirectURI: "http://localhost/callback",
		TokenToKey:        func(tok *auth.TokenResponse) string { return tok.AccessToken },
	}
}

// The code and URL have to be printed before polling begins: in a headless
// container this output is the entire user interface for the login.
func TestRunCodexDeviceFlow_PrintsCodeThenCompletes(t *testing.T) {
	srv := newCodexDeviceServer(t, 1)
	defer srv.Close()

	// Keep the flow from launching a real browser.
	t.Setenv("PATH", t.TempDir())
	t.Setenv("BROWSER", "")

	var result *auth.Result
	var err error
	out := captureStdout(t, func() {
		result, err = runCodexDeviceFlow(context.Background(), codexDeviceProvider(srv.URL))
	})
	if err != nil {
		t.Fatalf("runCodexDeviceFlow() error = %v", err)
	}
	if result.Err != nil {
		t.Fatalf("device auth failed: %v", result.Err)
	}
	if result.APIKey != "codex-cli-token" {
		t.Errorf("APIKey = %q, want codex-cli-token", result.APIKey)
	}
	if result.EnvVar != "OPENAI_API_KEY" {
		t.Errorf("EnvVar = %q, want OPENAI_API_KEY", result.EnvVar)
	}

	if !strings.Contains(out, "ABCD-9876") {
		t.Errorf("stdout = %q, want the user code", out)
	}
	if !strings.Contains(out, "https://auth.openai.com/codex/device") {
		t.Errorf("stdout = %q, want the verification URL", out)
	}
	if !strings.Contains(out, "Waiting for authorization") {
		t.Errorf("stdout = %q, want the wait notice", out)
	}
}

// A server that does not route /deviceauth is the documented failure here, and
// the caller needs an error rather than a nil session it would dereference.
func TestRunCodexDeviceFlow_StartFailureIsWrapped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	var result *auth.Result
	var err error
	_ = captureStdout(t, func() {
		result, err = runCodexDeviceFlow(context.Background(), codexDeviceProvider(srv.URL))
	})

	if err == nil {
		t.Fatal("runCodexDeviceFlow() = nil error, want the start failure reported")
	}
	if result != nil {
		t.Errorf("result = %+v, want nil alongside the error", result)
	}
	if !strings.Contains(err.Error(), "codex device auth") {
		t.Errorf("error = %q, want it to name the flow", err)
	}
}

// A provider that does not advertise codex device auth must be refused up
// front rather than posting to an endpoint that does not exist.
func TestRunCodexDeviceFlow_RejectsUnsupportedProvider(t *testing.T) {
	var err error
	_ = captureStdout(t, func() {
		_, err = runCodexDeviceFlow(context.Background(), auth.Provider{Name: "nope"})
	})
	if err == nil {
		t.Fatal("runCodexDeviceFlow() = nil error, want a refusal")
	}
}

// A polling failure travels in Result.Err, not as a returned error — saveResult
// is what turns it into the user-facing message.
func TestRunCodexDeviceFlow_PollFailureTravelsInResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/deviceauth/usercode" {
			fmt.Fprint(w, `{"device_auth_id":"dev-1","user_code":"ABCD-9876","interval":"1"}`)
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":"device_code_expired"}`)
	}))
	defer srv.Close()

	t.Setenv("PATH", t.TempDir())
	t.Setenv("BROWSER", "")

	var result *auth.Result
	var err error
	_ = captureStdout(t, func() {
		result, err = runCodexDeviceFlow(context.Background(), codexDeviceProvider(srv.URL))
	})
	if err != nil {
		t.Fatalf("runCodexDeviceFlow() error = %v, want the failure in Result.Err", err)
	}
	if result.Err == nil {
		t.Fatal("Result.Err = nil, want the expired device code reported")
	}
	if !strings.Contains(result.Err.Error(), "device_code_expired") {
		t.Errorf("Result.Err = %v, want it to name the server's reason", result.Err)
	}
}

// When nothing in the environment can open a URL, login must print it and
// carry on: the callback server is already listening, so the flow is one paste
// away from working and failing it would be gratuitous.
func TestOpenBrowserDefault_PrintsURLWhenNoHandlerExists(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("BROWSER", "")

	var err error
	out := captureStdout(t, func() {
		err = openBrowserDefault("https://auth.openai.com/authorize?client_id=test")
	})

	if err != nil {
		t.Errorf("openBrowserDefault() = %v, want nil so the login can continue", err)
	}
	if !strings.Contains(out, "https://auth.openai.com/authorize?client_id=test") {
		t.Errorf("stdout = %q, want the URL printed for the user to open by hand", out)
	}
}

// With a handler available the URL is handed off silently; printing it too
// would be noise in the middle of an interactive login.
func TestOpenBrowserDefault_SilentWhenAHandlerExists(t *testing.T) {
	// Re-execute this binary in stub mode: it accepts any arguments and exits
	// 0, standing in for a browser launcher without opening anything.
	t.Setenv(testBrowserStubEnv, "1")
	t.Setenv("BROWSER", os.Args[0])

	var err error
	out := captureStdout(t, func() {
		err = openBrowserDefault("https://example.invalid/")
	})

	if err != nil {
		t.Errorf("openBrowserDefault() = %v, want nil", err)
	}
	if strings.Contains(out, "Open this URL") {
		t.Errorf("stdout = %q, want no fallback message when a handler ran", out)
	}
}
