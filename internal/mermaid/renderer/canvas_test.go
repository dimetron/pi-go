package renderer

import (
	"strings"
	"testing"
)

func TestNewCanvas(t *testing.T) {
	c := NewCanvas(10, 5)
	if c.Width != 10 || c.Height != 5 {
		t.Errorf("expected 10x5, got %dx%d", c.Width, c.Height)
	}
	// All spaces
	for row := range c.Height {
		for col := range c.Width {
			if ch := c.Get(row, col); ch != ' ' {
				t.Errorf("expected space at (%d,%d), got %c", row, col, ch)
			}
		}
	}
}

func TestPutAndGet(t *testing.T) {
	c := NewCanvas(10, 5)
	c.Put(2, 3, 'X', false, "")
	if ch := c.Get(2, 3); ch != 'X' {
		t.Errorf("expected X, got %c", ch)
	}
}

func TestPutSkipsSpaces(t *testing.T) {
	c := NewCanvas(10, 5)
	c.Put(0, 0, 'A', false, "")
	c.Put(0, 0, ' ', false, "")
	if ch := c.Get(0, 0); ch != 'A' {
		t.Errorf("space should not overwrite, got %c", ch)
	}
}

func TestPutOutOfBounds(t *testing.T) {
	c := NewCanvas(5, 5)
	c.Put(-1, 0, 'X', false, "")
	c.Put(0, -1, 'X', false, "")
	c.Put(5, 0, 'X', false, "")
	c.Put(0, 5, 'X', false, "")
	// Should not panic
}

func TestJunctionMerging(t *testing.T) {
	c := NewCanvas(5, 5)
	c.Put(2, 2, '─', true, "")
	c.Put(2, 2, '│', true, "")
	if ch := c.Get(2, 2); ch != '┼' {
		t.Errorf("expected ┼ from merge, got %c", ch)
	}
}

func TestJunctionCorners(t *testing.T) {
	tests := []struct {
		a, b, want rune
	}{
		{'─', '┌', '┬'},
		{'│', '┌', '├'},
		{'┌', '┘', '┼'},
	}
	for _, tt := range tests {
		c := NewCanvas(3, 3)
		c.Put(1, 1, tt.a, true, "")
		c.Put(1, 1, tt.b, true, "")
		if got := c.Get(1, 1); got != tt.want {
			t.Errorf("merge(%c, %c) = %c, want %c", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestPutText(t *testing.T) {
	c := NewCanvas(20, 3)
	c.PutText(1, 2, "hello", "")
	for i, ch := range "hello" {
		if got := c.Get(1, 2+i); got != ch {
			t.Errorf("pos %d: expected %c, got %c", i, ch, got)
		}
	}
}

func TestDrawHorizontal(t *testing.T) {
	c := NewCanvas(10, 3)
	c.DrawHorizontal(1, 2, 7, '─', "")
	for col := 2; col <= 7; col++ {
		if ch := c.Get(1, col); ch != '─' {
			t.Errorf("col %d: expected ─, got %c", col, ch)
		}
	}
}

func TestDrawVertical(t *testing.T) {
	c := NewCanvas(5, 10)
	c.DrawVertical(2, 1, 6, '│', "")
	for row := 1; row <= 6; row++ {
		if ch := c.Get(row, 2); ch != '│' {
			t.Errorf("row %d: expected │, got %c", row, ch)
		}
	}
}

func TestToString(t *testing.T) {
	c := NewCanvas(5, 3)
	c.Put(0, 0, 'A', false, "")
	c.Put(1, 1, 'B', false, "")
	s := c.ToString()
	lines := strings.Split(s, "\n")
	if len(lines) != 2 {
		t.Errorf("expected 2 non-empty lines, got %d", len(lines))
	}
	if lines[0] != "A" {
		t.Errorf("line 0: expected 'A', got %q", lines[0])
	}
	if lines[1] != " B" {
		t.Errorf("line 1: expected ' B', got %q", lines[1])
	}
}

func TestResize(t *testing.T) {
	c := NewCanvas(5, 5)
	c.Put(2, 2, 'X', false, "")
	c.Resize(10, 10)
	if c.Width != 10 || c.Height != 10 {
		t.Errorf("expected 10x10 after resize, got %dx%d", c.Width, c.Height)
	}
	if ch := c.Get(2, 2); ch != 'X' {
		t.Errorf("content lost after resize")
	}
}

func TestResizeNoOp(t *testing.T) {
	c := NewCanvas(10, 10)
	c.Resize(5, 5) // smaller, should be no-op
	if c.Width != 10 || c.Height != 10 {
		t.Errorf("resize to smaller should be no-op")
	}
}

func TestFlipVertical(t *testing.T) {
	c := NewCanvas(3, 3)
	c.Put(0, 1, '▼', false, "")
	c.FlipVertical()
	if ch := c.Get(2, 1); ch != '▲' {
		t.Errorf("expected ▲ after flip, got %c", ch)
	}
}

func TestFlipHorizontal(t *testing.T) {
	c := NewCanvas(5, 3)
	c.Put(1, 0, '►', false, "")
	c.FlipHorizontal()
	if ch := c.Get(1, 4); ch != '◄' {
		t.Errorf("expected ◄ after flip, got %c", ch)
	}
}

func TestClearCell(t *testing.T) {
	c := NewCanvas(5, 5)
	c.Put(2, 2, 'X', false, "test")
	c.ClearCell(2, 2)
	if ch := c.Get(2, 2); ch != ' ' {
		t.Errorf("expected space after clear, got %c", ch)
	}
	if s := c.GetStyle(2, 2); s != "default" {
		t.Errorf("expected default style after clear, got %s", s)
	}
}

func TestStyle(t *testing.T) {
	c := NewCanvas(5, 5)
	c.Put(1, 1, 'A', false, "myStyle")
	if s := c.GetStyle(1, 1); s != "myStyle" {
		t.Errorf("expected myStyle, got %s", s)
	}
}
