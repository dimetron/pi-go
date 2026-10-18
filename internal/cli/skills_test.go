package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// skillSourceFixture writes a local source directory holding two skills in the
// marketplace layout, and returns its path.
func skillSourceFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, name := range []string{"show-me", "other-skill"} {
		dir := filepath.Join(root, "plugins", name, "skills", name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		content := "---\nname: " + name + "\ndescription: " + name + " description\n---\nBody.\n"
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// runSkillsCmd runs a `pi skills` subcommand with PI_GO_HOME pointed at home,
// returning combined output. This mirrors runSkillsCmd's sibling
// runPluginCmd, which the plugin tests use.
func runSkillsCmd(t *testing.T, home string, args ...string) (string, error) {
	t.Helper()
	t.Setenv("PI_GO_HOME", home)

	cmd := newSkillsCmd()
	cmd.SetArgs(args)
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetContext(context.Background())
	err := cmd.Execute()
	return buf.String(), err
}

func TestSkillsCmd_Structure(t *testing.T) {
	cmd := newSkillsCmd()
	if cmd.Use != "skills" {
		t.Errorf("Use = %q, want skills", cmd.Use)
	}
	if cmd.Short == "" || cmd.Long == "" {
		t.Error("expected help text")
	}
	want := map[string]bool{"add": false, "list": false, "remove": false}
	for _, sub := range cmd.Commands() {
		if _, ok := want[sub.Name()]; ok {
			want[sub.Name()] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("subcommand %q is not registered", name)
		}
	}
	// `ls` is documented as an alias for list.
	listCmd, _, err := cmd.Find([]string{"ls"})
	if err != nil || listCmd.Name() != "list" {
		t.Errorf("ls alias not wired: cmd=%v err=%v", listCmd, err)
	}

	addCmd, _, err := cmd.Find([]string{"add"})
	if err != nil {
		t.Fatalf("find add: %v", err)
	}
	if addCmd.Flags().Lookup("skill") == nil {
		t.Error("--skill flag is not registered")
	}
	if addCmd.Flags().Lookup("force") == nil {
		t.Error("--force flag is not registered")
	}
}

func TestSkillsAddInstallsNamedSkill(t *testing.T) {
	home := t.TempDir()
	src := skillSourceFixture(t)

	out, err := runSkillsCmd(t, home, "add", src, "--skill", "show-me")
	if err != nil {
		t.Fatalf("add: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Installed show-me") {
		t.Errorf("output does not report the install:\n%s", out)
	}

	installed := filepath.Join(home, "skills", "show-me", "SKILL.md")
	if _, statErr := os.Stat(installed); statErr != nil {
		t.Fatalf("skill not installed at %s: %v", installed, statErr)
	}
	if _, statErr := os.Stat(filepath.Join(home, "skills", "other-skill")); !os.IsNotExist(statErr) {
		t.Errorf("unselected skill was installed: %v", statErr)
	}
}

func TestSkillsAddInstallsEverySkillWithoutFilter(t *testing.T) {
	home := t.TempDir()
	src := skillSourceFixture(t)

	if _, err := runSkillsCmd(t, home, "add", src); err != nil {
		t.Fatalf("add: %v", err)
	}
	for _, name := range []string{"show-me", "other-skill"} {
		if _, err := os.Stat(filepath.Join(home, "skills", name, "SKILL.md")); err != nil {
			t.Errorf("%s not installed: %v", name, err)
		}
	}
}

func TestSkillsAddRequiresSource(t *testing.T) {
	home := t.TempDir()
	if _, err := runSkillsCmd(t, home, "add"); err == nil {
		t.Fatal("expected an error with no source argument")
	}
}

func TestSkillsAddUnknownSkillFails(t *testing.T) {
	home := t.TempDir()
	src := skillSourceFixture(t)

	_, err := runSkillsCmd(t, home, "add", src, "--skill", "nope")
	if err == nil {
		t.Fatal("expected an error for an unknown skill")
	}
	if !strings.Contains(err.Error(), "available: other-skill, show-me") {
		t.Errorf("error = %q, want the available skills listed", err)
	}
}

func TestSkillsAddRefusesExistingWithoutForce(t *testing.T) {
	home := t.TempDir()
	src := skillSourceFixture(t)

	if _, err := runSkillsCmd(t, home, "add", src, "--skill", "show-me"); err != nil {
		t.Fatalf("first add: %v", err)
	}
	if _, err := runSkillsCmd(t, home, "add", src, "--skill", "show-me"); err == nil {
		t.Fatal("expected an error on re-install without --force")
	}
	if _, err := runSkillsCmd(t, home, "add", src, "--skill", "show-me", "--force"); err != nil {
		t.Fatalf("add --force: %v", err)
	}
}

func TestSkillsListAndRemove(t *testing.T) {
	home := t.TempDir()
	src := skillSourceFixture(t)
	if _, err := runSkillsCmd(t, home, "add", src, "--skill", "show-me"); err != nil {
		t.Fatalf("add: %v", err)
	}

	out, err := runSkillsCmd(t, home, "list")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(out, "show-me") || !strings.Contains(out, "show-me description") {
		t.Errorf("list output missing the installed skill:\n%s", out)
	}

	out, err = runSkillsCmd(t, home, "remove", "show-me")
	if err != nil {
		t.Fatalf("remove: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Removed show-me") {
		t.Errorf("remove output = %q", out)
	}
	if _, err := os.Stat(filepath.Join(home, "skills", "show-me")); !os.IsNotExist(err) {
		t.Errorf("skill directory survived removal: %v", err)
	}

	// Listing an empty home is a message, not an error.
	out, err = runSkillsCmd(t, home, "list")
	if err != nil {
		t.Fatalf("empty list: %v", err)
	}
	if !strings.Contains(out, "No skills installed") {
		t.Errorf("empty list output = %q", out)
	}
}

func TestSkillsRemoveMissingSkillFails(t *testing.T) {
	home := t.TempDir()
	_, err := runSkillsCmd(t, home, "remove", "absent")
	if err == nil {
		t.Fatal("expected an error removing a skill that is not installed")
	}
	if !strings.Contains(err.Error(), "not installed: absent") {
		t.Errorf("error = %q", err)
	}
}

// TestRootRegistersSkillsCmd guards the wiring: the command existing is not
// enough if the root command never attaches it.
func TestRootRegistersSkillsCmd(t *testing.T) {
	var buf bytes.Buffer
	cmd := newRootCmd()
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"skills", "--help"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(buf.String(), "Install individual skills") {
		t.Errorf("skills help not reachable from the root command:\n%s", buf.String())
	}
}
