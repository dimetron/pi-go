package palace

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"
)

func edgeKG(t *testing.T, edges ...[2]string) (*KnowledgeGraph, []int64) {
	t.Helper()
	kg, _ := newEngine(t)
	var ids []int64
	for _, e := range edges {
		ids = append(ids, assert1(t, kg, Ent(e[0]), Ent("knows"), Ent(e[1]), Valid{}))
	}
	return kg, ids
}

func nodeNames(p Path) string {
	s := ""
	for i, n := range p.Nodes {
		if i > 0 {
			s += ">"
		}
		s += n.Text
	}
	return s
}

func pathNames(ps []Path) []string {
	var out []string
	for _, p := range ps {
		out = append(out, nodeNames(p))
	}
	return out
}

func wantPaths(t *testing.T, got []Path, want ...string) {
	t.Helper()
	g := pathNames(got)
	if !slices.Equal(g, want) {
		t.Fatalf("paths = %v, want %v", g, want)
	}
}

func TestPaths_NeedsEndpoint(t *testing.T) {
	kg, _ := newEngine(t)
	if _, err := kg.Now().Paths(context.Background(), PathQuery{}); err == nil {
		t.Fatal("want error")
	}
}

func TestParsePathMode(t *testing.T) {
	for s, want := range map[string]PathMode{"reach": PathReach, "shortest": PathShortest, "all_shortest": PathAllShortest, "trails": PathTrails} {
		got, err := ParsePathMode(s)
		if err != nil || got != want || got.String() != s {
			t.Fatalf("%s: %v %v", s, got, err)
		}
	}
	if _, err := ParsePathMode("bogus"); err == nil {
		t.Fatal("want error")
	}
}

func TestPaths_ReachAndShortest(t *testing.T) {
	kg, _ := edgeKG(t, [2]string{"a", "b"}, [2]string{"b", "c"}, [2]string{"a", "c"}, [2]string{"c", "d"})
	ctx := context.Background()
	got, err := kg.Now().Paths(ctx, PathQuery{From: "a", Mode: PathReach})
	if err != nil {
		t.Fatal(err)
	}
	wantPaths(t, got, "a>b", "a>c", "a>c>d")
	got, _ = kg.Now().Paths(ctx, PathQuery{From: "a", To: "d", Mode: PathShortest})
	wantPaths(t, got, "a>c>d")
	if len(got[0].Steps) != 2 || got[0].Steps[0].From != Ent("a") || got[0].Arrival != nil {
		t.Fatalf("steps = %+v", got[0].Steps)
	}
}

func TestPaths_AllShortest(t *testing.T) {
	kg, _ := edgeKG(t, [2]string{"a", "b"}, [2]string{"a", "c"}, [2]string{"b", "d"}, [2]string{"c", "d"}, [2]string{"a", "x"}, [2]string{"x", "y"}, [2]string{"y", "d"})
	got, err := kg.Now().Paths(context.Background(), PathQuery{From: "a", To: "d", Mode: PathAllShortest})
	if err != nil {
		t.Fatal(err)
	}
	wantPaths(t, got, "a>b>d", "a>c>d")
	// Parallel edges are distinct shortest paths.
	kg2, _ := edgeKG(t, [2]string{"a", "b"})
	assert1(t, kg2, Ent("a"), Ent("likes"), Ent("b"), Valid{})
	got, _ = kg2.Now().Paths(context.Background(), PathQuery{From: "a", To: "b", Mode: PathAllShortest})
	wantPaths(t, got, "a>b", "a>b")
	if got[0].Steps[0].EID >= got[1].Steps[0].EID {
		t.Fatal("not ordered by eid")
	}
}

