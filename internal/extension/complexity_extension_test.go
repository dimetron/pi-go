package extension

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// --- bundledMainFile ---

// TestBundledMainFile pins the fallback rule the original inline block used:
// the conventional SKILL.md wins, but an *empty* one still falls through to
// the first file. Collapsing those two conditions would be the easy mistake.
func TestBundledMainFile(t *testing.T) {
	tests := []struct {
		name      string
		skillName string
		files     []BundledSkillFile
		want      string
	}{
		{
			name:      "no files yields nothing",
			skillName: "alpha",
			files:     nil,
			want:      "",
		},
		{
			name:      "conventional path wins",
			skillName: "alpha",
			files: []BundledSkillFile{
				{RelPath: "bundled_skills/alpha/other.md", Content: []byte("other")},
				{RelPath: "bundled_skills/alpha/SKILL.md", Content: []byte("main")},
			},
			want: "main",
		},
		{
			name:      "conventional path wins even when it is first",
			skillName: "alpha",
			files: []BundledSkillFile{
				{RelPath: "bundled_skills/alpha/SKILL.md", Content: []byte("main")},
				{RelPath: "bundled_skills/alpha/other.md", Content: []byte("other")},
			},
			want: "main",
		},
		{
			name:      "no conventional path falls back to the first file",
			skillName: "alpha",
			files: []BundledSkillFile{
				{RelPath: "bundled_skills/alpha/other.md", Content: []byte("first")},
				{RelPath: "bundled_skills/alpha/more.md", Content: []byte("second")},
			},
			want: "first",
		},
		{
			// The original tested len(mainFile) == 0 *after* the loop, so an
			// empty SKILL.md does not suppress the fallback — and the fallback
			// is files[0], not "the next file".
			name:      "empty conventional file falls back to files[0]",
			skillName: "alpha",
			files: []BundledSkillFile{
				{RelPath: "bundled_skills/alpha/other.md", Content: []byte("fallback")},
				{RelPath: "bundled_skills/alpha/SKILL.md", Content: []byte("")},
			},
			want: "fallback",
		},
		{
			// Same rule, and here files[0] *is* the empty SKILL.md, so the
			// fallback reproduces the empty result rather than skipping ahead.
			name:      "empty conventional file at index 0 stays empty",
			skillName: "alpha",
			files: []BundledSkillFile{
				{RelPath: "bundled_skills/alpha/SKILL.md", Content: []byte("")},
				{RelPath: "bundled_skills/alpha/other.md", Content: []byte("not reached")},
			},
			want: "",
		},
		{
			name:      "everything empty yields nothing",
			skillName: "alpha",
			files: []BundledSkillFile{
				{RelPath: "bundled_skills/alpha/SKILL.md", Content: []byte("")},
			},
			want: "",
		},
		{
			name:      "another skill's SKILL.md does not match",
			skillName: "alpha",
			files: []BundledSkillFile{
				{RelPath: "bundled_skills/beta/SKILL.md", Content: []byte("beta")},
			},
			want: "beta", // matched only by the first-file fallback
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := string(bundledMainFile(tt.files, tt.skillName)); got != tt.want {
				t.Errorf("bundledMainFile = %q, want %q", got, tt.want)
			}
		})
	}
}

// --- skillCandidates ---

