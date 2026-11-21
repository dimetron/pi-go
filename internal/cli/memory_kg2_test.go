package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimetron/pi-go/internal/palace"
)

func TestMemoryKG2_SchemaGraphsErase(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "palace.db")
	run := func(args ...string) error {
		cmd := newMemoryKGCmd()
		cmd.SetArgs(append(args, "--db", dbPath))
		return cmd.Execute()
	}
	if err := run("schema", "email", "--set", "--sensitive", "--value-type", "string"); err != nil {
		t.Fatalf("schema: %v", err)
	}
	ctx := context.Background()
	p, err := palace.New(palace.WithDBPath(dbPath))
	if err != nil {
		t.Fatal(err)
	}
	sch, err := p.KG().Schema(ctx, "email")
	if err != nil || !sch.Sensitive || sch.ValueType != "string" {
		t.Fatalf("schema = %+v, %v", sch, err)
	}
	if _, err := p.KG().Add(ctx, palace.TripleInput{Subject: "alice", Predicate: "email", Object: "a@x",
		Literal: true, Graph: "crm"}); err != nil {
		t.Fatal(err)
	}
	p.Close()

	if err := run("graphs"); err != nil {
		t.Fatalf("graphs: %v", err)
	}
	if err := run("erase", "alice"); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("erase without --yes = %v", err)
	}
	if err := run("erase", "alice", "--yes", "--reason", "request"); err != nil {
		t.Fatalf("erase: %v", err)
	}
	p, err = palace.New(palace.WithDBPath(dbPath))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	sts, err := p.KG().Now().InGraph("crm").About(ctx, "alice", "")
	if err != nil || len(sts) != 1 || sts[0].Object != palace.Str(palace.ErasedText) {
		t.Fatalf("after erase = %+v, %v", sts, err)
	}
}

func TestMemoryKG3_SelectPathRulesBundleAnswersVolatile(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "palace.db")
	run := func(args ...string) error {
		cmd := newMemoryKGCmd()
		cmd.SetArgs(append(args, "--db", dbPath))
		return cmd.Execute()
	}
	p, err := palace.New(palace.WithDBPath(dbPath))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, in := range []palace.TripleInput{
		{Subject: "alice", Predicate: "manager", Object: "bob"},
		{Subject: "bob", Predicate: "manager", Object: "carol"},
	} {
		if _, err := p.KG().Add(ctx, in); err != nil {
			t.Fatal(err)
		}
	}
	p.Close()
	prog := filepath.Join(dir, "org.dl")
	if err := os.WriteFile(prog, []byte("reports(X, Y) :- manager(X, Y).\nreports(X, Z) :- manager(X, Y), reports(Y, Z)."), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"select", "SELECT ?b WHERE { <alice> <manager> ?b }", "--save", "mgr"},
		{"select", "MATCH (a)-[:manager]->(b) RETURN a, b", "--lang", "cypher"},
		{"path", "alice", "carol"},
		{"rules", "store", "org", "--file", prog},
		{"rules", "solve", "org", "--goal", "reports(alice, Y)"},
		{"rules", "materialize", "org"},
		{"rules", "check", "org"},
		{"answers", "check", "mgr"},
		{"answers", "list"},
		{"answers", "refresh", "mgr"},
		{"answers", "delete", "mgr"},
		{"volatile", "set", "alice", "last_seen", "today"},
		{"volatile", "get", "alice", "last_seen"},
		{"volatile", "clear", "alice", "last_seen"},
		{"bundle", "export", "e1"},
	} {
		if err := run(args...); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
	p, err = palace.New(palace.WithDBPath(dbPath))
	if err != nil {
		t.Fatal(err)
	}
	b, err := p.KG().Now().Bundle(ctx, 1, palace.BundleOptions{})
	if err != nil {
		t.Fatal(err)
	}
	reports, _ := p.KG().Now().About(ctx, "alice", "subject")
	p.Close()
	if len(reports) != 3 {
		t.Fatalf("materialize did not write: %+v", reports)
	}
	data, _ := json.Marshal(b)
	bundle := filepath.Join(dir, "b.json")
	if err := os.WriteFile(bundle, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run("bundle", "import", bundle); err != nil {
		t.Fatalf("bundle import: %v", err)
	}
	if err := run("rules", "frobnicate", "org"); err == nil {
		t.Fatal("unknown rules action accepted")
	}
}
