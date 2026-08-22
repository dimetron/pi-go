package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimetron/pi-go/internal/testenv"
)

// writeGlobalConfig points HOME at a temp dir holding the given config, so
// writeRoleSummary reads it instead of the developer's real one.
func writeGlobalConfig(t *testing.T, cfg map[string]any) {
	t.Helper()
	home := t.TempDir()
	testenv.SetHome(t, home)

	dir := filepath.Join(home, ".pi-go")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), raw, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	// config.Load also merges ./.pi-go/config.json from the process working
	// directory, which during `go test` is the package source tree. Move to a
	// clean dir so the repo's own config cannot alter the result.
	t.Chdir(t.TempDir())
}

func TestWriteRoleSummaryShowsEachRole(t *testing.T) {
	writeGlobalConfig(t, map[string]any{
		"roles": map[string]any{
			"default": map[string]any{"model": "claude-sonnet-5"},
			"smol":    map[string]any{"model": "claude-haiku-4-5-20251001"},
			"slow":    map[string]any{"model": "claude-opus-5"},
		},
	})
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test")

	var buf bytes.Buffer
	writeRoleSummary(&buf)
	out := buf.String()

	for _, want := range []string{
		"default", "claude-sonnet-5",
		"smol", "claude-haiku-4-5-20251001",
		"slow", "claude-opus-5",
		"anthropic", "ANTHROPIC_API_KEY (set)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("role summary missing %q:\n%s", want, out)
		}
	}

	// plan is unconfigured here and must be reported as a fallback rather than
	// silently echoing the default's model.
	if !strings.Contains(out, "falls back to default") {
		t.Errorf("unconfigured plan role not reported as a fallback:\n%s", out)
	}
}

func TestWriteRoleSummaryFlagsMissingCredential(t *testing.T) {
	writeGlobalConfig(t, map[string]any{
		"roles": map[string]any{"default": map[string]any{"model": "claude-sonnet-5"}},
	})
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "")

	var buf bytes.Buffer
	writeRoleSummary(&buf)

	if !strings.Contains(buf.String(), "ANTHROPIC_API_KEY (MISSING)") {
		t.Errorf("absent credential not flagged:\n%s", buf.String())
	}
}

// A local Ollama daemon needs no key, so demanding one would be a false alarm;
// a :cloud tag reaches api.ollama.com and does.
func TestWriteRoleSummaryOllamaCredential(t *testing.T) {
	tests := []struct {
		name  string
		model string
		want  string
	}{
		{"local daemon needs no key", "ollama/gemma4:e4b", "none (local daemon)"},
		{"cloud tag needs a key", "minimax-m3:cloud", "OLLAMA_API_KEY (MISSING)"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			writeGlobalConfig(t, map[string]any{
				"roles": map[string]any{"default": map[string]any{"model": tc.model}},
			})
			t.Setenv("OLLAMA_API_KEY", "")

			var buf bytes.Buffer
			writeRoleSummary(&buf)

			if !strings.Contains(buf.String(), tc.want) {
				t.Errorf("got:\n%s\nwant it to contain %q", buf.String(), tc.want)
			}
		})
	}
}

// The footer belongs to the root command only; a help func set on the root is
// inherited by every subcommand, which is exactly how it would leak.
func TestRootHelpFooterDoesNotLeakToSubcommands(t *testing.T) {
	writeGlobalConfig(t, map[string]any{
		"roles": map[string]any{"default": map[string]any{"model": "claude-sonnet-5"}},
	})

	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"--help"})
	if err := root.Execute(); err != nil {
		t.Fatalf("root --help: %v", err)
	}
	if !strings.Contains(buf.String(), "Configured roles") {
		t.Errorf("root help is missing the role table:\n%s", buf.String())
	}

	buf.Reset()
	root.SetArgs([]string{"audit", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatalf("audit --help: %v", err)
	}
	if strings.Contains(buf.String(), "Configured roles") {
		t.Errorf("subcommand help inherited the root footer:\n%s", buf.String())
	}
}

// The examples are the part of help most likely to rot, so pin the routing
// forms they teach.
func TestRootExampleCoversEveryProvider(t *testing.T) {
	example := newRootCmd().Example
	for _, want := range []string{
		"--model claude-", "--model gpt-", "--model gemini-", "--model mistral-",
		"--model ollama/", "--model minimax-m3:cloud", "--model azure/", "--model opencode/",
	} {
		if !strings.Contains(example, want) {
			t.Errorf("root examples missing %q", want)
		}
	}
}
