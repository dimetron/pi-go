package palace

import (
	"sort"
	"strings"
	"testing"
)

// tripleKey renders a triple as "subject|predicate|object" so a test can state
// its expectation as a plain string instead of a struct literal.
func tripleKey(t ExtractedTriple) string {
	return t.Subject + "|" + t.Predicate + "|" + t.Object
}

func tripleKeys(triples []ExtractedTriple) []string {
	keys := make([]string, 0, len(triples))
	for _, t := range triples {
		keys = append(keys, tripleKey(t))
	}
	return keys
}

// TestExtractTriples_Branches characterizes the input shapes that reach the
// heuristics' guard clauses. These are the branches the sorted/truncated tool
// output does not exercise, so they are pinned here directly against
// extractTriples.
func TestExtractTriples_Branches(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		text       string
		sourceFile string
		want       []string // every triple, as subject|predicate|object
	}{
		{
			name:       "stdlib imports are dropped as noise",
			text:       "import \"fmt\"\nimport \"strings\"\n",
			sourceFile: "internal/palace/kg.go",
			want:       nil,
		},
		{
			name:       "relative imports are dropped as noise",
			text:       "import \"./sibling\"\nimport \"../parent\"\n",
			sourceFile: "internal/palace/kg.go",
			want:       nil,
		},
		{
			name: "imports without a source file anchor to codebase",
			text: "import \"github.com/dimetron/pi-go/internal/palace\"\n",
			want: []string{"codebase|imports|github.com/dimetron/pi-go/internal/palace"},
		},
		{
			name:       "the same import twice yields one triple",
			text:       "import \"github.com/spf13/cobra\"\nimport \"github.com/spf13/cobra\"\n",
			sourceFile: "cmd/root.go",
			want:       []string{"root.go|imports|github.com/spf13/cobra"},
		},
		{
			name:       "an import of the source file itself is self-referential and dropped",
			text:       "import \"root.go\"\n",
			sourceFile: "root.go",
			want:       nil,
		},
		{
			name:       "a parenthesized import block is scanned line by line",
			text:       "import (\n\t\"fmt\"\n\t\"github.com/spf13/cobra\"\n)\n",
			sourceFile: "cmd/root.go",
			want:       []string{"root.go|imports|github.com/spf13/cobra"},
		},
		{
			name:       "an unterminated import block runs to end of text",
			text:       "import (\n\t\"github.com/spf13/cobra\"\n\t\"github.com/spf13/pflag\"\n",
			sourceFile: "cmd/root.go",
			want: []string{
				"root.go|imports|github.com/spf13/cobra",
				"root.go|imports|github.com/spf13/pflag",
			},
		},
		{
			name:       "a path already seen on a single-line import is not re-emitted from the block",
			text:       "import \"github.com/spf13/cobra\"\n\nimport (\n\t\"github.com/spf13/cobra\"\n)\n",
			sourceFile: "cmd/root.go",
			want:       []string{"root.go|imports|github.com/spf13/cobra"},
		},
		{
			name:       "duplicates inside one block collapse to a single triple",
			text:       "import (\n\t\"github.com/spf13/cobra\"\n\t\"github.com/spf13/cobra\"\n)\n",
			sourceFile: "cmd/root.go",
			want:       []string{"root.go|imports|github.com/spf13/cobra"},
		},
		{
			name: "declarations without a source file yield nothing",
			text: "func Handle() {}\ntype Server struct {\n}\nclass Widget:\n",
			want: nil,
		},
		{
			name:       "generic declarations of common words are filtered",
			text:       "const err = 1\nvar result = 2\nlet ctx = 3\nconst Router = 4\n",
			sourceFile: "web/app.js",
			want:       []string{"Router|defined_in|web/app.js"},
		},
		{
			name:       "a func and a type of the same name collapse to one triple",
			text:       "func Server() {}\ntype Server struct {\n}\n",
			sourceFile: "internal/api/server.go",
			want:       []string{"Server|defined_in|internal/api/server.go"},
		},
		{
			name:       "sibling paths in the same directory become part_of",
			text:       "See internal/auth/token.go and internal/auth/handler.go for details.",
			sourceFile: "internal/auth/handler.go",
			want:       []string{"internal/auth/token.go|part_of|handler.go"},
		},
		{
			name:       "paths in a different directory are not part_of the source file",
			text:       "See internal/other/thing.go for details.",
			sourceFile: "internal/auth/handler.go",
			want:       nil,
		},
		{
			name: "paths without a source file yield nothing",
			text: "See internal/auth/token.go for details.",
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := tripleKeys(extractTriples(tt.text, tt.sourceFile))
			sort.Strings(got)
			want := append([]string(nil), tt.want...)
			sort.Strings(want)

			if strings.Join(got, "\n") != strings.Join(want, "\n") {
				t.Errorf("extractTriples(%q, %q):\n got %v\nwant %v", tt.text, tt.sourceFile, got, want)
			}
		})
	}
}

