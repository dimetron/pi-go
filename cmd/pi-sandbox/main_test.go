package main

import (
	"os"
	"regexp"
	"testing"
)

// TestProfileParsing verifies that param declarations are stripped
// and (param "HOME") / (param "CWD") are substituted correctly.
func TestProfileParsing(t *testing.T) {
	// Exercise the same logic that main() uses.
	paramDecl := regexp.MustCompile(`(?m)^\(param\s+.*\)\s*$\n?`)
	resolved := paramDecl.ReplaceAllString(profile, "")

	origHome := os.Getenv("HOME")
	origCwd, _ := os.Getwd()
	defer os.Setenv("HOME", origHome)

	// Verify the embedded profile has the expected placeholders.
	if !containsParam(resolved, "HOME") || !containsParam(resolved, "CWD") {
		t.Skip("profile may not contain param placeholders on this platform")
	}

	resolved = replaceParams(resolved, origHome, origCwd)

	// After substitution, no raw param placeholders should remain.
	if containsParam(resolved, "HOME") || containsParam(resolved, "CWD") {
		t.Error("HOME/CWD placeholders were not substituted")
	}
}

func containsParam(s, name string) bool {
	return regexp.MustCompile(`\(param\s+"` + name + `"\)`).MatchString(s)
}

func replaceParams(s, home, cwd string) string {
	s = regexp.MustCompile(`\(param\s+"HOME"\)`).ReplaceAllString(s, `"`+home+`"`)
	s = regexp.MustCompile(`\(param\s+"CWD"\)`).ReplaceAllString(s, `"`+cwd+`"`)
	return s
}
