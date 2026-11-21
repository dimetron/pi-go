package palace

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
)

// Phase-2 core: schema completion, upsert, derived upsert, named graphs,
// volatile state, crypto-shredding, as-of masking and budgets.

func mustErr(t *testing.T, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
}

func txErr(kg *KnowledgeGraph, fn func(*KGTx) error) error {
	_, err := kg.Transact(context.Background(), TxOptions{Author: "test"}, fn)
	return err
}

func TestKGSchema2_ValueAndSubjectType(t *testing.T) {
	kg, _ := newEngine(t)
	tx(t, kg, func(w *KGTx) error {
		return w.SetPredicate(Ent("age"), PredicateSchema{ValueType: "int", SubjectType: "entity", IsEdge: false})
	})
	assert1(t, kg, Ent("alice"), Ent("age"), Int(41), Valid{})
	mustErr(t, txErr(kg, func(w *KGTx) error {
		_, _, err := w.Assert(Ent("bob"), Ent("age"), Str("old"), Valid{})
		return err
	}), ErrSchemaViolation)
	// The rejected value was never interned.
	var n int
	_, db := kg, kg.store.DB()
	if err := db.QueryRow("SELECT count(*) FROM kg_term WHERE lex = 'old'").Scan(&n); err != nil || n != 0 {
		t.Fatalf("rejected value interned: n=%d err=%v", n, err)
	}
	e := assert1(t, kg, Ent("alice"), Ent("knows"), Ent("bob"), Valid{})
	mustErr(t, txErr(kg, func(w *KGTx) error {
		_, _, err := w.Annotate(e, Ent("age"), Int(3))
		return err
	}), ErrSchemaViolation)

	// A schema live data breaks is rejected.
	mustErr(t, txErr(kg, func(w *KGTx) error {
		return w.SetPredicate(Ent("age"), PredicateSchema{ValueType: "string"})
	}), ErrSchemaViolation)
	tx(t, kg, func(w *KGTx) error { return w.SetPredicate(Ent("knows"), PredicateSchema{IsEdge: true}) })
	sch, err := kg.Schema(context.Background(), "knows")
	if err != nil || !sch.IsEdge || sch.ValueType != "" {
		t.Fatalf("schema = %+v, %v", sch, err)
	}
	// Unknown names are refused.
	if err := txErr(kg, func(w *KGTx) error {
		return w.SetPredicate(Ent("x"), PredicateSchema{ValueType: "hue"})
	}); err == nil {
		t.Fatal("unknown value type accepted")
	}
}

func TestKGSchema2_CheckOrderValueTypeFirst(t *testing.T) {
	kg, _ := newEngine(t)
	tx(t, kg, func(w *KGTx) error {
		return w.SetPredicate(Ent("email"), PredicateSchema{ValueType: "string", Unique: true})
	})
	assert1(t, kg, Ent("alice"), Ent("email"), Str("a@x"), Valid{})
	// Wrong type and a unique collision at once: the type check reports.
	err := txErr(kg, func(w *KGTx) error {
		_, _, err := w.Assert(Ent("bob"), Ent("email"), Int(1), Valid{})
		return err
	})
	if !errors.Is(err, ErrSchemaViolation) || !strings.Contains(err.Error(), "value type") {
		t.Fatalf("err = %v", err)
	}
}

func TestKGUpsert(t *testing.T) {
	kg, _ := newEngine(t)
	mustErr(t, txErr(kg, func(w *KGTx) error {
		_, _, _, err := w.Upsert(Ent("email"), Str("a@x"))
		return err
	}), ErrNotUnique)
	tx(t, kg, func(w *KGTx) error { return w.SetPredicate(Ent("email"), PredicateSchema{Unique: true}) })
	var n1, n2 Value
	var new1, new2 bool
	tx(t, kg, func(w *KGTx) error {
		var err error
		n1, _, new1, err = w.Upsert(Ent("email"), Str("a@x"))
		return err
	})
	tx(t, kg, func(w *KGTx) error {
		var err error
		n2, _, new2, err = w.Upsert(Ent("email"), Str("a@x"))
		return err
	})
	if !new1 || new2 || n1 != n2 || !strings.HasPrefix(n1.Text, "node:") {
		t.Fatalf("upsert: %v %v %v %v", n1, new1, n2, new2)
	}
}