// TestExtractTriples_ConfidenceAndReason pins the confidence tier and reason
// attached to each heuristic's output, since the tool sorts on confidence and
// the agent reads the reason.
func TestExtractTriples_ConfidenceAndReason(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		text       string
		sourceFile string
		key        string
		wantConf   float64
		wantReason string
	}{
		{
			name:       "anchored import",
			text:       "import \"github.com/spf13/cobra\"\n",
			sourceFile: "cmd/root.go",
			key:        "root.go|imports|github.com/spf13/cobra",
			wantConf:   0.95,
			wantReason: "import statement, anchored to source file",
		},
		{
			name:       "unanchored import",
			text:       "import \"github.com/spf13/cobra\"\n",
			key:        "codebase|imports|github.com/spf13/cobra",
			wantConf:   0.50,
			wantReason: "import statement (no source file anchor)",
		},
		{
			name:       "go func declaration",
			text:       "func Handle() {}\n",
			sourceFile: "internal/api/server.go",
			key:        "Handle|defined_in|internal/api/server.go",
			wantConf:   0.95,
			wantReason: "Go func declaration in source file",
		},
		{
			name:       "go type declaration",
			text:       "type Server struct {\n}\n",
			sourceFile: "internal/api/server.go",
			key:        "Server|defined_in|internal/api/server.go",
			wantConf:   0.95,
			wantReason: "Go type declaration in source file",
		},
		{
			name:       "generic declaration",
			text:       "class Widget:\n",
			sourceFile: "web/widget.py",
			key:        "Widget|defined_in|web/widget.py",
			wantConf:   0.95,
			wantReason: "declaration in source file",
		},
		{
			name:       "sibling path",
			text:       "See internal/auth/token.go for details.",
			sourceFile: "internal/auth/handler.go",
			key:        "internal/auth/token.go|part_of|handler.go",
			wantConf:   0.70,
			wantReason: "shares directory with source file",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			for _, got := range extractTriples(tt.text, tt.sourceFile) {
				if tripleKey(got) != tt.key {
					continue
				}
				if got.Confidence != tt.wantConf {
					t.Errorf("confidence = %v, want %v", got.Confidence, tt.wantConf)
				}
				if got.Reason != tt.wantReason {
					t.Errorf("reason = %q, want %q", got.Reason, tt.wantReason)
				}
				return
			}
			t.Fatalf("triple %q not extracted from %q", tt.key, tt.text)
		})
	}
}

// TestExtractTriples_FirstHeuristicOwnsTheTriple pins the dedup tie-break: the
// same (subject, predicate, object) found twice keeps the confidence and reason
// of whichever heuristic ran first, not the highest-confidence one.
func TestExtractTriples_FirstHeuristicOwnsTheTriple(t *testing.T) {
	t.Parallel()

	// "cobra" is imported on a single line and again inside a block; the
	// single-line pass runs first and owns the reason.
	const text = "import \"github.com/spf13/cobra\"\n\nimport (\n\t\"github.com/spf13/cobra\"\n)\n"

	triples := extractTriples(text, "cmd/root.go")
	if len(triples) != 1 {
		t.Fatalf("got %d triples, want 1: %v", len(triples), tripleKeys(triples))
	}
	if triples[0].Reason != "import statement, anchored to source file" {
		t.Errorf("reason = %q", triples[0].Reason)
	}
}

func TestIsStdlibImport(t *testing.T) {
	t.Parallel()

	tests := []struct {
		path string
		want bool
	}{
		{"", true},
		{"fmt", true},
		{"net/http", true},
		{"./local", true},
		{"../parent", true},
		{"github.com/spf13/cobra", false},
		{"gopkg.in/yaml.v3", false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			t.Parallel()
			if got := isStdlibImport(tt.path); got != tt.want {
				t.Errorf("isStdlibImport(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestFileBaseAndPathDir(t *testing.T) {
	t.Parallel()

	tests := []struct {
		path     string
		wantBase string
		wantDir  string
	}{
		{"internal/palace/kg.go", "kg.go", "internal/palace"},
		{"main.go", "main.go", ""},
		{"", "", ""},
		{"/abs/path.go", "path.go", "/abs"},
		{"trailing/", "", "trailing"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			t.Parallel()
			if got := fileBase(tt.path); got != tt.wantBase {
				t.Errorf("fileBase(%q) = %q, want %q", tt.path, got, tt.wantBase)
			}
			if got := pathDir(tt.path); got != tt.wantDir {
				t.Errorf("pathDir(%q) = %q, want %q", tt.path, got, tt.wantDir)
			}
		})
	}
}

func TestIsCommonWord(t *testing.T) {
	t.Parallel()

	for _, word := range []string{"this", "ERR", "Ctx", "result", "temp"} {
		if !isCommonWord(word) {
			t.Errorf("isCommonWord(%q) = false, want true", word)
		}
	}
	for _, word := range []string{"Router", "Handle", "Server", "errors"} {
		if isCommonWord(word) {
			t.Errorf("isCommonWord(%q) = true, want false", word)
		}
	}
}

func TestGoImportBlocks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		text string
		want []string
	}{
		{
			name: "no import block",
			text: "package main\n\nimport \"fmt\"\n",
			want: nil,
		},
		{
			name: "one closed block",
			text: "import (\n\t\"fmt\"\n)\n\nfunc main() {}\n",
			want: []string{"\n\t\"fmt\""},
		},
		{
			name: "unterminated block runs to end of text",
			text: "import (\n\t\"fmt\"\n",
			want: []string{"\n\t\"fmt\"\n"},
		},
		{
			name: "two blocks",
			text: "import (\n\t\"fmt\"\n)\nimport (\n\t\"os\"\n)\n",
			want: []string{"\n\t\"fmt\"", "\n\t\"os\""},
		},
		{
			name: "a closing paren mid-line does not end the block",
			text: "import (\n\t\"fmt\" // f(x)\n\t\"os\"\n)\n",
			want: []string{"\n\t\"fmt\" // f(x)\n\t\"os\""},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := goImportBlocks(tt.text)
			if len(got) != len(tt.want) {
				t.Fatalf("goImportBlocks(%q) = %q, want %q", tt.text, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("block %d = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}
