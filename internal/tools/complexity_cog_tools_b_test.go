package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
)

// The expectations in this file were captured by running the same inputs
// against the pre-refactor source (git archive of the parent commit into a
// scratch tree), so they pin the observable output of the originals rather than
// agreeing with the flattened code about what it happens to produce.

// cogBSearchLines builds n lines from format, substituting the line index.
func cogBSearchLines(n int, format string) string {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = fmt.Sprintf(format, i)
	}
	return strings.Join(lines, "\n")
}

// cogBSearchLinesMod builds n lines from format, substituting i%mod then i so
// the results spread across mod distinct files.
func cogBSearchLinesMod(n, mod int, format string) string {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = fmt.Sprintf(format, i%mod, i)
	}
	return strings.Join(lines, "\n")
}

// cogBBlankAlternating builds n lines where every even index is blank, so the
// grouper has to drop empty lines.
func cogBBlankAlternating(n int) string {
	lines := make([]string, n)
	for i := range lines {
		if i%2 != 0 {
			lines[i] = fmt.Sprintf("b.go:%d:v", i)
		}
	}
	return strings.Join(lines, "\n")
}

func TestCogBGroupSearchOutputGolden(t *testing.T) {
	cases := []struct {
		name   string
		in     string
		total  int
		per    int
		wantOK bool
		want   string
	}{
		{
			name:   "short",
			in:     cogBSearchLines(5, "a.go:%d:hit"),
			total:  100,
			per:    20,
			wantOK: false,
			want:   "a.go:0:hit\na.go:1:hit\na.go:2:hit\na.go:3:hit\na.go:4:hit",
		},
		{
			name:   "nocolon",
			in:     cogBSearchLines(25, "plainline%d"),
			total:  100,
			per:    20,
			wantOK: false,
			want:   "plainline0\nplainline1\nplainline2\nplainline3\nplainline4\nplainline5\nplainline6\nplainline7\nplainline8\nplainline9\nplainline10\nplainline11\nplainline12\nplainline13\nplainline14\nplainline15\nplainline16\nplainline17\nplainline18\nplainline19\nplainline20\nplainline21\nplainline22\nplainline23\nplainline24",
		},
		{
			name:   "basic",
			in:     cogBSearchLinesMod(30, 3, "file%d.go:%d:some match text here"),
			total:  100,
			per:    20,
			wantOK: true,
			want:   "file0.go (10 matches):\n  0:some match text here\n  3:some match text here\n  6:some match text here\n  9:some match text here\n  12:some match text here\n  15:some match text here\n  18:some match text here\n  21:some match text here\n  24:some match text here\n  27:some match text here\nfile1.go (10 matches):\n  1:some match text here\n  4:some match text here\n  7:some match text here\n  10:some match text here\n  13:some match text here\n  16:some match text here\n  19:some match text here\n  22:some match text here\n  25:some match text here\n  28:some match text here\nfile2.go (10 matches):\n  2:some match text here\n  5:some match text here\n  8:some match text here\n  11:some match text here\n  14:some match text here\n  17:some match text here\n  20:some match text here\n  23:some match text here\n  26:some match text here\n  29:some match text here\n",
		},
		{
			name:   "perfile",
			in:     cogBSearchLinesMod(30, 2, "f%d.go:%d:xx"),
			total:  100,
			per:    3,
			wantOK: true,
			want:   "f0.go (15 matches):\n  0:xx\n  2:xx\n  4:xx\n  ... and 12 more matches\nf1.go (15 matches):\n  1:xx\n  3:xx\n  5:xx\n  ... and 12 more matches\n",
		},
		{
			name:   "total",
			in:     cogBSearchLinesMod(40, 4, "f%d.go:%d:yy"),
			total:  5,
			per:    3,
			wantOK: true,
			want:   "f0.go (10 matches):\n  0:yy\n  4:yy\n  8:yy\n  ... and 7 more matches\nf1.go (10 matches):\n  1:yy\n  5:yy\n\n... (5 total matches shown, limited to 5)\n",
		},
		{
			name:   "totalzero",
			in:     cogBSearchLines(25, "z.go:%d:q"),
			total:  0,
			per:    3,
			wantOK: true,
			want:   "z.go (25 matches):\n\n... (0 total matches shown, limited to 0)\n",
		},
		{
			name:   "blanks",
			in:     cogBBlankAlternating(25),
			total:  100,
			per:    20,
			wantOK: true,
			want:   "b.go (12 matches):\n  1:v\n  3:v\n  5:v\n  7:v\n  9:v\n  11:v\n  13:v\n  15:v\n  17:v\n  19:v\n  21:v\n  23:v\n",
		},
		{
			name:   "nolinenum",
			in:     cogBSearchLines(22, "c.go:content %d"),
			total:  100,
			per:    20,
			wantOK: true,
			want:   "c.go (22 matches):\n  content 0\n  content 1\n  content 2\n  content 3\n  content 4\n  content 5\n  content 6\n  content 7\n  content 8\n  content 9\n  content 10\n  content 11\n  content 12\n  content 13\n  content 14\n  content 15\n  content 16\n  content 17\n  content 18\n  content 19\n  ... and 2 more matches\n",
		},
		{
			name:   "grows",
			in:     cogBSearchLines(21, "d.go:%d:"),
			total:  100,
			per:    20,
			wantOK: true,
			want:   "d.go (21 matches):\n  0:\n  1:\n  2:\n  3:\n  4:\n  5:\n  6:\n  7:\n  8:\n  9:\n  10:\n  11:\n  12:\n  13:\n  14:\n  15:\n  16:\n  17:\n  18:\n  19:\n  ... and 1 more matches\n",
		},
		{
			name:   "onematch",
			in:     cogBSearchLines(20, "e%d.go:1:aaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
			total:  100,
			per:    20,
			wantOK: false,
			want:   "e0.go:1:aaaaaaaaaaaaaaaaaaaaaaaaaaaa\ne1.go:1:aaaaaaaaaaaaaaaaaaaaaaaaaaaa\ne2.go:1:aaaaaaaaaaaaaaaaaaaaaaaaaaaa\ne3.go:1:aaaaaaaaaaaaaaaaaaaaaaaaaaaa\ne4.go:1:aaaaaaaaaaaaaaaaaaaaaaaaaaaa\ne5.go:1:aaaaaaaaaaaaaaaaaaaaaaaaaaaa\ne6.go:1:aaaaaaaaaaaaaaaaaaaaaaaaaaaa\ne7.go:1:aaaaaaaaaaaaaaaaaaaaaaaaaaaa\ne8.go:1:aaaaaaaaaaaaaaaaaaaaaaaaaaaa\ne9.go:1:aaaaaaaaaaaaaaaaaaaaaaaaaaaa\ne10.go:1:aaaaaaaaaaaaaaaaaaaaaaaaaaaa\ne11.go:1:aaaaaaaaaaaaaaaaaaaaaaaaaaaa\ne12.go:1:aaaaaaaaaaaaaaaaaaaaaaaaaaaa\ne13.go:1:aaaaaaaaaaaaaaaaaaaaaaaaaaaa\ne14.go:1:aaaaaaaaaaaaaaaaaaaaaaaaaaaa\ne15.go:1:aaaaaaaaaaaaaaaaaaaaaaaaaaaa\ne16.go:1:aaaaaaaaaaaaaaaaaaaaaaaaaaaa\ne17.go:1:aaaaaaaaaaaaaaaaaaaaaaaaaaaa\ne18.go:1:aaaaaaaaaaaaaaaaaaaaaaaaaaaa\ne19.go:1:aaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultCompactorConfig()
			cfg.MaxSearchTotal = tt.total
			cfg.MaxSearchPerFile = tt.per

			got, ok := groupSearchOutput(tt.in, cfg)
			if ok != tt.wantOK {
				t.Errorf("ok = %v, want %v", ok, tt.wantOK)
			}
			if got != tt.want {
				t.Errorf("output mismatch\n got: %q\nwant: %q", got, tt.want)
			}
		})
	}
}

