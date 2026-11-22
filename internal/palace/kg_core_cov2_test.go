package palace

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestKGCov_ValueStringsAndEncoding(t *testing.T) {
	when := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		v    Value
		want string
	}{
		{Ent("alice"), "alice"}, {Str("hi"), "hi"}, {StmtRef(7), "e7"}, {Int(-3), "-3"},
		{Num(0.25), "0.25"}, {Bool(true), "true"}, {Bool(false), "false"},
		{TimeVal(when), "2026-03-01T12:00:00Z"}, {txVal(9), "tx9"}, {Value{Kind: ValueKind(99)}, ""},
	} {
		if got := c.v.String(); got != c.want {
			t.Errorf("%#v.String() = %q, want %q", c.v, got, c.want)
		}
	}
	if !StmtRef(1).IsStatement() || Ent("a").IsStatement() {
		t.Error("IsStatement")
	}
	if _, _, _, ok := termKey(Value{Kind: ValueKind(99)}); ok {
		t.Error("termKey accepted an unknown kind")
	}
	if placeholders(0) != "" || placeholders(3) != "?,?,?" && placeholders(3) != "?, ?, ?" {
		t.Errorf("placeholders = %q / %q", placeholders(0), placeholders(3))
	}

	kg, _ := newEngine(t)
	for name, v := range map[string]Value{
		"stmt zero": StmtRef(0), "unknown kind": {Kind: ValueKind(99)}, "empty entity": Ent(""),
	} {
		err := txErr(kg, func(w *KGTx) error {
			_, _, err := w.Assert(v, Ent("p"), Ent("o"), Valid{})
			return err
		})
		if err == nil {
			t.Errorf("%s: asserted", name)
		}
	}
	// A subject that is a literal, and a predicate that is not an entity.
	if err := txErr(kg, func(w *KGTx) error { _, _, err := w.Assert(Str("lit"), Ent("p"), Ent("o"), Valid{}); return err }); err == nil {
		t.Error("literal subject accepted")
	}
	if err := txErr(kg, func(w *KGTx) error { _, _, err := w.Assert(Ent("a"), Str("p"), Ent("o"), Valid{}); return err }); err == nil {
		t.Error("literal predicate accepted")
	}
	if err := txErr(kg, func(w *KGTx) error { _, err := w.Retract(4242); return err }); !errors.Is(err, ErrNotLive) {
		t.Errorf("retract of a missing statement: %v", err)
	}
	var rk RetractKind = 42
	for k, want := range map[RetractKind]string{RetExplicit: "retracted", RetCascade: "cascade", RetSupersede: "superseded", RetCardinality: "replaced", rk: "kind(42)"} {
		if k.String() != want {
			t.Errorf("RetractKind(%d) = %q", int(k), k.String())
		}
	}
	tx(t, kg, func(w *KGTx) error {
		if w.T() < 1 {
			t.Errorf("T() = %d", w.T())
		}
		return nil
	})
	for k, want := range map[ValueKind]string{KindEntity: "entity", KindStatement: "statement", KindString: "string",
		KindInt: "integer", KindNumber: "number", KindBool: "bool", KindTime: "time", KindTx: "transaction", ValueKind(99): ""} {
		if kindName(k) != want {
			t.Errorf("kindName(%d) = %q", k, kindName(k))
		}
	}
}

type noDBStore struct{ PalaceStore }

func (noDBStore) DB() *sql.DB { return nil }

func TestKGCov_NoDatabase(t *testing.T) {
	kg := NewKnowledgeGraph(noDBStore{})
	ctx := context.Background()
	if _, err := kg.Transact(ctx, TxOptions{}, func(*KGTx) error { return nil }); !errors.Is(err, ErrKGUnavailable) {
		t.Errorf("Transact: %v", err)
	}
	if _, err := kg.Now().About(ctx, "a", ""); !errors.Is(err, ErrKGUnavailable) {
		t.Errorf("About: %v", err)
	}
	if _, err := kg.AsOfInstant(ctx, time.Now()); !errors.Is(err, ErrKGUnavailable) {
		t.Errorf("AsOfInstant: %v", err)
	}
	if _, err := kg.View(ctx, "2026-01-01", ""); !errors.Is(err, ErrKGUnavailable) {
		t.Errorf("View: %v", err)
	}
}

