package palace

import (
	"context"
	"errors"
	"strings"
	"testing"
)

const elpiReports = `
% transitive closure over the manager relation
reports X Y :- tm.triple X manager Y.
reports X Z :- tm.triple X manager Y, reports Y Z.
`

func elpiRows(t *testing.T, v View, prog, goal string) []map[string]Value {
	t.Helper()
	p, err := ParseElpi(prog)
	if err != nil {
		t.Fatalf("ParseElpi: %v", err)
	}
	res, err := v.Elpi(context.Background(), p, goal, ElpiOptions{})
	if err != nil {
		t.Fatalf("Elpi(%s): %v", goal, err)
	}
	return res.Rows
}

func TestElpi_TransitiveClosureAndViews(t *testing.T) {
	kg, _ := newEngine(t)
	ab := assert1(t, kg, Ent("alice"), Ent("manager"), Ent("bob"), Valid{})
	assert1(t, kg, Ent("bob"), Ent("manager"), Ent("carol"), Valid{})
	rows := elpiRows(t, kg.Now(), elpiReports, `reports (iri "alice") Y`)
	if len(rows) != 2 || rows[0]["Y"] != Ent("bob") || rows[1]["Y"] != Ent("carol") {
		t.Fatalf("rows = %v", rows)
	}
	t2, _ := lastTOf(kg)
	tx(t, kg, func(w *KGTx) error { _, err := w.Retract(ab); return err })
	if rows := elpiRows(t, kg.Now(), elpiReports, "reports alice Y"); len(rows) != 0 {
		t.Fatalf("retracted fact still used: %v", rows)
	}
	if rows := elpiRows(t, kg.AsOf(t2), elpiReports, "reports alice Y"); len(rows) != 2 {
		t.Fatalf("as-of rows = %v", rows)
	}
}

func TestElpi_BuiltinsAndNegation(t *testing.T) {
	kg, _ := newEngine(t)
	assert1(t, kg, Ent("ann"), Ent("age"), Int(41), Valid{})
	assert1(t, kg, Ent("ben"), Ent("age"), Int(17), Valid{})
	assert1(t, kg, Ent("ben"), Ent("guardian"), Ent("ann"), Valid{})
	prog := `
adult X :- tm.triple X age A, A > 17.
ward X :- tm.triple X age _, not (adult X).
older X Y :- tm.triple X age A, tm.triple Y age B, A > B, X \= Y.
`
	if rows := elpiRows(t, kg.Now(), prog, "adult X"); len(rows) != 1 || rows[0]["X"] != Ent("ann") {
		t.Fatalf("adult = %v", rows)
	}
	if rows := elpiRows(t, kg.Now(), prog, "ward X"); len(rows) != 1 || rows[0]["X"] != Ent("ben") {
		t.Fatalf("ward = %v", rows)
	}
	if rows := elpiRows(t, kg.Now(), prog, "older X Y"); len(rows) != 1 || rows[0]["Y"] != Ent("ben") {
		t.Fatalf("older = %v", rows)
	}
	if rows := elpiRows(t, kg.Now(), prog, "X = 42"); len(rows) != 1 || rows[0]["X"] != Int(42) {
		t.Fatalf("= = %v", rows)
	}
}

func TestElpi_Errors(t *testing.T) {
	kg, _ := newEngine(t)
	_, err := ParseElpi("reports X Y :- tm.triple X manager Y")
	if !errors.Is(err, ErrElpiProgram) || !strings.Contains(err.Error(), "1:") {
		t.Fatalf("missing dot = %v", err)
	}
	if _, err := ParseElpi(`p F :- F = x\ x.`); !errors.Is(err, ErrElpiProgram) {
		t.Fatalf("lambda = %v", err)
	}
	if _, err := ParseElpi("tm.triple a b c."); !errors.Is(err, ErrElpiProgram) {
		t.Fatalf("builtin redefined = %v", err)
	}
	loop, _ := ParseElpi("loop X :- loop X.")
	if _, err := kg.Now().Elpi(context.Background(), loop, "loop a", ElpiOptions{Fuel: 10_000}); !errors.Is(err, ErrElpiOutOfFuel) {
		t.Fatalf("loop = %v", err)
	}
	if _, err := kg.Now().Elpi(context.Background(), loop, "nope a", ElpiOptions{}); !errors.Is(err, ErrElpiRun) {
		t.Fatalf("unknown predicate = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := kg.Now().Elpi(ctx, loop, "loop a", ElpiOptions{}); err == nil {
		t.Fatal("canceled run succeeded")
	}
}
