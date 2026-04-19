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
	phase      string // "waiting" (manual key), "sso" (browser SSO), "device" (device code), "manual-code" (browser + paste callback)
	provider   string // selected provider
	manualCode *auth.ManualCodeSession
}

// loginSSOResultMsg is sent when the SSO flow completes asynchronously.
type loginSSOResultMsg struct {
	result *auth.Result
}

// handleLoginCommand initiates the /login flow.
// Usage: /login [provider]
// Providers: codex
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
		m.logLogin("unknown provider requested: %q", provName)
		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role:    "assistant",
			content: fmt.Sprintf("Unknown provider: `%s`. Available: codex", provName),
		})
		return m, nil
	}

	m.logLogin("/login %s requested", authProv.Name)
	return m.loginStart(authProv)
}

// logLogin writes a structured entry to the session log if a logger is configured.
// Login events are prefixed with "login:" so they are easy to grep in session logs.
func (m *model) logLogin(format string, args ...any) {
	if m == nil || m.cfg.Logger == nil {
		return
	}
	m.cfg.Logger.Info("login: " + fmt.Sprintf(format, args...))
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

	m.chatModel.Messages = append(m.chatModel.Messages, message{
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
			m.logLogin("tls-preflight failed for %s kind=%s msg=%q", prov.Name, result.Kind, result.Message)
			m.chatModel.Messages = append(m.chatModel.Messages, message{
				role:    "assistant",
				content: auth.FormatTLSPreflightFix(result),
			})
			return m, nil
		}
		m.logLogin("tls-preflight ok for %s", prov.Name)
	}

	// Auto-select: device flow > manual-code > PKCE > manual.
	switch {
	case prov.UseDeviceFlow && prov.DeviceURL != "":
		m.logLogin("selected device flow for %s", prov.Name)
		return m.loginStartDeviceFlow(prov)
	case prov.ManualCode && prov.AuthURL != "" && prov.TokenURL != "":
		m.logLogin("selected manual-code flow for %s", prov.Name)
		return m.loginStartManualCode(prov)
	case prov.AuthURL != "" && prov.TokenURL != "":
		m.logLogin("selected pkce flow for %s codex=%v", prov.Name, prov.CodexOAuth)
		return m.loginStartPKCEFlow(prov)
	default:
		m.logLogin("selected manual-key entry for %s", prov.Name)
		return m.loginStartManual(prov)
	}
}

// loginStartManualCode runs the PKCE flow for providers (Anthropic) where the
// user pastes the final callback URL or authorization code into the prompt.
func (m *model) loginStartManualCode(prov auth.Provider) (tea.Model, tea.Cmd) {
	sess, err := auth.StartManualCodeFlow(prov)
	if err != nil {
		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role:    "assistant",
			content: fmt.Sprintf("Login error for %s: %v", prov.Name, err),
		})
		return m, nil
	}

	_ = openBrowser(sess.AuthURL)

	m.login = &loginState{
		phase:      "manual-code",
		provider:   prov.Name,
		manualCode: sess,
	}

	m.chatModel.Messages = append(m.chatModel.Messages, message{
		role: "assistant",
		content: fmt.Sprintf(
			"**%s Login**\n\n"+
				"1. Approve access in your browser (opened automatically).\n"+
				"2. Paste the final redirect URL here and press **Enter**.\n"+
				"   If you only have the authorization code, paste that instead.\n\n"+
				"If the browser did not open, visit:\n%s\n\n"+
				"Press **Esc** to cancel.",
			prov.Name, sess.AuthURL),
	})

	return m, nil
}

