package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimetron/pi-go/internal/palace"
)

// kgCLI runs `kg <args> --db <path>` and returns what it printed.
type kgCLI struct {
	t  *testing.T
	db string
}

func newKGCLI(t *testing.T) *kgCLI {
	t.Helper()
	db := filepath.Join(t.TempDir(), "palace.db")
	p, err := palace.New(palace.WithDBPath(db))
	if err != nil {
		t.Fatal(err)
	}
	p.Close()
	return &kgCLI{t: t, db: db}
}

func (k *kgCLI) run(args ...string) (string, error) {
	k.t.Helper()
	var err error
	out := captureStdout(k.t, func() {
		cmd := newMemoryKGCmd()
		cmd.SilenceUsage, cmd.SilenceErrors = true, true
		cmd.SetArgs(append(args, "--db", k.db))
		err = cmd.Execute()
	})
	return out, err
}

func (k *kgCLI) ok(args ...string) string {
	k.t.Helper()
	out, err := k.run(args...)
	if err != nil {
		k.t.Fatalf("%v: %v", args, err)
	}
	return out
}

func (k *kgCLI) fails(want string, args ...string) {
	k.t.Helper()
	_, err := k.run(args...)
	if err == nil {
		k.t.Fatalf("%v succeeded, want an error containing %q", args, want)
	}
	if !strings.Contains(err.Error(), want) {
		k.t.Fatalf("%v: error = %v, want it to contain %q", args, err, want)
	}
}

func (k *kgCLI) seed(inputs ...palace.TripleInput) {
	k.t.Helper()
	p, err := palace.New(palace.WithDBPath(k.db))
	if err != nil {
		k.t.Fatal(err)
	}
	defer p.Close()
	for _, in := range inputs {
		if _, err := p.KG().Add(context.Background(), in); err != nil {
			k.t.Fatal(err)
		}
	}
}

func has(t *testing.T, out string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(out, w) {
			t.Errorf("output lacks %q:\n%s", w, out)
		}
	}
}

// brokenDB is a database path whose parent is a regular file, so opening the
// palace fails on every platform.
func brokenDB(t *testing.T) string {
	t.Helper()
	f := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(f, "palace.db")
}

func TestMemoryKGCov_OpenFailures(t *testing.T) {
	bad := brokenDB(t)
	for _, args := range [][]string{
		{"query", "a"}, {"add", "a", "p", "b"}, {"timeline", "a"}, {"explain", "e1"}, {"conflicts"}, {"log"},
		{"graphs"}, {"schema", "p"}, {"erase", "a", "--yes"}, {"select", "SELECT ?x WHERE { ?x ?p ?o }"},
		{"path", "a"}, {"rules", "check", "r"}, {"bundle", "export", "e1"}, {"answers", "list"},
		{"volatile", "get", "a", "k"},
	} {
		cmd := newMemoryKGCmd()
		cmd.SilenceUsage, cmd.SilenceErrors = true, true
		cmd.SetArgs(append(args, "--db", bad))
		var err error
		captureStdout(t, func() { err = cmd.Execute() })
		if err == nil || !strings.Contains(err.Error(), "opening palace") {
			t.Errorf("%v: error = %v, want an opening-palace failure", args, err)
		}
	}
}

func TestMemoryKGCov_ReadCommands(t *testing.T) {
	k := newKGCLI(t)
	has(t, k.ok("query", "ghost"), `No triples found for "ghost".`)
	has(t, k.ok("timeline", "ghost"), `No timeline entries for "ghost".`)
	has(t, k.ok("conflicts"), "No conflicting facts.")
	has(t, k.ok("log"), "No changes.")
	has(t, k.ok("graphs"), "No named graphs.")

	k.fails("kg add", "add", "alice", "knows", "")
	k.fails("parsing --valid-from", "add", "alice", "knows", "bob", "--valid-from", "yesterday")
	has(t, k.ok("add", "alice", "knows", "bob", "--valid-from", "2020-01-01"), "Added triple: e1")
	has(t, k.ok("add", "alice", "uses", "postgres"), "Added triple")
	has(t, k.ok("add", "alice", "uses", "mysql"), "Added triple")

	// Valid-time columns and the as-of filter.
	out := k.ok("query", "alice", "--direction", "subject")
	has(t, out, `Triples for "alice"`, "2020-01-01", "active", "postgres", "mysql")
	has(t, k.ok("query", "alice", "--as-of", "2019-01-01"), "uses")
	k.fails("kg query", "query", "alice", "--tx-as-of", "garbage")
	k.fails("kg query", "query", "alice", "--as-of", "garbage")

	// Close one fact, then supersede nothing: the timeline reports each status.
	p, err := palace.New(palace.WithDBPath(k.db))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := p.KG().Invalidate(ctx, "alice", "knows", "bob"); err != nil {
		t.Fatal(err)
	}
	p.Close()
	out = k.ok("timeline", "alice")
	has(t, out, `Timeline for "alice"`, "superseded by e", "(from 2020-01-01)", "ended")

	// Explain, with the new statement and the error paths.
	k.fails("invalid statement id", "explain", "bogus")
	k.fails("invalid statement id", "explain", "e0")
	k.fails("no statement e999", "explain", "999")
	k.fails("", "explain", "1", "--tx-as-of", "garbage")
	has(t, k.ok("explain", "e2"), "## e2: alice → uses → postgres", "Status: live")

	// Conflicts: two databases for alice.
	has(t, k.ok("conflicts", "--subject", "alice", "--predicate", "uses"), "Conflicts (1)", "postgres", "mysql")
	has(t, k.ok("conflicts", "--subject", "nobody"), "No conflicting facts.")

	// The log shows the assertion and retraction kinds.
	out = k.ok("log")
	has(t, out, "tx1", "+ e1", "- (superseded)")
	has(t, k.ok("log", "--since", "999"), "No changes.")
}

