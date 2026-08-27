package validate

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/dimetron/pi-go/internal/sop/specdoc"
)

// Target is what a rule is asked about: one artifact, plus the spec it belongs
// to so cross-artifact rules can reach its siblings.
type Target struct {
	Artifact string // "plan.md"
	Content  string
	Spec     *specdoc.Spec
	RepoRoot string // for rules that resolve paths or probe commands
}

// Rule checks one property of a Target.
type Rule func(Target, Args) Findings

var registry = map[string]Rule{}

// Register adds a rule under name. It panics on a duplicate: a silently
// shadowed rule would validate nothing while appearing to pass.
func Register(name string, r Rule) {
	if _, dup := registry[name]; dup {
		panic("validate: duplicate rule " + name)
	}
	registry[name] = r
}

// Lookup returns the rule registered under name.
func Lookup(name string) (Rule, bool) {
	r, ok := registry[name]
	return r, ok
}

// RuleNames lists every registered rule.
func RuleNames() []string {
	out := make([]string, 0, len(registry))
	for n := range registry {
		out = append(out, n)
	}
	return out
}

// Apply runs one rule spec against a target.
func Apply(spec string, t Target) Findings {
	name, args, err := ParseRule(spec)
	if err != nil {
		return Findings{{Artifact: t.Artifact, Rule: spec, Severity: SeverityError, Message: err.Error()}}
	}
	rule, ok := Lookup(name)
	if !ok {
		return Findings{{
			Artifact: t.Artifact, Rule: name, Severity: SeverityError,
			Message: fmt.Sprintf("unknown validator %q", name),
			Fix:     "use one of: " + strings.Join(RuleNames(), ", "),
		}}
	}
	out := rule(t, args)
	for i := range out {
		if out[i].Artifact == "" {
			out[i].Artifact = t.Artifact
		}
		if out[i].Rule == "" {
			out[i].Rule = name
		}
		if out[i].Severity == "" {
			out[i].Severity = SeverityError
		}
	}
	return out
}

func finding(msg, fix string) Findings {
	return Findings{{Severity: SeverityError, Message: msg, Fix: fix}}
}

func warn(msg, fix string) Findings {
	return Findings{{Severity: SeverityWarn, Message: msg, Fix: fix}}
}

func init() {
	Register("non_empty", ruleNonEmpty)
	Register("max_lines", ruleMaxLines)
	Register("has_headings", ruleHasHeadings)
	Register("min_qa", ruleMinQA)
	Register("research_at_least", ruleResearchAtLeast)
	Register("lists_slices", ruleListsSlices)
	Register("slices_are_checkboxes", ruleSlicesAreCheckboxes)
	Register("slice_count", ruleSliceCount)
	Register("every_slice_has", ruleEverySliceHas)
	Register("slice_budget", ruleSliceBudget)
	Register("gates_present", ruleGatesPresent)
	Register("gates_are_executable", ruleGatesAreExecutable)
	Register("done_criteria", ruleDoneCriteria)
	Register("no_placeholders", ruleNoPlaceholders)
	Register("references_exist", ruleReferencesExist)
	Register("plan_slices_match_prompt_slices", rulePlanMatchesPrompt)
	Register("outline_slices_match_plan_slices", ruleOutlineMatchesPlan)
	Register("acceptance_criteria_are_given_when_then", ruleGivenWhenThen)
	Register("no_solution_language", ruleNoSolutionLanguage)
}

// --- shape ---

func ruleNonEmpty(t Target, _ Args) Findings {
	if strings.TrimSpace(t.Content) == "" {
		return finding(t.Artifact+" is missing or empty",
			"write "+t.Artifact+" before this phase can complete")
	}
	return nil
}

