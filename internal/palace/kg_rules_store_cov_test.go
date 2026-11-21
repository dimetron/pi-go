package palace

import (
	"context"
	"slices"
	"strings"
	"testing"
)

func TestRulesStoreCov_SplitProgramText(t *testing.T) {
	syn, text := splitProgramText("%% syntax: n3\n@prefix : <u:> .")
	if syn != "n3" || text != "@prefix : <u:> ." {
		t.Fatalf("split = %q %q", syn, text)
	}
	// a stored text without the header is Datalog, returned whole
	syn, text = splitProgramText("p(X, Y) :- q(X, Y).")
	if syn != "datalog" || text != "p(X, Y) :- q(X, Y)." {
		t.Fatalf("legacy split = %q %q", syn, text)
	}
	// a header with no body
	syn, text = splitProgramText("%% syntax:  datalog  ")
	if syn != "datalog" || text != "" {
		t.Fatalf("header only = %q %q", syn, text)
	}
}

func TestRulesStoreCov_StoreProgramValidation(t *testing.T) {
	kg, _ := newEngine(t)
	err := txErr(kg, func(w *KGTx) error { _, err := w.StoreProgram("  ", "datalog", "p(X, Y) :- q(X, Y)."); return err })
	if err == nil || !strings.Contains(err.Error(), "program name is required") {
		t.Fatalf("empty name = %v", err)
	}
	err = txErr(kg, func(w *KGTx) error { _, err := w.StoreProgram("p", "prolog", "x."); return err })
	mustErr(t, err, ErrRuleParse)
	err = txErr(kg, func(w *KGTx) error { _, err := w.StoreProgram("p", "n3", "{ ?x ?p ?y } => { ?x ?p ?y } ."); return err })
	mustErr(t, err, ErrRuleParse)
	// empty syntax defaults to datalog; name and syntax are trimmed
	eid := storeProgram(t, kg, " spaced ", "  ", "p(X, Y) :- q(X, Y).")
	prog, got, err := kg.LoadProgram(context.Background(), "spaced")
	if err != nil || got != eid || prog.Rules() != 1 {
		t.Fatalf("LoadProgram = %v e%d %v", prog, got, err)
	}
	// the same text again reports the live statement as Existing
	rep := tx(t, kg, func(w *KGTx) error { _, err := w.StoreProgram("spaced", "DATALOG", "p(X, Y) :- q(X, Y)."); return err })
	if len(rep.Existing) != 1 || rep.Existing[0] != eid {
		t.Fatalf("existing = %v, want [e%d]", rep.Existing, eid)
	}
}

func TestRulesStoreCov_N3ProgramSupersedeWithdrawsConclusions(t *testing.T) {
	kg, _ := newEngine(t)
	assert1(t, kg, Ent("a"), Ent("manager"), Ent("b"), Valid{})
	v1 := "@prefix : <u:> . { ?x :manager ?y } => { ?x :reports ?y } ."
	v2 := "@prefix : <u:> . { ?x :manager ?y } => { ?x :boss ?y } ."
	old := storeProgram(t, kg, "n3p", "n3", v1)
	rep := materialize1(t, kg, "n3p", kg.Now())
	if len(rep.Derived) != 1 || rep.Program != old || liveCount(t, kg, "reports") != 1 {
		t.Fatalf("first run = %+v", rep)
	}
	neweid := storeProgram(t, kg, "n3p", "n3", v2)
	if neweid == old || live(t, kg, old) {
		t.Fatalf("n3 program not superseded: old live=%v", live(t, kg, old))
	}
	if liveCount(t, kg, "reports") != 0 {
		t.Fatal("conclusions of the superseded program are still live")
	}
	p, _, err := kg.LoadProgram(context.Background(), "n3p")
	if err != nil || p.Rules() != 1 {
		t.Fatalf("reload = %v %v", p, err)
	}
	rep = materialize1(t, kg, "n3p", kg.Now())
	if len(rep.Derived) != 1 || liveCount(t, kg, "boss") != 1 {
		t.Fatalf("second run = %+v", rep)
	}
}

