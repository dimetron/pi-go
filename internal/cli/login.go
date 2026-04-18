package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dimetron/pi-go/internal/auth"
	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/provider"
)

var flagLoginModel string

func newLoginCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "login [provider]",
		Short: "Authenticate with an LLM provider",
		Long: `Authenticate with an LLM provider and save the credentials.

Supported providers:
  openai       OpenAI (api.openai.com) — device code flow
  codex        ChatGPT (chatgpt.com) — browser OAuth PKCE
  gemini       Google AI (ai.google.dev) — browser OAuth PKCE

Examples:
  pi login openai                       # Authenticate with OpenAI
  pi login                              # Interactive provider selection`,
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
		return fmt.Errorf("unknown provider %q — supported: codex", providerName)
	}

	fmt.Printf("Logging in to %s...\n\n", prov.Name)

	ctx := context.Background()

	// Run the appropriate flow.
	var result *auth.Result
	var err error
	switch {
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

func runDeviceFlow(ctx context.Context, prov auth.Provider) (*auth.Result, error) {
	sess, err := auth.DeviceFlow(ctx, prov)
	if err != nil {
		return nil, fmt.Errorf("device flow: %w", err)
	}

	fmt.Printf("Visit: %s\n", sess.VerificationURI)
	fmt.Printf("Code:  %s\n\n", sess.UserCode)
	fmt.Println("Waiting for authorization...")

	result, err := auth.PollDeviceToken(ctx, prov, sess.DeviceCode, sess.Interval)
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

func openBrowser(url string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url).Run()
	case "linux":
		if err := exec.Command("xdg-open", url).Run(); err != nil {
			return exec.Command("open", url).Run()
		}
		return nil
	case "windows":
		return exec.Command("cmd", "/c", "start", "", url).Run()
	default:
		fmt.Printf("Open this URL in your browser: %s\n", url)
		return nil
	}
}