// TestSkillCandidates pins the discovery order and the two skip rules: plain
// files are not skills, and a subdirectory without a SKILL.md is not one
// either. The directory's own SKILL.md is always candidate zero, listed
// whether or not it exists — appendDirSkills stats it again.
func TestSkillCandidates(t *testing.T) {
	dir := t.TempDir()
	setupSkillWithContent(t, dir, "beta", "---\nname: beta\n---\nB")
	setupSkillWithContent(t, dir, "alpha", "---\nname: alpha\n---\nA")
	// A subdirectory with no SKILL.md.
	if err := os.MkdirAll(filepath.Join(dir, "empty-sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A plain file, not a directory.
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := skillCandidates(dir, readDir(t, dir))

	if len(got) != 3 {
		t.Fatalf("got %d candidates, want 3: %+v", len(got), got)
	}
	if got[0].path != filepath.Join(dir, "SKILL.md") {
		t.Errorf("candidate[0].path = %q, want the directory's own SKILL.md", got[0].path)
	}
	if got[0].defaultName != filepath.Base(dir) {
		t.Errorf("candidate[0].defaultName = %q, want %q", got[0].defaultName, filepath.Base(dir))
	}
	// os.ReadDir sorts by name, so alpha precedes beta.
	for i, want := range []string{"alpha", "beta"} {
		c := got[i+1]
		if c.defaultName != want {
			t.Errorf("candidate[%d].defaultName = %q, want %q", i+1, c.defaultName, want)
		}
		if c.path != filepath.Join(dir, want, "SKILL.md") {
			t.Errorf("candidate[%d].path = %q, want %q", i+1, c.path, filepath.Join(dir, want, "SKILL.md"))
		}
	}
}

// TestSkillCandidatesOwnSkillFile covers a directory that is itself a skill.
func TestSkillCandidatesOwnSkillFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: self\n---\nS"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := skillCandidates(dir, readDir(t, dir))
	if len(got) != 1 {
		t.Fatalf("got %d candidates, want 1: %+v", len(got), got)
	}
}

// --- auditRejects ---

const (
	bidiOverride = "\u202E" // critical
	tagChar      = "\U000E0001"
	zeroWidth    = "\u200B" // warning only
)

// TestAuditRejects walks all five outcomes the inline audit block encoded.
func TestAuditRejects(t *testing.T) {
	tests := []struct {
		name        string
		mode        AuditMode
		body        string
		path        string // overrides the written file when non-empty
		wantReject  bool
		wantBlocked int
	}{
		{
			name: "skip mode never scans and never rejects",
			mode: AuditSkip, body: "Bad " + bidiOverride + " char.",
			wantReject: false, wantBlocked: 0,
		},
		{
			name: "clean file is not rejected",
			mode: AuditBlock, body: "Perfectly ordinary text.",
			wantReject: false, wantBlocked: 0,
		},
		{
			name: "warning-only finding is not rejected",
			mode: AuditBlock, body: "Zero" + zeroWidth + "width.",
			wantReject: false, wantBlocked: 0,
		},
		{
			name: "critical finding under block is rejected and recorded",
			mode: AuditBlock, body: "Hidden " + bidiOverride + " text.",
			wantReject: true, wantBlocked: 1,
		},
		{
			name: "critical tag char under block is rejected",
			mode: AuditBlock, body: "Tag " + tagChar + " text.",
			wantReject: true, wantBlocked: 1,
		},
		{
			name: "critical finding under warn is not rejected",
			mode: AuditWarn, body: "Hidden " + bidiOverride + " text.",
			wantReject: false, wantBlocked: 0,
		},
		{
			name: "a scan failure warns and loads",
			mode: AuditBlock, path: filepath.Join(t.TempDir(), "does-not-exist", "SKILL.md"),
			wantReject: false, wantBlocked: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := tt.path
			if path == "" {
				dir := t.TempDir()
				path = filepath.Join(dir, "SKILL.md")
				if err := os.WriteFile(path, []byte("---\nname: s\n---\n"+tt.body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			var blocked []string
			got := auditRejects(LoadOptions{AuditMode: tt.mode}, skillCandidate{path: path, defaultName: "s"}, &blocked)
			if got != tt.wantReject {
				t.Errorf("auditRejects = %v, want %v", got, tt.wantReject)
			}
			if len(blocked) != tt.wantBlocked {
				t.Errorf("blocked = %v, want %d entries", blocked, tt.wantBlocked)
			}
			if tt.wantBlocked > 0 && blocked[0] != path {
				t.Errorf("blocked[0] = %q, want %q", blocked[0], path)
			}
		})
	}
}

// TestAuditRejectsAppendsAcrossCalls checks blocked accumulates rather than
// being replaced — it is threaded through by pointer now, not closed over.
func TestAuditRejectsAppendsAcrossCalls(t *testing.T) {
	dir := t.TempDir()
	var blocked []string
	for _, name := range []string{"one", "two"} {
		setupSkillWithContent(t, dir, name, "---\nname: "+name+"\n---\nBad "+bidiOverride+" char.")
		path := filepath.Join(dir, name, "SKILL.md")
		if !auditRejects(LoadOptions{AuditMode: AuditBlock}, skillCandidate{path: path, defaultName: name}, &blocked) {
			t.Fatalf("%s: expected rejection", name)
		}
	}
	if len(blocked) != 2 {
		t.Errorf("blocked = %v, want 2 entries", blocked)
	}
}

// --- appendDirSkills ---

// TestAppendDirSkillsMissingDir keeps the "not an error" rule for a directory
// that does not exist, and returns the accumulator untouched.
func TestAppendDirSkillsMissingDir(t *testing.T) {
	seen := map[string]int{}
	in := []Skill{{Name: "kept"}}
	got, err := appendDirSkills(in, seen, nil, LoadOptions{AuditMode: AuditSkip}, filepath.Join(t.TempDir(), "nope"))
	if err != nil {
		t.Fatalf("missing dir should not error: %v", err)
	}
	if len(got) != 1 || got[0].Name != "kept" {
		t.Errorf("skills = %+v, want the input unchanged", got)
	}
}

// skipIfReadDirOnFileIsNotAnError skips tests that stand a regular file in for
// an unreadable directory. On Unix os.ReadDir fails on it with ENOTDIR, which
// is not os.IsNotExist, so it exercises the fatal branch; on Windows the same
// call does not produce such an error, so there is no way to reach that branch
// from the filesystem.
func skipIfReadDirOnFileIsNotAnError(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("os.ReadDir on a regular file is not a non-IsNotExist error on Windows")
	}
}

// TestAppendDirSkillsUnreadableDir keeps the other half of that rule: a read
// failure that is not "not exist" is fatal, and carries the directory name.
func TestAppendDirSkillsUnreadableDir(t *testing.T) {
	skipIfReadDirOnFileIsNotAnError(t)
	// A regular file where a directory was expected: ReadDir fails with
	// ENOTDIR, which is not os.IsNotExist.
	notADir := filepath.Join(t.TempDir(), "file.txt")
	if err := os.WriteFile(notADir, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	var blocked []string
	got, err := appendDirSkills(nil, map[string]int{}, &blocked, LoadOptions{AuditMode: AuditSkip}, notADir)
	if err == nil {
		t.Fatal("expected an error for a non-directory")
	}
	if !strings.Contains(err.Error(), "reading skills dir "+notADir) {
		t.Errorf("error = %q, want it to name the directory", err)
	}
	if got != nil {
		t.Errorf("skills = %+v, want nil on error", got)
	}
}

// TestAppendDirSkillsParseFailureIsFatal covers the other error return. A
// SKILL.md that is a directory passes the Stat guard but cannot be read.
func TestAppendDirSkillsParseFailureIsFatal(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "broken", "SKILL.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	var blocked []string
	_, err := appendDirSkills(nil, map[string]int{}, &blocked, LoadOptions{AuditMode: AuditSkip}, dir)
	if err == nil {
		t.Fatal("expected an error for an unreadable SKILL.md")
	}
	if !strings.Contains(err.Error(), "parsing ") {
		t.Errorf("error = %q, want a parsing failure", err)
	}
}

// TestAppendDirSkillsDefaultNameAndSource pins the two per-skill fixups the
// loop applies: the directory name fills in for a missing frontmatter name,
// and Source is decided by whether the search dir is absolute.
func TestAppendDirSkillsDefaultNameAndSource(t *testing.T) {
	t.Run("absolute dir is a user skill and names default from the dir", func(t *testing.T) {
		dir := t.TempDir()
		// No name: in the frontmatter, so the directory name is used.
		setupSkillWithContent(t, dir, "unnamed", "---\ndescription: no name here\n---\nBody.")
		var blocked []string
		got, err := appendDirSkills(nil, map[string]int{}, &blocked, LoadOptions{AuditMode: AuditSkip}, dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 {
			t.Fatalf("got %d skills, want 1", len(got))
		}
		if got[0].Name != "unnamed" {
			t.Errorf("Name = %q, want the directory name %q", got[0].Name, "unnamed")
		}
		if got[0].Source != "user" {
			t.Errorf("Source = %q, want user for an absolute dir", got[0].Source)
		}
	})

	t.Run("relative dir is a project skill", func(t *testing.T) {
		base := t.TempDir()
		t.Chdir(base)
		setupSkillWithContent(t, "skills", "local", "---\nname: local\ndescription: d\n---\nBody.")
		var blocked []string
		got, err := appendDirSkills(nil, map[string]int{}, &blocked, LoadOptions{AuditMode: AuditSkip}, "skills")
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 {
			t.Fatalf("got %d skills, want 1", len(got))
		}
		if got[0].Source != "project" {
			t.Errorf("Source = %q, want project for a relative dir", got[0].Source)
		}
	})
}

// TestAppendDirSkillsOverrideByIndex is the reason seen carries an index
// rather than a bool: a later directory must replace the earlier skill in
// place, not append a duplicate.
func TestAppendDirSkillsOverrideByIndex(t *testing.T) {
	first := t.TempDir()
	second := t.TempDir()
	setupSkillWithContent(t, first, "dup", "---\nname: dup\ndescription: first\n---\nA")
	setupSkillWithContent(t, second, "other", "---\nname: other\ndescription: other\n---\nO")
	setupSkillWithContent(t, second, "dup", "---\nname: dup\ndescription: second\n---\nB")

	seen := map[string]int{}
	var blocked []string
	opts := LoadOptions{AuditMode: AuditSkip}

	skills, err := appendDirSkills(nil, seen, &blocked, opts, first)
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 1 || skills[0].Description != "first" {
		t.Fatalf("after first dir: %+v", skills)
	}

	skills, err = appendDirSkills(skills, seen, &blocked, opts, second)
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 2 {
		t.Fatalf("got %d skills, want 2 (dup overridden in place, other appended)", len(skills))
	}
	got, ok := FindSkill(skills, "dup")
	if !ok {
		t.Fatal("dup missing after override")
	}
	if got.Description != "second" {
		t.Errorf("dup.Description = %q, want the later directory to win", got.Description)
	}
	if seen["dup"] != 0 {
		t.Errorf("seen[dup] = %d, want the original index 0", seen["dup"])
	}
}

// --- appendBundledSkills / LoadSkillsWithOptions ---

// TestAppendBundledSkillsIndexesSeen checks the bundled pass records an index
// for every skill it appends, which is what lets a user skill override one.
func TestAppendBundledSkillsIndexesSeen(t *testing.T) {
	seen := map[string]int{}
	skills := appendBundledSkills(nil, seen)
	if len(skills) == 0 {
		t.Skip("no bundled skills embedded in this build")
	}
	if len(seen) != len(skills) {
		t.Fatalf("seen has %d entries for %d skills", len(seen), len(skills))
	}
	for name, idx := range seen {
		if idx < 0 || idx >= len(skills) {
			t.Fatalf("seen[%q] = %d, out of range", name, idx)
		}
		if skills[idx].Name != name {
			t.Errorf("seen[%q] points at %q", name, skills[idx].Name)
		}
		if skills[idx].Source != "bundled" {
			t.Errorf("%q has Source %q, want bundled", name, skills[idx].Source)
		}
	}
}

// TestAppendBundledSkillsAppendsToExisting checks the accumulator form: the
// helper appends to what it is given rather than starting fresh.
func TestAppendBundledSkillsAppendsToExisting(t *testing.T) {
	seen := map[string]int{}
	base := []Skill{{Name: "pre-existing"}}
	got := appendBundledSkills(base, seen)
	if len(got) < 1 || got[0].Name != "pre-existing" {
		t.Fatalf("skills = %+v, want the input preserved at index 0", got)
	}
	for name, idx := range seen {
		if got[idx].Name != name {
			t.Errorf("seen[%q] = %d points at %q; indices must account for the prefix", name, idx, got[idx].Name)
		}
	}
}

// TestLoadSkillsWithOptionsUserOverridesBundled is the end-to-end check that
// the two extracted passes still share one index map: a user skill named after
// a bundled one replaces it instead of duplicating it.
func TestLoadSkillsWithOptionsUserOverridesBundled(t *testing.T) {
	bundled := appendBundledSkills(nil, map[string]int{})
	if len(bundled) == 0 {
		t.Skip("no bundled skills embedded in this build")
	}
	target := bundled[0].Name

	dir := t.TempDir()
	setupSkillWithContent(t, dir, target, "---\nname: "+target+"\ndescription: overridden by user\n---\nUser body.")

	skills, err := LoadSkillsWithOptions(LoadOptions{AuditMode: AuditSkip}, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != len(bundled) {
		t.Fatalf("got %d skills, want %d — the override should not add one", len(skills), len(bundled))
	}
	got, ok := FindSkill(skills, target)
	if !ok {
		t.Fatalf("%q missing after override", target)
	}
	if got.Source != "user" {
		t.Errorf("Source = %q, want user", got.Source)
	}
	if got.Description != "overridden by user" {
		t.Errorf("Description = %q, want the user skill's", got.Description)
	}
}

// TestLoadSkillsWithOptionsMultipleDirsStopAtFirstError checks the error from
// a later directory still aborts the whole load, as the inline loop did.
func TestLoadSkillsWithOptionsMultipleDirsStopAtFirstError(t *testing.T) {
	skipIfReadDirOnFileIsNotAnError(t)
	good := t.TempDir()
	setupSkillWithContent(t, good, "fine", "---\nname: fine\ndescription: d\n---\nB")

	notADir := filepath.Join(t.TempDir(), "file.txt")
	if err := os.WriteFile(notADir, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	skills, err := LoadSkillsWithOptions(LoadOptions{AuditMode: AuditSkip}, good, notADir)
	if err == nil {
		t.Fatal("expected an error from the second directory")
	}
	if skills != nil {
		t.Errorf("skills = %+v, want nil on error", skills)
	}
}

// TestLoadSkillsWithOptionsBlockedSummary checks the blocked list still
// reaches the trailing summary now that it is threaded through by pointer.
func TestLoadSkillsWithOptionsBlockedSummary(t *testing.T) {
	dirA := t.TempDir()
	dirB := t.TempDir()
	setupSkillWithContent(t, dirA, "bad-a", "---\nname: bad-a\ndescription: d\n---\nX"+bidiOverride+"Y")
	setupSkillWithContent(t, dirB, "bad-b", "---\nname: bad-b\ndescription: d\n---\nX"+tagChar+"Y")
	setupSkillWithContent(t, dirB, "good", "---\nname: good\ndescription: d\n---\nfine")

	skills, err := LoadSkillsWithOptions(LoadOptions{AuditMode: AuditBlock}, dirA, dirB)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := FindSkill(skills, "bad-a"); ok {
		t.Error("bad-a should have been blocked")
	}
	if _, ok := FindSkill(skills, "bad-b"); ok {
		t.Error("bad-b should have been blocked")
	}
	if _, ok := FindSkill(skills, "good"); !ok {
		t.Error("good should have loaded")
	}
	if want := 1 + bundledSkillCount(t); len(skills) != want {
		t.Errorf("got %d skills, want %d", len(skills), want)
	}
}

func readDir(t *testing.T, dir string) []os.DirEntry {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	return entries
}
