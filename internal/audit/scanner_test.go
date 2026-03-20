package audit

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScanTextASCIIFastPath(t *testing.T) {
	result := ScanText("Hello, World! This is plain ASCII.\nLine 2.", "test.md")
	if len(result.Findings) != 0 {
		t.Errorf("expected 0 findings for ASCII, got %d", len(result.Findings))
	}
}

func TestScanTextTagCharacters(t *testing.T) {
	// U+E0001 (LANGUAGE TAG) — critical.
	content := "Hello \U000E0001 World"
	result := ScanText(content, "test.md")
	if len(result.Findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(result.Findings))
	}
	f := result.Findings[0]
	if f.Severity != SeverityCritical {
		t.Errorf("severity = %v, want critical", f.Severity)
	}
	if f.Codepoint != "U+E0001" {
		t.Errorf("codepoint = %s, want U+E0001", f.Codepoint)
	}
}

func TestScanTextBiDiOverrides(t *testing.T) {
	// U+202E (Right-to-Left Override) — critical.
	content := "normal \u202E reversed"
	result := ScanText(content, "test.md")
	if len(result.Findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(result.Findings))
	}
	if result.Findings[0].Severity != SeverityCritical {
		t.Errorf("severity = %v, want critical", result.Findings[0].Severity)
	}
}

func TestScanTextVariationSelectors(t *testing.T) {
	// U+E0100 — critical (Glassworm vector).
	content := "text\U000E0100more"
	result := ScanText(content, "test.md")
	if len(result.Findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(result.Findings))
	}
	if result.Findings[0].Severity != SeverityCritical {
		t.Errorf("severity = %v, want critical", result.Findings[0].Severity)
	}
	if result.Findings[0].Description != "Variation selector supplement (Glassworm vector)" {
		t.Errorf("description = %q", result.Findings[0].Description)
	}
}

func TestScanTextZeroWidthWarning(t *testing.T) {
	// U+200B (Zero Width Space) — warning.
	content := "word\u200Bword"
	result := ScanText(content, "test.md")
	if len(result.Findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(result.Findings))
	}
	if result.Findings[0].Severity != SeverityWarning {
		t.Errorf("severity = %v, want warning", result.Findings[0].Severity)
	}
}

func TestScanTextBiDiMarks(t *testing.T) {
	// U+200E (LRM), U+200F (RLM) — warning.
	content := "text\u200Etext\u200Ftext"
	result := ScanText(content, "test.md")
	if len(result.Findings) != 2 {
		t.Fatalf("expected 2 findings, got %d", len(result.Findings))
	}
	for _, f := range result.Findings {
		if f.Severity != SeverityWarning {
			t.Errorf("severity = %v, want warning", f.Severity)
		}
	}
}

func TestScanTextInvisibleOperators(t *testing.T) {
	// U+2061 (Function Application) — warning.
	content := "f\u2061(x)"
	result := ScanText(content, "test.md")
	if len(result.Findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(result.Findings))
	}
	if result.Findings[0].Severity != SeverityWarning {
		t.Errorf("severity = %v, want warning", result.Findings[0].Severity)
	}
}

func TestScanTextUnusualWhitespace(t *testing.T) {
	// U+00A0 (No-Break Space) — info.
	content := "hello\u00A0world"
	result := ScanText(content, "test.md")
	if len(result.Findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(result.Findings))
	}
	if result.Findings[0].Severity != SeverityInfo {
		t.Errorf("severity = %v, want info", result.Findings[0].Severity)
	}
}

func TestScanTextZWJEmojiContext(t *testing.T) {
	// ZWJ between two emoji → downgrade to info.
	// 👨‍👩 (man ZWJ woman)
	content := "\U0001F468\u200D\U0001F469"
	result := ScanText(content, "test.md")
	if len(result.Findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(result.Findings))
	}
	if result.Findings[0].Severity != SeverityInfo {
		t.Errorf("ZWJ between emoji: severity = %v, want info", result.Findings[0].Severity)
	}
	if result.Findings[0].Description != "ZWJ in emoji sequence (safe)" {
		t.Errorf("description = %q", result.Findings[0].Description)
	}
}

func TestScanTextZWJNotBetweenEmoji(t *testing.T) {
	// ZWJ not between emoji → stays warning.
	content := "abc\u200Ddef"
	result := ScanText(content, "test.md")
	if len(result.Findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(result.Findings))
	}
	if result.Findings[0].Severity != SeverityWarning {
		t.Errorf("ZWJ not in emoji: severity = %v, want warning", result.Findings[0].Severity)
	}
}

func TestScanTextBOMAtStart(t *testing.T) {
	// BOM at position 0 → info.
	content := "\uFEFFHello"
	result := ScanText(content, "test.md")
	if len(result.Findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(result.Findings))
	}
	if result.Findings[0].Severity != SeverityInfo {
		t.Errorf("BOM at start: severity = %v, want info", result.Findings[0].Severity)
	}
}

func TestScanTextBOMMidFile(t *testing.T) {
	// BOM in middle → stays warning.
	content := "Hello\uFEFFWorld"
	result := ScanText(content, "test.md")
	if len(result.Findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(result.Findings))
	}
	if result.Findings[0].Severity != SeverityWarning {
		t.Errorf("BOM mid-file: severity = %v, want warning", result.Findings[0].Severity)
	}
}

func TestScanTextMixedCategories(t *testing.T) {
	// Mix: tag char (critical) + ZWSP (warning) + NBSP (info).
	content := "\U000E0001\u200B\u00A0"
	result := ScanText(content, "test.md")
	if len(result.Findings) != 3 {
		t.Fatalf("expected 3 findings, got %d", len(result.Findings))
	}
	critical, warning, info := result.CountBySeverity()
	if critical != 1 || warning != 1 || info != 1 {
		t.Errorf("counts: critical=%d, warning=%d, info=%d", critical, warning, info)
	}
}

