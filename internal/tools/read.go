package tools

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"
)

// The read tool answers to three ceilings, not one. A single line cap is not
// enough: 2000 ordinary lines and 2000 minified lines are different orders of
// magnitude, and either can be the thing that blows the context.
const (
	// defaultReadLimit bounds an ordinary large file.
	defaultReadLimit = 2000

	// readByteBudget bounds a file whose lines are exceptionally wide, where
	// the line window alone would let far too much through.
	readByteBudget = maxOutputBytes

	// maxReadLineChars bounds one line, catching minified or generated code
	// that fits the line window while exhausting the byte budget on its own.
	maxReadLineChars = 2000
)

// utf8BOM is stripped before numbering so the first line does not begin with an
// invisible character that no editor or search will match.
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// base64ImagePattern matches markdown image references with base64 data URIs
// e.g., ![Alt](data:image/png;base64,iVBORw0KG...)
var base64ImagePattern = regexp.MustCompile(`!\[([^\]]*)\]\(data:[^)]+\)`)

// stripBase64Images replaces markdown image references with base64 data URIs
// with a placeholder to reduce output size. This helps prevent LLM confusion
// when reading markdown files that contain embedded screenshots.
func stripBase64Images(content string) string {
	return base64ImagePattern.ReplaceAllString(content, "![$1](data:image/png;base64,...[stripped])")
}

// ReadInput defines the parameters for the read tool.
type ReadInput struct {
	// The absolute path to the file to read.
	FilePath string `json:"file_path"`
	// Optional line offset to start reading from (1-based). Defaults to 1.
	Offset int `json:"offset,omitempty"`
	// Optional maximum number of lines to read. 0 means up to 2000 lines.
	Limit int `json:"limit,omitempty"`
}

// ReadOutput contains the result of reading a file.
type ReadOutput struct {
	// The file content with line numbers.
	Content string `json:"content"`
	// Total number of lines in the file.
	TotalLines int `json:"total_lines"`
	// Whether the output was truncated.
	Truncated bool `json:"truncated,omitempty"`
	// NextOffset is the offset that continues exactly where this call stopped.
	// Zero when the file was read to the end.
	//
	// It is precomputed so the model never has to do pagination arithmetic —
	// getting it wrong costs a wasted turn and a re-read of the same bytes.
	NextOffset int `json:"next_offset,omitempty"`
	// Note explains an outcome the content alone does not: an empty file, an
	// offset past the end, a clamped line. Silence is the most expensive thing
	// this tool can return, because an invisible non-answer costs a turn to
	// discover and another to work around.
	Note string `json:"note,omitempty"`
}

func newReadTool(sb *Sandbox, ledger *ReadLedger) (tool.Tool, error) {
	return newTool("read", `Read a file's contents. Returns the content with line numbers.

Required: file_path (absolute path to the file).
Optional: offset (start line, 1-based), limit (max lines to read).

Large files come back a window at a time; next_offset is the exact offset to pass to continue.`,
		func(_ agent.Context, input ReadInput) (ReadOutput, error) {
			// The ledger must be threaded through here, not dropped: write
			// gates on it, so a read that does not record leaves every
			// overwrite of an existing file permanently rejected.
			return readHandlerWithLedger(sb, input, ledger)
		}, map[string]string{
			// The model reaches for all of these; repairing them costs nothing
			// and each one otherwise burns a turn on a schema error.
			"path":          "file_path",
			"filePath":      "file_path",
			"filepath":      "file_path",
			"absolutePath":  "file_path",
			"absolute_path": "file_path",
			"target_file":   "file_path",
			"targetFile":    "file_path",
			"file":          "file_path",
			"start_line":    "offset",
			"startLine":     "offset",
			"max_lines":     "limit",
			"maxLines":      "limit",
		})
}

func readHandler(sb *Sandbox, input ReadInput) (ReadOutput, error) {
	return readHandlerWithLedger(sb, input, nil)
}

