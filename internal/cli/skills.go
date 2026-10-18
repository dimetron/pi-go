package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dimetron/pi-go/internal/plugin"
)

// newSkillsCmd wires up `pi skills`, which installs individual skills from a
// repository without going through a plugin marketplace.
//
// This is the shortcut for "I want that one skill": a marketplace bundle
// installs everything its catalog lists, whereas `pi skills add` copies only
// the named skill directory into ~/.pi-go/skills, where it outranks a bundled or
// plugin skill of the same name.
func newSkillsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skills",
		Short: "Install individual skills from a repository",
		Long: `Install individual skills from a git repository or a local directory.

A skill is a directory holding a SKILL.md file. Skills are discovered under
skills/<name>/SKILL.md and .pi-go/skills/<name>/SKILL.md — the layouts used by
plugin marketplaces and by pi-go's own skill directories.

Installed skills live in ~/.pi-go/skills and are available in every session. A
plugin bundles skills from a whole catalog; this installs just the ones you
name:

    pi skills add humanlayer/skills --skill show-me`,
	}

	cmd.AddCommand(newSkillsAddCmd())
	cmd.AddCommand(newSkillsListCmd())
	cmd.AddCommand(newSkillsRemoveCmd())

	return cmd
}

func newSkillsAddCmd() *cobra.Command {
	var (
		names []string
		force bool
	)
	cmd := &cobra.Command{
		Use:   "add <source>",
		Short: "Install skills from a repository or directory",
		Long: `Install skills from a git repository or a local directory.

The source may be a GitHub shorthand (owner/repo), a git URL, or a local
directory. --skill selects which skills to install and may be repeated; without
it, every skill the source contains is installed.

An existing skill of the same name is left alone unless --force is given, so an
install cannot silently replace a skill you wrote or edited.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			m, ctx, cancel, err := newPluginManager(cmd)
			if err != nil {
				return err
			}
			defer cancel()

			installed, err := m.InstallSkills(ctx, args[0], names, force)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			for _, skill := range installed {
				fmt.Fprintf(out, "Installed %s -> %s\n", skill.Name, skill.Path)
			}
			fmt.Fprintf(out, "\n%d skill(s) installed. Run /skill-list in a session to see them.\n", len(installed))
			return nil
		},
	}
	cmd.Flags().StringSliceVar(&names, "skill", nil,
		"install only this skill (repeatable, or comma-separated)")
	cmd.Flags().BoolVar(&force, "force", false, "replace skills that are already installed")
	return cmd
}

func newSkillsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List skills installed in the user skill directory",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			home, err := pluginHome()
			if err != nil {
				return err
			}
			skills, err := plugin.ListInstalledSkills(home)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(skills) == 0 {
				fmt.Fprintf(out, "No skills installed in %s\n", plugin.SkillsDir(home))
				return nil
			}
			for _, skill := range skills {
				fmt.Fprintf(out, "%-32s %s\n", skill.Name, skill.Description)
			}
			return nil
		},
	}
}

func newSkillsRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <skill> [skill...]",
		Short: "Remove installed skills",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			home, err := pluginHome()
			if err != nil {
				return err
			}
			removed, missing := plugin.RemoveSkills(home, args)
			out := cmd.OutOrStdout()
			for _, name := range removed {
				fmt.Fprintf(out, "Removed %s\n", name)
			}
			if len(missing) > 0 {
				return fmt.Errorf("not installed: %s", strings.Join(missing, ", "))
			}
			return nil
		},
	}
}