func TestScanTextLineColTracking(t *testing.T) {
	content := "line1\nab\u200Bcd\nline3"
	result := ScanText(content, "test.md")
	if len(result.Findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(result.Findings))
	}
	f := result.Findings[0]
	if f.Line != 2 {
		t.Errorf("line = %d, want 2", f.Line)
	}
	if f.Col != 3 {
		t.Errorf("col = %d, want 3", f.Col)
	}
}

func TestScanFileNotExist(t *testing.T) {
	_, err := ScanFile("/nonexistent/path/file.md")
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

func TestScanFileNonUTF8(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "binary.md")
	// Write invalid UTF-8 bytes.
	if err := os.WriteFile(path, []byte{0xFF, 0xFE, 0x80, 0x81}, 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := ScanFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !result.HasCritical() {
		t.Error("expected critical finding for non-UTF-8 file")
	}
}

func TestScanFileClean(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "clean.md")
	if err := os.WriteFile(path, []byte("Clean ASCII file\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := ScanFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Findings) != 0 {
		t.Errorf("expected 0 findings, got %d", len(result.Findings))
	}
}

func TestScanSkillDirs(t *testing.T) {
	dir := t.TempDir()

	// Clean skill.
	cleanDir := filepath.Join(dir, "clean-skill")
	os.MkdirAll(cleanDir, 0o755)
	os.WriteFile(filepath.Join(cleanDir, "SKILL.md"), []byte("Clean skill"), 0o644)

	// Dirty skill with BiDi override.
	dirtyDir := filepath.Join(dir, "dirty-skill")
	os.MkdirAll(dirtyDir, 0o755)
	os.WriteFile(filepath.Join(dirtyDir, "SKILL.md"), []byte("Dirty \u202E skill"), 0o644)

	result, err := ScanSkillDirs(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Files) != 2 {
		t.Errorf("expected 2 files, got %d", len(result.Files))
	}
	if !result.HasCritical() {
		t.Error("expected critical finding from dirty skill")
	}
}

func TestScanSkillDirsNonExistent(t *testing.T) {
	result, err := ScanSkillDirs("/nonexistent/dir")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Files) != 0 {
		t.Errorf("expected 0 files, got %d", len(result.Files))
	}
}

func TestHasCritical(t *testing.T) {
	r := &ScanResult{Findings: []ScanFinding{
		{Severity: SeverityWarning},
	}}
	if r.HasCritical() {
		t.Error("should not have critical")
	}

	r.Findings = append(r.Findings, ScanFinding{Severity: SeverityCritical})
	if !r.HasCritical() {
		t.Error("should have critical")
	}
}

func TestHasWarning(t *testing.T) {
	r := &ScanResult{Findings: []ScanFinding{
		{Severity: SeverityInfo},
	}}
	if r.HasWarning() {
		t.Error("should not have warning")
	}

	r.Findings = append(r.Findings, ScanFinding{Severity: SeverityWarning})
	if !r.HasWarning() {
		t.Error("should have warning")
	}
}

func TestClassify(t *testing.T) {
	findings := []ScanFinding{
		{Severity: SeverityInfo},
		{Severity: SeverityWarning},
	}
	if got := Classify(findings); got != SeverityWarning {
		t.Errorf("classify = %v, want warning", got)
	}

	findings = append(findings, ScanFinding{Severity: SeverityCritical})
	if got := Classify(findings); got != SeverityCritical {
		t.Errorf("classify = %v, want critical", got)
	}
}

func TestExitCode(t *testing.T) {
	tests := []struct {
		severity Severity
		want     int
	}{
		{SeverityInfo, 0},
		{SeverityWarning, 2},
		{SeverityCritical, 1},
	}
	for _, tt := range tests {
		findings := []ScanFinding{{Severity: tt.severity}}
		if got := ExitCode(findings); got != tt.want {
			t.Errorf("ExitCode(%v) = %d, want %d", tt.severity, got, tt.want)
		}
	}
}

func TestExitCodeEmpty(t *testing.T) {
	if got := ExitCode(nil); got != 0 {
		t.Errorf("ExitCode(nil) = %d, want 0", got)
	}
}

// TestSeverityStringAll covers all severity levels including the default unknown case.
func TestSeverityStringAll(t *testing.T) {
	tests := []struct {
		sev  Severity
		want string
	}{
		{SeverityInfo, "info"},
		{SeverityWarning, "warning"},
		{SeverityCritical, "critical"},
		{Severity(999), "unknown"}, // default branch
	}
	for _, tt := range tests {
		got := tt.sev.String()
		if got != tt.want {
			t.Errorf("Severity(%d).String() = %q, want %q", tt.sev, got, tt.want)
		}
	}
}

// TestScanSkillDirsNonExistentDirSkipped verifies that a non-existent dir
// is silently skipped by ScanSkillDirs.
func TestScanSkillDirsNonExistentDirSkipped(t *testing.T) {
	result, err := ScanSkillDirs("/tmp/nonexistent-dir-xyz-99999")
	if err != nil {
		t.Fatalf("expected no error for non-existent dir, got: %v", err)
	}
	if len(result.Findings) != 0 {
		t.Errorf("expected 0 findings for empty scan, got %d", len(result.Findings))
	}
}

// TestScanSkillDirsFindsSkillMD verifies that ScanSkillDirs scans SKILL.md files.
func TestScanSkillDirsFindsSkillMD(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "my-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Write a clean SKILL.md (no dangerous chars).
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# My Skill\nClean content."), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := ScanSkillDirs(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Files) != 1 {
		t.Errorf("expected 1 scanned file, got %d", len(result.Files))
	}
}
