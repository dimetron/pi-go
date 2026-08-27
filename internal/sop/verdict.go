package sop

import (
	"fmt"
	"regexp"
	"strings"
)

// Verdict is a review stage's structured result.
//
// The prose contract asked the Verifier to "End your reply with exactly one
// line: VERDICT: PASS or VERDICT: FAIL", and nothing read the answer back into
// control flow. Across the coordinator sessions in the corpus the verdict was
// PASS 9 times out of 9 — not because the work was sound, but because a
// verdict nobody routes on cannot fail.
//
// Parsing it into a value is the first half of the fix; routing an edge on it
// is the second.
type Verdict struct {
	Result string  `json:"verdict"` // VerdictPass | VerdictFail
	Unmet  []Unmet `json:"unmet,omitempty"`
	// Stated is false when no verdict line was found at all. An absent verdict
	// is not a pass: it means the Verifier never ran, or never finished.
	Stated bool `json:"stated"`
}

// Unmet is one Done Criterion the Verifier judged not met.
type Unmet struct {
	Criterion string `json:"criterion"`
	File      string `json:"file,omitempty"`
	Line      int    `json:"line,omitempty"`
	Why       string `json:"why,omitempty"`
}

// Verdict results.
const (
	VerdictPass = "PASS"
	VerdictFail = "FAIL"
)

// Passed reports whether the review may advance. An unstated verdict never
// passes.
func (v Verdict) Passed() bool { return v.Stated && v.Result == VerdictPass }

// Reason explains a non-passing verdict for the retry briefing.
func (v Verdict) Reason() string {
	switch {
	case !v.Stated:
		return "the Verifier produced no VERDICT line, so the review did not complete"
	case v.Result == VerdictFail && len(v.Unmet) > 0:
		items := make([]string, 0, len(v.Unmet))
		for _, u := range v.Unmet {
			entry := "- " + u.Criterion
			if u.File != "" {
				entry += fmt.Sprintf(" (%s", u.File)
				if u.Line > 0 {
					entry += fmt.Sprintf(":%d", u.Line)
				}
				entry += ")"
			}
			if u.Why != "" {
				entry += " — " + u.Why
			}
			items = append(items, entry)
		}
		return "the Verifier reported these criteria NOT MET:\n" + strings.Join(items, "\n")
	case v.Result == VerdictFail:
		return "the Verifier returned VERDICT: FAIL"
	default:
		return ""
	}
}

var (
	verdictRe = regexp.MustCompile(`(?im)^\s*\**\s*VERDICT\s*\**\s*:\s*\**\s*(PASS|FAIL)\b`)
	unmetRe   = regexp.MustCompile(`(?im)^\s*[-*]?\s*(.+?)\s*[-—:]\s*NOT\s+MET\b\s*(.*)$`)
	fileRefRe = regexp.MustCompile("`?([\\w./-]+\\.[a-zA-Z]+)`?(?::(\\d+))?")
)

// ParseVerdict extracts a verdict from a review agent's reply.
//
// The last VERDICT line wins: a Verifier that reasons aloud may echo the word
// while explaining, and its conclusion is what counts.
func ParseVerdict(text string) Verdict {
	matches := verdictRe.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return Verdict{Stated: false}
	}
	v := Verdict{
		Result: strings.ToUpper(matches[len(matches)-1][1]),
		Stated: true,
	}
	if v.Result == VerdictFail {
		v.Unmet = parseUnmet(text)
	}
	return v
}

func parseUnmet(text string) []Unmet {
	var out []Unmet
	for _, m := range unmetRe.FindAllStringSubmatch(text, -1) {
		criterion := strings.TrimSpace(strings.Trim(m[1], "*`"))
		if criterion == "" {
			continue
		}
		u := Unmet{Criterion: criterion, Why: strings.TrimSpace(m[2])}
		if ref := fileRefRe.FindStringSubmatch(m[2]); ref != nil {
			u.File = ref[1]
			if ref[2] != "" {
				u.Line = atoi(ref[2])
			}
		}
		out = append(out, u)
	}
	return out
}

func atoi(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	return n
}

// SliceResult is a worker's structured report on one slice.
//
// VerifyPassed is set by whoever ran the verify command, not by the worker
// claiming it. That distinction is the whole point: plan.md checkboxes in the
// corpus read 0/50, 0/41 and 6/41 because ticking one was an instruction
// rather than a consequence.
type SliceResult struct {
	Slice        int      `json:"slice"`
	Title        string   `json:"title,omitempty"`
	Status       string   `json:"status"` // done | blocked | partial
	FilesChanged []string `json:"files_changed,omitempty"`
	VerifyCmd    string   `json:"verify_cmd,omitempty"`
	VerifyPassed bool     `json:"verify_passed"`
	Blockers     []string `json:"blockers,omitempty"`
	Notes        string   `json:"notes,omitempty"`
}

// Done reports whether the slice may be ticked: it must claim completion and
// its verify command must have actually passed.
func (r SliceResult) Done() bool {
	return r.Status == "done" && r.VerifyPassed
}
