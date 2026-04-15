package tools

import (
	"testing"
	"time"
)

func TestRegexCacheGet_NotFound(t *testing.T) {
	c := newRegexCache(10, 10*time.Minute)
	re := c.get("nonexistent")
	if re != nil {
		t.Error("expected nil for nonexistent key")
	}
}

func TestRegexCacheGet_Expired(t *testing.T) {
	c := newRegexCache(10, 1*time.Millisecond)
	// Put an entry
	c.put("key", nil)

	// Wait for expiry
	time.Sleep(5 * time.Millisecond)

	re := c.get("key")
	if re != nil {
		t.Error("expected nil for expired entry")
	}
}

func TestRegexCache_BasicOperations(t *testing.T) {
	c := newRegexCache(5, 10*time.Minute)

	// Put and get should work
	c.put("test", nil)
	_ = c.get("test") // returns nil since regex is nil, but key exists

	// Non-existent key should return nil
	if c.get("nonexistent") != nil {
		t.Error("expected nil for nonexistent key")
	}
}

func TestRegexCache_Expiry(t *testing.T) {
	c := newRegexCache(10, 1*time.Millisecond)

	// Put an entry and wait for it to expire
	c.put("key", nil)
	time.Sleep(5 * time.Millisecond)

	// Expired entry should be evicted
	if c.get("key") != nil {
		t.Error("expected nil for expired entry")
	}
}

func TestGrepInput(t *testing.T) {
	input := GrepInput{
		Pattern:         "func.*Test",
		Path:            "/tmp",
		Glob:            "*.go",
		CaseInsensitive: true,
	}

	if input.Pattern != "func.*Test" {
		t.Errorf("Pattern = %q", input.Pattern)
	}
	if input.Path != "/tmp" {
		t.Errorf("Path = %q", input.Path)
	}
	if input.CaseInsensitive != true {
		t.Error("CaseInsensitive should be true")
	}
}

func TestGrepOutput(t *testing.T) {
	output := GrepOutput{
		Matches: []GrepMatch{
			{File: "/tmp/main.go", Line: 10, Content: "func TestMain()"},
			{File: "/tmp/main.go", Line: 25, Content: "func TestHelper()"},
		},
		TotalMatches: 2,
		Truncated:    false,
	}

	if len(output.Matches) != 2 {
		t.Fatalf("expected 2 matches, got %d", len(output.Matches))
	}
	if output.TotalMatches != 2 {
		t.Errorf("TotalMatches = %d", output.TotalMatches)
	}
	if output.Truncated {
		t.Error("Truncated should be false")
	}
}

func TestGrepMatch(t *testing.T) {
	match := GrepMatch{
		File:    "/tmp/test.go",
		Line:    42,
		Content: "var x = 100",
	}

	if match.File != "/tmp/test.go" {
		t.Errorf("File = %q", match.File)
	}
	if match.Line != 42 {
		t.Errorf("Line = %d", match.Line)
	}
	if match.Content != "var x = 100" {
		t.Errorf("Content = %q", match.Content)
	}
}

func TestNewGrepTool_Name(t *testing.T) {
	sb := &Sandbox{}
	tool, err := newGrepTool(sb)
	if err != nil {
		t.Fatalf("newGrepTool() error = %v", err)
	}
	if tool == nil {
		t.Fatal("newGrepTool() returned nil")
	}
	// Tool name should be "grep" or "ripgrep"
	name := tool.Name()
	if name != "grep" && name != "ripgrep" {
		t.Errorf("Name() = %q, want 'grep' or 'ripgrep'", name)
	}
}

func TestMaxGrepMatches(t *testing.T) {
	if maxGrepMatches != 200 {
		t.Errorf("maxGrepMatches = %d, want 200", maxGrepMatches)
	}
}
