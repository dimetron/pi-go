package palace

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"path/filepath"
	"strings"
	"testing"
)

func bs(id, s, p, o string, layers ...BundleStmt) BundleStmt {
	return BundleStmt{ID: id, S: BundleValue{Value: Ent(s)}, P: BundleValue{Value: sysPred(p)}, O: BundleValue{Value: Ent(o)}, Layers: layers}
}

func TestKGCov_ImportBundleRejections(t *testing.T) {
	kg, _ := newEngine(t)
	tx(t, kg, func(w *KGTx) error { return w.SetPredicate(Ent("email"), PredicateSchema{Sensitive: true}) })
	sens := func(v Value) BundleValue { return BundleValue{Value: v, Sensitive: true} }
	root := func(s BundleStmt) *Bundle { return &Bundle{Version: BundleVersion, Root: s} }

	withO := func(o BundleValue) BundleStmt {
		b := bs("b1", "a", "likes", "x")
		b.O = o
		return b
	}
	withS := func(s BundleValue) BundleStmt {
		b := bs("b1", "a", "likes", "x")
		b.S = s
		return b
	}
	withP := func(p BundleValue) BundleStmt {
		b := bs("b1", "a", "likes", "x")
		b.P = p
		return b
	}
	for name, tc := range map[string]struct {
		b    *Bundle
		want error  // errors.Is target, or nil
		text string // substring otherwise
	}{
		"nil bundle":         {nil, nil, "unsupported bundle version"},
		"bad version":        {&Bundle{Version: BundleVersion + 1, Root: bs("b1", "a", "p", "o")}, nil, "unsupported bundle version"},
		"no id":              {root(bs("", "a", "p", "o")), nil, "no id"},
		"duplicate id":       {root(bs("b1", "a", "p", "o", bs("b1", "a", "p", "o2"))), nil, "used twice"},
		"unknown ref":        {root(withO(BundleValue{Ref: "b9"})), ErrBundleCycle, ""},
		"stmt and ref":       {root(withO(BundleValue{Ref: "b1", Stmt: &BundleStmt{}})), nil, "both stmt and ref"},
		"reserved predicate": {root(withP(BundleValue{Value: sysPred(predSupersedes)})), ErrReservedPredicate, ""},
		"literal predicate":  {root(withP(BundleValue{Value: Str("p")})), nil, "predicate must be an entity"},
		"stmt predicate":     {root(withP(BundleValue{Ref: "b1"})), nil, "predicate must be an entity"},
		"literal subject":    {root(withS(BundleValue{Value: Str("lit")})), nil, "subject must be an entity or a statement"},
		"unusable kind":      {root(withO(BundleValue{Value: Value{Kind: ValueKind(99)}})), nil, "unusable kind"},
		"tx object kind":     {root(withO(BundleValue{Value: txVal(3)})), nil, "unusable kind"},
		"sensitive entity":   {root(withO(BundleValue{Value: Ent("x"), Sensitive: true})), nil, "only a literal object can be sensitive"},
		"sensitive subject":  {root(withS(sens(Str("lit")))), nil, "subject must be an entity or a statement"},
		"sensitive into plain": {root(func() BundleStmt {
			b := bs("b1", "a", "likes", "x")
			b.O = sens(Str("secret"))
			return b
		}()), ErrSchemaViolation, ""},
		"sensitive into unknown predicate": {root(func() BundleStmt {
			b := bs("b1", "a", "never_seen", "x")
			b.O = sens(Str("secret"))
			return b
		}()), ErrSchemaViolation, ""},
		"empty interval": {root(func() BundleStmt {
			b := bs("b1", "a", "likes", "x")
			b.Valid = &BundleValid{From: day("2025-01-01"), To: day("2024-01-01")}
			return b
		}()), ErrEmptyInterval, ""},
		"bad layer": {root(bs("b1", "a", "likes", "x", bs("", "a", "p", "o"))), nil, "no id"},
		"membership shape": {root(func() BundleStmt {
			b := bs("b1", "a", predInGraph, "g")
			return b
		}()), nil, "a membership is"},
		"membership engine subject": {root(withP(BundleValue{Value: sysPred(predUnique)})), ErrReservedPredicate, ""},
	} {
		t.Run(name, func(t *testing.T) {
			before := countLive(t, kg)
			err := txErr(kg, func(w *KGTx) error { _, err := w.ImportBundle(tc.b); return err })
			if err == nil {
				t.Fatal("imported")
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if tc.text != "" && !strings.Contains(err.Error(), tc.text) {
				t.Fatalf("err = %v, want text %q", err, tc.text)
			}
			if countLive(t, kg) != before {
				t.Fatal("a rejected bundle wrote statements")
			}
		})
	}
}

func TestKGCov_BundleRoundTripShapes(t *testing.T) {
	ctx := context.Background()
	kg, _ := newEngine(t)
	tx(t, kg, func(w *KGTx) error { return w.SetPredicate(Ent("email"), PredicateSchema{Sensitive: true}) })
	var e, mail int64
	tx(t, kg, func(w *KGTx) error {
		var err error
		if e, _, err = w.Assert(Ent("alice"), Ent("knows"), Ent("bob"), Valid{From: day("2020-01-01")}); err != nil {
			return err
		}
		if mail, _, err = w.Assert(Ent("alice"), Ent("email"), Str("a@secret.example"), Valid{}); err != nil {
			return err
		}
		if _, _, err = w.Annotate(e, Ent("note"), Str("n")); err != nil {
			return err
		}
		_, _, err = w.AddToGraph(e, "g")
		return err
	})
	// Missing and engine-only roots.
	if _, err := kg.Now().Bundle(ctx, 9999, BundleOptions{}); !errors.Is(err, ErrNotLive) {
		t.Errorf("Bundle of a missing statement: %v", err)
	}
	if _, err := kg.Now().InGraph("other").Bundle(ctx, e, BundleOptions{}); !errors.Is(err, ErrNotLive) {
		t.Errorf("Bundle outside its graph: %v", err)
	}
	// A sealed value needs IncludeSensitive.
	if _, err := kg.Now().Bundle(ctx, mail, BundleOptions{}); !errors.Is(err, ErrSensitiveBundle) {
		t.Errorf("sealed bundle without opt-in: %v", err)
	}
	b, err := kg.Now().Bundle(ctx, mail, BundleOptions{IncludeSensitive: true})
	if err != nil || !b.Root.O.Sensitive || b.Root.O.Text != "a@secret.example" {
		t.Fatalf("sensitive bundle = %+v, %v", b, err)
	}
	// The sensitive bundle imports into a sensitive target and is sealed there.
	kg2, db2 := newEngine(t)
	tx(t, kg2, func(w *KGTx) error { return w.SetPredicate(Ent("email"), PredicateSchema{Sensitive: true}) })
	importBundle(t, kg2, b)
	if rawContains(t, db2, "secret") {
		t.Error("imported sensitive value reached the database in the clear")
	}
	// Importing twice is a no-op.
	n := countLive(t, kg2)
	importBundle(t, kg2, b)
	if countLive(t, kg2) != n {
		t.Error("second import added statements")
	}
	// The full bundle of e carries its note and graph membership.
	full, err := kg.Now().Bundle(ctx, e, BundleOptions{})
	if err != nil || len(full.Root.Layers) < 2 {
		t.Fatalf("bundle = %+v, %v", full, err)
	}
	kg3, _ := newEngine(t)
	importBundle(t, kg3, full)
	if gs, _ := kg3.Now().ListGraphs(ctx); len(gs) != 1 || gs[0].Name != "g" {
		t.Errorf("graph membership not imported: %+v", gs)
	}
	// A bundle whose root is an engine statement is refused.
	sup := tx(t, kg, func(w *KGTx) error { _, err := w.Supersede(e, Patch{Object: new(Ent("carl"))}); return err })
	_ = sup
	var supEID int64
	if ms, _ := kg.History().Match(ctx, Pattern{Predicate: new(sysPred(predSupersedes)), IncludeEngine: true}); len(ms) == 1 {
		supEID = ms[0].EID
	}
	if supEID == 0 {
		t.Fatal("no supersedes statement")
	}
	if _, err := kg.Now().Bundle(ctx, supEID, BundleOptions{}); !errors.Is(err, ErrReservedPredicate) {
		t.Errorf("bundle of an engine statement: %v", err)
	}
}

func TestKGCov_VectorEdges(t *testing.T) {
	ctx := context.Background()
	kg, _ := newEngine(t)
	e := assert1(t, kg, Ent("alice"), Ent("likes"), Ent("tea"), Valid{})
	assert1(t, kg, Ent("bob"), Ent("likes"), Ent("coffee"), Valid{})

	if _, err := kg.EmbedStatements(ctx, nil, "m", 1); !errors.Is(err, ErrChannelUnavailable) {
		t.Errorf("no embedder: %v", err)
	}
	if _, err := kg.EmbedStatements(ctx, newWordEmbedder(testWords), "  ", 1); err == nil {
		t.Error("blank model accepted")
	}
	if err := kg.PutVector(ctx, e, "", []float32{1}); err == nil {
		t.Error("blank model accepted by PutVector")
	}
	for name, emb := range map[string]Embedder{
		"embed error":  &covEmbedder{err: errors.New("boom")},
		"short result": &covEmbedder{drop: true},
		"nan vector":   &covEmbedder{vec: []float32{float32(math.NaN()), 1}},
		"zero vector":  &covEmbedder{vec: []float32{0, 0}},
	} {
		if _, err := kg.EmbedStatements(ctx, emb, "bad", 10); err == nil {
			t.Errorf("%s: backfill succeeded", name)
		}
	}
	if vecCount(t, kg, "bad") != 0 {
		t.Error("a failed backfill left vectors behind")
	}
	// Search with a default k, a wrong-sized query, a bad query vector, an
	// embedder error, and a miscounted result.
	good := newWordEmbedder(testWords)
	if _, err := kg.EmbedStatements(ctx, good, "m", 0); err != nil {
		t.Fatal(err)
	}
	if got, err := kg.Now().VectorSearch(ctx, good, "m", "tea", 0); err != nil || len(got) != 2 || got[0].Rank != 1 {
		t.Fatalf("default-k search = %v, %v", scoredEIDs(got), err)
	}
	if _, err := kg.Now().VectorSearch(ctx, nil, "m", "tea", 3); !errors.Is(err, ErrChannelUnavailable) {
		t.Errorf("no embedder: %v", err)
	}
	if _, err := kg.Now().VectorSearch(ctx, good, "never-embedded", "tea", 3); !errors.Is(err, ErrChannelUnavailable) {
		t.Errorf("unknown model: %v", err)
	}
	for name, emb := range map[string]Embedder{
		"embed error":    &covEmbedder{err: errors.New("boom")},
		"wrong count":    &covEmbedder{drop: true},
		"wrong size":     &covEmbedder{vec: []float32{1, 2, 3}},
		"bad query":      &covEmbedder{vec: []float32{0, 0, 0, 0}},
		"nonfinite":      &covEmbedder{vec: []float32{float32(math.Inf(1)), 0, 0, 0}},
		"empty response": &covEmbedder{empty: true},
	} {
		if _, err := kg.Now().VectorSearch(ctx, emb, "m", "tea", 3); err == nil {
			t.Errorf("%s: search succeeded", name)
		}
	}
	// Recall defaults k and fuses both channels.
	if hits, err := kg.Now().Recall(ctx, good, "m", "tea", 0); err != nil || len(hits) == 0 {
		t.Fatalf("recall = %+v, %v", hits, err)
	}
	if _, err := kg.Now().Recall(ctx, nil, "m", "tea", 3); !errors.Is(err, ErrChannelUnavailable) {
		t.Errorf("recall without an embedder: %v", err)
	}
	// History keeps its vectors: a retracted statement can still be embedded.
	tx(t, kg, func(w *KGTx) error { _, err := w.Retract(e); return err })
	if err := kg.PutVector(ctx, e, "m", []float32{1, 0, 0, 0}); err != nil {
		t.Errorf("vector for a retracted statement: %v", err)
	}
}

type covEmbedder struct {
	err   error
	drop  bool
	empty bool
	vec   []float32
}

func (s *covEmbedder) Embed(texts []string) ([][]float32, error) {
	if s.err != nil {
		return nil, s.err
	}
	if s.empty {
		return nil, nil
	}
	n := len(texts)
	if s.drop {
		n--
	}
	out := make([][]float32, n)
	for i := range out {
		out[i] = s.vec
		if s.vec == nil {
			out[i] = []float32{1, 0}
		}
	}
	return out, nil
}

func (s *covEmbedder) Close() {}

// The legacy import of a pre-graph palace reports a storage failure at every
// step instead of leaving a half-migrated file.
func TestKGCov_LegacyMigrationFaults(t *testing.T) {
	build := func(t *testing.T) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), "palace.db")
		raw, err := sql.Open("sqlite", path+"?"+connPragmas)
		if err != nil {
			t.Fatal(err)
		}
		defer raw.Close()
		if _, err := raw.Exec(`CREATE TABLE schema_versions (id INTEGER PRIMARY KEY, version INTEGER UNIQUE NOT NULL, applied_at TEXT NOT NULL)`); err != nil {
			t.Fatal(err)
		}
		for i := range 5 {
			if err := applyMigration(raw, i+1, migrations[i]); err != nil {
				t.Fatal(err)
			}
		}
		for _, q := range []string{
			`INSERT INTO entities (id, name, created_at) VALUES ('alice','Alice','2026-01-01T00:00:00Z'), ('auth','auth','2026-01-01T00:00:00Z')`,
			`INSERT INTO triples (id, subject_id, predicate_id, object_id, valid_from, valid_to, source_file, extracted_at) VALUES
				('t1', 'alice', 'works_on', 'auth', '2026-02-01T00:00:00Z', '2026-01-01T00:00:00Z', 'notes.md', '2026-01-02T10:00:00Z'),
				('t2', 'alice', 'knows', 'auth', 'garbage', NULL, '', 'not a time')`,
		} {
			if _, err := raw.Exec(q); err != nil {
				t.Fatal(err)
			}
		}
		return path
	}
	// First, the clean run: both triples arrive, the empty interval loses its end.
	path := build(t)
	db, err := OpenDB(path)
	if err != nil {
		t.Fatal(err)
	}
	kg := NewKnowledgeGraph(NewSQLitePalaceStore(db))
	got, err := kg.Query(context.Background(), "alice", "", "subject")
	if err != nil || len(got) != 2 {
		t.Fatalf("migrated = %+v, %v", got, err)
	}
	for _, tr := range got {
		if tr.PredicateID == "works_on" && tr.ValidTo != nil {
			t.Errorf("an empty interval kept its end: %+v", tr)
		}
	}
	_ = db.Close()

	for n := 1; n < 300; n++ {
		path := build(t)
		f := &faults{}
		probe, err := sql.Open("sqlite", path+"?"+connPragmas)
		if err != nil {
			t.Fatal(err)
		}
		fdb := sql.OpenDB(&faultConnector{drv: probe.Driver(), dsn: path + "?" + connPragmas, f: f})
		f.arm(n)
		err = migrate(fdb)
		fired := f.fired()
		_ = fdb.Close()
		_ = probe.Close()
		if !fired {
			if err != nil {
				t.Fatalf("clean migrate failed: %v", err)
			}
			return
		}
		if err == nil {
			t.Fatalf("call %d: injected failure swallowed", n)
		}
		if !errors.Is(err, errInjected) {
			t.Fatalf("call %d: error does not wrap the cause: %v", n, err)
		}
	}
	t.Fatal("migration made too many calls")
}
