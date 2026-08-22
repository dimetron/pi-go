package tools

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/jsonschema-go/jsonschema"

	"github.com/dimetron/pi-go/internal/lsp"
)

// This file pins the behavior of the helpers extracted while reducing
// cyclomatic complexity in lsp.go, read.go, registry.go, session_stats.go and
// session_sweep.go. Each test exercises a boundary the original branch
// structure encoded, so a behavioral drift shows up here rather than in the
// tool output.

// --- lsp.go: symbolKindName ------------------------------------------------

// TestSymbolKindNameTable checks every entry of the lookup table that replaced
// the dispatch switch, including the fallback for unmapped kinds.
func TestSymbolKindNameTable(t *testing.T) {
	want := map[int]string{
		1: "file", 2: "module", 3: "namespace", 4: "package", 5: "class",
		6: "method", 7: "property", 8: "field", 9: "constructor", 10: "enum",
		11: "interface", 12: "function", 13: "variable", 14: "constant",
		23: "struct",
	}
	for kind, name := range want {
		if got := symbolKindName(kind); got != name {
			t.Errorf("symbolKindName(%d) = %q, want %q", kind, got, name)
		}
	}
	if len(symbolKindNames) != len(want) {
		t.Errorf("symbolKindNames has %d entries, want %d", len(symbolKindNames), len(want))
	}
	// Kinds the table does not carry fall through to the numeric form, both
	// inside the recognized range (15-22 are unmapped) and outside it.
	for _, kind := range []int{0, 15, 22, 24, 999, -1} {
		if got, want := symbolKindName(kind), fmt.Sprintf("kind(%d)", kind); got != want {
			t.Errorf("symbolKindName(%d) = %q, want %q", kind, got, want)
		}
	}
	// The constants and the table must not have drifted apart.
	if symbolKindName(lsp.SymbolKindStruct) != "struct" {
		t.Error("lsp.SymbolKindStruct is no longer mapped to \"struct\"")
	}
}

// --- read.go: readNonText --------------------------------------------------

func TestReadNonText(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)

	t.Run("plain text is not handled", func(t *testing.T) {
		path := writeFile(t, dir, "plain.txt", "hello\nworld\n")
		out, handled, err := readNonText(sb, path, 12)
		if err != nil {
			t.Fatalf("readNonText: %v", err)
		}
		if handled {
			t.Fatalf("plain text should be left to the caller, got %+v", out)
		}
	})

	t.Run("binary is handled without returning its bytes", func(t *testing.T) {
		path := writeFile(t, dir, "blob.bin", "MZ\x00\x01\x02binary")
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		out, handled, err := readNonText(sb, path, info.Size())
		if err != nil {
			t.Fatalf("readNonText: %v", err)
		}
		if !handled {
			t.Fatal("binary file should be handled")
		}
		if strings.Contains(out.Content, "\x00") {
			t.Error("binary bytes leaked into the output")
		}
	})

	t.Run("notebook is rendered", func(t *testing.T) {
		nb := `{"cells":[{"cell_type":"code","source":["print(1)\n"]}]}`
		path := writeFile(t, dir, "nb.ipynb", nb)
		out, handled, err := readNonText(sb, path, int64(len(nb)))
		if err != nil {
			t.Fatalf("readNonText: %v", err)
		}
		if !handled {
			t.Fatal("notebook should be handled")
		}
		if !strings.Contains(out.Note, "Jupyter notebook") {
			t.Errorf("note does not mention the notebook rendering: %q", out.Note)
		}
	})

	t.Run("unreadable file reports handled so the caller stops", func(t *testing.T) {
		_, handled, err := readNonText(sb, filepath.Join(dir, "missing.txt"), 1)
		if err == nil {
			t.Fatal("expected an error for a missing file")
		}
		if !handled {
			t.Error("an error must still stop the caller from windowing the file")
		}
		if !strings.HasPrefix(err.Error(), "reading file: ") {
			t.Errorf("error lost its wrapper: %v", err)
		}
	})
}

// --- read.go: renderWindow -------------------------------------------------

func TestRenderWindow(t *testing.T) {
	tests := []struct {
		name       string
		win        window
		offset     int
		totalLines int
		wantNote   string
		wantNext   int
		wantTrunc  bool
		wantInBody string
	}{
		{
			name:       "complete window has no note",
			win:        window{content: "     1\ta\n", lastLine: 1},
			offset:     1,
			totalLines: 1,
		},
		{
			name:       "clamped lines are reported without truncating",
			win:        window{content: "     1\ta\n", lastLine: 1, clampedLines: 2},
			offset:     1,
			totalLines: 1,
			wantNote:   "2 line(s) longer than 2000 characters were clipped; the clipped part is marked inline.",
		},
		{
			name: "limit stop names the resume offset",
			win: window{
				content: "     1\ta\n", lastLine: 1,
				stoppedEarly: true, nextOffset: 2,
			},
			offset:     1,
			totalLines: 9,
			wantNote:   "showing lines 1-1 of 9; continue with offset=2.",
			wantNext:   2,
			wantTrunc:  true,
			wantInBody: "... (truncated: showing lines 1-1 of 9; continue with offset=2)",
		},
		{
			name: "byte budget stop says so",
			win: window{
				content: "     3\tc\n", lastLine: 4,
				stoppedEarly: true, hitByteBudget: true, nextOffset: 5,
			},
			offset:     3,
			totalLines: 40,
			wantNote:   fmt.Sprintf("showing lines 3-4 of 40 (stopped at the %dKB output budget); continue with offset=5.", readByteBudget/1024),
			wantNext:   5,
			wantTrunc:  true,
		},
		{
			name: "both notes are joined",
			win: window{
				content: "     1\ta\n", lastLine: 1, clampedLines: 1,
				stoppedEarly: true, nextOffset: 2,
			},
			offset:     1,
			totalLines: 5,
			wantNote: "1 line(s) longer than 2000 characters were clipped; the clipped part is marked inline. " +
				"showing lines 1-1 of 5; continue with offset=2.",
			wantNext:  2,
			wantTrunc: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := renderWindow(tt.win, tt.offset, tt.totalLines)
			if out.Note != tt.wantNote {
				t.Errorf("note = %q, want %q", out.Note, tt.wantNote)
			}
			if out.NextOffset != tt.wantNext {
				t.Errorf("next offset = %d, want %d", out.NextOffset, tt.wantNext)
			}
			if out.Truncated != tt.wantTrunc {
				t.Errorf("truncated = %v, want %v", out.Truncated, tt.wantTrunc)
			}
			if out.TotalLines != tt.totalLines {
				t.Errorf("total lines = %d, want %d", out.TotalLines, tt.totalLines)
			}
			if tt.wantInBody != "" && !strings.Contains(out.Content, tt.wantInBody) {
				t.Errorf("content %q does not contain %q", out.Content, tt.wantInBody)
			}
		})
	}

	t.Run("base64 images are stripped from the window", func(t *testing.T) {
		win := window{content: "     1\t![shot](data:image/png;base64,AAAA)\n", lastLine: 1}
		out := renderWindow(win, 1, 1)
		if strings.Contains(out.Content, "AAAA") {
			t.Errorf("base64 payload survived: %q", out.Content)
		}
	})
}