func ruleMaxLines(t Target, a Args) Findings {
	maxLines := a.Int("max", 2000)
	if n := specdoc.CountLines(t.Content); n > maxLines {
		return finding(
			fmt.Sprintf("%s is %d lines, over the %d-line limit", t.Artifact, n, maxLines),
			"the read tool returns at most 2000 lines per call, so a worker sent to read this "+
				"sees only part of it — split the feature into sequential specs rather than shrinking the prose")
	}
	return nil
}

func ruleHasHeadings(t Target, a Args) Findings {
	var out Findings
	for _, want := range a.Items() {
		if !specdoc.HasHeading(t.Content, want) {
			out = append(out, Finding{
				Severity: SeverityError,
				Message:  fmt.Sprintf("required section %q is missing", want),
				Fix:      fmt.Sprintf("add a `## %s` section", want),
			})
		}
	}
	return out
}

var qaMarkerRe = regexp.MustCompile(`(?im)^\s*(?:[-*>]\s*)?(?:#{1,6}\s*)?(?:\*\*)?q(?:uestion)?\s*\d*\s*[:.]|^\s*.*\?\s*$`)

func ruleMinQA(t Target, a Args) Findings {
	minQA := a.Int("min", 3)
	if n := len(qaMarkerRe.FindAllString(t.Content, -1)); n < minQA {
		// A warning, not a block: requirements.md written as a scope document
		// rather than a Q&A transcript still captures the requirements. The
		// finding is worth surfacing because unasked questions are where
		// rework comes from, but it is not a correctness property.
		return warn(
			fmt.Sprintf("only %d clarifying questions recorded, want at least %d", n, minQA),
			"ask the user about scope, constraints, edge cases and acceptance criteria, "+
				"appending each answer to requirements.md")
	}
	return nil
}

func ruleResearchAtLeast(t Target, a Args) Findings {
	minRes := a.Int("min", 2)
	if t.Spec == nil {
		return nil
	}
	if n := len(t.Spec.Research); n < minRes {
		return Findings{{
			Artifact: "research/", Severity: SeverityError,
			Message: fmt.Sprintf("%d research files, want at least %d", n, minRes),
			Fix: "split the question into independent angles and dispatch them in one parallel " +
				"`subagent` call; each agent writes its findings to research/<topic>.md",
		}}
	}
	return nil
}

// --- slices ---

var listItemRe = regexp.MustCompile(`(?m)^\s*(?:\d+\.|[-*])\s+\S`)

func ruleListsSlices(t Target, a Args) Findings {
	minItems := a.Int("min", 2)
	n := len(listItemRe.FindAllString(t.Content, -1))
	if h := specdoc.SliceHeadings(t.Content); h > n {
		n = h
	}
	if n < minItems {
		return finding(
			fmt.Sprintf("%s lists %d items, want at least %d slices", t.Artifact, n, minItems),
			"the outline is the cheap review checkpoint: list the slices and their order "+
				"before expanding into the full plan")
	}
	return nil
}

func ruleSlicesAreCheckboxes(t Target, _ Args) Findings {
	slices := specdoc.ParsePlanSlices(t.Content)
	if len(slices) == 0 {
		return finding("plan.md declares no slices",
			"write each slice as `- [ ] Step N: <title>` so progress is machine-readable")
	}
	if !strings.Contains(t.Content, "- [ ]") && !strings.Contains(t.Content, "- [x]") &&
		!strings.Contains(t.Content, "- [X]") {
		return finding("plan.md slices are headings, not checkboxes",
			"add a `## Progress` checklist of `- [ ] Step N: <title>` lines; /run ticks these to track completion")
	}
	return nil
}

func ruleSliceCount(t Target, a Args) Findings {
	maxSlices := a.Int("max", 25)
	minSlices := a.Int("min", 1)
	n := len(specdoc.ParsePlanSlices(t.Content))
	if n == 0 {
		n = len(specdoc.ParsePromptSlices(t.Content))
	}
	switch {
	case n < minSlices:
		return finding(fmt.Sprintf("%d slices, want at least %d", n, minSlices),
			"break the work into verifiable vertical slices")
	case n > maxSlices:
		return finding(
			fmt.Sprintf("%d slices, over the %d-slice limit", n, maxSlices),
			"a run drives one slice per worker within a bounded retry budget; split the feature "+
				"into sequential specs and note the running order in each one's Constraints")
	}
	return nil
}

