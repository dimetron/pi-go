package palace

import (
	"context"
	"strings"
	"testing"
)

// Regression tests for the Codex review of PR #487, one per finding.

func sensitivePalace(t *testing.T) *KnowledgeGraph {
	t.Helper()
	kg, _ := newEngine(t)
	tx(t, kg, func(w *KGTx) error { return w.SetPredicate(Ent("secret"), PredicateSchema{Sensitive: true}) })
	assert1(t, kg, Ent("alice"), Ent("secret"), Str("hunter2-password"), Valid{})
	return kg
}

// #1: materializing a rule must not copy a sealed value into plaintext.
func TestReview_MaterializeRefusesSealedCopy(t *testing.T) {
	kg := sensitivePalace(t)
	tx(t, kg, func(w *KGTx) error {
		_, err := w.StoreProgram("leak", "datalog", "copy(X, V) :- secret(X, V).")
		return err
	})
	_, err := kg.Materialize(context.Background(), TxOptions{}, "leak", kg.Now(), RuleOptions{})
	mustErr(t, err, ErrNotMaterializable)
	if rawContains(t, kg.store.DB(), "hunter2") {
		t.Fatal("plaintext of a sealed value was written")
	}
	// Under a sensitive head predicate the copy is sealed and allowed.
	tx(t, kg, func(w *KGTx) error { return w.SetPredicate(Ent("copy"), PredicateSchema{Sensitive: true}) })
	if _, err := kg.Materialize(context.Background(), TxOptions{}, "leak", kg.Now(), RuleOptions{}); err != nil {
		t.Fatalf("sensitive head: %v", err)
	}
	if rawContains(t, kg.store.DB(), "hunter2") {
		t.Fatal("sealed copy leaked plaintext")
	}
}

