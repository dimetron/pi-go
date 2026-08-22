package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/text/unicode/norm"
)

// blockedPaths are files that are not files in any useful sense: reading them
// either never returns or returns an unbounded stream of nothing.
//
// They are refused before any I/O, because the damage is done by the open call
// itself — /dev/zero will happily fill the byte budget with NULs, and
// /dev/stdin can block the whole turn.
var blockedPaths = map[string]bool{
	"/dev/zero":    true,
	"/dev/random":  true,
	"/dev/urandom": true,
	"/dev/stdin":   true,
	"/dev/stdout":  true,
	"/dev/stderr":  true,
	"/dev/tty":     true,
	"/dev/null":    true,
	"/dev/full":    true,
}

// isBlockedPath reports whether path names a device that must not be read.
func isBlockedPath(path string) bool {
	// Slash-normalized: these are POSIX device names, and a model hands them
	// over spelled that way whatever it is running on. filepath.Clean would
	// turn them into \dev\zero on Windows and the lookup would miss.
	clean := filepath.ToSlash(filepath.Clean(path))
	if blockedPaths[clean] {
		return true
	}
	// /proc/<pid>/fd/* is the same hazard wearing a different name: reading one
	// can block forever on a pipe the agent has no control over.
	if strings.HasPrefix(clean, "/proc/") && strings.Contains(clean, "/fd/") {
		return true
	}
	return false
}

// quoteReplacer maps the characters a model most often substitutes when it
// echoes a path back through prose. Smart quotes come from documentation and
// chat clients; the exotic spaces come from copied terminal output.
var quoteReplacer = strings.NewReplacer(
	"‘", "'", // left single quote
	"’", "'", // right single quote
	"“", `"`, // left double quote
	"”", `"`, // right double quote
	" ", " ", // non-breaking space
	" ", " ", // figure space
	" ", " ", // narrow no-break space
	" ", " ", // thin space
)

// pathCandidates returns the spellings to try for a path that did not resolve,
// most likely first and without duplicates.
//
// The same filename can be spelled several ways that compare unequal as bytes:
// macOS stores names NFD-decomposed while most tools emit NFC, and a path that
// has been through a chat client or a docs page may carry curly quotes or an
// exotic space. Each is a real file the model cannot open for a reason it
// cannot see.
func pathCandidates(path string) []string {
	seen := map[string]bool{path: true}
	out := []string{path}

	add := func(s string) {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}

	add(norm.NFC.String(path))
	add(norm.NFD.String(path))
	add(quoteReplacer.Replace(path))
	add(norm.NFC.String(quoteReplacer.Replace(path)))
	add(strings.TrimSpace(path))
	add(strings.Trim(path, `"'`))

	return out
}

// resolveReadPath returns the spelling of path that actually exists.
//
// It returns the original path unchanged when that already resolves, so the
// common case costs one Stat and nothing else.
func resolveReadPath(sb *Sandbox, path string) (string, os.FileInfo, error) {
	if isBlockedPath(path) {
		return "", nil, fmt.Errorf("%s is a device file, not a readable file; reading it would block or return unbounded data", path)
	}

	info, err := sb.Stat(path)
	if err == nil {
		return path, info, nil
	}
	firstErr := err

	for _, candidate := range pathCandidates(path)[1:] {
		if isBlockedPath(candidate) {
			continue
		}
		if info, err := sb.Stat(candidate); err == nil {
			return candidate, info, nil
		}
	}

	if suggestion := didYouMean(sb, path); suggestion != "" {
		return "", nil, fmt.Errorf("reading file: %w%s", firstErr, suggestion)
	}
	return "", nil, fmt.Errorf("reading file: %w", firstErr)
}

// didYouMean returns a " (did you mean ...)" clause naming near neighbors of a
// path that does not exist, or "" when nothing is close enough.
//
// A bare ENOENT costs at least one turn to recover from — the model has to list
// the directory to find out what it got wrong. Naming the candidates turns that
// into a correction on the next call.
func didYouMean(sb *Sandbox, path string) string {
	dir := filepath.Dir(path)
	base := filepath.Base(path)

	entries, err := sb.ReadDir(dir)
	if err != nil || len(entries) == 0 {
		return ""
	}

	var matches []string
	for _, e := range entries {
		name := e.Name()
		if name == base {
			continue
		}
		if strings.Contains(strings.ToLower(name), strings.ToLower(base)) ||
			strings.Contains(strings.ToLower(base), strings.ToLower(name)) ||
			withinEditDistance(name, base, 2) {
			matches = append(matches, name)
			if len(matches) == 3 {
				break
			}
		}
	}
	if len(matches) == 0 {
		return ""
	}
	return fmt.Sprintf(" (did you mean %s?)", strings.Join(quoteAll(matches), ", "))
}

func quoteAll(names []string) []string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = fmt.Sprintf("%q", n)
	}
	return out
}

// withinEditDistance reports whether a and b are at most maxDist edits apart.
//
// The bound is what keeps this cheap and the suggestions relevant: a length
// difference greater than maxDist is decided without any work, and the table is
// only ever maxDist wide in practice.
func withinEditDistance(a, b string, maxDist int) bool {
	if a == b {
		return true
	}
	ar, br := []rune(a), []rune(b)
	if abs(len(ar)-len(br)) > maxDist {
		return false
	}

	prev := make([]int, len(br)+1)
	curr := make([]int, len(br)+1)
	for j := range prev {
		prev[j] = j
	}

	for i := 1; i <= len(ar); i++ {
		curr[0] = i
		rowMin := curr[0]
		for j := 1; j <= len(br); j++ {
			cost := 1
			if ar[i-1] == br[j-1] {
				cost = 0
			}
			curr[j] = min(min(curr[j-1]+1, prev[j]+1), prev[j-1]+cost)
			rowMin = min(rowMin, curr[j])
		}
		// Every remaining row can only grow, so once the best cell in a row is
		// past the bound the answer is settled.
		if rowMin > maxDist {
			return false
		}
		prev, curr = curr, prev
	}
	return prev[len(br)] <= maxDist
}
