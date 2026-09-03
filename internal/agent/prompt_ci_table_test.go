package agent

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// ciTableExample returns the box-drawing table lines from the CI status tables
// section of SystemInstruction, with the example's leading indent stripped.
func ciTableExample(t *testing.T) []string {
	t.Helper()
	const heading = "# CI status tables"
	i := strings.Index(SystemInstruction, heading)
	if i < 0 {
		t.Fatalf("SystemInstruction has no %q section", heading)
	}
	section := SystemInstruction[i:]
	if j := strings.Index(section[len(heading):], "\n# "); j >= 0 {
		section = section[:len(heading)+j]
	}
	var rows []string
	for _, line := range strings.Split(section, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" && strings.ContainsAny(trimmed, "┌├└│") {
			rows = append(rows, trimmed)
		}
	}
	if len(rows) == 0 {
		t.Fatal("CI status tables section contains no box-drawing example")
	}
	return rows
}

// TestSystemInstruction_CITableSectionPresent pins the instruction that CI
// status is reported as a box-drawing table rather than a markdown one, since
// the TUI renders responses as text and a markdown table arrives as raw pipes.
func TestSystemInstruction_CITableSectionPresent(t *testing.T) {
	for _, want := range []string{
		"# CI status tables",
		"✅ pass",
		"❌ fail",
		"⏳ running",
	} {
		if !strings.Contains(SystemInstruction, want) {
			t.Errorf("SystemInstruction missing %q", want)
		}
	}
	if !strings.Contains(SystemInstruction, "except for CI and check status") {
		t.Error("the Diagrams section must point at the CI status table format")
	}
}

// TestSystemInstruction_CITableExampleAligns checks the worked example is
// actually a well-formed grid. The example is what the model copies, so a
// misaligned one teaches misalignment — and emoji are two columns wide, which
// is exactly the mistake this catches.
func TestSystemInstruction_CITableExampleAligns(t *testing.T) {
	rows := ciTableExample(t)

	width := ansi.StringWidth(rows[0])
	for i, row := range rows {
		if got := ansi.StringWidth(row); got != width {
			t.Errorf("row %d width = %d, want %d (all rows must be equal width)\n%s", i, got, width, row)
		}
	}

	// Every row must place its column rules at the same display columns.
	want := ruleColumns(rows[0])
	for i, row := range rows {
		if got := ruleColumns(row); !equalInts(got, want) {
			t.Errorf("row %d rules at columns %v, want %v\n%s", i, got, want, row)
		}
	}
}

// ruleColumns returns the display columns at which a table row places a
// vertical rule of any kind.
func ruleColumns(row string) []int {
	var cols []int
	col := 0
	for _, r := range row {
		if strings.ContainsRune("│┌┬┐├┼┤└┴┘", r) {
			cols = append(cols, col)
		}
		col += ansi.StringWidth(string(r))
	}
	return cols
}

func equalInts(a, b []int) bool {
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
