package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/provider"
)

func newModelCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "model",
		Short: "Manage LLM models",
		Long:  `Commands for listing and inspecting available LLM models from configured providers.`,
	}

	cmd.AddCommand(newModelListCmd())
	return cmd
}

func newModelListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list [provider]",
		Short: "List available models from a provider's API",
		Long: `Lists available model IDs by calling the provider's model listing API.

If a provider is specified as an argument, only that provider is queried.
If no provider is given, all configured providers (those with an API key
or base URL set) are queried in turn.

Providers: anthropic, openai, gemini, mistral, xai, ollama, openrouter

Examples:
  pi model list                 # list models for all configured providers
  pi model list anthropic       # list models from Anthropic
  pi model list openai          # list models from OpenAI
  pi model list gemini          # list models from Gemini
  pi model list mistral         # list models from Mistral
  pi model list xai             # list models from xAI
  pi model list ollama          # list locally installed Ollama models
  pi model list openrouter      # list models from OpenRouter`,
		Args: cobra.MaximumNArgs(1),
		RunE: runModelList,
	}

	cmd.Flags().StringVar(&flagURL, "url", "", "Alternative base URL for the provider API endpoint")
	cmd.Flags().BoolVar(&flagInsecure, "insecure", false, "Skip TLS certificate verification")
	return cmd
}

// allProviders is the fixed list of providers supporting model listing.
var allProviders = []string{"anthropic", "openai", "gemini", "mistral", "xai", "ollama", "openrouter"}

func runModelList(cmd *cobra.Command, args []string) error {
	loadDotEnv()

	keys := config.APIKeys()
	// A broken or absent config must not stop model listing — fall back to
	// env-only base URLs, which is what this command did before baseURLs existed.
	cfg, err := config.Load()
	if err != nil {
		cfg = config.Defaults()
	}
	baseURLs := cfg.ResolveBaseURLs()

	// Determine which providers to query.
	var providers []string
	if len(args) == 1 {
		p := strings.ToLower(args[0])
		switch p {
		case "anthropic", "openai", "gemini", "mistral", "xai", "ollama", "openrouter":
			providers = []string{p}
		case "azure":
			// Not a live query: enumerating deployments needs ARM credentials
			// and the resource ID, not the inference API key, so there is
			// nothing to call. This used to list OpenAI's catalog instead,
			// which showed models the subscription does not serve, omitted the
			// deployments it does, and failed outright without OPENAI_API_KEY.
			printAzureDeployments(cmd.OutOrStdout())
			return nil
		default:
			return fmt.Errorf("unknown provider %q; valid: anthropic, openai, azure, gemini, mistral, xai, ollama, openrouter", args[0])
		}
	} else {
		// Query all providers that have credentials or a base URL configured.
		for _, p := range allProviders {
			if p == "ollama" {
				providers = append(providers, p)
				continue
			}
			if keys[p] != "" || baseURLs[p] != "" || flagURL != "" {
				providers = append(providers, p)
			}
		}
		// Azure is not in allProviders because it has nothing to query, but a
		// configured Azure user asking for "every provider" should still see
		// their deployments rather than have them silently omitted.
		azureConfigured := keys["azure"] != "" || os.Getenv("AZURE_OPENAI_ENDPOINT") != ""
		if azureConfigured {
			printAzureDeployments(cmd.OutOrStdout())
		}
		if len(providers) == 0 {
			if azureConfigured {
				return nil
			}
			return fmt.Errorf("no providers configured; set an API key (e.g. OPENAI_API_KEY) or specify a provider: pi model list <provider>")
		}
	}

	exitCode := 0
	for _, p := range providers {
		opts := provider.ListModelsOptions{
			APIKey:   keys[p],
			Insecure: flagInsecure,
		}
		// Resolve base URL: --url flag > env var > default.
		baseURL := flagURL
		if baseURL == "" {
			baseURL = baseURLs[p]
		}
		if baseURL == "" && p == "ollama" {
			baseURL = "http://localhost:11434"
		}
		opts.BaseURL = baseURL

		ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
		models, err := provider.ListModels(ctx, p, opts)
		cancel()

		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", p, err)
			exitCode = 1
			continue
		}

		// Sort model IDs alphabetically.
		sort.Slice(models, func(i, j int) bool {
			return models[i].ID < models[j].ID
		})

		fmt.Printf("%s (%d models):\n", p, len(models))
		for _, m := range models {
			if m.OwnedBy != "" {
				fmt.Printf("  %-45s  %s\n", m.ID, m.OwnedBy)
			} else {
				fmt.Printf("  %s\n", m.ID)
			}
		}
		fmt.Println()
	}

	if exitCode != 0 {
		return fmt.Errorf("one or more providers failed")
	}
	return nil
}

// printAzureDeployments renders the embedded Azure deployment catalog.
//
// The context window is shown because it is the part that actually differs:
// a deployment is named after the model it serves, but is provisioned with its
// own limit, and most of these disagree with the same model ID on OpenAI's API.
// That number drives auto-compaction, so it is worth being able to read it back.
func printAzureDeployments(w io.Writer) {
	deployments := provider.AzureDeployments()
	fmt.Fprintf(w, "azure (%d deployments, from the embedded catalog):\n", len(deployments))
	for _, d := range deployments {
		fmt.Fprintf(w, "  %-45s  %s context\n", d.Name, humanTokens(d.ContextWindow))
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  Deployment names are chosen per subscription; use --model azure/<deployment>.")
	fmt.Fprintln(w, "  An uncataloged name still works — its window falls back to the OpenAI entry")
	fmt.Fprintln(w, "  it is named after, or override it with \"contextWindow\" in config.json.")
	fmt.Fprintln(w)
}

// humanTokens renders a token count as a compact K/M figure.
func humanTokens(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.2fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%dK", n/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}
