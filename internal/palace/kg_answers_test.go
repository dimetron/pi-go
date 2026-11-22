package palace

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func counters(t *testing.T, kg *KnowledgeGraph) [3]int {
	t.Helper()
	var c [3]int
	db := kg.store.DB()
	for i, q := range []string{"SELECT count(*) FROM kg_tx", "SELECT count(*) FROM kg_stmt", "SELECT count(*) FROM kg_term"} {
		if err := db.QueryRow(q).Scan(&c[i]); err != nil {
			t.Fatal(err)
		}
	}
	return c
}

const qWorks = `SELECT ?who WHERE { ?who <works_on> <auth> }`

func TestAnswers_StaleOnRetractAndNewRows(t *testing.T) {
	ctx := context.Background()
	kg, _ := newEngine(t)
	a := assert1(t, kg, Ent("alice"), Ent("works_on"), Ent("auth"), Valid{})
	assert1(t, kg, Ent("alice"), Ent("likes"), Ent("tea"), Valid{})

	before := counters(t, kg)
	saved, err := kg.SaveAnswer(ctx, "auth-team", "sparql", qWorks, kg.Now())
	if err != nil || len(saved.Rows) != 1 || saved.Rows[0]["who"] != Ent("alice") {
		t.Fatalf("save = %+v, %v", saved, err)
	}
	// An unrelated predicate keeps it fresh, and the checkpoint advances.
	assert1(t, kg, Ent("bob"), Ent("likes"), Ent("coffee"), Valid{})
	ck, err := kg.CheckAnswer(ctx, "auth-team")
	if err != nil || ck.Stale != "" || ck.Checkpoint <= saved.Checkpoint {
		t.Fatalf("unrelated write = %+v, %v", ck, err)
	}
	// A new matching row stales it although every dependency is still live.
	assert1(t, kg, Ent("bob"), Ent("works_on"), Ent("auth"), Valid{})
	ck, _ = kg.CheckAnswer(ctx, "auth-team")
	if !strings.Contains(ck.Stale, "may add rows") {
		t.Fatalf("new row: stale = %q", ck.Stale)
	}
	// Checking again gives the same verdict; only a refresh clears it.
	if again, _ := kg.CheckAnswer(ctx, "auth-team"); again.Stale != ck.Stale || again.Checkpoint != ck.Checkpoint {
		t.Fatal("check is not idempotent")
	}
	ref, err := kg.RefreshAnswer(ctx, "auth-team")
	if err != nil || len(ref.Rows) != 2 || ref.Stale != "" {
		t.Fatalf("refresh = %+v, %v", ref, err)
	}
	// A retracted support stales it with the triggering event.
	tx(t, kg, func(w *KGTx) error { _, err := w.Retract(a); return err })
	ck, _ = kg.CheckAnswer(ctx, "auth-team")
	if !strings.Contains(ck.Stale, "retracted") {
		t.Fatalf("retract: stale = %q", ck.Stale)
	}
	// Answers are derived records: save/check/refresh/delete wrote no history
	// beyond the test's own four transactions.
	after := counters(t, kg)
	if after[0] != before[0]+3 {
		t.Fatalf("transactions %d -> %d", before[0], after[0])
	}
	if err := kg.DeleteAnswer(ctx, "auth-team"); err != nil {
		t.Fatal(err)
	}
	if _, err := kg.CheckAnswer(ctx, "auth-team"); !errors.Is(err, ErrNoAnswer) {
		t.Fatalf("deleted answer = %v", err)
	}
	if c := counters(t, kg); c != after {
		t.Fatalf("delete changed history: %v -> %v", after, c)
	}
}

func TestAnswers_NegativeQueryStaleOnRetraction(t *testing.T) {
	ctx := context.Background()
	kg, _ := newEngine(t)
	assert1(t, kg, Ent("alice"), Ent("role"), Ent("dev"), Valid{})
	block := assert1(t, kg, Ent("alice"), Ent("blocked"), Bool(true), Valid{})
	q := `SELECT ?who WHERE { ?who <role> <dev> FILTER NOT EXISTS { ?who <blocked> true } }`
	saved, err := kg.SaveAnswer(ctx, "unblocked", "sparql", q, kg.Now())
	if err != nil || len(saved.Rows) != 0 {
		t.Fatalf("save = %+v, %v", saved, err)
	}
	tx(t, kg, func(w *KGTx) error { _, err := w.Retract(block); return err })
	ck, _ := kg.CheckAnswer(ctx, "unblocked")
	if !strings.Contains(ck.Stale, "may change rows") {
		t.Fatalf("removing a blocker left an empty answer fresh: %+v", ck)
	}
}