func TestRulesStoreCov_WidenSetsEachBound(t *testing.T) {
	kg, _ := newEngine(t)
	peid := storeProgram(t, kg, "r", "datalog", "r(X, Y) :- a(X, Y).")
	cases := []struct {
		name     string
		valid    Valid
		wantFrom bool
		wantTo   bool
	}{
		{"both bounded", Valid{From: day("2026-01-01"), To: day("2026-06-01")}, true, true},
		{"only from", Valid{From: day("2026-01-01")}, true, false},
		{"only to", Valid{To: day("2026-06-01")}, false, true},
	}
	for i, tc := range cases {
		subj := Ent("s" + string(rune('a'+i)))
		a := assert1(t, kg, subj, Ent("a"), Ent("y"), tc.valid)
		tx(t, kg, func(w *KGTx) error {
			_, _, err := w.Derive(subj, Ent("r"), Ent("y"), Valid{From: day("2026-02-01"), To: day("2026-03-01")}, []int64{a, peid})
			return err
		})
	}
	rep := materialize1(t, kg, "r", kg.Now())
	if len(rep.Widened) != 3 || len(rep.Derived) != 0 {
		t.Fatalf("run = %+v", rep)
	}
	for i, tc := range cases {
		wide, err := kg.Get(context.Background(), rep.Widened[i])
		if err != nil {
			t.Fatal(err)
		}
		if (wide.ValidFrom != nil) != tc.wantFrom || (wide.ValidTo != nil) != tc.wantTo {
			t.Errorf("%s: widened interval = [%v, %v)", tc.name, wide.ValidFrom, wide.ValidTo)
		}
	}
}

func TestRulesStoreCov_MaterializeLiteralConclusions(t *testing.T) {
	kg, _ := newEngine(t)
	assert1(t, kg, Ent("ann"), Ent("age"), Int(41), Valid{})
	assert1(t, kg, Ent("ann"), Ent("nick"), Str("annie"), Valid{})
	storeProgram(t, kg, "lit", "datalog", "years(X, N) :- age(X, N).\nalias(X, N) :- nick(X, N).")
	rep := materialize1(t, kg, "lit", kg.Now())
	if len(rep.Derived) != 2 {
		t.Fatalf("derived = %v", rep.Derived)
	}
	got, err := kg.Now().Match(context.Background(), Pattern{Predicate: new(Ent("years"))})
	if err != nil || len(got) != 1 || got[0].Object != Int(41) {
		t.Fatalf("years = %+v %v", got, err)
	}
}

func TestRulesStoreCov_NoSealedCopyIntoPlainPredicate(t *testing.T) {
	kg, _ := newEngine(t)
	tx(t, kg, func(w *KGTx) error { return w.SetPredicate(Ent("email"), PredicateSchema{Sensitive: true}) })
	assert1(t, kg, Ent("ann"), Ent("email"), Str("ann@secret.example"), Valid{})
	storeProgram(t, kg, "leak", "datalog", "contact(X, E) :- email(X, E).")
	_, err := kg.Materialize(context.Background(), TxOptions{}, "leak", kg.Now(), RuleOptions{})
	mustErr(t, err, ErrNotMaterializable)
	if !strings.Contains(err.Error(), "sensitive value") {
		t.Fatalf("err = %v", err)
	}
	if n := liveCount(t, kg, "contact"); n != 0 {
		t.Fatalf("leak wrote %d statements", n)
	}
	// the same copy into a sensitive predicate is allowed
	tx(t, kg, func(w *KGTx) error { return w.SetPredicate(Ent("contact"), PredicateSchema{Sensitive: true}) })
	rep := materialize1(t, kg, "leak", kg.Now())
	if len(rep.Derived) != 1 {
		t.Fatalf("sensitive target = %+v", rep)
	}
}