// --- read.go: readSrcLine, srcLine.endsFile, window.emitLine ---------------

func TestReadSrcLine(t *testing.T) {
	t.Run("line with terminator is not at EOF", func(t *testing.T) {
		r := bufio.NewReader(strings.NewReader("alpha\nbeta\n"))
		src, err := readSrcLine(r)
		if err != nil {
			t.Fatalf("readSrcLine: %v", err)
		}
		if src.text != "alpha" || src.clipped != 0 || src.atEOF {
			t.Errorf("got %+v, want {alpha 0 false}", src)
		}
	})

	t.Run("final unterminated line carries atEOF", func(t *testing.T) {
		r := bufio.NewReader(strings.NewReader("tail"))
		src, err := readSrcLine(r)
		if err != nil {
			t.Fatalf("readSrcLine: %v", err)
		}
		if src.text != "tail" || !src.atEOF {
			t.Errorf("got %+v, want {tail 0 true}", src)
		}
	})

	t.Run("EOF is not an error", func(t *testing.T) {
		r := bufio.NewReader(strings.NewReader(""))
		src, err := readSrcLine(r)
		if err != nil {
			t.Fatalf("EOF must not surface as an error: %v", err)
		}
		if src.text != "" || !src.atEOF {
			t.Errorf("got %+v, want the empty phantom line at EOF", src)
		}
	})

	t.Run("over-long line is clipped and counted", func(t *testing.T) {
		long := strings.Repeat("x", maxReadLineChars+37)
		r := bufio.NewReader(strings.NewReader(long + "\n"))
		src, err := readSrcLine(r)
		if err != nil {
			t.Fatalf("readSrcLine: %v", err)
		}
		if len(src.text) != maxReadLineChars {
			t.Errorf("kept %d bytes, want %d", len(src.text), maxReadLineChars)
		}
		if src.clipped != 37 {
			t.Errorf("clipped = %d, want 37", src.clipped)
		}
	})
}

func TestSrcLineEndsFile(t *testing.T) {
	tests := []struct {
		name string
		src  srcLine
		line int
		want bool
	}{
		{"phantom line after the final newline", srcLine{atEOF: true}, 3, true},
		{"empty first read of an empty file", srcLine{atEOF: true}, 0, false},
		{"blank line in the middle of a file", srcLine{}, 3, false},
		{"content at EOF is real", srcLine{text: "x", atEOF: true}, 3, false},
		{"clipped-only line at EOF is real", srcLine{clipped: 5, atEOF: true}, 3, false},
		// A lone CR is not empty until it is normalized, which happens after
		// this check — so it still counts as a line.
		{"lone carriage return at EOF is a line", srcLine{text: "\r", atEOF: true}, 3, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.src.endsFile(tt.line); got != tt.want {
				t.Errorf("endsFile(%d) = %v, want %v", tt.line, got, tt.want)
			}
		})
	}
}

