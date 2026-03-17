package tui

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/dimetron/pi-go/internal/auth"
	"github.com/dimetron/pi-go/internal/config"
)

// loginState tracks the /login interactive flow.
type loginState struct {
	phase    string // "waiting" (manual key), "sso" (browser SSO), "device" (device code)
	provider string // selected provider
}

// loginSSOResultMsg is sent when the SSO flow completes asynchronously.
type loginSSOResultMsg struct {
	result *auth.Result
}

// handleLoginCommand initiates the /login flow.
// Usage: /login [provider]
// Providers: codex, openai, anthropic, gemini
// Auto-selects the best auth flow (device code, PKCE, or manual key entry).
func (m *model) handleLoginCommand(args []string) (tea.Model, tea.Cmd) {
	var provName string
	for _, arg := range args {
		if !strings.HasPrefix(arg, "-") {
			provName = strings.ToLower(arg)
		}
	}

	// No args: show status.
	if provName == "" {
		return m.loginShowStatus()
	}

	// Find provider.
	authProv, ok := auth.FindProvider(provName)
	if !ok {
		m.messages = append(m.messages, message{
			role:    "assistant",
			content: fmt.Sprintf("Unknown provider: `%s`. Available: codex, openai, anthropic, gemini", provName),
		})
		return m, nil
	}

	return m.loginStart(authProv)
}

// loginShowStatus displays current API key status for all providers.
func (m *model) loginShowStatus() (tea.Model, tea.Cmd) {
	keys := config.APIKeys()
	var sb strings.Builder
	sb.WriteString("**API Key Status:**\n\n")

	for _, p := range auth.Providers() {
		status := "not set"
		if _, ok := keys[p.Name]; ok {
			status = "configured"
		}
		fmt.Fprintf(&sb, "- **%s** — %s\n", p.Name, status)
	}

	sb.WriteString("\n**Usage:** `/login <provider>`\n")
	sb.WriteString("Example: `/login codex`")

	m.messages = append(m.messages, message{
		role:    "assistant",
		content: sb.String(),
	})
	return m, nil
}

// loginStart auto-selects the best auth flow for a provider.
func (m *model) loginStart(prov auth.Provider) (tea.Model, tea.Cmd) {
	// TLS preflight for providers that need it (codex/openai).
	if prov.TLSPreflight {
		result := auth.RunTLSPreflight(4000)
		if !result.OK && result.Kind == "tls-cert" {
			m.messages = append(m.messages, message{
				role:    "assistant",
				content: auth.FormatTLSPreflightFix(result),
			})
			return m, nil
		}
	}

	// Auto-select: device flow > PKCE > manual.
	if prov.UseDeviceFlow && prov.DeviceURL != "" {
		return m.loginStartDeviceFlow(prov)
	}
	if prov.AuthURL != "" && prov.TokenURL != "" {
		return m.loginStartPKCEFlow(prov)
	}
	return m.loginStartManual(prov)
}

// loginStartManual opens the provider key page and waits for manual key entry.
func (m *model) loginStartManual(prov auth.Provider) (tea.Model, tea.Cmd) {
	_ = openBrowser(prov.KeyPageURL)

	m.login = &loginState{
		phase:    "waiting",
		provider: prov.Name,
	}

	m.messages = append(m.messages, message{
		role: "assistant",
		content: fmt.Sprintf(
			"Opening **%s** API key page in your browser...\n\n"+
				"Paste your API key and press **Enter** to save, or **Esc** to cancel.\n\n"+
				"The key will be saved to `~/.pi-go/.env` as `%s`.",
			prov.Name, prov.EnvVar),
	})

	return m, nil
}

// loginStartPKCEFlow runs OAuth PKCE flow with local callback server.
func (m *model) loginStartPKCEFlow(prov auth.Provider) (tea.Model, tea.Cmd) {
	m.login = &loginState{
		phase:    "sso",
		provider: prov.Name,
	}

	m.messages = append(m.messages, message{
		role: "assistant",
		content: fmt.Sprintf(
			"Starting **%s** login...\n\n"+
				"A browser window will open for authentication.\n"+
				"Press **Esc** to cancel.",
			prov.Name),
	})

	// Run PKCE flow in background.
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()

		result, err := auth.PKCEFlow(ctx, prov, openBrowser)
		if err != nil {
			result = &auth.Result{Provider: prov.Name, Err: err}
		}
		return loginSSOResultMsg{result: result}
	}
}

