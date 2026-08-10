package tools

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// sniffLen is how much of a file is inspected to classify it. net/http uses the
// same figure, and it is more than enough for every magic number below.
const sniffLen = 512

// notebookOutputLimit bounds one notebook cell's captured output. A single
// dataframe dump or a training log can be larger than the notebook's entire
// source, and it is almost never what the reader came for.
const notebookOutputLimit = 10_000

// contentKind is how the read tool decides what a file *is*.
//
// Classification is by magic bytes, never by extension: an extension is a claim
// by whoever named the file, and acting on it is how garbage bytes reach the
// provider dressed as a .png, or how a perfectly readable file gets refused for
// having the wrong suffix.
type contentKind int

const (
	kindText contentKind = iota
	kindImage
	kindPDF
	kindNotebook
	kindBinary
)

// classifyContent inspects a prefix of the file and its name, in that order of
// authority.
func classifyContent(prefix []byte, path string) contentKind {
	switch {
	case len(prefix) == 0:
		return kindText
	case bytes.HasPrefix(prefix, []byte("%PDF-")):
		return kindPDF
	case isImagePrefix(prefix):
		return kindImage
	}

	// SVG is XML: it is text, it is editable, and refusing it as "an image"
	// would be actively unhelpful. It is checked before the binary sniff
	// because its magic is a string, not a byte pattern.
	if looksLikeSVG(prefix) {
		return kindText
	}

	if isBinaryPrefix(prefix) {
		return kindBinary
	}

	// A notebook is valid JSON text; the extension only decides whether to
	// render it as a document rather than dump the raw JSON.
	if strings.EqualFold(pathExt(path), ".ipynb") {
		return kindNotebook
	}
	return kindText
}

func pathExt(path string) string {
	if i := strings.LastIndex(path, "."); i >= 0 {
		return path[i:]
	}
	return ""
}

func isImagePrefix(prefix []byte) bool {
	return strings.HasPrefix(http.DetectContentType(prefix), "image/")
}

func looksLikeSVG(prefix []byte) bool {
	head := strings.ToLower(string(prefix))
	return strings.Contains(head, "<svg") ||
		(strings.Contains(head, "<?xml") && strings.Contains(head, "svg"))
}

// isBinaryPrefix reports whether the prefix contains a NUL byte, the oldest and
// still most reliable text/binary discriminator: no text encoding this tool
// will meet embeds a NUL in ordinary content.
func isBinaryPrefix(prefix []byte) bool {
	return bytes.IndexByte(prefix, 0) >= 0
}

// describeNonText builds the answer for a file whose bytes must not be returned
// verbatim, or "" when the file is ordinary text.
//
// Returning the bytes is not an option: a binary dumped into the transcript is
// pure cost — it cannot be read, cannot be edited, and displaces context that
// could have held something useful.
func describeNonText(kind contentKind, path string, prefix []byte, size int64) *ReadOutput {
	switch kind {
	case kindImage:
		return &ReadOutput{
			Note: fmt.Sprintf("%s is an image (%s, %d bytes). Use the read_image tool to actually see it — "+
				"this tool would only return bytes you cannot read.",
				path, http.DetectContentType(prefix), size),
		}
	case kindPDF:
		return &ReadOutput{
			Note: fmt.Sprintf("%s is a PDF (%d bytes), so it has no text lines to number. "+
				"Extract the text first: `pdftotext %q -` in the bash tool.",
				path, size, path),
		}
	case kindBinary:
		return &ReadOutput{
			Note: fmt.Sprintf("%s is a binary file (%s, %d bytes) and has no text to show. "+
				"Inspect it with a tool that understands the format (file, xxd, strings) via bash.",
				path, http.DetectContentType(prefix), size),
		}
	case kindText, kindNotebook:
		return nil
	}
	return nil
}

// notebook is the subset of the .ipynb schema worth rendering.
type notebook struct {
	Cells []struct {
		CellType string          `json:"cell_type"`
		Source   json.RawMessage `json:"source"`
		Outputs  []struct {
			OutputType string          `json:"output_type"`
			Text       json.RawMessage `json:"text"`
			Name       string          `json:"name"`
			EName      string          `json:"ename"`
			EValue     string          `json:"evalue"`
			Data       map[string]any  `json:"data"`
		} `json:"outputs"`
	} `json:"cells"`
}

// renderNotebook turns a .ipynb into something a reader can actually follow.
//
// The raw JSON is close to unreadable and mostly not the content: escaped
// source strings, base64 image payloads and captured dataframe dumps. Rendering
// cells as a document costs a fraction of the tokens and answers the question
// the reader actually had.
func renderNotebook(data []byte) (string, error) {
	var nb notebook
	if err := json.Unmarshal(data, &nb); err != nil {
		return "", fmt.Errorf("parsing notebook: %w", err)
	}

	var b strings.Builder
	for i, cell := range nb.Cells {
		fmt.Fprintf(&b, "### Cell %d [%s]\n", i+1, cell.CellType)
		b.WriteString(joinNotebookSource(cell.Source))
		b.WriteString("\n")

		for _, out := range cell.Outputs {
			switch {
			case out.EName != "":
				fmt.Fprintf(&b, "[output: %s: %s]\n", out.EName, out.EValue)
			case len(out.Text) > 0:
				text := joinNotebookSource(out.Text)
				if len(text) > notebookOutputLimit {
					// A pointer, not the payload: the reader can go get it if
					// it turns out to matter.
					fmt.Fprintf(&b, "[output: %d chars captured, too large to inline — "+
						"read it with: jq -r '.cells[%d].outputs[].text' <file>]\n", len(text), i)
					continue
				}
				fmt.Fprintf(&b, "[output]\n%s\n", text)
			case len(out.Data) > 0:
				fmt.Fprintf(&b, "[output: %s]\n", strings.Join(sortedKeys(out.Data), ", "))
			}
		}
		b.WriteString("\n")
	}
	return b.String(), nil
}

// joinNotebookSource accepts both shapes the schema allows for a source field:
// a list of lines, or one string.
func joinNotebookSource(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var lines []string
	if err := json.Unmarshal(raw, &lines); err == nil {
		return strings.Join(lines, "")
	}
	var single string
	if err := json.Unmarshal(raw, &single); err == nil {
		return single
	}
	return ""
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// Small maps; a simple insertion sort keeps the output stable without
	// pulling in a dependency on ordering elsewhere.
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}
