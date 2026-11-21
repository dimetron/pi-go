package cli

import (
	"context"
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
