package lsp

import (
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/dimetron/pi-go/internal/notice"
)

// captureNotices installs a notice sink for the duration of the test and
// returns a function yielding the messages it collected. Safe to call
// concurrently: it waits for in-flight hook callbacks to finish.
func captureNotices(t *testing.T) func() []string {
	t.Helper()
	var mu sync.Mutex
	var got []string
	prev := notice.SetSink(func(msg string) {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, msg)
	})
	t.Cleanup(func() { notice.SetSink(prev) })
	return func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), got...)
	}
}

// captureStderr redirects both os.Stderr and the standard logger to a pipe and
// returns a function yielding what was written. The TUI guard needs both: a
// fmt.Fprint to os.Stderr and a log.Printf both corrupt the alternate screen,
// and stdlog captures the logger's own writer rather than the fd.
func captureStderr(t *testing.T) func() string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	origStderr, origLogOut := os.Stderr, log.Writer()
	os.Stderr = w
	log.SetOutput(w)
	t.Cleanup(func() {
		os.Stderr = origStderr
		log.SetOutput(origLogOut)
		_ = w.Close()
		_ = r.Close()
	})
	return func() string {
		_ = w.Close()
		out, err := io.ReadAll(r)
		if err != nil {
			t.Fatalf("read stderr pipe: %v", err)
		}
		return string(out)
	}
}

// missingTypeScriptManager reports the TypeScript language as configured but not
// installed — the state of this host, where `typescript-language-server` is
// absent, so writing any .ts file takes the "not found in PATH" branch.
func missingTypeScriptManager() *Manager {
	return &Manager{
		languages: map[string]*LanguageConfig{
			"typescript": {
				Command:        "typescript-language-server",
				Args:           []string{"--stdio"},
				FileExtensions: []string{".ts", ".tsx", ".js", ".jsx"},
				RootMarkers:    []string{"tsconfig.json", "package.json"},
				LanguageID:     "typescript",
			},
		},
		servers:     map[string]*Server{},
		diagnostics: map[string][]Diagnostic{},
		available:   map[string]bool{"typescript": false},
	}
}

// TestLSPHook_MissingServerReportsNoticeNotStderr is the TUI guard for the
// reproduction: with typescript-language-server absent, a write to a .ts file
// must report the missing server through the notice sink the TUI drains, and
// must not write a single byte to stderr.
//
// Before the fix this path called log.Printf, whose default sink is os.Stderr.
// The Bubble Tea renderer owns the alternate screen, so those bytes were painted
// into the layout and stayed until the next full repaint.
func TestLSPHook_MissingServerReportsNoticeNotStderr(t *testing.T) {
	notices := captureNotices(t)
	stderr := captureStderr(t)

	mgr := missingTypeScriptManager()
	cb := BuildLSPAfterToolCallback(mgr)

	_, err := cb(nil, &mockTool{name: "write"}, nil, map[string]any{"path": "/tmp/x.ts"}, nil)
	if err != nil {
		t.Fatalf("LSP hook returned an error: %v", err)
	}

	if out := stderr(); out != "" {
		t.Errorf("LSP hook wrote %d bytes to stderr, which corrupts the TUI:\n%s", len(out), out)
	}

	msgs := notices()
	if len(msgs) == 0 {
		t.Fatal("expected a notice naming the missing language server, got none")
	}
	if !strings.Contains(msgs[0], "typescript-language-server not found in PATH") {
		t.Errorf("notice does not explain the missing server: %q", msgs[0])
	}
	// The install hint is the only actionable part of the message.
	if !strings.Contains(msgs[0], "npm install -g typescript-language-server typescript") {
		t.Errorf("notice dropped the install hint: %q", msgs[0])
	}
}

// TestLSPHook_EditToolMissingServerReportsNoticeNotStderr covers the same
// reproduction through the `edit` tool, which is the other half of the callback
// gate and reaches ServerFor by a separate branch.
func TestLSPHook_EditToolMissingServerReportsNoticeNotStderr(t *testing.T) {
	notices := captureNotices(t)
	stderr := captureStderr(t)

	mgr := missingTypeScriptManager()
	cb := BuildLSPAfterToolCallback(mgr)

	if _, err := cb(nil, &mockTool{name: "edit"}, nil, map[string]any{"path": "/tmp/x.ts"}, nil); err != nil {
		t.Fatalf("LSP hook returned an error: %v", err)
	}

	if out := stderr(); out != "" {
		t.Errorf("LSP hook wrote %d bytes to stderr, which corrupts the TUI:\n%s", len(out), out)
	}
	if msgs := notices(); len(msgs) == 0 {
		t.Error("expected a notice naming the missing language server, got none")
	}
}

// TestFormatFile_FormatErrorReportsNoticeNotStderr guards the formatter's own
// failure path. A server that is installed but errors during formatting must
// not reach the terminal either.
func TestFormatFile_FormatErrorReportsNoticeNotStderr(t *testing.T) {
	notices := captureNotices(t)
	stderr := captureStderr(t)

	tmpFile := t.TempDir() + "/test.go"
	if err := os.WriteFile(tmpFile, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	mockSrv := &mockServer{formatErr: fmt.Errorf("format failed")}
	result := formatFileWithFormatter(mockSrv, tmpFile, map[string]any{"path": tmpFile})

	if _, ok := result["lsp_formatted"]; ok {
		t.Error("expected no lsp_formatted when format fails")
	}
	if out := stderr(); out != "" {
		t.Errorf("formatter wrote %d bytes to stderr:\n%s", len(out), out)
	}
	if msgs := notices(); len(msgs) == 0 || !strings.Contains(msgs[0], "format failed") {
		t.Errorf("expected a notice carrying the format error, got %q", msgs)
	}
}

// TestLSPHook_NoStderrAcrossAllWritePaths is the broad guard: every branch the
// write/edit hook can take is exercised with stderr and the standard logger
// captured, and none may write. Without it the fix is only as good as the
// branches a reviewer remembers to check.
func TestLSPHook_NoStderrAcrossAllWritePaths(t *testing.T) {
	notices := captureNotices(t)
	stderr := captureStderr(t)

	tmps := t.TempDir()
	existing := tmps + "/existing.go"
	if err := os.WriteFile(existing, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name   string
		tool   string
		path   any
		toolEr error
	}{
		{"missing server", "write", "/tmp/x.ts", nil},
		{"missing server (edit)", "edit", "/tmp/x.ts", nil},
		{"path is not a string", "write", 12345, nil},
		{"path is empty", "write", "", nil},
		{"path key absent", "write", nil, nil},
		{"file unreadable", "write", tmps + "/nope.go", nil},
		{"tool itself failed", "write", existing, fmt.Errorf("write failed")},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mgr := missingTypeScriptManager()
			// Go is available so the "file unreadable" case reaches the read
			// branch rather than stopping at the missing-server branch.
			mgr.languages["go"] = &LanguageConfig{Command: "gopls", FileExtensions: []string{".go"}}
			mgr.available["go"] = true

			cb := BuildLSPAfterToolCallback(mgr)
			args := map[string]any{}
			if tc.path != nil {
				args["path"] = tc.path
			}
			if _, err := cb(nil, &mockTool{name: tc.tool}, nil, args, tc.toolEr); err != nil {
				t.Fatalf("hook returned an error: %v", err)
			}
			// Any error the hook raised must be a notice, never a direct write.
			_ = notices()
		})
	}

	if out := stderr(); out != "" {
		t.Errorf("LSP hook wrote %d bytes to stderr across write paths:\n%s", len(out), out)
	}
}
