// Package specdoc parses the Markdown artifacts a /plan session produces.
//
// It exists so that /plan, /run and the validators all read a spec the same
// way. Before it, gate parsing lived only in the TUI, slice parsing existed in
// two shapes that disagreed, and nothing read PROMPT.md's slice list at all —
// so a PROMPT.md describing a different set of slices than plan.md was
// undetectable.
package specdoc

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Artifact names a /plan phase output. The order is the order the PDD SOP
// produces them in.
const (
	RoughIdea    = "rough-idea.md"
	Requirements = "requirements.md"
	Design       = "design.md"
	Outline      = "outline.md"
	Plan         = "plan.md"
	Prompt       = "PROMPT.md"
	Summary      = "summary.md"
)

// Artifacts lists every artifact the PDD SOP defines, in phase order.
var Artifacts = []string{RoughIdea, Requirements, Design, Outline, Plan, Prompt, Summary}

// Gate is a validation command declared in PROMPT.md's "## Gates" section.
type Gate struct {
	Name    string
	Command string
	Line    int
}

// Slice is one unit of implementable work. It is parsed from plan.md's
// checklist (or slice headings) and from PROMPT.md's "## Implementation
// Slices" list, which is why Files/Verify may be empty: plan.md carries the
// checkbox, PROMPT.md carries the execution detail.
type Slice struct {
	Index        int
	Title        string
	Done         bool
	Files        []string
	Verify       string
	ParallelSafe bool
	HasParallel  bool // whether parallel-safe was stated at all
	Line         int
	Body         string
}

// Spec is a loaded spec directory.
type Spec struct {
	Name     string            // "features/TOO/024-mistral-provider"
	Dir      string            // absolute path
	Files    map[string]string // artifact name -> content, only for files present
	Research []string          // basenames under research/
}

// Load reads every artifact present in <workDir>/specs/<name>. A missing
// artifact is simply absent from Files — that is a validation finding, not a
// load error, so the caller can report all of them at once.
func Load(workDir, name string) (*Spec, error) {
	dir := filepath.Join(workDir, "specs", filepath.FromSlash(name))
	st, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("spec %q: %w", name, err)
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("spec %q: not a directory", name)
	}

	spec := &Spec{Name: name, Dir: dir, Files: map[string]string{}}
	for _, a := range Artifacts {
		b, err := os.ReadFile(filepath.Join(dir, a))
		if err != nil {
			continue
		}
		spec.Files[a] = string(b)
	}

	entries, err := os.ReadDir(filepath.Join(dir, "research"))
	if err == nil {
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
				spec.Research = append(spec.Research, e.Name())
			}
		}
	}
	return spec, nil
}

// Has reports whether the artifact is present and holds anything but whitespace.
func (s *Spec) Has(artifact string) bool {
	return strings.TrimSpace(s.Files[artifact]) != ""
}

// --- headings ---

var headingRe = regexp.MustCompile(`(?m)^(#{1,6})\s+(.+?)\s*$`)

// Headings returns every Markdown heading's text, in document order, with the
// leading hashes and any trailing punctuation stripped.
func Headings(content string) []string {
	var out []string
	for _, m := range headingRe.FindAllStringSubmatch(content, -1) {
		out = append(out, strings.TrimSpace(m[2]))
	}
	return out
}

// HasHeading reports whether any heading contains want, case-insensitively.
// Containment rather than equality: real specs write "## Gates" but also
// "## Acceptance Criteria (Given/When/Then)".
func HasHeading(content, want string) bool {
	want = strings.ToLower(want)
	for _, h := range Headings(content) {
		if strings.Contains(strings.ToLower(h), want) {
			return true
		}
	}
	return false
}

