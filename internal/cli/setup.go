package cli

import (
	"fmt"
	"os"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	"github.com/dimetron/pi-go/internal/auth"
	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/provider"
	"github.com/dimetron/pi-go/internal/tui"
)

// setupProviderEntry is what `pi setup` knows about a provider before the user
// has chosen one: which env var authenticates it, whether it needs one at all,
// and where to get a key.
//
// Order is the order shown in the radio list, and it is deliberate: the three
// hosted providers most people arrive with come first, the local daemons that
// need no account after them, and the ones needing a separate subscription or
// deployment last.
type setupProviderEntry struct {
	name     string
	label    string
	envVar   string
	needsKey bool
	keyURL   string
	// optionalKey asks for a key but accepts a blank answer; see
	// tui.SetupProvider.OptionalKey.
	optionalKey bool
}

var setupProviders = []setupProviderEntry{
	{
		name:     "anthropic",
		label:    "Anthropic (Claude)",
		envVar:   "ANTHROPIC_API_KEY",
		needsKey: true,
		keyURL:   "https://console.anthropic.com/settings/keys",
	},
	{
		name:     "openai",
		label:    "OpenAI",
		envVar:   "OPENAI_API_KEY",
		needsKey: true,
		keyURL:   "https://platform.openai.com/api-keys",
	},
	{
		name:     "gemini",
		label:    "Google Gemini",
		envVar:   "GEMINI_API_KEY",
		needsKey: true,
		keyURL:   "https://aistudio.google.com/apikey",
	},
	{
		name:     "ollama",
		label:    "Ollama (local daemon)",
		envVar:   "OLLAMA_API_KEY",
		needsKey: false,
	},
	{
		name:     "agentgateway",
		label:    "agentgateway (local gateway)",
		envVar:   "AGENTGATEWAY_API_KEY",
		needsKey: false,
		// A gateway runs open by default, but one with an apiKey policy
		// rejects every request without AGENTGATEWAY_API_KEY, so the key is
		// asked for rather than skipped.
		optionalKey: true,
	},
	{
		name:     "mistral",
		label:    "Mistral",
		envVar:   "MISTRAL_API_KEY",
		needsKey: true,
		keyURL:   "https://console.mistral.ai/api-keys",
	},
	{
		name:     "xai",
		label:    "xAI (Grok)",
		envVar:   "XAI_API_KEY",
		needsKey: true,
		keyURL:   "https://console.x.ai",
	},
	{
		name:     "openrouter",
		label:    "OpenRouter",
		envVar:   "OPENROUTER_API_KEY",
		needsKey: true,
		keyURL:   "https://openrouter.ai/keys",
	},
	{
		name:     "opencode",
		label:    "OpenCode",
		envVar:   "OPENCODE_API_KEY",
		needsKey: true,
		keyURL:   "https://opencode.ai/auth",
	},
	{
		name:     "azure",
		label:    "Azure OpenAI (deployment)",
		envVar:   "AZUREOPENAI_API_KEY",
		needsKey: true,
		keyURL:   "https://portal.azure.com",
	},
}

func newSetupCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Configure a provider interactively",
		Long: `Configure a provider, its API key and the default model, using an
interactive wizard.

The wizard asks three questions:

  1. which provider to use
  2. the API key for that provider (skipped for a local daemon; optional
     for agentgateway, which only needs one behind an apiKey policy)
  3. which model should be the default

On confirm it writes the key to ~/.pi-go/.env and the provider and model to
the "default" role in ~/.pi-go/config.json — the same places ` + "`pi login`" + ` and a
hand-edited config use, so nothing is configured in a form only this command
understands.

The model list is read from the embedded catalog, so the wizard needs no
network and cannot fail on a provider that is unreachable. For a provider with
no offline catalog (Ollama, agentgateway, Azure) the model is typed in by hand,
because only the user knows which models their daemon or deployment serves.

Examples:
  pi setup                 # configure a provider
  pi login codex           # OAuth login (ChatGPT subscription)`,
		Args: cobra.NoArgs,
		RunE: runSetup,
	}
	return cmd
}

func runSetup(cmd *cobra.Command, _ []string) error {
	// Load .env first so an already-configured key shows up as the current
	// state rather than reading as unset.
	loadDotEnv()

	if !setupHasTTY() {
		return fmt.Errorf("pi setup needs a terminal; on a headless host set the key directly, e.g. ANTHROPIC_API_KEY=... in ~/.pi-go/.env")
	}

	cfg, err := config.Load()
	if err != nil {
		// A broken config is not a reason to refuse setup: this command
		// exists to produce a working one. Start from defaults instead.
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: could not read existing config (%v); starting from defaults\n", err)
		cfg = config.Defaults()
	}

	wizardCfg := tui.SetupConfig{
		Providers:       setupWizardProviders(),
		Candidates:      setupModelCandidates(),
		InitialProvider: currentProvider(cfg),
		Theme:           cfg.Theme,
	}

	w := tui.NewSetupWizard(wizardCfg, tui.PaletteForName(cfg.Theme))

	opts := append(tui.SetupProgramOptions(), tea.WithContext(cmd.Context()))
	p := tea.NewProgram(w, opts...)
	if _, err := p.Run(); err != nil {
		return fmt.Errorf("running setup wizard: %w", err)
	}

	result := w.Result()
	if result.Canceled {
		fmt.Fprintln(cmd.OutOrStdout(), "Setup canceled; nothing changed.")
		return nil
	}

	entry, ok := lookupSetupProvider(result.Provider)
	if !ok {
		return fmt.Errorf("unknown provider %q", result.Provider)
	}

	if err := saveSetupResult(entry, result); err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "\nConfigured %s\n", entry.label)
	if entry.writesKey(result.APIKey) {
		fmt.Fprintf(out, "  key    ~/.pi-go/.env (%s)\n", maskKey(result.APIKey))
	}
	fmt.Fprintf(out, "  model  %s\n", result.Model)
	fmt.Fprintf(out, "  config ~/.pi-go/config.json\n")
	fmt.Fprintln(out, "\nRun `pi` to start a session.")
	return nil
}