func TestWindowEmitLine(t *testing.T) {
	t.Run("line inside the limit is written and does not close the window", func(t *testing.T) {
		var b strings.Builder
		var w window
		r := bufio.NewReader(strings.NewReader("more\n"))
		if w.emitLine(&b, r, srcLine{text: "hello"}, 1, 1, 10) {
			t.Fatal("window closed early")
		}
		if b.String() != "     1\thello\n" {
			t.Errorf("wrote %q", b.String())
		}
		if w.lastLine != 1 || w.stoppedEarly {
			t.Errorf("window = %+v", w)
		}
	})

	t.Run("clipped line gets the inline marker", func(t *testing.T) {
		var b strings.Builder
		var w window
		r := bufio.NewReader(strings.NewReader(""))
		w.emitLine(&b, r, srcLine{text: "abc", clipped: 9}, 1, 1, 10)
		if !strings.Contains(b.String(), "… [9 more characters on this line, clipped]") {
			t.Errorf("missing clip marker in %q", b.String())
		}
		if w.clampedLines != 1 {
			t.Errorf("clampedLines = %d, want 1", w.clampedLines)
		}
	})

	t.Run("limit reached with more to read truncates", func(t *testing.T) {
		var b strings.Builder
		var w window
		r := bufio.NewReader(strings.NewReader("next line\n"))
		if !w.emitLine(&b, r, srcLine{text: "last"}, 2, 1, 2) {
			t.Fatal("hitting the limit must close the window")
		}
		if !w.stoppedEarly || w.nextOffset != 3 {
			t.Errorf("window = %+v, want stoppedEarly with nextOffset 3", w)
		}
	})

	t.Run("limit reached exactly at EOF is not truncation", func(t *testing.T) {
		var b strings.Builder
		var w window
		r := bufio.NewReader(strings.NewReader(""))
		if !w.emitLine(&b, r, srcLine{text: "last", atEOF: true}, 2, 1, 2) {
			t.Fatal("hitting the limit must close the window")
		}
		if w.stoppedEarly || w.nextOffset != 0 {
			t.Errorf("window = %+v, want a clean end", w)
		}
	})

	t.Run("limit reached with an exhausted reader is not truncation", func(t *testing.T) {
		var b strings.Builder
		var w window
		r := bufio.NewReader(strings.NewReader(""))
		if !w.emitLine(&b, r, srcLine{text: "last"}, 2, 1, 2) {
			t.Fatal("hitting the limit must close the window")
		}
		if w.stoppedEarly {
			t.Errorf("nothing left to read, yet window = %+v", w)
		}
	})

	t.Run("byte budget stops before committing the line", func(t *testing.T) {
		var b strings.Builder
		b.WriteString(strings.Repeat("x", readByteBudget))
		before := b.Len()
		var w window
		r := bufio.NewReader(strings.NewReader(""))
		if !w.emitLine(&b, r, srcLine{text: "overflow"}, 7, 1, 100) {
			t.Fatal("the byte budget must close the window")
		}
		if b.Len() != before {
			t.Error("the line that did not fit was written anyway")
		}
		if !w.hitByteBudget || !w.stoppedEarly || w.nextOffset != 7 {
			t.Errorf("window = %+v, want a budget stop resuming at line 7", w)
		}
	})

	t.Run("the first line is never rejected by the budget", func(t *testing.T) {
		var b strings.Builder
		var w window
		r := bufio.NewReader(strings.NewReader(""))
		huge := strings.Repeat("y", readByteBudget+1)
		if w.emitLine(&b, r, srcLine{text: huge}, 1, 1, 100) {
			t.Fatal("an empty buffer must accept the line whatever its size")
		}
		if b.Len() == 0 {
			t.Error("the first line was dropped")
		}
	})
}

// --- registry.go: effectiveSchemaType, markCoerceProp ----------------------

func TestEffectiveSchemaType(t *testing.T) {
	tests := []struct {
		name string
		prop *jsonschema.Schema
		want string
	}{
		{"single type wins", &jsonschema.Schema{Type: "integer"}, "integer"},
		{"single type beats Types", &jsonschema.Schema{Type: "integer", Types: []string{"boolean"}}, "integer"},
		{"first non-null of Types", &jsonschema.Schema{Types: []string{"null", "array"}}, "array"},
		{"leading non-null of Types", &jsonschema.Schema{Types: []string{"object", "null"}}, "object"},
		{"only null yields nothing", &jsonschema.Schema{Types: []string{"null"}}, ""},
		{"no type at all", &jsonschema.Schema{}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := effectiveSchemaType(tt.prop); got != tt.want {
				t.Errorf("effectiveSchemaType = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMarkCoerceProp(t *testing.T) {
	t.Run("top level registers only the full path", func(t *testing.T) {
		props := map[string]bool{}
		markCoerceProp(props, "", "depth", "depth")
		if len(props) != 1 || !props["depth"] {
			t.Errorf("props = %v, want only depth", props)
		}
	})
	t.Run("nested also registers the bare name", func(t *testing.T) {
		props := map[string]bool{}
		markCoerceProp(props, "tasks.$", "depth", "tasks.$.depth")
		if !props["tasks.$.depth"] || !props["depth"] {
			t.Errorf("props = %v, want both the full path and the bare name", props)
		}
	})
}

// TestCollectFromSchemaNested pins the shape the extracted helpers must
// reproduce: nested objects, array items, and multi-type properties.
func TestCollectFromSchemaNested(t *testing.T) {
	schema := &jsonschema.Schema{
		Properties: map[string]*jsonschema.Schema{
			"tasks": {
				Type: "array",
				Items: &jsonschema.Schema{
					Type: "object",
					Properties: map[string]*jsonschema.Schema{
						"depth":   {Type: "integer"},
						"verbose": {Type: "boolean"},
						"tags":    {Types: []string{"null", "array"}},
					},
				},
			},
			"opts": {
				Type: "object",
				Properties: map[string]*jsonschema.Schema{
					"limit": {Type: "number"},
				},
			},
			"label": {Type: "string"},
		},
	}
	intP, boolP, jsonP := collectCoerceProps(schema)

	for _, want := range []string{"tasks.$.depth", "depth", "opts.limit", "limit"} {
		if !intP[want] {
			t.Errorf("intProps missing %q (got %v)", want, intP)
		}
	}
	for _, want := range []string{"tasks.$.verbose", "verbose"} {
		if !boolP[want] {
			t.Errorf("boolProps missing %q (got %v)", want, boolP)
		}
	}
	for _, want := range []string{"tasks", "opts", "tasks.$.tags", "tags"} {
		if !jsonP[want] {
			t.Errorf("jsonProps missing %q (got %v)", want, jsonP)
		}
	}
	if intP["label"] || boolP["label"] || jsonP["label"] {
		t.Error("a string property must not be registered for coercion")
	}
}

// --- registry.go: coerceStringValue, parseStringifiedJSON, numberAsFloat64 --

func TestCoerceStringValue(t *testing.T) {
	c := &coercingTool{
		intProps:  map[string]bool{"n": true},
		boolProps: map[string]bool{"b": true},
		jsonProps: map[string]bool{"j": true},
	}
	tests := []struct {
		name string
		in   string
		path string
		want any
	}{
		{"integer string becomes float64", "42", "n", float64(42)},
		{"float string becomes float64", "1.5", "n", 1.5},
		{"negative integer", "-7", "n", float64(-7)},
		{"unparseable number stays put", "many", "n", nil},
		{"bool word", "true", "b", true},
		{"bool digit", "0", "b", false},
		{"unparseable bool stays put", "yes please", "b", nil},
		{"stringified array", `["a"]`, "j", []any{"a"}},
		{"unregistered path is untouched", "42", "other", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := c.coerceStringValue(tt.in, tt.path)
			if fmt.Sprint(got) != fmt.Sprint(tt.want) {
				t.Errorf("coerceStringValue(%q, %q) = %#v, want %#v", tt.in, tt.path, got, tt.want)
			}
		})
	}

	t.Run("int registration takes precedence over bool", func(t *testing.T) {
		both := &coercingTool{
			intProps:  map[string]bool{"x": true},
			boolProps: map[string]bool{"x": true},
		}
		if got := both.coerceStringValue("1", "x"); got != float64(1) {
			t.Errorf("got %#v, want float64(1) — intProps is checked first", got)
		}
	})
}

func TestParseStringifiedJSON(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string // fmt.Sprint of the result
	}{
		{"array", `["a","b"]`, "[a b]"},
		{"object", `{"k":1}`, "map[k:1]"},
		{"surrounding space is tolerated", "  [1]  ", "[1]"},
		{"unbracketed string", "plain", "<nil>"},
		{"quoted scalar is not a document", `"plain"`, "<nil>"},
		{"mismatched brackets", `[1}`, "<nil>"},
		{"bracketed but invalid JSON", `[1,]`, "<nil>"},
		{"empty", "", "<nil>"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := fmt.Sprint(parseStringifiedJSON(tt.in)); got != tt.want {
				t.Errorf("parseStringifiedJSON(%q) = %s, want %s", tt.in, got, tt.want)
			}
		})
	}
}

func TestNumberAsFloat64(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want any
	}{
		{"float64 passes through", float64(3), float64(3)},
		{"float32", float32(2.5), 2.5},
		{"int", int(4), float64(4)},
		{"int64", int64(5), float64(5)},
		{"int32", int32(6), float64(6)},
		{"uint is not converted", uint(7), nil},
		{"string is not converted", "8", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := numberAsFloat64(tt.in); got != tt.want {
				t.Errorf("numberAsFloat64(%#v) = %#v, want %#v", tt.in, got, tt.want)
			}
		})
	}
}