func TestMemoryKGCov_SchemaGraphsErase(t *testing.T) {
	k := newKGCLI(t)
	has(t, k.ok("schema", "knows"), `knows: one=false unique=false value_type="" subject_type="" is_edge=false sensitive=false`)
	has(t, k.ok("schema", "spouse", "--set", "--one", "--unique", "--edge", "--subject-type", "entity"),
		"one=true unique=true", `subject_type="entity"`, "is_edge=true")
	// --set replaces: the omitted flags are cleared.
	has(t, k.ok("schema", "spouse", "--set", "--value-type", "string"), `value_type="string"`, "one=false", "unique=false")
	has(t, k.ok("schema", "spouse"), `value_type="string"`)
	k.fails("", "schema", "spouse", "--set", "--value-type", "bogus")
	k.fails("", "schema", "spouse", "--set", "--subject-type", "bogus")
	k.fails("accepts 1 arg", "schema")

	has(t, k.ok("erase", "nobody", "--yes"), "nobody has no sensitive values to erase.")
	k.fails("--yes", "erase", "nobody")
	k.fails("accepts 1 arg", "erase", "--yes")

	k.ok("schema", "email", "--set", "--sensitive")
	k.seed(palace.TripleInput{Subject: "alice", Predicate: "email", Object: "a@x.example", Literal: true, Graph: "crm"})
	has(t, k.ok("graphs"), "crm")
	out := k.ok("erase", "alice", "--yes", "--reason", "gdpr")
	has(t, out, "Erased alice at tx")
}

func TestMemoryKGCov_SelectAndPath(t *testing.T) {
	k := newKGCLI(t)
	k.seed(
		palace.TripleInput{Subject: "alice", Predicate: "manager", Object: "bob", Graph: "org"},
		palace.TripleInput{Subject: "bob", Predicate: "manager", Object: "carol", Graph: "org"},
	)
	q := "SELECT ?b WHERE { <alice> <manager> ?b }"
	has(t, k.ok("select", q), "bob")
	has(t, k.ok("select", q, "--graph", "org"), "bob")
	out := k.ok("select", q, "--graph", "elsewhere")
	if strings.Contains(out, "bob") {
		t.Errorf("graph scope ignored: %s", out)
	}
	has(t, k.ok("select", q, "--save", "mgr", "--tx-as-of", "tx2", "--as-of", "2030-01-01"), "Saved mgr")
	k.fails("", "select", q, "--tx-as-of", "garbage")
	k.fails("", "select", q, "--lang", "gremlin")
	k.fails("", "select", q, "--lang", "gremlin", "--save", "x")
	k.fails("", "select", "SELECT nonsense")

	has(t, k.ok("path", "alice", "carol"), "alice -[e", "carol")
	has(t, k.ok("path", "alice", "carol", "--graph", "org", "--mode", "all_shortest", "--predicate", "manager", "--time-respecting", "--max-hops", "3"), "carol")
	has(t, k.ok("path", "alice", "--mode", "reach", "--direction", "out"), "bob")
	has(t, k.ok("path", "carol", "alice"), "No path found.")
	has(t, k.ok("path", "carol", "alice", "--graph", "elsewhere"), "No path found.")
	k.fails("", "path", "alice", "carol", "--mode", "teleport")
	k.fails("", "path", "alice", "carol", "--direction", "sideways")
}

