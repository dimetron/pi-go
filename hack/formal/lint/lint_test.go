package formal

import (
	"strings"
	"testing"
)

// Each test below is a defect that actually produced a green run while these
// models were being written. The spec text is the shape that fooled the checker,
// reduced to the fewest lines that still reproduce it. A check that has never been
// seen to fire is not a check.

func findingsByKind(fs []Finding) map[string][]Finding {
	out := make(map[string][]Finding)
	for _, f := range fs {
		out[f.Kind] = append(out[f.Kind], f)
	}
	return out
}

func hasKind(fs []Finding, kind string) bool {
	for _, f := range fs {
		if f.Kind == kind {
			return true
		}
	}
	return false
}

// The /run model's first version tested gateOutcome without any action assigning
// it, so both gate rules quantified over an empty set and a mutation that made a
// hung gate retry passed cleanly.
func TestLint_UnassignedVariable(t *testing.T) {
	src := `
VARIABLES phase, gateOutcome

Init == phase = "none" /\ gateOutcome = ""

Run ==
    /\ phase = "running"
    /\ phase' = "gating"

Next == Run
`
	fs := Lint("spec.tla", src)
	if !hasKind(fs, "unassigned-variable") {
		t.Fatalf("want unassigned-variable for gateOutcome, got %v", fs)
	}
	var found bool
	for _, f := range fs {
		if f.Kind == "unassigned-variable" && strings.Contains(f.Detail, "gateOutcome") {
			found = true
		}
	}
	if !found {
		t.Errorf("the finding does not name gateOutcome: %v", fs)
	}
}

// The /goal model's Resume assigned canceled' twice under a top-level assignment:
// a conjunction, so the branch could never fire. It cost a false pass on the very
// property that model exists to check.
func TestLint_DuplicateAssignmentInOneBranch(t *testing.T) {
	src := `
VARIABLES phase, canceled

Init == phase = "none" /\ canceled = TRUE

Resume ==
    /\ phase = "stopped"
    /\ canceled' = FALSE
    /\ canceled' = TRUE
    /\ phase' = "running"

Next == Resume
`
	fs := Lint("spec.tla", src)
	if !hasKind(fs, "duplicate-assignment") {
		t.Fatalf("want duplicate-assignment for canceled, got %v", fs)
	}
}

// The same variable assigned in two IF branches is two alternatives, not a
// contradiction. Reporting it would make the lint cry wolf on every model, which
// is how a checker gets ignored.
func TestLint_DuplicateAcrossBranchesIsAllowed(t *testing.T) {
	src := `
VARIABLES phase, status

Init == phase = "idle" /\ status = "none"

Step ==
    /\ phase' = IF status = "none"
                 THEN "running"
                 ELSE "idle"
    /\ status' = IF phase = "idle"
                  THEN "started"
                  ELSE "none"

Next == Step
`
	if fs := Lint("spec.tla", src); len(fs) != 0 {
		t.Fatalf("want no findings for per-branch assignments, got %v", fs)
	}
}

// A disjunction of cases is the other way TLA+ writes alternatives. ReadVerdict
// uses three, and an early version reported all of them as duplicates.
func TestLint_DuplicateAcrossDisjunctsIsAllowed(t *testing.T) {
	src := `
VARIABLES phase, verdictStated, verdictPass

Init == phase = "idle" /\ verdictStated = FALSE /\ verdictPass = FALSE

Read(outcome) ==
    /\ phase = "verifying"
    /\ phase' = phase
    /\ \/ outcome = "pass" /\ verdictStated' = TRUE /\ verdictPass' = TRUE
       \/ outcome = "fail" /\ verdictStated' = TRUE /\ verdictPass' = FALSE
       \/ outcome = "none" /\ verdictStated' = FALSE /\ verdictPass' = FALSE

Next == \E o \in {"pass", "fail", "none"} : Read(o)
`
	if fs := Lint("spec.tla", src); len(fs) != 0 {
		t.Fatalf("want no findings for per-disjunct assignments, got %v", fs)
	}
}

// A bulk edit put quitVia in every action's UNCHANGED tuple while also assigning
// it. Every action that quit became unsatisfiable, the model explored two states,
// and the run reported success.
func TestLint_AssignedAndUnchanged(t *testing.T) {
	src := `
VARIABLES phase, quitVia

Init == phase = "idle" /\ quitVia = ""

Quit ==
    /\ phase = "idle"
    /\ phase' = "quitting"
    /\ quitVia' = "command"
    /\ UNCHANGED <<quitVia>>

Next == Quit
`
	fs := Lint("spec.tla", src)
	if !hasKind(fs, "assigned-and-unchanged") {
		t.Fatalf("want assigned-and-unchanged for quitVia, got %v", fs)
	}
}

// The exemption for IF/ELSE must not swallow a real straight-line contradiction.
func TestLint_AssignedAndUnchangedStillFiresInStraightLineAction(t *testing.T) {
	src := `
VARIABLES a, b

Init == a = 0 /\ b = 0

Both ==
    /\ a' = 1
    /\ b' = b
    /\ UNCHANGED <<a, b>>

Next == Both
`
	fs := Lint("spec.tla", src)
	if !hasKind(fs, "assigned-and-unchanged") {
		t.Fatalf("want assigned-and-unchanged, got %v", fs)
	}
}

