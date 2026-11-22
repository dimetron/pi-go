package palace

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestElpiCov_ParseErrorPositions(t *testing.T) {
	cases := []struct {
		name, src, want string
	}{
		{"var head", "X :- true.", "a clause head must be a predicate"},
		{"applied var", "X a.", "only a name can be applied"},
		{"unterminated string", "p \"abc.", "unterminated string"},
		{"unclosed paren", "p (f a.", "expected ')'"},
		{"bad number", "p -.", "bad number"},
		{"unexpected char", "p :- ?.", "unexpected"},
		{"eof in body", "p :- ", "unexpected end of program"},
		{"eof in head after comment", "% only a comment\np :- q,", "unexpected end of program"},
		{"builtin not", "not X.", "builtin"},
		{"builtin true", "true.", "builtin"},
		{"lambda", "p :- \\x.", "λ-abstraction"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseElpi(tc.src)
			if !errors.Is(err, ErrElpiProgram) {
				t.Fatalf("err = %v, want ErrElpiProgram", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %q, want %q", err, tc.want)
			}
		})
	}
	// the position is line:col of the failure on the right line
	_, err := ParseElpi("a.\nb.\nc :- \"x.")
	if err == nil || !strings.Contains(err.Error(), "3:") {
		t.Fatalf("position = %v, want line 3", err)
	}
}

func TestElpiCov_ParseTermForms(t *testing.T) {
	p, err := ParseElpi(`
% comment line
n 1.
n 2.5.
n -3.
d "text".
e a-b.
e a.b.
fn (f x 1) _.
`)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.clauses["n/1"]) != 3 || len(p.clauses["fn/2"]) != 1 {
		t.Fatalf("clauses = %v", p.clauses)
	}
	h := p.clauses["n/1"][1].head.args[0]
	if h.val != Num(2.5) {
		t.Fatalf("decimal = %v", h.val)
	}
	if v := p.clauses["n/1"][2].head.args[0].val; v != Int(-3) {
		t.Fatalf("negative = %v", v)
	}
	if v := p.clauses["e/1"][0].head.args[0].val; v != (Value{Kind: KindEntity, Text: "a-b"}) {
		t.Fatalf("dashed = %v", v)
	}
	if v := p.clauses["e/1"][1].head.args[0].val; v != (Value{Kind: KindEntity, Text: "a.b"}) {
		t.Fatalf("dotted = %v", v)
	}
	// a constant head is keyed name/0
	p0, err := ParseElpi("ready.")
	if err != nil || len(p0.clauses["ready/0"]) != 1 {
		t.Fatalf("constant head: %v %v", p0, err)
	}
}

func TestElpiCov_GoalParsingErrors(t *testing.T) {
	kg, _ := newEngine(t)
	p, _ := ParseElpi("ok.")
	v := kg.Now()
	for _, tc := range []struct{ goal, want string }{
		{"ok, ", "unexpected end"},
		{"ok extra . junk", "unexpected text after the goal"},
		{"X = ", "unexpected end"},
		{"ok \"unterminated", "unterminated string"},
	} {
		_, err := v.Elpi(context.Background(), p, tc.goal, ElpiOptions{})
		if !errors.Is(err, ErrElpiProgram) || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("goal %q: err = %v, want %q", tc.goal, err, tc.want)
		}
	}
}

