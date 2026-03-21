package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dimetron/pi-go/internal/audit"
)

func newAuditCmd() *cobra.Command {
	var (
		flagDir     string
		flagFile    string
		flagStrip   bool
		flagDryRun  bool
		flagForce   bool
		flagVerbose bool
		flagFormat  string
		flagOutput  string
	)

	cmd := &cobra.Command{
		Use:   "audit",
		Short: "Scan skill files for hidden Unicode characters",
		Long: `Scans SKILL.md files for hidden Unicode characters that could be used for
prompt injection attacks (invisible instructions, BiDi overrides, tag characters).

By default, scans all skill directories. Use --file or --dir to target specific paths.

Exit codes: 0 = clean/info only, 1 = critical findings, 2 = warning findings.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAudit(cmd, flagDir, flagFile, flagStrip, flagDryRun, flagForce, flagVerbose, flagFormat, flagOutput)
		},
	}

	cmd.Flags().StringVar(&flagDir, "dir", "", "Directory to scan (default: all skill dirs)")
	cmd.Flags().StringVar(&flagFile, "file", "", "Single file to scan")
	cmd.Flags().BoolVar(&flagStrip, "strip", false, "Remove dangerous characters from files")
	cmd.Flags().BoolVar(&flagDryRun, "dry-run", false, "Preview strip changes without modifying files")
	cmd.Flags().BoolVar(&flagForce, "force", false, "Strip without confirmation")
	cmd.Flags().BoolVarP(&flagVerbose, "verbose", "v", false, "Show info-level findings")
	cmd.Flags().StringVar(&flagFormat, "format", "text", "Output format: text, json, markdown")
	cmd.Flags().StringVar(&flagOutput, "output", "", "Write output to file (auto-detects format from extension)")

	return cmd
}

func runAudit(_ *cobra.Command, dir, file string, strip, dryRun, force, verbose bool, format, output string) error {
	var result *audit.ScanResult
	var err error

	switch {
	case file != "":
		result, err = audit.ScanFile(file)
	case dir != "":
		result, err = audit.ScanSkillDirs(dir)
	default:
		// Scan default skill directories.
		dirs := defaultSkillDirs()
		result, err = audit.ScanSkillDirs(dirs...)
	}
	if err != nil {
		return fmt.Errorf("scan failed: %w", err)
	}

	// Handle strip mode.
	if strip {
		return handleStrip(result, dryRun, force, verbose)
	}

	// Auto-detect format from output file extension.
	if output != "" && format == "text" {
		switch {
		case strings.HasSuffix(output, ".json"):
			format = "json"
		case strings.HasSuffix(output, ".md"):
			format = "markdown"
		}
	}

	// Format output.
	var out string
	switch format {
	case "json":
		out, err = audit.FormatJSON(result)
		if err != nil {
			return err
		}
	case "markdown":
		out = audit.FormatMarkdown(result)
	default:
		out = audit.FormatText(result, verbose)
	}

	// Write to file or stdout.
	if output != "" {
		if err := os.WriteFile(output, []byte(out), 0o644); err != nil {
			return fmt.Errorf("writing output: %w", err)
		}
		fmt.Fprintf(os.Stderr, "Output written to %s\n", output)
	} else {
		fmt.Print(out)
	}

	// Return exit code via os.Exit for non-zero codes.
	code := audit.ExitCode(result.Findings)
	if code != 0 {
		os.Exit(code)
	}
	return nil
}

func handleStrip(result *audit.ScanResult, dryRun, force, verbose bool) error {
	if len(result.Findings) == 0 {
		fmt.Println("No dangerous characters found. Nothing to strip.")
		return nil
	}

	// Show what would be stripped.
	dangerousFiles := make(map[string]bool)
	for _, f := range result.Findings {
		if f.Severity >= audit.SeverityWarning {
			dangerousFiles[f.File] = true
		}
	}

	if len(dangerousFiles) == 0 {
		fmt.Println("Only info-level findings. Nothing to strip.")
		return nil
	}

	// Preview.
	fmt.Printf("Files with dangerous characters (%d):\n", len(dangerousFiles))
	for f := range dangerousFiles {
		fmt.Printf("  %s\n", f)
	}

	if dryRun {
		fmt.Println("\n[dry-run] No files modified.")
		// Show detailed findings.
		fmt.Print(audit.FormatText(result, verbose))
		return nil
	}

	if !force {
		fmt.Print("\nStrip dangerous characters from these files? [y/N] ")
		var answer string
		_, _ = fmt.Scanln(&answer)
		if answer != "y" && answer != "Y" {
			fmt.Println("Aborted.")
			return nil
		}
	}

	// Strip files.
	for f := range dangerousFiles {
		if err := audit.StripFile(f); err != nil {
			return fmt.Errorf("stripping %s: %w", f, err)
		}
		fmt.Printf("  Stripped: %s (backup: %s.bak)\n", f, f)
	}

	fmt.Printf("\nDone. %d file(s) cleaned.\n", len(dangerousFiles))
	return nil
}

func defaultSkillDirs() []string {
	dirs := []string{}
	if homeDir, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(homeDir, ".pi-go", "skills"))
	}
	dirs = append(dirs,
		filepath.Join(".pi-go", "skills"),
		filepath.Join(".claude", "skills"),
		filepath.Join(".cursor", "skills"),
	)
	return dirs
}
