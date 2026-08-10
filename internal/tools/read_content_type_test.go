package tools

import (
	"strings"
	"testing"
)

func TestClassifyContent(t *testing.T) {
	png := pngBytes(t)

	tests := []struct {
		name   string
		prefix []byte
		path   string
		want   contentKind
	}{
		{"plain text", []byte("package main\n"), "main.go", kindText},
		{"png by magic", png, "photo.png", kindImage},
		// The extension lies in both directions; the bytes decide.
		{"png named .txt", png, "notes.txt", kindImage},
		{"text named .png", []byte("this is not a png\n"), "fake.png", kindText},
		{"pdf by magic", []byte("%PDF-1.7\n%âãÏÓ\n"), "paper.pdf", kindPDF},
		{"svg is text", []byte(`<svg xmlns="http://www.w3.org/2000/svg"><rect/></svg>`), "icon.svg", kindText},
		{"xml-declared svg is text", []byte(`<?xml version="1.0"?><svg viewBox="0 0 1 1"/>`), "icon.svg", kindText},
		{"nul byte means binary", []byte{'M', 'Z', 0x00, 0x01, 0x02}, "a.exe", kindBinary},
		{"notebook by extension", []byte(`{"cells": []}`), "analysis.ipynb", kindNotebook},
		{"empty is text", nil, "empty.txt", kindText},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifyContent(tt.prefix, tt.path); got != tt.want {
				t.Errorf("classifyContent(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

// TestRead_BinaryReturnsNoBytes is the point of classification: a binary in the
// transcript is pure cost — unreadable, uneditable, and displacing context that
// could have held something useful.
func TestRead_BinaryReturnsNoBytes(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)
	path := writeFile(t, dir, "a.bin", "MZ\x00\x01\x02\x03binary garbage\x00\x00")

	out, err := readHandler(sb, ReadInput{FilePath: path})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if out.Content != "" {
		t.Errorf("binary content leaked into the transcript: %q", out.Content)
	}
	if !strings.Contains(out.Note, "binary file") {
		t.Errorf("Note does not identify the file: %q", out.Note)
	}
	if !strings.Contains(out.Note, "xxd") {
		t.Errorf("Note offers no way forward: %q", out.Note)
	}
}

func TestRead_ImagePointsAtReadImage(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)
	path := writeFile(t, dir, "shot.png", string(pngBytes(t)))

	out, err := readHandler(sb, ReadInput{FilePath: path})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if out.Content != "" {
		t.Errorf("image bytes leaked into the transcript: %q", out.Content)
	}
	if !strings.Contains(out.Note, "read_image") {
		t.Errorf("Note does not name the tool that can actually see it: %q", out.Note)
	}
}

func TestRead_PDFSuggestsPdftotext(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)
	path := writeFile(t, dir, "paper.pdf", "%PDF-1.7\nnot really a pdf body\n")

	out, err := readHandler(sb, ReadInput{FilePath: path})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(out.Note, "pdftotext") {
		t.Errorf("Note does not give the extraction command: %q", out.Note)
	}
}

// TestRead_SVGIsReadableText guards against over-eager binary refusal: SVG is
// XML, it is editable, and refusing it would be actively unhelpful.
func TestRead_SVGIsReadableText(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)
	path := writeFile(t, dir, "icon.svg", `<svg xmlns="http://www.w3.org/2000/svg">`+"\n"+`  <rect width="10" height="10"/>`+"\n"+`</svg>`+"\n")

	out, err := readHandler(sb, ReadInput{FilePath: path})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(out.Content, "<rect") {
		t.Errorf("SVG was not returned as text: content=%q note=%q", out.Content, out.Note)
	}
	if out.TotalLines != 3 {
		t.Errorf("TotalLines = %d, want 3", out.TotalLines)
	}
}

func TestRead_NotebookRendersCells(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)

	nb := `{"cells":[
	  {"cell_type":"markdown","source":["# Title\n","\n","Some prose.\n"],"outputs":[]},
	  {"cell_type":"code","source":["print('hi')\n"],
	   "outputs":[{"output_type":"stream","name":"stdout","text":["hi\n"]}]},
	  {"cell_type":"code","source":["boom()\n"],
	   "outputs":[{"output_type":"error","ename":"NameError","evalue":"boom is not defined"}]}
	]}`
	path := writeFile(t, dir, "analysis.ipynb", nb)

	out, err := readHandler(sb, ReadInput{FilePath: path})
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	for _, want := range []string{"Cell 1 [markdown]", "# Title", "print('hi')", "NameError: boom is not defined"} {
		if !strings.Contains(out.Content, want) {
			t.Errorf("rendering is missing %q:\n%s", want, out.Content)
		}
	}
	// The raw JSON scaffolding must not survive into the output.
	if strings.Contains(out.Content, `"cell_type"`) {
		t.Error("raw notebook JSON leaked into the rendering")
	}
	if !strings.Contains(out.Note, "notebook") {
		t.Errorf("Note does not say the content is a rendering: %q", out.Note)
	}
}

// TestRead_NotebookRichOutputNamesItsFormats covers display_data cells, whose
// payload is usually a base64 image: naming the MIME types costs a line, while
// inlining the payload would cost the whole budget for something the model
// cannot see from this tool anyway.
func TestRead_NotebookRichOutputNamesItsFormats(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)

	nb := `{"cells":[{"cell_type":"code","source":["plot()\n"],` +
		`"outputs":[{"output_type":"display_data","data":{"image/png":"iVBORw0KGgo=","text/plain":"<Figure>"}}]}]}`
	path := writeFile(t, dir, "plots.ipynb", nb)

	out, err := readHandler(sb, ReadInput{FilePath: path})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.Contains(out.Content, "iVBORw0KGgo=") {
		t.Error("the base64 payload was inlined")
	}
	// Stable, sorted order so the same notebook renders the same way twice.
	if !strings.Contains(out.Content, "[output: image/png, text/plain]") {
		t.Errorf("rich output formats not named in sorted order:\n%s", out.Content)
	}
}

// TestRead_NotebookHugeOutputBecomesAPointer keeps a captured dataframe dump
// from consuming the budget the notebook's actual source needed.
func TestRead_NotebookHugeOutputBecomesAPointer(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)

	huge := strings.Repeat("0.123456789 ", 2000) // well past notebookOutputLimit
	nb := `{"cells":[{"cell_type":"code","source":["df.describe()\n"],` +
		`"outputs":[{"output_type":"stream","text":["` + huge + `"]}]}]}`
	path := writeFile(t, dir, "big.ipynb", nb)

	out, err := readHandler(sb, ReadInput{FilePath: path})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.Contains(out.Content, huge) {
		t.Error("the whole captured output was inlined")
	}
	if !strings.Contains(out.Content, "too large to inline") {
		t.Errorf("no pointer offered in place of the output:\n%s", out.Content)
	}
	if !strings.Contains(out.Content, "jq") {
		t.Error("the pointer does not say how to retrieve the output")
	}
	if !strings.Contains(out.Content, "df.describe()") {
		t.Error("the cell source was lost along with its output")
	}
}