func TestCogBGroupSearchLinesByFile(t *testing.T) {
	byFile, order := groupSearchLinesByFile([]string{
		"b.go:1:x", "", "nocolon", "a.go:2:y", "b.go:3:z", ":leading colon",
	})

	if want := []string{"b.go", "a.go", ""}; !cogBEqualStrings(order, want) {
		t.Errorf("order = %v, want %v", order, want)
	}
	if len(byFile["b.go"]) != 2 {
		t.Errorf("b.go matches = %d, want 2", len(byFile["b.go"]))
	}
	if len(byFile[""]) != 1 {
		t.Errorf("empty-file matches = %d, want 1", len(byFile[""]))
	}
	if _, ok := byFile["nocolon"]; ok {
		t.Error("line without a colon was grouped")
	}
}

func cogBEqualStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestCogBStripSearchLinePrefix(t *testing.T) {
	cases := []struct{ in, want string }{
		{"a.go:12:hit", "12:hit"},
		{"nocolon", "nocolon"},
		{":", ""},
		{"", ""},
	}
	for _, tt := range cases {
		if got := stripSearchLinePrefix(tt.in); got != tt.want {
			t.Errorf("stripSearchLinePrefix(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestCogBWriteSearchFileGroup(t *testing.T) {
	cfg := DefaultCompactorConfig()
	cfg.MaxSearchTotal = 4
	cfg.MaxSearchPerFile = 2

	var b strings.Builder
	total := writeSearchFileGroup(&b, "x.go", []string{"x.go:1:a", "x.go:2:b", "x.go:3:c"}, 0, cfg)
	if total != 2 {
		t.Errorf("total = %d, want 2", total)
	}
	want := "x.go (3 matches):\n  1:a\n  2:b\n  ... and 1 more matches\n"
	if b.String() != want {
		t.Errorf("got %q, want %q", b.String(), want)
	}

	// Already at the running total: header only, no matches, total unchanged.
	var b2 strings.Builder
	total2 := writeSearchFileGroup(&b2, "y.go", []string{"y.go:1:a"}, 4, cfg)
	if total2 != 4 {
		t.Errorf("total2 = %d, want 4", total2)
	}
	if b2.String() != "y.go (1 matches):\n" {
		t.Errorf("got %q, want header only", b2.String())
	}
}

// cogBSourceLong builds a >50-line file mixing doc comments, block comments,
// hash comments, trailing comments and blank runs.
func cogBSourceLong() string {
	body := cogBSourceBody()
	var lines []string
	for len(lines) < 60 {
		lines = append(lines, body...)
	}
	return strings.Join(lines, "\n")
}

// cogBSourceShort returns the same body once, which is under the 50-line floor.
func cogBSourceShort() string {
	return strings.Join(cogBSourceBody(), "\n")
}

func cogBSourceBody() []string {
	return []string{
		"package main",
		"",
		"",
		"",
		"// doc comment line",
		"// second doc line",
		"import \"fmt\"",
		"",
		"/* block start",
		"   block middle",
		"   block end */",
		"func main() {",
		"\t// inner comment",
		"\tfmt.Println(\"hi\") // trailing",
		"",
		"",
		"\t# not go but hash",
		"\t/* one line block */",
		"\tx := 1",
		"}",
	}
}

// cogBSourceUnterminated builds a file whose block comment never closes, so the
// in-block state stays set for the rest of the scan.
func cogBSourceUnterminated() string {
	var lines []string
	for len(lines) < 55 {
		lines = append(lines, "/* never closed", "still inside", "more")
	}
	return strings.Join(lines, "\n")
}

// cogBSourceNoComments builds a file with nothing the filter can remove.
func cogBSourceNoComments() string {
	lines := make([]string, 60)
	for i := range lines {
		lines[i] = fmt.Sprintf("line%d", i)
	}
	return strings.Join(lines, "\n")
}

func TestCogBFilterSourceCodeGolden(t *testing.T) {
	cases := []struct {
		name   string
		in     string
		level  string
		wantOK bool
		want   string
	}{
		{
			name:   "long-aggressive",
			in:     cogBSourceLong(),
			level:  "aggressive",
			wantOK: true,
			want:   "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(\"hi\") // trailing\n\n\tx := 1\n}\npackage main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(\"hi\") // trailing\n\n\tx := 1\n}\npackage main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(\"hi\") // trailing\n\n\tx := 1\n}",
		},
		{
			name:   "short-aggressive",
			in:     cogBSourceShort(),
			level:  "aggressive",
			wantOK: false,
			want:   "package main\n\n\n\n// doc comment line\n// second doc line\nimport \"fmt\"\n\n/* block start\n   block middle\n   block end */\nfunc main() {\n\t// inner comment\n\tfmt.Println(\"hi\") // trailing\n\n\n\t# not go but hash\n\t/* one line block */\n\tx := 1\n}",
		},
		{
			name:   "long-minimal",
			in:     cogBSourceLong(),
			level:  "minimal",
			wantOK: true,
			want:   "package main\n\nimport \"fmt\"\n\n/* block start\n   block middle\n   block end */\nfunc main() {\n\tfmt.Println(\"hi\") // trailing\n\n\t# not go but hash\n\t/* one line block */\n\tx := 1\n}\npackage main\n\nimport \"fmt\"\n\n/* block start\n   block middle\n   block end */\nfunc main() {\n\tfmt.Println(\"hi\") // trailing\n\n\t# not go but hash\n\t/* one line block */\n\tx := 1\n}\npackage main\n\nimport \"fmt\"\n\n/* block start\n   block middle\n   block end */\nfunc main() {\n\tfmt.Println(\"hi\") // trailing\n\n\t# not go but hash\n\t/* one line block */\n\tx := 1\n}",
		},
		{
			name:   "short-minimal",
			in:     cogBSourceShort(),
			level:  "minimal",
			wantOK: false,
			want:   "package main\n\n\n\n// doc comment line\n// second doc line\nimport \"fmt\"\n\n/* block start\n   block middle\n   block end */\nfunc main() {\n\t// inner comment\n\tfmt.Println(\"hi\") // trailing\n\n\n\t# not go but hash\n\t/* one line block */\n\tx := 1\n}",
		},
		{
			name:   "long-moderate",
			in:     cogBSourceLong(),
			level:  "moderate",
			wantOK: true,
			want:   "package main\n\n// doc comment line\n// second doc line\nimport \"fmt\"\n\n/* block start\n   block middle\n   block end */\nfunc main() {\n\t// inner comment\n\tfmt.Println(\"hi\") // trailing\n\n\t# not go but hash\n\t/* one line block */\n\tx := 1\n}\npackage main\n\n// doc comment line\n// second doc line\nimport \"fmt\"\n\n/* block start\n   block middle\n   block end */\nfunc main() {\n\t// inner comment\n\tfmt.Println(\"hi\") // trailing\n\n\t# not go but hash\n\t/* one line block */\n\tx := 1\n}\npackage main\n\n// doc comment line\n// second doc line\nimport \"fmt\"\n\n/* block start\n   block middle\n   block end */\nfunc main() {\n\t// inner comment\n\tfmt.Println(\"hi\") // trailing\n\n\t# not go but hash\n\t/* one line block */\n\tx := 1\n}",
		},
		{
			name:   "short-moderate",
			in:     cogBSourceShort(),
			level:  "moderate",
			wantOK: false,
			want:   "package main\n\n\n\n// doc comment line\n// second doc line\nimport \"fmt\"\n\n/* block start\n   block middle\n   block end */\nfunc main() {\n\t// inner comment\n\tfmt.Println(\"hi\") // trailing\n\n\n\t# not go but hash\n\t/* one line block */\n\tx := 1\n}",
		},
		{
			name:   "long-",
			in:     cogBSourceLong(),
			level:  "",
			wantOK: true,
			want:   "package main\n\n// doc comment line\n// second doc line\nimport \"fmt\"\n\n/* block start\n   block middle\n   block end */\nfunc main() {\n\t// inner comment\n\tfmt.Println(\"hi\") // trailing\n\n\t# not go but hash\n\t/* one line block */\n\tx := 1\n}\npackage main\n\n// doc comment line\n// second doc line\nimport \"fmt\"\n\n/* block start\n   block middle\n   block end */\nfunc main() {\n\t// inner comment\n\tfmt.Println(\"hi\") // trailing\n\n\t# not go but hash\n\t/* one line block */\n\tx := 1\n}\npackage main\n\n// doc comment line\n// second doc line\nimport \"fmt\"\n\n/* block start\n   block middle\n   block end */\nfunc main() {\n\t// inner comment\n\tfmt.Println(\"hi\") // trailing\n\n\t# not go but hash\n\t/* one line block */\n\tx := 1\n}",
		},
		{
			name:   "short-",
			in:     cogBSourceShort(),
			level:  "",
			wantOK: false,
			want:   "package main\n\n\n\n// doc comment line\n// second doc line\nimport \"fmt\"\n\n/* block start\n   block middle\n   block end */\nfunc main() {\n\t// inner comment\n\tfmt.Println(\"hi\") // trailing\n\n\n\t# not go but hash\n\t/* one line block */\n\tx := 1\n}",
		},
		{
			name:   "long-none",
			in:     cogBSourceLong(),
			level:  "none",
			wantOK: true,
			want:   "package main\n\n// doc comment line\n// second doc line\nimport \"fmt\"\n\n/* block start\n   block middle\n   block end */\nfunc main() {\n\t// inner comment\n\tfmt.Println(\"hi\") // trailing\n\n\t# not go but hash\n\t/* one line block */\n\tx := 1\n}\npackage main\n\n// doc comment line\n// second doc line\nimport \"fmt\"\n\n/* block start\n   block middle\n   block end */\nfunc main() {\n\t// inner comment\n\tfmt.Println(\"hi\") // trailing\n\n\t# not go but hash\n\t/* one line block */\n\tx := 1\n}\npackage main\n\n// doc comment line\n// second doc line\nimport \"fmt\"\n\n/* block start\n   block middle\n   block end */\nfunc main() {\n\t// inner comment\n\tfmt.Println(\"hi\") // trailing\n\n\t# not go but hash\n\t/* one line block */\n\tx := 1\n}",
		},
		{
			name:   "short-none",
			in:     cogBSourceShort(),
			level:  "none",
			wantOK: false,
			want:   "package main\n\n\n\n// doc comment line\n// second doc line\nimport \"fmt\"\n\n/* block start\n   block middle\n   block end */\nfunc main() {\n\t// inner comment\n\tfmt.Println(\"hi\") // trailing\n\n\n\t# not go but hash\n\t/* one line block */\n\tx := 1\n}",
		},
		{
			name:   "unterm-aggressive",
			in:     cogBSourceUnterminated(),
			level:  "aggressive",
			wantOK: true,
			want:   "",
		},
		{
			name:   "unterm-minimal",
			in:     cogBSourceUnterminated(),
			level:  "minimal",
			wantOK: false,
			want:   "/* never closed\nstill inside\nmore\n/* never closed\nstill inside\nmore\n/* never closed\nstill inside\nmore\n/* never closed\nstill inside\nmore\n/* never closed\nstill inside\nmore\n/* never closed\nstill inside\nmore\n/* never closed\nstill inside\nmore\n/* never closed\nstill inside\nmore\n/* never closed\nstill inside\nmore\n/* never closed\nstill inside\nmore\n/* never closed\nstill inside\nmore\n/* never closed\nstill inside\nmore\n/* never closed\nstill inside\nmore\n/* never closed\nstill inside\nmore\n/* never closed\nstill inside\nmore\n/* never closed\nstill inside\nmore\n/* never closed\nstill inside\nmore\n/* never closed\nstill inside\nmore\n/* never closed\nstill inside\nmore",
		},
		{
			name:   "unterm-moderate",
			in:     cogBSourceUnterminated(),
			level:  "moderate",
			wantOK: false,
			want:   "/* never closed\nstill inside\nmore\n/* never closed\nstill inside\nmore\n/* never closed\nstill inside\nmore\n/* never closed\nstill inside\nmore\n/* never closed\nstill inside\nmore\n/* never closed\nstill inside\nmore\n/* never closed\nstill inside\nmore\n/* never closed\nstill inside\nmore\n/* never closed\nstill inside\nmore\n/* never closed\nstill inside\nmore\n/* never closed\nstill inside\nmore\n/* never closed\nstill inside\nmore\n/* never closed\nstill inside\nmore\n/* never closed\nstill inside\nmore\n/* never closed\nstill inside\nmore\n/* never closed\nstill inside\nmore\n/* never closed\nstill inside\nmore\n/* never closed\nstill inside\nmore\n/* never closed\nstill inside\nmore",
		},
		{
			name:   "blank-aggressive",
			in:     strings.Repeat("\n", 60),
			level:  "aggressive",
			wantOK: true,
			want:   "",
		},
		{
			name:   "blank-minimal",
			in:     strings.Repeat("\n", 60),
			level:  "minimal",
			wantOK: true,
			want:   "",
		},
		{
			name:   "blank-moderate",
			in:     strings.Repeat("\n", 60),
			level:  "moderate",
			wantOK: true,
			want:   "",
		},
		{
			name:   "keep-aggressive",
			in:     cogBSourceNoComments(),
			level:  "aggressive",
			wantOK: false,
			want:   "line0\nline1\nline2\nline3\nline4\nline5\nline6\nline7\nline8\nline9\nline10\nline11\nline12\nline13\nline14\nline15\nline16\nline17\nline18\nline19\nline20\nline21\nline22\nline23\nline24\nline25\nline26\nline27\nline28\nline29\nline30\nline31\nline32\nline33\nline34\nline35\nline36\nline37\nline38\nline39\nline40\nline41\nline42\nline43\nline44\nline45\nline46\nline47\nline48\nline49\nline50\nline51\nline52\nline53\nline54\nline55\nline56\nline57\nline58\nline59",
		},
		{
			name:   "keep-minimal",
			in:     cogBSourceNoComments(),
			level:  "minimal",
			wantOK: false,
			want:   "line0\nline1\nline2\nline3\nline4\nline5\nline6\nline7\nline8\nline9\nline10\nline11\nline12\nline13\nline14\nline15\nline16\nline17\nline18\nline19\nline20\nline21\nline22\nline23\nline24\nline25\nline26\nline27\nline28\nline29\nline30\nline31\nline32\nline33\nline34\nline35\nline36\nline37\nline38\nline39\nline40\nline41\nline42\nline43\nline44\nline45\nline46\nline47\nline48\nline49\nline50\nline51\nline52\nline53\nline54\nline55\nline56\nline57\nline58\nline59",
		},
		{
			name:   "keep-moderate",
			in:     cogBSourceNoComments(),
			level:  "moderate",
			wantOK: false,
			want:   "line0\nline1\nline2\nline3\nline4\nline5\nline6\nline7\nline8\nline9\nline10\nline11\nline12\nline13\nline14\nline15\nline16\nline17\nline18\nline19\nline20\nline21\nline22\nline23\nline24\nline25\nline26\nline27\nline28\nline29\nline30\nline31\nline32\nline33\nline34\nline35\nline36\nline37\nline38\nline39\nline40\nline41\nline42\nline43\nline44\nline45\nline46\nline47\nline48\nline49\nline50\nline51\nline52\nline53\nline54\nline55\nline56\nline57\nline58\nline59",
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := filterSourceCode(tt.in, tt.level)
			if ok != tt.wantOK {
				t.Errorf("ok = %v, want %v", ok, tt.wantOK)
			}
			if got != tt.want {
				t.Errorf("output mismatch\n got: %q\nwant: %q", got, tt.want)
			}
		})
	}
}

func TestCogBSourceLineFilterState(t *testing.T) {
	// Blank-run collapsing keeps the first blank of each run and resets on the
	// next non-blank line.
	f := sourceLineFilter{level: "moderate"}
	seq := []struct {
		line string
		want bool
	}{
		{"", true},
		{"", false},
		{"  ", false},
		{"code", true},
		{"", true},
		{"", false},
	}
	for i, s := range seq {
		if got := f.keep(s.line); got != s.want {
			t.Errorf("step %d keep(%q) = %v, want %v", i, s.line, got, s.want)
		}
	}

	// Aggressive drops every line from /* until the closing */ inclusive.
	a := sourceLineFilter{level: "aggressive"}
	for _, line := range []string{"/* open", "inside", "close */"} {
		if a.keep(line) {
			t.Errorf("aggressive kept block line %q", line)
		}
	}
	if !a.keep("code") {
		t.Error("aggressive dropped code after the block closed")
	}
	if a.keep("// comment") || a.keep("# comment") {
		t.Error("aggressive kept a line comment")
	}

	// Minimal keeps block bodies and hash lines, drops only leading // lines.
	m := sourceLineFilter{level: "minimal"}
	if !m.keep("/* open") || !m.keep("inside") || !m.keep("close */") {
		t.Error("minimal dropped a block comment line")
	}
	if !m.keep("# comment") {
		t.Error("minimal dropped a hash comment")
	}
	if m.keep("// doc") {
		t.Error("minimal kept a // comment")
	}
	if !m.keep("code() // trailing") {
		t.Error("minimal dropped a trailing comment line")
	}
}

// cogBWriteTree creates files (path -> contents) under a fresh temp dir.
func cogBWriteTree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for p, c := range files {
		full := filepath.Join(dir, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// cogBFormatPatterns renders patterns in a sorted, stable form. Sorting is
// deliberate: loadGitignore merges its per-directory buckets by ranging over a
// map, so the order across directories is not defined.
func cogBFormatPatterns(pats []GitignorePattern) string {
	out := make([]string, 0, len(pats))
	for _, p := range pats {
		out = append(out, fmt.Sprintf("{%q,%q,%v,%v}", p.dir, p.pattern, p.isNeg, p.isDir))
	}
	sort.Strings(out)
	return "[" + strings.Join(out, " ") + "]"
}

func TestCogBLoadGitignoreGolden(t *testing.T) {
	trees := map[string]map[string]string{
		"root-only": {
			".gitignore": "# comment\n\n*.log\nbuild/\n!keep.log\nnode_modules\ndocs/**/*.tmp\n  \n!*.keep\n",
			"a.go":       "x",
		},
		"nested": {
			"sub/.gitignore": "*.bak\n!important.bak\n",
			"sub/a.go":       "x",
		},
		"none": {
			"a.go": "x",
		},
		"crlf": {
			".gitignore": "*.log\r\ntmp/\r\n",
		},
		// Pruned directories are never descended into, so their .gitignore
		// files contribute nothing.
		"pruned": {
			"node_modules/.gitignore": "*.everything\n",
			".git/.gitignore":         "*.gitinternal\n",
			"vendor/.gitignore":       "*.vendored\n",
			".gitignore":              "*.top\n",
		},
	}

	want := map[string]string{
		"crlf":      "[{\"\",\"*.log\",false,false} {\"\",\"tmp\",false,true}]",
		"nested":    "[{\"sub\",\"*.bak\",false,false} {\"sub\",\"important.bak\",true,false}]",
		"none":      "[]",
		"pruned":    "[{\"\",\"*.top\",false,false}]",
		"root-only": "[{\"\",\"  \",false,false} {\"\",\"*.keep\",true,false} {\"\",\"*.log\",false,false} {\"\",\"build\",false,true} {\"\",\"docs/*/*.tmp\",false,false} {\"\",\"keep.log\",true,false} {\"\",\"node_modules\",false,false}]",
	}

	for name, files := range trees {
		t.Run(name, func(t *testing.T) {
			sb, err := NewSandbox(cogBWriteTree(t, files))
			if err != nil {
				t.Fatal(err)
			}
			defer sb.Close()

			pats, err := sb.loadGitignore()
			if err != nil {
				t.Fatalf("loadGitignore: %v", err)
			}
			if got := cogBFormatPatterns(pats); got != want[name] {
				t.Errorf("patterns mismatch\n got: %s\nwant: %s", got, want[name])
			}
		})
	}
}

func TestCogBLoadGitignoreUnreadableFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("os.Chmod(0o000) does not make a file unreadable on Windows")
	}
	// A .gitignore that cannot be read contributes no patterns and does not
	// abort the walk: the sibling directory's file is still collected.
	dir := cogBWriteTree(t, map[string]string{
		".gitignore":     "*.unreadable\n",
		"sub/.gitignore": "*.readable\n",
	})
	if err := os.Chmod(filepath.Join(dir, ".gitignore"), 0o000); err != nil {
		t.Skipf("chmod unsupported: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(dir, ".gitignore"), 0o644) })

	sb, err := NewSandbox(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer sb.Close()

	pats, err := sb.loadGitignore()
	if err != nil {
		t.Fatalf("loadGitignore: %v", err)
	}
	for _, p := range pats {
		if p.pattern == "*.unreadable" {
			t.Fatal("unreadable .gitignore contributed a pattern")
		}
	}
	if got := cogBFormatPatterns(pats); got != `[{"sub","*.readable",false,false}]` {
		t.Errorf("patterns = %s", got)
	}
}

// cogBEntry is a minimal fs.DirEntry for exercising path-skipping decisions.
type cogBEntry struct {
	name  string
	isDir bool
}

func (e cogBEntry) Name() string               { return e.name }
func (e cogBEntry) IsDir() bool                { return e.isDir }
func (e cogBEntry) Type() os.FileMode          { return 0 }
func (e cogBEntry) Info() (os.FileInfo, error) { return nil, nil }

// cogBSkipPatterns is the pattern list the shouldSkipPath golden was captured
// against: a glob, a directory-only pattern, a negation shadowed by the glob,
// and a pattern scoped to a subdirectory.
func cogBSkipPatterns() []GitignorePattern {
	return []GitignorePattern{
		{dir: "", pattern: "*.log"},
		{dir: "", pattern: "build", isDir: true},
		{dir: "", pattern: "keep.log", isNeg: true},
		{dir: "sub", pattern: "*.bak"},
	}
}

func TestCogBShouldSkipPathGolden(t *testing.T) {
	cases := []struct {
		path  string
		isDir bool
		want  bool
	}{
		{path: "a.log", isDir: false, want: true},
		{path: "keep.log", isDir: false, want: true},
		{path: "build", isDir: true, want: true},
		{path: "build", isDir: false, want: false},
		{path: "a.go", isDir: false, want: false},
		{path: ".hidden", isDir: false, want: true},
		{path: ".pi-go", isDir: true, want: false},
		{path: ".claude", isDir: true, want: false},
		{path: ".cursor", isDir: true, want: false},
		{path: "node_modules", isDir: true, want: true},
		{path: "vendor", isDir: true, want: true},
		{path: "__pycache__", isDir: true, want: true},
		{path: "target", isDir: true, want: true},
		{path: ".", isDir: true, want: false},
		{path: "sub/x.bak", isDir: false, want: true},
		{path: "other/x.bak", isDir: false, want: false},
		{path: "sub/deep/x.bak", isDir: false, want: true},
		{path: "sub/x.log", isDir: false, want: true},
	}

	pats := cogBSkipPatterns()
	for _, tt := range cases {
		name := tt.path
		if tt.isDir {
			name += "/"
		}
		t.Run(name, func(t *testing.T) {
			e := cogBEntry{name: filepath.Base(tt.path), isDir: tt.isDir}
			if got := shouldSkipPath(tt.path, e, pats); got != tt.want {
				t.Errorf("shouldSkipPath(%q, isDir=%v) = %v, want %v", tt.path, tt.isDir, got, tt.want)
			}
		})
	}
}

func TestCogBShouldSkipPathNegationWins(t *testing.T) {
	// A negation reached before any matching positive pattern un-skips the path.
	pats := []GitignorePattern{
		{dir: "", pattern: "keep.log", isNeg: true},
		{dir: "", pattern: "*.log"},
	}
	e := cogBEntry{name: "keep.log"}
	if shouldSkipPath("keep.log", e, pats) {
		t.Error("negation listed first did not un-skip keep.log")
	}
	if !shouldSkipPath("other.log", cogBEntry{name: "other.log"}, pats) {
		t.Error("other.log should still be skipped")
	}
}

func TestCogBGitignorePatternMatchesGolden(t *testing.T) {
	pats := []GitignorePattern{
		{dir: "", pattern: "*.log"},
		{dir: "docs", pattern: "*.tmp"},
		{dir: "", pattern: "build", isDir: true},
	}

	cases := []struct {
		pat  int
		path string
		want bool
	}{
		{pat: 0, path: "a.log", want: true},
		{pat: 1, path: "a.log", want: false},
		{pat: 2, path: "a.log", want: false},
		{pat: 0, path: "sub/a.log", want: true},
		{pat: 1, path: "sub/a.log", want: false},
		{pat: 2, path: "sub/a.log", want: false},
		{pat: 0, path: "docs/x.tmp", want: false},
		{pat: 1, path: "docs/x.tmp", want: true},
		{pat: 2, path: "docs/x.tmp", want: false},
		{pat: 0, path: "docsx/x.tmp", want: false},
		{pat: 1, path: "docsx/x.tmp", want: false},
		{pat: 2, path: "docsx/x.tmp", want: false},
		{pat: 0, path: "docs/deep/x.tmp", want: false},
		{pat: 1, path: "docs/deep/x.tmp", want: true},
		{pat: 2, path: "docs/deep/x.tmp", want: false},
		{pat: 0, path: "build", want: false},
		{pat: 1, path: "build", want: false},
		{pat: 2, path: "build", want: true},
		{pat: 0, path: "sub/build", want: false},
		{pat: 1, path: "sub/build", want: false},
		{pat: 2, path: "sub/build", want: true},
	}

	for _, tt := range cases {
		if got := pats[tt.pat].matches(tt.path); got != tt.want {
			t.Errorf("pattern %d (%q, dir=%q).matches(%q) = %v, want %v",
				tt.pat, pats[tt.pat].pattern, pats[tt.pat].dir, tt.path, got, tt.want)
		}
	}
}

func TestCogBAppendPartsText(t *testing.T) {
	var sb strings.Builder
	appendPartsText(&sb, a2a.ContentParts{
		a2a.NewTextPart("one"),
		a2a.NewTextPart(""),
		a2a.NewTextPart("two"),
	})
	if sb.String() != "onetwo" {
		t.Errorf("got %q, want %q", sb.String(), "onetwo")
	}

	var empty strings.Builder
	appendPartsText(&empty, nil)
	if empty.String() != "" {
		t.Errorf("nil parts wrote %q", empty.String())
	}
}

func TestCogBAppendStreamEvent(t *testing.T) {
	cases := []struct {
		name     string
		event    a2a.Event
		wantText string
		wantEnd  bool
	}{
		{
			name:     "message is terminal",
			event:    a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart("hello"), a2a.NewTextPart("")),
			wantText: "hello",
			wantEnd:  true,
		},
		{
			name: "task is terminal",
			event: &a2a.Task{History: []*a2a.Message{
				a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart("done")),
			}},
			wantText: "done",
			wantEnd:  true,
		},
		{
			name:     "empty task is terminal and silent",
			event:    &a2a.Task{},
			wantText: "",
			wantEnd:  true,
		},
		{
			name:     "terminal status ends the stream",
			event:    &a2a.TaskStatusUpdateEvent{Status: a2a.TaskStatus{State: a2a.TaskStateCompleted}},
			wantText: "",
			wantEnd:  true,
		},
		{
			name:     "working status continues the stream",
			event:    &a2a.TaskStatusUpdateEvent{Status: a2a.TaskStatus{State: a2a.TaskStateWorking}},
			wantText: "",
			wantEnd:  false,
		},
		{
			name: "artifact accumulates and continues",
			event: &a2a.TaskArtifactUpdateEvent{Artifact: &a2a.Artifact{
				Parts: a2a.ContentParts{a2a.NewTextPart("chunk"), a2a.NewTextPart("")},
			}},
			wantText: "chunk",
			wantEnd:  false,
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			c := newStreamTextCollector()
			end := c.appendStreamEvent(tt.event)
			if end != tt.wantEnd {
				t.Errorf("terminal = %v, want %v", end, tt.wantEnd)
			}
			if c.String() != tt.wantText {
				t.Errorf("text = %q, want %q", c.String(), tt.wantText)
			}
		})
	}
}

func TestCogBAppendStreamEventAccumulates(t *testing.T) {
	// Two artifact chunks then a terminal status: the builder keeps both.
	c := newStreamTextCollector()
	for _, chunk := range []string{"a", "b"} {
		e := &a2a.TaskArtifactUpdateEvent{Artifact: &a2a.Artifact{
			Parts: a2a.ContentParts{a2a.NewTextPart(chunk)},
		}}
		if c.appendStreamEvent(e) {
			t.Fatal("artifact event reported the stream as ended")
		}
	}
	if !c.appendStreamEvent(&a2a.TaskStatusUpdateEvent{
		Status: a2a.TaskStatus{State: a2a.TaskStateFailed},
	}) {
		t.Fatal("failed status did not end the stream")
	}
	if c.String() != "ab" {
		t.Errorf("accumulated %q, want %q", c.String(), "ab")
	}
}

// cogBAnchoredGitignore is a .gitignore exercising the pattern forms that
// decide sandbox enforcement: anchored (/x), recursive (**/x), path-bearing
// (a/b/c), plain globs, negations and directory-only forms.
const cogBAnchoredGitignore = "/anchored.log\n**/deep.txt\na/b/c.txt\n*.o\n!.gitkeep\n/build/\n!/build/keep/\n\\#literal\n"

func TestCogBLoadGitignoreAnchoredForms(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(cogBAnchoredGitignore), 0o644); err != nil {
		t.Fatal(err)
	}

	sb, err := NewSandbox(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer sb.Close()

	pats, err := sb.loadGitignore()
	if err != nil {
		t.Fatalf("loadGitignore: %v", err)
	}

	want := `[{"","*.o",false,false} {"","*/deep.txt",false,false} {"",".gitkeep",true,false} ` +
		`{"","/anchored.log",false,false} {"","/build",false,true} {"","/build/keep",true,true} ` +
		`{"","\\#literal",false,false} {"","a/b/c.txt",false,false}]`
	if got := cogBFormatPatterns(pats); got != want {
		t.Errorf("patterns mismatch\n got: %s\nwant: %s", got, want)
	}
}

// TestCogBAnchoredPatternsMatchNothing pins a pre-existing limitation that the
// refactor must not silently change in either direction. GitignorePattern.matches
// globs against filepath.Base(path), so any pattern still containing a slash
// after parsing - anchored (/anchored.log), recursive (**/deep.txt -> */deep.txt)
// or path-bearing (a/b/c.txt) - can never match, because a base name never
// contains a separator. Only slash-free patterns are enforced, and those match
// at any depth.
//
// The expectations here were captured from the pre-refactor code. If a later
// change fixes the limitation, this test is the thing that will notice.
func TestCogBAnchoredPatternsMatchNothing(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(cogBAnchoredGitignore), 0o644); err != nil {
		t.Fatal(err)
	}
	sb, err := NewSandbox(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer sb.Close()

	pats, err := sb.loadGitignore()
	if err != nil {
		t.Fatalf("loadGitignore: %v", err)
	}

	// path -> the patterns that match it, by pattern text.
	want := map[string][]string{
		"anchored.log":     nil, // "/anchored.log" is inert: it holds a slash
		"sub/anchored.log": nil,
		"deep.txt":         nil, // "**/deep.txt" parsed to "*/deep.txt": inert
		"x/deep.txt":       nil,
		"a/b/c.txt":        nil, // path-bearing pattern: inert
		"c.txt":            nil,
		"build":            nil, // "/build/" parsed to "/build": inert
		"build/keep":       nil,
		"foo.o":            {"*.o"}, // slash-free glob: matches at root
		"sub/foo.o":        {"*.o"}, // ... and at any depth
		".gitkeep":         {".gitkeep"},
		"#literal":         {"\\#literal"},
	}

	for path, wantPats := range want {
		var got []string
		for _, p := range pats {
			if p.matches(path) {
				got = append(got, p.pattern)
			}
		}
		if !cogBEqualStrings(got, wantPats) {
			t.Errorf("matches(%q) = %v, want %v", path, got, wantPats)
		}
	}
}

func TestCogBParseGitignoreLineGolden(t *testing.T) {
	cases := []struct {
		in      string
		wantOK  bool
		pattern string
		isNeg   bool
		isDir   bool
	}{
		{"", false, "", false, false},                          // blank line skipped
		{"#comment", false, "", false, false},                  // comment skipped
		{"  ", true, "  ", false, false},                       // whitespace is a live pattern
		{"*.log", true, "*.log", false, false},                 // plain glob
		{"!keep.log", true, "keep.log", true, false},           // negation
		{"build/", true, "build", false, true},                 // directory-only
		{"!build/", true, "build", true, true},                 // negated directory-only
		{"/anchored", true, "/anchored", false, false},         // anchor kept verbatim
		{"**/deep", true, "*/deep", false, false},              // ** collapses to *
		{"a/**/b", true, "a/*/b", false, false},                // ** collapses mid-path
		{"**", true, "*", false, false},                        // bare ** collapses
		{"trailing\r", true, "trailing", false, false},         // CR stripped
		{"!/neg/", true, "/neg", true, true},                   // negated anchored dir
		{"a/b/c", true, "a/b/c", false, false},                 // path kept
		{"\\#literal", true, "\\#literal", false, false},       // escaped hash kept
		{"space in name", true, "space in name", false, false}, // spaces kept
		{"*.log ", true, "*.log ", false, false},               // trailing space NOT trimmed
	}

	for _, tt := range cases {
		t.Run(tt.in, func(t *testing.T) {
			p, ok := parseGitignoreLine(tt.in, "somedir")
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if p.pattern != tt.pattern || p.isNeg != tt.isNeg || p.isDir != tt.isDir {
				t.Errorf("got {%q,%v,%v}, want {%q,%v,%v}",
					p.pattern, p.isNeg, p.isDir, tt.pattern, tt.isNeg, tt.isDir)
			}
			if p.dir != "somedir" {
				t.Errorf("dir = %q, want %q", p.dir, "somedir")
			}
		})
	}
}