func TestTryCoerceByInputType(t *testing.T) {
	c := &coercingTool{intProps: map[string]bool{"n": true}}

	if got := c.tryCoerce(float64(9), "n"); got != nil {
		t.Errorf("a float64 is already in JSON form, got %#v", got)
	}
	if got := c.tryCoerce(int64(9), "n"); got != float64(9) {
		t.Errorf("int64 = %#v, want float64(9)", got)
	}
	if got := c.tryCoerce(int64(9), "other"); got != nil {
		t.Errorf("an unregistered path must not coerce, got %#v", got)
	}
	if got := c.tryCoerce(json.Number("12"), "n"); got != float64(12) {
		t.Errorf("json.Number = %#v, want float64(12)", got)
	}
	if got := c.tryCoerce(json.Number("nope"), "n"); got != nil {
		t.Errorf("an unparseable json.Number must stay put, got %#v", got)
	}
	if got := c.tryCoerce(json.Number("12"), "other"); got != nil {
		t.Errorf("an unregistered path must not coerce, got %#v", got)
	}
	if got := c.tryCoerce(true, "n"); got != nil {
		t.Errorf("an unhandled type must stay put, got %#v", got)
	}
}

// --- registry.go: coerceAtPath, coerceArrayItemProp ------------------------

func TestCoerceAtPath(t *testing.T) {
	c := &coercingTool{intProps: map[string]bool{"depth": true}}

	t.Run("registered path coerces", func(t *testing.T) {
		obj := map[string]any{"depth": "3"}
		c.coerceAtPath(obj, "depth", "depth", "3")
		if obj["depth"] != float64(3) {
			t.Errorf("depth = %#v, want float64(3)", obj["depth"])
		}
	})

	t.Run("unregistered path leaves the value alone", func(t *testing.T) {
		obj := map[string]any{"depth": "3"}
		c.coerceAtPath(obj, "depth", "other.depth", "3")
		if obj["depth"] != "3" {
			t.Errorf("depth = %#v, want the untouched string", obj["depth"])
		}
	})

	t.Run("registered but uncoercible leaves the value alone", func(t *testing.T) {
		obj := map[string]any{"depth": "deep"}
		c.coerceAtPath(obj, "depth", "depth", "deep")
		if obj["depth"] != "deep" {
			t.Errorf("depth = %#v, want the untouched string", obj["depth"])
		}
	})

	t.Run("the passed value is used, not the map's current one", func(t *testing.T) {
		obj := map[string]any{"depth": "999"}
		c.coerceAtPath(obj, "depth", "depth", "3")
		if obj["depth"] != float64(3) {
			t.Errorf("depth = %#v, want float64(3) from the passed value", obj["depth"])
		}
	})
}

func TestCoerceArrayItemProp(t *testing.T) {
	c := &coercingTool{
		intProps:  map[string]bool{"tasks.$.depth": true},
		boolProps: map[string]bool{"tasks.$.verbose": true},
		jsonProps: map[string]bool{"tasks.$.tags": true},
	}

	t.Run("parent.$ path coerces an array item property", func(t *testing.T) {
		obj := map[string]any{"depth": "3", "verbose": "true", "tags": `["x"]`}
		for _, k := range []string{"depth", "verbose", "tags"} {
			c.coerceArrayItemProp(obj, k, "tasks")
		}
		if obj["depth"] != float64(3) {
			t.Errorf("depth = %#v", obj["depth"])
		}
		if obj["verbose"] != true {
			t.Errorf("verbose = %#v", obj["verbose"])
		}
		if fmt.Sprint(obj["tags"]) != "[x]" {
			t.Errorf("tags = %#v", obj["tags"])
		}
	})

	t.Run("unregistered property is untouched", func(t *testing.T) {
		obj := map[string]any{"name": "7"}
		c.coerceArrayItemProp(obj, "name", "tasks")
		if obj["name"] != "7" {
			t.Errorf("name = %#v, want the untouched string", obj["name"])
		}
	})

	t.Run("nested arrays recurse under the dotted path", func(t *testing.T) {
		nested := &coercingTool{intProps: map[string]bool{"tasks.chain.$.depth": true}}
		obj := map[string]any{"chain": []any{map[string]any{"depth": "4"}}}
		nested.coerceArrayItemProp(obj, "chain", "tasks")
		inner, ok := obj["chain"].([]any)[0].(map[string]any)
		if !ok {
			t.Fatalf("chain item is %#v", obj["chain"])
		}
		if inner["depth"] != float64(4) {
			t.Errorf("nested depth = %#v, want float64(4)", inner["depth"])
		}
	})
}

