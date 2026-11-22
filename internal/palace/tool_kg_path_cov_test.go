package palace

import (
	"context"
	"testing"
)

func TestToolKGPathCov_ModesDirectionsAndErrors(t *testing.T) {
	p := newToolPalace(t)
	ctx := mockToolCtx{Context: context.Background()}
	for _, in := range []KGAddToolInput{
		{Subject: "a", Predicate: "knows", Object: "b", ValidFrom: "2026-01-01", ValidTo: "2026-02-01"},
		{Subject: "b", Predicate: "knows", Object: "c", ValidFrom: "2026-01-10"},
		{Subject: "a", Predicate: "knows", Object: "c"},
		{Subject: "a", Predicate: "likes", Object: "d"},
	} {
		if out, _ := palaceKGAddHandler(ctx, p, in); out.Content == "" {
			t.Fatal("add failed")
		}
	}
	run := func(in KGPathToolInput) string {
		out, err := palaceKGPathHandler(ctx, p, in)
		if err != nil {
			t.Fatalf("handler error %v (errors belong in the content)", err)
		}
		return out.Content
	}
	mustContain(t, run(KGPathToolInput{From: "a", To: "c"}), "1 path(s)", "a -[e3]-> c")
	mustContain(t, run(KGPathToolInput{From: "a", To: "c", Mode: "all_shortest", MinHops: 1}), "1 path(s)")
	mustContain(t, run(KGPathToolInput{From: "a", To: "c", Mode: "trails"}), "2 path(s)", "a -[e1]-> b -[e2]-> c")
	mustContain(t, run(KGPathToolInput{From: "a", To: "c", Mode: "trails", Limit: 1}), "1 path(s)")
	mustContain(t, run(KGPathToolInput{From: "a", Mode: "reach", Predicates: []string{"likes"}}), "a -[e4]-> d")
	// time-respecting reports the arrival instant
	mustContain(t, run(KGPathToolInput{From: "a", To: "c", Mode: "trails", TimeRespecting: true}),
		"arrives 2026-01-10T00:00:00Z")
	// backwards from To alone, and direction in
	mustContain(t, run(KGPathToolInput{To: "d"}), "a -[e4]-> d")
	mustContain(t, run(KGPathToolInput{From: "c", Direction: "in", Mode: "shortest"}), "c -[e")
	mustContain(t, run(KGPathToolInput{From: "d", Direction: "both", To: "c", MaxHops: 2}), "d -[e4]-> a")
	// as-of valid time: a>b is gone in March, so only the direct edge remains
	mustContain(t, run(KGPathToolInput{From: "a", To: "c", Mode: "trails", AsOf: "2026-03-01"}), "1 path(s)")
	mustContain(t, run(KGPathToolInput{From: "a", To: "c", TxAsOf: "tx1"}), "No path found.")
	mustContain(t, run(KGPathToolInput{From: "a", To: "c", TxAsOf: "tx3"}), "1 path(s)")
	mustContain(t, run(KGPathToolInput{From: "zzz", To: "c"}), "No path found.")
	// errors
	mustContain(t, run(KGPathToolInput{From: "a", Mode: "fastest"}), "Error:", "unknown path mode")
	mustContain(t, run(KGPathToolInput{From: "a", TxAsOf: "garbage"}), "Error:", "tx_as_of must be a transaction number or a time")
	mustContain(t, run(KGPathToolInput{From: "a", AsOf: "garbage"}), "Error:", "invalid as_of")
	mustContain(t, run(KGPathToolInput{From: "a", Direction: "sideways"}), "Error:", `unknown path direction "sideways"`)
	mustContain(t, run(KGPathToolInput{From: "a", MinHops: 5, MaxHops: 2}), "Error:", "exceeds max hops")
	mustContain(t, run(KGPathToolInput{To: "c", TimeRespecting: true}), "Error:", "needs from")
	mustContain(t, run(KGPathToolInput{MinHops: -1, From: "a"}), "Error:", "must not be negative")
}
