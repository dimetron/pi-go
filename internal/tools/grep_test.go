package tools

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
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

func TestRegexCache_EvictOldestAtCapacity(t *testing.T) {
	c := newRegexCache(2, 10*time.Minute)

	// Fill to capacity.
	c.put("first", nil)
	// Ensure monotonic ordering between entries.
	time.Sleep(2 * time.Millisecond)
	c.put("second", nil)

	if len(c.entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(c.entries))
	}

	// Adding a third entry should evict the oldest ("first").
	time.Sleep(2 * time.Millisecond)
	c.put("third", nil)

	if len(c.entries) != 2 {
		t.Errorf("expected 2 entries after eviction, got %d", len(c.entries))
	}
	if _, ok := c.entries["first"]; ok {
		t.Error("expected oldest entry 'first' to be evicted")
	}
	if _, ok := c.entries["second"]; !ok {
		t.Error("expected 'second' to remain")
	}
	if _, ok := c.entries["third"]; !ok {
		t.Error("expected 'third' to be stored")
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

func TestGrepPattern(t *testing.T) {
	tests := []struct {
		name      string
		input     GrepInput
		wantMatch string
		wantSkip  string
		wantErr   bool
	}{
		{
			name:      "plain pattern is case sensitive",
			input:     GrepInput{Pattern: "Needle"},
			wantMatch: "a Needle here",
			wantSkip:  "a needle here",
		},
		{
			name:      "case-insensitive flag is compiled in",
			input:     GrepInput{Pattern: "Needle", CaseInsensitive: true},
			wantMatch: "a needle here",
			wantSkip:  "nothing",
		},
		{
			name:    "invalid regex is an error",
			input:   GrepInput{Pattern: "("},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			re, err := grepPattern(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatal("grepPattern: expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("grepPattern: %v", err)
			}
			if !re.MatchString(tt.wantMatch) {
				t.Errorf("pattern did not match %q", tt.wantMatch)
			}
			if re.MatchString(tt.wantSkip) {
				t.Errorf("pattern unexpectedly matched %q", tt.wantSkip)
			}
		})
	}
}

// TestGrepPattern_CaseVariantsDoNotShareCacheEntry guards the cache key: the
// case-insensitive flag is part of the compiled expression, so the sensitive
// and insensitive forms of one pattern must not alias each other.
func TestGrepPattern_CaseVariantsDoNotShareCacheEntry(t *testing.T) {
	pattern := "GrepPatternCacheKeyProbe"

	sensitive, err := grepPattern(GrepInput{Pattern: pattern})
	if err != nil {
		t.Fatalf("grepPattern: %v", err)
	}
	insensitive, err := grepPattern(GrepInput{Pattern: pattern, CaseInsensitive: true})
	if err != nil {
		t.Fatalf("grepPattern: %v", err)
	}

	lower := strings.ToLower(pattern)
	if sensitive.MatchString(lower) {
		t.Error("case-sensitive pattern matched the lowercased input")
	}
	if !insensitive.MatchString(lower) {
		t.Error("case-insensitive pattern did not match the lowercased input")
	}
}

// stubDirEntry is a minimal fs.DirEntry for exercising the skip decision
// without touching the filesystem.
type stubDirEntry struct {
	name string
	dir  bool
}

func (e stubDirEntry) Name() string               { return e.name }
func (e stubDirEntry) IsDir() bool                { return e.dir }
func (e stubDirEntry) Type() fs.FileMode          { return 0 }
func (e stubDirEntry) Info() (fs.FileInfo, error) { return nil, nil }

func TestSkipGrepFile(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		glob string
		path string
		want bool
	}{
		{"empty glob keeps every file", "", "main.go", false},
		{"matching glob keeps the file", "*.go", "main.go", false},
		{"non-matching glob skips the file", "*.ts", "main.go", true},
		{"glob matches the base name, not the path", "*.go", "sub/main.go", false},
		{"brace glob is not supported by filepath.Match", "*.{ts,tsx}", "main.ts", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d := stubDirEntry{name: filepath.Base(tt.path)}
			if got := skipGrepFile(tt.glob, tt.path, d, nil); got != tt.want {
				t.Errorf("skipGrepFile(%q, %q) = %v, want %v", tt.glob, tt.path, got, tt.want)
			}
		})
	}
}

// TestGrepHandler_SingleFile covers the non-directory branch, including the
// cap: Matches is truncated to maxGrepMatches while TotalMatches still reports
// everything found.
func TestGrepHandler_SingleFile(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)

	if err := os.WriteFile(filepath.Join(dir, "small.txt"),
		[]byte("alpha\nbeta\nalpha again\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var big strings.Builder
	for i := range maxGrepMatches + 50 {
		fmt.Fprintf(&big, "hit %d\n", i)
	}
	if err := os.WriteFile(filepath.Join(dir, "big.txt"), []byte(big.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Run("under the cap", func(t *testing.T) {
		out, err := grepHandler(sb, GrepInput{Pattern: "alpha", Path: "small.txt"})
		if err != nil {
			t.Fatal(err)
		}
		if out.TotalMatches != 2 || len(out.Matches) != 2 {
			t.Fatalf("TotalMatches=%d len(Matches)=%d, want 2 and 2", out.TotalMatches, len(out.Matches))
		}
		if out.Truncated {
			t.Error("Truncated = true, want false")
		}
		if out.Matches[0].Line != 1 || out.Matches[1].Line != 3 {
			t.Errorf("line numbers = %d, %d, want 1, 3", out.Matches[0].Line, out.Matches[1].Line)
		}
	})

	t.Run("over the cap", func(t *testing.T) {
		out, err := grepHandler(sb, GrepInput{Pattern: "hit", Path: "big.txt"})
		if err != nil {
			t.Fatal(err)
		}
		if len(out.Matches) != maxGrepMatches {
			t.Errorf("len(Matches) = %d, want %d", len(out.Matches), maxGrepMatches)
		}
		if out.TotalMatches != maxGrepMatches+50 {
			t.Errorf("TotalMatches = %d, want %d", out.TotalMatches, maxGrepMatches+50)
		}
		if !out.Truncated {
			t.Error("Truncated = false, want true")
		}
	})

	t.Run("missing path is an error", func(t *testing.T) {
		if _, err := grepHandler(sb, GrepInput{Pattern: "x", Path: "nope.txt"}); err == nil {
			t.Error("expected an error for a missing path")
		}
	})

	t.Run("empty pattern is an error", func(t *testing.T) {
		if _, err := grepHandler(sb, GrepInput{}); err == nil {
			t.Error("expected an error for an empty pattern")
		}
	})
}