// TestCoerceArgsEndToEnd exercises the whole coercion path over a payload of
// the shape the LLM actually sends, so the split helpers are checked together.
func TestCoerceArgsEndToEnd(t *testing.T) {
	schema := &jsonschema.Schema{
		Properties: map[string]*jsonschema.Schema{
			"tasks": {
				Type: "array",
				Items: &jsonschema.Schema{
					Type: "object",
					Properties: map[string]*jsonschema.Schema{
						"depth":   {Type: "integer"},
						"verbose": {Type: "boolean"},
					},
				},
			},
			"limit": {Type: "integer"},
		},
	}
	intP, boolP, jsonP := collectCoerceProps(schema)
	c := &coercingTool{intProps: intP, boolProps: boolP, jsonProps: jsonP}

	args := map[string]any{
		"limit": "5",
		"tasks": `[{"depth":"2","verbose":"false"}]`,
	}
	c.coerceArgs(args)

	if args["limit"] != float64(5) {
		t.Errorf("limit = %#v, want float64(5)", args["limit"])
	}
	tasks, ok := args["tasks"].([]any)
	if !ok {
		t.Fatalf("tasks was not parsed out of its string form: %#v", args["tasks"])
	}
	first, ok := tasks[0].(map[string]any)
	if !ok {
		t.Fatalf("task item = %#v", tasks[0])
	}
	if first["depth"] != float64(2) {
		t.Errorf("depth = %#v, want float64(2)", first["depth"])
	}
	if first["verbose"] != false {
		t.Errorf("verbose = %#v, want false", first["verbose"])
	}
}

// --- session_stats.go: option resolution and scanning ----------------------

func TestResolveSessionStatsOptions(t *testing.T) {
	t.Run("defaults fill in", func(t *testing.T) {
		before := time.Now()
		opts, err := resolveSessionStatsOptions(SessionStatsInput{SessionDir: "/tmp/x"})
		if err != nil {
			t.Fatalf("resolveSessionStatsOptions: %v", err)
		}
		if opts.highToolCalls != 20 || opts.highTurns != 5 || opts.hours != 24 {
			t.Errorf("opts = %+v, want the documented defaults", opts)
		}
		if want := before.Add(-24 * time.Hour); opts.cutoff.Before(want.Add(-time.Minute)) {
			t.Errorf("cutoff %v is not ~24h back from %v", opts.cutoff, before)
		}
	})

	t.Run("explicit values win", func(t *testing.T) {
		opts, err := resolveSessionStatsOptions(SessionStatsInput{
			SessionDir: "/tmp/x", HighToolCalls: 3, HighTurns: 2, Hours: 1,
		})
		if err != nil {
			t.Fatalf("resolveSessionStatsOptions: %v", err)
		}
		if opts.highToolCalls != 3 || opts.highTurns != 2 || opts.hours != 1 {
			t.Errorf("opts = %+v, want the caller's values", opts)
		}
	})

	t.Run("negative values fall back to the defaults", func(t *testing.T) {
		opts, err := resolveSessionStatsOptions(SessionStatsInput{
			SessionDir: "/tmp/x", HighToolCalls: -1, HighTurns: -1, Hours: -1,
		})
		if err != nil {
			t.Fatalf("resolveSessionStatsOptions: %v", err)
		}
		if opts.highToolCalls != 20 || opts.highTurns != 5 || opts.hours != 24 {
			t.Errorf("opts = %+v, want the documented defaults", opts)
		}
	})

	t.Run("empty dir resolves under the home directory", func(t *testing.T) {
		opts, err := resolveSessionStatsOptions(SessionStatsInput{})
		if err != nil {
			t.Skipf("no home directory available: %v", err)
		}
		if !strings.HasSuffix(opts.sessionDir, filepath.Join(".pi-go", "sessions")) {
			t.Errorf("sessionDir = %q, want it under ~/.pi-go/sessions", opts.sessionDir)
		}
	})
}

