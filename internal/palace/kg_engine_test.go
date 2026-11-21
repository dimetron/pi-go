package palace

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// newEngine returns a knowledge graph on a fresh in-memory palace database
// with a clock that advances one second per call.
func newEngine(t *testing.T) (*KnowledgeGraph, *sql.DB) {
	t.Helper()
	db, err := OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	kg := NewKnowledgeGraph(NewSQLitePalaceStore(db))
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	kg.now = func() time.Time { clock = clock.Add(time.Second); return clock }
	return kg, db
}

func day(s string) *time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return &t
}

// tx runs fn in one committed transaction and fails the test on error.
func tx(t *testing.T, kg *KnowledgeGraph, fn func(*KGTx) error) *TxReport {
	t.Helper()
	rep, err := kg.Transact(context.Background(), TxOptions{Author: "test"}, fn)
	if err != nil {
		t.Fatalf("Transact: %v", err)
	}
	return rep
}

func assert1(t *testing.T, kg *KnowledgeGraph, s, p, o Value, v Valid) int64 {
	t.Helper()
	var eid int64
	tx(t, kg, func(w *KGTx) error {
		var err error
		eid, _, err = w.Assert(s, p, o, v)
		return err
	})
	return eid
}

func live(t *testing.T, kg *KnowledgeGraph, eid int64) bool {
	t.Helper()
	st, err := kg.Get(context.Background(), eid)
	if err != nil || st == nil {
		t.Fatalf("Get(e%d) = %v, %v", eid, st, err)
	}
	return st.Live()
}

func TestKGValue_RoundTrip(t *testing.T) {
	kg, db := newEngine(t)
	ctx := context.Background()
	values := []Value{
		Ent("Alice Smith"), StmtRef(42), Str("hello"), Int(-7), Int(1 << 40), Num(0.25),
		Bool(true), Bool(false), TimeVal(*day("2026-03-01")), txVal(9),
	}
	tx(t, kg, func(w *KGTx) error {
		d := newDecoder()
		var ids []objectID
		for _, v := range values {
			o, _, err := encodeValue(ctx, w.q, v, true)
			if err != nil {
				return err
			}
			again, _, err := encodeValue(ctx, w.q, v, true)
			if err != nil || again != o {
				t.Errorf("interning %v twice gave %v and %v (%v)", v, o, again, err)
			}
			ids = append(ids, o)
		}
		if err := d.load(ctx, w.q, ids); err != nil {
			return err
		}
		for i, o := range ids {
			if got := d.value(o); got != values[i] {
				t.Errorf("round trip %v = %v", values[i], got)
			}
		}
		return nil
	})
	if Ent("Alice Smith").Text != "alice_smith" {
		t.Errorf("Ent did not normalize")
	}
	var n int
	if err := db.QueryRow("SELECT count(*) FROM kg_term").Scan(&n); err != nil || n != 3 {
		t.Errorf("terms = %d (%v), want 3 (entity, string, number)", n, err)
	}
	if _, _, err := encodeValue(ctx, db, Int(1<<60), false); err == nil {
		t.Error("out-of-range integer accepted")
	}
}

func TestKGTriggers_NeverForget(t *testing.T) {
	kg, db := newEngine(t)
	e1 := assert1(t, kg, Ent("alice"), Ent("works_at"), Ent("acme"), Valid{})
	e2 := assert1(t, kg, Ent("bob"), Ent("works_at"), Ent("acme"), Valid{})
	tx(t, kg, func(w *KGTx) error { _, err := w.Retract(e2); return err })

	mustFail := func(name, query string, args ...any) {
		t.Helper()
		if _, err := db.Exec(query, args...); err == nil {
			t.Errorf("%s: succeeded, want trigger abort", name)
		}
	}
	mustFail("delete statement", "DELETE FROM kg_stmt WHERE eid = ?", e1)
	mustFail("change object", "UPDATE kg_stmt SET o = s WHERE eid = ?", e1)
	mustFail("second retraction", "UPDATE kg_stmt SET t_ret = t_ret, ret_kind = 1 WHERE eid = ?", e2)
	mustFail("backdated retraction", "UPDATE kg_stmt SET t_ret = 1, ret_kind = 0 WHERE eid = ?", e1)
	// The newest transaction is committed too: only an open one may date rows.
	mustFail("retraction in a committed tx", "UPDATE kg_stmt SET t_ret = (SELECT max(t) FROM kg_tx), ret_kind = 0 WHERE eid = ?", e1)
	mustFail("assertion in a committed tx", "INSERT INTO kg_stmt (s, p, o, t_add) SELECT 1, 1, 1, max(t) FROM kg_tx")
	mustFail("backdated assertion", "INSERT INTO kg_stmt (s, p, o, t_add) VALUES (1, 1, 1, 1)")
	mustFail("replace statement", "INSERT OR REPLACE INTO kg_stmt (eid, s, p, o, t_add) VALUES (?, 1, 1, 1, 3)", e1)
	mustFail("replace term by key", "INSERT OR REPLACE INTO kg_term (tag, lex) VALUES (1, 'alice')")
	mustFail("replace term by id", "INSERT OR REPLACE INTO kg_term (id, tag, lex) VALUES (1, 3, 'x')")
	mustFail("delete term", "DELETE FROM kg_term")
	mustFail("update term", "UPDATE kg_term SET lex = 'mallory'")
	mustFail("replace tx by instant", "INSERT OR REPLACE INTO kg_tx (t, instant) SELECT 99, instant FROM kg_tx WHERE t = 1")
	mustFail("tx out of order", "INSERT INTO kg_tx (t, instant) VALUES (10, 9999999999999)")
	mustFail("delete tx", "DELETE FROM kg_tx")
	mustFail("rewind watermark", "UPDATE kg_meta SET value = 1")
	mustFail("delete meta", "DELETE FROM kg_meta")

	if !live(t, kg, e1) || live(t, kg, e2) {
		t.Error("statement states changed")
	}
}

