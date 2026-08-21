package webserver

import (
	"strings"
	"unicode/utf8"
)

// screenBuf reconstructs what a terminal shows from the raw byte stream a PTY
// emits, so something other than a browser can read the coding agent's output.
//
// It exists because a TUI's output stream is not a transcript. pi renders with
// Bubble Tea, which repaints differentially: each frame moves the cursor up
// over the lines it drew last time, jumps to the column where the line first
// changed, and rewrites from there. Stripping the escape sequences and keeping
// the bytes would therefore yield the same paragraph dozens of times, each a
// slightly different draft, with the unchanged prefixes missing — useless to a
// model asked "what did it say?". Replaying the cursor movements collapses
// those frames back into the one screen a human sees.
//
// Replaying them requires a column, not just a row. A row-only model was tried
// first and produced lines like "skills ✓ tools ✓agent... git ✓ lsp ✓": a
// repaint that seeks to column 40 and writes there appends instead of
// overwriting. The column is what makes a partial repaint land where it was
// aimed.
//
// It is still a screen model rather than a terminal: it has no scroll region,
// no alternate buffer, no tab stops beyond every eighth column, and it treats
// each drawn line as unbounded rather than wrapping at the window width. pi
// runs inline in web mode, which uses none of those.
type screenBuf struct {
	lines [][]rune
	row   int // the line the cursor is on
	col   int // the column within that line
	max   int // retained scrollback, in lines

	// pending holds an escape sequence or a UTF-8 rune that a PTY read cut in
	// half, to be completed by the next chunk. Without it a split "\x1b[2A"
	// loses the cursor move AND prints "[2A" into the screen — a corrupted
	// frame every time a read boundary lands inside a sequence, which at 32KB
	// reads happens often enough to see.
	pending []byte
}

// maxPending bounds the carry. A real escape sequence is a handful of bytes, so
// anything longer is a stream that will never complete — a lone 0x1b in binary
// output — and holding it would stall the parser for the rest of the session.
const maxPending = 64

// defaultScreenLines is how much scrollback one PTY keeps for voice reads. A
// screen is ~50 lines, so this holds several screens of history — enough for
// "what did it just do?" without letting an agent that prints a large file grow
// the buffer without bound.
const defaultScreenLines = 600

// maxScreenCols bounds one line. A runaway CUF ("\x1b[999999C") must not make
// this allocate a line of that width.
const maxScreenCols = 2000

// tabWidth is the fixed tab stop. Real terminals allow custom stops; nothing
// pi draws sets any.
const tabWidth = 8

func newScreenBuf(max int) *screenBuf {
	if max <= 0 {
		max = defaultScreenLines
	}
	return &screenBuf{lines: [][]rune{nil}, max: max}
}

// feed replays one chunk of PTY output. Chunk boundaries do not matter: a
// sequence cut in half is carried to the next call.
func (s *screenBuf) feed(b []byte) {
	if len(s.pending) > 0 {
		b = append(s.pending, b...)
		s.pending = nil
	}
	for i := 0; i < len(b); {
		c := b[i]
		switch {
		case c == 0x1b:
			n, complete := s.escape(b[i:])
			if !complete {
				i = s.hold(b, i)
				continue
			}
			i += n
		case c == '\n':
			s.row++
			s.ensureRow()
			i++
		case c == '\r':
			s.col = 0
			i++
		case c == '\b':
			if s.col > 0 {
				s.col--
			}
			i++
		case c == '\t':
			s.col = min((s.col/tabWidth+1)*tabWidth, maxScreenCols)
			i++
		case c < 0x20 || c == 0x7f:
			// Every other C0 control (BEL, the SI/SO pair a box-drawing charset
			// switch emits) has no place in a text reading of the screen.
			i++
		default:
			n, complete := s.text(b[i:])
			i += n
			if !complete {
				i = s.hold(b, i)
			}
		}
	}
	s.trim()
}

// hold stashes the truncated tail of b for the next feed and returns the index
// to resume from. A tail too long to be a real sequence is not held: its escape
// byte is dropped so the parser keeps moving.
func (s *screenBuf) hold(b []byte, i int) int {
	rest := b[i:]
	if len(rest) > maxPending {
		return i + 1
	}
	s.pending = append([]byte(nil), rest...)
	return len(b)
}