func TestRulesStoreCov_MaterializeInGraphViewOnlyAdds(t *testing.T) {
	kg, _ := newEngine(t)
	a := assert1(t, kg, Ent("x"), Ent("a"), Ent("y"), Valid{})
	storeProgram(t, kg, "r", "datalog", "r(X, Y) :- a(X, Y).")
	rep := materialize1(t, kg, "r", kg.Now())
	if len(rep.Derived) != 1 {
		t.Fatalf("run = %+v", rep)
	}
	// An in-graph view sees no premise, so it must not reconcile the
	// conclusion away.
	rep = materialize1(t, kg, "r", kg.Now().InGraph("elsewhere"))
	if len(rep.Reconciled) != 0 || liveCount(t, kg, "r") != 1 {
		t.Fatalf("graph view reconciled: %+v", rep)
	}
	// The plain now view reconciles once the premise is gone.
	tx(t, kg, func(w *KGTx) error { _, err := w.Retract(a); return err })
	materialize1(t, kg, "r", kg.Now())
	if liveCount(t, kg, "r") != 0 {
		t.Fatal("conclusion outlived its premise")
	}
}

func TestRulesStoreCov_LoadProgramMissingForms(t *testing.T) {
	kg, _ := newEngine(t)
	ctx := context.Background()
	// the subject exists as an entity but no program was ever stored
	assert1(t, kg, programSubject("ghost"), Ent("note"), Str("not a program"), Valid{})
	_, _, err := kg.LoadProgram(ctx, "ghost")
	mustErr(t, err, ErrProgramNotFound)
	// a retracted program is no longer live
	eid := storeProgram(t, kg, "gone", "datalog", "p(X, Y) :- q(X, Y).")
	if _, _, err := kg.LoadProgram(ctx, " gone "); err != nil {
		t.Fatalf("trimmed name: %v", err)
	}
	tx(t, kg, func(w *KGTx) error { _, err := w.Retract(eid); return err })
	_, _, err = kg.LoadProgram(ctx, "gone")
	mustErr(t, err, ErrProgramNotFound)
	if !strings.Contains(err.Error(), "gone") {
		t.Fatalf("err = %v", err)
	}
}

func TestRulesStoreCov_ReconcileRetractsSeveralStaleInEidOrder(t *testing.T) {
	kg, _ := newEngine(t)
	assert1(t, kg, Ent("x"), Ent("a"), Ent("y"), Valid{})
	other := assert1(t, kg, Ent("o"), Ent("unrelated"), Ent("p"), Valid{})
	peid := storeProgram(t, kg, "r", "datalog", "r(X, Y) :- a(X, Y).")
	// Two hand-written conclusions that rest on the program but on a premise
	// the rules no longer derive them from: they stay live (their premise is
	// live) until a run reconciles them.
	var stale []int64
	tx(t, kg, func(w *KGTx) error {
		for _, s := range []string{"s2", "s1"} {
			eid, _, err := w.Derive(Ent(s), Ent("r"), Ent("z"), Valid{}, []int64{other, peid})
			if err != nil {
				return err
			}
			stale = append(stale, eid)
		}
		return nil
	})
	rep := materialize1(t, kg, "r", kg.Now())
	if len(rep.Derived) != 1 || !slices.Equal(rep.Reconciled, stale) {
		t.Fatalf("run = %+v, want derived 1 and reconciled %v in eid order", rep, stale)
	}
	for _, e := range stale {
		if live(t, kg, e) {
			t.Fatalf("stale conclusion e%d still live", e)
		}
	}
	if n := liveCount(t, kg, "r"); n != 1 {
		t.Fatalf("live conclusions = %d, want 1", n)
	}
}

func TestRulesStoreCov_MaterializeSurfacesSchemaViolation(t *testing.T) {
	kg, _ := newEngine(t)
	assert1(t, kg, Ent("x"), Ent("a"), Ent("y"), Valid{})
	storeProgram(t, kg, "r", "datalog", "r(X, Y) :- a(X, Y).")
	tx(t, kg, func(w *KGTx) error { return w.SetPredicate(Ent("r"), PredicateSchema{ValueType: "string"}) })
	_, err := kg.Materialize(context.Background(), TxOptions{}, "r", kg.Now(), RuleOptions{})
	mustErr(t, err, ErrSchemaViolation)
	if !strings.Contains(err.Error(), "materialize r(x, y)") {
		t.Fatalf("err = %v, want the failing tuple named", err)
	}
	if n := liveCount(t, kg, "r"); n != 0 {
		t.Fatalf("failed run wrote %d statements", n)
	}
}
