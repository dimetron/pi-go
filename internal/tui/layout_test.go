package tui

import (
	"testing"
)

func TestNewLayout_Breakpoints(t *testing.T) {
	tests := []struct {
		width int
		want  Breakpoint
	}{
		{30, BreakpointNarrow},
		{59, BreakpointNarrow},
		{60, BreakpointNormal},
		{80, BreakpointNormal},
		{100, BreakpointNormal},
		{101, BreakpointWide},
		{200, BreakpointWide},
	}
	for _, tt := range tests {
		l := NewLayout(tt.width, 40)
		if l.Breakpoint != tt.want {
			t.Errorf("NewLayout(%d, 40).Breakpoint = %d, want %d", tt.width, l.Breakpoint, tt.want)
		}
	}
}

func TestLayout_MaxPathLen(t *testing.T) {
	tests := []struct {
		width int
		want  int
	}{
		{40, 30},  // narrow
		{80, 50},  // normal
		{120, 80}, // wide
	}
	for _, tt := range tests {
		l := NewLayout(tt.width, 40)
		if got := l.MaxPathLen(); got != tt.want {
			t.Errorf("NewLayout(%d).MaxPathLen() = %d, want %d", tt.width, got, tt.want)
		}
	}
}

func TestLayout_TruncatePath_Short(t *testing.T) {
	l := NewLayout(80, 40)
	path := "src/main.go"
	got := l.TruncatePath(path, 50)
	if got != path {
		t.Errorf("TruncatePath(%q, 50) = %q, want %q", path, got, path)
	}
}

func TestLayout_TruncatePath_Long(t *testing.T) {
	l := NewLayout(80, 40)
	path := "internal/very/deeply/nested/package/directory/structure/file.go"
	got := l.TruncatePath(path, 30)
	if len(got) > 30 {
		t.Errorf("TruncatePath(%q, 30) len = %d, want <= 30; got %q", path, len(got), got)
	}
	// Should contain the filename
	if got[len(got)-7:] != "file.go" {
		t.Errorf("TruncatePath should preserve filename, got %q", got)
	}
}

func TestLayout_TruncatePath_DefaultMax(t *testing.T) {
	l := NewLayout(40, 40) // narrow -> MaxPathLen=30
	path := "internal/very/deeply/nested/package/directory/structure/file.go"
	got := l.TruncatePath(path, 0) // 0 means use MaxPathLen
	if len(got) > 30 {
		t.Errorf("TruncatePath with default max: len = %d, want <= 30; got %q", len(got), got)
	}
}

func TestLayout_TruncatePath_VeryShortMax(t *testing.T) {
	l := NewLayout(80, 40)
	path := "internal/pkg/file.go"
	got := l.TruncatePath(path, 3)
	if len(got) > 3 {
		t.Errorf("TruncatePath(%q, 3) len = %d, want <= 3; got %q", path, len(got), got)
	}
}

func TestLayout_TruncatePath_LongFilename(t *testing.T) {
	l := NewLayout(80, 40)
	path := "src/very_long_filename_that_exceeds_max.go"
	got := l.TruncatePath(path, 20)
	if len(got) > 20 {
		t.Errorf("TruncatePath with long filename: len = %d, want <= 20; got %q", len(got), got)
	}
}

func TestLayout_StatusWidth(t *testing.T) {
	tests := []struct {
		width int
		want  int
	}{
		{10, 20},  // too small, clamp to 20
		{80, 76},  // normal
		{120, 116}, // wide
	}
	for _, tt := range tests {
		l := NewLayout(tt.width, 40)
		if got := l.StatusWidth(); got != tt.want {
			t.Errorf("NewLayout(%d).StatusWidth() = %d, want %d", tt.width, got, tt.want)
		}
	}
}

func TestLayout_ChatWidth(t *testing.T) {
	tests := []struct {
		width int
		want  int
	}{
		{10, 40},  // too small, clamp to 40
		{80, 76},  // normal
		{120, 116}, // wide
	}
	for _, tt := range tests {
		l := NewLayout(tt.width, 40)
		if got := l.ChatWidth(); got != tt.want {
			t.Errorf("NewLayout(%d).ChatWidth() = %d, want %d", tt.width, got, tt.want)
		}
	}
}

func TestLayout_IsNarrow(t *testing.T) {
	if !NewLayout(40, 40).IsNarrow() {
		t.Error("NewLayout(40).IsNarrow() should be true")
	}
	if NewLayout(80, 40).IsNarrow() {
		t.Error("NewLayout(80).IsNarrow() should be false")
	}
}

func TestLayout_SeparatorWidth(t *testing.T) {
	l := NewLayout(80, 40)
	if got := l.SeparatorWidth(); got != 76 {
		t.Errorf("SeparatorWidth() = %d, want 76", got)
	}
	l = NewLayout(10, 40)
	if got := l.SeparatorWidth(); got != 20 {
		t.Errorf("SeparatorWidth() = %d, want 20 (clamped)", got)
	}
}

func TestLayout_DimensionsPreserved(t *testing.T) {
	l := NewLayout(120, 50)
	if l.Width != 120 {
		t.Errorf("Width = %d, want 120", l.Width)
	}
	if l.Height != 50 {
		t.Errorf("Height = %d, want 50", l.Height)
	}
}