func TestKGUpsertDerivedAndWidening(t *testing.T) {
	kg, _ := newEngine(t)
	e1 := assert1(t, kg, Ent("alice"), Ent("knows"), Ent("bob"), Valid{})
	e2 := assert1(t, kg, Ent("bob"), Ent("knows"), Ent("carol"), Valid{})
	up := func(sup ...int64) (Value, int64, bool) {
		var (
			n   Value
			eid int64
			nw  bool
		)
		tx(t, kg, func(w *KGTx) error {
			var err error
			n, eid, nw, err = w.UpsertDerived(Ent("skolem_key"), Str("r/1/abc"), sup)
			return err
		})
		return n, eid, nw
	}
	n1, k1, new1 := up(e1)
	n2, k2, new2 := up(e1)
	if !new1 || new2 || n1 != n2 || k1 != k2 {
		t.Fatalf("not reused across transactions: %v %v %v %v", n1, n2, new1, new2)
	}
	n3, _, new3 := up(e1, e2)
	if !new3 || n3 == n1 {
		t.Fatal("a different support set reused the node")
	}
	tx(t, kg, func(w *KGTx) error { _, err := w.Retract(e1); return err })
	if live(t, kg, k1) {
		t.Fatal("key did not fall with its support")
	}
	n4, _, new4 := up(e2)
	if !new4 || n4 == n1 {
		t.Fatal("expected a new node after the support fell")
	}
	// An asserted key is never reused.
	assert1(t, kg, Ent("n0"), Ent("skolem_key"), Str("k"), Valid{})
	var n5 Value
	tx(t, kg, func(w *KGTx) error {
		var err error
		n5, _, _, err = w.UpsertDerived(Ent("skolem_key"), Str("k"), []int64{e2})
		return err
	})
	if n5.Text == "n0" {
		t.Fatal("asserted key reused")
	}

	// D52: widening a derived statement by supersede keeps its support.
	var d int64
	tx(t, kg, func(w *KGTx) error {
		var err error
		d, _, err = w.Derive(Ent("bob"), Ent("reaches"), Ent("carol"), Valid{From: day("2026-02-01"), To: day("2026-03-01")}, []int64{e2})
		return err
	})
	var wide int64
	tx(t, kg, func(w *KGTx) error {
		var err error
		wide, err = w.Supersede(d, Patch{ValidFrom: day("2026-01-01")})
		return err
	})
	ex, err := kg.Now().Explain(context.Background(), wide)
	if err != nil || len(ex.Supports) != 1 || ex.Supports[0].EID != e2 {
		t.Fatalf("widened support = %+v, %v", ex, err)
	}
	tx(t, kg, func(w *KGTx) error { _, err := w.Retract(e2); return err })
	if live(t, kg, wide) {
		t.Fatal("widened conclusion did not fall with its support")
	}
}

func TestKGGraphs(t *testing.T) {
	ctx := context.Background()
	kg, _ := newEngine(t)
	a := assert1(t, kg, Ent("alice"), Ent("knows"), Ent("bob"), Valid{})
	b := assert1(t, kg, Ent("alice"), Ent("likes"), Ent("tea"), Valid{})
	var conf int64
	tx(t, kg, func(w *KGTx) error {
		var err error
		conf, _, err = w.Annotate(a, Ent("confidence"), Num(0.9))
		return err
	})
	before := tx(t, kg, func(w *KGTx) error {
		_, _, err := w.AddToGraph(a, "Work")
		return err
	}).T - 1

	got, err := kg.Now().InGraph("work").About(ctx, "alice", "")
	if err != nil || len(got) != 1 || got[0].EID != a {
		t.Fatalf("graph about = %+v, %v", got, err)
	}
	// The default graph is the union.
	if all, _ := kg.Now().About(ctx, "alice", ""); len(all) != 2 {
		t.Fatalf("default graph = %d facts", len(all))
	}
	// Layers of a member are in the graph; membership is bitemporal.
	if l, _ := kg.Now().InGraph("work").Match(ctx, Pattern{Subject: new(StmtRef(a))}); len(l) != 1 || l[0].EID != conf {
		t.Fatalf("layers in graph = %+v", l)
	}
	if old, _ := kg.AsOf(before).InGraph("work").About(ctx, "alice", ""); len(old) != 0 {
		t.Fatal("membership visible before it was added")
	}
	if gs, _ := kg.Now().ListGraphs(ctx); len(gs) != 1 || gs[0].Name != "work" || gs[0].Members != 1 {
		t.Fatalf("graphs = %+v", gs)
	}
	// Superseding replays the membership; retracting drops it.
	var a2 int64
	tx(t, kg, func(w *KGTx) error {
		var err error
		a2, err = w.Supersede(a, Patch{Object: new(Ent("carol"))})
		return err
	})
	if gs, _ := kg.Now().GraphsOf(ctx, a2); len(gs) != 1 || gs[0] != "work" {
		t.Fatalf("membership not replayed: %v", gs)
	}
	tx(t, kg, func(w *KGTx) error {
		_, _, err := w.AddToGraph(b, "work")
		return err
	})
	tx(t, kg, func(w *KGTx) error {
		ok, err := w.RemoveFromGraph(b, "work")
		if !ok {
			t.Error("RemoveFromGraph = false")
		}
		return err
	})
	if !live(t, kg, b) {
		t.Fatal("removing from a graph retracted the fact")
	}
	tx(t, kg, func(w *KGTx) error {
		n, err := w.DropGraph("work")
		if n != 1 {
			t.Errorf("DropGraph = %d", n)
		}
		return err
	})
	if got, _ := kg.Now().InGraph("work").About(ctx, "alice", ""); len(got) != 0 || !live(t, kg, a2) {
		t.Fatal("drop graph: members still visible or facts gone")
	}
	if got, _ := kg.Now().InGraph("nope").About(ctx, "alice", ""); len(got) != 0 {
		t.Fatal("unknown graph matched")
	}
}

