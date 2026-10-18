package diagram

import (
	"strings"
	"testing"
)

// commitIDs lists commit ids in definition order, so assertions read against
// the source rather than against map iteration order.
func commitIDs(g *gitGraph) []string {
	ids := make([]string, len(g.commits))
	for i, c := range g.commits {
		ids[i] = c.id
	}
	return ids
}

func findCommit(g *gitGraph, id string) *gitCommit {
	for i := range g.commits {
		if g.commits[i].id == id {
			return &g.commits[i]
		}
	}
	return nil
}

func TestParseGitGraphInitDirective(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "mainBranchName under gitGraph",
			src:  "%%{init: {\"gitGraph\": {\"mainBranchName\": \"trunk\"}}}%%\ngitGraph\ncommit",
			want: "trunk",
		},
		{
			name: "mainBranchName at top level",
			src:  "%%{init: {\"mainBranchName\": \"develop\"}}%%\ngitGraph\ncommit",
			want: "develop",
		},
		{
			name: "single-quoted json is repaired",
			src:  "%%{init: {'gitGraph': {'mainBranchName': 'primary'}}}%%\ngitGraph\ncommit",
			want: "primary",
		},
		{
			name: "no directive keeps the default",
			src:  "gitGraph\ncommit",
			want: "main",
		},
		{
			// Malformed JSON is ignored rather than aborting the parse.
			name: "malformed json is ignored",
			src:  "%%{init: {not json at all}}%%\ngitGraph\ncommit",
			want: "main",
		},
		{
			name: "non-string mainBranchName is ignored",
			src:  "%%{init: {\"gitGraph\": {\"mainBranchName\": 42}}}%%\ngitGraph\ncommit",
			want: "main",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := parseGitGraph(tt.src)
			if g.mainBranchName != tt.want {
				t.Errorf("mainBranchName = %q, want %q", g.mainBranchName, tt.want)
			}
		})
	}
}

func TestParseGitGraphBranch(t *testing.T) {
	g := parseGitGraph(`gitGraph
commit id: "a"
branch feature
commit id: "b"
commit id: "c"`)

	if len(g.commits) != 3 {
		t.Fatalf("expected 3 commits, got %d: %v", len(g.commits), commitIDs(g))
	}
	// Commits after the branch belong to the new branch.
	for _, id := range []string{"b", "c"} {
		c := findCommit(g, id)
		if c == nil {
			t.Fatalf("commit %q missing", id)
		}
		if c.branch != "feature" {
			t.Errorf("commit %q branch = %q, want feature", id, c.branch)
		}
	}
	// The pre-branch commit stays on the main branch.
	if c := findCommit(g, "a"); c == nil || c.branch != "main" {
		t.Errorf("expected commit a on main, got %+v", c)
	}
}

func TestParseGitGraphBranchWithOrder(t *testing.T) {
	g := parseGitGraph(`gitGraph
commit id: "a"
branch feature order: 3
commit id: "b"`)

	var found *gitBranch
	for i := range g.branches {
		if g.branches[i].name == "feature" {
			found = &g.branches[i]
		}
	}
	if found == nil {
		t.Fatal("expected the feature branch to be recorded")
	}
	if found.order != 3 {
		t.Errorf("branch order = %d, want 3", found.order)
	}
}

func TestParseGitGraphMerge(t *testing.T) {
	g := parseGitGraph(`gitGraph
commit id: "a"
branch feature
commit id: "b"
checkout main
merge feature id: "m"`)

	merged := findCommit(g, "m")
	if merged == nil {
		t.Fatalf("expected the merge commit, got %v", commitIDs(g))
	}
	// A merge commit has two parents: the branch head and the merged head.
	if len(merged.parents) != 2 {
		t.Errorf("expected 2 parents, got %v", merged.parents)
	}
	if merged.branch != "main" {
		t.Errorf("expected the merge on main, got %q", merged.branch)
	}
}

func TestParseGitGraphMergeGeneratesID(t *testing.T) {
	// Without an explicit id the parser assigns one, so the commit is still
	// addressable by later commits.
	g := parseGitGraph(`gitGraph
commit id: "a"
branch feature
commit id: "b"
checkout main
merge feature`)

	if len(g.commits) != 3 {
		t.Fatalf("expected 3 commits, got %v", commitIDs(g))
	}
	last := g.commits[len(g.commits)-1]
	if last.id == "" {
		t.Error("expected an auto-generated merge id")
	}
	if len(last.parents) != 2 {
		t.Errorf("expected 2 parents on the auto-id merge, got %v", last.parents)
	}
}

