package browser

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

// recordEnv names the file the test binary writes the URL it was handed to
// when it is re-executed as a stand-in browser. See TestMain.
const recordEnv = "PI_TEST_BROWSER_RECORD"

// TestMain doubles as the fake browser. Open resolves handlers through
// exec.LookPath and runs them, so the stand-in has to be a real executable:
// a #!/bin/sh script is neither executable nor even findable on Windows, where
// LookPath only accepts the PATHEXT suffixes. Re-executing the test binary
// works everywhere.
func TestMain(m *testing.M) {
	if record := os.Getenv(recordEnv); record != "" {
		// Acting as the browser: the URL is the last argument we were given.
		_ = os.WriteFile(record, []byte(os.Args[len(os.Args)-1]), 0o600)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestHandlers(t *testing.T) {
	t.Parallel()

	const url = "https://auth.openai.com/codex/device"

	tests := []struct {
		name       string
		goos       string
		browserEnv string
		want       [][]string
	}{
		{
			name: "darwin uses open",
			goos: "darwin",
			want: [][]string{{"open", url}},
		},
		{
			// The empty title argument is load-bearing: without it start reads
			// the URL as a window title and opens nothing.
			name: "windows passes start an empty title",
			goos: "windows",
			want: [][]string{{"cmd", "/c", "start", "", url}},
		},
		{
			name: "linux falls through the freedesktop handlers",
			goos: "linux",
			want: [][]string{
				{"xdg-open", url},
				{"gio", "open", url},
				{"wslview", url},
				{"x-www-browser", url},
				{"www-browser", url},
			},
		},
		{
			// The regression this package exists for: in a dev container the
			// only handler that reaches a human is the one VS Code injects, so
			// it has to be tried before the platform defaults.
			name:       "BROWSER wins over the platform default",
			goos:       "linux",
			browserEnv: "/vscode/bin/helpers/browser.sh",
			want: [][]string{
				{"/vscode/bin/helpers/browser.sh", url},
				{"xdg-open", url},
				{"gio", "open", url},
				{"wslview", url},
				{"x-www-browser", url},
				{"www-browser", url},
			},
		},
		{
			name:       "BROWSER is honored on darwin too",
			goos:       "darwin",
			browserEnv: "/usr/local/bin/open-in-host",
			want: [][]string{
				{"/usr/local/bin/open-in-host", url},
				{"open", url},
			},
		},
		{
			name: "unknown platform still gets the unix handlers",
			goos: "freebsd",
			want: [][]string{
				{"xdg-open", url},
				{"gio", "open", url},
				{"wslview", url},
				{"x-www-browser", url},
				{"www-browser", url},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := handlers(tt.goos, tt.browserEnv, url)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("handlers mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestOpen_NoHandler checks the failure that callers depend on: Open must
// report ErrNoHandler rather than an exec error, so login can fall back to
// printing the URL instead of aborting.
func TestOpen_NoHandler(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("BROWSER", "")

	if err := Open("https://example.invalid/"); !errors.Is(err, ErrNoHandler) {
		t.Errorf("Open() = %v, want ErrNoHandler", err)
	}
}

// TestOpen_UsesBrowserEnv is the dev-container case this package exists for:
// $BROWSER is the only handler that reaches a human, so it must be run in
// preference to anything installed alongside it.
//
// The stand-in handler is a script that records its arguments, which also pins
// that Open passes the URL through unmangled.
func TestOpen_UsesBrowserEnv(t *testing.T) {
	record := filepath.Join(t.TempDir(), "opened.txt")
	t.Setenv(recordEnv, record)
	t.Setenv("BROWSER", os.Args[0])

	const url = "https://auth.openai.com/codex/device"
	if err := Open(url); err != nil {
		t.Fatalf("Open() = %v, want nil when $BROWSER can run", err)
	}

	// Open uses Start, not Run, so the handler may not have finished yet.
	var got []byte
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(record); err == nil && len(b) > 0 {
			got = b
			break
		}
		time.Sleep(2 * time.Millisecond)
	}

	if string(got) != url {
		t.Errorf("$BROWSER received %q, want %q", got, url)
	}
}

// An entry that is not on PATH must be skipped rather than aborting the whole
// search — that fallthrough is what makes the per-platform handler list useful.
func TestOpen_SkipsMissingHandlers(t *testing.T) {
	t.Setenv(recordEnv, filepath.Join(t.TempDir(), "opened.txt"))

	// An empty PATH means every platform default fails LookPath; only the
	// absolute $BROWSER path can resolve.
	t.Setenv("PATH", t.TempDir())
	t.Setenv("BROWSER", os.Args[0])

	if err := Open("https://example.invalid/"); err != nil {
		t.Errorf("Open() = %v, want nil; the reachable handler was skipped", err)
	}
}