func TestKGVolatile(t *testing.T) {
	ctx := context.Background()
	kg, db := newEngine(t)
	alice, seen := Ent("alice"), Ent("last_seen")
	r3 := tx(t, kg, func(w *KGTx) error { return w.SetVolatile(alice, seen, Str("t3")) })
	r4 := tx(t, kg, func(w *KGTx) error { return w.SetVolatile(alice, seen, Str("t4")) })
	var n int
	var at int64
	if err := db.QueryRow("SELECT count(*), max(updated_at) FROM kg_volatile").Scan(&n, &at); err != nil || n != 1 ||
		at != r4.Instant.UnixMilli() {
		t.Fatalf("volatile rows n=%d at=%d err=%v", n, at, err)
	}
	got, err := kg.Now().Resolve(ctx, alice, seen)
	if err != nil || len(got) != 1 || got[0].Value != Str("t4") || got[0].EID != 0 {
		t.Fatalf("resolve = %+v, %v", got, err)
	}
	// Not a statement, not an event, absent under time travel.
	if m, _ := kg.Now().Match(ctx, Pattern{Subject: &alice, Predicate: &seen}); len(m) != 0 {
		t.Fatal("volatile visible as a triple")
	}
	if ev, _ := kg.Events(ctx, r3.T-1, 0); len(ev) != 0 {
		t.Fatalf("volatile wrote events: %+v", ev)
	}
	if got, _ := kg.AsOf(r4.T).Resolve(ctx, alice, seen); len(got) != 0 {
		t.Fatal("volatile visible as of")
	}
	if got, _ := kg.History().Resolve(ctx, alice, seen); len(got) != 0 {
		t.Fatal("volatile visible in history")
	}
	// A failed transaction discards the write.
	_ = txErr(kg, func(w *KGTx) error {
		if err := w.SetVolatile(alice, seen, Str("lost")); err != nil {
			return err
		}
		return errors.New("boom")
	})
	if got, _ := kg.Now().Resolve(ctx, alice, seen); got[0].Value != Str("t4") {
		t.Fatal("failed transaction kept its volatile write")
	}
	// A statement wins over a volatile value.
	assert1(t, kg, alice, seen, Str("stated"), Valid{})
	if got, _ := kg.Now().Resolve(ctx, alice, seen); len(got) != 1 || got[0].Value != Str("stated") {
		t.Fatalf("statement did not win: %+v", got)
	}
	// Clear twice succeeds.
	score := Ent("score")
	tx(t, kg, func(w *KGTx) error { return w.SetVolatile(alice, score, Int(5)) })
	tx(t, kg, func(w *KGTx) error { return w.ClearVolatile(alice, score) })
	tx(t, kg, func(w *KGTx) error { return w.ClearVolatile(alice, score) })
	if got, _ := kg.Now().Resolve(ctx, alice, score); len(got) != 0 {
		t.Fatal("cleared value still resolves")
	}
	// Sensitive keys cannot hold volatile values.
	tx(t, kg, func(w *KGTx) error { return w.SetPredicate(Ent("ssn"), PredicateSchema{Sensitive: true}) })
	mustErr(t, txErr(kg, func(w *KGTx) error { return w.SetVolatile(alice, Ent("ssn"), Str("123")) }), ErrSensitiveVolatile)
}

