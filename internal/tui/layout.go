package tui

import (
	"path/filepath"
	"strings"
)

// Breakpoint represents terminal width categories.
type Breakpoint int

const (
	BreakpointNarrow Breakpoint = iota // < 60 cols
	BreakpointNormal                   // 60-100 cols
	BreakpointWide                     // > 100 cols
)

// Layout holds terminal dimensions and the computed breakpoint.
type Layout struct {
	Width      int
	Height     int
	Breakpoint Breakpoint
}

// NewLayout creates a Layout from terminal dimensions.
func NewLayout(w, h int) Layout {
	bp := BreakpointNormal
	switch {
	case w < 60:
		bp = BreakpointNarrow
	case w > 100:
		bp = BreakpointWide
	}
	return Layout{Width: w, Height: h, Breakpoint: bp}
}

// TruncatePath shortens a file path to fit within max characters.
// It preserves the filename and as much of the leading path as possible,
// inserting "..." when truncation is needed.
func (l Layout) TruncatePath(path string, max int) string {
	if max <= 0 {
		max = l.MaxPathLen()
	}
	if len(path) <= max {
		return path
	}
	if max <= 3 {
		return path[:max]
	}

	base := filepath.Base(path)
	if len(base) >= max {
		return base[:max-3] + "..."
	}

	// Show ".../<base>" if the base fits with ellipsis prefix.
	prefix := max - len(base) - 4 // room for ".../""
	if prefix <= 0 {
		return ".../" + base
	}

	dir := filepath.Dir(path)
	// Take leading portion of the directory.
	leading := dir
	if len(leading) > prefix {
		leading = leading[:prefix]
		// Trim to last separator for cleaner output.
		if idx := strings.LastIndex(leading, string(filepath.Separator)); idx > 0 {
			leading = leading[:idx]
		}
	}
	return leading + "/.../" + base
}

// StatusWidth returns the available width for the status bar content.
func (l Layout) StatusWidth() int {
	w := l.Width - 4 // side padding
	if w < 20 {
		return 20
	}
	return w
}

// ChatWidth returns the available width for chat message content.
func (l Layout) ChatWidth() int {
	w := l.Width - 4 // side padding
	if w < 40 {
		return 40
	}
	return w
}

// MaxPathLen returns the maximum path display length for the current breakpoint.
func (l Layout) MaxPathLen() int {
	switch l.Breakpoint {
	case BreakpointNarrow:
		return 30
	case BreakpointWide:
		return 80
	default:
		return 50
	}
}

// IsNarrow returns true if the terminal is in narrow mode.
func (l Layout) IsNarrow() bool {
	return l.Breakpoint == BreakpointNarrow
}

// SeparatorWidth returns the width for horizontal separators.
func (l Layout) SeparatorWidth() int {
	w := l.Width - 4
	if w < 20 {
		return 20
	}
	return w
}
