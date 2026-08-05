package auth

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// OpenAI's device authorization is not RFC 8628, so DeviceFlow/PollDeviceToken
// cannot drive it. The differences that matter:
//
//   - requests and responses are JSON, not form-encoded;
//   - the handle is device_auth_id, not device_code, and there is no
//     grant_type=urn:ietf:params:oauth:grant-type:device_code;
//   - "not approved yet" is signaled by HTTP 403/404 rather than an
//     authorization_pending error body;
//   - approval yields an authorization code plus the PKCE verifier the server
//     generated, which then goes through the ordinary token endpoint.
//
// The shape is taken from the codex CLI (codex-rs/login/src/device_code_auth.rs).
//
// Worth the separate path because it needs no local callback listener: PKCE
// requires the OAuth redirect to reach localhost inside whatever machine pi is
// running on, which is exactly what fails in a dev container, a Codespace, or
// over a plain SSH session.

// codexDeviceMaxWait caps a single device authorization, matching the codex
// CLI. The server expires the code on its own schedule; this only stops pi
// polling forever if the user walks away.
const codexDeviceMaxWait = 15 * time.Minute

// codexDeviceDefaultInterval is used when the server omits an interval, so a
// missing field cannot turn the poll loop into a hot spin.
const codexDeviceDefaultInterval = 5 * time.Second

// CodexDeviceSession is an in-progress codex device authorization. The caller
// displays VerificationURL and UserCode, then hands the session back to
// CompleteCodexDeviceFlow.
type CodexDeviceSession struct {
	VerificationURL string
	UserCode        string

	deviceAuthID string
	interval     time.Duration
}

// codexUserCodeResponse is the reply from the usercode endpoint.
type codexUserCodeResponse struct {
	DeviceAuthID string        `json:"device_auth_id"`
	UserCode     string        `json:"user_code"`
	UserCodeAlt  string        `json:"usercode"`
	Interval     codexInterval `json:"interval"`
}

// codexApprovalResponse is the reply from the token endpoint once the user has
// approved. The code_challenge is also returned but is not needed: only the
// verifier travels to the token endpoint.
type codexApprovalResponse struct {
	AuthorizationCode string `json:"authorization_code"`
	CodeVerifier      string `json:"code_verifier"`
}

// codexInterval accepts the poll interval as a JSON string ("5", which is what
// auth.openai.com sends and the only form the codex CLI parses) or as a plain
// number, so a server-side change to the more obvious encoding does not break
// login.
type codexInterval int

// UnmarshalJSON implements json.Unmarshaler.
func (c *codexInterval) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(strings.Trim(string(b), `"`))
	if s == "" || s == "null" {
		return nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return fmt.Errorf("device auth interval %q: %w", s, err)
	}
	*c = codexInterval(n)
	return nil
}

// StartCodexDeviceFlow requests a user code and returns the session to display.
// It performs no polling, so the caller can render the code before blocking.
func StartCodexDeviceFlow(ctx context.Context, prov Provider) (*CodexDeviceSession, error) {
	if !prov.CodexDeviceAuth || prov.DeviceURL == "" {
		return nil, fmt.Errorf("provider %s does not support codex device auth", prov.Name)
	}

	status, body, err := postJSON(ctx, prov.DeviceURL+"/usercode", map[string]string{
		"client_id": prov.ClientID,
	})
	if err != nil {
		return nil, fmt.Errorf("requesting device user code: %w", err)
	}
	if status == http.StatusNotFound {
		// Upstream calls this out specifically: a self-hosted or older auth
		// server simply does not route /deviceauth.
		return nil, fmt.Errorf("device auth is not enabled for this Codex server — use browser login instead")
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("device user code request failed (%d): %s", status, sanitizeErrorBody(body))
	}

	var ucr codexUserCodeResponse
	if err := json.Unmarshal(body, &ucr); err != nil {
		return nil, fmt.Errorf("parsing device user code response: %w", err)
	}

	userCode := cmp.Or(ucr.UserCode, ucr.UserCodeAlt)
	if ucr.DeviceAuthID == "" || userCode == "" {
		return nil, fmt.Errorf("device user code response missing device_auth_id or user_code")
	}

	interval := time.Duration(ucr.Interval) * time.Second
	if interval <= 0 {
		interval = codexDeviceDefaultInterval
	}

	logf("codex-device: user code issued interval=%s verify=%s", interval, prov.DeviceVerifyURL)

	return &CodexDeviceSession{
		VerificationURL: prov.DeviceVerifyURL,
		UserCode:        userCode,
		deviceAuthID:    ucr.DeviceAuthID,
		interval:        interval,
	}, nil
}