func TestPaths_Trails(t *testing.T) {
	kg, _ := edgeKG(t, [2]string{"a", "b"}, [2]string{"b", "a"}, [2]string{"b", "c"})
	got, err := kg.Now().Paths(context.Background(), PathQuery{From: "a", To: "c", Mode: PathTrails})
	if err != nil {
		t.Fatal(err)
	}
	// a>b>c, and a>b>a>b>c repeats edge a>b so is not a trail.
	wantPaths(t, got, "a>b>c")
	got, _ = kg.Now().Paths(context.Background(), PathQuery{From: "a", Mode: PathTrails, MaxHops: 4})
	for _, p := range got {
		seen := map[int64]bool{}
		for _, s := range p.Steps {
			if seen[s.EID] {
				t.Fatalf("repeated edge in %v", nodeNames(p))
			}
			seen[s.EID] = true
		}
	}
	if len(got) != 3 { // a>b, a>b>a, a>b>c
		t.Fatalf("trails = %v", pathNames(got))
	}
	lim, _ := kg.Now().Paths(context.Background(), PathQuery{From: "a", Mode: PathTrails, Limit: 1})
	if len(lim) != 1 {
		t.Fatalf("limit = %d", len(lim))
	}
}

func TestPaths_CyclesTerminate(t *testing.T) {
	kg, _ := edgeKG(t, [2]string{"a", "b"}, [2]string{"b", "c"}, [2]string{"c", "a"})
	for _, m := range []PathMode{PathReach, PathShortest, PathAllShortest, PathTrails} {
		got, err := kg.Now().Paths(context.Background(), PathQuery{From: "a", Mode: m, MaxHops: 16})
		if err != nil {
			t.Fatalf("%v: %v", m, err)
		}
		if len(got) == 0 {
			t.Fatalf("%v: no paths", m)
		}
	}
}

func TestPaths_HopLimits(t *testing.T) {
	kg, _ := edgeKG(t, [2]string{"a", "b"}, [2]string{"b", "c"}, [2]string{"c", "d"})
	ctx := context.Background()
	got, _ := kg.Now().Paths(ctx, PathQuery{From: "a", MaxHops: 2})
	wantPaths(t, got, "a>b", "a>b>c")
	got, _ = kg.Now().Paths(ctx, PathQuery{From: "a", MinHops: 2, MaxHops: 3})
	wantPaths(t, got, "a>b>c", "a>b>c>d")
	if _, err := kg.Now().Paths(ctx, PathQuery{From: "a", MinHops: 3, MaxHops: 2}); err == nil {
		t.Fatal("min > max must fail")
	}
	// MaxHops is capped at 16: a 20-long chain is cut off.
	kg2, _ := newEngine(t)
	for i := 0; i < 20; i++ {
		assert1(t, kg2, Ent(fmt.Sprintf("n%02d", i)), Ent("next"), Ent(fmt.Sprintf("n%02d", i+1)), Valid{})
	}
	got, _ = kg2.Now().Paths(ctx, PathQuery{From: "n00", MaxHops: 99})
	if len(got) != 16 {
		t.Fatalf("capped reach = %d, want 16", len(got))
	}
}

func TestPaths_MinHopsDirectDoesNotSuppressLonger(t *testing.T) {
	kg, _ := edgeKG(t, [2]string{"a", "b"}, [2]string{"a", "c"}, [2]string{"c", "b"})
	ctx := context.Background()
	for _, m := range []PathMode{PathReach, PathShortest, PathAllShortest, PathTrails} {
		got, err := kg.Now().Paths(ctx, PathQuery{From: "a", To: "b", MinHops: 2, Mode: m})
		if err != nil {
			t.Fatal(err)
		}
		wantPaths(t, got, "a>c>b")
	}
}

func TestPaths_FrontierGuard(t *testing.T) {
	kg, _ := newEngine(t)
	for i := 0; i < 10; i++ {
		assert1(t, kg, Ent("hub"), Ent("knows"), Ent(fmt.Sprintf("n%d", i)), Valid{})
	}
	_, err := kg.Now().Paths(context.Background(), PathQuery{From: "hub", MaxFrontier: 5})
	mustErr(t, err, ErrBudget)
	_, err = kg.Now().Paths(context.Background(), PathQuery{From: "hub", Mode: PathTrails, MaxFrontier: 5})
	mustErr(t, err, ErrBudget)
	_, err = kg.Now().Paths(context.Background(), PathQuery{From: "hub", Budget: Budget{MaxRows: 2}})
	mustErr(t, err, ErrBudget)
}

