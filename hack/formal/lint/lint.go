// Package formal lints the TLA+ models under hack/formal for the mistakes that
// make a model check pass for the wrong reason.
//
// Every check here exists because that mistake produced a false green while these
// models were being written:
//
//	unassigned-variable   A variable no action assigns. TLC reports it as
//	                      "Successor state is not completely specified", but only
//	                      on the paths it explores, and any rule over such a
//	                      variable is vacuously true. An entire gate rule was
//	                      silently vacuous this way.
//	duplicate-assignment  The same variable assigned twice in one branch. TLA+
//	                      conjoins them, so a contradicting assignment can never
//	                      fire. This hid the ordering bug the /goal model exists
//	                      to find.
//	assigned-and-unchanged A variable both assigned and listed in an UNCHANGED
//	                      tuple pins it twice. A bulk edit made every action in
//	                      the session model unsatisfiable this way, and the run
//	                      explored two states while reporting success.
//	vacuous-rule          A rule whose premise needs a literal no action produces.
//
// The lint is branch-aware: THEN/ELSE and a leading \/ start separate branches, so
// an action that differs per branch is not read as a contradiction.
package formal

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Finding is one structural problem in one spec. String renders it the way the
// command prints it, so a caller that logs findings and a caller that compares
// them see the same text.
type Finding struct {
	File   string
	Action string
	Rule   string
	Kind   string
	Detail string
}

func (f Finding) String() string {
	loc := f.File
	if f.Action != "" {
		loc += ":" + f.Action
	}
	return fmt.Sprintf("%s: %s: %s", loc, f.Kind, f.Detail)
}

var (
	// A parameterised action (ReadVerdict(outcome) ==) is a boundary too. Missing
	// it folds one action's assignments into the previous one and invents
	// duplicate-assignment findings.
	actionRe = regexp.MustCompile(`(?m)^([A-Z]\w*)\s*(?:\([^)]*\))?\s*==`)
	assignRe = regexp.MustCompile(`\b(\w+)'\s*=`)
	// An UNCHANGED tuple of variables, as opposed to `UNCHANGED vars`.
	unchangedTupleRe = regexp.MustCompile(`UNCHANGED\s+<<([^>]*)>>`)
	// `UNCHANGED V` where V is a named set defined as `V == <<a, b>>`.
	unchangedNamedRe = regexp.MustCompile(`(?m)UNCHANGED\s+([A-Z]\w*)\s*$`)
	namedSetOpenRe   = regexp.MustCompile(`(?m)^(\w+)\s*==\s*<<`)
	identRe          = regexp.MustCompile(`[A-Za-z_]\w*`)
	varBlockRe       = regexp.MustCompile(`(?ms)^VARIABLES\b(.*?)(?:^\S|\z)`)
	vacuousRuleRe    = regexp.MustCompile(`(?m)^(\w+)\s*==\s*\n?\s*(\w+)\s*=\s*"([^"]*)"\s*=>`)
)

// reserved are definitions that are not actions: the spec itself and the frames.
var reserved = map[string]bool{"Init": true, "Next": true, "FairSpec": true, "Spec": true}

// stripComments removes TLA+ comments so a variable named in prose is not read as
// code. A comment starts at `\*` outside a string literal.
func stripComments(text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		inString := false
		cut := len(line)
		for j := 0; j < len(line); j++ {
			switch {
			case line[j] == '"':
				inString = !inString
			case !inString && strings.HasPrefix(line[j:], `\*`):
				cut = j
			}
			if cut != len(line) {
				break
			}
		}
		lines[i] = line[:cut]
	}
	return strings.Join(lines, "\n")
}

// declaredVariables returns the names on the VARIABLES line(s), which may wrap.
func declaredVariables(code string) []string {
	m := varBlockRe.FindStringSubmatch(code)
	if m == nil {
		return nil
	}
	return identRe.FindAllString(m[1], -1)
}

// actions splits the spec into one body per top-level definition, keyed by name.
// Reserved definitions and named sets are included; the caller skips them, so a
// body boundary is never mistaken for the previous action's text.
func actions(code string) map[string]string {
	marks := actionRe.FindAllStringSubmatchIndex(code, -1)
	out := make(map[string]string, len(marks))
	for i, m := range marks {
		name := code[m[2]:m[3]]
		start := m[0]
		end := len(code)
		if i+1 < len(marks) {
			end = marks[i+1][0]
		}
		out[name] = code[start:end]
	}
	return out
}

// namedSets resolves `V == <<a, b>>` so `UNCHANGED V` counts the same as the tuple
// it names. The tuple is scanned with balanced brackets rather than a pattern: a
// multi-line tuple whose next definition follows on the following line makes a
// greedy pattern swallow it, and a single-line pattern misses the tuple entirely.
func namedSets(code string) map[string]map[string]bool {
	out := make(map[string]map[string]bool)
	for _, m := range namedSetOpenRe.FindAllStringSubmatchIndex(code, -1) {
		name := code[m[2]:m[3]]
		depth, i := 0, m[1]-2
		for i < len(code)-1 {
			if strings.HasPrefix(code[i:], "<<") {
				depth++
				i += 2
				continue
			}
			if strings.HasPrefix(code[i:], ">>") {
				depth--
				i += 2
				if depth == 0 {
					break
				}
				continue
			}
			i++
		}
		members := make(map[string]bool)
		for _, id := range identRe.FindAllString(code[m[1]-2:i], -1) {
			members[id] = true
		}
		out[name] = members
	}
	return out
}