// Section returns the lines under the first heading containing name, up to the
// next heading at the same or shallower depth. The heading line is excluded.
func Section(content, name string) string {
	lines := strings.Split(content, "\n")
	want := strings.ToLower(name)

	start, depth := -1, 0
	for i, line := range lines {
		m := headingRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		if start == -1 {
			if strings.Contains(strings.ToLower(m[2]), want) {
				start, depth = i+1, len(m[1])
			}
			continue
		}
		if len(m[1]) <= depth {
			return strings.Join(lines[start:i], "\n")
		}
	}
	if start == -1 {
		return ""
	}
	return strings.Join(lines[start:], "\n")
}

// --- gates ---

// gateRe matches "- **name**: `command`" and "- name: `command`".
var gateRe = regexp.MustCompile("^-\\s+\\*{0,2}([^*:]+?)\\*{0,2}\\s*:\\s*`([^`]+)`")

// ParseGates extracts the gates declared in PROMPT.md's "## Gates" section.
func ParseGates(promptMD string) []Gate {
	body := Section(promptMD, "Gates")
	if body == "" {
		return nil
	}
	offset := strings.Index(promptMD, body)
	base := 1
	if offset >= 0 {
		base = strings.Count(promptMD[:offset], "\n") + 1
	}

	var gates []Gate
	for i, line := range strings.Split(body, "\n") {
		m := gateRe.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		gates = append(gates, Gate{
			Name:    strings.TrimSpace(m[1]),
			Command: strings.TrimSpace(m[2]),
			Line:    base + i,
		})
	}
	return gates
}

// --- slices ---

var (
	checkboxRe = regexp.MustCompile(`^-\s+\[([ xX])\]\s+(.+)$`)
	// numberedSliceRe matches an explicitly numbered slice heading at any depth:
	// real plans write "## Slice 1 — Title" as often as "### Slice 1: Title".
	numberedSliceRe = regexp.MustCompile(`^#{2,4}\s+(?:Slice|Step)\s+(\d+)\s*[:—–-]?\s*(.*)$`)
	// sliceHeadingRe is the older fallback: any H3 is a slice.
	sliceHeadingRe = regexp.MustCompile(`^###\s+(?:Slice\s+(\d+):?\s*)?(.+)$`)
	stepPrefixRe   = regexp.MustCompile(`^(?:Step|Slice)\s+\d+\s*[:.]\s*`)
	promptSliceRe  = regexp.MustCompile(`^(\d+)\.\s+(.+)$`)
	verifyRe       = regexp.MustCompile("(?i)verify\\s*:\\s*`([^`]+)`")
	filesRe        = regexp.MustCompile("(?i)files\\s*:\\s*((?:\\s*`[^`]+`\\s*,?)+)")
	backtickRe     = regexp.MustCompile("`([^`]+)`")
	parallelRe     = regexp.MustCompile(`(?i)parallel[- ]safe\s*:\s*(yes|no|true|false)`)
)

// ParsePlanSlices extracts slices from plan.md. Checkbox lines are
// authoritative; "### Slice N" headings are the fallback for plans written
// before the checklist convention.
func ParsePlanSlices(planMD string) []Slice {
	lines := strings.Split(planMD, "\n")

	var boxes []Slice
	for i, line := range lines {
		m := checkboxRe.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		title := stepPrefixRe.ReplaceAllString(strings.TrimSpace(m[2]), "")
		boxes = append(boxes, Slice{
			Index: len(boxes) + 1,
			Title: title,
			Done:  strings.EqualFold(m[1], "x"),
			Line:  i + 1,
		})
	}
	if len(boxes) > 0 {
		return boxes
	}

	// Explicitly numbered slice headings come next: they are unambiguous, and
	// counting them at any depth avoids reading "## Gates" or "## Constraints"
	// as slices the way a depth-only rule would.
	var numbered []Slice
	for i, line := range lines {
		m := numberedSliceRe.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		numbered = append(numbered, Slice{
			Index: len(numbered) + 1,
			Title: strings.TrimSpace(m[2]),
			Line:  i + 1,
		})
	}
	if len(numbered) > 0 {
		return numbered
	}

	var heads []Slice
	for i, line := range lines {
		m := sliceHeadingRe.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		heads = append(heads, Slice{
			Index: len(heads) + 1,
			Title: strings.TrimSpace(m[2]),
			Line:  i + 1,
		})
	}
	return heads
}

