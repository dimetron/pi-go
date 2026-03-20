package audit

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// ScanFinding represents a single hidden character detected.
type ScanFinding struct {
	File        string   `json:"file"`
	Line        int      `json:"line"`
	Col         int      `json:"col"`
	Codepoint   string   `json:"codepoint"`
	Severity    Severity `json:"severity"`
	Description string   `json:"description"`
}

// ScanResult holds findings for one or more files.
type ScanResult struct {
	Files    []string      `json:"files"`
	Findings []ScanFinding `json:"findings"`
}

// HasCritical returns true if any finding is critical.
func (r *ScanResult) HasCritical() bool {
	for _, f := range r.Findings {
		if f.Severity == SeverityCritical {
			return true
		}
	}
	return false
}

// HasWarning returns true if any finding is warning or higher.
func (r *ScanResult) HasWarning() bool {
	for _, f := range r.Findings {
		if f.Severity >= SeverityWarning {
			return true
		}
	}
	return false
}

// CountBySeverity returns counts for each severity level.
func (r *ScanResult) CountBySeverity() (critical, warning, info int) {
	for _, f := range r.Findings {
		switch f.Severity {
		case SeverityCritical:
			critical++
		case SeverityWarning:
			warning++
		case SeverityInfo:
			info++
		}
	}
	return
}

// isASCII returns true if the content contains only ASCII characters.
func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] > 127 {
			return false
		}
	}
	return true
}

// isEmoji returns true if the rune is likely an emoji base character.
func isEmoji(r rune) bool {
	// Common emoji ranges.
	return (r >= 0x1F600 && r <= 0x1F64F) || // Emoticons
		(r >= 0x1F300 && r <= 0x1F5FF) || // Misc Symbols and Pictographs
		(r >= 0x1F680 && r <= 0x1F6FF) || // Transport and Map
		(r >= 0x1F1E0 && r <= 0x1F1FF) || // Flags
		(r >= 0x2600 && r <= 0x26FF) || // Misc symbols
		(r >= 0x2700 && r <= 0x27BF) || // Dingbats
		(r >= 0x1F900 && r <= 0x1F9FF) || // Supplemental Symbols
		(r >= 0x1FA00 && r <= 0x1FA6F) || // Chess Symbols
		(r >= 0x1FA70 && r <= 0x1FAFF) // Symbols and Pictographs Extended-A
}

// ScanText scans content for hidden Unicode characters.
// filename is used for reporting only.
func ScanText(content, filename string) *ScanResult {
	result := &ScanResult{
		Files: []string{filename},
	}

	// ASCII fast-path: if all bytes are ASCII, nothing to find.
	if isASCII(content) {
		return result
	}

	runes := []rune(content)
	line := 1
	col := 1

	for i, r := range runes {
		if r == '\n' {
			line++
			col = 1
			continue
		}

		entry, found := charTable[r]
		if found {
			severity := entry.Severity
			description := entry.Description

			// Smart context: ZWJ between emoji characters → downgrade to info.
			if r == 0x200D {
				prevEmoji := i > 0 && isEmoji(runes[i-1])
				nextEmoji := i < len(runes)-1 && isEmoji(runes[i+1])
				if prevEmoji && nextEmoji {
					severity = SeverityInfo
					description = "ZWJ in emoji sequence (safe)"
				}
			}

			// Smart context: BOM at file start → downgrade to info.
			if r == 0xFEFF && i == 0 {
				severity = SeverityInfo
				description = "BOM at file start (harmless)"
			}

			result.Findings = append(result.Findings, ScanFinding{
				File:        filename,
				Line:        line,
				Col:         col,
				Codepoint:   fmt.Sprintf("U+%04X", r),
				Severity:    severity,
				Description: description,
			})
		}

		col++
	}

	return result
}

// ScanFile reads a file and scans it for hidden characters.
func ScanFile(path string) (*ScanResult, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	// Check for valid UTF-8.
	if !utf8.Valid(data) {
		return &ScanResult{
			Files: []string{path},
			Findings: []ScanFinding{{
				File:        path,
				Line:        1,
				Col:         1,
				Codepoint:   "N/A",
				Severity:    SeverityCritical,
				Description: "File contains invalid UTF-8 (possible binary)",
			}},
		}, nil
	}

	return ScanText(string(data), path), nil
}

// ScanSkillDirs discovers and scans all SKILL.md files in the given directories.
func ScanSkillDirs(dirs ...string) (*ScanResult, error) {
	combined := &ScanResult{}

	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("reading dir %s: %w", dir, err)
		}

		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			skillFile := filepath.Join(dir, entry.Name(), "SKILL.md")
			if _, err := os.Stat(skillFile); err != nil {
				continue
			}

			result, err := ScanFile(skillFile)
			if err != nil {
				return nil, err
			}
			combined.Files = append(combined.Files, result.Files...)
			combined.Findings = append(combined.Findings, result.Findings...)
		}
	}

	return combined, nil
}

// StripDangerous removes critical and warning characters from content,
// preserving ZWJ in emoji sequences and info-level characters.
func StripDangerous(content string) string {
	runes := []rune(content)
	var b strings.Builder
	b.Grow(len(content))

	for i, r := range runes {
		entry, found := charTable[r]
		if !found || entry.Severity == SeverityInfo {
			b.WriteRune(r)
			continue
		}

		// Preserve ZWJ between emoji.
		if r == 0x200D {
			prevEmoji := i > 0 && isEmoji(runes[i-1])
			nextEmoji := i < len(runes)-1 && isEmoji(runes[i+1])
			if prevEmoji && nextEmoji {
				b.WriteRune(r)
				continue
			}
		}

		// Preserve BOM at file start.
		if r == 0xFEFF && i == 0 {
			b.WriteRune(r)
			continue
		}

		// Skip critical and warning characters.
	}

	return b.String()
}

// StripFile reads a file, strips dangerous characters, and writes it back.
// Creates a .bak backup before modifying.
func StripFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}

	original := string(data)
	stripped := StripDangerous(original)

	if original == stripped {
		return nil // nothing to strip
	}

	// Create backup.
	if err := os.WriteFile(path+".bak", data, 0o644); err != nil {
		return fmt.Errorf("creating backup: %w", err)
	}

	// Write stripped content.
	if err := os.WriteFile(path, []byte(stripped), 0o644); err != nil {
		return fmt.Errorf("writing stripped file: %w", err)
	}

	return nil
}

// Classify returns the highest severity found in the findings.
func Classify(findings []ScanFinding) Severity {
	highest := SeverityInfo
	for _, f := range findings {
		if f.Severity > highest {
			highest = f.Severity
		}
	}
	return highest
}

// ExitCode returns the appropriate exit code for the findings.
// 0=clean/info, 1=critical, 2=warnings only.
func ExitCode(findings []ScanFinding) int {
	severity := Classify(findings)
	switch severity {
	case SeverityCritical:
		return 1
	case SeverityWarning:
		return 2
	default:
		return 0
	}
}
