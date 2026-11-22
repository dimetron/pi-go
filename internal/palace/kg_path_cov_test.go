package palace

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
)

func TestPathCov_ModeStringAndParse(t *testing.T) {
	if got := PathMode(42).String(); got != "PathMode(42)" {
		t.Fatalf("unknown mode = %q", got)
	}
	for s, want := range map[string]PathMode{
		"": PathReach, " REACH ": PathReach, "all-shortest": PathAllShortest,
		"allshortest": PathAllShortest, "trail": PathTrails, "Shortest": PathShortest,
	} {
		if got, err := ParsePathMode(s); err != nil || got != want {
			t.Errorf("ParsePathMode(%q) = %v, %v; want %v", s, got, err, want)
		}
	}
	_, err := ParsePathMode("fastest")
	if err == nil || !strings.Contains(err.Error(), `unknown path mode "fastest"`) {
		t.Fatalf("bad mode = %v", err)
	}
}

func TestPathCov_QueryValidation(t *testing.T) {
	kg, _ := edgeKG(t, [2]string{"a", "b"})
	ctx := context.Background()
	cases := []struct {
		name string
		pq   PathQuery
		want string
	}{
		{"no endpoint", PathQuery{}, "needs from or to"},
		{"time-respecting backward", PathQuery{To: "b", TimeRespecting: true}, "needs from"},
		{"negative min", PathQuery{From: "a", MinHops: -1}, "must not be negative"},
		{"negative max", PathQuery{From: "a", MaxHops: -1}, "must not be negative"},
		{"negative limit", PathQuery{From: "a", Limit: -1}, "must not be negative"},
		{"negative frontier", PathQuery{From: "a", MaxFrontier: -1}, "must not be negative"},
		{"min above max", PathQuery{From: "a", MinHops: 4, MaxHops: 2}, "min hops 4 exceeds max hops 2"},
		{"bad direction", PathQuery{From: "a", Direction: "sideways"}, `unknown path direction "sideways"`},
	}
	for _, tc := range cases {
		_, err := kg.Now().Paths(ctx, tc.pq)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", tc.name, err, tc.want)
		}
	}
}