func TestParseGitGraphMergeTypeAndTag(t *testing.T) {
	g := parseGitGraph(`gitGraph
commit id: "a"
branch feature
commit id: "b"
checkout main
merge feature id: "m" type: HIGHLIGHT tag: "v1"`)

	merged := findCommit(g, "m")
	if merged == nil {
		t.Fatal("expected the merge commit")
	}
	if merged.ctype != "HIGHLIGHT" {
		t.Errorf("ctype = %q, want HIGHLIGHT", merged.ctype)
	}
	if merged.tag != "v1" {
		t.Errorf("tag = %q, want v1", merged.tag)
	}
}

func TestParseGitGraphCherryPick(t *testing.T) {
	g := parseGitGraph(`gitGraph
commit id: "a"
branch feature
commit id: "b"
checkout main
cherry-pick id: "b"`)

	// The cherry-pick lands as a new commit with the source as second parent.
	var picked *gitCommit
	for i := range g.commits {
		if strings.Contains(g.commits[i].id, "b-cherry") {
			picked = &g.commits[i]
		}
	}
	if picked == nil {
		t.Fatalf("expected a cherry-pick commit, got %v", commitIDs(g))
	}
	if len(picked.parents) != 2 || picked.parents[1] != "b" {
		t.Errorf("expected parents [head, b], got %v", picked.parents)
	}
	if picked.branch != "main" {
		t.Errorf("expected the cherry-pick on main, got %q", picked.branch)
	}
}

func TestParseGitGraphCherryPickUnknownCommitWarns(t *testing.T) {
	g := parseGitGraph(`gitGraph
commit id: "a"
cherry-pick id: "nope"`)

	if len(g.warnings) == 0 {
		t.Fatal("expected a warning for an unknown commit")
	}
	if !strings.Contains(g.warnings[0], "nope") {
		t.Errorf("expected the warning to name the commit, got %q", g.warnings[0])
	}
	// The bad directive must not add a commit.
	if len(g.commits) != 1 {
		t.Errorf("expected only the original commit, got %v", commitIDs(g))
	}
}

func TestParseGitGraphReset(t *testing.T) {
	t.Run("reset to a branch name", func(t *testing.T) {
		g := parseGitGraph(`gitGraph
commit id: "a"
branch feature
commit id: "b"
checkout main
reset main`)
		if len(g.warnings) > 0 {
			t.Errorf("expected no warnings, got %v", g.warnings)
		}
	})

	t.Run("reset to an unknown ref warns", func(t *testing.T) {
		g := parseGitGraph(`gitGraph
commit id: "a"
reset ghost`)
		if len(g.warnings) == 0 {
			t.Fatal("expected a warning for an unknown ref")
		}
		if !strings.Contains(g.warnings[0], "ghost") {
			t.Errorf("expected the warning to name the ref, got %q", g.warnings[0])
		}
	})

	t.Run("walking back farther than the history warns", func(t *testing.T) {
		g := parseGitGraph(`gitGraph
commit id: "a"
reset main~5`)
		if len(g.warnings) == 0 {
			t.Fatal("expected a warning when the ancestors run out")
		}
	})

	t.Run("walking back one ancestor succeeds", func(t *testing.T) {
		g := parseGitGraph(`gitGraph
commit id: "a"
commit id: "b"
reset main~1`)
		if len(g.warnings) > 0 {
			t.Errorf("expected no warnings, got %v", g.warnings)
		}
	})
}

func TestParseGitGraphCommitTypesAndTags(t *testing.T) {
	g := parseGitGraph(`gitGraph
commit id: "n"
commit id: "r" type: REVERSE
commit id: "h" type: HIGHLIGHT tag: "release"`)

	for _, tt := range []struct{ id, ctype, tag string }{
		{"n", "NORMAL", ""},
		{"r", "REVERSE", ""},
		{"h", "HIGHLIGHT", "release"},
	} {
		c := findCommit(g, tt.id)
		if c == nil {
			t.Fatalf("commit %q missing", tt.id)
		}
		if c.ctype != tt.ctype {
			t.Errorf("commit %q ctype = %q, want %q", tt.id, c.ctype, tt.ctype)
		}
		if c.tag != tt.tag {
			t.Errorf("commit %q tag = %q, want %q", tt.id, c.tag, tt.tag)
		}
	}
}