// escape consumes one escape sequence, reporting its length and whether it was
// complete. An incomplete one is carried to the next chunk by feed.
func (s *screenBuf) escape(b []byte) (int, bool) {
	if len(b) < 2 {
		return 0, false
	}
	switch b[1] {
	case '[':
		return s.csi(b)
	case ']':
		// OSC: terminated by BEL or ST (ESC \). pi sets the window title this
		// way; nothing in it belongs on screen.
		for i := 2; i < len(b); i++ {
			if b[i] == 0x07 {
				return i + 1, true
			}
			if b[i] == 0x1b && i+1 < len(b) && b[i+1] == '\\' {
				return i + 2, true
			}
		}
		return 0, false
	case '(', ')', '#', '%':
		// Charset designators: ESC ( B and friends, always two bytes of payload.
		if len(b) < 3 {
			return 0, false
		}
		return 3, true
	default:
		// Two-byte escapes: ESC 7, ESC =, ESC M and the rest. None of them
		// change what a line holds.
		return 2, true
	}
}

// csi consumes one CSI sequence, applying the cursor and erase operations that
// change what the screen holds and discarding the rest (SGR color, cursor
// visibility, bracketed-paste mode).
func (s *screenBuf) csi(b []byte) (int, bool) {
	i := 2
	start := i
	for i < len(b) && (b[i] >= 0x30 && b[i] <= 0x3f) {
		i++ // parameter bytes
	}
	params := string(b[start:i])
	for i < len(b) && (b[i] >= 0x20 && b[i] <= 0x2f) {
		i++ // intermediate bytes
	}
	if i >= len(b) {
		return 0, false // truncated by a chunk boundary
	}
	final := b[i]
	i++

	switch final {
	case 'A': // CUU — the move an inline renderer makes to redraw its frame
		s.row = max(s.row-csiNum(params, 0, 1), 0)
	case 'B', 'e': // CUD
		s.row += csiNum(params, 0, 1)
		s.ensureRow()
	case 'C', 'a': // CUF — how a differential repaint skips an unchanged prefix
		s.col = min(s.col+csiNum(params, 0, 1), maxScreenCols)
	case 'D': // CUB
		s.col = max(s.col-csiNum(params, 0, 1), 0)
	case 'G', '`': // CHA — absolute column, the other half of a partial repaint
		s.col = clampCol(csiNum(params, 0, 1) - 1)
	case 'E': // CNL
		s.row += csiNum(params, 0, 1)
		s.col = 0
		s.ensureRow()
	case 'F': // CPL
		s.row = max(s.row-csiNum(params, 0, 1), 0)
		s.col = 0
	case 'K': // EL
		s.eraseLine(csiNum(params, 0, 0))
	case 'J': // ED
		s.eraseDisplay(csiNum(params, 0, 0))
	case 'H', 'f': // CUP
		// The row is absolute against the window, which an inline renderer does
		// not have — it never emits CUP for that reason. The column is still
		// meaningful, so honor it and leave the row alone.
		s.col = clampCol(csiNum(params, 1, 1) - 1)
	case 'P': // DCH — delete characters, shifting the rest of the line left
		s.deleteChars(csiNum(params, 0, 1))
	case 'X': // ECH — blank characters in place
		s.blank(s.col, s.col+csiNum(params, 0, 1))
	}
	return i, true
}

// eraseLine applies EL with the given mode.
func (s *screenBuf) eraseLine(mode int) {
	line := s.line()
	switch mode {
	case 1: // to the cursor
		s.blank(0, s.col+1)
	case 2: // the whole line
		s.setLine(nil)
	default: // 0 — from the cursor to the end, which is what a repaint uses
		if s.col < len(line) {
			s.setLine(line[:s.col])
		}
	}
}

// eraseDisplay applies ED with the given mode.
func (s *screenBuf) eraseDisplay(mode int) {
	switch mode {
	case 2, 3: // everything
		s.lines = [][]rune{nil}
		s.row, s.col = 0, 0
	case 1: // to the cursor
		for i := 0; i < s.row && i < len(s.lines); i++ {
			s.lines[i] = nil
		}
		s.blank(0, s.col+1)
	default: // 0 — from the cursor down
		s.eraseLine(0)
		if s.row+1 < len(s.lines) {
			s.lines = s.lines[:s.row+1]
		}
	}
}

// blank overwrites [from, to) on the cursor line with spaces, without changing
// the line's length beyond what it already held.
func (s *screenBuf) blank(from, to int) {
	line := s.line()
	if from < 0 {
		from = 0
	}
	if to > len(line) {
		to = len(line)
	}
	for i := from; i < to; i++ {
		line[i] = ' '
	}
	s.setLine(line)
}

