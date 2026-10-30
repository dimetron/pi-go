package tools

import (
	"fmt"
	"regexp"
	"strings"
)

// heredocHeader matches an unquoted heredoc redirect: `<<EOF` or `<<-EOF`.
//
// The quoted forms need no separate exclusion. The delimiter pattern starts with
// a letter, so `<<'EOF'`, `<<"EOF"` and `<<\EOF` fail to match here at all —
// which is correct, because bash treats those bodies as literal text and they
// cannot produce the failure this file describes.
var heredocHeader = regexp.MustCompile(`<<-?([A-Za-z_][A-Za-z0-9_]*)`)

// bashLinePrefix matches the location prefix bash puts on a diagnostic, such as
// `bash: line 1: ` or `repro.sh: line 3: `, so it can be stripped before the
// offending text is quoted back to the caller.
var bashLinePrefix = regexp.MustCompile(`^[^:\n]*: line \d+: `)

// heredocBodyHasExpansion reports whether a heredoc body contains anything bash
// would try to expand. A body with no `$` or backtick cannot trip the failure,
// and warning about it would put a note on the ordinary `cat > x <<EOF` that
// works fine.
func heredocBodyHasExpansion(body string) bool {
	return strings.ContainsAny(body, "$`")
}

// findUnquotedHeredoc returns the delimiter of the first unquoted heredoc whose
// body bash would expand, and the body itself.
func findUnquotedHeredoc(command string) (delimiter, body string, ok bool) {
	lines := strings.Split(command, "\n")
	for i := 0; i < len(lines); i++ {
		m := heredocHeader.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		delim := m[1]
		var bodyLines []string
		end := len(lines)
		for j := i + 1; j < len(lines); j++ {
			if strings.TrimSpace(lines[j]) == delim {
				end = j
				break
			}
			bodyLines = append(bodyLines, lines[j])
		}
		joined := strings.Join(bodyLines, "\n")
		if heredocBodyHasExpansion(joined) {
			return delim, joined, true
		}
		// Skip past this heredoc so its body is not rescanned for a header.
		i = end
	}
	return "", "", false
}

// heredocExpansionBody extracts the text bash echoed back as the offending
// command, from a `bad substitution` diagnostic.
//
// The message is spread over several lines (bash echoes the whole body), so this
// slices rather than regex-matches: `.` does not cross a newline without (?s),
// and a pattern that quietly failed to span one would silently never fire.
func heredocExpansionBody(stderr string) (string, bool) {
	const marker = ": bad substitution"
	idx := strings.Index(stderr, marker)
	if idx < 0 {
		return "", false
	}
	echoed := strings.TrimSpace(bashLinePrefix.ReplaceAllString(stderr[:idx], ""))
	return echoed, true
}

// heredocNote explains a silent truncation caused by an unquoted heredoc.
//
// It is worth surfacing because nothing else reports it. A `${...}` bash cannot
// parse inside an unquoted heredoc aborts only the command being fed from that
// heredoc; bash carries on with the rest of the script, and the redirect has
// already created the target file. The outcome is a zero-byte file, exit status
// 0, and no indication that the write did not happen. Observed in session
// 261030-1206-80357-8852b, where the model wrote a Python patcher this way, then
// read the truncated file it had meant to edit and acted on that.
func heredocNote(command string, out BashOutput) string {
	if out.Running || out.ExitCode != 0 || out.Stderr == "" {
		return ""
	}
	echoed, ok := heredocExpansionBody(out.Stderr)
	if !ok {
		return ""
	}
	delim, _, ok := findUnquotedHeredoc(command)
	if !ok {
		return ""
	}

	var what string
	if echoed == "" {
		what = "a heredoc body was expanded by bash and rejected"
	} else {
		what = fmt.Sprintf("heredoc body beginning %q was expanded by bash and rejected", firstLine(echoed))
	}

	return fmt.Sprintf("%s. "+
		"The <<%s delimiter is unquoted, so bash expanded the body and wrote what was left. "+
		"Any file this command redirected may now be empty or truncated even though the exit status is 0. "+
		"Verify that file before relying on it, and quote the delimiter (<<'%s') to keep the body literal.",
		what, delim, delim)
}

// appendNote joins a new note onto any existing one without dropping either.
func appendNote(existing, add string) string {
	switch {
	case add == "":
		return existing
	case existing == "":
		return add
	default:
		return existing + " " + add
	}
}

// firstLine returns s's first line, shortened to keep a note readable.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	const max = 80
	if len(s) > max {
		return s[:max] + "..."
	}
	return s
}
