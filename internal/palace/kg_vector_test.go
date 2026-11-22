package palace

import (
	"context"
	"errors"
	"math"
	"strings"
	"sync"
	"testing"
)

// wordEmbedder embeds text as the sum of per-word vectors; unknown words are
// ignored. It records every text it is asked to embed.
type wordEmbedder struct {
	mu    sync.Mutex
	words map[string][]float32
	dim   int
	seen  []string
}

func newWordEmbedder(words map[string][]float32) *wordEmbedder {
	e := &wordEmbedder{words: words}
	for _, v := range words {
		e.dim = len(v)
	}
	return e
}

func (e *wordEmbedder) Embed(texts []string) ([][]float32, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([][]float32, len(texts))
	for i, t := range texts {
		e.seen = append(e.seen, t)
		v := make([]float32, e.dim)
		for _, w := range strings.Fields(strings.ToLower(t)) {
			for j, f := range e.words[w] {
				v[j] += f
			}
		}
		if allZero(v) {
			v[e.dim-1] = 1 // a text of unknown words: a fixed direction
		}
		out[i] = v
	}
	return out, nil
}

func (e *wordEmbedder) Close() {}

func (e *wordEmbedder) texts() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.seen...)
}

func allZero(v []float32) bool {
	for _, f := range v {
		if f != 0 {
			return false
		}
	}
	return true
}

var testWords = map[string][]float32{
	"tea":    {1, 0, 0, 0},
	"chai":   {0.9, 0.3, 0, 0},
	"coffee": {0, 1, 0, 0},
	"bike":   {0, 0, 1, 0},
}

func scoredEIDs(sc []Scored) []int64 {
	out := make([]int64, len(sc))
	for i, s := range sc {
		out[i] = s.Statement.EID
	}
	return out
}