func ruleEverySliceHas(t Target, a Args) Findings {
	want := a.Items()
	if len(want) == 0 {
		want = []string{"files", "verify", "parallel_safe"}
	}
	slices := specdoc.ParsePromptSlices(t.Content)
	if len(slices) == 0 {
		return finding("no implementation slices found",
			"list slices under `## Implementation Slices` as `1. **<name>** — <what>, files: `<paths>`, verify: `<cmd>`, parallel-safe: <yes|no>`")
	}
	var out Findings
	for _, s := range slices {
		var missing []string
		for _, w := range want {
			switch strings.ToLower(strings.ReplaceAll(w, "-", "_")) {
			case "files":
				if len(s.Files) == 0 {
					missing = append(missing, "files")
				}
			case "verify":
				if s.Verify == "" {
					missing = append(missing, "verify")
				}
			case "parallel_safe":
				if !s.HasParallel {
					missing = append(missing, "parallel-safe")
				}
			}
		}
		if len(missing) > 0 {
			out = append(out, Finding{
				Severity: SeverityError, Line: s.Line,
				Message: fmt.Sprintf("slice %d (%s) does not state: %s", s.Index, s.Title, strings.Join(missing, ", ")),
				Fix: "a worker cannot see the plan; each slice must name its files, its verify command, " +
					"and whether it is parallel-safe",
			})
		}
	}
	return out
}

func ruleSliceBudget(t Target, a Args) Findings {
	maxFiles := a.Int("max_files", 10)
	var out Findings
	for _, s := range specdoc.ParsePromptSlices(t.Content) {
		if len(s.Files) > maxFiles {
			out = append(out, Finding{
				Severity: SeverityError, Line: s.Line,
				Message: fmt.Sprintf("slice %d (%s) names %d files, over the %d-file budget",
					s.Index, s.Title, len(s.Files), maxFiles),
				Fix: "size each slice to one worker's context — an oversized slice grows the worker " +
					"past the model limit and the provider drops the stream mid-slice",
			})
		}
	}
	return out
}

// --- gates ---

func ruleGatesPresent(t Target, a Args) Findings {
	minGates := a.Int("min", 1)
	if n := len(specdoc.ParseGates(t.Content)); n < minGates {
		return finding(
			fmt.Sprintf("%d gates declared, want at least %d", n, minGates),
			"add a `## Gates` section with the project's real build and test commands")
	}
	return nil
}

// placeholderRe catches template text left unfilled: <build command>, TBD, ...
var placeholderRe = regexp.MustCompile(`(?i)<[^>]*(command|paths?|name|title|description|what|outcome|cmd)[^>]*>|\bTBD\b|\bFIXME\b|\bXXX\b|^\s*\.\.\.\s*$`)

func ruleGatesAreExecutable(t Target, _ Args) Findings {
	gates := specdoc.ParseGates(t.Content)
	if len(gates) == 0 {
		return finding("no gates to check",
			"add a `## Gates` section; /run has nothing to verify the tree with otherwise")
	}
	var out Findings
	for _, g := range gates {
		if placeholderRe.MatchString(g.Command) {
			out = append(out, Finding{
				Severity: SeverityError, Line: g.Line,
				Message: fmt.Sprintf("gate %q is still a template placeholder: %s", g.Name, g.Command),
				Fix:     "discover the project's real build and test commands during research and put them here",
			})
			continue
		}
		if f := probeCommand(g, t.RepoRoot); f != nil {
			out = append(out, *f)
		}
	}
	return out
}