// SliceHeadings counts explicitly numbered slice headings. outline.md states
// its slices as headings rather than as a list, so a rule that only counts
// list items reads a perfectly good outline as empty.
func SliceHeadings(content string) int {
	n := 0
	for _, line := range strings.Split(content, "\n") {
		if numberedSliceRe.MatchString(strings.TrimSpace(line)) {
			n++
		}
	}
	return n
}

// ParsePromptSlices extracts the numbered list under PROMPT.md's
// "## Implementation Slices". Each entry runs until the next numbered item, so
// a slice whose detail wraps across lines is read whole.
func ParsePromptSlices(promptMD string) []Slice {
	body := Section(promptMD, "Implementation Slices")
	if body == "" {
		return nil
	}
	offset := strings.Index(promptMD, body)
	base := 1
	if offset >= 0 {
		base = strings.Count(promptMD[:offset], "\n") + 1
	}

	var slices []Slice
	var cur *Slice
	var buf []string

	flush := func() {
		if cur == nil {
			return
		}
		cur.Body = strings.Join(buf, "\n")
		applySliceDetail(cur)
		slices = append(slices, *cur)
		cur, buf = nil, nil
	}

	for i, line := range strings.Split(body, "\n") {
		m := promptSliceRe.FindStringSubmatch(strings.TrimSpace(line))
		if m != nil {
			flush()
			cur = &Slice{Index: len(slices) + 1, Title: sliceTitle(m[2]), Line: base + i}
			buf = []string{m[2]}
			continue
		}
		if cur != nil {
			buf = append(buf, line)
		}
	}
	flush()
	return slices
}

// sliceTitle takes the first bolded run as the name, falling back to the text
// up to the first em-dash or the whole line.
func sliceTitle(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "**"); i >= 0 {
		if j := strings.Index(s[i+2:], "**"); j >= 0 {
			return strings.TrimSpace(s[i+2 : i+2+j])
		}
	}
	for _, sep := range []string{" — ", " – ", " - "} {
		if i := strings.Index(s, sep); i > 0 {
			return strings.TrimSpace(s[:i])
		}
	}
	return s
}

func applySliceDetail(s *Slice) {
	if m := verifyRe.FindStringSubmatch(s.Body); m != nil {
		s.Verify = strings.TrimSpace(m[1])
	}
	if m := filesRe.FindStringSubmatch(s.Body); m != nil {
		for _, f := range backtickRe.FindAllStringSubmatch(m[1], -1) {
			s.Files = append(s.Files, strings.TrimSpace(f[1]))
		}
	}
	if m := parallelRe.FindStringSubmatch(s.Body); m != nil {
		s.HasParallel = true
		v := strings.ToLower(m[1])
		s.ParallelSafe = v == "yes" || v == "true"
	}
}

// --- done criteria ---

var doneItemRe = regexp.MustCompile(`^-\s+(?:\[[ xX]\]\s+)?(.+)$`)

// DoneCriteria returns the bullet items under "## Done Criteria".
func DoneCriteria(promptMD string) []string {
	body := Section(promptMD, "Done Criteria")
	if body == "" {
		return nil
	}
	var out []string
	for _, line := range strings.Split(body, "\n") {
		m := doneItemRe.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		if t := strings.TrimSpace(m[1]); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// References returns the backticked paths listed under "## Reference".
func References(promptMD string) []string {
	body := Section(promptMD, "Reference")
	if body == "" {
		return nil
	}
	var out []string
	for _, m := range backtickRe.FindAllStringSubmatch(body, -1) {
		if p := strings.TrimSpace(m[1]); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// CountLines reports the number of lines in content.
func CountLines(content string) int {
	if content == "" {
		return 0
	}
	n := strings.Count(content, "\n")
	if !strings.HasSuffix(content, "\n") {
		n++
	}
	return n
}
