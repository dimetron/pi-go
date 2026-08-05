// Package browser opens URLs in whatever browser the user can actually see.
//
// pi runs in three places that disagree about what "the browser" means: a
// desktop session, a remote dev container or Codespace the user reaches over
// SSH, and a headless CI runner. Only the first has a browser of its own, so
// Open works through the handoff mechanisms each environment provides and
// reports ErrNoHandler when there is none — letting the caller print the URL
// instead of failing a login that would otherwise have completed.
package browser

import (
	"errors"
	"os"
	"os/exec"
	"runtime"
)

// ErrNoHandler reports that the environment provides no program able to open a
// URL.
var ErrNoHandler = errors.New("no browser handler found")

// Open opens url in the user's browser, returning ErrNoHandler if nothing in
// the environment can.
func Open(url string) error {
	for _, argv := range handlers(runtime.GOOS, os.Getenv("BROWSER"), url) {
		if _, err := exec.LookPath(argv[0]); err != nil {
			continue
		}
		// Start rather than Run: xdg-open and open hand off and exit, but a
		// browser invoked directly — x-www-browser with no running instance —
		// stays up until the user closes it, which would block the login.
		if err := exec.Command(argv[0], argv[1:]...).Start(); err == nil {
			return nil
		}
	}
	return ErrNoHandler
}

// handlers returns the candidate argv lists to try, most specific first. It is
// pure so the per-platform ordering is testable without launching anything.
func handlers(goos, browserEnv, url string) [][]string {
	var argvs [][]string
	// $BROWSER is how VS Code Remote and Codespaces hand a URL back to the
	// machine the user is sitting at. It is set inside the container, so it has
	// to be tried before anything installed alongside it.
	if browserEnv != "" {
		argvs = append(argvs, []string{browserEnv, url})
	}

	switch goos {
	case "darwin":
		return append(argvs, []string{"open", url})
	case "windows":
		// The empty string is start's title argument. Without it start reads a
		// quoted URL as the window title and opens nothing.
		return append(argvs, []string{"cmd", "/c", "start", "", url})
	default:
		return append(argvs,
			[]string{"xdg-open", url},      // freedesktop, the usual answer
			[]string{"gio", "open", url},   // GNOME, successor to gvfs-open
			[]string{"wslview", url},       // WSL, hands off to the Windows host
			[]string{"x-www-browser", url}, // Debian alternatives system
			[]string{"www-browser", url},
		)
	}
}