// loginStartManual opens the provider key page and waits for manual key entry.
func (m *model) loginStartManual(prov auth.Provider) (tea.Model, tea.Cmd) {
	_ = openBrowser(prov.KeyPageURL)

	m.login = &loginState{
		phase:    "waiting",
		provider: prov.Name,
	}

	m.chatModel.Messages = append(m.chatModel.Messages, message{
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

	title := fmt.Sprintf("Starting **%s** login...", prov.Name)
	browserLine := "A browser window will open for authentication."
	if prov.CodexOAuth {
		title = "Starting **codex OAuth** login..."
		browserLine = "A browser window will open for Codex OAuth."
	}
	m.chatModel.Messages = append(m.chatModel.Messages, message{
		role: "assistant",
		content: fmt.Sprintf(
			"%s\n\n"+
				"%s\n"+
				"Press **Esc** to cancel.",
			title, browserLine),
	})

	// Run PKCE flow in background.
	log := m.cfg.Logger
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()

		if log != nil {
			log.Info(fmt.Sprintf("login: pkce flow starting provider=%s client_id=%s auth_url=%s", prov.Name, prov.ClientID, prov.AuthURL))
		}
		result, err := auth.PKCEFlow(ctx, prov, openBrowser)
		if err != nil {
			if log != nil {
				log.Errorf("login: pkce flow aborted provider=%s err=%v", prov.Name, err)
			}
			result = &auth.Result{Provider: prov.Name, Err: err}
		} else if log != nil {
			if result.Err != nil {
				log.Errorf("login: pkce callback/exchange failed provider=%s err=%v", prov.Name, result.Err)
			} else {
				log.Info(fmt.Sprintf("login: pkce callback/exchange ok provider=%s key_present=%v", prov.Name, result.APIKey != ""))
			}
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
		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role:    "assistant",
			content: fmt.Sprintf("Login error for %s: %v", prov.Name, err),
		})
		return m, nil
	}

	// Open browser to verification URI.
	_ = openBrowser(dcr.VerificationURI)

	m.chatModel.Messages = append(m.chatModel.Messages, message{
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
	log := m.cfg.Logger
	if log != nil {
		log.Info(fmt.Sprintf("login: device flow prompt provider=%s verification_uri=%s interval=%ds", prov.Name, dcr.VerificationURI, interval))
	}
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()

		result, err := auth.PollDeviceToken(ctx, prov, deviceCode, interval)
		if err != nil {
			if log != nil {
				log.Errorf("login: device flow poll aborted provider=%s err=%v", prov.Name, err)
			}
			result = &auth.Result{Provider: prov.Name, Err: err}
		} else if log != nil {
			if result.Err != nil {
				log.Errorf("login: device flow poll failed provider=%s err=%v", prov.Name, result.Err)
			} else {
				log.Info(fmt.Sprintf("login: device flow ok provider=%s key_present=%v", prov.Name, result.APIKey != ""))
			}
		}
		return loginSSOResultMsg{result: result}
	}
}

// handleLoginSSOResult processes the async SSO result.
func (m *model) handleLoginSSOResult(msg loginSSOResultMsg) (tea.Model, tea.Cmd) {
	// If login was canceled while SSO was running, ignore the result.
	if m.login == nil {
		m.logLogin("sso result ignored: login was canceled")
		return m, nil
	}
	m.login = nil

	r := msg.result
	if r.Err != nil {
		m.logLogin("result: failure provider=%s err=%v", r.Provider, r.Err)
		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role:    "assistant",
			content: fmt.Sprintf("Login failed: %v", r.Err),
		})
		return m, nil
	}

	if r.APIKey == "" {
		m.logLogin("result: empty key provider=%s env=%s", r.Provider, r.EnvVar)
		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role:    "assistant",
			content: fmt.Sprintf("Login returned empty key for %s.", r.Provider),
		})
		return m, nil
	}

	// Save the key.
	if err := auth.SaveKey(r.EnvVar, r.APIKey); err != nil {
		m.logLogin("result: save key failed provider=%s env=%s err=%v", r.Provider, r.EnvVar, err)
		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role:    "assistant",
			content: fmt.Sprintf("Error saving key: %v", err),
		})
		return m, nil
	}

	masked := maskKey(r.APIKey)
	m.logLogin("result: success provider=%s env=%s key=%s", r.Provider, r.EnvVar, masked)
	m.chatModel.Messages = append(m.chatModel.Messages, message{
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
		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role:    "assistant",
			content: "Internal error: unknown provider.",
		})
		return m, nil
	}

	if err := auth.SaveKey(prov.EnvVar, apiKey); err != nil {
		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role:    "assistant",
			content: fmt.Sprintf("Error saving key: %v", err),
		})
		return m, nil
	}

	masked := maskKey(apiKey)
	m.chatModel.Messages = append(m.chatModel.Messages, message{
		role: "assistant",
		content: fmt.Sprintf(
			"Saved **%s** key `%s` to `~/.pi-go/.env`.\n\nThe key is active for this session.",
			provName, masked),
	})
	return m, nil
}

// handleLoginCodeSubmit exchanges a pasted manual-code for an API key.
func (m *model) handleLoginCodeSubmit(pasted string) (tea.Model, tea.Cmd) {
	sess := m.login.manualCode
	provName := m.login.provider
	if sess == nil {
		m.login = nil
		m.logLogin("manual-code submit failed: no session")
		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role:    "assistant",
			content: "Internal error: no manual-code session.",
		})
		return m, nil
	}

	m.logLogin("manual-code submit provider=%s pasted_len=%d", provName, len(pasted))
	m.chatModel.Messages = append(m.chatModel.Messages, message{
		role:    "assistant",
		content: fmt.Sprintf("Exchanging code for **%s** API key...", provName),
	})

	log := m.cfg.Logger
	return m, func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		result, err := auth.CompleteManualCodeFlow(ctx, sess, pasted)
		if err != nil {
			if log != nil {
				log.Errorf("login: manual-code exchange aborted provider=%s err=%v", provName, err)
			}
			result = &auth.Result{Provider: provName, Err: err}
		} else if log != nil {
			if result.Err != nil {
				log.Errorf("login: manual-code exchange failed provider=%s err=%v", provName, result.Err)
			} else {
				log.Info(fmt.Sprintf("login: manual-code exchange ok provider=%s key_present=%v", provName, result.APIKey != ""))
			}
		}
		return loginSSOResultMsg{result: result}
	}
}

// handleLoginCancel cancels the login flow.
func (m *model) handleLoginCancel() (tea.Model, tea.Cmd) {
	m.login = nil
	m.chatModel.Messages = append(m.chatModel.Messages, message{
		role:    "assistant",
		content: "Login canceled.",
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