func TestMemoryKGCov_Rules(t *testing.T) {
	k := newKGCLI(t)
	k.seed(
		palace.TripleInput{Subject: "alice", Predicate: "manager", Object: "bob"},
		palace.TripleInput{Subject: "bob", Predicate: "manager", Object: "alice"},
	)
	dir := t.TempDir()
	write := func(name, text string) string {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	dl := write("org.dl", "reports(X, Y) :- manager(X, Y).\n:- manager(X, Y), manager(Y, X).")
	elpi := write("org.elpi", "reports X Y :- tm.triple X manager Y.")
	n3 := write("org.n3", "@prefix : <http://example.org/> .\n{ ?x :manager ?y } => { ?x :reports ?y } .")
	badElpi := write("bad.elpi", "reports X Y :- tm.triple X manager Y")

	has(t, k.ok("rules", "store", "org", "--file", dl), "Stored org as e")
	k.fails("missing.dl", "rules", "store", "org", "--file", filepath.Join(dir, "missing.dl")) // OS-neutral: the message names the file on every platform

	out := k.ok("rules", "check", "org")
	has(t, out, "witnesses")
	has(t, k.ok("rules", "check", "x", "--file", write("ok.dl", "reports(X, Y) :- manager(X, Y).")), "No constraint violations.")
	k.fails("", "rules", "check", "missing")
	k.fails("", "rules", "solve", "missing", "--goal", "reports(alice, Y)")

	has(t, k.ok("rules", "solve", "org", "--goal", "reports(alice, Y)"), "bob")
	has(t, k.ok("rules", "solve", "n3", "--syntax", "n3", "--file", n3, "--goal", "reports(alice, Y)"), "bob")
	has(t, k.ok("rules", "solve", "e", "--syntax", "elpi", "--file", elpi, "--goal", "reports alice Y"), "bob")
	k.fails("", "rules", "solve", "e", "--syntax", "elpi", "--file", badElpi, "--goal", "reports alice Y")
	k.fails("", "rules", "solve", "e", "--syntax", "elpi", "--file", elpi, "--goal", "nope alice")
	k.fails("", "rules", "solve", "org", "--goal", "((")
	k.fails("", "rules", "solve", "bad", "--syntax", "cobol", "--file", dl, "--goal", "reports(alice, Y)")

	has(t, k.ok("rules", "materialize", "org", "--dry-run"), "org: ", "new")
	k.fails("", "rules", "materialize", "missing")
	k.fails("unknown rules action", "rules", "dance", "org")
	k.fails("accepts 2 arg", "rules", "store")
}

func TestMemoryKGCov_BundleAnswersVolatile(t *testing.T) {
	k := newKGCLI(t)
	k.seed(palace.TripleInput{Subject: "alice", Predicate: "manager", Object: "bob"})
	dir := t.TempDir()

	// Bundles.
	has(t, k.ok("bundle", "export", "e1"), `"version": 1`)
	k.fails("invalid statement id", "bundle", "export", "zero")
	k.fails("", "bundle", "export", "e1", "--tx-as-of", "garbage")
	k.fails("", "bundle", "export", "e999")
	k.fails("unknown bundle action", "bundle", "sync", "e1")
	k.fails("", "bundle", "import", filepath.Join(dir, "missing.json"))
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	k.fails("", "bundle", "import", bad)
	old := filepath.Join(dir, "old.json")
	if err := os.WriteFile(old, []byte(`{"version":99}`), 0o600); err != nil {
		t.Fatal(err)
	}
	k.fails("unsupported bundle version", "bundle", "import", old)
	good := filepath.Join(dir, "good.json")
	if err := os.WriteFile(good, []byte(k.ok("bundle", "export", "e1", "--include-sensitive")), 0o600); err != nil {
		t.Fatal(err)
	}
	has(t, k.ok("bundle", "import", good), "Imported: 0 new, 1 already known.")

	// Saved answers.
	has(t, k.ok("answers", "list"), "")
	k.ok("select", "SELECT ?b WHERE { <alice> <manager> ?b }", "--save", "mgr")
	has(t, k.ok("answers", "list"), "mgr", "sparql", "fresh")
	has(t, k.ok("answers", "check", "mgr"), "mgr is fresh")
	k.seed(palace.TripleInput{Subject: "alice", Predicate: "manager", Object: "carol"})
	has(t, k.ok("answers", "check", "mgr"), "mgr is stale:")
	has(t, k.ok("answers", "list"), "stale:")
	has(t, k.ok("answers", "refresh", "mgr"), "mgr is fresh", "carol")
	k.fails("needs a name", "answers", "check")
	k.fails("unknown answers action", "answers", "explode", "mgr")
	k.fails("", "answers", "check", "nope")
	k.fails("", "answers", "refresh", "nope")
	k.ok("answers", "delete", "nope") // deleting what is not there is a no-op
	k.ok("answers", "delete", "mgr")
	if out := k.ok("answers", "list"); strings.Contains(out, "mgr") {
		t.Errorf("answer survived a delete: %s", out)
	}

	// Volatile state.
	k.fails("set needs a value", "volatile", "set", "alice", "seen")
	k.fails("unknown volatile action", "volatile", "wipe", "alice", "seen")
	if out := k.ok("volatile", "get", "alice", "seen"); strings.TrimSpace(out) != "" {
		t.Errorf("get of an unset value printed %q", out)
	}
	k.ok("volatile", "set", "alice", "seen", "today")
	if out := k.ok("volatile", "get", "alice", "seen"); strings.TrimSpace(out) != "today" {
		t.Errorf("get = %q", out)
	}
	k.ok("volatile", "clear", "alice", "seen")
	if out := k.ok("volatile", "get", "alice", "seen"); strings.TrimSpace(out) != "" {
		t.Errorf("cleared value printed %q", out)
	}
}