func TestParseGitGraphDirection(t *testing.T) {
	for _, dir := range []string{"TB", "BT", "LR"} {
		g := parseGitGraph("gitGraph " + dir + "\ncommit id: \"a\"")
		if g.direction != dir {
			t.Errorf("direction = %q, want %q", g.direction, dir)
		}
		if !g.directionExplicit {
			t.Errorf("expected directionExplicit for %q", dir)
		}
	}
	// No directive keeps the LR default and is not marked explicit.
	g := parseGitGraph("gitGraph\ncommit id: \"a\"")
	if g.direction != "LR" || g.directionExplicit {
		t.Errorf("expected implicit LR default, got %q explicit=%v", g.direction, g.directionExplicit)
	}
}

func TestParseERAttribute(t *testing.T) {
	tests := []struct {
		name string
		text string
		want *erAttribute
	}{
		{
			name: "type and name",
			text: "string name",
			want: &erAttribute{attrType: "string", name: "name"},
		},
		{
			name: "with a primary key",
			text: "int id PK",
			want: &erAttribute{attrType: "int", name: "id", keys: []string{"PK"}},
		},
		{
			name: "with several keys",
			text: "int id PK, FK",
			want: &erAttribute{attrType: "int", name: "id", keys: []string{"PK", "FK"}},
		},
		{
			name: "with a comment",
			text: `string email "the address"`,
			want: &erAttribute{attrType: "string", name: "email", comment: "the address"},
		},
		{
			name: "with a key and a comment",
			text: `int id PK "surrogate"`,
			want: &erAttribute{attrType: "int", name: "id", keys: []string{"PK"}, comment: "surrogate"},
		},
		{
			name: "surrounding whitespace",
			text: "   int   id   ",
			want: &erAttribute{attrType: "int", name: "id"},
		},
		{name: "empty", text: "", want: nil},
		{name: "single token", text: "int", want: nil},
		// PK must be a whole token; a type-like word ending in PK is part of
		// the name, not a key marker.
		{name: "key marker must be separate", text: "int myPK", want: &erAttribute{attrType: "int", name: "myPK"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseERAttribute(tt.text)
			if tt.want == nil {
				if got != nil {
					t.Fatalf("expected nil, got %+v", got)
				}
				return
			}
			if got == nil {
				t.Fatalf("expected %+v, got nil", tt.want)
			}
			if got.attrType != tt.want.attrType || got.name != tt.want.name || got.comment != tt.want.comment {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
			if len(got.keys) != len(tt.want.keys) {
				t.Fatalf("keys = %v, want %v", got.keys, tt.want.keys)
			}
			for i := range got.keys {
				if got.keys[i] != tt.want.keys[i] {
					t.Errorf("keys = %v, want %v", got.keys, tt.want.keys)
					break
				}
			}
		})
	}
}

func TestFormatERAttribute(t *testing.T) {
	tests := []struct {
		name string
		attr erAttribute
		want string
	}{
		{"type and name", erAttribute{attrType: "int", name: "id"}, "int id"},
		{"with a key", erAttribute{attrType: "int", name: "id", keys: []string{"PK"}}, "int id PK"},
		{"with keys", erAttribute{attrType: "int", name: "id", keys: []string{"PK", "FK"}}, "int id PK,FK"},
		{"with a comment", erAttribute{attrType: "string", name: "n", comment: "c"}, `string n "c"`},
		{
			"round trip",
			erAttribute{attrType: "int", name: "id", keys: []string{"PK"}, comment: "c"},
			`int id PK "c"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatERAttribute(tt.attr); got != tt.want {
				t.Errorf("formatERAttribute = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestERAttributeRoundTrip(t *testing.T) {
	// Parsing then formatting must be stable, so a rendered attribute can be
	// re-parsed by anything downstream.
	for _, text := range []string{
		"int id PK",
		"string name",
		`string email "the address"`,
		`int id PK,FK "surrogate"`,
	} {
		attr := parseERAttribute(text)
		if attr == nil {
			t.Fatalf("failed to parse %q", text)
		}
		if got := formatERAttribute(*attr); got != text {
			t.Errorf("round trip of %q = %q", text, got)
		}
	}
}
