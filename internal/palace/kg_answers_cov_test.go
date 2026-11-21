package palace

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
)

func TestAnswersValidation(t *testing.T) {
	ctx := context.Background()
	kg, _ := newEngine(t)
	assert1(t, kg, Ent("alice"), Ent("works_on"), Ent("auth"), Valid{})
	before := counters(t, kg)

	if _, err := kg.SaveAnswer(ctx, "  ", "sparql", qWorks, kg.Now()); err == nil || !strings.Contains(err.Error(), "needs a name") {
		t.Fatalf("blank name: %v", err)
	}
	if _, err := kg.SaveAnswer(ctx, "a", "gremlin", qWorks, kg.Now()); err == nil || !strings.Contains(err.Error(), `"gremlin"`) {
		t.Fatalf("bad lang: %v", err)
	}
	if _, err := kg.SaveAnswer(ctx, "a", "sparql", "SELECT nonsense", kg.Now()); !errors.Is(err, ErrQueryParse) {
		t.Fatalf("bad query: %v", err)
	}
	if _, err := kg.SaveAnswer(ctx, "a", "sparql", `CONSTRUCT {} WHERE {}`, kg.Now()); !errors.Is(err, ErrUnsupportedQuery) {
		t.Fatalf("unsupported query: %v", err)
	}
	// Nothing was saved by the failures.
	if list, err := kg.ListAnswers(ctx); err != nil || len(list) != 0 {
		t.Fatalf("list after failures = %v, %v", list, err)
	}
	for name, call := range map[string]func() error{
		"check":   func() error { _, err := kg.CheckAnswer(ctx, "nope"); return err },
		"refresh": func() error { _, err := kg.RefreshAnswer(ctx, "nope"); return err },
	} {
		if err := call(); !errors.Is(err, ErrNoAnswer) || !strings.Contains(err.Error(), "nope") {
			t.Fatalf("%s unknown: %v", name, err)
		}
	}
	// Deleting an unknown answer succeeds.
	if err := kg.DeleteAnswer(ctx, "nope"); err != nil {
		t.Fatal(err)
	}
	// Saved answers are derived: none of this wrote a transaction.
	if after := counters(t, kg); after != before {
		t.Fatalf("counters %v -> %v", before, after)
	}
}