// branches splits an action into its alternatives so an assignment in one is not
// read as contradicting an assignment in another. A THEN, an ELSE, or a leading \/
// starts a new branch; a duplicate within one branch is still a real defect.
func branches(body string) []string {
	var out []string
	var cur []string
	flush := func() {
		if len(cur) > 0 {
			out = append(out, strings.Join(cur, "\n"))
			cur = nil
		}
	}
	for _, line := range strings.Split(body, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "THEN") || strings.HasPrefix(t, "ELSE") || strings.HasPrefix(t, `\/`) {
			flush()
		}
		cur = append(cur, line)
	}
	flush()
	return out
}

// Lint checks one spec's source text and returns its findings. file is used only
// for messages.
func Lint(file, src string) []Finding {
	code := stripComments(src)
	sets := namedSets(code)
	declared := declaredVariables(code)
	acts := actions(code)
	if len(declared) == 0 || len(acts) == 0 {
		return []Finding{{File: file, Kind: "unparseable", Detail: "could not find VARIABLES or actions"}}
	}

	var findings []Finding
	assigned := make(map[string]bool)

	// Deterministic order: a map iteration would make output differ between runs
	// and between the Go and Python implementations.
	names := make([]string, 0, len(acts))
	for name := range acts {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		body := acts[name]
		if reserved[name] || sets[name] != nil {
			continue
		}

		// An UNCHANGED tuple preserves variables; those are not assignments, so
		// they are collected separately.
		preserved := make(map[string]bool)
		for _, m := range unchangedTupleRe.FindAllStringSubmatch(body, -1) {
			for _, id := range identRe.FindAllString(m[1], -1) {
				preserved[id] = true
			}
		}
		for _, m := range unchangedNamedRe.FindAllStringSubmatch(body, -1) {
			for id := range sets[m[1]] {
				preserved[id] = true
			}
		}

		counts := make(map[string]int)
		for _, m := range assignRe.FindAllStringSubmatch(body, -1) {
			counts[m[1]]++
		}
		vars := make([]string, 0, len(counts))
		for v := range counts {
			vars = append(vars, v)
		}
		sort.Strings(vars)

		hasBranch := strings.Contains(body, "THEN") || strings.Contains(body, "ELSE")
		bodies := branches(body)

		for _, v := range vars {
			assigned[v] = true
			// An UNCHANGED tuple does not excuse an assignment. `x' = v /\
			// UNCHANGED <<x>>` pins x twice, so either the value must be v or the
			// action is unsatisfiable. An IF/ELSE body is exempt: its branches are
			// alternatives, and the per-branch duplicate check below covers them.
			if preserved[v] && !hasBranch {
				findings = append(findings, Finding{
					File: file, Action: name, Kind: "assigned-and-unchanged",
					Detail: fmt.Sprintf("%s is both assigned and listed in UNCHANGED "+
						"(TLA+ conjoins them; the action is dead or the value is forced)", v),
				})
			}
			worst := 0
			for _, b := range bodies {
				n := 0
				for _, m := range assignRe.FindAllStringSubmatch(b, -1) {
					if m[1] == v {
						n++
					}
				}
				if n > worst {
					worst = n
				}
			}
			if worst > 1 {
				findings = append(findings, Finding{
					File: file, Action: name, Kind: "duplicate-assignment",
					Detail: fmt.Sprintf("%s assigned %d times in one branch "+
						"(TLA+ conjoins them; a contradicting assignment cannot fire)", v, worst),
				})
			}
		}
		for v := range preserved {
			assigned[v] = true
		}
	}

	for _, v := range declared {
		if !assigned[v] {
			findings = append(findings, Finding{
				File: file, Kind: "unassigned-variable",
				Detail: fmt.Sprintf("%s is never assigned by any action "+
					"(TLC stops with 'not completely specified', and a rule over it is vacuous)", v),
			})
		}
	}

	// A rule whose premise needs a literal no action assigns is not a proof of
	// anything. This catches the easy case: a premise on a literal value that
	// nothing produces.
	isDeclared := make(map[string]bool, len(declared))
	for _, v := range declared {
		isDeclared[v] = true
	}
	for _, m := range vacuousRuleRe.FindAllStringSubmatch(code, -1) {
		rule, variable, value := m[1], m[2], m[3]
		if !isDeclared[variable] {
			continue
		}
		// Every literal assigned to the variable anywhere in the spec.
		litRe := regexp.MustCompile(`\b` + regexp.QuoteMeta(variable) + `'\s*=\s*("[^"]*"|\w+)`)
		assigns := litRe.FindAllStringSubmatch(code, -1)
		literalOnly := len(assigns) > 0
		for _, a := range assigns {
			if !strings.HasPrefix(a[1], `"`) {
				literalOnly = false
				break
			}
		}
		if !literalOnly {
			continue
		}
		found := false
		for _, a := range assigns {
			if a[1] == `"`+value+`"` {
				found = true
				break
			}
		}
		if !found {
			findings = append(findings, Finding{
				File: file, Rule: rule, Kind: "vacuous-rule",
				Detail: fmt.Sprintf("premise needs %s = %q, which no action assigns", variable, value),
			})
		}
	}
	return findings
}

// LintDir lints every .tla under root, in sorted order.
func LintDir(root string) ([]Finding, int, error) {
	var specs []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(path, ".tla") {
			specs = append(specs, path)
		}
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	sort.Strings(specs)
	var findings []Finding
	for _, spec := range specs {
		src, err := os.ReadFile(spec)
		if err != nil {
			return nil, len(specs), err
		}
		findings = append(findings, Lint(spec, string(src))...)
	}
	return findings, len(specs), nil
}