func rawContains(t *testing.T, db *sql.DB, needle string) bool {
	t.Helper()
	var n int
	if err := db.QueryRow("SELECT count(*) FROM kg_term WHERE instr(lex, ?) > 0", needle).Scan(&n); err != nil {
		t.Fatal(err)
	}
	var f int
	if err := db.QueryRow("SELECT count(*) FROM kg_term_fts WHERE kg_term_fts MATCH ?", `"`+needle+`"`).Scan(&f); err != nil {
		t.Fatal(err)
	}
	return n+f > 0
}

func TestKGSeal_EncryptedAtRestAndErase(t *testing.T) {
	ctx := context.Background()
	kg, db := newEngine(t)
	tx(t, kg, func(w *KGTx) error { return w.SetPredicate(Ent("email"), PredicateSchema{Sensitive: true}) })
	e := assert1(t, kg, Ent("alice"), Ent("email"), Str("alice@secret.example"), Valid{})
	again := assert1(t, kg, Ent("alice"), Ent("email"), Str("alice@secret.example"), Valid{})
	if again != e {
		t.Fatal("sealed assert is not idempotent")
	}
	if rawContains(t, db, "secret") {
		t.Fatal("plaintext of a sensitive value reached kg_term or its index")
	}
	got, err := kg.Now().About(ctx, "alice", "")
	if err != nil || len(got) != 1 || got[0].Object != Str("alice@secret.example") {
		t.Fatalf("decoded = %+v, %v", got, err)
	}
	// Annotations of a sensitive fact share the subject key.
	tx(t, kg, func(w *KGTx) error {
		if err := w.SetPredicate(Ent("note"), PredicateSchema{Sensitive: true}); err != nil {
			return err
		}
		_, _, err := w.Annotate(e, Ent("note"), Str("private remark"))
		return err
	})
	var keys int
	if err := db.QueryRow("SELECT count(*) FROM kg_seal_key").Scan(&keys); err != nil || keys != 1 {
		t.Fatalf("seal keys = %d, %v", keys, err)
	}
	// Supersede patches are sealed too.
	var e2 int64
	tx(t, kg, func(w *KGTx) error {
		var err error
		e2, err = w.Supersede(e, Patch{Object: new(Str("alice@hidden.example"))})
		return err
	})
	if rawContains(t, db, "hidden") || rawContains(t, db, "private") {
		t.Fatal("supersede or annotation leaked plaintext")
	}
	// Constant matching with and without a bound subject.
	alice, email, val := Ent("alice"), Ent("email"), Str("alice@hidden.example")
	if m, _ := kg.Now().Match(ctx, Pattern{Subject: &alice, Predicate: &email, Object: &val}); len(m) != 1 || m[0].EID != e2 {
		t.Fatalf("bound-subject sealed match = %+v", m)
	}
	if m, _ := kg.Now().Match(ctx, Pattern{Predicate: &email, Object: &val}); len(m) != 1 || m[0].EID != e2 {
		t.Fatalf("free-subject sealed match = %+v", m)
	}

	rep, err := kg.Erase(ctx, TxOptions{Author: "dpo", Reason: "gdpr request"}, "alice")
	if err != nil || !rep.Erased || len(rep.Tx.Erased) != 1 {
		t.Fatalf("erase = %+v, %v", rep, err)
	}
	got, _ = kg.Now().About(ctx, "alice", "")
	if len(got) != 1 || got[0].Object != Str(ErasedText) || got[0].EID != e2 {
		t.Fatalf("after erase = %+v", got)
	}
	// History stays: the old version and its times are still there.
	if h, _ := kg.History().About(ctx, "alice", ""); len(h) != 2 {
		t.Fatalf("history after erase = %d", len(h))
	}
	// Writing to an erased subject fails; erasing twice reports false.
	mustErr(t, txErr(kg, func(w *KGTx) error {
		_, _, err := w.Assert(Ent("alice"), Ent("email"), Str("new@x"), Valid{})
		return err
	}), ErrErased)
	if rep, err := kg.Erase(ctx, TxOptions{}, "alice"); err != nil || rep.Erased {
		t.Fatalf("second erase = %+v, %v", rep, err)
	}
}

