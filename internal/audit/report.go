package audit

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// FormatText renders scan results as a terminal-friendly table.
// If verbose is false, info-level findings are filtered out.
func FormatText(result *ScanResult, verbose bool) string {
	if len(result.Findings) == 0 {
		return fmt.Sprintf("Scanned %d file(s): no hidden characters found.\n", len(result.Files))
	}

	findings := filterFindings(result.Findings, verbose)
	if len(findings) == 0 {
		critical, warning, info := result.CountBySeverity()
		return fmt.Sprintf("Scanned %d file(s): %d info finding(s) (use --verbose to show).\n"+
			"  critical: %d, warning: %d, info: %d\n",
			len(result.Files), info, critical, warning, info)
	}

	sortFindings(findings)

	var b strings.Builder
	critical, warning, info := result.CountBySeverity()
	b.WriteString(fmt.Sprintf("Scanned %d file(s): %d finding(s)\n", len(result.Files), len(result.Findings)))
	b.WriteString(fmt.Sprintf("  critical: %d, warning: %d, info: %d\n\n", critical, warning, info))

	// Table header.
	b.WriteString(fmt.Sprintf("%-10s %-40s %8s  %-10s  %s\n",
		"SEVERITY", "FILE", "LINE:COL", "CODEPOINT", "DESCRIPTION"))
	b.WriteString(strings.Repeat("-", 100) + "\n")

	for _, f := range findings {
		file := f.File
		if len(file) > 40 {
			file = "..." + file[len(file)-37:]
		}
		b.WriteString(fmt.Sprintf("%-10s %-40s %4d:%-3d  %-10s  %s\n",
			f.Severity, file, f.Line, f.Col, f.Codepoint, f.Description))
	}

	return b.String()
}

// jsonReport is the JSON output structure.
type jsonReport struct {
	Summary  jsonSummary   `json:"summary"`
	Findings []ScanFinding `json:"findings"`
}

type jsonSummary struct {
	FilesScanned int `json:"files_scanned"`
	Critical     int `json:"critical"`
	Warning      int `json:"warning"`
	Info         int `json:"info"`
	Total        int `json:"total"`
}

// FormatJSON renders scan results as JSON.
func FormatJSON(result *ScanResult) (string, error) {
	critical, warning, info := result.CountBySeverity()
	report := jsonReport{
		Summary: jsonSummary{
			FilesScanned: len(result.Files),
			Critical:     critical,
			Warning:      warning,
			Info:         info,
			Total:        len(result.Findings),
		},
		Findings: result.Findings,
	}
	if report.Findings == nil {
		report.Findings = []ScanFinding{}
	}

	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshaling JSON: %w", err)
	}
	return string(data) + "\n", nil
}

// FormatMarkdown renders scan results as a GitHub-flavored markdown table.
func FormatMarkdown(result *ScanResult) string {
	if len(result.Findings) == 0 {
		return fmt.Sprintf("## Audit Results\n\nScanned %d file(s): no hidden characters found.\n", len(result.Files))
	}

	findings := make([]ScanFinding, len(result.Findings))
	copy(findings, result.Findings)
	sortFindings(findings)

	critical, warning, info := result.CountBySeverity()

	var b strings.Builder
	b.WriteString("## Audit Results\n\n")
	b.WriteString(fmt.Sprintf("Scanned **%d** file(s): **%d** finding(s)\n\n",
		len(result.Files), len(result.Findings)))
	b.WriteString(fmt.Sprintf("| Metric | Count |\n|--------|-------|\n"))
	b.WriteString(fmt.Sprintf("| Critical | %d |\n| Warning | %d |\n| Info | %d |\n\n", critical, warning, info))

	b.WriteString("| Severity | File | Line:Col | Codepoint | Description |\n")
	b.WriteString("|----------|------|----------|-----------|-------------|\n")

	for _, f := range findings {
		b.WriteString(fmt.Sprintf("| %s | %s | %d:%d | `%s` | %s |\n",
			f.Severity, f.File, f.Line, f.Col, f.Codepoint, f.Description))
	}

	return b.String()
}

// filterFindings returns findings, optionally excluding info-level.
func filterFindings(findings []ScanFinding, includeInfo bool) []ScanFinding {
	if includeInfo {
		return findings
	}
	var filtered []ScanFinding
	for _, f := range findings {
		if f.Severity > SeverityInfo {
			filtered = append(filtered, f)
		}
	}
	return filtered
}

// sortFindings sorts findings by severity (critical first), then file, then line.
func sortFindings(findings []ScanFinding) {
	sort.Slice(findings, func(i, j int) bool {
		if findings[i].Severity != findings[j].Severity {
			return findings[i].Severity > findings[j].Severity
		}
		if findings[i].File != findings[j].File {
			return findings[i].File < findings[j].File
		}
		return findings[i].Line < findings[j].Line
	})
}
