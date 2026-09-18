package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimetron/pi-go/internal/auth"
	"github.com/dimetron/pi-go/internal/browser"
	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/provider"
)

var flagLoginModel string

func newLoginCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "login [provider]",
		Short: "Authenticate with a provider",
		Long: `Authenticate with a provider and save the credentials.

Supported providers:
  codex        ChatGPT (chatgpt.com) — device auth
  xai          Grok via a SuperGrok / X Premium+ subscription — device code
               (uses your subscription instead of per-token API billing)

Examples:
  pi login codex                        # Authenticate with Codex
  pi login xai                          # Use your Grok subscription
  pi login                              # Interactive provider selection

An existing "grok" CLI login is adopted automatically when you log in to xai,
so there is no need to authenticate twice.`,
		Args: cobra.MaximumNArgs(1),
		RunE: runLogin,
	}
	cmd.Flags().StringVar(&flagLoginModel, "model", "", "Set this model as the default after login")
	return cmd
}

func runLogin(cmd *cobra.Command, args []string) error {
	// Load env vars first so SaveKey is consistent.
	loadDotEnv()

	var providerName string
	if len(args) > 0 {
		providerName = args[0]
	}

	// If no provider specified, prompt interactively.
	if providerName == "" {
		providerName = promptProvider()
		if providerName == "" {
			return nil // User canceled
		}
	}

	// Find the provider.
	prov, ok := auth.FindProvider(providerName)
	if !ok {
		names := make([]string, 0, len(auth.Providers()))
		for _, p := range auth.Providers() {
			names = append(names, p.Name)
		}
		return fmt.Errorf("unknown provider %q — supported: %s", providerName, strings.Join(names, ", "))
	}

	// A user who already ran `grok login` holds a usable subscription
	// credential. Adopt it rather than making them authenticate twice; fall
	// through to the interactive flow when there is nothing to adopt.
	if prov.Name == "xai" {
		if adopted, err := auth.ImportGrokCLITokens(); err != nil {
			fmt.Printf("Note: could not read grok CLI credentials: %v\n", err)
		} else if adopted != nil {
			return saveAdoptedXAI(adopted)
		}
	}

	fmt.Printf("Logging in to %s...\n\n", prov.Name)

	ctx := context.Background()

	// Run the appropriate flow.
	var result *auth.Result
	var err error
	switch {
	case prov.CodexDeviceAuth:
		result, err = runCodexDeviceFlow(ctx, prov)
	case prov.UseDeviceFlow:
		result, err = runDeviceFlow(ctx, prov)
	case prov.ManualCode:
		result, err = runManualCodeFlow(ctx, prov)
	default:
		result, err = runPKCEFlow(ctx, prov)
	}
	if err != nil {
		return err
	}

	// Save credentials.
	if err := saveResult(result); err != nil {
		return err
	}

	// Save default model if --model was provided.
	if flagLoginModel != "" {
		provName := prov.Name
		// Resolve provider from model name if needed.
		if info, err := provider.Resolve(flagLoginModel); err == nil && info.Provider != "" {
			provName = info.Provider
		}
		if err := config.SaveDefaultRole(flagLoginModel, provName); err != nil {
			return fmt.Errorf("saving default model: %w", err)
		}
		fmt.Printf("Default model set to %s\n", flagLoginModel)
	}

	return nil
}

// saveAdoptedXAI persists a subscription credential adopted from the grok CLI
// and reports the outcome. It mirrors saveResult's contract (env var plus
// ~/.pi-go/.env) so the credential is picked up on the next run.
func saveAdoptedXAI(set *auth.XAITokenSet) error {
	if err := auth.SaveXAITokens(set); err != nil {
		return fmt.Errorf("saving xAI credentials: %w", err)
	}
	if err := auth.SaveKey("XAI_API_KEY", set.AccessToken); err != nil {
		return fmt.Errorf("saving xAI key: %w", err)
	}
	_ = os.Setenv("XAI_API_KEY", set.AccessToken)
	fmt.Println("Imported your existing grok CLI session.")
	if !set.ExpiresAt.IsZero() {
		fmt.Printf("Access token valid until %s (refreshed automatically).\n", set.ExpiresAt.Format(time.RFC3339))
	}
	fmt.Println("Run a grok-* model to use your SuperGrok subscription.")
	return nil
}