func TestElpiCov_BuiltinsAndTerms(t *testing.T) {
	kg, _ := newEngine(t)
	assert1(t, kg, Ent("a"), Ent("score"), Num(2.5), Valid{})
	assert1(t, kg, Ent("b"), Ent("score"), Int(3), Valid{})
	assert1(t, kg, Ent("c"), Ent("label"), Str("hi"), Valid{})
	v := kg.Now()
	prog := `
pair (f X Y) X Y.
big X :- tm.triple X score S, S > 2.6.
small X :- tm.triple X score S, S < 2.6.
lbl X :- tm.triple X label L, L > 1.
`
	count := func(goal string) []map[string]Value { return elpiRows(t, v, prog, goal) }

	if rows := count("true, fail"); len(rows) != 0 {
		t.Fatalf("fail = %v", rows)
	}
	if rows := count("true"); len(rows) != 1 || len(rows[0]) != 0 {
		t.Fatalf("true = %v", rows)
	}
	if rows := count("big X"); len(rows) != 1 || rows[0]["X"] != Ent("b") {
		t.Fatalf("big = %v", rows)
	}
	if rows := count("small X"); len(rows) != 1 || rows[0]["X"] != Ent("a") {
		t.Fatalf("small = %v", rows)
	}
	// a string is not comparable: the goal fails instead of erroring
	if rows := count("lbl X"); len(rows) != 0 {
		t.Fatalf("string > int = %v", rows)
	}
	// \= between differing constants and equal constants
	if rows := count("a \\= b"); len(rows) != 1 {
		t.Fatalf("a \\= b = %v", rows)
	}
	if rows := count("a \\= a"); len(rows) != 0 {
		t.Fatalf("a \\= a = %v", rows)
	}
	// unify compound terms; a non-ground binding prints as text
	rows := count("pair T 1 2")
	if len(rows) != 1 || rows[0]["T"] != Str("(f 1 2)") {
		t.Fatalf("pair = %v", rows)
	}
	rows = count("pair T 1 Y")
	if len(rows) != 1 || rows[0]["T"] != Str("(f 1 _)") {
		t.Fatalf("partial pair = %v", rows)
	}
	// functor mismatch and arity mismatch never unify
	if rows := count("(f 1 2) = (g 1 2)"); len(rows) != 0 {
		t.Fatalf("functor mismatch = %v", rows)
	}
	if rows := count("(f 1 2) = (f 1)"); len(rows) != 0 {
		t.Fatalf("arity mismatch = %v", rows)
	}
	// app vs const never unifies
	if rows := count("(f 1) = 1"); len(rows) != 0 {
		t.Fatalf("app vs const = %v", rows)
	}
	// occurs check
	if rows := count("X = (f X)"); len(rows) != 0 {
		t.Fatalf("occurs (left) = %v", rows)
	}
	if rows := count("(f X) = X"); len(rows) != 0 {
		t.Fatalf("occurs (right) = %v", rows)
	}
	// var-var aliasing succeeds
	if rows := count("X = Y, Y = 4"); len(rows) != 1 || rows[0]["X"] != Int(4) || rows[0]["Y"] != Int(4) {
		t.Fatalf("alias = %v", rows)
	}
	if rows := count("X = X"); len(rows) != 1 {
		t.Fatalf("X = X = %v", rows)
	}
	// anonymous variables in a goal are independent and never reported
	rows = count("tm.triple _ score _")
	if len(rows) != 2 || len(rows[0]) != 0 {
		t.Fatalf("anon = %v", rows)
	}
	// a string literal term and iri forms
	if rows := count(`tm.triple (iri "c") label "hi"`); len(rows) != 1 {
		t.Fatalf("string literal match = %v", rows)
	}
	if rows := count(`tm.triple S label "nope"`); len(rows) != 0 {
		t.Fatalf("string literal miss = %v", rows)
	}
}