func readHandlerWithLedger(sb *Sandbox, input ReadInput, ledger *ReadLedger) (ReadOutput, error) {
	if input.FilePath == "" {
		return ReadOutput{}, fmt.Errorf("file_path is required")
	}

	// A path that does not resolve as written may still be a real file spelled
	// differently — see resolveReadPath. Failing that, the error names near
	// neighbors rather than leaving the model to list the directory itself.
	path, info, err := resolveReadPath(sb, input.FilePath)
	if err != nil {
		return ReadOutput{}, err
	}
	if info.IsDir() {
		return ReadOutput{}, fmt.Errorf("%s is a directory, not a file", path)
	}

	if info.Size() == 0 {
		// An empty file has been seen in full by definition.
		ledger.Record(path, info, false)
		return ReadOutput{
			TotalLines: 0,
			Note:       fmt.Sprintf("%s is empty (0 bytes).", path),
		}, nil
	}

	// Decide what the file is before reading any of it as text, so bytes that
	// are not text never reach the transcript.
	prefix, err := readPrefix(sb, path, sniffLen)
	if err != nil {
		return ReadOutput{}, fmt.Errorf("reading file: %w", err)
	}
	kind := classifyContent(prefix, path)
	if out := describeNonText(kind, path, prefix, info.Size()); out != nil {
		return *out, nil
	}
	if kind == kindNotebook {
		return readNotebook(sb, path)
	}

	totalLines, err := countFileLines(sb, path)
	if err != nil {
		return ReadOutput{}, fmt.Errorf("reading file: %w", err)
	}

	offset := max(input.Offset, 1)
	if offset > totalLines {
		// Not an error: asking past the end is a navigation mistake, and the
		// model can fix it immediately if it is told the shape of the file.
		return ReadOutput{
			TotalLines: totalLines,
			Note: fmt.Sprintf("offset %d is past the end of %s, which has %d lines. Valid offsets are 1-%d.",
				offset, path, totalLines, totalLines),
		}, nil
	}

	limit := input.Limit
	explicitLimit := limit > 0
	if !explicitLimit {
		limit = defaultReadLimit
	}

	win, err := readWindow(sb, path, offset, limit)
	if err != nil {
		return ReadOutput{}, fmt.Errorf("reading file: %w", err)
	}

	content := stripBase64Images(win.content)

	out := ReadOutput{
		Content:    content,
		TotalLines: totalLines,
		Truncated:  win.stoppedEarly,
	}

	var notes []string
	if win.clampedLines > 0 {
		notes = append(notes, fmt.Sprintf(
			"%d line(s) longer than %d characters were clipped; the clipped part is marked inline.",
			win.clampedLines, maxReadLineChars))
	}
	if win.stoppedEarly {
		out.NextOffset = win.nextOffset
		reason := fmt.Sprintf("showing lines %d-%d of %d", offset, win.lastLine, totalLines)
		if win.hitByteBudget {
			// The resume offset is the line the budget cut, not the one after
			// it, so no line is skipped between windows.
			reason += fmt.Sprintf(" (stopped at the %dKB output budget)", readByteBudget/1024)
		}
		notes = append(notes, reason+fmt.Sprintf("; continue with offset=%d.", win.nextOffset))
		out.Content += fmt.Sprintf("\n... (truncated: %s; continue with offset=%d)", reason, win.nextOffset)
	}
	out.Note = strings.Join(notes, " ")

	// A window that stopped early, or one that started past line 1, is a
	// partial view: overwriting on the strength of it would discard lines the
	// agent never saw.
	ledger.Record(path, info, win.stoppedEarly || offset > 1)

	return out, nil
}

// readPrefix reads up to n bytes from the start of a file for classification.
func readPrefix(sb *Sandbox, path string, n int) ([]byte, error) {
	f, err := sb.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	buf := make([]byte, n)
	read, err := io.ReadFull(f, buf)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, err
	}
	return buf[:read], nil
}

// readNotebook renders a .ipynb as a document rather than returning its JSON.
func readNotebook(sb *Sandbox, path string) (ReadOutput, error) {
	data, err := sb.ReadFile(path)
	if err != nil {
		return ReadOutput{}, fmt.Errorf("reading file: %w", err)
	}
	rendered, err := renderNotebook(data)
	if err != nil {
		// A malformed notebook is still a text file; fall back rather than
		// refusing to show the reader anything at all.
		return ReadOutput{}, err
	}
	content := truncateOutput(rendered)
	return ReadOutput{
		Content:    content,
		TotalLines: strings.Count(rendered, "\n"),
		Truncated:  len(content) < len(rendered),
		Note: fmt.Sprintf("%s is a Jupyter notebook, rendered as cells. "+
			"Line numbers refer to this rendering, not to the underlying JSON.", path),
	}, nil
}

// window is the result of formatting one slice of a file.
type window struct {
	content       string
	lastLine      int  // last line number included
	nextOffset    int  // offset that resumes exactly after this window
	stoppedEarly  bool // a ceiling stopped the read before the end of the file
	hitByteBudget bool
	clampedLines  int
}