func TestScanSessions(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	event := func(author string, turnComplete bool) string {
		b, err := json.Marshal(map[string]any{
			"Timestamp": now.Format(time.RFC3339Nano), "Author": author, "TurnComplete": turnComplete,
		})
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}

	writeEvents(t, root, "live", event("pi", true))
	writeEvents(t, root, "archive", event("pi", true))
	// A plain file, not a directory, must be skipped.
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A directory without events.jsonl must be skipped.
	if err := os.MkdirAll(filepath.Join(root, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A session older than the cutoff must be skipped.
	writeEvents(t, root, "stale", event("pi", true))
	old := now.Add(-72 * time.Hour)
	if err := os.Chtimes(filepath.Join(root, "stale", "events.jsonl"), old, old); err != nil {
		t.Fatal(err)
	}

	opts := sessionStatsOptions{
		sessionDir: root, highToolCalls: 20, highTurns: 5, hours: 24,
		cutoff: now.Add(-24 * time.Hour),
	}
	stats, totals, err := scanSessions(opts)
	if err != nil {
		t.Fatalf("scanSessions: %v", err)
	}
	if totals == nil {
		t.Fatal("totals must be allocated even when nothing accumulates")
	}
	if len(stats) != 1 || stats[0].ID != "live" {
		ids := make([]string, len(stats))
		for i, s := range stats {
			ids[i] = s.ID
		}
		t.Fatalf("scanned %v, want only [live]", ids)
	}

	t.Run("a missing directory is an error", func(t *testing.T) {
		opts := opts
		opts.sessionDir = filepath.Join(root, "nope")
		if _, _, err := scanSessions(opts); err == nil {
			t.Fatal("expected an error for a missing sessions dir")
		} else if !strings.HasPrefix(err.Error(), "reading sessions dir: ") {
			t.Errorf("error lost its wrapper: %v", err)
		}
	})
}

// --- session_stats.go: counters and anomalies ------------------------------

func TestSessionCountersObserveTimestamp(t *testing.T) {
	var c sessionCounters
	early := "2026-01-01T10:00:00Z"
	late := "2026-01-01T12:30:00Z"

	c.observeTimestamp("")           // ignored
	c.observeTimestamp("not a time") // ignored
	c.observeTimestamp(late)         // sets both ends
	c.observeTimestamp(early)        // widens the start
	c.observeTimestamp("2026-01-01T11:00:00Z")

	if got := c.firstTS.Format(time.RFC3339); got != "2026-01-01T10:00:00Z" {
		t.Errorf("firstTS = %s, want the earliest timestamp", got)
	}
	if got := c.lastTS.Format(time.RFC3339); got != "2026-01-01T12:30:00Z" {
		t.Errorf("lastTS = %s, want the latest timestamp", got)
	}
}

func TestSessionCountersObserveContent(t *testing.T) {
	tests := []struct {
		name                             string
		content                          any
		wantCalls, wantResults, wantGits int
	}{
		{"nil content", nil, 0, 0, 0},
		{"content is not an object", "text", 0, 0, 0},
		{"no parts key", map[string]any{"role": "user"}, 0, 0, 0},
		{"parts is not a list", map[string]any{"parts": "nope"}, 0, 0, 0},
		{"a part that is not an object is skipped", map[string]any{"parts": []any{"nope"}}, 0, 0, 0},
		{
			"a plain text part counts for nothing",
			map[string]any{"parts": []any{map[string]any{"text": "hi"}}},
			0, 0, 0,
		},
		{
			"function calls and responses are counted separately",
			map[string]any{"parts": []any{
				map[string]any{"functionCall": map[string]any{"name": "read"}},
				map[string]any{"functionResponse": map[string]any{"name": "read"}},
			}},
			1, 1, 0,
		},
		{
			"a git tool call is also a git op",
			map[string]any{"parts": []any{
				map[string]any{"functionCall": map[string]any{"name": "git-overview"}},
			}},
			1, 0, 1,
		},
		{
			"a bash git command is a git op",
			map[string]any{"parts": []any{
				map[string]any{"functionCall": map[string]any{
					"name": "bash", "args": map[string]any{"command": "  git status"},
				}},
			}},
			1, 0, 1,
		},
		{
			"a bash non-git command is not",
			map[string]any{"parts": []any{
				map[string]any{"functionCall": map[string]any{
					"name": "bash", "args": map[string]any{"command": "ls"},
				}},
			}},
			1, 0, 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var c sessionCounters
			c.observeContent(tt.content)
			if c.toolCalls != tt.wantCalls || c.toolResults != tt.wantResults || c.gitOps != tt.wantGits {
				t.Errorf("calls=%d results=%d gits=%d, want %d/%d/%d",
					c.toolCalls, c.toolResults, c.gitOps, tt.wantCalls, tt.wantResults, tt.wantGits)
			}
		})
	}
}

func TestSessionCountersObserve(t *testing.T) {
	t.Run("a model turn counts, a user turn does not", func(t *testing.T) {
		var c sessionCounters
		c.observe(&sessionEvent{TurnComplete: true, Author: "pi"})
		c.observe(&sessionEvent{TurnComplete: true, Author: "user"})
		c.observe(&sessionEvent{TurnComplete: false, Author: "pi"})
		if c.turns != 1 {
			t.Errorf("turns = %d, want 1", c.turns)
		}
	})

	t.Run("one event is at most one error", func(t *testing.T) {
		var c sessionCounters
		c.observe(&sessionEvent{ErrorCode: "E", ErrorMessage: "boom", Interrupted: true})
		if c.errors != 1 {
			t.Errorf("errors = %d, want 1", c.errors)
		}
	})

	t.Run("each error signal alone counts", func(t *testing.T) {
		for _, ev := range []sessionEvent{
			{ErrorCode: "E"}, {ErrorMessage: "boom"}, {Interrupted: true},
		} {
			var c sessionCounters
			c.observe(&ev)
			if c.errors != 1 {
				t.Errorf("%+v gave errors = %d, want 1", ev, c.errors)
			}
		}
	})

	t.Run("a clean event counts nothing", func(t *testing.T) {
		var c sessionCounters
		c.observe(&sessionEvent{Author: "pi"})
		if c.turns != 0 || c.errors != 0 || c.toolCalls != 0 {
			t.Errorf("counters = %+v, want all zero", c)
		}
	})
}

func TestSessionCountersStat(t *testing.T) {
	t.Run("counters are carried across verbatim", func(t *testing.T) {
		c := sessionCounters{
			lines: 9, toolCalls: 8, toolResults: 7, turns: 6, errors: 5, gitOps: 4,
			firstTS: time.Unix(1000, 0), lastTS: time.Unix(1090, 0),
		}
		s := c.stat("sid")
		if s.ID != "sid" || s.Lines != 9 || s.ToolCalls != 8 || s.ToolResults != 7 ||
			s.Turns != 6 || s.Errors != 5 || s.GitOps != 4 {
			t.Errorf("stat = %+v", s)
		}
		if s.Duration != 90*time.Second {
			t.Errorf("duration = %v, want 90s", s.Duration)
		}
	})

	t.Run("no timestamps means no duration", func(t *testing.T) {
		var c sessionCounters
		if got := c.stat("sid").Duration; got != 0 {
			t.Errorf("duration = %v, want 0", got)
		}
	})

	t.Run("only one timestamp means no duration", func(t *testing.T) {
		c := sessionCounters{lastTS: time.Unix(1090, 0)}
		if got := c.stat("sid").Duration; got != 0 {
			t.Errorf("duration = %v, want 0", got)
		}
	})
}

func TestSessionAnomalies(t *testing.T) {
	tests := []struct {
		name string
		stat sessionStat
		want []string
	}{
		{"quiet session", sessionStat{ToolCalls: 5, Turns: 2}, nil},
		{"threshold is exclusive", sessionStat{ToolCalls: 20, Turns: 5}, nil},
		{"high tool calls", sessionStat{ToolCalls: 21}, []string{"high tool calls (21)"}},
		{"excessive turns", sessionStat{Turns: 6}, []string{"excessive turns (6)"}},
		{"errors", sessionStat{Errors: 2}, []string{"errors (2)"}},
		{"git ops threshold is exclusive", sessionStat{GitOps: defaultHighGitOps}, nil},
		{"many git ops", sessionStat{GitOps: defaultHighGitOps + 1},
			[]string{fmt.Sprintf("many git operations (%d)", defaultHighGitOps+1)}},
		{"long idle needs both a long duration and few calls",
			sessionStat{Duration: 31 * time.Minute, ToolCalls: 4}, []string{"long idle session"}},
		{"a long busy session is not idle",
			sessionStat{Duration: 31 * time.Minute, ToolCalls: 5}, nil},
		{"a short quiet session is not idle",
			sessionStat{Duration: 29 * time.Minute, ToolCalls: 1}, nil},
		{"anomalies come out in a fixed order",
			sessionStat{ToolCalls: 21, Turns: 6, Errors: 1, GitOps: defaultHighGitOps + 1},
			[]string{
				"high tool calls (21)", "excessive turns (6)", "errors (1)",
				fmt.Sprintf("many git operations (%d)", defaultHighGitOps+1),
			}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sessionAnomalies(tt.stat, 20, 5)
			if strings.Join(got, "|") != strings.Join(tt.want, "|") {
				t.Errorf("anomalies = %v, want %v", got, tt.want)
			}
		})
	}
}

