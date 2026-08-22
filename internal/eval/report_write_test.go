package eval

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// TestSparkline_Empty: nil and empty series produce no output.
func TestSparkline_Empty(t *testing.T) {
	if got := sparkline(nil); got != "" {
		t.Errorf("sparkline(nil) = %q, want empty", got)
	}
	if got := sparkline([]ConcurrencySample{}); got != "" {
		t.Errorf("sparkline(empty) = %q, want empty", got)
	}
}

// TestSparkline_UsesValidUnicodeLevels is the core fix regression: the levels
// string is UTF-8 (8 runes), and indexing by byte produced invalid UTF-8.
func TestSparkline_UsesValidUnicodeLevels(t *testing.T) {
	samples := []ConcurrencySample{
		{Running: 0}, {Running: 1}, {Running: 2}, {Running: 4}, {Running: 8},
	}
	got := sparkline(samples)

	if !utf8.ValidString(got) {
		t.Fatalf("sparkline output is not valid UTF-8: %q", got)
	}

	// The graph glyphs should be exactly 5 runes before the annotation.
	body := strings.TrimSuffix(got, "  (max 8, 5 samples)")
	graph := []rune(body)
	if len(graph) != 5 {
		t.Fatalf("graph has %d runes, want 5: %q", len(graph), body)
	}
	// Scale 0..8 over 8 levels: 0→▁, 1→▁/▂, 2→▂, 4→▄, 8→█.
	if graph[0] != '▁' {
		t.Errorf("first glyph = %c, want ▁", graph[0])
	}
	if graph[4] != '█' {
		t.Errorf("last glyph = %c, want █", graph[4])
	}
}

// TestSparkline_AllZero: the scale floor keeps all-zero series valid.
func TestSparkline_AllZero(t *testing.T) {
	samples := []ConcurrencySample{{Running: 0}, {Running: 0}, {Running: 0}}
	got := sparkline(samples)
	if !strings.HasSuffix(got, "  (max 1, 3 samples)") {
		t.Errorf("annotation = %q, want max 1", got)
	}
	if !utf8.ValidString(got) {
		t.Errorf("output is not valid UTF-8: %q", got)
	}
}

// TestSparkline_BucketsToWidth: more samples than columns collapses to width
// and keeps isolated peaks under max aggregation.
func TestSparkline_BucketsToWidth(t *testing.T) {
	const width = 100
	n := 201
	samples := make([]ConcurrencySample, 0, n)
	for i := 0; i < n; i++ {
		// Peaks at bucket boundaries and mid-bucket so aggregation is exercised.
		running := 0
		switch i {
		case 0, 100, 200:
			running = 5
		}
		samples = append(samples, ConcurrencySample{Running: running})
	}
	got := sparkline(samples)

	body := strings.TrimSuffix(got, "  (max 5, 201 samples)")
	graph := []rune(body)
	if len(graph) != width {
		t.Errorf("graph has %d runes, want %d", len(graph), width)
	}
	if !utf8.ValidString(got) {
		t.Errorf("output is not valid UTF-8: %q", got)
	}
}

// TestSparkline_NegativeValuesAreClamped: negative Running values must not
// panic on a negative index.
func TestSparkline_NegativeValuesAreClamped(t *testing.T) {
	samples := []ConcurrencySample{{Running: -1}, {Running: 2}}
	got := sparkline(samples)
	if !utf8.ValidString(got) {
		t.Errorf("output is not valid UTF-8: %q", got)
	}
}

// TestRenderMarkdown_IncludesConcurrencySparkline: non-empty samples render
// the fenced sparkline; empty samples omit it.
func TestRenderMarkdown_IncludesConcurrencySparkline(t *testing.T) {
	r := &RunReport{
		Concurrency: ConcurrencyMetrics{
			PoolBudget: 4,
			Samples: []ConcurrencySample{
				{Running: 0}, {Running: 1}, {Running: 2}, {Running: 3}, {Running: 4},
			},
		},
	}
	md := RenderMarkdown(r)
	if !strings.Contains(md, "```\n") {
		t.Errorf("markdown does not include the fenced sparkline:\n%s", md)
	}
	if !strings.Contains(md, "samples)") {
		t.Errorf("markdown sparkline is missing the sample count:\n%s", md)
	}

	empty := RenderMarkdown(&RunReport{})
	if strings.Contains(empty, "```\n▁") {
		t.Errorf("empty-samples markdown still has a sparkline:\n%s", empty)
	}
}