func TestKGTransact_NumberingDryRunRollback(t *testing.T) {
	kg, db := newEngine(t)
	ctx := context.Background()
	kg.now = func() time.Time { return time.UnixMilli(1000) } // frozen clock
	var instants []time.Time
	for i := range 3 {
		rep := tx(t, kg, func(w *KGTx) error { return nil })
		if rep.T != int64(i+1) {
			t.Errorf("tx %d numbered %d", i+1, rep.T)
		}
		instants = append(instants, rep.Instant)
	}
	if !instants[0].Before(instants[1]) || !instants[1].Before(instants[2]) {
		t.Errorf("instants not strictly increasing under a frozen clock: %v", instants)
	}

	rep, err := kg.Transact(ctx, TxOptions{DryRun: true}, func(w *KGTx) error {
		_, _, err := w.Assert(Ent("a"), Ent("p"), Ent("b"), Valid{})
		return err
	})
	if err != nil || !rep.DryRun || len(rep.Asserted) != 1 {
		t.Fatalf("dry run = %+v, %v", rep, err)
	}
	boom := errors.New("boom")
	if _, err := kg.Transact(ctx, TxOptions{}, func(w *KGTx) error {
		if _, _, err := w.Assert(Ent("a"), Ent("p"), Ent("c"), Valid{}); err != nil {
			return err
		}
		return boom
	}); !errors.Is(err, boom) {
		t.Fatalf("error not returned: %v", err)
	}
	var stmts, last int
	_ = db.QueryRow("SELECT count(*) FROM kg_stmt").Scan(&stmts)
	_ = db.QueryRow("SELECT value FROM kg_meta").Scan(&last)
	if stmts != 0 || last != 3 {
		t.Errorf("after dry run and failure: statements=%d last_t=%d, want 0 and 3", stmts, last)
	}
	if next := tx(t, kg, func(w *KGTx) error { return nil }); next.T != 4 {
		t.Errorf("next tx = %d, want 4 (no gap)", next.T)
	}
}