// probeCommand checks that a gate's leading program exists. It resolves the
// binary rather than running the gate: running it here would be a build, and
// the point is to catch an unrunnable gate at plan time cheaply.
func probeCommand(g specdoc.Gate, repoRoot string) *Finding {
	fields := strings.Fields(g.Command)
	if len(fields) == 0 {
		return &Finding{Severity: SeverityError, Line: g.Line,
			Message: fmt.Sprintf("gate %q has an empty command", g.Name)}
	}
	prog := fields[0]
	// Shell builtins and assignments are not resolvable binaries; accept them.
	switch prog {
	case "cd", "set", "export", "if", "for", "while", "true", "false", "[", "test":
		return nil
	}
	if strings.ContainsAny(prog, "=$(){}") {
		return nil
	}
	if strings.Contains(prog, "/") {
		if _, err := os.Stat(filepath.Join(repoRoot, prog)); err == nil {
			return nil
		}
	}
	if _, err := exec.LookPath(prog); err != nil {
		return &Finding{
			Severity: SeverityError, Line: g.Line,
			Message: fmt.Sprintf("gate %q runs %q, which is not on PATH", g.Name, prog),
			Fix:     "use a command that exists in this project — check the Makefile targets",
		}
	}
	if prog == "make" && len(fields) > 1 && repoRoot != "" {
		if f := probeMakeTarget(g, fields[1], repoRoot); f != nil {
			return f
		}
	}
	return nil
}

func probeMakeTarget(g specdoc.Gate, target, repoRoot string) *Finding {
	if strings.HasPrefix(target, "-") {
		return nil
	}
	body, err := os.ReadFile(filepath.Join(repoRoot, "Makefile"))
	if err != nil {
		return nil // no Makefile to check against; not this rule's business
	}
	targetRe := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(target) + `\s*:`)
	if targetRe.Match(body) {
		return nil
	}
	return &Finding{
		Severity: SeverityWarn, Line: g.Line,
		Message: fmt.Sprintf("gate %q runs `make %s`, but the Makefile has no such target", g.Name, target),
		Fix:     "check `make help` for the real target name",
	}
}

// --- done criteria and placeholders ---

func ruleDoneCriteria(t Target, a Args) Findings {
	minCrit := a.Int("min", 3)
	noPlaceholders := a.Bool("no_placeholders", true)

	crit := specdoc.DoneCriteria(t.Content)
	if len(crit) < minCrit {
		return finding(
			fmt.Sprintf("%d Done Criteria, want at least %d", len(crit), minCrit),
			"without Done Criteria the Verifier falls back to counting checkboxes, "+
				"which cannot tell an implemented slice from a stubbed one")
	}
	if !noPlaceholders {
		return nil
	}
	var out Findings
	for _, c := range crit {
		if placeholderRe.MatchString(c) {
			out = append(out, Finding{
				Severity: SeverityError,
				Message:  "Done Criterion is still a template placeholder: " + c,
				Fix:      "state an outcome checkable by reading code or running a command",
			})
		}
	}
	return out
}

func ruleNoPlaceholders(t Target, _ Args) Findings {
	var out Findings
	for i, line := range strings.Split(t.Content, "\n") {
		if placeholderRe.MatchString(line) {
			out = append(out, Finding{
				Severity: SeverityError, Line: i + 1,
				Message: "unfilled template placeholder: " + strings.TrimSpace(line),
				Fix:     "replace it with the real value discovered during research",
			})
		}
	}
	return out
}

// --- cross-artifact ---

func ruleReferencesExist(t Target, _ Args) Findings {
	if t.RepoRoot == "" {
		return nil
	}
	var out Findings
	for _, ref := range specdoc.References(t.Content) {
		if !isRepoPath(ref) {
			continue
		}
		if _, err := os.Stat(filepath.Join(t.RepoRoot, filepath.FromSlash(ref))); err != nil {
			out = append(out, Finding{
				Severity: SeverityError,
				Message:  "Reference points at a path that does not exist: " + ref,
				Fix:      "write the referenced artifact, or correct the path",
			})
		}
	}
	return out
}