func TestAnswersListReplaceDelete(t *testing.T) {
	ctx := context.Background()
	kg, _ := newEngine(t)
	assert1(t, kg, Ent("alice"), Ent("works_on"), Ent("auth"), Valid{})
	if list, err := kg.ListAnswers(ctx); err != nil || len(list) != 0 {
		t.Fatalf("empty list = %v, %v", list, err)
	}
	if _, err := kg.SaveAnswer(ctx, "zeta", "cypher", `MATCH (a)-[:works_on]->(b) RETURN a, b`, kg.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := kg.SaveAnswer(ctx, "alpha", "SPARQL", qWorks, kg.Now()); err != nil {
		t.Fatal(err)
	}
	list, err := kg.ListAnswers(ctx)
	if err != nil || len(list) != 2 || list[0].Name != "alpha" || list[1].Name != "zeta" {
		t.Fatalf("list = %+v, %v", list, err)
	}
	if list[1].Lang != "cypher" || len(list[1].Rows) != 1 || list[1].Rows[0]["b"] != Ent("auth") {
		t.Fatalf("cypher answer = %+v", list[1])
	}
	// Saving under an existing name replaces it and clears a stale mark.
	assert1(t, kg, Ent("bob"), Ent("works_on"), Ent("auth"), Valid{})
	if ck, _ := kg.CheckAnswer(ctx, "alpha"); ck.Stale == "" {
		t.Fatal("expected stale")
	}
	re, err := kg.SaveAnswer(ctx, "alpha", "cypher", `MATCH (a)-[:works_on]->(b) RETURN a`, kg.Now())
	if err != nil || len(re.Rows) != 2 || re.Lang != "cypher" || re.Stale != "" {
		t.Fatalf("replace = %+v, %v", re, err)
	}
	ck, err := kg.CheckAnswer(ctx, "alpha")
	if err != nil || ck.Stale != "" || ck.Lang != "cypher" {
		t.Fatalf("check replaced = %+v, %v", ck, err)
	}
	// Refresh keeps the language of the saved answer.
	rf, err := kg.RefreshAnswer(ctx, "zeta")
	if err != nil || rf.Lang != "cypher" || len(rf.Rows) != 2 {
		t.Fatalf("refresh cypher = %+v, %v", rf, err)
	}
	if err := kg.DeleteAnswer(ctx, "alpha"); err != nil {
		t.Fatal(err)
	}
	if _, err := kg.CheckAnswer(ctx, "alpha"); !errors.Is(err, ErrNoAnswer) {
		t.Fatalf("check deleted: %v", err)
	}
	if list, _ := kg.ListAnswers(ctx); len(list) != 1 || list[0].Name != "zeta" {
		t.Fatalf("list after delete = %+v", list)
	}
}

func TestAnswersPredicateBranches(t *testing.T) {
	ctx := context.Background()
	kg, _ := newEngine(t)
	r := assert1(t, kg, Ent("alice"), Ent("works_on"), Ent("auth"), Valid{})
	assert1(t, kg, Ent("alice"), Ent("likes"), Ent("tea"), Valid{})

	// A variable predicate reads every predicate: any assertion stales it.
	if _, err := kg.SaveAnswer(ctx, "any", "sparql", `SELECT ?o WHERE { <alice> ?p ?o . FILTER(?o = <tea>) }`, kg.Now()); err != nil {
		t.Fatal(err)
	}
	var preds string
	if _, err := kgDB(kg).Exec(`UPDATE kg_saved_answer SET deps = '[]' WHERE name = 'any'`); err != nil {
		t.Fatal(err)
	}
	if err := kgDB(kg).QueryRow(`SELECT preds FROM kg_saved_answer WHERE name = 'any'`).Scan(&preds); err != nil || preds != "null" {
		t.Fatalf("preds = %q, %v", preds, err)
	}
	assert1(t, kg, Ent("zed"), Ent("anything"), Ent("else"), Valid{})
	ck, err := kg.CheckAnswer(ctx, "any")
	if err != nil || !strings.Contains(ck.Stale, "may add rows") {
		t.Fatalf("any predicate: %+v, %v", ck, err)
	}

	// Predicates unknown at save time: unrelated writes leave it fresh and the
	// checkpoint moves; asserting the predicate stales it.
	if _, err := kg.SaveAnswer(ctx, "ghost", "sparql", `SELECT ?x WHERE { ?x <haunts> <house> }`, kg.Now()); err != nil {
		t.Fatal(err)
	}
	assert1(t, kg, Ent("carol"), Ent("likes"), Ent("jam"), Valid{})
	ck, err = kg.CheckAnswer(ctx, "ghost")
	if err != nil || ck.Stale != "" {
		t.Fatalf("ghost unrelated: %+v, %v", ck, err)
	}
	saved, _ := kg.CheckAnswer(ctx, "ghost")
	assert1(t, kg, Ent("casper"), Ent("haunts"), Ent("house"), Valid{})
	ck, err = kg.CheckAnswer(ctx, "ghost")
	if err != nil || !strings.Contains(ck.Stale, "may add rows") || ck.Checkpoint != saved.Checkpoint {
		t.Fatalf("ghost assert: %+v, %v", ck, err)
	}

	// A negative (OPTIONAL) query is staled by a retraction of a statement with
	// a predicate it reads, even though that statement is not a dependency.
	if _, err := kg.SaveAnswer(ctx, "opt", "sparql",
		`SELECT ?x ?t WHERE { ?x <works_on> <auth> . OPTIONAL { ?x <likes> ?t } }`, kg.Now()); err != nil {
		t.Fatal(err)
	}
	likes := assert1(t, kg, Ent("zoe"), Ent("likes"), Ent("fig"), Valid{})
	if ck, _ := kg.CheckAnswer(ctx, "opt"); !strings.Contains(ck.Stale, "may add rows") {
		t.Fatalf("opt assert: %+v", ck)
	}
	if _, err := kg.RefreshAnswer(ctx, "opt"); err != nil {
		t.Fatal(err)
	}
	tx(t, kg, func(w *KGTx) error { _, err := w.Retract(likes); return err })
	ck, err = kg.CheckAnswer(ctx, "opt")
	if err != nil || !strings.Contains(ck.Stale, "may change rows") {
		t.Fatalf("opt retract: %+v, %v", ck, err)
	}

	// The same retraction leaves a monotonic query fresh.
	if _, err := kg.SaveAnswer(ctx, "mono", "sparql", `SELECT ?x WHERE { ?x <works_on> <auth> }`, kg.Now()); err != nil {
		t.Fatal(err)
	}
	other := assert1(t, kg, Ent("yan"), Ent("likes"), Ent("kiwi"), Valid{})
	tx(t, kg, func(w *KGTx) error { _, err := w.Retract(other); return err })
	if ck, _ := kg.CheckAnswer(ctx, "mono"); ck.Stale != "" {
		t.Fatalf("mono: %+v", ck)
	}
	// Retracting its own support names the statement.
	tx(t, kg, func(w *KGTx) error { _, err := w.Retract(r); return err })
	if ck, _ := kg.CheckAnswer(ctx, "mono"); !strings.Contains(ck.Stale, "retracted at tx") {
		t.Fatalf("mono retract: %+v", ck)
	}
}

func TestAnswersGraphScopedFresh(t *testing.T) {
	ctx := context.Background()
	kg, _ := newEngine(t)
	e := assert1(t, kg, Ent("alice"), Ent("works_on"), Ent("auth"), Valid{})
	tx(t, kg, func(w *KGTx) error { _, _, err := w.AddToGraph(e, "crm"); return err })
	if _, err := kg.SaveAnswer(ctx, "g", "sparql", qWorks, kg.Now().InGraph("crm")); err != nil {
		t.Fatal(err)
	}
	// An unrelated write: no membership change, so still fresh.
	assert1(t, kg, Ent("bob"), Ent("likes"), Ent("tea"), Valid{})
	ck, err := kg.CheckAnswer(ctx, "g")
	if err != nil || ck.Stale != "" || !strings.Contains(ck.View, "crm") {
		t.Fatalf("graph fresh: %+v, %v", ck, err)
	}
	// Adding a statement to the graph is a membership change.
	e2 := assert1(t, kg, Ent("bob"), Ent("works_on"), Ent("auth"), Valid{})
	if ck, _ := kg.CheckAnswer(ctx, "g"); ck.Stale == "" {
		t.Fatalf("assert in graph: %+v", ck)
	}
	_ = e2
}

func TestAnswersCorruptRows(t *testing.T) {
	ctx := context.Background()
	kg, db := newEngine(t)
	assert1(t, kg, Ent("alice"), Ent("works_on"), Ent("auth"), Valid{})
	cases := []struct{ col, val, want string }{
		{"view", "not json", "answer view"},
		{"result", "not json", "answer result"},
		{"deps", "not json", "answer deps"},
		{"preds", "not json", "answer preds"},
	}
	for _, c := range cases {
		t.Run(c.col, func(t *testing.T) {
			if _, err := kg.SaveAnswer(ctx, "a", "sparql", qWorks, kg.Now()); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec("UPDATE kg_saved_answer SET "+c.col+" = ?", c.val); err != nil {
				t.Fatal(err)
			}
			for name, call := range map[string]func() error{
				"check":   func() error { _, err := kg.CheckAnswer(ctx, "a"); return err },
				"refresh": func() error { _, err := kg.RefreshAnswer(ctx, "a"); return err },
				"list":    func() error { _, err := kg.ListAnswers(ctx); return err },
			} {
				if err := call(); err == nil || !strings.Contains(err.Error(), c.want) {
					t.Fatalf("%s with bad %s: %v", name, c.col, err)
				}
			}
		})
	}
	// A stored result of "null" is an empty answer, not an error.
	if _, err := kg.SaveAnswer(ctx, "n", "sparql", qWorks, kg.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE kg_saved_answer SET result = 'null' WHERE name = 'n'`); err != nil {
		t.Fatal(err)
	}
	if a, err := kg.CheckAnswer(ctx, "n"); err != nil || len(a.Rows) != 0 {
		t.Fatalf("null result: %+v, %v", a, err)
	}
}

func TestAnswersStorageFailures(t *testing.T) {
	ctx := context.Background()

	t.Run("write blocked", func(t *testing.T) {
		kg, db := newEngine(t)
		assert1(t, kg, Ent("alice"), Ent("works_on"), Ent("auth"), Valid{})
		if _, err := kg.SaveAnswer(ctx, "a", "sparql", qWorks, kg.Now()); err != nil {
			t.Fatal(err)
		}
		for _, trg := range []string{
			`CREATE TRIGGER no_ins BEFORE INSERT ON kg_saved_answer BEGIN SELECT RAISE(ABORT, 'insert blocked'); END`,
			`CREATE TRIGGER no_upd BEFORE UPDATE ON kg_saved_answer BEGIN SELECT RAISE(ABORT, 'update blocked'); END`,
			`CREATE TRIGGER no_del BEFORE DELETE ON kg_saved_answer BEGIN SELECT RAISE(ABORT, 'delete blocked'); END`,
		} {
			if _, err := db.Exec(trg); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := kg.SaveAnswer(ctx, "b", "sparql", qWorks, kg.Now()); err == nil || !strings.Contains(err.Error(), "save answer") {
			t.Fatalf("save: %v", err)
		}
		if _, err := kg.RefreshAnswer(ctx, "a"); err == nil || !strings.Contains(err.Error(), "save answer") {
			t.Fatalf("refresh: %v", err)
		}
		if err := kg.DeleteAnswer(ctx, "a"); err == nil || !strings.Contains(err.Error(), "delete answer") {
			t.Fatalf("delete: %v", err)
		}
		// A check that must record a verdict fails when the update is blocked.
		assert1(t, kg, Ent("bob"), Ent("works_on"), Ent("auth"), Valid{})
		if _, err := kg.CheckAnswer(ctx, "a"); err == nil || !strings.Contains(err.Error(), "check answer") {
			t.Fatalf("check stale write: %v", err)
		}
		// A fresh check also advances the checkpoint, which is blocked too.
		if _, err := kg.SaveAnswer(ctx, "c", "sparql", `SELECT ?x WHERE { ?x <nothing> <here> }`, kg.Now()); err == nil {
			t.Fatal("insert should stay blocked")
		}
	})

	t.Run("fresh checkpoint write blocked", func(t *testing.T) {
		kg, db := newEngine(t)
		assert1(t, kg, Ent("alice"), Ent("works_on"), Ent("auth"), Valid{})
		if _, err := kg.SaveAnswer(ctx, "a", "sparql", qWorks, kg.Now()); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`CREATE TRIGGER no_upd BEFORE UPDATE ON kg_saved_answer BEGIN SELECT RAISE(ABORT, 'update blocked'); END`); err != nil {
			t.Fatal(err)
		}
		assert1(t, kg, Ent("bob"), Ent("likes"), Ent("tea"), Valid{})
		if _, err := kg.CheckAnswer(ctx, "a"); err == nil || !strings.Contains(err.Error(), "check answer") {
			t.Fatalf("check: %v", err)
		}
	})

	t.Run("table missing", func(t *testing.T) {
		kg, db := newEngine(t)
		assert1(t, kg, Ent("alice"), Ent("works_on"), Ent("auth"), Valid{})
		if _, err := db.Exec(`DROP TABLE kg_saved_answer`); err != nil {
			t.Fatal(err)
		}
		if _, err := kg.SaveAnswer(ctx, "a", "sparql", qWorks, kg.Now()); err == nil || !strings.Contains(err.Error(), "save answer") {
			t.Fatalf("save: %v", err)
		}
		if _, err := kg.CheckAnswer(ctx, "a"); err == nil || !strings.Contains(err.Error(), "load answer") {
			t.Fatalf("check: %v", err)
		}
		if _, err := kg.RefreshAnswer(ctx, "a"); err == nil || !strings.Contains(err.Error(), "load answer") {
			t.Fatalf("refresh: %v", err)
		}
		if err := kg.DeleteAnswer(ctx, "a"); err == nil || !strings.Contains(err.Error(), "delete answer") {
			t.Fatalf("delete: %v", err)
		}
		if _, err := kg.ListAnswers(ctx); err == nil || !strings.Contains(err.Error(), "list answers") {
			t.Fatalf("list: %v", err)
		}
	})

	t.Run("closed database", func(t *testing.T) {
		kg, db := newEngine(t)
		assert1(t, kg, Ent("alice"), Ent("works_on"), Ent("auth"), Valid{})
		_ = db.Close()
		if _, err := kg.SaveAnswer(ctx, "a", "sparql", qWorks, kg.Now()); err == nil || !strings.Contains(err.Error(), "begin") {
			t.Fatalf("save: %v", err)
		}
		if _, err := kg.CheckAnswer(ctx, "a"); err == nil {
			t.Fatal("check on a closed database succeeded")
		}
		if err := kg.DeleteAnswer(ctx, "a"); err == nil {
			t.Fatal("delete on a closed database succeeded")
		}
	})

	t.Run("statements unreadable", func(t *testing.T) {
		kg, db := newEngine(t)
		assert1(t, kg, Ent("alice"), Ent("works_on"), Ent("auth"), Valid{})
		// No dependencies: the retraction scan has nothing to read, so the
		// predicate scan is the first to touch kg_stmt.
		if _, err := kg.SaveAnswer(ctx, "empty", "sparql", `SELECT ?x WHERE { ?x <works_on> <zzz> }`, kg.Now()); err != nil {
			t.Fatal(err)
		}
		if _, err := kg.SaveAnswer(ctx, "full", "sparql", qWorks, kg.Now()); err != nil {
			t.Fatal(err)
		}
		assert1(t, kg, Ent("bob"), Ent("likes"), Ent("tea"), Valid{})
		if _, err := db.Exec(`ALTER TABLE kg_stmt RENAME TO kg_stmt_gone`); err != nil {
			t.Skipf("cannot rename kg_stmt: %v", err)
		}
		for _, name := range []string{"empty", "full"} {
			if _, err := kg.CheckAnswer(ctx, name); err == nil || !strings.Contains(err.Error(), "check answer") {
				t.Fatalf("%s: %v", name, err)
			}
		}
		// The query itself cannot run, so a refresh fails before any write.
		if _, err := kg.RefreshAnswer(ctx, "full"); err == nil {
			t.Fatal("refresh without statements succeeded")
		}
	})

	t.Run("watermark unreadable", func(t *testing.T) {
		kg, db := newEngine(t)
		assert1(t, kg, Ent("alice"), Ent("works_on"), Ent("auth"), Valid{})
		if _, err := kg.SaveAnswer(ctx, "a", "sparql", qWorks, kg.Now()); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`ALTER TABLE kg_meta RENAME TO kg_meta_gone`); err != nil {
			t.Skipf("cannot rename kg_meta: %v", err)
		}
		if _, err := kg.CheckAnswer(ctx, "a"); err == nil {
			t.Fatal("check without the watermark succeeded")
		}
		if _, err := kg.RefreshAnswer(ctx, "a"); err == nil {
			t.Fatal("refresh without the watermark succeeded")
		}
		if _, err := kg.SaveAnswer(ctx, "b", "sparql", qWorks, kg.Now()); err == nil {
			t.Fatal("save without the watermark succeeded")
		}
	})
}

// kgDB is the database behind a test engine.
func kgDB(kg *KnowledgeGraph) *sql.DB { return kg.store.DB() }
