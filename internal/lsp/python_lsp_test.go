package lsp

import (
	"os"
	"path/filepath"
	"testing"
)

// TestPythonLSP_BrokenCode tests the Python LSP infrastructure with broken Python code.
// This test verifies the LSP manager can handle Python files and demonstrates
// diagnostic collection infrastructure.
//
// Note: Full E2E with real LSP servers requires network access to download
// language server packages. This test verifies the basic infrastructure.
func TestPythonLSP_BrokenCode(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Python LSP E2E test in short mode")
	}

	// Verify uvx is available
	uvxPath := resolveCommand("uvx")
	if uvxPath == "" {
		t.Skip("uvx not found in PATH (required for Python LSP)")
	}

	// Create a temp project with pyproject.toml (root marker for Python)
	tmpDir := t.TempDir()
	pyprojectPath := filepath.Join(tmpDir, "pyproject.toml")
	if err := os.WriteFile(pyprojectPath, []byte(`[project]
name = "lsp-test"
version = "0.1.0"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create broken Python file with various errors
	brokenPyPath := filepath.Join(tmpDir, "test_broken.py")
	brokenCode := `# Broken Python code with various errors

def foo():
    return x + y  # NameError: undefined names x, y

def bar(a, b):
    result = a + b
    return result
    
def baz():
    unused = 42  # Warning: unused variable
    print("hello"
    # SyntaxError: missing closing paren

class MyClass:
    def __init__(self):
        self.value = 1
        
    def method(self, x, y):
        return x / y  # No error but edge case
        
def broken_list():
    return [1, 2, 3,  # Missing closing bracket
`
	if err := os.WriteFile(brokenPyPath, []byte(brokenCode), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create manager and verify Python is configured
	mgr := NewManager(nil)

	// Verify Python language is configured
	langs := mgr.Languages()
	pythonCfg, ok := langs["python"]
	if !ok {
		t.Fatal("Python language should be configured by default")
	}
	if pythonCfg.Command == "" {
		t.Error("expected python command to be set")
	} else {
		t.Logf("Python command: %s", pythonCfg.Command)
	}
	if pythonCfg.LanguageID != "python" {
		t.Errorf("expected language ID to be python, got %s", pythonCfg.LanguageID)
	}

	// Check if uvx is available (i.e., the python LSP would be available)
	if !mgr.Available("python") {
		t.Log("Note: Python LSP server (uvx) not found in PATH. Full LSP test skipped.")
		t.Log("To enable: uvx ruff server")
		t.Skip("Python LSP server binary not available")
	}

	t.Log("Python LSP server is available")

	// Test that the file URI conversion works correctly
	uri := fileURI(brokenPyPath)
	t.Logf("File URI: %s", uri)

	// Test that diagnostics can be collected (even if empty)
	diags := mgr.CachedDiagnostics(uri)
	t.Logf("Initial diagnostics for %s: %d", filepath.Base(brokenPyPath), len(diags))

	// Cleanup
	mgr.Shutdown()
}

// TestPythonLSP_DetectLanguage verifies Python file detection works
func TestPythonLSP_DetectLanguage(t *testing.T) {
	mgr := NewManager(nil)
	defer mgr.Shutdown()

	tests := []struct {
		file string
		isPy bool
	}{
		{"test.py", true},
		{"module.pyi", true},
		{"script.PY", true},
		{"test.go", false},
		{"test.js", false},
	}

	for _, tt := range tests {
		lang := DetectLanguage(tt.file, mgr.Languages())
		if tt.isPy && lang != "python" {
			t.Errorf("DetectLanguage(%q) = %q, want python", tt.file, lang)
		}
		if !tt.isPy && lang == "python" {
			t.Errorf("DetectLanguage(%q) = %q, want not python", tt.file, lang)
		}
	}
}

// TestPythonLSP_FindRoot verifies FindRoot works for Python projects
func TestPythonLSP_FindRoot(t *testing.T) {
	// Create temp dir with pyproject.toml
	tmpDir := t.TempDir()
	pyprojectPath := filepath.Join(tmpDir, "pyproject.toml")
	if err := os.WriteFile(pyprojectPath, []byte(`[project]`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create nested Python file
	nestedDir := filepath.Join(tmpDir, "src", "utils")
	if err := os.MkdirAll(nestedDir, 0o755); err != nil {

		t.Fatal(err)
	}
	pyPath := filepath.Join(nestedDir, "helper.py")
	if err := os.WriteFile(pyPath, []byte("# py\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Verify FindRoot walks up to pyproject.toml
	root := FindRoot(pyPath, []string{"pyproject.toml", "setup.py", "requirements.txt"})
	if root != tmpDir {
		t.Errorf("FindRoot() = %q, want %q", root, tmpDir)
	}
}
