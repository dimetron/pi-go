package refs

import (
	"testing"
)

func TestValidator_ValidatePath(t *testing.T) {
	v := NewValidator("/home/user/project")

	tests := []struct {
		name    string
		path    string
		wantErr bool
		errMsg  string
	}{
		{
			name:    "valid path",
			path:    "src/main.go",
			wantErr: false,
		},
		{
			name:    "valid nested path",
			path:    "pkg/internal/foo/bar.go",
			wantErr: false,
		},
		{
			name:    "path traversal with ..",
			path:    "../etc/passwd",
			wantErr: true,
			errMsg:  "path traversal",
		},
		{
			name:    "absolute path traversal",
			path:    "/etc/passwd",
			wantErr: true,
			errMsg:  "path traversal",
		},
		{
			name:    "blocked dir .ssh",
			path:    ".ssh/id_rsa",
			wantErr: true,
			errMsg:  "blocked directory",
		},
		{
			name:    "blocked dir .aws",
			path:    ".aws/credentials",
			wantErr: true,
			errMsg:  "blocked directory",
		},
		{
			name:    "blocked dir .gnupg",
			path:    ".gnupg/secring.gpg",
			wantErr: true,
			errMsg:  "blocked directory",
		},
		{
			name:    "sensitive file id_rsa",
			path:    "id_rsa",
			wantErr: true,
			errMsg:  "sensitive credential file",
		},
		{
			name:    "sensitive file .env",
			path:    ".env",
			wantErr: true,
			errMsg:  "sensitive credential file",
		},
		{
			name:    "sensitive file .bashrc",
			path:    ".bashrc",
			wantErr: true,
			errMsg:  "sensitive credential file",
		},
		{
			name:    "double dot traversal",
			path:    "src/../../etc/passwd",
			wantErr: true,
			errMsg:  "path traversal",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := v.ValidatePath(tt.path)
			if tt.wantErr {
				if err == nil {
					t.Errorf("ValidatePath(%q) expected error containing %q, got nil", tt.path, tt.errMsg)
					return
				}
				if !contains(err.Error(), tt.errMsg) {
					t.Errorf("ValidatePath(%q) error = %q, want error containing %q", tt.path, err.Error(), tt.errMsg)
				}
			} else {
				if err != nil {
					t.Errorf("ValidatePath(%q) unexpected error: %v", tt.path, err)
				}
			}
		})
	}
}

func TestValidator_IsBinaryFile(t *testing.T) {
	v := NewValidator("/test")

	tests := []struct {
		name     string
		data     []byte
		expected bool
	}{
		{
			name:     "empty data",
			data:     []byte{},
			expected: false,
		},
		{
			name:     "valid UTF-8 text",
			data:     []byte("Hello, World! This is a text file."),
			expected: false,
		},
		{
			name:     "UTF-8 with special chars",
			data:     []byte("Hello 世界 🌍"),
			expected: false,
		},
		{
			name:     "null bytes (binary)",
			data:     []byte("Hello\x00World"),
			expected: true,
		},
		{
			name:     "many null bytes (binary)",
			data:     []byte("\x00\x00\x00\x00\x00"),
			expected: true,
		},
		{
			name:     "invalid UTF-8",
			data:     []byte{0xff, 0xfe, 0xfd},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := v.IsBinaryFile(tt.data)
			if got != tt.expected {
				t.Errorf("IsBinaryFile() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestValidator_IsBinaryPath(t *testing.T) {
	v := NewValidator("/test")

	tests := []struct {
		name     string
		path     string
		expected bool
	}{
		{"go source", "main.go", false},
		{"js source", "index.js", false},
		{"py source", "script.py", false},
		{"md file", "README.md", false},
		{"png image", "logo.png", true},
		{"jpg image", "photo.jpg", true},
		{"gif image", "animation.gif", true},
		{"pdf doc", "doc.pdf", true},
		{"zip archive", "archive.zip", true},
		{"exe file", "program.exe", true},
		{"dll file", "library.dll", true},
		{"mp4 video", "video.mp4", true},
		{"woff font", "font.woff", true},
		{"wasm binary", "module.wasm", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := v.IsBinaryPath(tt.path)
			if got != tt.expected {
				t.Errorf("IsBinaryPath(%q) = %v, want %v", tt.path, got, tt.expected)
			}
		})
	}
}

func TestValidator_ValidateFile(t *testing.T) {
	v := NewValidator("/test")

	tests := []struct {
		name    string
		path    string
		wantErr bool
		errMsg  string
	}{
		{
			name:    "valid text file",
			path:    "src/main.go",
			wantErr: false,
		},
		{
			name:    "binary file rejected",
			path:    "image.png",
			wantErr: true,
			errMsg:  "binary",
		},
		{
			name:    "path traversal rejected",
			path:    "../etc/passwd",
			wantErr: true,
			errMsg:  "path traversal",
		},
		{
			name:    "sensitive file rejected",
			path:    ".env",
			wantErr: true,
			errMsg:  "sensitive",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := v.ValidateFile(tt.path)
			if tt.wantErr {
				if err == nil {
					t.Errorf("ValidateFile(%q) expected error containing %q, got nil", tt.path, tt.errMsg)
					return
				}
				if !contains(err.Error(), tt.errMsg) {
					t.Errorf("ValidateFile(%q) error = %q, want error containing %q", tt.path, err.Error(), tt.errMsg)
				}
			} else {
				if err != nil {
					t.Errorf("ValidateFile(%q) unexpected error: %v", tt.path, err)
				}
			}
		})
	}
}

func TestValidator_ValidateFolder(t *testing.T) {
	v := NewValidator("/test")

	tests := []struct {
		name    string
		path    string
		wantErr bool
		errMsg  string
	}{
		{
			name:    "valid folder",
			path:    "src/components",
			wantErr: false,
		},
		{
			name:    "blocked dir rejected",
			path:    ".ssh",
			wantErr: true,
			errMsg:  "blocked directory",
		},
		{
			name:    "path traversal rejected",
			path:    "../../etc",
			wantErr: true,
			errMsg:  "path traversal",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := v.ValidateFolder(tt.path)
			if tt.wantErr {
				if err == nil {
					t.Errorf("ValidateFolder(%q) expected error containing %q, got nil", tt.path, tt.errMsg)
					return
				}
				if !contains(err.Error(), tt.errMsg) {
					t.Errorf("ValidateFolder(%q) error = %q, want error containing %q", tt.path, err.Error(), tt.errMsg)
				}
			} else {
				if err != nil {
					t.Errorf("ValidateFolder(%q) unexpected error: %v", tt.path, err)
				}
			}
		})
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
