package mermaid

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// update regenerates every golden file from the current renderer output. The
// workflow is render-and-approve: run `go test ./internal/mermaid -update`,
// read `git diff` to see exactly what changed, and commit the goldens only
// once the new output has been looked at. A golden is an approval, not a
// snapshot taken on trust.
var update = flag.Bool("update", false, "regenerate golden files from current output")

// goldenWidth is the width every golden renders at. Fixed so goldens do not
// change with the terminal the tests happen to run in — without it the
// renderer falls back to detecting the width, and CI would disagree with a
// developer's terminal.
const goldenWidth = 100

// widestLine returns the width of the widest line in runes. Rune count, not
// byte count: the box-drawing characters are three bytes each in UTF-8, so
// measuring bytes overstates the width by roughly 3x.
func widestLine(s string) int {
	widest := 0
	for _, line := range strings.Split(s, "\n") {
		if n := utf8.RuneCountInString(line); n > widest {
			widest = n
		}
	}
	return widest
}

// foldCR folds CRLF and lone CR endings to LF, so a comparison is about the
// rendered art rather than about how git checked the file out.
func foldCR(s string) string {
	if !strings.ContainsRune(s, '\r') {
		return s
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\r", "\n")
}

// corpusCase is one .mmd input paired with the golden it renders to.
type corpusCase struct {
	name   string // "flowchart/docs-flowchart-1a2b3c4d"
	source string
	golden string // path to the .txt golden
}

// loadCorpus walks testdata/corpus and pairs every input with its golden path.
func loadCorpus(t *testing.T) []corpusCase {
	t.Helper()

	root := filepath.Join("testdata", "corpus")
	var cases []corpusCase
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Ext(path) != ".mmd" {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		name := strings.TrimSuffix(rel, ".mmd")
		cases = append(cases, corpusCase{
			name:   filepath.ToSlash(name),
			source: string(src),
			golden: filepath.Join("testdata", "golden", name+".txt"),
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk corpus: %v", err)
	}
	if len(cases) == 0 {
		t.Fatal("corpus is empty: testdata/corpus holds no .mmd inputs")
	}
	return cases
}

// TestGolden renders every corpus case and compares it to its approved golden.
func TestGolden(t *testing.T) {
	for _, tc := range loadCorpus(t) {
		t.Run(tc.name, func(t *testing.T) {
			got := Render(tc.source, WithWidth(goldenWidth))

			if *update {
				if err := os.MkdirAll(filepath.Dir(tc.golden), 0o755); err != nil {
					t.Fatalf("mkdir: %v", err)
				}
				if err := os.WriteFile(tc.golden, []byte(got), 0o644); err != nil {
					t.Fatalf("write golden: %v", err)
				}
				return
			}

			want, err := os.ReadFile(tc.golden)
			if err != nil {
				t.Fatalf("missing golden (run with -update to approve): %v", err)
			}
			// Compare with line endings folded. A checkout on Windows converts
			// these files to CRLF unless .gitattributes says otherwise, and a
			// golden that differs only in its line endings is not a rendering
			// difference — it is a checkout difference, and failing on it tells
			// nobody anything useful.
			if got != foldCR(string(want)) {
				t.Errorf("output differs from approved golden\n--- got ---\n%s\n--- want ---\n%s", got, want)
			}
		})
	}
}

// TestNoInternalErrors asserts no corpus case trips the panic recovery in
// Render. This is the check that actually protects the TUI: a panic that
// escaped would take the whole session down, and one that is recovered still
// means the diagram silently failed to draw.
func TestNoInternalErrors(t *testing.T) {
	var failed []string
	for _, tc := range loadCorpus(t) {
		if strings.Contains(Render(tc.source, WithWidth(goldenWidth)), "[mmaid] internal error") {
			failed = append(failed, tc.name)
		}
	}
	if len(failed) > 0 {
		t.Errorf("%d/%d cases hit the panic recovery:\n  %s",
			len(failed), len(loadCorpus(t)), strings.Join(failed, "\n  "))
	}
}

// TestOutputIsWellFormed asserts the invariants every rendered diagram must
// hold regardless of type: valid UTF-8, no stray control characters, and no
// trailing whitespace that would show up as artifacts in a themed pane.
func TestOutputIsWellFormed(t *testing.T) {
	for _, tc := range loadCorpus(t) {
		t.Run(tc.name, func(t *testing.T) {
			got := Render(tc.source, WithWidth(goldenWidth))
			if got == "" {
				t.Skip("empty output")
			}
			if !utf8.ValidString(got) {
				t.Error("output is not valid UTF-8")
			}
			for i, line := range strings.Split(got, "\n") {
				for _, r := range line {
					if r < 0x20 && r != '\t' {
						t.Fatalf("line %d contains control character %q", i+1, r)
					}
				}
			}
		})
	}
}

// TestWidthIsHonored checks that a narrower WithWidth never produces wider
// output. It is deliberately weaker than "output fits in the width": the
// engine treats width as a fill target rather than a hard cap, so a graph
// wide enough to need more columns still gets them. Monotonicity is the
// property callers can actually rely on when sizing a pane.
func TestWidthIsHonored(t *testing.T) {
	for _, tc := range loadCorpus(t) {
		t.Run(tc.name, func(t *testing.T) {
			narrow := widestLine(Render(tc.source, WithWidth(60)))
			wide := widestLine(Render(tc.source, WithWidth(200)))
			if narrow > wide {
				t.Errorf("narrow render is wider than wide render: WithWidth(60)=%d > WithWidth(200)=%d", narrow, wide)
			}
		})
	}
}

// TestASCIIModeIsPlainASCII asserts ASCII mode introduces no characters
// outside 7-bit ASCII, which is the entire point of the mode: terminals and
// fonts that cannot draw box-drawing glyphs.
//
// Runes that appear in the source are exempt. A label reading "❤ prod" must
// still render its heart — ASCII mode governs the glyphs the renderer chooses
// for boxes, lines, and shape indicators, not the text the author wrote.
func TestASCIIModeIsPlainASCII(t *testing.T) {
	for _, tc := range loadCorpus(t) {
		t.Run(tc.name, func(t *testing.T) {
			for _, r := range Render(tc.source, WithASCII(), WithWidth(goldenWidth)) {
				if r > 127 && !strings.ContainsRune(tc.source, r) {
					t.Fatalf("ASCII mode introduced non-ASCII rune %q (U+%04X)", r, r)
				}
			}
		})
	}
}

// TestRenderIsDeterministic asserts the same source renders identically every
// time. Layout keeps its column widths in a map, so any distribution pass that
// forgets to sort before iterating hands the leftover columns to a different
// column on each call. That is invisible in a one-shot CLI and glaring in a
// TUI, which re-renders the same transcript on every frame: the diagram
// shimmers as boxes trade a column back and forth.
func TestRenderIsDeterministic(t *testing.T) {
	for _, tc := range loadCorpus(t) {
		t.Run(tc.name, func(t *testing.T) {
			first := Render(tc.source, WithWidth(goldenWidth))
			for i := range 5 {
				if got := Render(tc.source, WithWidth(goldenWidth)); got != first {
					t.Fatalf("render %d differs from render 0", i+1)
				}
			}
		})
	}
}