// A rule whose premise needs a literal no action produces can never fire.
func TestLint_VacuousRule(t *testing.T) {
	src := `
VARIABLES phase, gateOutcome

Init == phase = "none" /\ gateOutcome = ""

Run ==
    /\ phase = "running"
    /\ phase' = "gating"
    /\ gateOutcome' = "pass"

HangNeverRetries ==
    gateOutcome = "hang" => phase = "failed"

Next == Run
`
	fs := Lint("spec.tla", src)
	if !hasKind(fs, "vacuous-rule") {
		t.Fatalf("want vacuous-rule for gateOutcome = hang, got %v", fs)
	}
}

// A premise the actions do produce is not vacuous.
func TestLint_VacuousRule_AllowsReachablePremise(t *testing.T) {
	src := `
VARIABLES phase, gateOutcome

Init == phase = "none" /\ gateOutcome = ""

Run ==
    /\ phase = "gating"
    /\ phase' = "failed"
    /\ gateOutcome' = "hang"

HangStops ==
    gateOutcome = "hang" => phase = "failed"

Next == Run
`
	if fs := Lint("spec.tla", src); hasKind(fs, "vacuous-rule") {
		t.Fatalf("gateOutcome = hang is assigned, so the rule is not vacuous: %v", fs)
	}
}

// A comment naming a variable must not be read as code: the models document their
// variables in prose right beside the declarations.
func TestLint_CommentsAreNotCode(t *testing.T) {
	src := `
VARIABLES phase

\* gateOutcome is mentioned here and nowhere assigned, but this is a comment.
Init == phase = "none"

Run ==
    /\ phase' = "running"

Next == Run
`
	fs := Lint("spec.tla", src)
	for _, f := range fs {
		if strings.Contains(f.Detail, "gateOutcome") {
			t.Fatalf("a variable named only in a comment was reported: %v", f)
		}
	}
}

// A named set used in UNCHANGED reserves exactly its members, so a variable that
// happens to be in `V` is preserved rather than read as an unassigned action.
func TestLint_NamedSetInUnchanged(t *testing.T) {
	src := `
VARIABLES phase, a

V == <<phase, a>>

Init == phase = "idle" /\ a = 0

Idle ==
    /\ phase = "idle"
    /\ UNCHANGED V

Next == Idle
`
	fs := Lint("spec.tla", src)
	if hasKind(fs, "unassigned-variable") || hasKind(fs, "assigned-and-unchanged") {
		t.Fatalf("named-set UNCHANGED should preserve phase and a, got %v", fs)
	}
}

// A definition whose body is a set literal is not an action; linting it would
// report every member as assigned by nothing.
func TestLint_NamedSetIsNotAnAction(t *testing.T) {
	src := `
VARIABLES x

V == <<x>>

Init == x = 0

Run ==
    /\ x' = 1

Next == Run
`
	if fs := Lint("spec.tla", src); hasKind(fs, "duplicate-assignment") {
		t.Fatalf("the named set V was linted as an action: %v", fs)
	}
}

// Parameterised actions must end the previous action's body. Missing this folded
// one action's assignments into the one before it and invented duplicates.
func TestLint_ParameterisedActionIsABoundary(t *testing.T) {
	src := `
VARIABLES x, y

Init == x = 0 /\ y = 0

Step ==
    /\ x' = 1

Read(o) ==
    /\ y' = IF o = "a" THEN 1 ELSE 2

Next == Step
`
	fs := Lint("spec.tla", src)
	for _, f := range fs {
		if f.Action == "Step" && f.Kind == "duplicate-assignment" {
			t.Fatalf("Read(o)'s body was folded into Step: %v", f)
		}
	}
}

func TestLint_Unparseable(t *testing.T) {
	fs := Lint("spec.tla", "not a spec at all")
	if !hasKind(fs, "unparseable") {
		t.Fatalf("want unparseable, got %v", fs)
	}
}

// The real models under hack/formal must stay clean. This is the guard that keeps
// the lint itself from drifting away from the specs it checks.
func TestLint_RepoModelsAreClean(t *testing.T) {
	findings, specs, err := LintDir("../..")
	if err != nil {
		t.Fatalf("LintDir: %v", err)
	}
	if specs < 5 {
		t.Fatalf("found %d specs, want at least 5; the walk is not reaching the models", specs)
	}
	for _, f := range findings {
		t.Errorf("model is not lint-clean: %v", f)
	}
}

// kinds sanity: the helper the other tests lean on must see what it claims.
func TestFindingsByKind(t *testing.T) {
	fs := []Finding{{Kind: "a"}, {Kind: "b"}, {Kind: "a"}}
	got := findingsByKind(fs)
	if len(got["a"]) != 2 || len(got["b"]) != 1 {
		t.Fatalf("findingsByKind = %v", got)
	}
}