func TestAnswers_FixedAsOfAndFrozenFuture(t *testing.T) {
	ctx := context.Background()
	kg, _ := newEngine(t)
	a := assert1(t, kg, Ent("alice"), Ent("works_on"), Ent("auth"), Valid{})
	t1, _ := lastTOf(kg)
	if _, err := kg.SaveAnswer(ctx, "past", "cypher", "MATCH (a)-[:works_on]->(b) RETURN a, b", kg.AsOf(t1)); err != nil {
		t.Fatal(err)
	}
	future, err := kg.SaveAnswer(ctx, "future", "sparql", qWorks, kg.AsOf(t1+5))
	if err != nil || !strings.Contains(future.View, "tx1") {
		t.Fatalf("future bound not frozen: %+v, %v", future, err)
	}
	tx(t, kg, func(w *KGTx) error { _, err := w.Retract(a); return err })
	assert1(t, kg, Ent("bob"), Ent("works_on"), Ent("auth"), Valid{})
	for _, name := range []string{"past", "future"} {
		ck, err := kg.CheckAnswer(ctx, name)
		if err != nil || ck.Stale != "" || len(ck.Rows) != 1 {
			t.Fatalf("%s: as-of answer changed: %+v, %v", name, ck, err)
		}
	}
}

func TestAnswers_GraphMembershipAndErase(t *testing.T) {
	ctx := context.Background()
	kg, _ := newEngine(t)
	tx(t, kg, func(w *KGTx) error { return w.SetPredicate(Ent("email"), PredicateSchema{Sensitive: true}) })
	e := assert1(t, kg, Ent("alice"), Ent("email"), Str("alice@secret.example"), Valid{})
	tx(t, kg, func(w *KGTx) error { _, _, err := w.AddToGraph(e, "crm"); return err })
	saved, err := kg.SaveAnswer(ctx, "mails", "sparql", `SELECT ?m WHERE { <alice> <email> ?m }`, kg.Now().InGraph("crm"))
	if err != nil || len(saved.Rows) != 1 || saved.Rows[0]["m"] != Str(sealedPlaceholder) {
		t.Fatalf("sealed value stored in a saved answer: %+v, %v", saved, err)
	}
	var stored string
	if err := kg.store.DB().QueryRow("SELECT result FROM kg_saved_answer").Scan(&stored); err != nil ||
		strings.Contains(stored, "secret") {
		t.Fatalf("plaintext in kg_saved_answer: %s, %v", stored, err)
	}
	// Membership-only changes stale a graph-scoped answer.
	tx(t, kg, func(w *KGTx) error { _, err := w.RemoveFromGraph(e, "crm"); return err })
	if ck, _ := kg.CheckAnswer(ctx, "mails"); !strings.Contains(ck.Stale, "graph membership") {
		t.Fatalf("membership change: %+v", ck)
	}
	if _, err := kg.RefreshAnswer(ctx, "mails"); err != nil {
		t.Fatal(err)
	}
	// Erasure stales every answer and drops its rows.
	if _, err := kg.Erase(ctx, TxOptions{}, "alice"); err != nil {
		t.Fatal(err)
	}
	ck, _ := kg.CheckAnswer(ctx, "mails")
	if !strings.Contains(ck.Stale, "erased") || len(ck.Rows) != 0 {
		t.Fatalf("after erase = %+v", ck)
	}
	if list, _ := kg.ListAnswers(ctx); len(list) != 1 {
		t.Fatalf("list = %+v", list)
	}
}

func TestAnswers_FailedRefreshKeepsStale(t *testing.T) {
	ctx := context.Background()
	kg, db := newEngine(t)
	assert1(t, kg, Ent("alice"), Ent("works_on"), Ent("auth"), Valid{})
	if _, err := kg.SaveAnswer(ctx, "a", "sparql", qWorks, kg.Now()); err != nil {
		t.Fatal(err)
	}
	assert1(t, kg, Ent("bob"), Ent("works_on"), Ent("auth"), Valid{})
	ck, _ := kg.CheckAnswer(ctx, "a")
	if _, err := db.Exec("UPDATE kg_saved_answer SET query = 'SELECT nonsense'"); err != nil {
		t.Fatal(err)
	}
	if _, err := kg.RefreshAnswer(ctx, "a"); err == nil {
		t.Fatal("refresh of a broken query succeeded")
	}
	after, _ := kg.CheckAnswer(ctx, "a")
	if after.Stale != ck.Stale || after.Checkpoint != ck.Checkpoint {
		t.Fatalf("failed refresh changed status: %+v -> %+v", ck, after)
	}
}
