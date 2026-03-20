package audit

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestFormatTextEmpty(t *testing.T) {
	result := &ScanResult{Files: []string{"a.md", "b.md"}}
	out := FormatText(result, false)
	if !strings.Contains(out, "2 file(s)") {
		t.Errorf("expected file count, got: %s", out)
	}
	if !strings.Contains(out, "no hidden characters") {
		t.Errorf("expected clean message, got: %s", out)
	}
}

func TestFormatTextWithFindings(t *testing.T) {
	result := &ScanResult{
		Files: []string{"test.md"},
		Findings: []ScanFinding{
			{File: "test.md", Line: 1, Col: 5, Codepoint: "U+E0001", Severity: SeverityCritical, Description: "Tag char"},
			{File: "test.md", Line: 2, Col: 3, Codepoint: "U+200B", Severity: SeverityWarning, Description: "ZWSP"},
			{File: "test.md", Line: 3, Col: 1, Codepoint: "U+00A0", Severity: SeverityInfo, Description: "NBSP"},
		},
	}

	// Without verbose: info filtered.
	out := FormatText(result, false)
	if !strings.Contains(out, "critical") {
		t.Error("expected critical in output")
	}
	if strings.Contains(out, "NBSP") {
		t.Error("info should be filtered without verbose")
	}

	// With verbose: info included.
	out = FormatText(result, true)
	if !strings.Contains(out, "NBSP") {
		t.Error("info should be included with verbose")
	}
}

func TestFormatTextInfoOnlyNoVerbose(t *testing.T) {
	result := &ScanResult{
		Files: []string{"test.md"},
		Findings: []ScanFinding{
			{File: "test.md", Severity: SeverityInfo, Description: "NBSP"},
		},
	}
	out := FormatText(result, false)
	if !strings.Contains(out, "info finding") {
		t.Errorf("expected info finding message, got: %s", out)
	}
}

func TestFormatJSON(t *testing.T) {
	result := &ScanResult{
		Files: []string{"test.md"},
		Findings: []ScanFinding{
			{File: "test.md", Line: 1, Col: 1, Codepoint: "U+E0001", Severity: SeverityCritical, Description: "Tag"},
		},
	}

	out, err := FormatJSON(result)
	if err != nil {
		t.Fatal(err)
	}

	var report jsonReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if report.Summary.Critical != 1 {
		t.Errorf("critical = %d, want 1", report.Summary.Critical)
	}
	if report.Summary.FilesScanned != 1 {
		t.Errorf("files_scanned = %d, want 1", report.Summary.FilesScanned)
	}
	if len(report.Findings) != 1 {
		t.Errorf("findings count = %d, want 1", len(report.Findings))
	}
}

func TestFormatJSONEmpty(t *testing.T) {
	result := &ScanResult{Files: []string{"a.md"}}
	out, err := FormatJSON(result)
	if err != nil {
		t.Fatal(err)
	}

	var report jsonReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if report.Summary.Total != 0 {
		t.Errorf("total = %d, want 0", report.Summary.Total)
	}
	if report.Findings == nil {
		t.Error("findings should be non-nil empty array")
	}
}

func TestFormatMarkdown(t *testing.T) {
	result := &ScanResult{
		Files: []string{"test.md"},
		Findings: []ScanFinding{
			{File: "test.md", Line: 1, Col: 1, Codepoint: "U+E0001", Severity: SeverityCritical, Description: "Tag"},
		},
	}

	out := FormatMarkdown(result)
	if !strings.Contains(out, "## Audit Results") {
		t.Error("expected markdown header")
	}
	if !strings.Contains(out, "| critical |") {
		t.Error("expected severity in table")
	}
	if !strings.Contains(out, "`U+E0001`") {
		t.Error("expected codepoint in table")
	}
}

func TestFormatMarkdownEmpty(t *testing.T) {
	result := &ScanResult{Files: []string{"a.md"}}
	out := FormatMarkdown(result)
	if !strings.Contains(out, "no hidden characters") {
		t.Errorf("expected clean message, got: %s", out)
	}
}
