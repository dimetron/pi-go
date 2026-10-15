package extension

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimetron/pi-go/internal/plugin"
)

func TestExpandCommand(t *testing.T) {
	tests := []struct {
		name string
		body string
		args []string
		want string
	}{
		{name: "arguments placeholder", body: "Review $ARGUMENTS", args: []string{"a.go", "b.go"}, want: "Review a.go b.go"},
		{name: "braced arguments", body: "Review ${ARGUMENTS}.", args: []string{"a.go"}, want: "Review a.go."},
		{name: "positional", body: "Look at $1 and then $2.", args: []string{"a.go", "b.go"}, want: "Look at a.go and then b.go."},
		{name: "missing positional is empty", body: "Look at $1 and $2.", args: []string{"a.go"}, want: "Look at a.go and ."},
		{name: "ten is not one", body: "$1 $10", args: []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"}, want: "a j"},
		{name: "no placeholder appends", body: "Review this.", args: []string{"a.go"}, want: "Review this.\n\na.go"},
		{name: "placeholder suppresses append", body: "Review $1.", args: []string{"a.go", "extra"}, want: "Review a.go."},
		{name: "no args leaves body", body: "Review this.", args: nil, want: "Review this."},
		{name: "word suffix is not a placeholder", body: "$ARGUMENTS_FILE and $1st", args: []string{"x"}, want: "$ARGUMENTS_FILE and $1st\n\nx"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ExpandCommand(tt.body, tt.args); got != tt.want {
				t.Errorf("ExpandCommand() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSplitCommandFrontmatterKeepsMarkdownRule(t *testing.T) {
	body := "First paragraph.\n\n---\n\nSecond paragraph.\n"
	description, hint, got := splitCommandFrontmatter(body)
	if description != "" || hint != "" {
		t.Fatalf("frontmatter = %q %q, want none", description, hint)
	}
	if got != "First paragraph.\n\n---\n\nSecond paragraph." {
		t.Fatalf("body = %q", got)
	}
}

func TestLoadCommandsFileShapes(t *testing.T) {
	dir := t.TempDir()
	writeCommand(t, filepath.Join(dir, "review.md"), "---\ndescription: \"Review a change\"\nargument-hint: [path]\nallowed-tools: read\n---\nReview $1 for bugs.\n")
	writeCommand(t, filepath.Join(dir, "review.prompt"), "this loses to markdown\n")
	writeCommand(t, filepath.Join(dir, "ship.prompt"), "Ship $ARGUMENTS\n")
	writeCommand(t, filepath.Join(dir, "git", "commit.md"), "Write a commit message.\n")
	writeCommand(t, filepath.Join(dir, "README.md"), "not a command\n")
	writeCommand(t, filepath.Join(dir, "bad name.md"), "nope\n")
	writeCommand(t, filepath.Join(dir, "a", "b", "c", "too-deep.md"), "too deep\n")

	cmds, err := LoadCommands(CommandDir{Path: dir, Source: "project"})
	if err != nil {
		t.Fatalf("LoadCommands: %v", err)
	}
	if len(cmds) != 3 {
		t.Fatalf("len(cmds) = %d, want 3 (%+v)", len(cmds), commandNames(cmds))
	}

	review, ok := FindCommand(cmds, "Review")
	if !ok {
		t.Fatal("review command not found")
	}
	if review.Description != "Review a change" {
		t.Errorf("description = %q", review.Description)
	}
	if review.ArgumentHint != "[path]" {
		t.Errorf("argument hint = %q", review.ArgumentHint)
	}
	if review.Body != "Review $1 for bugs." {
		t.Errorf("body = %q", review.Body)
	}
	if review.Invocation() != "/review [path]" {
		t.Errorf("invocation = %q", review.Invocation())
	}
	if !strings.Contains(review.Summary(), "Review a change") || !strings.Contains(review.Summary(), "[path]") {
		t.Errorf("summary = %q", review.Summary())
	}

	ship, ok := FindCommand(cmds, "ship")
	if !ok || ship.Body != "Ship $ARGUMENTS" {
		t.Fatalf("ship = %+v, found %v", ship, ok)
	}
	commit, ok := FindCommand(cmds, "git:commit")
	if !ok || commit.Body != "Write a commit message." {
		t.Fatalf("git:commit = %+v, found %v", commit, ok)
	}
}

func TestLoadCommandsPrecedence(t *testing.T) {
	user := t.TempDir()
	projectClaude := t.TempDir()
	project := t.TempDir()
	pluginDir := t.TempDir()
	writeCommand(t, filepath.Join(user, "review.md"), "user\n")
	writeCommand(t, filepath.Join(user, "user-only.md"), "user only\n")
	writeCommand(t, filepath.Join(projectClaude, "review.md"), "claude project\n")
	writeCommand(t, filepath.Join(project, "review.md"), "pi-go project\n")
	writeCommand(t, filepath.Join(pluginDir, "review.md"), "plugin\n")
	writeCommand(t, filepath.Join(pluginDir, "brainstorm.md"), "plugin only\n")

	cmds, err := LoadCommands(
		CommandDir{Path: pluginDir, Source: "plugin", Namespace: "superpowers"},
		CommandDir{Path: user, Source: "user"},
		CommandDir{Path: projectClaude, Source: "project"},
		CommandDir{Path: project, Source: "project"},
	)
	if err != nil {
		t.Fatalf("LoadCommands: %v", err)
	}

	review, ok := FindCommand(cmds, "review")
	if !ok || review.Body != "pi-go project" {
		t.Fatalf("review body = %q, found %v", review.Body, ok)
	}
	if _, ok := FindCommand(cmds, "user-only"); !ok {
		t.Error("user-only command missing")
	}
	pluginReview, ok := FindCommand(cmds, "superpowers:review")
	if !ok || pluginReview.Body != "plugin" {
		t.Fatalf("namespaced review = %+v, found %v", pluginReview, ok)
	}
	if _, ok := FindCommand(cmds, "superpowers:brainstorm"); !ok {
		t.Error("namespaced plugin command missing")
	}
	// A plugin command must not occupy the un-namespaced name.
	if got, ok := FindCommand(cmds, "brainstorm"); ok {
		t.Errorf("brainstorm leaked out of its plugin namespace: %+v", got)
	}
}

func TestLoadCommandsMissingDir(t *testing.T) {
	cmds, err := LoadCommands(CommandDir{Path: filepath.Join(t.TempDir(), "missing")})
	if err != nil {
		t.Fatalf("LoadCommands: %v", err)
	}
	if len(cmds) != 0 {
		t.Fatalf("cmds = %+v, want none", cmds)
	}
}

func TestLoadCommandsBlocksCritical(t *testing.T) {
	dir := t.TempDir()
	writeCommand(t, filepath.Join(dir, "clean.md"), "Clean body.\n")
	writeCommand(t, filepath.Join(dir, "dirty.md"), "Hidden \u202E text.\n")

	cmds, err := LoadCommandsWithOptions(LoadOptions{AuditMode: AuditBlock}, CommandDir{Path: dir, Source: "project"})
	if err != nil {
		t.Fatalf("LoadCommands: %v", err)
	}
	if _, ok := FindCommand(cmds, "clean"); !ok {
		t.Error("clean command was blocked")
	}
	if _, ok := FindCommand(cmds, "dirty"); ok {
		t.Error("dirty command was loaded")
	}
}

func TestCommandDirsOrder(t *testing.T) {
	userHome := t.TempDir()
	piHome := filepath.Join(userHome, ".pi-go")
	root := filepath.Join(t.TempDir(), "repo", "sub")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}

	pluginCommands := filepath.Join(plugin.PluginDir(piHome, "demo"), "commands")
	if err := os.MkdirAll(pluginCommands, 0o755); err != nil {
		t.Fatal(err)
	}
	reg := plugin.NewRegistry()
	reg.Plugins["demo"] = plugin.Installed{Name: "demo", InstalledAt: "2026-01-01T00:00:00Z"}
	if err := reg.Save(piHome); err != nil {
		t.Fatal(err)
	}

	for _, dir := range []string{
		filepath.Join(userHome, ".claude", "commands"),
		filepath.Join(userHome, ".pi-go", "commands"),
		filepath.Join(filepath.Dir(root), ".claude", "commands"),
		filepath.Join(filepath.Dir(root), ".pi-go", "commands"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	dirs := commandDirs(root, userHome, piHome)
	want := []CommandDir{
		{Path: pluginCommands, Source: "plugin", Namespace: "demo"},
		{Path: filepath.Join(userHome, ".claude", "commands"), Source: "user"},
		{Path: filepath.Join(userHome, ".pi-go", "commands"), Source: "user"},
		{Path: filepath.Join(filepath.Dir(root), ".claude", "commands"), Source: "project"},
		{Path: filepath.Join(filepath.Dir(root), ".pi-go", "commands"), Source: "project"},
	}
	if len(dirs) != len(want) {
		t.Fatalf("commandDirs = %+v, want %+v", dirs, want)
	}
	for i := range want {
		if dirs[i] != want[i] {
			t.Errorf("dirs[%d] = %+v, want %+v", i, dirs[i], want[i])
		}
	}
}

func TestResolvePiHomeHonorsOverride(t *testing.T) {
	t.Setenv("PI_GO_HOME", "/opt/pi-go")
	if got := resolvePiHome("/home/someone"); got != "/opt/pi-go" {
		t.Errorf("resolvePiHome = %q, want the PI_GO_HOME override", got)
	}
}

func writeCommand(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func commandNames(cmds []Command) []string {
	names := make([]string, 0, len(cmds))
	for _, c := range cmds {
		names = append(names, c.Name)
	}
	return names
}
