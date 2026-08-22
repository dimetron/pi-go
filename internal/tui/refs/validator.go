package refs

import (
	"errors"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// Validator enforces security policies on references.
type Validator struct {
	workDir        string
	blockedDirs    []string
	sensitiveFiles []string
}

// NewValidator creates a new Validator with default security policies.
func NewValidator(workDir string) *Validator {
	return &Validator{
		workDir: workDir,
		// Sensitive paths that contain credentials or secrets
		blockedDirs: []string{
			".ssh",
			".aws",
			".gnupg",
			".kube",
			"skills/.hub",
		},
		sensitiveFiles: []string{
			"id_rsa",
			"id_ed25519",
			"id_ecdsa",
			"id_dsa",
			"authorized_keys",
			"known_hosts",
			"config", // ssh config
			".bashrc",
			".zshrc",
			".profile",
			".bash_profile",
			".zprofile",
			".netrc",
			".pgpass",
			".npmrc",
			".pypirc",
			".env",
			".env.local",
			".env.development",
			".env.production",
			".git-credentials",
			".netrc",
		},
	}
}

// ValidatePath checks if a path is safe to access.
// Returns an error with a warning message if the path is blocked.
func (v *Validator) ValidatePath(path string) error {
	// Clean the path to resolve any ".." components
	cleaned := filepath.Clean(path)

	// Check for path traversal attempts
	if v.isPathTraversal(path, cleaned) {
		return errors.New("path traversal detected: path escapes sandbox")
	}

	// Check if path is in a blocked directory
	if v.isBlockedDir(cleaned) {
		return errors.New("path is in a blocked directory")
	}

	// Check if file is a sensitive file
	if v.isSensitiveFile(cleaned) {
		return errors.New("path is a sensitive credential file")
	}

	return nil
}

// isPathTraversal checks if the path attempts to escape the sandbox.
func (v *Validator) isPathTraversal(path, cleaned string) bool {
	// Check for ".." traversal
	parts := strings.Split(cleaned, string(filepath.Separator))
	for _, part := range parts {
		if part == ".." {
			return true
		}
	}

	// For absolute paths, check if they escape workDir
	if filepath.IsAbs(path) && v.workDir != "" {
		absWorkDir, err := filepath.Abs(v.workDir)
		if err == nil {
			if !strings.HasPrefix(path, absWorkDir) {
				return true
			}
		}
	}

	// A rooted path that filepath.IsAbs does not call absolute still escapes.
	// On Windows "/etc/passwd" is drive-relative rather than absolute, so the
	// check above lets it through; filepath.IsLocal rejects it, along with
	// drive-relative "C:x" and the reserved device names.
	if !filepath.IsAbs(path) && !filepath.IsLocal(cleaned) {
		return true
	}

	return false
}

// isBlockedDir checks if the path is in a blocked directory.
func (v *Validator) isBlockedDir(path string) bool {
	pathLower := strings.ToLower(filepath.ToSlash(path))
	for _, blocked := range v.blockedDirs {
		if strings.Contains(pathLower, strings.ToLower(blocked)) {
			return true
		}
	}
	return false
}

// isSensitiveFile checks if the filename matches a sensitive file pattern.
func (v *Validator) isSensitiveFile(path string) bool {
	base := filepath.Base(path)
	baseLower := strings.ToLower(base)

	for _, sensitive := range v.sensitiveFiles {
		if baseLower == strings.ToLower(sensitive) {
			return true
		}
	}

	return false
}

// IsBinaryFile checks if a file is binary by examining its content.
func (v *Validator) IsBinaryFile(data []byte) bool {
	if len(data) == 0 {
		return false
	}

	// Check for null bytes (common in binary files)
	nullCount := 0
	checkLen := len(data)
	if checkLen > 8192 {
		checkLen = 8192
	}
	for i := 0; i < checkLen; i++ {
		if data[i] == 0 {
			nullCount++
		}
	}
	// If more than 1% null bytes, likely binary
	if nullCount > checkLen/100 {
		return true
	}

	// Check UTF-8 validity
	if !utf8.Valid(data) {
		return true
	}

	return false
}

// IsBinaryPath checks if a file path points to a known binary file type.
func (v *Validator) IsBinaryPath(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	binaryExtensions := []string{
		".exe", ".dll", ".so", ".dylib", ".bin", ".o", ".a",
		".png", ".jpg", ".jpeg", ".gif", ".bmp", ".ico", ".webp",
		".mp3", ".mp4", ".avi", ".mov", ".mkv", ".wav", ".flac",
		".zip", ".tar", ".gz", ".bz2", ".xz", ".rar", ".7z",
		".pdf", ".doc", ".docx", ".xls", ".xlsx", ".ppt", ".pptx",
		".woff", ".woff2", ".ttf", ".otf", ".eot",
		".wasm", ".swp", ".swo",
	}
	for _, binaryExt := range binaryExtensions {
		if ext == binaryExt {
			return true
		}
	}
	return false
}

// ValidateFile validates a file reference for security and accessibility.
func (v *Validator) ValidateFile(path string) error {
	if err := v.ValidatePath(path); err != nil {
		return err
	}
	if v.IsBinaryPath(path) {
		return errors.New("binary files are not supported")
	}
	return nil
}

// ValidateFolder validates a folder reference for security and accessibility.
func (v *Validator) ValidateFolder(path string) error {
	return v.ValidatePath(path)
}
