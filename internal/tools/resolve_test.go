package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/text/unicode/norm"
)

func TestIsBlockedPath(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"/dev/zero", true},
		{"/dev/urandom", true},
		{"/dev/stdin", true},
		{"/proc/1234/fd/0", true},
		{"/dev/../dev/zero", true}, // cleaned before matching
		{"/etc/hosts", false},
		{"/home/user/dev/zero.txt", false},
		{"main.go", false},
	}
	for _, tt := range tests {
		if got := isBlockedPath(tt.path); got != tt.want {
			t.Errorf("isBlockedPath(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

// TestRead_BlockedDeviceRefusedBeforeIO matters because the damage is done by
// the open itself: /dev/zero fills the byte budget with NULs and /dev/stdin can
// block the whole turn.
func TestRead_BlockedDeviceRefusedBeforeIO(t *testing.T) {
	sb := testSandbox(t, t.TempDir())

	_, err := readHandler(sb, ReadInput{FilePath: "/dev/zero"})
	if err == nil {
		t.Fatal("reading /dev/zero should be refused")
	}
	if !strings.Contains(err.Error(), "device file") {
		t.Errorf("error %q does not explain why", err)
	}
}

func TestWithinEditDistance(t *testing.T) {
	tests := []struct {
		a, b string
		max  int
		want bool
	}{
		{"main.go", "main.go", 2, true},
		{"main.go", "mian.go", 2, true}, // transposition = 2 edits
		{"config.go", "confg.go", 2, true},
		{"server.go", "sever.go", 2, true},
		{"main.go", "handler.go", 2, false},
		{"a", "abcdefgh", 2, false}, // length gap decides it immediately
		{"", "", 2, true},
	}
	for _, tt := range tests {
		if got := withinEditDistance(tt.a, tt.b, tt.max); got != tt.want {
			t.Errorf("withinEditDistance(%q, %q, %d) = %v, want %v", tt.a, tt.b, tt.max, got, tt.want)
		}
	}
}

func TestPathCandidates_CoversTheCommonMisspellings(t *testing.T) {
	// A curly apostrophe is what a path picks up passing through prose.
	got := pathCandidates("/tmp/it’s/main.go")
	if got[0] != "/tmp/it’s/main.go" {
		t.Errorf("the original spelling must be tried first, got %q", got[0])
	}
	if !containsString(got, "/tmp/it's/main.go") {
		t.Errorf("straightened-quote candidate missing from %q", got)
	}

	// A non-breaking space is what it picks up passing through a terminal.
	got = pathCandidates("/tmp/my dir/x.go")
	if !containsString(got, "/tmp/my dir/x.go") {
		t.Errorf("space-normalized candidate missing from %q", got)
	}
}

func containsString(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// TestRead_ResolvesNFDvsNFC is the case macOS creates on its own: the file
// system stores the name decomposed while almost every other tool emits it
// composed, so the two spellings differ as bytes and neither the model nor the
// user can see why the open failed.
func TestRead_ResolvesNFDvsNFC(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)

	decomposed := norm.NFD.String("café.txt")
	composed := norm.NFC.String("café.txt")
	if decomposed == composed {
		t.Skip("no normalization difference on this platform")
	}

	path := filepath.Join(dir, decomposed)
	if err := os.WriteFile(path, []byte("beans\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Ask with the composed spelling; the file on disk is decomposed.
	out, err := readHandler(sb, ReadInput{FilePath: filepath.Join(dir, composed)})
	if err != nil {
		t.Fatalf("read with the other normalization failed: %v", err)
	}
	if !strings.Contains(out.Content, "beans") {
		t.Errorf("content = %q, want it to contain %q", out.Content, "beans")
	}
}

// TestRead_DidYouMean turns a bare ENOENT — which costs a turn to diagnose —
// into a correction the model can make on its next call.
func TestRead_DidYouMean(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)
	writeFile(t, dir, "handler.go", "package main\n")

	_, err := readHandler(sb, ReadInput{FilePath: filepath.Join(dir, "handlers.go")})
	if err == nil {
		t.Fatal("reading a missing file should fail")
	}
	if !strings.Contains(err.Error(), "did you mean") {
		t.Errorf("error offers no suggestion: %v", err)
	}
	if !strings.Contains(err.Error(), "handler.go") {
		t.Errorf("error does not name the near neighbor: %v", err)
	}
}

func TestRead_NoSuggestionWhenNothingIsClose(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)
	writeFile(t, dir, "totally-unrelated-name.md", "x\n")

	_, err := readHandler(sb, ReadInput{FilePath: filepath.Join(dir, "zzz.go")})
	if err == nil {
		t.Fatal("reading a missing file should fail")
	}
	if strings.Contains(err.Error(), "did you mean") {
		t.Errorf("suggested an unrelated file: %v", err)
	}
}
