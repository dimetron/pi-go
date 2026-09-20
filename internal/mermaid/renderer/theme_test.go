package renderer

import (
	"strings"
	"testing"
)

// The theme helpers pick ANSI colors from a palette, and the depth-aware ones
// are the only path used by treemaps and gantt charts. They had no tests, so a
// palette index change or a dropped ceiling would only show up as a rendering
// difference nobody diffs.
func TestThemeRegionStyles(t *testing.T) {
	plain := buildTheme("plain", "cyan", "white", "white", "cyan", "white", "white", "cyan", "", "")
	deep := buildThemeWithDepth("deep", "cyan", "white", "white", "cyan", "white", "white", "cyan", "", "")

	if plain.HasDepthColors() {
		t.Error("buildTheme produced a depth-aware theme")
	}
	if !deep.HasDepthColors() {
		t.Error("buildThemeWithDepth did not mark the theme depth-aware")
	}

	// A theme without depth colors must render no escape sequence at all: the
	// callers concatenate this with "_ansi:" and an empty string is what makes
	// the plain output stay plain.
	depthStyles := map[string]func(section, depth int) string{
		"RegionStyle":       deep.RegionStyle,
		"RegionBorderStyle": deep.RegionBorderStyle,
		"RegionLabelStyle":  deep.RegionLabelStyle,
		"RegionTextStyle":   deep.RegionTextStyle,
		"RegionBarStyle":    deep.RegionBarStyle,
	}
	plainStyles := map[string]func(section, depth int) string{
		"RegionStyle":       plain.RegionStyle,
		"RegionBorderStyle": plain.RegionBorderStyle,
		"RegionLabelStyle":  plain.RegionLabelStyle,
		"RegionTextStyle":   plain.RegionTextStyle,
		"RegionBarStyle":    plain.RegionBarStyle,
	}
	for name, fn := range plainStyles {
		if got := fn(0, 0); got != "" {
			t.Errorf("%s on a plain theme = %q, want empty", name, got)
		}
	}
	for name, fn := range depthStyles {
		if got := fn(0, 0); !strings.HasPrefix(got, "\033[") {
			t.Errorf("%s on a depth theme = %q, want an ANSI sequence", name, got)
		}
	}

	// Depth lightens: the deep step must differ from the shallow one, or nesting
	// would be invisible.
	if deep.RegionStyle(0, 0) == deep.RegionStyle(0, 3) {
		t.Error("RegionStyle produced the same color at depth 0 and depth 3")
	}

	// Section index wraps the palette rather than panicking. 8 is len(regionPalette).
	if got, wrapped := deep.RegionStyle(8, 0), deep.RegionStyle(0, 0); got != wrapped {
		t.Errorf("section 8 = %q, want it to wrap to section 0 = %q", got, wrapped)
	}
}

// Colors are clamped so a large depth cannot emit an out-of-range channel; the
// callers rely on the value being renderable, not merely different.
//
// The two styles clamp different channels, so each has its own ceiling:
// RegionStyle is white fg (fixed) on the region bg, capped at 235, while
// RegionBarStyle is the region fg capped at 235 with a bg 50 lighter, capped at
// 255. Past the ceiling every channel sits at its cap.
func TestThemeRegionStyleClampsChannels(t *testing.T) {
	deep := buildThemeWithDepth("deep", "cyan", "white", "white", "cyan", "white", "white", "cyan", "", "")

	tests := map[string]struct{ got, want string }{
		"RegionStyle": {
			got:  deep.RegionStyle(0, 1000),
			want: "\033[38;2;255;255;255m\033[48;2;235;235;235m",
		},
		"RegionBarStyle": {
			got:  deep.RegionBarStyle(0, 1000),
			want: "\033[38;2;235;235;235m\033[48;2;255;255;255m",
		},
		"RegionBorderStyle": {
			got:  deep.RegionBorderStyle(0, 1000),
			want: "\033[38;2;255;255;255m\033[48;2;235;235;235m",
		},
		"RegionTextStyle": {
			got:  deep.RegionTextStyle(0, 1000),
			want: "\033[38;2;255;255;255m",
		},
	}
	for name, tc := range tests {
		if tc.got != tc.want {
			t.Errorf("%s at depth 1000 = %q, want %q", name, tc.got, tc.want)
		}
	}
}