// loginStartDeviceFlow runs the OAuth device code flow.
func (m *model) loginStartDeviceFlow(prov auth.Provider) (tea.Model, tea.Cmd) {
	m.login = &loginState{
		phase:    "device",
		provider: prov.Name,
	}

	// Request device code synchronously (fast HTTP call), then poll async.
	dcr, err := auth.DeviceFlow(context.Background(), prov)
	if err != nil {
		m.login = nil
		m.messages = append(m.messages, message{
			role:    "assistant",
			content: fmt.Sprintf("Login error for %s: %v", prov.Name, err),
		})
		return m, nil
	}

	// Open browser to verification URI.
	_ = openBrowser(dcr.VerificationURI)

	m.messages = append(m.messages, message{
		role: "assistant",
		content: fmt.Sprintf(
			"**%s Device Login**\n\n"+
				"1. Open: %s\n"+
				"2. Enter code: **`%s`**\n"+
				"3. Approve access in your browser\n\n"+
				"Waiting for authorization... Press **Esc** to cancel.",
			prov.Name, dcr.VerificationURI, dcr.UserCode),
	})

	// Poll for token in background.
	deviceCode := dcr.DeviceCode
	interval := dcr.Interval
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()

		result, err := auth.PollDeviceToken(ctx, prov, deviceCode, interval)
		if err != nil {
			result = &auth.Result{Provider: prov.Name, Err: err}
		}
		return loginSSOResultMsg{result: result}
	}
}

// handleLoginSSOResult processes the async SSO result.
func (m *model) handleLoginSSOResult(msg loginSSOResultMsg) (tea.Model, tea.Cmd) {
	// If login was cancelled while SSO was running, ignore the result.
	if m.login == nil {
		return m, nil
	}
	m.login = nil

	r := msg.result
	if r.Err != nil {
		m.messages = append(m.messages, message{
			role:    "assistant",
			content: fmt.Sprintf("Login failed: %v", r.Err),
		})
		return m, nil
	}

	if r.APIKey == "" {
		m.messages = append(m.messages, message{
			role:    "assistant",
			content: fmt.Sprintf("Login returned empty key for %s.", r.Provider),
		})
		return m, nil
	}

	// Save the key.
	if err := auth.SaveKey(r.EnvVar, r.APIKey); err != nil {
		m.messages = append(m.messages, message{
			role:    "assistant",
			content: fmt.Sprintf("Error saving key: %v", err),
		})
		return m, nil
	}

	masked := maskKey(r.APIKey)
	m.messages = append(m.messages, message{
		role: "assistant",
		content: fmt.Sprintf(
			"Login successful! Saved **%s** key `%s` to `~/.pi-go/.env`.\n\n"+
				"The key is active for this session.",
			r.Provider, masked),
	})
	return m, nil
}

// handleLoginSave saves a manually entered API key to ~/.pi-go/.env.
func (m *model) handleLoginSave(apiKey string) (tea.Model, tea.Cmd) {
	provName := m.login.provider
	m.login = nil

	prov, ok := auth.FindProvider(provName)
	if !ok {
		m.messages = append(m.messages, message{
			role:    "assistant",
			content: "Internal error: unknown provider.",
		})
		return m, nil
	}

	if err := auth.SaveKey(prov.EnvVar, apiKey); err != nil {
		m.messages = append(m.messages, message{
			role:    "assistant",
			content: fmt.Sprintf("Error saving key: %v", err),
		})
		return m, nil
	}

	masked := maskKey(apiKey)
	m.messages = append(m.messages, message{
		role: "assistant",
		content: fmt.Sprintf(
			"Saved **%s** key `%s` to `~/.pi-go/.env`.\n\nThe key is active for this session.",
			provName, masked),
	})
	return m, nil
}

// handleLoginCancel cancels the login flow.
func (m *model) handleLoginCancel() (tea.Model, tea.Cmd) {
	m.login = nil
	m.messages = append(m.messages, message{
		role:    "assistant",
		content: "Login cancelled.",
	})
	return m, nil
}

// maskKey masks an API key for display, showing first 4 and last 4 chars.
func maskKey(key string) string {
	if len(key) <= 8 {
		return "****"
	}
	return key[:4] + "..." + key[len(key)-4:]
}

// openBrowser opens a URL in the default browser.
// It is a var so tests can replace it with a mock.
var openBrowser = openBrowserDefault

func openBrowserDefault(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", url)
	default:
		return fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}
	return cmd.Start()
}
