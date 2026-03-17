package lsp

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/tool"
)

const (
	// FormatTimeout is the maximum time to wait for formatting results.
	FormatTimeout = 5 * time.Second

	// DiagnosticsDelay is the time to wait for the server to push diagnostics
	// after a file change notification.
	DiagnosticsDelay = 2 * time.Second
)

// BuildLSPAfterToolCallback creates an AfterToolCallback that:
// - Formats files after write tool calls via the LSP server
// - Collects diagnostics after write/edit tool calls and appends them to the result
//
// If no LSP server is available for a file type, the callback is a no-op.
// All errors are logged but never fail the tool call.
func BuildLSPAfterToolCallback(mgr *Manager) llmagent.AfterToolCallback {
	return func(ctx tool.Context, t tool.Tool, args, result map[string]any, err error) (map[string]any, error) {
		name := t.Name()
		if name != "write" && name != "edit" {
			return result, nil
		}

		// Tool failed — don't try LSP operations.
		if err != nil {
			return result, nil
		}

		// Extract file path from the result map.
		filePath, ok := result["path"].(string)
		if !ok || filePath == "" {
			return result, nil
		}

		// Get LSP server for this file type.
		srv, srvErr := mgr.ServerFor(filePath)
		if srvErr != nil {
			log.Printf("lsp hook: server for %s: %v", filePath, srvErr)
			return result, nil
		}
		if srv == nil {
			// No server configured/installed for this language — skip silently.
			return result, nil
		}

		// Read current file content and notify the server.
		content, readErr := os.ReadFile(filePath)
		if readErr != nil {
			log.Printf("lsp hook: reading %s: %v", filePath, readErr)
			return result, nil
		}

		if notifyErr := srv.NotifyChange(filePath, string(content)); notifyErr != nil {
			log.Printf("lsp hook: notify change %s: %v", filePath, notifyErr)
			return result, nil
		}

		// For write tool: attempt formatting.
		if name == "write" {
			result = formatFile(ctx, mgr, srv, filePath, result)
		}

		// For both write and edit: collect diagnostics.
		result = collectDiagnostics(mgr, srv, filePath, result)

		return result, nil
	}
}

// formatter is an interface for LSP servers that can format files.
type formatter interface {
	Format(ctx context.Context, file string) ([]TextEdit, error)
	NotifyChange(file, content string) error
}

// formatFile requests formatting from the LSP server and applies edits to disk.
func formatFile(_ context.Context, _ *Manager, srv *Server, filePath string, result map[string]any) map[string]any {
	return formatFileWithFormatter(srv, filePath, result)
}

// formatFileWithFormatter allows injecting a formatter for testing.
func formatFileWithFormatter(fmtr formatter, filePath string, result map[string]any) map[string]any {
	fmtCtx, cancel := context.WithTimeout(context.Background(), FormatTimeout)
	defer cancel()

	edits, err := fmtr.Format(fmtCtx, filePath)
	if err != nil {
		log.Printf("lsp hook: format %s: %v", filePath, err)
		return result
	}
	if len(edits) == 0 {
		return result
	}

	// Apply text edits to the file.
	content, err := os.ReadFile(filePath)
	if err != nil {
		log.Printf("lsp hook: read for format %s: %v", filePath, err)
		return result
	}

	formatted := ApplyTextEdits(string(content), edits)
	if err := os.WriteFile(filePath, []byte(formatted), 0o644); err != nil {
		log.Printf("lsp hook: write formatted %s: %v", filePath, err)
		return result
	}

	// Notify server of the formatted content.
	if err := fmtr.NotifyChange(filePath, formatted); err != nil {
		log.Printf("lsp hook: notify formatted %s: %v", filePath, err)
	}

	result["lsp_formatted"] = true
	return result
}

// collectDiagnostics waits briefly for diagnostics and appends them to the result.
func collectDiagnostics(mgr *Manager, srv *Server, filePath string, result map[string]any) map[string]any {
	// Wait briefly for the server to push diagnostics.
	time.Sleep(DiagnosticsDelay)
	return collectDiagnosticsImmediate(mgr, srv, filePath, result)
}

// collectDiagnosticsImmediate appends cached diagnostics to the result without waiting.
func collectDiagnosticsImmediate(mgr *Manager, _ *Server, filePath string, result map[string]any) map[string]any {
	absPath, err := filepath.Abs(filePath)
	if err != nil {
		absPath = filePath
	}
	uri := pathToURI(absPath)

	diags := mgr.CachedDiagnostics(uri)
	if len(diags) == 0 {
		return result
	}

	// Filter to errors and warnings only.
	var filtered []string
	for _, d := range diags {
		if d.Severity > SeverityWarning {
			continue
		}
		line := fmt.Sprintf("%s:%d:%d: %s: %s",
			filepath.Base(filePath),
			d.Range.Start.Line+1,
			d.Range.Start.Character+1,
			d.SeverityString(),
			d.Message,
		)
		filtered = append(filtered, line)
	}

	if len(filtered) > 0 {
		result["lsp_diagnostics"] = strings.Join(filtered, "\n")
	}

	return result
}

// ApplyTextEdits applies LSP TextEdit operations to a document string.
// Edits are applied in reverse order (bottom-up) to preserve line/character offsets.
func ApplyTextEdits(content string, edits []TextEdit) string {
	if len(edits) == 0 {
		return content
	}

	lines := strings.Split(content, "\n")

	// Sort edits in reverse order (bottom-up) so earlier offsets remain valid.
	sorted := make([]TextEdit, len(edits))
	copy(sorted, edits)
	for i := 0; i < len(sorted)-1; i++ {
		for j := i + 1; j < len(sorted); j++ {
			if editBefore(sorted[j], sorted[i]) {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}

	for _, edit := range sorted {
		startLine := edit.Range.Start.Line
		startChar := edit.Range.Start.Character
		endLine := edit.Range.End.Line
		endChar := edit.Range.End.Character

		// Clamp to document bounds.
		if startLine >= len(lines) {
			startLine = len(lines) - 1
		}
		if endLine >= len(lines) {
			endLine = len(lines) - 1
		}
		if startLine < 0 {
			startLine = 0
		}
		if endLine < 0 {
			endLine = 0
		}
		if startChar > len(lines[startLine]) {
			startChar = len(lines[startLine])
		}
		if endChar > len(lines[endLine]) {
			endChar = len(lines[endLine])
		}

		// Build the new content: prefix + newText + suffix.
		prefix := lines[startLine][:startChar]
		suffix := lines[endLine][endChar:]

		newLines := strings.Split(prefix+edit.NewText+suffix, "\n")

		// Replace the affected lines.
		result := make([]string, 0, len(lines)-endLine+startLine+len(newLines))
		result = append(result, lines[:startLine]...)
		result = append(result, newLines...)
		result = append(result, lines[endLine+1:]...)
		lines = result
	}

	return strings.Join(lines, "\n")
}

// editBefore returns true if edit a should be applied before edit b
// (i.e., a has a later position than b, so it sorts first in reverse order).
func editBefore(a, b TextEdit) bool {
	if a.Range.Start.Line != b.Range.Start.Line {
		return a.Range.Start.Line > b.Range.Start.Line
	}
	return a.Range.Start.Character > b.Range.Start.Character
}