func TestElpiCov_RunErrors(t *testing.T) {
	kg, _ := newEngine(t)
	assert1(t, kg, Ent("a"), Ent("p"), Ent("b"), Valid{})
	assert1(t, kg, Ent("a"), Ent("p"), Ent("c"), Valid{})
	v := kg.Now()
	ctx := context.Background()
	p, _ := ParseElpi("pass X :- tm.triple X p _.")

	// comparison of an unbound variable
	_, err := v.Elpi(ctx, p, "X < 3", ElpiOptions{})
	if !errors.Is(err, ErrElpiRun) || !strings.Contains(err.Error(), "needs bound numbers") {
		t.Fatalf("unbound < = %v", err)
	}
	_, err = v.Elpi(ctx, p, "3 > Y", ElpiOptions{})
	if !errors.Is(err, ErrElpiRun) {
		t.Fatalf("unbound > = %v", err)
	}
	// tm.triple argument that is a compound term
	_, err = v.Elpi(ctx, p, "tm.triple (f a) p X", ElpiOptions{})
	if !errors.Is(err, ErrElpiRun) || !strings.Contains(err.Error(), "argument 1 is not a value") {
		t.Fatalf("triple app = %v", err)
	}
	// error inside a negation propagates
	_, err = v.Elpi(ctx, p, "not (undefined_thing 1)", ElpiOptions{})
	if !errors.Is(err, ErrElpiRun) || !strings.Contains(err.Error(), "unknown predicate undefined_thing/1") {
		t.Fatalf("not err = %v", err)
	}
	// MaxRows
	_, err = v.Elpi(ctx, p, "pass X", ElpiOptions{MaxRows: 1})
	if !errors.Is(err, ErrElpiOutOfFuel) || !strings.Contains(err.Error(), "more than 1 solutions") {
		t.Fatalf("max rows = %v", err)
	}
	res, err := v.Elpi(ctx, p, "pass X", ElpiOptions{MaxRows: 2})
	if err != nil || len(res.Rows) != 2 || res.FuelUsed == 0 {
		t.Fatalf("max rows ok = %+v %v", res, err)
	}
	// budget on rows read
	_, err = v.Elpi(ctx, p, "pass X", ElpiOptions{Budget: Budget{MaxRows: 1}})
	if !errors.Is(err, ErrBudget) {
		t.Fatalf("budget = %v", err)
	}
	// fuel
	_, err = v.Elpi(ctx, p, "pass X, pass Y, pass Z", ElpiOptions{Fuel: 3})
	if !errors.Is(err, ErrElpiOutOfFuel) || !strings.Contains(err.Error(), "after 3 steps") {
		t.Fatalf("fuel = %v", err)
	}
}

func TestElpiCov_CancelMidRun(t *testing.T) {
	kg, _ := newEngine(t)
	// A long-running, fuel-hungry loop: the periodic meter check sees the
	// canceled context (every 256 steps) before the fuel limit.
	loop, _ := ParseElpi("loop X :- loop X.")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := kg.Now().Elpi(ctx, loop, "true, loop a", ElpiOptions{Fuel: 1_000_000})
	if err == nil {
		t.Fatal("canceled run succeeded")
	}
	if errors.Is(err, ErrElpiOutOfFuel) {
		t.Fatalf("err = %v, want a context error before fuel", err)
	}
}

func TestElpiCov_ElpiVars(t *testing.T) {
	rows := []map[string]Value{{"B": Int(1), "A": Int(2)}, {"C": Int(3), "A": Int(4)}}
	got := ElpiVars(rows)
	if strings.Join(got, ",") != "A,B,C" {
		t.Fatalf("ElpiVars = %v", got)
	}
	if got := ElpiVars(nil); len(got) != 0 {
		t.Fatalf("ElpiVars(nil) = %v", got)
	}
}

func TestElpiCov_NumericAndKeys(t *testing.T) {
	if f, ok := numeric(Int(3)); !ok || f != 3 {
		t.Fatalf("int = %v %v", f, ok)
	}
	if f, ok := numeric(Num(1.5)); !ok || f != 1.5 {
		t.Fatalf("num = %v %v", f, ok)
	}
	if _, ok := numeric(Str("x")); ok {
		t.Fatal("string numeric")
	}
	if _, ok := numeric(Ent("x")); ok {
		t.Fatal("entity numeric")
	}
	if k := (&eTerm{kind: eVar, name: "X"}).key(); k != "" {
		t.Fatalf("var key = %q", k)
	}
}