func TestKGSeal_SchemaRules(t *testing.T) {
	kg, _ := newEngine(t)
	assert1(t, kg, Ent("alice"), Ent("phone"), Str("555"), Valid{})
	mustErr(t, txErr(kg, func(w *KGTx) error {
		return w.SetPredicate(Ent("phone"), PredicateSchema{Sensitive: true})
	}), ErrSchemaViolation)
	mustErr(t, txErr(kg, func(w *KGTx) error {
		return w.SetPredicate(Ent("ssn"), PredicateSchema{Sensitive: true, Unique: true})
	}), ErrSchemaViolation)
	// Entities are never sealed.
	tx(t, kg, func(w *KGTx) error { return w.SetPredicate(Ent("friend"), PredicateSchema{Sensitive: true}) })
	assert1(t, kg, Ent("alice"), Ent("friend"), Ent("bob"), Valid{})
	if m, _ := kg.Now().About(context.Background(), "bob", ""); len(m) != 1 {
		t.Fatal("entity object was sealed")
	}
}

func TestKGSealKeyTriggers(t *testing.T) {
	kg, db := newEngine(t)
	tx(t, kg, func(w *KGTx) error { return w.SetPredicate(Ent("email"), PredicateSchema{Sensitive: true}) })
	assert1(t, kg, Ent("alice"), Ent("email"), Str("a@x"), Valid{})
	var subject int64
	if err := db.QueryRow("SELECT subject FROM kg_seal_key").Scan(&subject); err != nil {
		t.Fatal(err)
	}
	for name, stmt := range map[string]string{
		"delete":            "DELETE FROM kg_seal_key",
		"replace":           "INSERT OR REPLACE INTO kg_seal_key (subject, key, created_t) VALUES (?, zeroblob(64), 1)",
		"rekey":             "UPDATE kg_seal_key SET key = randomblob(64) WHERE subject = ?",
		"backdated destroy": "UPDATE kg_seal_key SET key = zeroblob(64), destroyed_t = 1 WHERE subject = ?",
		"backdated create":  "INSERT INTO kg_seal_key (subject, key, created_t) VALUES (?+16, zeroblob(64), 1)",
	} {
		if _, err := db.Exec(stmt, subject); err == nil {
			t.Errorf("%s: raw SQL succeeded", name)
		}
	}
}

func TestKGAsOfMasksLaterRetraction(t *testing.T) {
	ctx := context.Background()
	kg, _ := newEngine(t)
	e := assert1(t, kg, Ent("alice"), Ent("knows"), Ent("bob"), Valid{})
	t1, _ := lastTOf(kg)
	before, _ := kg.AsOf(t1).About(ctx, "alice", "")
	tx(t, kg, func(w *KGTx) error { _, err := w.Retract(e); return err })
	after, _ := kg.AsOf(t1).About(ctx, "alice", "")
	if len(before) != 1 || len(after) != 1 || after[0].TRet != nil || after[0].RetKind != nil ||
		after[0].RetractedAt != nil || before[0].EID != after[0].EID {
		t.Fatalf("as-of row changed after a later retraction: %+v -> %+v", before, after)
	}
	if ex, _ := kg.AsOf(t1).Explain(ctx, e); ex.Statement.TRet != nil {
		t.Fatal("explain as-of shows a later retraction")
	}
}

func lastTOf(kg *KnowledgeGraph) (int64, error) {
	var t int64
	err := kg.read(context.Background(), func(q querier) error {
		var err error
		t, err = lastT(context.Background(), q)
		return err
	})
	return t, err
}

func TestKGMeter(t *testing.T) {
	m := newMeter(context.Background(), Budget{})
	if err := m.take(1_000_000, 1<<30); err != nil {
		t.Fatalf("empty budget limited: %v", err)
	}
	m = newMeter(context.Background(), Budget{MaxRows: 3})
	if m.take(2, 0) != nil || !errors.Is(m.take(2, 0), ErrBudget) {
		t.Fatal("rows budget not shared across takes")
	}
	m = newMeter(context.Background(), Budget{MaxBytes: 10})
	if !errors.Is(m.takeStatements([]Statement{{Subject: Ent("a"), Object: Str(strings.Repeat("x", 20))}}), ErrBudget) {
		t.Fatal("bytes budget ignored an oversized literal")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := newMeter(ctx, Budget{}).take(0, 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel = %v", err)
	}
}