// deleteChars removes n runes at the cursor, pulling the remainder left.
func (s *screenBuf) deleteChars(n int) {
	line := s.line()
	if s.col >= len(line) || n <= 0 {
		return
	}
	end := min(s.col+n, len(line))
	s.setLine(append(line[:s.col:s.col], line[end:]...))
}

// csiNum reads the idx-th numeric parameter, falling back to def for an absent,
// empty or unparseable one — the same defaulting a terminal applies.
func csiNum(params string, idx, def int) int {
	params = strings.TrimLeft(params, "?<>=!")
	fields := strings.Split(params, ";")
	if idx >= len(fields) {
		return def
	}
	field := fields[idx]
	if i := strings.IndexByte(field, ':'); i >= 0 {
		field = field[:i] // sub-parameters, as in SGR 38:2:...
	}
	if field == "" {
		return def
	}
	n := 0
	for _, r := range field {
		if r < '0' || r > '9' {
			return def
		}
		n = n*10 + int(r-'0')
		if n > 1_000_000 {
			return def
		}
	}
	return n
}

// text writes the run of printable bytes at the head of b at the cursor,
// reporting how many bytes it consumed and whether it stopped at a complete
// boundary. It reports incomplete only for a multi-byte rune the chunk cut in
// half, which feed then carries — an invalid byte is consumed rather than held,
// since no later chunk will make it valid.
func (s *screenBuf) text(b []byte) (int, bool) {
	n := 0
	complete := true
	runes := make([]rune, 0, len(b))
	for n < len(b) {
		c := b[n]
		if c < 0x20 || c == 0x7f || c == 0x1b {
			break
		}
		if c < utf8.RuneSelf {
			runes = append(runes, rune(c))
			n++
			continue
		}
		if !utf8.RuneStart(c) {
			n++ // stray continuation byte
			continue
		}
		if n+utf8SeqLen(c) > len(b) {
			complete = false // cut by a chunk boundary
			break
		}
		r, decoded := utf8.DecodeRune(b[n:])
		if r == utf8.RuneError && decoded == 1 {
			n++ // invalid however it is read
			continue
		}
		runes = append(runes, r)
		n += decoded
	}
	if len(runes) > 0 {
		s.write(runes)
	}
	return n, complete
}

// write puts runes at the cursor, overwriting what is there and padding with
// spaces when the cursor sits past the end of the line.
func (s *screenBuf) write(runes []rune) {
	line := s.line()
	for len(line) < s.col {
		line = append(line, ' ')
	}
	for _, r := range runes {
		if s.col >= maxScreenCols {
			break
		}
		if s.col < len(line) {
			line[s.col] = r
		} else {
			line = append(line, r)
		}
		s.col++
	}
	s.setLine(line)
}

func (s *screenBuf) line() []rune {
	s.ensureRow()
	return s.lines[s.row]
}

func (s *screenBuf) setLine(v []rune) {
	s.ensureRow()
	s.lines[s.row] = v
}

func (s *screenBuf) ensureRow() {
	if s.row < 0 {
		s.row = 0
	}
	for len(s.lines) <= s.row {
		s.lines = append(s.lines, nil)
	}
}

func clampCol(c int) int {
	return min(max(c, 0), maxScreenCols)
}

// trim enforces the scrollback bound, dropping the oldest lines.
func (s *screenBuf) trim() {
	if len(s.lines) <= s.max {
		return
	}
	drop := len(s.lines) - s.max
	s.lines = append([][]rune(nil), s.lines[drop:]...)
	s.row = max(s.row-drop, 0)
}

// snapshot renders the last n lines of the screen as plain text. Trailing blank
// lines are dropped and interior blank runs are collapsed, because the reader
// is a language model being asked what the agent did, and a TUI's vertical
// padding is noise to it.
func (s *screenBuf) snapshot(n int) string {
	lines := make([]string, len(s.lines))
	for i, l := range s.lines {
		lines[i] = strings.TrimRight(string(l), " \t")
	}
	end := len(lines)
	for end > 0 && lines[end-1] == "" {
		end--
	}
	lines = lines[:end]

	out := make([]string, 0, len(lines))
	blanks := 0
	for _, l := range lines {
		if l == "" {
			blanks++
			if blanks > 1 {
				continue
			}
		} else {
			blanks = 0
		}
		out = append(out, l)
	}
	for len(out) > 0 && out[0] == "" {
		out = out[1:]
	}
	if n > 0 && len(out) > n {
		out = out[len(out)-n:]
	}
	return strings.Join(out, "\n")
}
