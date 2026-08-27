// Package validate checks the artifacts a /plan session produces against a
// machine-readable contract.
//
// Before it, the only check on a whole plan was a single os.Stat on PROMPT.md
// (internal/tui/plan.go). Everything else the PDD SOP asks for — an outline,
// slices that name their files and verify command, gates that actually run,
// Done Criteria that are not template placeholders — was prose the model could
// decline. Across 53 spec directories, 3 had a complete artifact set and
// outline.md was missing from 70%.
//
// A rule here returns Findings rather than a bool, because a Finding is fed
// back to the agent that produced the artifact: Message says what is wrong and
// Fix says what to do about it.
package validate

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Severity distinguishes a finding that blocks the pipeline from one that is
// only worth saying.
type Severity string

const (
	// SeverityError blocks: /plan will not finish and /run will not start.
	SeverityError Severity = "error"
	// SeverityWarn is reported and recorded but blocks nothing.
	SeverityWarn Severity = "warn"
)

// Finding is one rule violation.
type Finding struct {
	Artifact string   `json:"artifact"`
	Rule     string   `json:"rule"`
	Severity Severity `json:"severity"`
	Line     int      `json:"line,omitempty"`
	Message  string   `json:"message"`
	Fix      string   `json:"fix,omitempty"`
}

func (f Finding) String() string {
	loc := f.Artifact
	if f.Line > 0 {
		loc = fmt.Sprintf("%s:%d", f.Artifact, f.Line)
	}
	return fmt.Sprintf("[%s] %s (%s): %s", f.Severity, loc, f.Rule, f.Message)
}

// Findings is a rule result set.
type Findings []Finding

// Errors returns only the blocking findings.
func (fs Findings) Errors() Findings {
	var out Findings
	for _, f := range fs {
		if f.Severity == SeverityError {
			out = append(out, f)
		}
	}
	return out
}

// OK reports whether nothing blocking was found.
func (fs Findings) OK() bool { return len(fs.Errors()) == 0 }

// Format renders findings as a Markdown list, errors first.
func (fs Findings) Format() string {
	if len(fs) == 0 {
		return "No findings."
	}
	sorted := append(Findings(nil), fs...)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].Severity == SeverityError && sorted[j].Severity != SeverityError
	})
	var b strings.Builder
	for _, f := range sorted {
		loc := f.Artifact
		if f.Line > 0 {
			loc = fmt.Sprintf("%s:%d", f.Artifact, f.Line)
		}
		fmt.Fprintf(&b, "- **%s** `%s` — %s", strings.ToUpper(string(f.Severity)), loc, f.Message)
		if f.Fix != "" {
			fmt.Fprintf(&b, "\n  - fix: %s", f.Fix)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// --- rule arguments ---

// Args holds a rule invocation's arguments. The three shapes cover every rule
// the SOP schema needs: `max_lines(2000)`, `has_headings(["A","B"])`, and
// `slice_budget(max_files: 10, max_changed_lines: 400)`.
type Args struct {
	Positional []string
	List       []string
	Named      map[string]string
}

// Int returns the named argument, or the first positional one when name is
// empty or absent, falling back to def.
func (a Args) Int(name string, def int) int {
	if v, ok := a.Named[name]; ok {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return n
		}
	}
	for _, p := range a.Positional {
		if n, err := strconv.Atoi(strings.TrimSpace(p)); err == nil {
			return n
		}
	}
	return def
}

// Bool returns the named boolean argument, or def.
func (a Args) Bool(name string, def bool) bool {
	if v, ok := a.Named[name]; ok {
		b, err := strconv.ParseBool(strings.TrimSpace(v))
		if err == nil {
			return b
		}
	}
	return def
}

// Items returns the list argument, falling back to the positional strings.
func (a Args) Items() []string {
	if len(a.List) > 0 {
		return a.List
	}
	return a.Positional
}

var (
	ruleCallRe = regexp.MustCompile(`^\s*([a-z0-9_]+)\s*(?:\((.*)\)\s*)?$`)
	quotedRe   = regexp.MustCompile(`"([^"]*)"|'([^']*)'`)
)

// ParseRule splits a rule spec such as `slice_budget(max_files: 10)` into its
// name and arguments.
func ParseRule(spec string) (string, Args, error) {
	m := ruleCallRe.FindStringSubmatch(spec)
	if m == nil {
		return "", Args{}, fmt.Errorf("malformed rule %q", spec)
	}
	name := m[1]
	args := Args{Named: map[string]string{}}
	body := strings.TrimSpace(m[2])
	if body == "" {
		return name, args, nil
	}

	// A bracketed list is the whole argument: has_headings(["A","B"]).
	if strings.HasPrefix(body, "[") && strings.HasSuffix(body, "]") {
		for _, q := range quotedRe.FindAllStringSubmatch(body, -1) {
			args.List = append(args.List, firstNonEmpty(q[1], q[2]))
		}
		if len(args.List) == 0 {
			for _, part := range splitTop(strings.Trim(body, "[]")) {
				if p := strings.TrimSpace(part); p != "" {
					args.List = append(args.List, p)
				}
			}
		}
		return name, args, nil
	}

	for _, part := range splitTop(body) {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if k, v, ok := strings.Cut(part, ":"); ok && !strings.HasPrefix(part, `"`) {
			args.Named[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"'`)
			continue
		}
		args.Positional = append(args.Positional, strings.Trim(part, `"'`))
	}
	return name, args, nil
}

// splitTop splits on commas that are not inside brackets or quotes.
func splitTop(s string) []string {
	var out []string
	depth, quote := 0, rune(0)
	start := 0
	for i, r := range s {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			}
		case r == '"' || r == '\'':
			quote = r
		case r == '[' || r == '(':
			depth++
		case r == ']' || r == ')':
			depth--
		case r == ',' && depth == 0:
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