// CompleteCodexDeviceFlow polls until the user approves the code, then
// exchanges the resulting authorization code for a token. It blocks for up to
// codexDeviceMaxWait or until ctx is canceled.
func CompleteCodexDeviceFlow(ctx context.Context, prov Provider, sess *CodexDeviceSession) (*Result, error) {
	if sess == nil {
		return nil, fmt.Errorf("nil device session")
	}

	approval, err := pollCodexApproval(ctx, prov, sess)
	if err != nil {
		// Login failures travel in Result.Err, as in PollDeviceToken: the
		// returned error is reserved for the caller's own plumbing failing.
		return &Result{Provider: prov.Name, Err: err}, nil //nolint:nilerr // by design, see above
	}

	// The server generated the PKCE pair, so the verifier arrives with the
	// authorization code rather than being held locally as in PKCEFlow.
	tok, err := exchangeCode(ctx, prov, approval.AuthorizationCode, prov.DeviceRedirectURI, approval.CodeVerifier)
	if err != nil {
		return &Result{Provider: prov.Name, Err: fmt.Errorf("device code exchange: %w", err)}, nil
	}

	logf("codex-device: exchange ok")

	return &Result{
		Provider: prov.Name,
		APIKey:   prov.TokenToKey(tok),
		EnvVar:   prov.EnvVar,
	}, nil
}

// pollCodexApproval polls the token endpoint until the user approves.
func pollCodexApproval(ctx context.Context, prov Provider, sess *CodexDeviceSession) (*codexApprovalResponse, error) {
	deadline := time.NewTimer(codexDeviceMaxWait)
	defer deadline.Stop()

	ticker := time.NewTicker(sess.interval)
	defer ticker.Stop()

	payload := map[string]string{
		"device_auth_id": sess.deviceAuthID,
		"user_code":      sess.UserCode,
	}

	for {
		status, body, err := postJSON(ctx, prov.DeviceURL+"/token", payload)
		switch {
		case err != nil:
			return nil, fmt.Errorf("polling device authorization: %w", err)
		case status == http.StatusOK:
			var approval codexApprovalResponse
			if err := json.Unmarshal(body, &approval); err != nil {
				return nil, fmt.Errorf("parsing device authorization response: %w", err)
			}
			if approval.AuthorizationCode == "" || approval.CodeVerifier == "" {
				return nil, fmt.Errorf("device authorization response missing authorization_code or code_verifier")
			}
			return &approval, nil
		case status == http.StatusForbidden, status == http.StatusNotFound:
			// Not approved yet — the only signal the server gives.
		default:
			return nil, fmt.Errorf("device authorization failed (%d): %s", status, sanitizeErrorBody(body))
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-deadline.C:
			return nil, fmt.Errorf("device authorization timed out after %s", codexDeviceMaxWait)
		case <-ticker.C:
		}
	}
}

// postJSON posts payload as JSON and returns the status and body. The body is
// read regardless of status because the error paths report it.
func postJSON(ctx context.Context, endpoint string, payload any) (int, []byte, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return 0, nil, fmt.Errorf("encoding request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(encoded))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, fmt.Errorf("reading response: %w", err)
	}
	return resp.StatusCode, body, nil
}
