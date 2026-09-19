package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimetron/pi-go/internal/plugin"
)

// pluginTimeout bounds a whole plugin command. Installing clones one or two
// repositories, so it is generous but finite: a network stall must not hang a
// CLI invocation forever.
const pluginTimeout = 6 * time.Minute

// pluginHome returns the pi-go home directory, honoring PI_GO_HOME so a test
// (or a user with a relocated install) can point plugin operations elsewhere.
func pluginHome() (string, error) {
	if override := os.Getenv("PI_GO_HOME"); override != "" {
		return override, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locating home directory: %w", err)
	}
	return filepath.Join(home, ".pi-go"), nil
}

// newPluginManager builds a Manager rooted at the resolved pi-go home and a
// context bounded by pluginTimeout, with progress routed to the command's
// output. The returned cancel func must be deferred by the caller.
func newPluginManager(cmd *cobra.Command) (*plugin.Manager, context.Context, context.CancelFunc, error) {
	home, err := pluginHome()
	if err != nil {
		return nil, nil, nil, err
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), pluginTimeout)
	m := &plugin.Manager{
		PiHome: home,
		Log: func(format string, args ...any) {
			fmt.Fprintf(cmd.OutOrStdout(), format+"\n", args...)
		},
	}
	return m, ctx, cancel, nil
}

// newPluginCmd wires up `pi plugin`, which installs skills published through a
// Claude Code-compatible plugin marketplace.
func newPluginCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "plugin",
		Short: "Install skills from plugin marketplaces",
		Long: `Install and manage plugins — bundles of skills published through a plugin
marketplace.

pi-go reads the same .claude-plugin/marketplace.json manifest other coding
agents use, so existing marketplaces work unchanged:

    pi plugin marketplace add obra/superpowers-marketplace
    pi plugin install superpowers@superpowers-marketplace

Installed plugins live in ~/.pi-go/plugins, and their skills are discovered
automatically. Plugin skills have lower precedence than your own: a skill in
~/.pi-go/skills or .pi-go/skills with the same name always wins.`,
	}

	cmd.AddCommand(newPluginMarketplaceCmd())
	cmd.AddCommand(newPluginInstallCmd())
	cmd.AddCommand(newPluginListCmd())
	cmd.AddCommand(newPluginUninstallCmd())
	cmd.AddCommand(newPluginUpdateCmd())

	return cmd
}

func newPluginMarketplaceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "marketplace",
		Short: "Manage plugin marketplaces",
	}
	cmd.AddCommand(newPluginMarketplaceAddCmd())
	cmd.AddCommand(newPluginMarketplaceListCmd())
	return cmd
}

func newPluginMarketplaceAddCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "add <source>",
		Short: "Register a plugin marketplace",
		Long: `Register a plugin marketplace from a GitHub shorthand (owner/repo), a git URL,
or a local directory containing .claude-plugin/marketplace.json.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			m, ctx, cancel, err := newPluginManager(cmd)
			if err != nil {
				return err
			}
			defer cancel()

			rec, err := m.AddMarketplace(ctx, args[0])
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s  %s\n", rec.Name, rec.Source)
			return nil
		},
	}
}

func newPluginMarketplaceListCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List registered marketplaces",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			m, _, cancel, err := newPluginManager(cmd)
			if err != nil {
				return err
			}
			defer cancel()

			records, err := m.ListMarketplaces()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(records) == 0 {
				fmt.Fprintln(out, "No marketplaces registered. Add one with:\n  pi plugin marketplace add obra/superpowers-marketplace")
				return nil
			}
			for _, rec := range records {
				fmt.Fprintf(out, "%s  %s  (updated %s)\n", rec.Name, rec.Source, rec.LastUpdated)
			}
			return nil
		},
	}
}

func newPluginInstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install <plugin[@marketplace]>",
		Short: "Install a plugin",
		Long: `Install a plugin and make its skills available.

The marketplace may be omitted when exactly one registered marketplace offers
the plugin.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			m, ctx, cancel, err := newPluginManager(cmd)
			if err != nil {
				return err
			}
			defer cancel()

			inst, err := m.Install(ctx, args[0])
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Installed %s %s\n", inst.Name, inst.Version)
			return nil
		},
	}
}

func newPluginListCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List installed plugins",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			m, _, cancel, err := newPluginManager(cmd)
			if err != nil {
				return err
			}
			defer cancel()

			installed, err := m.List()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(installed) == 0 {
				fmt.Fprintln(out, "No plugins installed. Install one with:\n  pi plugin install superpowers@superpowers-marketplace")
				return nil
			}
			for _, p := range installed {
				sha := p.Sha
				if len(sha) > 8 {
					sha = sha[:8]
				}
				version := p.Version
				if version == "" {
					version = "unknown"
				}
				fmt.Fprintf(out, "%-32s %-12s %s", p.Name, version, p.Marketplace)
				if sha != "" {
					fmt.Fprintf(out, "  %s", sha)
				}
				fmt.Fprintln(out)
			}
			return nil
		},
	}
}

func newPluginUninstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "uninstall <plugin>",
		Aliases: []string{"remove", "rm"},
		Short:   "Uninstall a plugin",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			m, _, cancel, err := newPluginManager(cmd)
			if err != nil {
				return err
			}
			defer cancel()

			if err := m.Uninstall(args[0]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Uninstalled %s\n", args[0])
			return nil
		},
	}
}

func newPluginUpdateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "update [plugin]",
		Short: "Update installed plugins from their marketplace",
		Long: `Re-install plugins from their recorded source, then refresh the registered
marketplaces. With no argument, every installed plugin is updated.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			m, ctx, cancel, err := newPluginManager(cmd)
			if err != nil {
				return err
			}
			defer cancel()

			name := ""
			if len(args) == 1 {
				name = args[0]
			}
			// Refresh catalogs first, so a new version is visible to the
			// re-install that follows.
			if err := m.UpdateMarketplace(ctx, ""); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: %v\n", err)
			}
			changed, err := m.Update(ctx, name)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if changed {
				fmt.Fprintln(out, "Updated.")
			} else {
				fmt.Fprintln(out, "Already up to date.")
			}
			return nil
		},
	}
}