// isRepoPath reports whether a Reference entry names a path inside this
// checkout. It filters out commands, globs, and external module paths such as
// `google.golang.org/adk/v2@v2.2.0/agent/context.go`, which are legitimate
// references that will never resolve against the repo root.
func isRepoPath(ref string) bool {
	if !strings.Contains(ref, "/") || strings.ContainsAny(ref, " $*@") {
		return false
	}
	if strings.HasPrefix(ref, "~") || strings.HasPrefix(ref, "/") {
		return false // outside the checkout: a home path or an absolute one
	}
	head, _, _ := strings.Cut(ref, "/")
	return !strings.Contains(head, ".") // a domain-like first segment means a module path
}

func rulePlanMatchesPrompt(t Target, _ Args) Findings {
	if t.Spec == nil {
		return nil
	}
	plan := specdoc.ParsePlanSlices(t.Spec.Files[specdoc.Plan])
	prompt := specdoc.ParsePromptSlices(t.Spec.Files[specdoc.Prompt])
	return compareSlices(plan, prompt, "plan.md", "PROMPT.md",
		"PROMPT.md is what /run executes; a mismatch means the run drives a different set of slices than the plan tracks")
}

func ruleOutlineMatchesPlan(t Target, _ Args) Findings {
	if t.Spec == nil {
		return nil
	}
	outlineMD := t.Spec.Files[specdoc.Outline]
	outline := len(listItemRe.FindAllString(outlineMD, -1))
	if h := specdoc.SliceHeadings(outlineMD); h > 0 {
		outline = h
	}
	plan := len(specdoc.ParsePlanSlices(t.Spec.Files[specdoc.Plan]))
	if outline == 0 || plan == 0 {
		return nil // covered by the presence rules
	}
	if plan < outline {
		return warn(
			fmt.Sprintf("outline lists %d items but plan.md has %d slices — the plan dropped work the outline approved", outline, plan),
			"expand every outlined slice into the plan, or revise the outline and re-approve it")
	}
	return nil
}

// compareSlices reports a count mismatch as an error and unmatched titles as
// warnings. Titles are compared by token overlap: the two documents legitimately
// phrase the same slice differently, so exact equality would be noise.
func compareSlices(a, b []specdoc.Slice, aName, bName, why string) Findings {
	if len(a) == 0 || len(b) == 0 {
		return nil // absence is another rule's finding
	}
	var out Findings
	if len(a) != len(b) {
		out = append(out, Finding{
			Severity: SeverityError,
			Message:  fmt.Sprintf("%s has %d slices but %s has %d", aName, len(a), bName, len(b)),
			Fix:      why,
		})
	}
	for _, s := range a {
		if !anyMatch(s.Title, b) {
			out = append(out, Finding{
				Severity: SeverityWarn, Line: s.Line,
				Message: fmt.Sprintf("slice %q in %s has no counterpart in %s", s.Title, aName, bName),
				Fix:     why,
			})
		}
	}
	return out
}

func anyMatch(title string, in []specdoc.Slice) bool {
	for _, s := range in {
		if overlap(title, s.Title) >= 0.5 {
			return true
		}
	}
	return false
}

var wordRe = regexp.MustCompile(`[a-z0-9]+`)

// overlap is the fraction of a's significant words that also appear in b.
func overlap(a, b string) float64 {
	aw := wordRe.FindAllString(strings.ToLower(a), -1)
	bw := map[string]bool{}
	for _, w := range wordRe.FindAllString(strings.ToLower(b), -1) {
		bw[w] = true
	}
	var total, hit int
	for _, w := range aw {
		if len(w) < 3 || w == "the" || w == "and" || w == "for" || w == "slice" || w == "step" {
			continue
		}
		total++
		if bw[w] {
			hit++
		}
	}
	if total == 0 {
		return 1
	}
	return float64(hit) / float64(total)
}