// saveSetupResult persists the wizard's choice: the key to .env, the provider
// and model to the default role.
//
// The key is written before the config so a failure between the two leaves a
// key with an unset role — recoverable by re-running — rather than a role
// pointing at a credential that was never stored.
func saveSetupResult(entry setupProviderEntry, result tui.SetupResult) error {
	if entry.writesKey(result.APIKey) {
		if err := auth.SaveKey(entry.envVar, result.APIKey); err != nil {
			return fmt.Errorf("saving API key: %w", err)
		}
	}

	if err := config.SaveDefaultRole(result.Model, entry.name); err != nil {
		return fmt.Errorf("saving default model: %w", err)
	}
	return nil
}

// writesKey reports whether saving this result stores a key. A blank optional
// key writes nothing, so it leaves any key already in .env in place rather than
// erasing it.
func (e setupProviderEntry) writesKey(key string) bool {
	return e.needsKey || (e.optionalKey && strings.TrimSpace(key) != "")
}

// setupWizardProviders projects the CLI's provider table into the wizard's
// view of it.
func setupWizardProviders() []tui.SetupProvider {
	out := make([]tui.SetupProvider, 0, len(setupProviders))
	for _, p := range setupProviders {
		out = append(out, tui.SetupProvider{
			Name:        p.name,
			Label:       p.label,
			EnvVar:      p.envVar,
			NeedsKey:    p.needsKey,
			KeyURL:      p.keyURL,
			OptionalKey: p.optionalKey,
		})
	}
	return out
}

// setupHasTTY reports whether both stdin and stdout are terminals.
//
// It checks isatty rather than the os.ModeCharDevice bit used by isTerminal:
// /dev/null is a character device but not a terminal, so a char-device check
// accepts it. That matters here because `pi setup < /dev/null` — a plausible
// thing to run from a script or a Makefile — would then start an interactive
// program on a dead input, and the wizard would sit on an empty read forever
// instead of reporting that it has nowhere to draw.
//
// Both streams are required: stdin without a tty cannot be typed into, and
// stdout without one would print the wizard's escape sequences into a file.
func setupHasTTY() bool {
	return term.IsTerminal(os.Stdin.Fd()) && term.IsTerminal(os.Stdout.Fd())
}

// setupModelCandidates builds the offline model list per provider.
//
// It reads provider.CatalogFor, which merges the embedded modeldata snapshots
// with KnownModels and any cached live catalog. Reusing that function rather
// than the raw snapshot is what makes the wizard offer a model the user has
// already fetched with `pi model list` — and, because it is the same source
// ValidateModel checks against, every model offered is one pi will accept.
//
// Providers with no catalog (ollama, agentgateway, azure) get no entry on
// purpose: their models live on a daemon or in a subscription pi cannot
// enumerate offline, and inventing a list would offer names that do not exist.
// The wizard falls back to free-text entry for them.
func setupModelCandidates() map[string][]string {
	out := make(map[string][]string, len(setupProviders))
	for _, p := range setupProviders {
		ids := provider.CatalogFor(p.name)
		if len(ids) == 0 {
			continue
		}
		out[p.name] = setupRankModels(p.name, ids)
	}
	return out
}

// setupRankModels orders a catalog newest-first, which is what a user picking
// a default model wants to see: the current tier before last year's.
//
// The date comes from the models.dev snapshot, and models the snapshot does not
// cover sort last by name rather than being dropped — an unranked model is
// still a valid choice, just not a ranked one.
func setupRankModels(providerName string, ids []string) []string {
	type ranked struct {
		id   string
		date string
	}
	items := make([]ranked, 0, len(ids))
	for _, id := range ids {
		items = append(items, ranked{id: id, date: provider.ModelReleaseDate(providerName, id)})
	}

	// Two keys, date descending then ID ascending, so the order is
	// deterministic and two runs produce the same list.
	sort.Slice(items, func(i, j int) bool {
		if items[i].date != items[j].date {
			return items[i].date > items[j].date
		}
		return items[i].id < items[j].id
	})

	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.id)
	}
	return out
}

// currentProvider returns the provider already configured, so re-running setup
// starts on the user's existing choice.
func currentProvider(cfg config.Config) string {
	_, prov, _, _, _, err := cfg.ResolveRole("default")
	if err != nil {
		return ""
	}
	return prov
}

func lookupSetupProvider(name string) (setupProviderEntry, bool) {
	for _, p := range setupProviders {
		if strings.EqualFold(p.name, name) {
			return p, true
		}
	}
	return setupProviderEntry{}, false
}