func TestKGCov_SealMalformedAndWrongKey(t *testing.T) {
	kg, _ := newEngine(t)
	ctx := context.Background()
	q := mustQ(t, kg)
	key := make([]byte, sealKeySize)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	keys := map[objectID][]byte{7: key}
	sealWith := func(k []byte, plain string) string {
		gcm, err := sealCipher(k)
		if err != nil {
			t.Fatal(err)
		}
		iv := make([]byte, gcm.NonceSize())
		ct := gcm.Seal(nil, iv, []byte(plain), []byte("7"))
		return "7:" + base64.RawStdEncoding.EncodeToString(append(iv, ct...))
	}
	good := sealWith(key, `{"Kind":3,"Text":"x"}`)
	if v, err := openSealed(ctx, q, good, keys); err != nil || v.Text != "x" {
		t.Fatalf("good sealed term = %+v, %v", v, err)
	}
	wrong := make([]byte, sealKeySize)
	copy(wrong, key)
	wrong[0] ^= 0xff
	for name, tc := range map[string]struct {
		lex  string
		keys map[objectID][]byte
		want string
	}{
		"no colon":     {"nocolon", keys, "malformed"},
		"bad subject":  {"x:AAAA", keys, "malformed"},
		"bad base64":   {"7:!!!", keys, "malformed"},
		"too short":    {"7:AAAA", keys, "malformed"},
		"wrong key":    {good, map[objectID][]byte{7: wrong}, "unseal"},
		"not json":     {sealWith(key, "not json"), keys, "unseal"},
		"short key":    {good, map[objectID][]byte{7: {1, 2, 3}}, "bad seal key"},
		"erased/unset": {"9:AAAA", map[objectID][]byte{9: nil}, ""},
	} {
		v, err := openSealed(ctx, q, tc.lex, tc.keys)
		if tc.want == "" {
			if err != nil || v.Text != ErasedText {
				t.Errorf("%s: = %+v, %v", name, v, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", name, err, tc.want)
		}
	}
	if _, err := sealCipher(nil); err == nil {
		t.Error("sealCipher accepted an empty key")
	}
	if _, err := sealLex(nil, 1, Str("x")); err == nil {
		t.Error("sealLex accepted an empty key")
	}
	// A statement id that names nothing has no data subject.
	if _, err := dataSubject(ctx, q, makeOID(9999, tagStmt)); err == nil {
		t.Error("dataSubject of a missing statement")
	}
	if _, _, err := sealedLookup(ctx, q, makeOID(9999, tagStmt), Str("x")); err == nil {
		t.Error("sealedLookup of a missing statement")
	}
}

func TestKGCov_EraseEdges(t *testing.T) {
	ctx := context.Background()
	kg, _ := newEngine(t)
	tx(t, kg, func(w *KGTx) error { return w.SetPredicate(Ent("email"), PredicateSchema{Sensitive: true}) })
	e := assert1(t, kg, Ent("alice"), Ent("email"), Str("a@secret.example"), Valid{})
	// Annotating a sealed fact seals under the same subject.
	tx(t, kg, func(w *KGTx) error {
		if err := w.SetPredicate(Ent("remark"), PredicateSchema{Sensitive: true}); err != nil {
			return err
		}
		_, _, err := w.Annotate(e, Ent("remark"), Str("shh"))
		return err
	})
	// Unknown subject: nothing to erase, and nothing was created.
	rep, err := kg.Erase(ctx, TxOptions{}, "nobody")
	if err != nil || rep.Erased || rep.Checkpointed {
		t.Fatalf("erase of unknown = %+v, %v", rep, err)
	}
	// A dry run reports the erasure but changes nothing.
	rep, err = kg.Erase(ctx, TxOptions{DryRun: true}, "alice")
	if err != nil || !rep.Erased || rep.Checkpointed {
		t.Fatalf("dry-run erase = %+v, %v", rep, err)
	}
	got, _ := kg.Now().About(ctx, "alice", "")
	if len(got) != 1 || got[0].Object != Str("a@secret.example") {
		t.Fatalf("dry run erased: %+v", got)
	}
	// A saved answer that quotes the subject is dropped.
	if _, err := kg.SaveAnswer(ctx, "mine", "sparql", "SELECT ?x WHERE { <alice> <knows> ?x }", kg.Now()); err != nil {
		t.Fatalf("SaveAnswer: %v", err)
	}
	rep, err = kg.Erase(ctx, TxOptions{Author: "dpo"}, "alice")
	if err != nil || !rep.Erased {
		t.Fatalf("erase = %+v, %v", rep, err)
	}
	// Annotation on the erased fact reads as erased too.
	if ls, err := kg.Now().Layers(ctx, e); err != nil || len(ls) == 0 {
		t.Fatalf("layers = %+v, %v", ls, err)
	}
}

func TestKGCov_VolatileGuards(t *testing.T) {
	kg, _ := newEngine(t)
	alice, seen := Ent("alice"), Ent("seen")
	e := assert1(t, kg, alice, Ent("knows"), Ent("bob"), Valid{})
	if err := txErr(kg, func(w *KGTx) error { return w.SetVolatile(Str("lit"), seen, Int(1)) }); err == nil ||
		!strings.Contains(err.Error(), "volatile subject") {
		t.Errorf("literal subject: %v", err)
	}
	for name, key := range map[string]Value{"literal key": Str("k"), "engine key": sysPred(predSupersedes), "statement key": StmtRef(e)} {
		if err := txErr(kg, func(w *KGTx) error { return w.SetVolatile(alice, key, Int(1)) }); err == nil ||
			!strings.Contains(err.Error(), "volatile key") {
			t.Errorf("%s: %v", name, err)
		}
	}
	// A statement can carry a volatile value; the subject-less clear is a no-op.
	tx(t, kg, func(w *KGTx) error { return w.SetVolatile(StmtRef(e), seen, Str("x")) })
	got, err := kg.Now().Resolve(context.Background(), StmtRef(e), seen)
	if err != nil || len(got) != 1 || got[0].Value != Str("x") || got[0].UpdatedAt == nil {
		t.Fatalf("resolve on a statement = %+v, %v", got, err)
	}
	tx(t, kg, func(w *KGTx) error { return w.ClearVolatile(Ent("never-seen"), Ent("never-key")) })
	// A valid-time or graph view never shows volatile values.
	if got, _ := kg.Now().ValidAt(time.Now()).Resolve(context.Background(), StmtRef(e), seen); len(got) != 0 {
		t.Errorf("volatile visible under ValidAt: %+v", got)
	}
	if got, _ := kg.Now().InGraph("g").Resolve(context.Background(), StmtRef(e), seen); len(got) != 0 {
		t.Errorf("volatile visible in a graph view: %+v", got)
	}
}

func TestKGCov_GraphEdges(t *testing.T) {
	ctx := context.Background()
	kg, _ := newEngine(t)
	e := assert1(t, kg, Ent("a"), Ent("p"), Ent("b"), Valid{})
	for _, g := range []string{"", "  "} {
		if err := txErr(kg, func(w *KGTx) error { _, _, err := w.AddToGraph(e, g); return err }); !errors.Is(err, ErrEmptyGraph) {
			t.Errorf("AddToGraph(%q): %v", g, err)
		}
		if err := txErr(kg, func(w *KGTx) error { _, err := w.RemoveFromGraph(e, g); return err }); !errors.Is(err, ErrEmptyGraph) {
			t.Errorf("RemoveFromGraph(%q): %v", g, err)
		}
		if err := txErr(kg, func(w *KGTx) error { _, err := w.DropGraph(g); return err }); !errors.Is(err, ErrEmptyGraph) {
			t.Errorf("DropGraph(%q): %v", g, err)
		}
	}
	if err := txErr(kg, func(w *KGTx) error { _, _, err := w.AddToGraph(9999, "g"); return err }); !errors.Is(err, ErrNotLive) {
		t.Errorf("AddToGraph of a missing statement: %v", err)
	}
	tx(t, kg, func(w *KGTx) error {
		if ok, err := w.RemoveFromGraph(e, "never"); err != nil || ok {
			t.Errorf("RemoveFromGraph of a non-member = %v, %v", ok, err)
		}
		if n, err := w.DropGraph("never"); err != nil || n != 0 {
			t.Errorf("DropGraph of an empty graph = %d, %v", n, err)
		}
		return nil
	})
	tx(t, kg, func(w *KGTx) error { _, _, err := w.AddToGraph(e, "g"); return err })
	if gs, err := kg.Now().GraphsOf(ctx, e); err != nil || len(gs) != 1 || gs[0] != "g" {
		t.Errorf("GraphsOf = %v, %v", gs, err)
	}
	if gs, err := kg.Now().ListGraphs(ctx); err != nil || len(gs) != 1 {
		t.Errorf("ListGraphs = %+v, %v", gs, err)
	}
	if gs, err := kg.Now().GraphsOf(ctx, 9999); err != nil || len(gs) != 0 {
		t.Errorf("GraphsOf of a missing statement = %v, %v", gs, err)
	}
	tx(t, kg, func(w *KGTx) error {
		if n, err := w.DropGraph("g"); err != nil || n != 1 {
			t.Errorf("DropGraph = %d, %v", n, err)
		}
		return nil
	})
	if gs, _ := kg.Now().ListGraphs(ctx); len(gs) != 0 {
		t.Errorf("graph survived a drop: %+v", gs)
	}
}

func TestKGCov_PredicateSchemaFlags(t *testing.T) {
	ctx := context.Background()
	kg, _ := newEngine(t)
	set := func(p string, sch PredicateSchema) {
		t.Helper()
		tx(t, kg, func(w *KGTx) error { return w.SetPredicate(Ent(p), sch) })
	}
	read := func(p string) PredicateSchema {
		t.Helper()
		sch, err := kg.Schema(ctx, p)
		if err != nil {
			t.Fatal(err)
		}
		return sch
	}
	all := PredicateSchema{One: true, Unique: true, ValueType: "entity", SubjectType: "entity", IsEdge: true}
	set("rel", all)
	if got := read("rel"); got != all {
		t.Fatalf("schema = %+v, want %+v", got, all)
	}
	// Re-setting the same schema changes nothing; replacing it unsets the rest.
	set("rel", all)
	only := PredicateSchema{SubjectType: "statement"}
	set("rel", only)
	if got := read("rel"); got != only {
		t.Fatalf("replaced schema = %+v, want %+v", got, only)
	}
	set("rel", PredicateSchema{})
	if got := read("rel"); got != (PredicateSchema{}) {
		t.Fatalf("cleared schema = %+v", got)
	}
	// Each flag alone.
	for i, sch := range []PredicateSchema{{One: true}, {Unique: true}, {ValueType: "bool"}, {SubjectType: "entity"}, {IsEdge: true}, {Sensitive: true}} {
		p := "flag" + strconv.Itoa(i)
		set(p, sch)
		if got := read(p); got != sch {
			t.Errorf("%s: %+v, want %+v", p, got, sch)
		}
	}
	// A predicate nobody described has the zero schema.
	if got := read("unknown"); got != (PredicateSchema{}) {
		t.Errorf("unknown predicate schema = %+v", got)
	}

	for name, f := range map[string]func(w *KGTx) error{
		"reserved":     func(w *KGTx) error { return w.SetPredicate(sysPred(predSupersedes), PredicateSchema{One: true}) },
		"non-entity":   func(w *KGTx) error { return w.SetPredicate(Str("p"), PredicateSchema{One: true}) },
		"bad subject":  func(w *KGTx) error { return w.SetPredicate(Ent("q"), PredicateSchema{SubjectType: "bogus"}) },
		"bad value":    func(w *KGTx) error { return w.SetPredicate(Ent("q"), PredicateSchema{ValueType: "bogus"}) },
		"upsert-sys":   func(w *KGTx) error { _, _, _, err := w.Upsert(sysPred(predUnique), Str("x")); return err },
		"upsert-str":   func(w *KGTx) error { _, _, _, err := w.Upsert(Str("p"), Str("x")); return err },
		"upsert-plain": func(w *KGTx) error { _, _, _, err := w.Upsert(Ent("plain"), Str("x")); return err },
		"derived-none": func(w *KGTx) error { _, _, _, err := w.UpsertDerived(Ent("k"), Str("x"), nil); return err },
	} {
		if err := txErr(kg, f); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	mustErr(t, txErr(kg, func(w *KGTx) error { _, _, _, err := w.Upsert(Ent("plain"), Str("x")); return err }), ErrNotUnique)
	mustErr(t, txErr(kg, func(w *KGTx) error { _, _, _, err := w.UpsertDerived(Ent("k"), Str("x"), nil); return err }), ErrNoSupport)
	mustErr(t, txErr(kg, func(w *KGTx) error { return w.SetPredicate(sysPred(predSupersedes), PredicateSchema{}) }), ErrReservedPredicate)
	mustErr(t, txErr(kg, func(w *KGTx) error { _, _, _, err := w.Upsert(sysPred(predUnique), Str("x")); return err }), ErrReservedPredicate)

	// SubjectType: statement admits annotations only.
	set("annot", PredicateSchema{SubjectType: "statement"})
	e := assert1(t, kg, Ent("a"), Ent("p"), Ent("b"), Valid{})
	tx(t, kg, func(w *KGTx) error { _, _, err := w.Annotate(e, Ent("annot"), Str("x")); return err })
	mustErr(t, txErr(kg, func(w *KGTx) error { _, _, err := w.Assert(Ent("a"), Ent("annot"), Str("x"), Valid{}); return err }), ErrSchemaViolation)
	// ... and the reverse, with live data that already breaks it.
	mustErr(t, txErr(kg, func(w *KGTx) error { return w.SetPredicate(Ent("p"), PredicateSchema{SubjectType: "statement"}) }), ErrSchemaViolation)
	mustErr(t, txErr(kg, func(w *KGTx) error { return w.SetPredicate(Ent("annot"), PredicateSchema{SubjectType: "entity"}) }), ErrSchemaViolation)
	mustErr(t, txErr(kg, func(w *KGTx) error { return w.SetPredicate(Ent("p"), PredicateSchema{ValueType: "int"}) }), ErrSchemaViolation)

	// Unique and cardinality-one data cannot be declared over breaking data.
	assert1(t, kg, Ent("c"), Ent("p"), Ent("b"), Valid{})
	mustErr(t, txErr(kg, func(w *KGTx) error { return w.SetPredicate(Ent("p"), PredicateSchema{Unique: true}) }), ErrSchemaViolation)
	assert1(t, kg, Ent("a"), Ent("p"), Ent("z"), Valid{})
	mustErr(t, txErr(kg, func(w *KGTx) error { return w.SetPredicate(Ent("p"), PredicateSchema{One: true}) }), ErrSchemaViolation)
	// A volatile plaintext value blocks sensitivity.
	tx(t, kg, func(w *KGTx) error { return w.SetVolatile(Ent("a"), Ent("vk"), Str("plain")) })
	mustErr(t, txErr(kg, func(w *KGTx) error { return w.SetPredicate(Ent("vk"), PredicateSchema{Sensitive: true}) }), ErrSchemaViolation)

	// Cardinality one replaces; unique upsert returns the same node.
	set("home", PredicateSchema{One: true})
	h1 := assert1(t, kg, Ent("a"), Ent("home"), Ent("x"), Valid{})
	assert1(t, kg, Ent("a"), Ent("home"), Ent("y"), Valid{})
	if live(t, kg, h1) {
		t.Error("cardinality one did not replace the old object")
	}
	set("key", PredicateSchema{Unique: true})
	var n1, n2 Value
	var new1, new2 bool
	tx(t, kg, func(w *KGTx) error { var err error; n1, _, new1, err = w.Upsert(Ent("key"), Str("k")); return err })
	tx(t, kg, func(w *KGTx) error { var err error; n2, _, new2, err = w.Upsert(Ent("key"), Str("k")); return err })
	if !new1 || new2 || n1 != n2 {
		t.Errorf("upsert: %v/%v then %v/%v", n1, new1, n2, new2)
	}
	// Sensitive predicates refuse derived upserts.
	set("pii", PredicateSchema{Sensitive: true})
	mustErr(t, txErr(kg, func(w *KGTx) error { _, _, _, err := w.UpsertDerived(Ent("pii"), Str("x"), []int64{e}); return err }), ErrSchemaViolation)
}

func TestKGCov_FacadeAndViews(t *testing.T) {
	ctx := context.Background()
	kg, _ := newEngine(t)
	if _, err := kg.QueryWith(ctx, KGQuery{}); err == nil {
		t.Error("QueryWith without entity or text")
	}
	if _, err := kg.Query(ctx, "", "", ""); err == nil {
		t.Error("Query without entity")
	}
	if _, err := kg.Timeline(ctx, ""); err == nil {
		t.Error("Timeline without entity")
	}
	if err := kg.Invalidate(ctx, "", "p", "o"); err == nil {
		t.Error("Invalidate without subject")
	}
	if _, err := kg.View(ctx, "garbage", ""); err == nil {
		t.Error("View with a bad tx_as_of")
	}
	if _, err := kg.View(ctx, "", "garbage"); err == nil {
		t.Error("View with a bad as_of")
	}
	for _, in := range []TripleInput{{}, {Subject: "a"}, {Subject: "a", Predicate: "p"}, {About: 5, Predicate: "p"}} {
		if _, err := kg.Add(ctx, in); err == nil {
			t.Errorf("Add(%+v) accepted", in)
		}
	}
	for _, name := range []string{"a1", "a2", "a3"} {
		if _, err := kg.Add(ctx, TripleInput{Subject: "alice", Predicate: "likes", Object: name}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := kg.QueryWith(ctx, KGQuery{Entity: "alice", Direction: "subject", Limit: 2})
	if err != nil || len(got) != 2 || got[0].ObjectID != "a2" || got[1].ObjectID != "a3" {
		t.Fatalf("limited query = %+v, %v", got, err)
	}
	got, err = kg.QueryWith(ctx, KGQuery{Text: "a2", Limit: 1})
	if err != nil || len(got) != 1 || got[0].ObjectID != "a2" {
		t.Fatalf("text query = %+v, %v", got, err)
	}
	// Named graphs restrict the facts.
	e := assert1(t, kg, Ent("carol"), Ent("likes"), Ent("tea"), Valid{})
	tx(t, kg, func(w *KGTx) error { _, _, err := w.AddToGraph(e, "prefs"); return err })
	if got, err := kg.QueryWith(ctx, KGQuery{Entity: "carol", Graph: "prefs"}); err != nil || len(got) != 1 {
		t.Fatalf("graph query = %+v, %v", got, err)
	}
	if got, err := kg.QueryWith(ctx, KGQuery{Entity: "alice", Graph: "prefs"}); err != nil || len(got) != 0 {
		t.Fatalf("graph query for another entity = %+v, %v", got, err)
	}
	// A tx_as_of given as a time resolves to the transactions then.
	if v, err := kg.View(ctx, time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339), ""); err != nil || v.mode != viewAsOf {
		t.Fatalf("View by time = %+v, %v", v, err)
	}
	if v, err := kg.View(ctx, "tx2", "2026-01-01"); err != nil || v.t != 2 || v.validAt == nil {
		t.Fatalf("View by tx number = %+v, %v", v, err)
	}

	// Invalidate: an open fact is closed; a fact that never held is retracted;
	// an already closed one is left alone.
	future := *day("2030-01-01")
	fe := assert1(t, kg, Ent("dan"), Ent("knows"), Ent("eve"), Valid{From: &future})
	ce := assert1(t, kg, Ent("dan"), Ent("knows"), Ent("fay"), Valid{From: day("2019-01-01"), To: day("2020-01-01")})
	oe := assert1(t, kg, Ent("dan"), Ent("knows"), Ent("gus"), Valid{From: day("2025-12-01")})
	for _, o := range []string{"eve", "fay", "gus"} {
		if err := kg.InvalidateWith(ctx, "dan", "knows", o, TxOptions{Author: "t", Reason: "r"}); err != nil {
			t.Fatal(err)
		}
	}
	if live(t, kg, fe) {
		t.Error("a fact that never held stayed live")
	}
	if !live(t, kg, ce) {
		t.Error("a closed fact was touched")
	}
	if live(t, kg, oe) {
		t.Error("an open fact was not superseded")
	}
	tl, err := kg.Timeline(ctx, "dan")
	if err != nil {
		t.Fatal(err)
	}
	statuses := map[string]int{}
	for _, tr := range tl {
		statuses[tr.Status]++
	}
	if statuses["superseded"] != 1 || statuses["retracted"] != 1 || statuses["ended"] != 2 {
		t.Errorf("timeline statuses = %v", statuses)
	}

	// Views describe themselves.
	v := kg.AsOf(3).ValidAt(*day("2026-03-01")).InGraph("a", "b")
	if s := v.String(); s != "as of tx3, valid at 2026-03-01T00:00:00Z, in graph a|b" {
		t.Errorf("String = %q", s)
	}
	if kg.History().String() != "history" || kg.Now().String() != "now" {
		t.Error("view names")
	}
	if !kg.History().IsHistory() || kg.Now().IsHistory() {
		t.Error("IsHistory")
	}
	if gs := v.Graphs(); len(gs) != 2 || gs[0] != "a" {
		t.Errorf("Graphs = %v", gs)
	}
	if _, err := kg.Get(ctx, 99999); err != nil {
		t.Logf("Get of a missing statement: %v", err)
	}
	if _, err := kg.Now().Match(ctx, Pattern{Limit: 1}); err != nil {
		t.Error(err)
	}
}

// A sealed term that cannot be opened stops an erasure instead of leaving a
// saved answer behind, and an empty sealed value is skipped.
func TestKGCov_EraseWithCorruptSealedTerm(t *testing.T) {
	ctx := context.Background()
	kg, db := newEngine(t)
	tx(t, kg, func(w *KGTx) error { return w.SetPredicate(Ent("email"), PredicateSchema{Sensitive: true}) })
	tx(t, kg, func(w *KGTx) error { return w.SetPredicate(Ent("nick"), PredicateSchema{Sensitive: true}) })
	assert1(t, kg, Ent("alice"), Ent("email"), Str("a@secret.example"), Valid{})
	assert1(t, kg, Ent("alice"), Ent("nick"), Str(""), Valid{})
	var subject int64
	if err := db.QueryRow("SELECT subject FROM kg_seal_key").Scan(&subject); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO kg_term (tag, lex) VALUES (?, ?)", tagSealed, strconv.FormatInt(subject, 10)+":!!!not-base64"); err != nil {
		t.Fatalf("plant a corrupt term: %v", err)
	}
	if _, err := kg.Erase(ctx, TxOptions{}, "alice"); err == nil || !strings.Contains(err.Error(), "malformed sealed term") {
		t.Fatalf("erase over a corrupt term: %v", err)
	}
	// Nothing was destroyed: the key is intact and the values still decode.
	got, err := kg.Now().About(ctx, "alice", "")
	if err != nil || len(got) != 2 {
		t.Fatalf("after a refused erase: %+v, %v", got, err)
	}
}