// runCodexDeviceFlow drives OpenAI's device auth. Unlike the PKCE path there is
// no callback to wait on, so the browser is a convenience: the user can equally
// well open the URL on a phone, which is the point in a headless container.
func runCodexDeviceFlow(ctx context.Context, prov auth.Provider) (*auth.Result, error) {
	sess, err := auth.StartCodexDeviceFlow(ctx, prov)
	if err != nil {
		return nil, fmt.Errorf("codex device auth: %w", err)
	}

	fmt.Printf("Open this URL and enter the code:\n\n  %s\n\n  Code: %s\n\n", sess.VerificationURL, sess.UserCode)
	_ = browser.Open(sess.VerificationURL)
	fmt.Println("Waiting for authorization...")

	result, err := auth.CompleteCodexDeviceFlow(ctx, prov, sess)
	if err != nil {
		return nil, fmt.Errorf("codex device auth: %w", err)
	}
	return result, nil
}

func runDeviceFlow(ctx context.Context, prov auth.Provider) (*auth.Result, error) {
	sess, err := auth.DeviceFlow(ctx, prov)
	if err != nil {
		return nil, fmt.Errorf("device flow: %w", err)
	}

	fmt.Printf("Visit: %s\n", sess.VerificationURL())
	fmt.Printf("Code:  %s\n\n", sess.UserCode)
	fmt.Println("Waiting for authorization...")

	result, err := auth.PollDeviceToken(ctx, prov, sess)
	if err != nil {
		return nil, fmt.Errorf("polling device token: %w", err)
	}

	return result, nil
}

func runManualCodeFlow(ctx context.Context, prov auth.Provider) (*auth.Result, error) {
	sess, err := auth.StartManualCodeFlow(prov)
	if err != nil {
		return nil, fmt.Errorf("starting manual code flow: %w", err)
	}

	fmt.Printf("Visit: %s\n\n", sess.AuthURL)
	fmt.Println("Open the URL above in your browser, log in, and copy the final redirect URL.")
	fmt.Println("If you only have the authorization code, paste that instead.")
	fmt.Println()

	code := promptString("Paste the redirect URL or code")
	if code == "" {
		return nil, fmt.Errorf("no code entered")
	}

	result, err := auth.CompleteManualCodeFlow(ctx, sess, code)
	if err != nil {
		return nil, fmt.Errorf("completing manual code flow: %w", err)
	}
	if result.Err != nil {
		return nil, fmt.Errorf("login error: %w", result.Err)
	}

	return result, nil
}

func runPKCEFlow(ctx context.Context, prov auth.Provider) (*auth.Result, error) {
	fmt.Println("Opening browser for authentication...")

	result, err := auth.PKCEFlow(ctx, prov, openBrowser)
	if err != nil {
		return nil, fmt.Errorf("PKCE flow: %w", err)
	}

	return result, nil
}

func saveResult(result *auth.Result) error {
	if result.Err != nil {
		return fmt.Errorf("login error: %w", result.Err)
	}

	if err := auth.SaveKey(result.EnvVar, result.APIKey); err != nil {
		return fmt.Errorf("saving key: %w", err)
	}

	// Set in process env so current session can use it.
	_ = os.Setenv(result.EnvVar, result.APIKey)

	masked := maskKey(result.APIKey)
	fmt.Printf("\nSuccessfully logged in to %s\n", result.Provider)
	fmt.Printf("Key saved to ~/.pi-go/.env (%s)\n", masked)
	return nil
}

func promptProvider() string {
	providers := auth.Providers()
	fmt.Println("Available providers:")
	for i, p := range providers {
		fmt.Printf("  %d. %s\n", i+1, p.Name)
	}
	fmt.Println()

	n := promptInt("Select provider number")
	if n < 1 || n > len(providers) {
		return ""
	}
	return providers[n-1].Name
}

func promptString(msg string) string {
	fmt.Print(msg + ": ")
	var val string
	if _, err := fmt.Fscan(os.Stdin, &val); err != nil {
		return ""
	}
	return strings.TrimSpace(val)
}

func promptInt(msg string) int {
	fmt.Print(msg + " [number]: ")
	var val int
	if _, err := fmt.Fscan(os.Stdin, &val); err != nil {
		return 0
	}
	return val
}

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
	if err := browser.Open(url); err != nil {
		// Having no handler is the normal case in a dev container or over a
		// plain SSH session, and the callback server is already listening — so
		// print the URL and let the user finish the flow by hand rather than
		// fail a login that is one paste away from working.
		fmt.Printf("\nOpen this URL to continue:\n\n  %s\n\n", url)
	}
	return nil
}
