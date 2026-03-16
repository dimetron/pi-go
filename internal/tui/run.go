package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Gate represents a validation command parsed from the ## Gates section of PROMPT.md.
type Gate struct {
	Name    string
	Command string
}

// parseGates extracts gate entries from the ## Gates section of a PROMPT.md.
// Supports formats:
//   - **name**: `command`
//   - name: `command`
//
// Returns an empty slice if no Gates section is found.
func parseGates(promptMD string) []Gate {
	lines := strings.Split(promptMD, "\n")

	// Find the ## Gates section.
	inGates := false
	var gates []Gate

	// Match: - **name**: `command` or - name: `command`
	gateRe := regexp.MustCompile(`^-\s+\*{0,2}([^*:]+?)\*{0,2}\s*:\s*` + "`" + `([^` + "`" + `]+)` + "`")

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "## Gates") {
			inGates = true
			continue
		}

		// Stop at the next heading.
		if inGates && strings.HasPrefix(trimmed, "## ") {
			break
		}

		if !inGates {
			continue
		}

		matches := gateRe.FindStringSubmatch(trimmed)
		if matches != nil {
			gates = append(gates, Gate{
				Name:    strings.TrimSpace(matches[1]),
				Command: strings.TrimSpace(matches[2]),
			})
		}
	}

	return gates
}

// readPromptMD reads the PROMPT.md file from a spec directory.
func readPromptMD(workDir, specName string) (string, error) {
	promptPath := filepath.Join(workDir, "specs", specName, "PROMPT.md")
	content, err := os.ReadFile(promptPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("PROMPT.md not found at %s — has the /plan session completed?", promptPath)
		}
		return "", fmt.Errorf("failed to read PROMPT.md: %w", err)
	}
	return string(content), nil
}

// listAvailableSpecs scans the specs/ directory for subdirectories containing PROMPT.md.
// Returns a sorted list of spec names.
func listAvailableSpecs(workDir string) ([]string, error) {
	specsDir := filepath.Join(workDir, "specs")

	entries, err := os.ReadDir(specsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read specs directory: %w", err)
	}

	var specs []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		promptPath := filepath.Join(specsDir, entry.Name(), "PROMPT.md")
		if _, err := os.Stat(promptPath); err == nil {
			specs = append(specs, entry.Name())
		}
	}

	sort.Strings(specs)
	return specs, nil
}