func TestKGAssert_IdempotencyEpisodesAndChecks(t *testing.T) {
	kg, _ := newEngine(t)
	ctx := context.Background()
	e1 := assert1(t, kg, Ent("alice"), Ent("works_at"), Ent("acme"), Valid{From: day("2020-01-01"), To: day("2022-01-01")})
	var same, episode int64
	var sameNew, episodeNew bool
	tx(t, kg, func(w *KGTx) error {
		var err error
		if same, sameNew, err = w.Assert(Ent("Alice"), Ent("works_at"), Ent("ACME"),
			Valid{From: day("2021-01-01"), To: day("2021-06-01")}); err != nil {
			return err
		}
		episode, episodeNew, err = w.Assert(Ent("alice"), Ent("works_at"), Ent("acme"), Valid{From: day("2024-01-01")})
		return err
	})
	if same != e1 || sameNew {
		t.Errorf("overlapping assert = e%d new=%v, want existing e%d", same, sameNew, e1)
	}
	if episode == e1 || !episodeNew {
		t.Errorf("disjoint episode = e%d new=%v, want a new statement", episode, episodeNew)
	}
	parallel := int64(0)
	tx(t, kg, func(w *KGTx) error {
		var err error
		parallel, err = w.Create(Ent("alice"), Ent("works_at"), Ent("acme"), Valid{From: day("2024-01-01")})
		return err
	})
	if parallel == episode {
		t.Error("Create reused a statement")
	}

	for name, fn := range map[string]func(*KGTx) error{
		"reserved predicate": func(w *KGTx) error {
			_, _, err := w.Assert(Ent("a"), sysPred(predSupersedes), Ent("b"), Valid{})
			return err
		},
		"missing statement": func(w *KGTx) error { _, _, err := w.Annotate(9999, Ent("p"), Str("x")); return err },
		"empty interval": func(w *KGTx) error {
			_, _, err := w.Assert(Ent("a"), Ent("p"), Ent("b"), Valid{From: day("2026-01-01"), To: day("2026-01-01")})
			return err
		},
		"reversed interval": func(w *KGTx) error {
			_, _, err := w.Assert(Ent("a"), Ent("p"), Ent("b"), Valid{From: day("2026-02-01"), To: day("2026-01-01")})
			return err
		},
		"literal subject": func(w *KGTx) error { _, _, err := w.Assert(Str("a"), Ent("p"), Ent("b"), Valid{}); return err },
	} {
		if _, err := kg.Transact(ctx, TxOptions{}, fn); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// A retracted statement cannot be annotated.
	tx(t, kg, func(w *KGTx) error { _, err := w.Retract(parallel); return err })
	if _, err := kg.Transact(ctx, TxOptions{}, func(w *KGTx) error {
		_, _, err := w.Annotate(parallel, Ent("confidence"), Num(0.5))
		return err
	}); !errors.Is(err, ErrNotLive) {
		t.Errorf("annotating a retracted statement: %v, want ErrNotLive", err)
	}
}

func TestKGConfirm(t *testing.T) {
	kg, _ := newEngine(t)
	ctx := context.Background()
	e := assert1(t, kg, Ent("ci"), Ent("runs_on"), Ent("github_actions"), Valid{})
	tx(t, kg, func(w *KGTx) error {
		a, err := w.Confirm(e)
		if err != nil {
			return err
		}
		b, err := w.Confirm(e)
		if a != b {
			t.Errorf("confirming twice in one tx made two links: e%d, e%d", a, b)
		}
		return err
	})
	if _, err := kg.Transact(ctx, TxOptions{Author: "second", Source: "doc"}, func(w *KGTx) error {
		_, err := w.Confirm(e)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	ex, err := kg.Now().Explain(ctx, e)
	if err != nil {
		t.Fatal(err)
	}
	if ex.Evidence.Confirmations != 2 || len(ex.Confirmed) != 2 || ex.Confirmed[1].Author != "second" {
		t.Errorf("confirmations = %d %+v, want 2 with the second by 'second'", ex.Evidence.Confirmations, ex.Confirmed)
	}
	if len(ex.Layers) != 0 {
		t.Errorf("confirmations leaked into layers: %+v", ex.Layers)
	}
}

func TestKGRetract_CascadeDependentsAndLimit(t *testing.T) {
	kg, _ := newEngine(t)
	ctx := context.Background()
	fact := assert1(t, kg, Ent("alice"), Ent("works_at"), Ent("acme"), Valid{})
	other := assert1(t, kg, Ent("alice"), Ent("lives_in"), Ent("lisbon"), Valid{})
	var conf, meta int64
	tx(t, kg, func(w *KGTx) error {
		var err error
		if conf, _, err = w.Annotate(fact, Ent("confidence"), Num(0.8)); err != nil {
			return err
		}
		meta, _, err = w.Annotate(conf, Ent("method"), Str("llm"))
		return err
	})

	deps, err := kg.Now().Dependents(ctx, fact)
	if err != nil || !slices.Equal(deps, []int64{fact, conf, meta}) {
		t.Fatalf("Dependents = %v, %v; want [fact conf meta]", deps, err)
	}
	dry, err := kg.Transact(ctx, TxOptions{DryRun: true}, func(w *KGTx) error { _, err := w.Retract(fact); return err })
	if err != nil {
		t.Fatal(err)
	}
	var dryIDs []int64
	for _, r := range dry.Retracted {
		dryIDs = append(dryIDs, r.EID)
	}
	if !slices.Equal(dryIDs, deps) {
		t.Errorf("dry-run retraction %v != dependents %v", dryIDs, deps)
	}

	if _, err := kg.Transact(ctx, TxOptions{MaxCascade: 2}, func(w *KGTx) error {
		_, err := w.Retract(fact)
		return err
	}); !errors.Is(err, ErrCascadeLimit) {
		t.Fatalf("cascade limit: %v", err)
	}
	if !live(t, kg, fact) {
		t.Fatal("limit failure did not roll back")
	}

	rep := tx(t, kg, func(w *KGTx) error {
		ok, err := w.Retract(fact)
		if !ok {
			t.Error("first Retract returned false")
		}
		return err
	})
	kinds := map[int64]RetractKind{}
	for _, r := range rep.Retracted {
		kinds[r.EID] = r.Kind
	}
	if kinds[fact] != RetExplicit || kinds[conf] != RetCascade || kinds[meta] != RetCascade {
		t.Errorf("retraction kinds = %v", kinds)
	}
	if !live(t, kg, other) {
		t.Error("cascade crossed an entity to an unrelated fact")
	}
	tx(t, kg, func(w *KGTx) error {
		ok, err := w.Retract(fact)
		if ok {
			t.Error("second Retract returned true")
		}
		return err
	})
	// What it rested on stays visible as of before the retraction.
	before := kg.AsOf(rep.T - 1)
	if layers, _ := before.Layers(ctx, fact); len(layers) != 1 || layers[0].EID != conf {
		t.Errorf("AsOf before retraction layers = %+v", layers)
	}
	if deps, _ := before.Dependents(ctx, fact); len(deps) != 3 {
		t.Errorf("AsOf dependents after retraction = %v", deps)
	}
	if deps, _ := kg.Now().Dependents(ctx, fact); deps != nil {
		t.Errorf("Now dependents of a retracted fact = %v, want none", deps)
	}
}

func TestKGSupersede_ReplaysLayersAndLineage(t *testing.T) {
	kg, _ := newEngine(t)
	ctx := context.Background()
	fact := assert1(t, kg, Ent("alice"), Ent("works_at"), Ent("acme"), Valid{From: day("2025-01-01")})
	var conf, method, belief, outside int64
	tx(t, kg, func(w *KGTx) error {
		var err error
		if conf, _, err = w.Annotate(fact, Ent("confidence"), Num(0.8)); err != nil {
			return err
		}
		if method, _, err = w.Annotate(conf, Ent("method"), Str("llm")); err != nil {
			return err
		}
		if belief, _, err = w.Assert(Ent("belief9"), Ent("supported_by"), StmtRef(fact), Valid{}); err != nil {
			return err
		}
		outside, _, err = w.Assert(Ent("bob"), Ent("works_at"), Ent("acme"), Valid{})
		return err
	})

	if _, err := kg.Transact(ctx, TxOptions{}, func(w *KGTx) error {
		_, err := w.Supersede(fact, Patch{Object: new(StmtRef(conf))})
		return err
	}); !errors.Is(err, ErrPatchIntoCascade) {
		t.Errorf("patch into cascade: %v", err)
	}

	var newFact int64
	rep := tx(t, kg, func(w *KGTx) error {
		var err error
		newFact, err = w.Supersede(fact, Patch{Object: new(Ent("globex")), ValidFrom: day("2025-02-01")})
		return err
	})
	if len(rep.Superseded) != 1 || rep.Superseded[0] != (Supersession{Old: fact, New: newFact}) {
		t.Errorf("report superseded = %+v", rep.Superseded)
	}
	for _, e := range []int64{fact, conf, method, belief} {
		if live(t, kg, e) {
			t.Errorf("e%d still live after supersede", e)
		}
	}
	if !live(t, kg, outside) {
		t.Error("unrelated statement retracted")
	}

	now := kg.Now()
	st, _ := kg.Get(ctx, newFact)
	if st.Object != Ent("globex") || st.ValidFrom == nil || !st.ValidFrom.Equal(*day("2025-02-01")) || st.Subject != Ent("alice") {
		t.Errorf("corrected statement = %+v", st)
	}
	layers, _ := now.Layers(ctx, newFact)
	if len(layers) != 1 || layers[0].Object != Num(0.8) {
		t.Fatalf("replayed layers = %+v", layers)
	}
	sub, _ := now.Layers(ctx, layers[0].EID)
	if len(sub) != 1 || sub[0].Object != Str("llm") {
		t.Errorf("layer on layer not replayed onto the new layer: %+v", sub)
	}
	refs, _ := now.Match(ctx, Pattern{Predicate: new(Ent("supported_by"))})
	if len(refs) != 1 || refs[0].Object != StmtRef(newFact) {
		t.Errorf("reference not rewritten through sigma: %+v", refs)
	}
	ex, _ := now.Explain(ctx, newFact)
	if !slices.Equal(ex.Supersedes, []int64{fact}) {
		t.Errorf("lineage = %v", ex.Supersedes)
	}
	exOld, _ := now.Explain(ctx, fact)
	if exOld.Visible || !slices.Equal(exOld.SupersededBy, []int64{newFact}) {
		t.Errorf("old explain visible=%v superseded_by=%v", exOld.Visible, exOld.SupersededBy)
	}
	old, _ := kg.AsOf(rep.T-1).About(ctx, "alice", "subject")
	if len(old) != 1 || old[0].Object != Ent("acme") {
		t.Errorf("AsOf before correction = %+v", old)
	}

	// A supersede that would hand a unique value to a second subject fails.
	tx(t, kg, func(w *KGTx) error { return w.SetPredicate(Ent("email"), PredicateSchema{Unique: true}) })
	a := assert1(t, kg, Ent("alice"), Ent("email"), Str("a@x"), Valid{})
	assert1(t, kg, Ent("bob"), Ent("email"), Str("b@x"), Valid{})
	if _, err := kg.Transact(ctx, TxOptions{}, func(w *KGTx) error {
		_, err := w.Supersede(a, Patch{Object: new(Str("b@x"))})
		return err
	}); !errors.Is(err, ErrUniqueViolation) {
		t.Errorf("supersede into a unique collision: %v", err)
	}
	if !live(t, kg, a) {
		t.Error("failed supersede did not roll back")
	}
}

func TestKGSupersede_CardinalityOnWidening(t *testing.T) {
	kg, _ := newEngine(t)
	tx(t, kg, func(w *KGTx) error { return w.SetPredicate(Ent("role"), PredicateSchema{One: true}) })
	dev := assert1(t, kg, Ent("alice"), Ent("role"), Ent("dev"), Valid{From: day("2020-01-01"), To: day("2022-01-01")})
	lead := assert1(t, kg, Ent("alice"), Ent("role"), Ent("lead"), Valid{From: day("2023-01-01")})
	if !live(t, kg, dev) || !live(t, kg, lead) {
		t.Fatal("disjoint episodes of a cardinality-one predicate must coexist")
	}
	tx(t, kg, func(w *KGTx) error {
		_, err := w.Supersede(dev, Patch{ClearValidTo: true})
		return err
	})
	if live(t, kg, lead) {
		t.Error("widening dev over lead did not replace lead")
	}
}

func TestKGDerive(t *testing.T) {
	kg, _ := newEngine(t)
	ctx := context.Background()
	a := assert1(t, kg, Ent("ci"), Ent("uses"), Ent("go"), Valid{})
	b := assert1(t, kg, Ent("repo"), Ent("pins"), Ent("go_1.27"), Valid{})
	independent := assert1(t, kg, Ent("repo"), Ent("needs"), Ent("go_1.27"), Valid{})

	if _, err := kg.Transact(ctx, TxOptions{}, func(w *KGTx) error {
		_, _, err := w.Derive(Ent("repo"), Ent("needs"), Ent("go_1.27"), Valid{}, nil)
		return err
	}); !errors.Is(err, ErrNoSupport) {
		t.Errorf("empty support: %v", err)
	}

	var d1, d2, d3 int64
	var new2 bool
	tx(t, kg, func(w *KGTx) error {
		var err error
		if d1, _, err = w.Derive(Ent("repo"), Ent("needs"), Ent("go_1.27"), Valid{}, []int64{a, b}); err != nil {
			return err
		}
		if d2, new2, err = w.Derive(Ent("repo"), Ent("needs"), Ent("go_1.27"), Valid{}, []int64{b, a, b}); err != nil {
			return err
		}
		d3, _, err = w.Derive(Ent("repo"), Ent("needs"), Ent("go_1.27"), Valid{}, []int64{b})
		return err
	})
	if d1 == independent {
		t.Error("derive reused an independently asserted fact")
	}
	if d2 != d1 || new2 {
		t.Errorf("same support set gave e%d (new=%v), want e%d", d2, new2, d1)
	}
	if d3 == d1 {
		t.Error("a different support set reused the derivation")
	}
	ex, _ := kg.Now().Explain(ctx, d1)
	if len(ex.Supports) != 2 {
		t.Errorf("supports = %+v", ex.Supports)
	}
	if deps, _ := kg.Now().Dependents(ctx, a); !slices.Contains(deps, d1) || slices.Contains(deps, d3) {
		t.Errorf("dependents of a = %v, want d1 and not d3", deps)
	}

	tx(t, kg, func(w *KGTx) error { _, err := w.Retract(a); return err })
	if live(t, kg, d1) {
		t.Error("conclusion survived losing a support")
	}
	if !live(t, kg, d3) || !live(t, kg, independent) {
		t.Error("retraction reached a derivation or fact that did not rest on it")
	}
	if _, err := kg.Transact(ctx, TxOptions{}, func(w *KGTx) error {
		_, _, err := w.Derive(Ent("x"), Ent("y"), Ent("z"), Valid{}, []int64{a})
		return err
	}); !errors.Is(err, ErrNotLive) {
		t.Errorf("dead support: %v", err)
	}

	// Superseding a support drops the conclusion; superseding the conclusion
	// keeps its support links.
	var d4, d4b int64
	tx(t, kg, func(w *KGTx) error {
		var err error
		d4, _, err = w.Derive(Ent("repo"), Ent("builds_with"), Ent("make"), Valid{}, []int64{b})
		return err
	})
	tx(t, kg, func(w *KGTx) error {
		var err error
		d4b, err = w.Supersede(d4, Patch{ValidFrom: day("2026-01-01")})
		return err
	})
	if ex, _ := kg.Now().Explain(ctx, d4b); len(ex.Supports) != 1 || ex.Supports[0].EID != b {
		t.Errorf("superseded conclusion supports = %+v", ex.Supports)
	}
	var bNew int64
	tx(t, kg, func(w *KGTx) error {
		var err error
		bNew, err = w.Supersede(b, Patch{Object: new(Ent("go_1.28"))})
		return err
	})
	if live(t, kg, d3) || live(t, kg, d4b) {
		t.Error("conclusions survived a correction of their support")
	}
	if layers, _ := kg.Now().Match(ctx, Pattern{Subject: new(StmtRef(bNew)), IncludeEngine: true}); len(layers) != 0 {
		// Only the supersedes link has bNew as subject.
		for _, l := range layers {
			if l.Predicate != sysPred(predSupersedes) {
				t.Errorf("unexpected statement on corrected support: %+v", l)
			}
		}
	}
}

func TestKGSchema(t *testing.T) {
	kg, _ := newEngine(t)
	ctx := context.Background()
	tx(t, kg, func(w *KGTx) error { return w.SetPredicate(Ent("age"), PredicateSchema{One: true}) })
	thirty := assert1(t, kg, Ent("alice"), Ent("age"), Int(30), Valid{})
	tx(t, kg, func(w *KGTx) error { _, _, err := w.Annotate(thirty, Ent("source"), Str("form")); return err })
	rep := tx(t, kg, func(w *KGTx) error { _, _, err := w.Assert(Ent("alice"), Ent("age"), Int(31), Valid{}); return err })
	if live(t, kg, thirty) {
		t.Fatal("cardinality one did not replace the old value")
	}
	if rep.Retracted[0].Kind != RetCardinality {
		t.Errorf("replacement kind = %v", rep.Retracted[0].Kind)
	}
	cur, _ := kg.Now().About(ctx, "alice", "subject")
	if len(cur) != 1 || cur[0].Object != Int(31) {
		t.Errorf("current age = %+v", cur)
	}
	if l, _ := kg.Now().Layers(ctx, cur[0].EID); len(l) != 0 {
		t.Errorf("old value's layers carried to the new value: %+v", l)
	}

	// The endpoint is re-checked after replacement.
	tx(t, kg, func(w *KGTx) error { return w.SetPredicate(Ent("current_claim"), PredicateSchema{One: true}) })
	claim := assert1(t, kg, Ent("agent"), Ent("current_claim"), Ent("x"), Valid{})
	if _, err := kg.Transact(ctx, TxOptions{}, func(w *KGTx) error {
		_, _, err := w.Assert(Ent("agent"), Ent("current_claim"), StmtRef(claim), Valid{})
		return err
	}); !errors.Is(err, ErrNotLive) {
		t.Errorf("object retracted by its own replacement: %v, want ErrNotLive", err)
	}

	// A schema change that live data breaks is refused.
	assert1(t, kg, Ent("alice"), Ent("knows"), Ent("bob"), Valid{})
	assert1(t, kg, Ent("alice"), Ent("knows"), Ent("carol"), Valid{})
	if _, err := kg.Transact(ctx, TxOptions{}, func(w *KGTx) error {
		return w.SetPredicate(Ent("knows"), PredicateSchema{One: true})
	}); !errors.Is(err, ErrSchemaViolation) {
		t.Errorf("violating schema change: %v", err)
	}
	// The schema is read per write: declared and used in one transaction.
	tx(t, kg, func(w *KGTx) error {
		if _, _, err := w.Assert(Ent("bob"), Ent("team"), Ent("red"), Valid{}); err != nil {
			return err
		}
		if err := w.SetPredicate(Ent("team"), PredicateSchema{One: true}); err != nil {
			return err
		}
		_, _, err := w.Assert(Ent("bob"), Ent("team"), Ent("blue"), Valid{})
		return err
	})
	if teams, _ := kg.Now().About(ctx, "bob", "subject"); len(teams) != 1 || teams[0].Object != Ent("blue") {
		t.Errorf("schema declared in the same tx was not applied: %+v", teams)
	}
	// Unique.
	tx(t, kg, func(w *KGTx) error { return w.SetPredicate(Ent("email"), PredicateSchema{Unique: true}) })
	assert1(t, kg, Ent("alice"), Ent("email"), Str("a@x"), Valid{})
	if _, err := kg.Transact(ctx, TxOptions{}, func(w *KGTx) error {
		_, _, err := w.Assert(Ent("eve"), Ent("email"), Str("a@x"), Valid{})
		return err
	}); !errors.Is(err, ErrUniqueViolation) {
		t.Errorf("unique: %v", err)
	}
	// Dropping a flag lifts it; the schema history stays.
	tx(t, kg, func(w *KGTx) error { return w.SetPredicate(Ent("email"), PredicateSchema{}) })
	assert1(t, kg, Ent("eve"), Ent("email"), Str("a@x"), Valid{})
	hist, _ := kg.History().Match(ctx, Pattern{Subject: new(Ent("email")), IncludeEngine: true})
	if len(hist) != 1 || hist[0].Live() {
		t.Errorf("schema history = %+v", hist)
	}
}

func TestKGViews(t *testing.T) {
	kg, _ := newEngine(t)
	ctx := context.Background()
	before := kg.now()
	if v, err := kg.AsOfInstant(ctx, before); err != nil || v.t != 0 {
		t.Fatalf("AsOfInstant before any tx = %+v, %v", v, err)
	}
	e := assert1(t, kg, Ent("alice"), Ent("works_at"), Ent("acme"), Valid{From: day("2025-01-01"), To: day("2026-01-01")})
	if got, _ := kg.Now().ValidAt(*day("2026-01-01")).About(ctx, "alice", ""); len(got) != 0 {
		t.Error("valid time is not half-open")
	}
	if got, _ := kg.Now().ValidAt(*day("2025-12-31")).About(ctx, "alice", ""); len(got) != 1 {
		t.Error("ValidAt inside the interval missed the fact")
	}
	added := kg.now()
	rep := tx(t, kg, func(w *KGTx) error { _, err := w.Retract(e); return err })
	if got, _ := kg.Now().About(ctx, "alice", ""); len(got) != 0 {
		t.Error("Now shows a retracted fact")
	}
	v, _ := kg.AsOfInstant(ctx, added)
	if got, _ := v.About(ctx, "alice", ""); len(got) != 1 {
		t.Errorf("AsOfInstant(%v) = %v, want the fact", added, v)
	}
	if got, _ := kg.AsOf(rep.T).About(ctx, "alice", ""); len(got) != 0 {
		t.Error("AsOf(retracting tx) still shows the fact")
	}
	if got, _ := kg.History().About(ctx, "alice", ""); len(got) != 1 || got[0].Live() {
		t.Errorf("History = %+v", got)
	}
	if got, _ := kg.Now().About(ctx, "nobody", ""); got != nil {
		t.Errorf("unknown entity = %+v", got)
	}
}

// TestKGAsOfMasksLaterRetraction pins that an as-of row describes the view's
// instant, not the present: a statement retracted after the bound is reported
// with no retraction at all. Without the mask the row is selected correctly but
// still carries the later t_ret, so Live() and Triple.Status claim the fact was
// already gone — which is the one question a bitemporal store exists to answer.
func TestKGAsOfMasksLaterRetraction(t *testing.T) {
	kg, _ := newEngine(t)
	ctx := context.Background()
	e := assert1(t, kg, Ent("alice"), Ent("works_at"), Ent("acme"), Valid{})
	rep := tx(t, kg, func(w *KGTx) error { _, err := w.Retract(e); return err })

	// The statement was live at t_add..rep.T-1, so every view before the
	// retraction must show it live.
	if rep.T < 2 {
		t.Fatalf("retraction in tx%d, want tx2 or later", rep.T)
	}
	at := rep.T - 1
	sts, err := kg.AsOf(at).About(ctx, "alice", "")
	if err != nil || len(sts) != 1 {
		t.Fatalf("AsOf(%d) = %d rows, %v; want 1", at, len(sts), err)
	}
	if !sts[0].Live() {
		t.Errorf("AsOf(%d): e%d reported retracted (t_ret=%d) though the retraction was in tx%d",
			at, sts[0].EID, *sts[0].TRet, rep.T)
	}
	if sts[0].TRet != nil || sts[0].RetKind != nil || sts[0].RetractedAt != nil {
		t.Errorf("AsOf(%d): retraction fields = %v/%v/%v, want all nil",
			at, sts[0].TRet, sts[0].RetKind, sts[0].RetractedAt)
	}

	// The same through Match, Explain and the legacy Triple facade, since every
	// one of them is a separate path to the same row.
	if sts, err := kg.AsOf(rep.T-1).Match(ctx, Pattern{Subject: new(Ent("alice"))}); err != nil || len(sts) != 1 || !sts[0].Live() {
		t.Errorf("AsOf.Match = %+v, %v; want one live row", sts, err)
	}
	if ex, err := kg.AsOf(rep.T-1).Explain(ctx, e); err != nil || ex == nil {
		t.Fatalf("Explain: %v", err)
	} else if !ex.Statement.Live() {
		t.Errorf("Explain statement reported retracted under as-of: %+v", ex.Statement)
	}
	trips, err := kg.QueryWith(ctx, KGQuery{Entity: "alice", TxAsOf: "tx1"})
	if err != nil || len(trips) != 1 {
		t.Fatalf("QueryWith tx1 = %d rows, %v; want 1", len(trips), err)
	}
	if got := trips[0].Status; got != "active" {
		t.Errorf("--tx-as-of tx1 status = %q, want \"active\" (the fact was live at tx1)", got)
	}

	// History and Now keep their own meaning: history must still report the
	// retraction, and now must not report the statement at all.
	if got, _ := kg.History().About(ctx, "alice", ""); len(got) != 1 || got[0].Live() {
		t.Errorf("History = %+v; want one retracted row", got)
	}
	if got, _ := kg.Now().About(ctx, "alice", ""); len(got) != 0 {
		t.Errorf("Now = %+v; want nothing", got)
	}
}

func TestKGConflicts(t *testing.T) {
	kg, _ := newEngine(t)
	ctx := context.Background()
	a := assert1(t, kg, Ent("project"), Ent("database"), Ent("postgres"), Valid{From: day("2025-01-01")})
	if _, err := kg.Transact(ctx, TxOptions{Author: "reviewer"}, func(w *KGTx) error {
		e, _, err := w.Assert(Ent("project"), Ent("database"), Ent("sqlite"), Valid{From: day("2025-06-01")})
		if err != nil {
			return err
		}
		if _, _, err := w.Annotate(e, Ent("confidence"), Num(0.9)); err != nil {
			return err
		}
		_, _, err = w.Annotate(e, Ent("source"), Str("go.mod"))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	assert1(t, kg, Ent("project"), Ent("language"), Ent("go"), Valid{})

	cs, err := kg.Now().Conflicts(ctx, ConflictFilter{})
	if err != nil || len(cs) != 1 {
		t.Fatalf("conflicts = %+v, %v", cs, err)
	}
	c := cs[0]
	if c.Subject != Ent("project") || c.Predicate != Ent("database") || len(c.Values) != 2 {
		t.Fatalf("conflict = %+v", c)
	}
	sq := c.Values[1].Statements[0]
	if sq.Author != "reviewer" || sq.Confidence == nil || *sq.Confidence != 0.9 || !slices.Equal(sq.Sources, []string{"go.mod"}) {
		t.Errorf("evidence = %+v", sq)
	}
	if pg := c.Values[0].Statements[0]; pg.Confidence != nil || pg.Sources != nil {
		t.Errorf("absent evidence invented: %+v", pg)
	}
	// Valid time that does not overlap is not a conflict; a correction is not one either.
	tx(t, kg, func(w *KGTx) error { _, err := w.Supersede(a, Patch{ValidTo: day("2025-06-01")}); return err })
	if cs, _ := kg.Now().Conflicts(ctx, ConflictFilter{Subject: "project"}); len(cs) != 0 {
		t.Errorf("after closing postgres: %+v", cs)
	}
	if _, err := kg.History().Conflicts(ctx, ConflictFilter{}); !errors.Is(err, ErrHistoryConflicts) {
		t.Errorf("history conflicts: %v", err)
	}
}

func TestKGTextSearch(t *testing.T) {
	kg, db := newEngine(t)
	ctx := context.Background()
	e := assert1(t, kg, Ent("auth-service"), Ent("depends_on"), Ent("redis"), Valid{})
	tx(t, kg, func(w *KGTx) error {
		if _, _, err := w.Annotate(e, Ent("note"), Str("rate limiting middleware")); err != nil {
			return err
		}
		_, _, err := w.Annotate(e, Ent("confidence"), Num(0.75))
		return err
	})
	if got, _ := kg.Now().TextSearch(ctx, "auth", 10); len(got) != 1 || got[0].EID != e {
		t.Errorf("entity search = %+v", got)
	}
	if got, _ := kg.Now().TextSearch(ctx, "middleware", 10); len(got) != 1 || got[0].Subject != StmtRef(e) {
		t.Errorf("string search = %+v", got)
	}
	if got, _ := kg.Now().TextSearch(ctx, "0.75", 10); len(got) != 0 {
		t.Errorf("numbers are searchable: %+v", got)
	}
	if got, _ := kg.Now().TextSearch(ctx, `"`, 10); got != nil {
		t.Errorf("empty query = %+v", got)
	}
	tx(t, kg, func(w *KGTx) error { _, err := w.Retract(e); return err })
	if got, _ := kg.Now().TextSearch(ctx, "redis", 10); len(got) != 0 {
		t.Errorf("Now search shows retracted facts: %+v", got)
	}
	if got, _ := kg.History().TextSearch(ctx, "redis", 10); len(got) != 1 {
		t.Errorf("History search = %+v", got)
	}
	if _, err := db.Exec("INSERT INTO kg_term_fts(kg_term_fts) VALUES('integrity-check')"); err != nil {
		t.Errorf("fts integrity: %v", err)
	}
}

func TestKGEvents_ReplayEqualsAsOf(t *testing.T) {
	kg, _ := newEngine(t)
	ctx := context.Background()
	var e int64
	tx(t, kg, func(w *KGTx) error {
		var err error
		if e, _, err = w.Assert(Ent("a"), Ent("p"), Ent("b"), Valid{}); err != nil {
			return err
		}
		_, err = w.Retract(e) // assert and retract in one transaction
		return err
	})
	keep := assert1(t, kg, Ent("a"), Ent("p"), Ent("c"), Valid{})
	tx(t, kg, func(w *KGTx) error { _, err := w.Supersede(keep, Patch{Object: new(Ent("d"))}); return err })

	evs, err := kg.Events(ctx, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if evs[0].EID != e || evs[0].Op != "assert" || evs[1].EID != e || evs[1].Op != "retract" {
		t.Errorf("same-tx order = %+v", evs[:2])
	}
	last := evs[len(evs)-1].T
	for at := int64(0); at <= last; at++ {
		liveSet := map[int64]bool{}
		for _, ev := range evs {
			if ev.T > at {
				break
			}
			liveSet[ev.EID] = ev.Op == "assert"
		}
		var want []int64
		for id, ok := range liveSet {
			if ok {
				want = append(want, id)
			}
		}
		slices.Sort(want)
		sts, _ := kg.AsOf(at).Match(ctx, Pattern{IncludeEngine: true})
		var got []int64
		for _, s := range sts {
			got = append(got, s.EID)
		}
		slices.Sort(got)
		if !slices.Equal(got, want) {
			t.Errorf("AsOf(%d) = %v, replay = %v", at, got, want)
		}
	}
	if since, _ := kg.Events(ctx, last-1, 0); len(since) == 0 || since[0].T != last {
		t.Errorf("Events since = %+v", since)
	}
}

// TestKGMigration_ImportsLegacyTriples builds a schema-5 palace with triples
// in it, as the previous release wrote them, and opens it with this one.
func TestKGMigration_ImportsLegacyTriples(t *testing.T) {
	path := filepath.Join(t.TempDir(), "palace.db")
	raw, err := sql.Open("sqlite", path+"?"+connPragmas)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`CREATE TABLE schema_versions (id INTEGER PRIMARY KEY, version INTEGER UNIQUE NOT NULL, applied_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for i := range 5 {
		if err := applyMigration(raw, i+1, migrations[i]); err != nil {
			t.Fatal(err)
		}
	}
	for _, q := range []string{
		`INSERT INTO entities (id, name, created_at) VALUES ('alice','Alice','2026-01-01T00:00:00Z'), ('auth','auth','2026-01-01T00:00:00Z'), ('billing','billing','2026-01-01T00:00:00Z')`,
		`INSERT INTO triples (id, subject_id, predicate_id, object_id, valid_from, valid_to, source_file, extracted_at) VALUES
			('t_old', 'alice', 'works_on', 'auth', '2025-01-01T00:00:00Z', '2026-01-10T00:00:00Z', 'notes.md', '2026-01-02T10:00:00Z'),
			('t_new', 'alice', 'works_on', 'billing', '2026-01-10T00:00:00Z', NULL, '', '2026-01-02T10:00:00Z'),
			('t_sys', 'alice', 'sys:supersedes', 'auth', NULL, NULL, '', '2026-01-01T09:00:00Z')`,
	} {
		if _, err := raw.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	_ = raw.Close()

	db, err := OpenDB(path)
	if err != nil {
		t.Fatalf("OpenDB on schema 5: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	kg := NewKnowledgeGraph(NewSQLitePalaceStore(db))
	ctx := context.Background()

	got, err := kg.Query(ctx, "alice", "", "subject")
	if err != nil || len(got) != 3 {
		t.Fatalf("migrated facts = %+v, %v", got, err)
	}
	byID := map[string]*Triple{}
	for _, tr := range got {
		byID[tr.PredicateID+"/"+tr.ObjectID] = tr
	}
	old := byID["works_on/auth"]
	if old == nil || old.ValidTo == nil || !old.ValidTo.Equal(time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)) ||
		old.SourceFile != "notes.md" || old.Status != "ended" {
		t.Errorf("closed legacy triple = %+v", old)
	}
	if nw := byID["works_on/billing"]; nw == nil || nw.ValidTo != nil || nw.Status != "active" {
		t.Errorf("open legacy triple = %+v", nw)
	}
	if byID["sys:supersedes/auth"] == nil {
		t.Error("a legacy predicate in the sys: namespace was lost")
	}
	at := time.Date(2026, 1, 2, 10, 0, 0, 0, time.UTC)
	if !old.ExtractedAt.Equal(at) || !byID["works_on/billing"].ExtractedAt.Equal(at) {
		t.Errorf("tied extraction times not preserved: %v, %v", old.ExtractedAt, byID["works_on/billing"].ExtractedAt)
	}
	if feb, _ := kg.Query(ctx, "alice", "2026-02-01", "subject"); len(feb) != 2 {
		t.Errorf("valid-time query after migration = %d facts, want 2", len(feb))
	}
	// One transaction per legacy triple, in extraction order.
	if s, _ := kg.Stats(ctx); s.Transactions != 3 || s.TripleCount != 3 || s.ActiveTriples != 2 {
		t.Errorf("stats = %+v", s)
	}
	v, _ := kg.AsOfInstant(ctx, time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC))
	if early, _ := v.About(ctx, "alice", ""); len(early) != 1 {
		t.Errorf("tx-time view before the second extraction = %+v", early)
	}

	if _, err := db.Exec(`INSERT INTO triples (id, subject_id, predicate_id, object_id, extracted_at) VALUES ('x','alice','p','auth','2026-03-01T00:00:00Z')`); err == nil ||
		!strings.Contains(err.Error(), "upgrade pi") {
		t.Errorf("old-style write after upgrade: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO schema_versions (version, applied_at) VALUES (99, 'x')`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	if _, err := OpenDB(path); !errors.Is(err, errSchemaTooNew) {
		t.Errorf("opening a newer schema: %v", err)
	}
}

func TestKG_ConcurrentWritersOnOneFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "palace.db")
	var kgs []*KnowledgeGraph
	for range 2 {
		db, err := OpenDB(path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })
		kgs = append(kgs, NewKnowledgeGraph(NewSQLitePalaceStore(db)))
	}
	const perWriter = 20
	var wg sync.WaitGroup
	errs := make(chan error, 2*perWriter)
	for i, kg := range kgs {
		wg.Go(func() {
			for j := range perWriter {
				_, err := kg.Transact(context.Background(), TxOptions{}, func(w *KGTx) error {
					_, _, err := w.Assert(Ent("writer"), Ent("wrote"), Int(int64(i*1000+j)), Valid{})
					return err
				})
				if err != nil {
					errs <- err
				}
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent write: %v", err)
	}
	s, err := kgs[0].Stats(context.Background())
	if err != nil || s.Transactions != 2*perWriter || s.TripleCount != 2*perWriter {
		t.Errorf("stats after concurrent writes = %+v, %v", s, err)
	}
}