// --- session_stats.go: table and detail rendering --------------------------

func TestRenderSessionTable(t *testing.T) {
	stats := []sessionStat{
		{ID: "quiet", Lines: 3, ToolCalls: 1, Duration: 90 * time.Second},
		{ID: "noisy", Lines: 9, ToolCalls: 30, Errors: 2, Anomalies: []string{"errors (2)"}},
	}

	t.Run("only anomalous sessions by default", func(t *testing.T) {
		var b strings.Builder
		renderSessionTable(&b, stats, false)
		if strings.Contains(b.String(), "| quiet |") {
			t.Errorf("quiet session should be hidden:\n%s", b.String())
		}
		if !strings.Contains(b.String(), "| noisy | 9 | 30 | 0 | 2 | 0s | errors (2) |") {
			t.Errorf("noisy row missing or malformed:\n%s", b.String())
		}
	})

	t.Run("all shows every session with an em dash for none", func(t *testing.T) {
		var b strings.Builder
		renderSessionTable(&b, stats, true)
		if !strings.Contains(b.String(), "| quiet | 3 | 1 | 0 | 0 | 1m30s | — |") {
			t.Errorf("quiet row missing or malformed:\n%s", b.String())
		}
	})

	t.Run("the header is written even with no rows", func(t *testing.T) {
		var b strings.Builder
		renderSessionTable(&b, nil, false)
		if !strings.HasPrefix(b.String(), "| Session | Lines |") {
			t.Errorf("header missing:\n%s", b.String())
		}
	})
}

func TestRenderAnomalyDetails(t *testing.T) {
	t.Run("nothing is written when no session is anomalous", func(t *testing.T) {
		var b strings.Builder
		renderAnomalyDetails(&b, []sessionStat{{ID: "quiet"}})
		if b.Len() != 0 {
			t.Errorf("wrote %q, want nothing", b.String())
		}
	})

	t.Run("only anomalous sessions get a block", func(t *testing.T) {
		var b strings.Builder
		renderAnomalyDetails(&b, []sessionStat{
			{ID: "quiet"},
			{ID: "noisy", Lines: 9, ToolCalls: 30, Turns: 7, Errors: 2,
				Duration: 61 * time.Second, Anomalies: []string{"errors (2)", "excessive turns (7)"}},
		})
		out := b.String()
		if !strings.Contains(out, "### Anomaly Details") {
			t.Errorf("heading missing:\n%s", out)
		}
		if strings.Contains(out, "**quiet**") {
			t.Errorf("quiet session should not appear:\n%s", out)
		}
		for _, want := range []string{
			"**noisy**",
			"- Lines: 9, Tool calls: 30, Turns: 7, Errors: 2\n",
			"- Duration: 1m1s\n",
			"- Anomalies: errors (2); excessive turns (7)\n",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("missing %q in:\n%s", want, out)
			}
		}
	})
}

// --- session_sweep.go: the three sweep sections ----------------------------

