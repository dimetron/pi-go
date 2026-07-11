package cli

import (
	"context"
	"fmt"
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

Providers: anthropic, openai, gemini, mistral, ollama

Examples:
  pi model list                 # list models for all configured providers
  pi model list anthropic       # list models from Anthropic
  pi model list openai          # list models from OpenAI
  pi model list gemini          # list models from Gemini
  pi model list mistral         # list models from Mistral
  pi model list ollama          # list locally installed Ollama models`,
		Args: cobra.MaximumNArgs(1),
		RunE: runModelList,
	}

	cmd.Flags().StringVar(&flagURL, "url", "", "Alternative base URL for the provider API endpoint")
	cmd.Flags().BoolVar(&flagInsecure, "insecure", false, "Skip TLS certificate verification")
	return cmd
}

// allProviders is the fixed list of providers supporting model listing.
var allProviders = []string{"anthropic", "openai", "gemini", "mistral", "ollama"}

func runModelList(cmd *cobra.Command, args []string) error {
	loadDotEnv()

	keys := config.APIKeys()
	baseURLs := config.BaseURLs()

	// Determine which providers to query.
	var providers []string
	if len(args) == 1 {
		p := strings.ToLower(args[0])
		switch p {
		case "anthropic", "openai", "gemini", "mistral", "ollama":
			providers = []string{p}
		case "azure":
			providers = []string{"openai"} // Azure uses OpenAI-compatible API
		default:
			return fmt.Errorf("unknown provider %q; valid: anthropic, openai, gemini, mistral, ollama", args[0])
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
		if len(providers) == 0 {
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
