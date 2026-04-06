package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestStripBase64Images tests that the read tool strips base64 images from markdown files.
func TestStripBase64Images(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)

	t.Run("strips base64 images in markdown", func(t *testing.T) {
		mdPath := filepath.Join(dir, "test.md")
		content := "# Test\n\n![Screenshot](data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==)\n\nSome text here.\n"
		os.WriteFile(mdPath, []byte(content), 0o644)

		out, err := readHandler(sb, ReadInput{FilePath: mdPath})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out.Content, "iVBORw0KGgo") {
			t.Error("expected base64 data to be stripped")
		}
		if !strings.Contains(out.Content, "data:image/png;base64,...[stripped]") {
			t.Error("expected stripped placeholder in content")
		}
		if !strings.Contains(out.Content, "Screenshot") {
			t.Error("expected alt text to be preserved")
		}
	})
}
