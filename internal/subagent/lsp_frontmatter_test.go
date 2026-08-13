package subagent

import (
	"os"
	"path/filepath"
	"testing"
)

// writeAgent drops a SKILL-style agent definition into dir and returns its path.
func writeAgent(t *testing.T, dir, name, frontmatter string) string {
	t.Helper()
	path := filepath.Join(dir, name+".md")
	body := "---\nname: " + name + "\ndescription: test agent\n" + frontmatter + "---\nYou are a test agent.\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write agent: %v", err)
	}
	return path
}

func TestAgentConfigLSPFrontmatter(t *testing.T) {
	tests := []struct {
		name        string
		frontmatter string
		want        string
	}{
		{"absent", "", ""},
		{"full", "lsp: full\n", "full"},
		{"off", "lsp: off\n", "off"},
		{"min", "lsp: min\n", "min"},
		{"lowercased", "lsp: FULL\n", "full"},
		{"trimmed", "lsp:    full   \n", "full"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := writeAgent(t, dir, "navigator", tt.frontmatter)

			cfg, err := ParseAgentFile(path)
			if err != nil {
				t.Fatalf("ParseAgentFile: %v", err)
			}
			if cfg.LSP != tt.want {
				t.Fatalf("LSP = %q, want %q", cfg.LSP, tt.want)
			}
		})
	}
}

// TestSpawnOptsLSPReachesArgs pins the seam that makes "LSP in subagents only"
// work: the child's surface is chosen by the parent, on the child's command
// line. Without this the child falls back to its own default and the parent
// cannot hand it the wide set.
func TestSpawnOptsLSPReachesArgs(t *testing.T) {
	tests := []struct {
		name     string
		lsp      string
		wantFlag bool
		wantVal  string
	}{
		{"empty leaves flag off", "", false, ""},
		{"full is passed through", "full", true, "full"},
		{"off is passed through", "off", true, "off"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := spawnArgs(SpawnOpts{Prompt: "do the thing", LSP: tt.lsp})

			idx := -1
			for i, a := range args {
				if a == "--lsp" {
					idx = i
					break
				}
			}
			if !tt.wantFlag {
				if idx != -1 {
					t.Fatalf("args contain --lsp for empty LSP: %v", args)
				}
				return
			}
			if idx == -1 {
				t.Fatalf("args missing --lsp: %v", args)
			}
			if idx+1 >= len(args) || args[idx+1] != tt.wantVal {
				t.Fatalf("--lsp value = %v, want %q (args: %v)", args[idx+1:], tt.wantVal, args)
			}
			// The prompt must stay last — it is positional.
			if args[len(args)-1] != "do the thing" {
				t.Fatalf("prompt is not the final arg: %v", args)
			}
		})
	}
}