// TestWriteReport_WritesJSONAndMarkdown is the persisted-artifact contract:
// both files exist, the returned Markdown matches the file, and the JSON
// round-trips structurally.
func TestWriteReport_WritesJSONAndMarkdown(t *testing.T) {
	ts := time.Date(2025, 8, 21, 14, 30, 5, 0, time.UTC)
	report := &RunReport{
		Metadata: ReportMetadata{
			Spec:      "spec-a",
			Mode:      "single",
			Model:     "claude",
			Binary:    "/usr/local/bin/pi",
			GitHead:   "abcdef1234567890abcdef",
			Timestamp: ts,
			Duration:  "1m30s",
		},
		Outcome: RunOutcome{FinalPhase: "done", Retries: 0},
		Concurrency: ConcurrencyMetrics{
			PoolBudget: 4,
			Samples:    []ConcurrencySample{{Timestamp: ts, Running: 2}},
		},
		Trajectory: TrajectoryMetrics{TotalSteps: 1},
	}

	outDir := t.TempDir()
	jsonPath, mdPath, md, err := WriteReport(report, outDir)
	if err != nil {
		t.Fatalf("WriteReport: %v", err)
	}

	wantBase := "eval-spec-a-20250821-143005"
	if filepath.Base(jsonPath) != wantBase+".json" {
		t.Errorf("json basename = %s, want %s.json", filepath.Base(jsonPath), wantBase)
	}
	if filepath.Base(mdPath) != wantBase+".md" {
		t.Errorf("md basename = %s, want %s.md", filepath.Base(mdPath), wantBase)
	}

	// Files exist with 0644.
	for _, p := range []string{jsonPath, mdPath} {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatalf("stat %s: %v", p, err)
		}
		if perm := info.Mode().Perm(); perm != 0o644 {
			t.Errorf("mode of %s = %o, want 644", p, perm)
		}
	}

	// Returned Markdown matches the file.
	mdBytes, err := os.ReadFile(mdPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(mdBytes) != md {
		t.Errorf("returned Markdown != file contents:\n---returned---\n%s\n---file---\n%s", md, mdBytes)
	}

	// JSON round-trips.
	data, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatal(err)
	}
	var decoded RunReport
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal json: %v", err)
	}
	if decoded.Metadata.Spec != "spec-a" || decoded.Metadata.Model != "claude" {
		t.Errorf("decoded metadata mismatch: %+v", decoded.Metadata)
	}
}

// TestWriteReport_CreatesNestedOutputDir: a not-yet-created nested path works.
func TestWriteReport_CreatesNestedOutputDir(t *testing.T) {
	report := &RunReport{Metadata: ReportMetadata{Spec: "nested", Timestamp: time.Now()}}
	nested := filepath.Join(t.TempDir(), "deep", "report")
	jsonPath, mdPath, _, err := WriteReport(report, nested)
	if err != nil {
		t.Fatalf("WriteReport into nested dir: %v", err)
	}
	for _, p := range []string{jsonPath, mdPath} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("stat %s: %v", p, err)
		}
	}
}

// TestWriteReport_OutputPathIsNotDirectory: a regular file passed as outDir
// yields a stable, wrapped error.
func TestWriteReport_OutputPathIsNotDirectory(t *testing.T) {
	report := &RunReport{Metadata: ReportMetadata{Spec: "x", Timestamp: time.Now()}}
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, _, err := WriteReport(report, blocker)
	if err == nil {
		t.Fatal("expected an error when outDir is a regular file")
	}
	if !strings.Contains(err.Error(), "create report dir") {
		t.Errorf("error does not wrap the mkdir context: %v", err)
	}
}