func TestPaths_ContextCancel(t *testing.T) {
	kg, _ := edgeKG(t, [2]string{"a", "b"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := kg.Now().Paths(ctx, PathQuery{From: "a"})
	mustErr(t, err, context.Canceled)
}

func TestPaths_PredicatesAndNonEntityObjects(t *testing.T) {
	kg, _ := edgeKG(t, [2]string{"a", "b"})
	assert1(t, kg, Ent("a"), Ent("likes"), Ent("c"), Valid{})
	assert1(t, kg, Ent("a"), Ent("age"), Int(3), Valid{})
	ctx := context.Background()
	got, _ := kg.Now().Paths(ctx, PathQuery{From: "a", Predicates: []string{"likes"}})
	wantPaths(t, got, "a>c")
	got, _ = kg.Now().Paths(ctx, PathQuery{From: "a"})
	wantPaths(t, got, "a>b", "a>c")
}

func TestPaths_Direction(t *testing.T) {
	kg, _ := edgeKG(t, [2]string{"a", "b"}, [2]string{"c", "b"})
	ctx := context.Background()
	got, _ := kg.Now().Paths(ctx, PathQuery{From: "b"})
	wantPaths(t, got)
	got, _ = kg.Now().Paths(ctx, PathQuery{From: "b", Direction: "in"})
	wantPaths(t, got, "b>a", "b>c")
	got, _ = kg.Now().Paths(ctx, PathQuery{From: "a", Direction: "both", MaxHops: 2})
	wantPaths(t, got, "a>b", "a>b>c")
	// Only To: searched backwards, reported source first, edges oriented.
	got, _ = kg.Now().Paths(ctx, PathQuery{To: "b"})
	wantPaths(t, got, "a>b", "c>b")
	if got[0].Steps[0].From != Ent("a") || got[0].Steps[0].To != Ent("b") {
		t.Fatalf("step = %+v", got[0].Steps[0])
	}
	kg2, _ := edgeKG(t, [2]string{"a", "b"}, [2]string{"b", "c"})
	got, _ = kg2.Now().Paths(ctx, PathQuery{To: "c", Mode: PathShortest})
	wantPaths(t, got, "b>c", "a>b>c")
	if _, err := kg.Now().Paths(ctx, PathQuery{From: "a", Direction: "up"}); err == nil {
		t.Fatal("bad direction must fail")
	}
}

func TestPaths_GraphScope(t *testing.T) {
	kg, ids := edgeKG(t, [2]string{"a", "b"}, [2]string{"b", "c"})
	tx(t, kg, func(w *KGTx) error {
		_, _, err := w.AddToGraph(ids[0], "g")
		return err
	})
	ctx := context.Background()
	got, _ := kg.Now().InGraph("g").Paths(ctx, PathQuery{From: "a"})
	wantPaths(t, got, "a>b")
	got, _ = kg.Now().Paths(ctx, PathQuery{From: "a"})
	wantPaths(t, got, "a>b", "a>b>c")
}

func TestPaths_AsOf(t *testing.T) {
	kg, _ := edgeKG(t, [2]string{"a", "b"})
	before := kg.Now()
	var tOld int64
	tx(t, kg, func(w *KGTx) error { return nil })
	rep := tx(t, kg, func(w *KGTx) error {
		_, _, err := w.Assert(Ent("b"), Ent("knows"), Ent("c"), Valid{})
		return err
	})
	_ = before
	tOld = rep.T - 1
	ctx := context.Background()
	got, _ := kg.AsOf(tOld).Paths(ctx, PathQuery{From: "a"})
	wantPaths(t, got, "a>b")
	got, _ = kg.Now().Paths(ctx, PathQuery{From: "a"})
	wantPaths(t, got, "a>b", "a>b>c")
	// Retracted edges vanish from Now but stay in the older view.
	tx(t, kg, func(w *KGTx) error { _, err := w.Retract(rep.Asserted[0]); return err })
	got, _ = kg.Now().Paths(ctx, PathQuery{From: "a"})
	wantPaths(t, got, "a>b")
	got, _ = kg.AsOf(rep.T).Paths(ctx, PathQuery{From: "a"})
	wantPaths(t, got, "a>b", "a>b>c")
}

func timedKG(t *testing.T, a, b Valid) *KnowledgeGraph {
	t.Helper()
	kg, _ := newEngine(t)
	assert1(t, kg, Ent("a"), Ent("knows"), Ent("b"), a)
	assert1(t, kg, Ent("b"), Ent("knows"), Ent("c"), b)
	return kg
}

func vd(from, to string) Valid {
	v := Valid{}
	if from != "" {
		v.From = day(from)
	}
	if to != "" {
		v.To = day(to)
	}
	return v
}

func TestPaths_TimeRespecting(t *testing.T) {
	ctx := context.Background()
	q := PathQuery{From: "a", To: "c", TimeRespecting: true}
	cases := []struct {
		name string
		a, b Valid
		ok   bool
		arr  string
	}{
		{"overlap [1,5)->[0,2)", vd("2026-01-02", "2026-01-06"), vd("2026-01-01", "2026-01-03"), true, "2026-01-02"},
		{"wait [1,2)->[5,6)", vd("2026-01-02", "2026-01-03"), vd("2026-01-06", "2026-01-07"), true, "2026-01-06"},
		{"backwards [5,6)->[1,2)", vd("2026-01-06", "2026-01-07"), vd("2026-01-02", "2026-01-03"), false, ""},
		{"open ends", Valid{}, Valid{}, true, ""},
	}
	for _, c := range cases {
		kg := timedKG(t, c.a, c.b)
		got, err := kg.Now().Paths(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		if (len(got) == 1) != c.ok {
			t.Fatalf("%s: paths = %v", c.name, pathNames(got))
		}
		if c.ok {
			switch {
			case c.arr == "" && got[0].Arrival != nil, c.arr != "" && (got[0].Arrival == nil || !got[0].Arrival.Equal(*day(c.arr))):
				t.Fatalf("%s: arrival = %v, want %q", c.name, got[0].Arrival, c.arr)
			}
		}
		// Without time-respecting the walk exists regardless.
		plain, _ := kg.Now().Paths(ctx, PathQuery{From: "a", To: "c"})
		if len(plain) != 1 {
			t.Fatalf("%s: plain = %v", c.name, pathNames(plain))
		}
	}
}

func TestPaths_TimeRespectingEarliestArrival(t *testing.T) {
	kg, _ := newEngine(t)
	// a>x>c arrives at day 9 (via waiting); a>y>z>c arrives at day 3.
	assert1(t, kg, Ent("a"), Ent("knows"), Ent("x"), vd("2026-01-09", ""))
	assert1(t, kg, Ent("x"), Ent("knows"), Ent("c"), Valid{})
	assert1(t, kg, Ent("a"), Ent("knows"), Ent("y"), vd("2026-01-02", ""))
	assert1(t, kg, Ent("y"), Ent("knows"), Ent("z"), vd("2026-01-02", ""))
	assert1(t, kg, Ent("z"), Ent("knows"), Ent("c"), vd("2026-01-03", ""))
	ctx := context.Background()
	got, _ := kg.Now().Paths(ctx, PathQuery{From: "a", To: "c", TimeRespecting: true, Mode: PathReach})
	wantPaths(t, got, "a>y>z>c")
	got, _ = kg.Now().Paths(ctx, PathQuery{From: "a", To: "c", TimeRespecting: true, Mode: PathShortest})
	wantPaths(t, got, "a>x>c")
	got, _ = kg.Now().Paths(ctx, PathQuery{From: "a", To: "c", TimeRespecting: true, Mode: PathTrails})
	wantPaths(t, got, "a>x>c", "a>y>z>c")
}

func TestPaths_BudgetMetered(t *testing.T) {
	kg, _ := edgeKG(t, [2]string{"a", "b"}, [2]string{"b", "c"})
	_, err := kg.Now().Paths(context.Background(), PathQuery{From: "a", Budget: Budget{MaxBytes: 1}})
	mustErr(t, err, ErrBudget)
	_ = time.Now
}
