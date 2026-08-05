package browser

import (
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"
)

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