func TestRenderSweepFailures(t *testing.T) {
	t.Run("no aborts and no errors", func(t *testing.T) {
		var b strings.Builder
		renderSweepFailures(&b, newSweepTotals())
		out := b.String()
		if !strings.Contains(out, "## Failures") || !strings.Contains(out, "No aborted runs.") {
			t.Errorf("unexpected output:\n%s", out)
		}
		if strings.Contains(out, "| tool | errors |") {
			t.Errorf("an error table was written with no errors:\n%s", out)
		}
	})

	t.Run("aborts are listed", func(t *testing.T) {
		totals := newSweepTotals()
		totals.Aborts["repeated tool call"] = 3
		var b strings.Builder
		renderSweepFailures(&b, totals)
		if !strings.Contains(b.String(), "- 3x `repeated tool call`") {
			t.Errorf("abort line missing:\n%s", b.String())
		}
	})

	t.Run("error rates are a rate, and clean tools are cut off", func(t *testing.T) {
		totals := newSweepTotals()
		totals.tool("bash").Calls = 10
		totals.tool("bash").Errors = 3
		totals.tool("read").Calls = 100 // no errors: must not appear
		var b strings.Builder
		renderSweepFailures(&b, totals)
		out := b.String()
		if !strings.Contains(out, "| bash | 3 | 10 | 30% |") {
			t.Errorf("bash rate row missing:\n%s", out)
		}
		if strings.Contains(out, "| read |") {
			t.Errorf("a tool with no errors was listed:\n%s", out)
		}
		if !strings.Contains(out, "pi-check-session-logs") {
			t.Errorf("follow-up pointer missing:\n%s", out)
		}
	})

	t.Run("a tool with errors but no recorded calls does not divide by zero", func(t *testing.T) {
		totals := newSweepTotals()
		totals.tool("grep").Errors = 2
		var b strings.Builder
		renderSweepFailures(&b, totals)
		if !strings.Contains(b.String(), "| grep | 2 | 1 | 200% |") {
			t.Errorf("expected a floor of one call:\n%s", b.String())
		}
	})
}

func TestRenderSweepWaste(t *testing.T) {
	t.Run("nothing wasted", func(t *testing.T) {
		var b strings.Builder
		renderSweepWaste(&b, newSweepTotals())
		if !strings.Contains(b.String(), "Nothing oversized or duplicated.") {
			t.Errorf("unexpected output:\n%s", b.String())
		}
	})

	t.Run("oversize is attributed and the compactor coverage named", func(t *testing.T) {
		totals := newSweepTotals()
		totals.ToolBytes = 400000
		totals.tool("bash").Oversized = 40000
		totals.tool("session-stats").Oversized = 20000
		totals.tool("tree").Oversized = 0 // must be cut off
		var b strings.Builder
		renderSweepWaste(&b, totals)
		out := b.String()
		if !strings.Contains(out, "Reclaimable: **~15,000 tokens** (15% of tool output)") {
			t.Errorf("reclaim line wrong:\n%s", out)
		}
		if !strings.Contains(out, "| bash | 10,000 tok | yes |") {
			t.Errorf("bash row wrong:\n%s", out)
		}
		if !strings.Contains(out, "| session-stats | 5,000 tok | **no — uncapped** |") {
			t.Errorf("uncapped row wrong:\n%s", out)
		}
		if strings.Contains(out, "| tree |") {
			t.Errorf("a tool with no oversize was listed:\n%s", out)
		}
		if strings.Contains(out, "Duplicate results") {
			t.Errorf("duplicates mentioned with none recorded:\n%s", out)
		}
	})

	t.Run("hyphenated tool names match the underscored compactor set", func(t *testing.T) {
		totals := newSweepTotals()
		totals.tool("git-overview").Oversized = 8000
		var b strings.Builder
		renderSweepWaste(&b, totals)
		if !strings.Contains(b.String(), "| git-overview | 2,000 tok | yes |") {
			t.Errorf("git-overview should map to git_overview:\n%s", b.String())
		}
	})

	t.Run("duplicates alone are still waste", func(t *testing.T) {
		totals := newSweepTotals()
		totals.DupBytes = 8000
		var b strings.Builder
		renderSweepWaste(&b, totals)
		out := b.String()
		if !strings.Contains(out, "Reclaimable: **~2,000 tokens** (0% of tool output)") {
			t.Errorf("reclaim line wrong:\n%s", out)
		}
		if !strings.Contains(out, "Duplicate results re-sent inside one session: ~2,000 tokens.") {
			t.Errorf("duplicate line missing:\n%s", out)
		}
	})
}

func TestRenderSweepSpend(t *testing.T) {
	t.Run("no usage metadata", func(t *testing.T) {
		var b strings.Builder
		renderSweepSpend(&b, newSweepTotals(), 3)
		if !strings.Contains(b.String(), "No usage metadata") {
			t.Errorf("unexpected output:\n%s", b.String())
		}
	})

	t.Run("totals and per-session average", func(t *testing.T) {
		totals := newSweepTotals()
		totals.PromptTokens = 30000
		totals.OutputTokens = 1500
		var b strings.Builder
		renderSweepSpend(&b, totals, 3)
		out := b.String()
		if !strings.Contains(out, "Prompt 30,000 · output 1,500 (provider-reported, not estimated)") {
			t.Errorf("totals line wrong:\n%s", out)
		}
		if !strings.Contains(out, "Average 10,000 prompt tokens per session across 3 session(s).") {
			t.Errorf("average line wrong:\n%s", out)
		}
	})

	t.Run("no sessions means no average", func(t *testing.T) {
		totals := newSweepTotals()
		totals.PromptTokens = 30000
		var b strings.Builder
		renderSweepSpend(&b, totals, 0)
		if strings.Contains(b.String(), "Average") {
			t.Errorf("averaged over zero sessions:\n%s", b.String())
		}
	})
}

// TestRenderSweepComposition checks that renderSweep still emits the three
// sections, in order, now that each is its own function.
func TestRenderSweepComposition(t *testing.T) {
	totals := newSweepTotals()
	totals.PromptTokens = 100
	var b strings.Builder
	renderSweep(&b, totals, 1)
	out := b.String()
	failures := strings.Index(out, "## Failures")
	waste := strings.Index(out, "## Token waste")
	spend := strings.Index(out, "## Token spend")
	if failures < 0 || waste < 0 || spend < 0 {
		t.Fatalf("a section is missing:\n%s", out)
	}
	if failures >= waste || waste >= spend {
		t.Errorf("sections out of order: failures=%d waste=%d spend=%d", failures, waste, spend)
	}
}