func vecCount(t *testing.T, kg *KnowledgeGraph, model string) int {
	t.Helper()
	db, err := kg.database()
	if err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRow("SELECT count(*) FROM kg_stmt_vec WHERE model = ?", model).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestVector_BackfillSupersedeAndTransaction(t *testing.T) {
	ctx := context.Background()
	kg, _ := newEngine(t)
	emb := newWordEmbedder(testWords)
	a := assert1(t, kg, Ent("alice"), Ent("likes"), Ent("tea"), Valid{})
	assert1(t, kg, Ent("bob"), Ent("likes"), Ent("coffee"), Valid{})
	n, err := kg.EmbedStatements(ctx, emb, "m", 1)
	if err != nil || n != 2 {
		t.Fatalf("EmbedStatements = %d, %v", n, err)
	}
	last, _ := kg.Transaction(ctx, 3)
	if last == nil || last.Author != "palace-embed" {
		t.Fatalf("embed transaction = %+v", last)
	}
	// Nothing left: no transaction is spent.
	if n, err := kg.EmbedStatements(ctx, emb, "m", 0); err != nil || n != 0 {
		t.Fatalf("second backfill = %d, %v", n, err)
	}
	if tx4, _ := kg.Transaction(ctx, 4); tx4 != nil {
		t.Fatalf("an empty backfill committed %+v", tx4)
	}
	// The model and its dimension are recorded as a statement.
	if dim, ok, err := modelDim(ctx, mustQ(t, kg), "m"); err != nil || !ok || dim != 4 {
		t.Fatalf("model dim = %d %v %v", dim, ok, err)
	}
	// A supersede puts the new eid, and only it, in the next backfill.
	var a2 int64
	tx(t, kg, func(w *KGTx) error {
		var err error
		a2, err = w.Supersede(a, Patch{Object: new(Ent("chai"))})
		return err
	})
	if n, err := kg.EmbedStatements(ctx, emb, "m", 10); err != nil || n != 1 {
		t.Fatalf("backfill after supersede = %d, %v", n, err)
	}
	got, err := kg.Now().VectorSearch(ctx, emb, "m", "tea", 5)
	if err != nil || len(got) != 2 || got[0].Statement.EID != a2 {
		t.Fatalf("search = %v, %v", scoredEIDs(got), err)
	}
	// Another model starts from scratch and keeps its own dimension.
	if n, err := kg.EmbedStatements(ctx, emb, "m2", 10); err != nil || n != 2 {
		t.Fatalf("second model = %d, %v", n, err)
	}
}

func mustQ(t *testing.T, kg *KnowledgeGraph) querier {
	t.Helper()
	db, err := kg.database()
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func TestVector_ViewAwareness(t *testing.T) {
	ctx := context.Background()
	kg, _ := newEngine(t)
	emb := newWordEmbedder(testWords)
	e1 := assert1(t, kg, Ent("alice"), Ent("likes"), Ent("tea"), Valid{})
	e2 := assert1(t, kg, Ent("bob"), Ent("likes"), Ent("coffee"), Valid{})
	if _, err := kg.EmbedStatements(ctx, emb, "m", 10); err != nil {
		t.Fatal(err)
	}
	vecT := int64(3)
	// A retraction later removes it from Now but not from the past.
	tx(t, kg, func(w *KGTx) error { _, err := w.Retract(e2); return err })
	if got, err := kg.Now().VectorSearch(ctx, emb, "m", "coffee", 5); err != nil || len(got) != 1 || got[0].Statement.EID != e1 {
		t.Fatalf("now = %v, %v", scoredEIDs(got), err)
	}
	// Before the vectors were stored the view sees none, but the channel exists.
	if got, err := kg.AsOf(vecT-1).VectorSearch(ctx, emb, "m", "tea", 5); err != nil || len(got) != 0 {
		t.Fatalf("as of before the vectors = %v, %v", scoredEIDs(got), err)
	}
	past, err := kg.AsOf(vecT).VectorSearch(ctx, emb, "m", "coffee", 5)
	if err != nil || len(past) != 2 || past[0].Statement.EID != e2 || past[0].Statement.TRet != nil {
		t.Fatalf("as of the vectors = %v, %v", scoredEIDs(past), err)
	}
	if got, err := kg.History().VectorSearch(ctx, emb, "m", "coffee", 5); err != nil || len(got) != 2 {
		t.Fatalf("history = %v, %v", scoredEIDs(got), err)
	}
	// A graph restriction applies to vector search too.
	tx(t, kg, func(w *KGTx) error { _, _, err := w.AddToGraph(e1, "g"); return err })
	if got, err := kg.Now().InGraph("g").VectorSearch(ctx, emb, "m", "coffee", 5); err != nil || len(got) != 1 || got[0].Statement.EID != e1 {
		t.Fatalf("graph = %v, %v", scoredEIDs(got), err)
	}
}

func TestVector_ExactCosineOrder(t *testing.T) {
	ctx := context.Background()
	kg, _ := newEngine(t)
	var e [4]int64
	for i, o := range []string{"w", "x", "y", "z"} {
		e[i] = assert1(t, kg, Ent("s"), Ent("p"), Ent(o), Valid{})
	}
	vecs := [][]float32{{1, 0}, {1, 1}, {0, 1}, {-1, 0.1}}
	for i, v := range vecs {
		if err := kg.PutVector(ctx, e[i], "m", v); err != nil {
			t.Fatal(err)
		}
	}
	q := newWordEmbedder(map[string][]float32{"q": {1, 0.2}})
	got, err := kg.Now().VectorSearch(ctx, q, "m", "q", 3)
	if err != nil || len(got) != 3 {
		t.Fatalf("search = %v, %v", got, err)
	}
	want := []int64{e[0], e[1], e[2]}
	for i, s := range got {
		if s.Statement.EID != want[i] || s.Rank != i+1 {
			t.Fatalf("order = %v, want %v", scoredEIDs(got), want)
		}
	}
	if math.Abs(got[0].Score-1/math.Sqrt(1.04)) > 1e-6 {
		t.Fatalf("score = %v", got[0].Score)
	}
	if got[0].Score < got[1].Score || got[1].Score < got[2].Score {
		t.Fatalf("scores not descending: %+v", got)
	}
}

func TestVector_PutVectorGuards(t *testing.T) {
	ctx := context.Background()
	kg, db := newEngine(t)
	e1 := assert1(t, kg, Ent("a"), Ent("p"), Ent("b"), Valid{})
	e2 := assert1(t, kg, Ent("c"), Ent("p"), Ent("d"), Valid{})
	nan, inf := float32(math.NaN()), float32(math.Inf(1))
	for name, v := range map[string][]float32{"empty": nil, "nan": {1, nan}, "inf": {inf, 1}, "zero": {0, 0}} {
		if err := kg.PutVector(ctx, e1, "m", v); !errors.Is(err, ErrBadVector) {
			t.Fatalf("%s: err = %v", name, err)
		}
	}
	if err := kg.PutVector(ctx, e1, "m", []float32{1, 2}); err != nil {
		t.Fatal(err)
	}
	// The first vector fixed the dimension.
	if err := kg.PutVector(ctx, e2, "m", []float32{1, 2, 3}); !errors.Is(err, ErrBadVector) {
		t.Fatalf("dimension: err = %v", err)
	}
	// The same vector is a no-op: no transaction is written.
	last, _ := lastT(ctx, db)
	if err := kg.PutVector(ctx, e1, "m", []float32{1, 2}); err != nil {
		t.Fatalf("identical: %v", err)
	}
	if now, _ := lastT(ctx, db); now != last {
		t.Fatalf("identical put wrote tx %d", now)
	}
	if err := kg.PutVector(ctx, e1, "m", []float32{2, 1}); !errors.Is(err, ErrVectorConflict) {
		t.Fatalf("conflict: err = %v", err)
	}
	if err := kg.PutVector(ctx, 9999, "m", []float32{1, 2}); err == nil {
		t.Fatal("vector for a missing statement accepted")
	}
	// An embedder returning a wrong or bad vector is rejected too.
	if _, err := kg.EmbedStatements(ctx, newWordEmbedder(testWords), "m", 10); !errors.Is(err, ErrBadVector) {
		t.Fatalf("backfill with the wrong dimension: err = %v", err)
	}
	if vecCount(t, kg, "m") != 1 {
		t.Fatal("a failed backfill left vectors behind")
	}
}

func TestVector_ImmutableAtTheDatabase(t *testing.T) {
	ctx := context.Background()
	kg, db := newEngine(t)
	e1 := assert1(t, kg, Ent("a"), Ent("p"), Ent("b"), Valid{})
	if err := kg.PutVector(ctx, e1, "m", []float32{1, 2}); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		"DELETE FROM kg_stmt_vec",
		"UPDATE kg_stmt_vec SET vec = x'00000000'",
		"INSERT OR REPLACE INTO kg_stmt_vec (eid, model, dim, vec, t) VALUES (1, 'm', 2, x'0000803f00000040', 99)",
		"INSERT INTO kg_stmt_vec (eid, model, dim, vec, t) VALUES (1, 'other', 2, x'0000803f00000040', 1)",
	} {
		if _, err := db.Exec(q); err == nil {
			t.Fatalf("%q was accepted", q)
		}
	}
	if vecCount(t, kg, "m") != 1 {
		t.Fatal("vector table changed")
	}
}

func TestVector_RecallRRF(t *testing.T) {
	ctx := context.Background()
	kg, _ := newEngine(t)
	emb := newWordEmbedder(testWords)
	tx(t, kg, func(w *KGTx) error {
		for _, o := range []string{"tea", "chai", "coffee"} {
			if _, _, err := w.Assert(Ent("alice"), Ent("likes"), Ent(o), Valid{}); err != nil {
				return err
			}
		}
		_, _, err := w.Assert(Ent("tea"), Ent("is"), Str("hot"), Valid{})
		return err
	})
	if _, err := kg.EmbedStatements(ctx, emb, "m", 10); err != nil {
		t.Fatal(err)
	}
	hits, err := kg.Now().Recall(ctx, emb, "m", "tea", 10)
	if err != nil || len(hits) == 0 {
		t.Fatalf("recall = %v, %v", hits, err)
	}
	var both, vecOnly bool
	for i, h := range hits {
		want := 0.0
		if h.TextRank > 0 {
			want += 1 / float64(60+h.TextRank)
		}
		if h.VectorRank > 0 {
			want += 1 / float64(60+h.VectorRank)
		}
		if math.Abs(h.RRF-want) > 1e-12 {
			t.Fatalf("hit %d RRF = %v, want %v", i, h.RRF, want)
		}
		if i > 0 && hits[i-1].RRF < h.RRF {
			t.Fatalf("not ordered by RRF: %+v", hits)
		}
		both = both || (h.TextRank > 0 && h.VectorRank > 0)
		vecOnly = vecOnly || (h.TextRank == 0 && h.VectorRank > 0)
	}
	if !both || !vecOnly {
		t.Fatalf("channels not both exercised (both=%v vecOnly=%v): %+v", both, vecOnly, hits)
	}
	if top := hits[0]; top.TextRank == 0 || top.VectorRank == 0 {
		t.Fatalf("top hit should be in both channels: %+v", top)
	}
	if hits, _ := kg.Now().Recall(ctx, emb, "m", "tea", 1); len(hits) != 1 {
		t.Fatalf("k not honored: %d", len(hits))
	}
}

func TestVector_MissingChannelFails(t *testing.T) {
	ctx := context.Background()
	kg, _ := newEngine(t)
	emb := newWordEmbedder(testWords)
	assert1(t, kg, Ent("alice"), Ent("likes"), Ent("tea"), Valid{})
	v := kg.Now()
	if _, err := v.Recall(ctx, emb, "m", "tea", 5); !errors.Is(err, ErrChannelUnavailable) {
		t.Fatalf("no vectors: recall err = %v", err)
	}
	if _, err := v.VectorSearch(ctx, emb, "m", "tea", 5); !errors.Is(err, ErrChannelUnavailable) {
		t.Fatalf("no vectors: search err = %v", err)
	}
	if _, err := kg.EmbedStatements(ctx, emb, "m", 5); err != nil {
		t.Fatal(err)
	}
	if _, err := v.Recall(ctx, nil, "m", "tea", 5); !errors.Is(err, ErrChannelUnavailable) {
		t.Fatalf("nil embedder: err = %v", err)
	}
	if _, err := v.Recall(ctx, emb, "other", "tea", 5); !errors.Is(err, ErrChannelUnavailable) {
		t.Fatalf("other model: err = %v", err)
	}
	if _, err := kg.EmbedStatements(ctx, nil, "m", 5); !errors.Is(err, ErrChannelUnavailable) {
		t.Fatalf("nil embedder backfill: err = %v", err)
	}
	// A query of the wrong dimension is refused, not scored.
	if _, err := v.VectorSearch(ctx, newWordEmbedder(map[string][]float32{"tea": {1, 0}}), "m", "tea", 5); !errors.Is(err, ErrBadVector) {
		t.Fatalf("wrong query dimension: err = %v", err)
	}
}

func TestVector_NeverEmbedsSealedValues(t *testing.T) {
	ctx := context.Background()
	kg, db := newEngine(t)
	emb := newWordEmbedder(testWords)
	tx(t, kg, func(w *KGTx) error { return w.SetPredicate(Ent("email"), PredicateSchema{Sensitive: true}) })
	var sealed int64
	tx(t, kg, func(w *KGTx) error {
		var err error
		if sealed, _, err = w.Assert(Ent("alice"), Ent("email"), Str("alice@secret.example"), Valid{}); err != nil {
			return err
		}
		// A plain layer on a sealed statement stands on a sealed chain.
		if _, _, err = w.Annotate(sealed, Ent("checked"), Str("yesterday")); err != nil {
			return err
		}
		_, _, err = w.Assert(Ent("alice"), Ent("likes"), Ent("tea"), Valid{})
		return err
	})
	n, err := kg.EmbedStatements(ctx, emb, "m", 10)
	if err != nil || n != 1 {
		t.Fatalf("embedded %d, %v; only the plain statement is eligible", n, err)
	}
	if err := kg.PutVector(ctx, sealed, "m", []float32{1, 0, 0, 0}); !errors.Is(err, ErrBadVector) {
		t.Fatalf("caller vector for a sealed statement: err = %v", err)
	}
	// After erasure the sealed value decodes as sys:Erased: still not embedded.
	if _, err := kg.Erase(ctx, TxOptions{Author: "t"}, "alice"); err != nil {
		t.Fatal(err)
	}
	assert1(t, kg, Ent("bob"), Ent("likes"), Ent("coffee"), Valid{})
	if n, err := kg.EmbedStatements(ctx, emb, "m", 10); err != nil || n != 1 {
		t.Fatalf("post-erase backfill = %d, %v", n, err)
	}
	if _, err := kg.Now().Recall(ctx, emb, "m", "email", 5); err != nil {
		t.Fatal(err)
	}
	for _, s := range emb.texts() {
		if strings.Contains(s, "secret") || strings.Contains(s, ErasedText) || strings.Contains(s, "yesterday") {
			t.Fatalf("embedder received %q", s)
		}
	}
	var c int
	if err := db.QueryRow("SELECT count(*) FROM kg_stmt_vec WHERE eid = ?", sealed).Scan(&c); err != nil || c != 0 {
		t.Fatalf("vectors for the sealed statement = %d, %v", c, err)
	}
}
