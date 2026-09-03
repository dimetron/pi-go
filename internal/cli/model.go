package cli

import (
	"context"
	"encoding/json"
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

The human table shows each model's per-million-token input/output price in
USD, from the embedded models.dev snapshot (refreshed daily at startup).
Local providers (ollama, agentgateway) and models absent from the snapshot
show no price.

Providers: anthropic, openai, gemini, mistral, xai, ollama, openrouter, agentgateway

Examples:
  pi model list                 # list models for all configured providers
  pi model list anthropic       # list models from Anthropic
  pi model list openai          # list models from OpenAI
  pi model list gemini          # list models from Gemini
  pi model list mistral         # list models from Mistral
  pi model list xai             # list models from xAI
  pi model list ollama          # list locally installed Ollama models
  pi model list openrouter      # list models from OpenRouter
  pi model list agentgateway    # list models from the local agentgateway`,
		Args: cobra.MaximumNArgs(1),
		RunE: runModelList,
	}

	cmd.Flags().StringVar(&flagURL, "url", "", "Alternative base URL for the provider API endpoint")
	cmd.Flags().BoolVar(&flagInsecure, "insecure", false, "Skip TLS certificate verification")
	cmd.Flags().StringVarP(&flagModelListOutput, "output", "o", "", "Output format: \"json\" emits one JSON document per provider (default: human table)")
	return cmd
}

// allProviders is the fixed list of providers supporting model listing.
var allProviders = []string{"anthropic", "openai", "gemini", "mistral", "xai", "ollama", "openrouter", "agentgateway"}

// flagModelListOutput is the --output flag value for `pi model list`.
var flagModelListOutput string

// modelListJSONDoc is the per-provider JSON document emitted by `-o json`.
type modelListJSONDoc struct {
	Provider  string               `json:"provider"`
	FetchedAt string               `json:"fetched_at"`
	Models    []provider.ModelInfo `json:"models"`
}

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

	providers, err := selectModelListProviders(cmd.OutOrStdout(), args, keys, baseURLs)
	if err != nil {
		return err
	}
	// An empty list with no error means the command already produced its output
	// — the Azure catalog is printed, not queried.
	if len(providers) == 0 {
		return nil
	}

	exitCode := 0
	for _, p := range providers {
		opts := provider.ListModelsOptions{
			APIKey:   keys[p],
			Insecure: flagInsecure,
			BaseURL:  modelListBaseURL(p, baseURLs),
		}

		ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
		models, err := provider.ListModels(ctx, p, opts)
		cancel()

		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", p, err)
			exitCode = 1
			continue
		}

		if flagModelListOutput == "json" {
			doc := modelListJSONDoc{
				Provider:  p,
				FetchedAt: time.Now().UTC().Format(time.RFC3339),
				Models:    models,
			}
			// Sort by ID and pretty-print so the checked-in snapshots are
			// stable and diff-friendly across fetches.
			sort.Slice(doc.Models, func(i, j int) bool {
				return doc.Models[i].ID < doc.Models[j].ID
			})
			b, err := json.MarshalIndent(doc, "", "  ")
			if err != nil {
				fmt.Fprintf(os.Stderr, "%s: encoding JSON: %v\n", p, err)
				exitCode = 1
				continue
			}
			fmt.Fprintln(cmd.OutOrStdout(), string(b))
			continue
		}

		printProviderModels(p, models)
	}

	if exitCode != 0 {
		return fmt.Errorf("one or more providers failed")
	}
	return nil
}

// selectModelListProviders decides which providers `model list` queries: the
// one named as an argument, or every configured one. An empty result with a nil
// error means there is nothing left to query because the command has already
// printed all it had to say.
func selectModelListProviders(out io.Writer, args []string, keys, baseURLs map[string]string) ([]string, error) {
	if len(args) == 1 {
		p := strings.ToLower(args[0])
		switch p {
		case "anthropic", "openai", "gemini", "mistral", "xai", "ollama", "openrouter", "agentgateway":
			return []string{p}, nil
		case "azure":
			// Not a live query: enumerating deployments needs ARM credentials
			// and the resource ID, not the inference API key, so there is
			// nothing to call. This used to list OpenAI's catalog instead,
			// which showed models the subscription does not serve, omitted the
			// deployments it does, and failed outright without OPENAI_API_KEY.
			printAzureDeployments(out)
			return nil, nil
		default:
			return nil, fmt.Errorf("unknown provider %q; valid: anthropic, openai, azure, gemini, mistral, xai, ollama, openrouter, agentgateway", args[0])
		}
	}

	// Query all providers that have credentials or a base URL configured.
	var providers []string
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
	// their deployments rather than have them silently omitted. In JSON
	// mode the human table would corrupt the JSONL stream, so it is skipped.
	azureConfigured := keys["azure"] != "" || os.Getenv("AZURE_OPENAI_ENDPOINT") != ""
	if azureConfigured && flagModelListOutput != "json" {
		printAzureDeployments(out)
	}
	if len(providers) == 0 && !azureConfigured {
		return nil, fmt.Errorf("no providers configured; set an API key (e.g. OPENAI_API_KEY) or specify a provider: pi model list <provider>")
	}
	return providers, nil
}

// modelListBaseURL resolves the endpoint to query: --url flag > env var >
// default. Only Ollama and agentgateway have a default, because only those two
// are expected to be running locally on a well-known port with no configuration.
func modelListBaseURL(providerName string, baseURLs map[string]string) string {
	baseURL := flagURL
	if baseURL == "" {
		baseURL = baseURLs[providerName]
	}
	if baseURL == "" && providerName == "ollama" {
		baseURL = "http://localhost:11434"
	}
	if baseURL == "" && providerName == "agentgateway" {
		baseURL = "http://localhost:4000"
	}
	return baseURL
}

// printProviderModels renders one provider's catalog, sorted by model ID.
func printProviderModels(providerName string, models []provider.ModelInfo) {
	// Sort model IDs alphabetically.
	sort.Slice(models, func(i, j int) bool {
		return models[i].ID < models[j].ID
	})

	fmt.Printf("%s (%d models):\n", providerName, len(models))
	for _, m := range models {
		price := modelPrice(providerName, m.ID)
		if (providerName == "mistral" || providerName == "agentgateway") && m.ContextWindow > 0 {
			fmt.Printf("  %-45s  %-10s  %-22s  %s\n", m.ID, humanTokens(m.ContextWindow), price, strings.Join(m.Capabilities, ","))
		} else if m.OwnedBy != "" {
			fmt.Printf("  %-45s  %-22s  %s\n", m.ID, price, m.OwnedBy)
		} else {
			fmt.Printf("  %-45s  %s\n", m.ID, price)
		}
	}
	fmt.Println()
}

// modelPrice returns a human-readable per-million-token price for a model, or
// "" when the pricing snapshot has no entry for it. Prices come from the
// embedded models.dev snapshot (refreshed daily at startup); local providers
// (ollama, agentgateway) and unknown models have no price and show none.
func modelPrice(providerName, modelID string) string {
	pm, ok := provider.CostFor(providerName, modelID)
	if !ok {
		return ""
	}
	return fmt.Sprintf("$%s/$%s per 1M", priceAmount(pm.Input), priceAmount(pm.Output))
}

// priceAmount renders a per-million-token USD rate at a precision that suits
// its magnitude: two decimals for rates at or above a cent, three for the
// sub-cent rates some cheap models carry.
func priceAmount(v float64) string {
	if v == 0 {
		return "—"
	}
	if v < 0.01 {
		return fmt.Sprintf("%.3f", v)
	}
	return fmt.Sprintf("%.2f", v)
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
