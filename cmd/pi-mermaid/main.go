// Command pi-mermaid renders Mermaid diagrams as terminal art.
//
// It exists to make internal/mermaid quick to eyeball without starting the
// TUI: pipe a diagram in, see what the chat pane would draw. The -check mode
// batch-renders a directory of .mmd files and reports which ones fail, which
// is how the golden corpus gets triaged after an engine change.
//
//	pi-mermaid diagram.mmd
//	echo 'graph LR; A-->B' | pi-mermaid -w 60
//	pi-mermaid -check internal/mermaid/testdata/corpus
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/dimetron/pi-go/internal/mermaid"
)

func main() {
	var (
		width     = flag.Int("w", 0, "render width in columns (0 = detect from terminal)")
		ascii     = flag.Bool("a", false, "ASCII-only output")
		theme     = flag.String("t", "", "color theme name (empty = no color)")
		check     = flag.String("check", "", "batch-render every .mmd under this directory and report failures")
		quiet     = flag.Bool("q", false, "with -check, print only the summary")
		showUsage = flag.Bool("h", false, "show usage")
	)
	flag.Usage = func() {
		fmt.Fprint(os.Stderr, `pi-mermaid — render Mermaid diagrams as terminal art

USAGE
  pi-mermaid [flags] [file]      render a file, or stdin when no file is given
  pi-mermaid -check DIR          batch-render every .mmd under DIR

FLAGS
`)
		flag.PrintDefaults()
	}
	flag.Parse()

	if *showUsage {
		flag.Usage()
		os.Exit(0)
	}

	if *check != "" {
		os.Exit(runCheck(*check, *width, *ascii, *quiet))
	}

	source, err := readSource(flag.Arg(0))
	if err != nil {
		fmt.Fprintln(os.Stderr, "pi-mermaid:", err)
		os.Exit(1)
	}

	out := mermaid.Render(source, options(*width, *ascii, *theme)...)
	fmt.Println(out)

	// A render that tripped the panic recovery is a failure, not output, and
	// callers scripting this need a non-zero status to notice.
	if strings.Contains(out, "[mmaid] internal error") {
		os.Exit(1)
	}
}

// options builds the render options shared by both modes.
func options(width int, ascii bool, theme string) []mermaid.Option {
	opts := []mermaid.Option{}
	if width > 0 {
		opts = append(opts, mermaid.WithWidth(width))
	}
	if ascii {
		opts = append(opts, mermaid.WithASCII())
	}
	if theme != "" {
		opts = append(opts, mermaid.WithTheme(theme))
	}
	return opts
}

// readSource reads the named file, or stdin when name is empty or "-".
func readSource(name string) (string, error) {
	if name == "" || name == "-" {
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", fmt.Errorf("read stdin: %w", err)
		}
		return string(b), nil
	}
	b, err := os.ReadFile(name)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// checkResult is one file's outcome in batch mode.
type checkResult struct {
	path  string
	cols  int
	rows  int
	fail  bool
	note  string
	empty bool
}

// runCheck renders every .mmd under dir and reports what failed. It returns
// the process exit code: non-zero if anything tripped the panic recovery.
func runCheck(dir string, width int, ascii bool, quiet bool) int {
	var results []checkResult
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Ext(path) != ".mmd" {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		out := mermaid.Render(string(src), options(width, ascii, "")...)
		r := checkResult{path: path, cols: widest(out), rows: strings.Count(out, "\n") + 1}
		switch {
		case strings.Contains(out, "[mmaid] internal error"):
			r.fail, r.note = true, "panic recovered"
		case strings.TrimSpace(out) == "":
			r.empty, r.note = true, "empty output"
		case isDiagnostic(out):
			r.empty, r.note = true, strings.TrimSpace(firstLine(out))
		}
		results = append(results, r)
		return nil
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "pi-mermaid:", err)
		return 1
	}
	if len(results) == 0 {
		fmt.Fprintf(os.Stderr, "pi-mermaid: no .mmd files under %s\n", dir)
		return 1
	}

	sort.Slice(results, func(i, j int) bool { return results[i].path < results[j].path })

	var failed, degraded, widest int
	for _, r := range results {
		switch {
		case r.fail:
			failed++
		case r.empty:
			degraded++
		}
		if r.cols > widest {
			widest = r.cols
		}
		if !quiet && (r.fail || r.empty) {
			fmt.Printf("%-8s %s: %s\n", statusWord(r), rel(dir, r.path), r.note)
		}
	}

	fmt.Printf("\n%d files: %d rendered, %d degraded, %d failed (widest %d cols)\n",
		len(results), len(results)-failed-degraded, degraded, failed, widest)
	if failed > 0 {
		return 1
	}
	return 0
}

func statusWord(r checkResult) string {
	if r.fail {
		return "FAIL"
	}
	return "degraded"
}

func rel(dir, path string) string {
	if r, err := filepath.Rel(dir, path); err == nil {
		return r
	}
	return path
}

// isDiagnostic reports whether output is one of the renderers' "[type] reason"
// messages rather than a drawn diagram — the shape a parser gap produces.
func isDiagnostic(out string) bool {
	line := strings.TrimSpace(firstLine(out))
	return strings.HasPrefix(line, "[") && strings.Contains(line, "] ") && !strings.Contains(out, "\n\n")
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// widest returns the widest line in runes. Counting bytes would overstate the
// width roughly threefold: the box-drawing glyphs are three bytes each.
func widest(s string) int {
	w := 0
	for _, line := range strings.Split(s, "\n") {
		if n := utf8.RuneCountInString(line); n > w {
			w = n
		}
	}
	return w
}
