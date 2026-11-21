package palace

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func bundleJSON(t *testing.T, b *Bundle) string {
	t.Helper()
	raw, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func exportBundle(t *testing.T, v View, eid int64, opts BundleOptions) *Bundle {
	t.Helper()
	b, err := v.Bundle(context.Background(), eid, opts)
	if err != nil {
		t.Fatalf("Bundle: %v", err)
	}
	return b
}

func importBundle(t *testing.T, kg *KnowledgeGraph, b *Bundle) (map[string]int64, *TxReport) {
	t.Helper()
	var ids map[string]int64
	rep := tx(t, kg, func(w *KGTx) error {
		var err error
		ids, err = w.ImportBundle(b)
		return err
	})
	return ids, rep
}

func countLive(t *testing.T, kg *KnowledgeGraph) int {
	t.Helper()
	got, err := kg.Now().Match(context.Background(), Pattern{})
	if err != nil {
		t.Fatal(err)
	}
	return len(got)
}

// bundleFixture builds: S1 with layers, a dependent that points at S1 from an
// unrelated statement X (reference-only), and private noise about X.
func bundleFixture(t *testing.T, kg *KnowledgeGraph) (s1 int64) {
	t.Helper()
	tx(t, kg, func(w *KGTx) error {
		var err error
		s1, _, err = w.Assert(Ent("alice"), Ent("likes"), Ent("tea"), Valid{From: day("2024-01-01"), To: day("2025-01-01")})
		if err != nil {
			return err
		}
		l1, _, err := w.Annotate(s1, Ent("source"), Str("book"))
		if err != nil {
			return err
		}
		if _, _, err = w.Annotate(l1, Ent("page"), Int(7)); err != nil {
			return err
		}
		bob, _, err := w.Assert(Ent("bob"), Ent("believes"), StmtRef(s1), Valid{})
		if err != nil {
			return err
		}
		if _, _, err = w.Annotate(bob, Ent("certainty"), Num(0.5)); err != nil {
			return err
		}
		x, _, err := w.Assert(Ent("dave"), Ent("said"), Str("hi"), Valid{})
		if err != nil {
			return err
		}
		n, _, err := w.Assert(StmtRef(x), Ent("cites"), StmtRef(s1), Valid{})
		if err != nil {
			return err
		}
		if _, _, err = w.Annotate(n, Ent("weight"), Int(3)); err != nil {
			return err
		}
		_, _, err = w.Annotate(x, Ent("note"), Str("private noise"))
		return err
	})
	return s1
}

func TestBundle_RoundTripIdempotentAndScope(t *testing.T) {
	src, _ := newEngine(t)
	dst, _ := newEngine(t)
	s1 := bundleFixture(t, src)
	b := exportBundle(t, src.Now(), s1, BundleOptions{})
	raw := bundleJSON(t, b)
	if strings.Contains(raw, "private noise") {
		t.Fatal("dependents of a reference-only member were expanded")
	}
	if !strings.Contains(raw, "dave") || !strings.Contains(raw, `"version":1`) {
		t.Fatalf("closure over the subject reference is missing: %s", raw)
	}
	var back Bundle
	if err := json.Unmarshal([]byte(raw), &back); err != nil {
		t.Fatal(err)
	}
	ids, _ := importBundle(t, dst, &back)
	if len(ids) != 8 || countLive(t, dst) != 8 {
		t.Fatalf("imported %d ids, %d live statements, want 8", len(ids), countLive(t, dst))
	}
	again := exportBundle(t, dst.Now(), ids[b.Root.ID], BundleOptions{})
	if got := bundleJSON(t, again); got != raw {
		t.Fatalf("round trip differs:\n src %s\n dst %s", raw, got)
	}
	// Statements and layers carry the same content, valid time included.
	root, err := dst.Get(context.Background(), ids[b.Root.ID])
	if err != nil || root == nil || root.Object != Ent("tea") || root.ValidFrom == nil || !root.ValidFrom.Equal(*day("2024-01-01")) {
		t.Fatalf("root = %+v, %v", root, err)
	}
	if ls, _ := dst.Now().Layers(context.Background(), root.EID); len(ls) != 1 || ls[0].Predicate != Ent("source") {
		t.Fatalf("layers = %+v", ls)
	}
	// A second import writes nothing.
	ids2, rep := importBundle(t, dst, &back)
	if len(rep.Asserted) != 0 || countLive(t, dst) != 8 {
		t.Fatalf("re-import asserted %v", rep.Asserted)
	}
	for k, v := range ids {
		if ids2[k] != v {
			t.Fatalf("id %s moved %d -> %d", k, v, ids2[k])
		}
	}
}

func TestBundle_PastBundleViaAsOf(t *testing.T) {
	ctx := context.Background()
	kg, _ := newEngine(t)
	var s1, l1 int64
	tx(t, kg, func(w *KGTx) error {
		var err error
		if s1, _, err = w.Assert(Ent("a"), Ent("p"), Ent("b"), Valid{}); err != nil {
			return err
		}
		l1, _, err = w.Annotate(s1, Ent("note"), Str("one"))
		return err
	})
	t0 := tx(t, kg, func(w *KGTx) error { return nil }).T
	tx(t, kg, func(w *KGTx) error { _, _, err := w.Annotate(s1, Ent("note"), Str("two")); return err })
	tx(t, kg, func(w *KGTx) error { _, err := w.Retract(l1); return err })
	now := exportBundle(t, kg.Now(), s1, BundleOptions{})
	past := exportBundle(t, kg.AsOf(t0), s1, BundleOptions{})
	if len(now.Root.Layers) != 1 || now.Root.Layers[0].O.Text != "two" {
		t.Fatalf("now layers = %+v", now.Root.Layers)
	}
	if len(past.Root.Layers) != 1 || past.Root.Layers[0].O.Text != "one" {
		t.Fatalf("past layers = %+v", past.Root.Layers)
	}
	if _, err := kg.AsOf(0).Bundle(ctx, s1, BundleOptions{}); err == nil {
		t.Fatal("a bundle of a statement not yet written must fail")
	}
}

func TestBundle_StatementObjectInlinedThenRef(t *testing.T) {
	kg, _ := newEngine(t)
	var r int64
	tx(t, kg, func(w *KGTx) error {
		s1, _, err := w.Assert(Ent("alice"), Ent("likes"), Ent("tea"), Valid{})
		if err != nil {
			return err
		}
		if r, _, err = w.Assert(Ent("carol"), Ent("believes"), StmtRef(s1), Valid{}); err != nil {
			return err
		}
		_, _, err = w.Assert(StmtRef(r), Ent("cites"), StmtRef(s1), Valid{})
		return err
	})
	b := exportBundle(t, kg.Now(), r, BundleOptions{})
	if b.Root.O.Stmt == nil || b.Root.O.Stmt.P.Text != "likes" {
		t.Fatalf("object not inlined: %+v", b.Root.O)
	}
	if len(b.Root.Layers) != 1 || b.Root.Layers[0].O.Ref != b.Root.O.Stmt.ID || b.Root.Layers[0].S.Ref != b.Root.ID {
		t.Fatalf("second occurrence is not a ref: %+v", b.Root.Layers)
	}
	if strings.Contains(bundleJSON(t, b), `"kind":0`) {
		t.Fatal("a statement reference serialized an empty value")
	}
	dst, _ := newEngine(t)
	ids, _ := importBundle(t, dst, b)
	if len(ids) != 3 || countLive(t, dst) != 3 {
		t.Fatalf("ids %v, live %d", ids, countLive(t, dst))
	}
}

func TestBundle_LayerOfInlinedStatementReferencingParent(t *testing.T) {
	// L is a layer of X, and points at M, which inlines X as its object: the
	// export must not write L before M.
	kg, _ := newEngine(t)
	var m int64
	tx(t, kg, func(w *KGTx) error {
		x, _, err := w.Assert(Ent("a"), Ent("p"), Ent("b"), Valid{})
		if err != nil {
			return err
		}
		if m, _, err = w.Assert(Ent("c"), Ent("sees"), StmtRef(x), Valid{}); err != nil {
			return err
		}
		_, _, err = w.Assert(StmtRef(x), Ent("echo"), StmtRef(m), Valid{})
		return err
	})
	b := exportBundle(t, kg.Now(), m, BundleOptions{})
	dst, _ := newEngine(t)
	if ids, _ := importBundle(t, dst, b); len(ids) != 3 {
		t.Fatalf("ids = %v", ids)
	}
}

func TestBundle_CycleRejectedWithNothingWritten(t *testing.T) {
	kg, _ := newEngine(t)
	bad := &Bundle{Version: 1, Root: BundleStmt{
		ID: "b1", S: BundleValue{Value: Ent("a")}, P: BundleValue{Value: Ent("p")}, O: BundleValue{Value: Ent("b")},
		Layers: []BundleStmt{{
			ID: "b2", S: BundleValue{Ref: "b1"}, P: BundleValue{Value: Ent("q")}, O: BundleValue{Ref: "b3"},
		}},
	}}
	// The transaction commits anyway: only validation can keep b1 out.
	if _, err := kg.Transact(context.Background(), TxOptions{Author: "t"}, func(w *KGTx) error {
		_, err := w.ImportBundle(bad)
		mustErr(t, err, ErrBundleCycle)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if n := countLive(t, kg); n != 0 {
		t.Fatalf("%d statements written before the cycle was found", n)
	}
	self := &Bundle{Version: 1, Root: BundleStmt{
		ID: "b1", S: BundleValue{Value: Ent("a")}, P: BundleValue{Value: Ent("p")}, O: BundleValue{Ref: "b1"},
	}}
	mustErr(t, txErr(kg, func(w *KGTx) error { _, err := w.ImportBundle(self); return err }), ErrBundleCycle)
}

func TestBundle_EnginePredicates(t *testing.T) {
	ctx := context.Background()
	kg, _ := newEngine(t)
	var s2, d int64
	tx(t, kg, func(w *KGTx) error {
		s1, _, err := w.Assert(Ent("alice"), Ent("likes"), Ent("tea"), Valid{})
		if err != nil {
			return err
		}
		if s2, err = w.Supersede(s1, Patch{Object: new(Ent("coffee"))}); err != nil {
			return err
		}
		if _, err = w.Confirm(s2); err != nil {
			return err
		}
		d, _, err = w.Derive(Ent("alice"), Ent("drinks"), Ent("coffee"), Valid{}, []int64{s2})
		return err
	})
	if d == 0 {
		t.Fatal("no derived statement")
	}
	raw := bundleJSON(t, exportBundle(t, kg.Now(), s2, BundleOptions{}))
	for _, name := range []string{predSupersedes, predConfirmedBy, predDerivedFrom} {
		if strings.Contains(raw, name) {
			t.Fatalf("bundle holds %s: %s", name, raw)
		}
	}
	// Import refuses any other engine predicate, and refuses before writing.
	for _, pred := range []string{predSensitive, predRuleText, predSupersedes} {
		bad := &Bundle{Version: 1, Root: BundleStmt{
			ID: "b1", S: BundleValue{Value: Ent("a")}, P: BundleValue{Value: sysPred(pred)}, O: BundleValue{Value: Ent("b")},
		}}
		mustErr(t, txErr(kg, func(w *KGTx) error { _, err := w.ImportBundle(bad); return err }), ErrReservedPredicate)
	}
	// An engine statement cannot be a root.
	sup, err := kg.History().Match(ctx, Pattern{Predicate: new(sysPred(predSupersedes)), IncludeEngine: true})
	if err != nil || len(sup) == 0 {
		t.Fatalf("no supersedes statement: %v", err)
	}
	if _, err := kg.Now().Bundle(ctx, sup[0].EID, BundleOptions{}); err == nil {
		t.Fatal("bundle of engine bookkeeping must fail")
	}
}

func TestBundle_MembershipRoundTrip(t *testing.T) {
	ctx := context.Background()
	src, _ := newEngine(t)
	dst, _ := newEngine(t)
	var s1 int64
	tx(t, src, func(w *KGTx) error {
		var err error
		if s1, _, err = w.Assert(Ent("alice"), Ent("likes"), Ent("tea"), Valid{}); err != nil {
			return err
		}
		_, _, err = w.AddToGraph(s1, "diet")
		return err
	})
	b := exportBundle(t, src.Now(), s1, BundleOptions{})
	if !strings.Contains(bundleJSON(t, b), predInGraph) {
		t.Fatal("membership missing from the bundle")
	}
	importBundle(t, dst, b)
	got, err := dst.Now().InGraph("diet").Match(ctx, Pattern{})
	if err != nil || len(got) != 1 || got[0].Object != Ent("tea") {
		t.Fatalf("graph members = %+v, %v", got, err)
	}
	// Re-import is a no-op; a membership on a non-statement is refused.
	if _, rep := importBundle(t, dst, b); len(rep.Asserted) != 0 {
		t.Fatalf("re-import asserted %v", rep.Asserted)
	}
	bad := &Bundle{Version: 1, Root: BundleStmt{
		ID: "b1", S: BundleValue{Value: Ent("a")}, P: BundleValue{Value: sysPred(predInGraph)}, O: BundleValue{Value: Ent("g")},
	}}
	if err := txErr(dst, func(w *KGTx) error { _, err := w.ImportBundle(bad); return err }); err == nil {
		t.Fatal("membership of an entity must be refused")
	}
}

func TestBundle_Sensitive(t *testing.T) {
	ctx := context.Background()
	src, _ := newEngine(t)
	dst, dstDB := newEngine(t)
	mark := func(kg *KnowledgeGraph) {
		tx(t, kg, func(w *KGTx) error { return w.SetPredicate(Ent("email"), PredicateSchema{Sensitive: true}) })
	}
	mark(src)
	e := assert1(t, src, Ent("alice"), Ent("email"), Str("alice@secret.example"), Valid{})
	if _, err := src.Now().Bundle(ctx, e, BundleOptions{}); err == nil {
		t.Fatal("sealed value exported without IncludeSensitive")
	} else {
		mustErr(t, err, ErrSensitiveBundle)
	}
	b := exportBundle(t, src.Now(), e, BundleOptions{IncludeSensitive: true})
	if !b.Root.O.Sensitive || b.Root.O.Text != "alice@secret.example" {
		t.Fatalf("root object = %+v", b.Root.O)
	}
	if !strings.Contains(bundleJSON(t, b), `"sensitive":true`) {
		t.Fatal("sensitive marker missing")
	}
	// The target predicate is not sensitive: the plaintext must not land.
	mustErr(t, txErr(dst, func(w *KGTx) error { _, err := w.ImportBundle(b); return err }), ErrSchemaViolation)
	if rawContains(t, dstDB, "secret") || countLive(t, dst) != 0 {
		t.Fatal("rejected import left data behind")
	}
	mark(dst)
	ids, _ := importBundle(t, dst, b)
	if rawContains(t, dstDB, "secret") {
		t.Fatal("imported sensitive value is plaintext at rest")
	}
	got, err := dst.Get(ctx, ids[b.Root.ID])
	if err != nil || got == nil || got.Object != Str("alice@secret.example") {
		t.Fatalf("decoded = %+v, %v", got, err)
	}
	// A sealed value in a layer refuses the plain export as well.
	var s1 int64
	tx(t, src, func(w *KGTx) error {
		var err error
		if s1, _, err = w.Assert(Ent("alice"), Ent("knows"), Ent("bob"), Valid{}); err != nil {
			return err
		}
		_, _, err = w.Annotate(s1, Ent("email"), Str("x@secret.example"))
		return err
	})
	_, err = src.Now().Bundle(ctx, s1, BundleOptions{})
	mustErr(t, err, ErrSensitiveBundle)
}