// readWindow streams the requested slice of the file, applying all three
// ceilings as it goes.
//
// It streams rather than reading the file into memory and splitting it: a
// single multi-megabyte line — minified JS, an embedded blob — would otherwise
// be fully materialized before the window that was going to discard it is ever
// applied.
func readWindow(sb *Sandbox, path string, offset, limit int) (window, error) {
	f, err := sb.Open(path)
	if err != nil {
		return window{}, err
	}
	defer f.Close()

	r := bufio.NewReaderSize(f, 64*1024)

	var (
		b    strings.Builder
		w    window
		line int
	)
	w.lastLine = offset - 1

	for {
		text, clipped, readErr := readLineClamped(r, maxReadLineChars)
		atEOF := errors.Is(readErr, io.EOF)
		if readErr != nil && !atEOF {
			return window{}, readErr
		}
		if text == "" && clipped == 0 && atEOF && line >= 1 {
			break
		}
		line++

		if line == 1 {
			text = strings.TrimPrefix(text, string(utf8BOM))
		}
		// CRLF files are numbered like their LF equivalents; a stray \r would
		// otherwise land inside every line and defeat exact-match editing.
		text = strings.TrimSuffix(text, "\r")

		if line >= offset {
			if clipped > 0 {
				text += fmt.Sprintf("… [%d more characters on this line, clipped]", clipped)
				w.clampedLines++
			}
			// Check the byte budget before committing the line, so the budget
			// is a ceiling rather than a target that is always overshot.
			if b.Len() > 0 && b.Len()+len(text) > readByteBudget {
				w.stoppedEarly = true
				w.hitByteBudget = true
				w.nextOffset = line // resume on the line that did not fit
				return finishWindow(&b, w), nil
			}
			fmt.Fprintf(&b, "%6d\t%s\n", line, text)
			w.lastLine = line

			if line-offset+1 >= limit {
				// Filling the window is not the same as there being more to
				// read. A limit that lands exactly on the last line has not
				// truncated anything, and saying otherwise sends the model
				// after a page that does not exist.
				if !atEOF && moreToRead(r) {
					w.stoppedEarly = true
					w.nextOffset = line + 1
				}
				return finishWindow(&b, w), nil
			}
		}

		if atEOF {
			break
		}
	}

	return finishWindow(&b, w), nil
}

func finishWindow(b *strings.Builder, w window) window {
	w.content = b.String()
	return w
}

// moreToRead reports whether any byte remains after the current position.
func moreToRead(r *bufio.Reader) bool {
	_, err := r.Peek(1)
	return err == nil
}

// readLineClamped reads one line, keeping at most clamp bytes of it and
// reporting how many bytes past the clamp were discarded.
//
// The discarded bytes are never accumulated, so a 5MB single line costs the
// clamp, not 5MB.
func readLineClamped(r *bufio.Reader, clamp int) (string, int, error) {
	var (
		kept    []byte
		clipped int
	)
	for {
		chunk, err := r.ReadSlice('\n')
		switch {
		case err == nil:
			kept, clipped = appendClamped(kept, chunk[:len(chunk)-1], clamp, clipped)
			return string(kept), clipped, nil
		case errors.Is(err, bufio.ErrBufferFull):
			// Line is longer than the read buffer; keep consuming it.
			kept, clipped = appendClamped(kept, chunk, clamp, clipped)
		default:
			kept, clipped = appendClamped(kept, chunk, clamp, clipped)
			return string(kept), clipped, err
		}
	}
}

// appendClamped copies as much of src into dst as the clamp allows, counting
// the rest as clipped.
func appendClamped(dst, src []byte, clamp, clipped int) ([]byte, int) {
	if room := clamp - len(dst); room > 0 {
		if room > len(src) {
			room = len(src)
		}
		dst = append(dst, src[:room]...)
		src = src[room:]
	}
	return dst, clipped + len(src)
}

// countFileLines counts lines the way an editor does: a trailing newline
// terminates the last line rather than starting an empty one.
//
// Splitting on "\n" and taking len() — what this replaced — reports one line
// too many for every file that ends in a newline, which is nearly all of them.
// The model paginates on this number, so the error was not cosmetic.
func countFileLines(sb *Sandbox, path string) (int, error) {
	f, err := sb.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	var (
		buf      = make([]byte, 64*1024)
		lines    int
		lastByte byte
		any      bool
	)
	for {
		n, readErr := f.Read(buf)
		if n > 0 {
			any = true
			lines += bytes.Count(buf[:n], []byte{'\n'})
			lastByte = buf[n-1]
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return 0, readErr
		}
	}
	if !any {
		return 0, nil
	}
	if lastByte != '\n' {
		lines++ // the file ends mid-line; that partial line still counts
	}
	return lines, nil
}