// --- prose quality ---

// gwtRe accepts an arrow in place of the literal "then": specs write
// `Given no goal, when "/goal x" -> chat shows ...` and mean the same thing.
var gwtRe = regexp.MustCompile(`(?i)\bgiven\b.*\bwhen\b.*(?:\bthen\b|→|->)`)

func ruleGivenWhenThen(t Target, a Args) Findings {
	body := specdoc.Section(t.Content, "Acceptance Criteria")
	if strings.TrimSpace(body) == "" {
		return finding("no Acceptance Criteria section",
			"add `## Acceptance Criteria` with Given/When/Then statements")
	}
	// Criteria wrap across lines in real specs, so match against the section
	// with newlines folded into spaces rather than line by line.
	folded := strings.Join(strings.Fields(body), " ")
	minCrit := a.Int("min", 1)
	if n := len(gwtRe.FindAllString(folded, -1)); n < minCrit {
		return finding(
			fmt.Sprintf("%d Given/When/Then criteria, want at least %d", n, minCrit),
			"phrase each criterion as \"Given <precondition>, when <action>, then <expected outcome>\"")
	}
	return nil
}

var solutionRe = regexp.MustCompile(`(?im)^\s*(?:[-*]\s*)?(?:we should|we could|i (?:suggest|propose|recommend)|the fix is|my recommendation|proposed (?:solution|design|approach))\b`)

func ruleNoSolutionLanguage(t Target, _ Args) Findings {
	var out Findings
	for i, line := range strings.Split(t.Content, "\n") {
		if solutionRe.MatchString(line) {
			out = append(out, Finding{
				Severity: SeverityWarn, Line: i + 1,
				Message: "research states a proposal rather than a fact: " + strings.TrimSpace(line),
				Fix: "research must compress how the code works today; proposals belong in design.md, " +
					"where they can be reviewed against the facts",
			})
		}
	}
	return out
}

// --- manifest ---

// manifestShape is the subset of the SOP manifest this package reads. The
// manifest is written by package sop, which imports this one, so the rule
// decodes the file directly rather than importing back.
type manifestShape struct {
	SOPVersion int    `json:"sopVersion"`
	Contract   string `json:"contract"`
	Valid      bool   `json:"valid"`
}

// ManifestName duplicates sop.ManifestName for the same reason.
const ManifestName = ".sop-manifest.json"

func init() { Register("sop_manifest_valid", ruleManifestValid) }

// ruleManifestValid checks the spec carries a validation record this build can
// rely on. A missing manifest is a warning, not a block: specs written by hand
// or planned before manifests existed are still runnable, and the preflight
// rules re-derive what matters. A manifest that records a failed validation, or
// one from a newer SOP version, does block.
func ruleManifestValid(t Target, a Args) Findings {
	if t.Spec == nil || t.Spec.Dir == "" {
		return nil
	}
	maxVersion := a.Int("max_version", 0)

	b, err := os.ReadFile(filepath.Join(t.Spec.Dir, ManifestName))
	if err != nil {
		return warn("spec has no "+ManifestName+" — it has not been validated by /plan",
			"run the preflight rules directly, or re-plan the spec to produce a manifest")
	}
	var m manifestShape
	if err := json.Unmarshal(b, &m); err != nil {
		return finding(ManifestName+" is unreadable: "+err.Error(),
			"delete it; the spec is revalidated from its artifacts")
	}
	if maxVersion > 0 && m.SOPVersion > maxVersion {
		return finding(
			fmt.Sprintf("%s records SOP version %d, newer than this build's %d", ManifestName, m.SOPVersion, maxVersion),
			"a spec planned under a newer SOP may rely on rules this build does not implement")
	}
	if !m.Valid {
		return finding(
			fmt.Sprintf("%s records a failed validation (contract %q)", ManifestName, m.Contract),
			"the spec did not satisfy its contract when it was planned; fix the findings or re-plan it")
	}
	return nil
}
