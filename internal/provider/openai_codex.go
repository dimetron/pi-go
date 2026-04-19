package provider

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/dimetron/pi-go/internal/auth"
)

// codexBackendBaseURL is the ChatGPT backend that accepts codex OAuth
// access tokens. Platform endpoints like https://api.openai.com/v1/responses
// reject those tokens with "Missing scopes: api.responses.write" because the
// token isn't an sk-… API key. The ChatGPT backend routes the same request
// shape under /codex/responses instead. Matches pi-mono's
// openai-codex-responses provider (DEFAULT_CODEX_BASE_URL).
const codexBackendBaseURL = "https://chatgpt.com/backend-api/codex"

// codexBackendSupportedModels lists the model IDs the ChatGPT backend
// accepts when authenticated with a codex OAuth token. The backend
// explicitly 400s unsupported names with
// "The '<id>' model is not supported when using Codex with a ChatGPT
// account." Matches pi-mono's openai-codex-responses registry.
var codexBackendSupportedModels = []string{
	"gpt-5.1", "gpt-5.1-codex-max", "gpt-5.1-codex-mini",
	"gpt-5.2", "gpt-5.2-codex",
	"gpt-5.3-codex", "gpt-5.3-codex-spark",
	"gpt-5.4", "gpt-5.4-mini",
}

// isCodexBackendSupported reports whether modelName is accepted by the
// ChatGPT codex backend. Match is exact (case-insensitive) — the backend
// rejects prefix variants.
func isCodexBackendSupported(modelName string) bool {
	lower := strings.ToLower(modelName)
	for _, m := range codexBackendSupportedModels {
		if lower == m {
			return true
		}
	}
	return false
}

// extractChatGPTAccountID pulls the chatgpt_account_id claim from a codex
// OAuth JWT. Returns "" if the token isn't a JWT or the claim is absent.
func extractChatGPTAccountID(token string) string {
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) != 3 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		// Tolerate JWTs with padding characters.
		payload, err = base64.URLEncoding.DecodeString(parts[1])
		if err != nil {
			return ""
		}
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(payload, &raw); err != nil {
		return ""
	}
	authBlob, ok := raw["https://api.openai.com/auth"]
	if !ok {
		return ""
	}
	var claims struct {
		ChatGPTAccountID string `json:"chatgpt_account_id"`
	}
	if err := json.Unmarshal(authBlob, &claims); err != nil {
		return ""
	}
	return claims.ChatGPTAccountID
}

// errorBodyLoggingTransport wraps an http.RoundTripper and, on >=400
// responses, reads the body, logs it via auth.SetDebugLogger (which the
// TUI wires to the session logger), and replaces the response body with
// a buffer so the SDK can still consume it. Used only for the codex
// backend — the platform API's error channel is already useful.
type errorBodyLoggingTransport struct{ base http.RoundTripper }

func (t *errorBodyLoggingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if err != nil || resp == nil || resp.StatusCode < 400 || resp.Body == nil {
		return resp, err
	}
	body, readErr := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if readErr != nil {
		return resp, err
	}
	auth.Debug(fmt.Sprintf("codex backend %d on %s %s: %s",
		resp.StatusCode, req.Method, req.URL.Path, truncate(string(body), 1024)))
	resp.Body = io.NopCloser(bytes.NewReader(body))
	return resp, err
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