// RegionTextStyle bolds the top level so a section header reads as a heading,
// and drops the bold deeper in where everything would otherwise shout.
func TestThemeRegionTextStyleBoldsOnlyTopLevel(t *testing.T) {
	deep := buildThemeWithDepth("deep", "cyan", "white", "white", "cyan", "white", "white", "cyan", "", "")

	if got := deep.RegionTextStyle(0, 0); !strings.HasPrefix(got, "\033[1m") {
		t.Errorf("depth 0 = %q, want a bold prefix", got)
	}
	if got := deep.RegionTextStyle(0, 1); strings.HasPrefix(got, "\033[1m") {
		t.Errorf("depth 1 = %q, want no bold prefix", got)
	}
}

func TestThemePieColors(t *testing.T) {
	plain := buildTheme("plain", "cyan", "white", "white", "cyan", "white", "white", "cyan", "", "")
	if plain.HasPieBase() {
		t.Error("buildTheme produced a theme with a pie base color")
	}
	if got := plain.PieColors(3); got != nil {
		t.Errorf("PieColors on a theme without a base = %v, want nil", got)
	}

	mono := buildThemeMono("mono", "cyan", "white", "white", "cyan", "white", "white", "cyan", "", "", 0xD4, 0x84, 0x5A)
	if !mono.HasPieBase() {
		t.Error("buildThemeMono did not mark the theme as having a pie base")
	}

	// Requesting fewer than two slices is clamped to two: a single slice has no
	// brightness spread to compute, and n-1 would divide by zero.
	for _, n := range []int{-1, 0, 1} {
		if got := mono.PieColors(n); len(got) != 2 {
			t.Errorf("PieColors(%d) returned %d shades, want 2", n, len(got))
		}
	}

	shades := mono.PieColors(4)
	if len(shades) != 4 {
		t.Fatalf("PieColors(4) returned %d shades, want 4", len(shades))
	}
	// Shades spread from 35% to 100%, so they must increase and end at the base.
	for i := 1; i < len(shades); i++ {
		if shades[i][0] < shades[i-1][0] {
			t.Errorf("shade %d (%v) is darker than shade %d (%v)", i, shades[i], i-1, shades[i-1])
		}
	}
	if last := shades[len(shades)-1]; last[0] != 0xD4 || last[1] != 0x84 || last[2] != 0x5A {
		t.Errorf("brightest shade = %v, want the base color {212 132 90}", last)
	}
}

func TestParseHex(t *testing.T) {
	tests := []struct {
		in      string
		r, g, b int
	}{
		{"#D4845A", 0xD4, 0x84, 0x5A},
		{"D4845A", 0xD4, 0x84, 0x5A},
		{"#000000", 0, 0, 0},
		{"#FFFFFF", 255, 255, 255},
		{"#f5e6d3", 0xF5, 0xE6, 0xD3},
	}
	for _, tc := range tests {
		r, g, b := parseHex(tc.in)
		if r != tc.r || g != tc.g || b != tc.b {
			t.Errorf("parseHex(%q) = (%d,%d,%d), want (%d,%d,%d)", tc.in, r, g, b, tc.r, tc.g, tc.b)
		}
	}

	// A short or empty value must not panic on the slice indexing.
	for _, in := range []string{"", "#", "#12", "zzzzzz"} {
		func() {
			defer func() {
				if p := recover(); p != nil {
					t.Errorf("parseHex(%q) panicked: %v", in, p)
				}
			}()
			parseHex(in)
		}()
	}
}