func TestPathCov_MaxHopsCappedAt16(t *testing.T) {
	kg, _ := newEngine(t)
	for i := 0; i < 20; i++ {
		assert1(t, kg, Ent(fmt.Sprintf("n%02d", i)), Ent("knows"), Ent(fmt.Sprintf("n%02d", i+1)), Valid{})
	}
	got, err := kg.Now().Paths(context.Background(), PathQuery{From: "n00", MaxHops: 99, Mode: PathReach, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 16 {
		t.Fatalf("reached %d nodes, want 16 (cap)", len(got))
	}
	if last := got[len(got)-1]; len(last.Steps) != 16 {
		t.Fatalf("longest path = %d hops", len(last.Steps))
	}
}

func TestPathCov_BackwardSearchOrientation(t *testing.T) {
	kg, _ := edgeKG(t, [2]string{"a", "b"}, [2]string{"b", "c"}, [2]string{"x", "c"})
	ctx := context.Background()
	// only To: walk backwards along out edges, paths still read From-side first
	got, err := kg.Now().Paths(ctx, PathQuery{To: "c", Mode: PathShortest})
	if err != nil {
		t.Fatal(err)
	}
	wantPaths(t, got, "b>c", "x>c", "a>b>c")
	if p := got[2]; p.Steps[0].From != Ent("a") || p.Steps[1].To != Ent("c") {
		t.Fatalf("backward step orientation = %+v", p.Steps)
	}
	// direction in inverts the backward walk: it follows edges forward from the end
	got, _ = kg.Now().Paths(ctx, PathQuery{To: "a", Direction: "in", Mode: PathShortest})
	wantPaths(t, got, "b>a", "c>b>a") // edges point a->b->c, so they are walked from a
}

func TestPathCov_BothDirectionsDedupesSelfLoop(t *testing.T) {
	kg, _ := newEngine(t)
	assert1(t, kg, Ent("a"), Ent("knows"), Ent("a"), Valid{})
	assert1(t, kg, Ent("a"), Ent("knows"), Ent("b"), Valid{})
	got, err := kg.Now().Paths(context.Background(), PathQuery{From: "a", Direction: "both", Mode: PathShortest, MaxHops: 1})
	if err != nil {
		t.Fatal(err)
	}
	// the self loop is read twice (as subject and as object) but is one edge
	wantPaths(t, got, "a>b")
	got, _ = kg.Now().Paths(context.Background(), PathQuery{From: "a", To: "a", Direction: "both", MinHops: 1, MaxHops: 1, Mode: PathAllShortest})
	wantPaths(t, got, "a>a")
}

func TestPathCov_TieBreakIsDeterministic(t *testing.T) {
	kg, ids := edgeKG(t, [2]string{"a", "b"}, [2]string{"a", "c"}, [2]string{"b", "d"}, [2]string{"c", "d"})
	for _, mode := range []PathMode{PathReach, PathShortest} {
		got, err := kg.Now().Paths(context.Background(), PathQuery{From: "a", To: "d", Mode: mode})
		if err != nil {
			t.Fatal(err)
		}
		// equal hops and equal arrival: the lower eid sequence wins
		wantPaths(t, got, "a>b>d")
		if got[0].Steps[0].EID != ids[0] || got[0].Steps[1].EID != ids[2] {
			t.Fatalf("%v: eids = %v %v", mode, got[0].Steps[0].EID, got[0].Steps[1].EID)
		}
	}
	got, _ := kg.Now().Paths(context.Background(), PathQuery{From: "a", To: "d", Mode: PathAllShortest})
	wantPaths(t, got, "a>b>d", "a>c>d")
	// Limit truncates the all-shortest walks
	got, _ = kg.Now().Paths(context.Background(), PathQuery{From: "a", To: "d", Mode: PathAllShortest, Limit: 1})
	wantPaths(t, got, "a>b>d")
}

func TestPathCov_ShortestPrefersFewerHopsReachPrefersEarlier(t *testing.T) {
	kg, _ := newEngine(t)
	// direct edge valid late, detour valid early
	assert1(t, kg, Ent("a"), Ent("knows"), Ent("c"), vd("2026-03-01", ""))
	assert1(t, kg, Ent("a"), Ent("knows"), Ent("b"), vd("2026-01-01", ""))
	assert1(t, kg, Ent("b"), Ent("knows"), Ent("c"), vd("2026-01-01", ""))
	ctx := context.Background()
	q := PathQuery{From: "a", To: "c", TimeRespecting: true}
	q.Mode = PathReach
	got, _ := kg.Now().Paths(ctx, q)
	wantPaths(t, got, "a>b>c")
	if got[0].Arrival == nil || !got[0].Arrival.Equal(*day("2026-01-01")) {
		t.Fatalf("reach arrival = %v", got[0].Arrival)
	}
	q.Mode = PathShortest
	got, _ = kg.Now().Paths(ctx, q)
	wantPaths(t, got, "a>c")
	if got[0].Arrival == nil || !got[0].Arrival.Equal(*day("2026-03-01")) {
		t.Fatalf("shortest arrival = %v", got[0].Arrival)
	}
}

func TestPathCov_TrailsLimitsTargetsAndOrdering(t *testing.T) {
	kg, _ := edgeKG(t, [2]string{"a", "b"}, [2]string{"b", "c"}, [2]string{"a", "c"}, [2]string{"c", "a"})
	ctx := context.Background()
	got, err := kg.Now().Paths(ctx, PathQuery{From: "a", To: "c", Mode: PathTrails})
	if err != nil {
		t.Fatal(err)
	}
	// shortest first, then by eid sequence; a>b>c>a>c would reuse a>c? no: it
	// uses a>b, b>c, c>a, a>c, all distinct edges, so it is a valid trail.
	if len(got) < 2 || nodeNames(got[0]) != "a>c" || nodeNames(got[1]) != "a>b>c" {
		t.Fatalf("trails = %v", pathNames(got))
	}
	for i := 1; i < len(got); i++ {
		if len(got[i].Steps) < len(got[i-1].Steps) {
			t.Fatalf("not shortest first: %v", pathNames(got))
		}
	}
	// no edge appears twice in one trail
	for _, p := range got {
		seen := map[int64]bool{}
		for _, s := range p.Steps {
			if seen[s.EID] {
				t.Fatalf("trail %s repeats e%d", nodeNames(p), s.EID)
			}
			seen[s.EID] = true
		}
	}
	// MinHops excludes the direct edge; Limit stops enumeration
	got, _ = kg.Now().Paths(ctx, PathQuery{From: "a", To: "c", Mode: PathTrails, MinHops: 2})
	if len(got) == 0 || len(got[0].Steps) < 2 {
		t.Fatalf("min hops trails = %v", pathNames(got))
	}
	got, _ = kg.Now().Paths(ctx, PathQuery{From: "a", Mode: PathTrails, Limit: 1})
	if len(got) != 1 {
		t.Fatalf("limit trails = %v", pathNames(got))
	}
	// no target: every trail from a, never an empty one
	got, _ = kg.Now().Paths(ctx, PathQuery{From: "a", Mode: PathTrails, MaxHops: 1})
	wantPaths(t, got, "a>b", "a>c")
}

func TestPathCov_TrailsTimeRespectingAndGuards(t *testing.T) {
	kg, _ := newEngine(t)
	assert1(t, kg, Ent("a"), Ent("knows"), Ent("b"), vd("2026-01-05", "2026-01-06"))
	assert1(t, kg, Ent("b"), Ent("knows"), Ent("c"), vd("2026-01-01", "2026-01-02"))
	assert1(t, kg, Ent("b"), Ent("knows"), Ent("d"), vd("2026-01-07", ""))
	ctx := context.Background()
	got, err := kg.Now().Paths(ctx, PathQuery{From: "a", Mode: PathTrails, TimeRespecting: true})
	if err != nil {
		t.Fatal(err)
	}
	// b>c ended before the walk reached b, so only d is reachable through b
	wantPaths(t, got, "a>b", "a>b>d")
	if got[1].Arrival == nil || !got[1].Arrival.Equal(*day("2026-01-07")) {
		t.Fatalf("arrival = %v", got[1].Arrival)
	}
	// frontier guard in trails
	_, err = kg.Now().Paths(ctx, PathQuery{From: "a", Mode: PathTrails, MaxFrontier: 2})
	mustErr(t, err, ErrBudget)
	// a canceled context stops the search
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = kg.Now().Paths(cctx, PathQuery{From: "a", Mode: PathTrails}); err == nil {
		t.Fatal("canceled trails succeeded")
	}
	if _, err = kg.Now().Paths(cctx, PathQuery{From: "a", Mode: PathShortest}); err == nil {
		t.Fatal("canceled bfs succeeded")
	}
}

func TestPathCov_PredicateFilterAcrossSeveral(t *testing.T) {
	kg, _ := newEngine(t)
	assert1(t, kg, Ent("a"), Ent("knows"), Ent("b"), Valid{})
	assert1(t, kg, Ent("a"), Ent("owns"), Ent("c"), Valid{})
	assert1(t, kg, Ent("a"), Ent("hates"), Ent("d"), Valid{})
	ctx := context.Background()
	got, err := kg.Now().Paths(ctx, PathQuery{From: "a", Predicates: []string{"knows", "owns"}, Mode: PathShortest})
	if err != nil {
		t.Fatal(err)
	}
	wantPaths(t, got, "a>b", "a>c")
	got, _ = kg.Now().Paths(ctx, PathQuery{From: "a", Predicates: []string{"nosuch"}})
	if len(got) != 0 {
		t.Fatalf("unknown predicate matched %v", pathNames(got))
	}
	// a literal object is not an edge
	assert1(t, kg, Ent("a"), Ent("note"), Str("hi"), Valid{})
	got, _ = kg.Now().Paths(ctx, PathQuery{From: "a", Mode: PathShortest})
	if slices.Contains(pathNames(got), "a>hi") || len(got) != 3 {
		t.Fatalf("literal edge leaked: %v", pathNames(got))
	}
}

func TestPathCov_CmpEIDs(t *testing.T) {
	s := func(ids ...int64) []PathStep {
		out := make([]PathStep, len(ids))
		for i, id := range ids {
			out[i].EID = id
		}
		return out
	}
	if cmpEIDs(s(1), s(1, 2)) != -1 || cmpEIDs(s(1, 2), s(1)) != 1 {
		t.Fatal("length order")
	}
	if cmpEIDs(s(1, 3), s(1, 2)) != 1 || cmpEIDs(s(1, 2), s(1, 3)) != -1 {
		t.Fatal("eid order")
	}
	if cmpEIDs(s(1, 2), s(1, 2)) != 0 {
		t.Fatal("equal")
	}
}