// #2: erasing a subject drops saved answers that quote its values.
func TestReview_EraseDropsAnswersQuotingSealedValues(t *testing.T) {
	ctx := context.Background()
	kg := sensitivePalace(t)
	q := `SELECT ?who WHERE { ?who <secret> "hunter2-password" }`
	if _, err := kg.SaveAnswer(ctx, "quote", "sparql", q, kg.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := kg.SaveAnswer(ctx, "other", "sparql", `SELECT ?x WHERE { ?x <likes> ?y }`, kg.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := kg.Erase(ctx, TxOptions{}, "alice"); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := kg.store.DB().QueryRow("SELECT count(*) FROM kg_saved_answer WHERE instr(query, 'hunter2') > 0").Scan(&n); err != nil || n != 0 {
		t.Fatalf("query with an erased value survived: n=%d err=%v", n, err)
	}
	if _, err := kg.CheckAnswer(ctx, "other"); err != nil {
		t.Fatalf("unrelated answer dropped: %v", err)
	}
}

// #3: a predicate with volatile plaintext cannot become sensitive.
func TestReview_SensitiveRejectsVolatilePlaintext(t *testing.T) {
	kg, _ := newEngine(t)
	tx(t, kg, func(w *KGTx) error { return w.SetVolatile(Ent("alice"), Ent("pin"), Str("1234")) })
	mustErr(t, txErr(kg, func(w *KGTx) error {
		return w.SetPredicate(Ent("pin"), PredicateSchema{Sensitive: true})
	}), ErrSchemaViolation)
}

// #4: X = (f X) fails by the occurs check instead of looping.
func TestReview_ElpiOccursCheck(t *testing.T) {
	kg, _ := newEngine(t)
	prog, _ := ParseElpi("p X :- X = (f X).")
	res, err := kg.Now().Elpi(context.Background(), prog, "p Y", ElpiOptions{})
	if err != nil || len(res.Rows) != 0 {
		t.Fatalf("cyclic unification = %+v, %v", res, err)
	}
	res, err = kg.Now().Elpi(context.Background(), prog, "X = (f X)", ElpiOptions{})
	if err != nil || len(res.Rows) != 0 {
		t.Fatalf("goal cyclic unification = %+v, %v", res, err)
	}
}

// #5: a Cartesian product of bare nodes is charged to the budget.
func TestReview_CypherCartesianIsBudgeted(t *testing.T) {
	kg, _ := newEngine(t)
	for i := range 12 {
		assert1(t, kg, Ent("n"+string(rune('a'+i))), Ent("links"), Ent("hub"), Valid{})
	}
	_, err := kg.Now().RunQuery(context.Background(), "cypher", "MATCH (a),(b),(c),(d),(e),(f) RETURN a LIMIT 1",
		QueryOptions{Budget: Budget{MaxRows: 10_000}})
	mustErr(t, err, ErrBudget)
}

// #6: a GRAPH clause makes a saved answer depend on membership.
func TestReview_SavedGraphClauseTracksMembership(t *testing.T) {
	ctx := context.Background()
	kg, _ := newEngine(t)
	e := assert1(t, kg, Ent("alice"), Ent("works_on"), Ent("auth"), Valid{})
	if _, err := kg.SaveAnswer(ctx, "g", "sparql", `SELECT ?w WHERE { GRAPH <team> { ?w <works_on> <auth> } }`, kg.Now()); err != nil {
		t.Fatal(err)
	}
	tx(t, kg, func(w *KGTx) error { _, _, err := w.AddToGraph(e, "team"); return err })
	if ck, _ := kg.CheckAnswer(ctx, "g"); ck.Stale == "" {
		t.Fatal("membership change left a GRAPH answer fresh")
	}
}

// #7: retracting a row skipped by OFFSET stales the answer.
func TestReview_SavedOffsetIsNonMonotonic(t *testing.T) {
	ctx := context.Background()
	kg, _ := newEngine(t)
	a := assert1(t, kg, Ent("alice"), Ent("role"), Ent("dev"), Valid{})
	assert1(t, kg, Ent("bob"), Ent("role"), Ent("dev"), Valid{})
	assert1(t, kg, Ent("carol"), Ent("role"), Ent("dev"), Valid{})
	q := `SELECT ?w WHERE { ?w <role> <dev> } ORDER BY ?w LIMIT 1 OFFSET 1`
	saved, err := kg.SaveAnswer(ctx, "second", "sparql", q, kg.Now())
	if err != nil || saved.Rows[0]["w"] != Ent("bob") {
		t.Fatalf("save = %+v, %v", saved, err)
	}
	tx(t, kg, func(w *KGTx) error { _, err := w.Retract(a); return err })
	if ck, _ := kg.CheckAnswer(ctx, "second"); ck.Stale == "" {
		t.Fatal("OFFSET answer stayed fresh after a skipped row was retracted")
	}
}

// #8: save_as is subject to the same row cap as a plain query.
func TestReview_SelectSaveAsRespectsMaxRows(t *testing.T) {
	p := newToolPalace(t)
	ctx := mockToolCtx{Context: context.Background()}
	palaceKGAddHandler(ctx, p, KGAddToolInput{Subject: "a", Predicate: "r", Object: "x"})
	palaceKGAddHandler(ctx, p, KGAddToolInput{Subject: "b", Predicate: "r", Object: "x"})
	out, _ := palaceKGSelectHandler(ctx, p, KGSelectToolInput{Query: "SELECT ?s WHERE { ?s <r> <x> }", MaxRows: 1, SaveAs: "big"})
	mustContain(t, out.Content, "Error:")
	if list, _ := p.KG().ListAnswers(context.Background()); len(list) != 0 {
		t.Fatal("over-cap answer was saved")
	}
}

// #9: an object-only literal pattern finds sealed objects.
func TestReview_ObjectOnlyMatchFindsSealed(t *testing.T) {
	kg := sensitivePalace(t)
	rows, err := kg.Now().RunQuery(context.Background(), "sparql", `SELECT ?s ?p WHERE { ?s ?p "hunter2-password" }`, QueryOptions{})
	if err != nil || len(rows.Rows) != 1 || rows.Rows[0]["s"] != Ent("alice") {
		t.Fatalf("object-only match = %+v, %v", rows, err)
	}
}

// #10: a graph-scoped bundle leaves out statements outside the graph.
func TestReview_BundleRespectsGraph(t *testing.T) {
	ctx := context.Background()
	kg, _ := newEngine(t)
	root := assert1(t, kg, Ent("alice"), Ent("works_on"), Ent("auth"), Valid{})
	tx(t, kg, func(w *KGTx) error { _, _, err := w.AddToGraph(root, "team"); return err })
	outside := assert1(t, kg, Ent("bob"), Ent("believes"), StmtRef(root), Valid{})
	b, err := kg.Now().InGraph("team").Bundle(ctx, root, BundleOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if data := strings.ToLower(bundleText(b)); strings.Contains(data, "believes") {
		t.Fatalf("bundle exported e%d from outside the graph: %s", outside, data)
	}
}

func bundleText(b *Bundle) string {
	var sb strings.Builder
	var walk func(s BundleStmt)
	walk = func(s BundleStmt) {
		sb.WriteString(s.S.Text + " " + s.P.Text + " " + s.O.Text + ";")
		if s.O.Stmt != nil {
			walk(*s.O.Stmt)
		}
		for _, l := range s.Layers {
			walk(l)
		}
	}
	walk(b.Root)
	return sb.String()
}

// #11: derived upsert on a sensitive predicate is refused, not duplicated.
func TestReview_UpsertDerivedRejectsSensitive(t *testing.T) {
	kg := sensitivePalace(t)
	e := assert1(t, kg, Ent("alice"), Ent("knows"), Ent("bob"), Valid{})
	mustErr(t, txErr(kg, func(w *KGTx) error {
		_, _, _, err := w.UpsertDerived(Ent("secret"), Str("k"), []int64{e})
		return err
	}), ErrSchemaViolation)
}

// #12: each _ in a goal is its own variable.
func TestReview_ElpiAnonymousGoalVariables(t *testing.T) {
	kg, _ := newEngine(t)
	assert1(t, kg, Ent("alice"), Ent("manager"), Ent("bob"), Valid{})
	prog, _ := ParseElpi("")
	res, err := kg.Now().Elpi(context.Background(), prog, "tm.triple _ manager _", ElpiOptions{})
	if err != nil || len(res.Rows) != 1 || len(res.Rows[0]) != 0 {
		t.Fatalf("anonymous goal = %+v, %v", res, err)
	}
}

// #13: a To-only time-respecting search is refused rather than wrong.
func TestReview_ToOnlyTimeRespectingRejected(t *testing.T) {
	kg, _ := newEngine(t)
	assert1(t, kg, Ent("a"), Ent("r"), Ent("b"), Valid{From: day("2026-01-10"), To: day("2026-01-20")})
	assert1(t, kg, Ent("b"), Ent("r"), Ent("c"), Valid{From: day("2026-01-01"), To: day("2026-01-05")})
	if _, err := kg.Now().Paths(context.Background(), PathQuery{To: "c", TimeRespecting: true}); err == nil {
		t.Fatal("To-only time-respecting search accepted")
	}
	// From the start the journey is correctly impossible: b→c ended before a→b began.
	paths, err := kg.Now().Paths(context.Background(), PathQuery{From: "a", To: "c", TimeRespecting: true})
	if err != nil || len(paths) != 0 {
		t.Fatalf("forward = %+v, %v", paths, err)
	}
}

// #14: a value type that sealed values break is rejected.
func TestReview_ValueTypeChecksSealedKinds(t *testing.T) {
	kg := sensitivePalace(t)
	mustErr(t, txErr(kg, func(w *KGTx) error {
		return w.SetPredicate(Ent("secret"), PredicateSchema{Sensitive: true, ValueType: "int"})
	}), ErrSchemaViolation)
	if err := txErr(kg, func(w *KGTx) error {
		return w.SetPredicate(Ent("secret"), PredicateSchema{Sensitive: true, ValueType: "string"})
	}); err != nil {
		t.Fatalf("matching value type rejected: %v", err)
	}
}
