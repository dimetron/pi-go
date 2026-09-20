package tools

import (
	"fmt"
	"strings"
	"testing"
)

// These tests pin the information-preservation contract of the compactor. A byte
// saving that removes or misrepresents information the agent needs to act on is
// not a win, so each test asserts what survives and how a cut is disclosed —
// not merely that the output got smaller.

// TestCompact_EveryToolDisclosesItsCut asserts that for every tool whose output
// it trims, the compactor leaves the agent able to tell that the result is
// partial. Silence is the failure mode: a capped list that looks complete is
// worse than a long one.
func TestCompact_EveryToolDisclosesItsCut(t *testing.T) {
	cfg := DefaultCompactorConfig()

	t.Run("grep keeps the true total and flags truncation", func(t *testing.T) {
		ms := make([]GrepMatch, 400)
		for i := range ms {
			ms[i] = GrepMatch{File: fmt.Sprintf("f%d.go", i), Line: i, Content: "x"}
		}
		r := prodResult(t, GrepOutput{Matches: ms, TotalMatches: 400})
		applyCompaction(r, compactGrep(r, nil, cfg))
		if r["total_matches"] != float64(400) {
			t.Errorf("total_matches = %v, want 400 (pre-cap)", r["total_matches"])
		}
		if r["truncated"] != true {
			t.Errorf("truncated = %v, want true", r["truncated"])
		}
	})

	t.Run("find keeps the true total and flags truncation", func(t *testing.T) {
		fs := make([]string, 500)
		for i := range fs {
			fs[i] = fmt.Sprintf("f%d.go", i)
		}
		r := prodResult(t, FindOutput{Files: fs, TotalFiles: 500})
		applyCompaction(r, compactFind(r, nil, cfg))
		if r["total_files"] != float64(500) {
			t.Errorf("total_files = %v, want 500 (pre-cap)", r["total_files"])
		}
		if r["truncated"] != true {
			t.Errorf("truncated = %v, want true", r["truncated"])
		}
	})

	t.Run("ls keeps the true total and flags truncation", func(t *testing.T) {
		es := make([]LsEntry, 1000)
		for i := range es {
			es[i] = LsEntry{Name: fmt.Sprintf("f%d.go", i)}
		}
		r := prodResult(t, LsOutput{Entries: es, TotalEntries: 1000})
		applyCompaction(r, compactLs(r, nil, cfg))
		if r["total_entries"] != float64(1000) {
			t.Errorf("total_entries = %v, want 1000 (pre-cap)", r["total_entries"])
		}
		if r["truncated"] != true {
			t.Errorf("truncated = %v, want true", r["truncated"])
		}
	})

	t.Run("git-hunk keeps total_hunks", func(t *testing.T) {
		hs := make([]Hunk, 240)
		for i := range hs {
			hs[i] = Hunk{Header: "@@ -1 +1 @@", Content: "x", Added: 1, Removed: 1}
		}
		r := prodResult(t, GitHunkOutput{File: "f.go", Hunks: hs, TotalHunks: 240})
		applyCompaction(r, compactGitHunk(r, nil, cfg))
		if r["total_hunks"] != float64(240) {
			t.Errorf("total_hunks = %v, want 240 (pre-cap)", r["total_hunks"])
		}
	})

	t.Run("git-file-diff sets the tool's own truncated flag", func(t *testing.T) {
		var b strings.Builder
		for i := 0; i < 1500; i++ {
			fmt.Fprintf(&b, "-old %d\n+new %d\n", i, i)
		}
		r := prodResult(t, GitFileDiffOutput{File: "f.go", Diff: b.String(), LinesAdded: 1500})
		cr := compactGitFileDiff(r, nil, cfg)
		if cr == nil {
			t.Fatal("expected compaction")
		}
		applyCompaction(r, cr)
		// The tool declares this field (git_diff.go:32) and sets it when its own
		// 256KB cap fires. Compaction is the same loss and must report it.
		if r["truncated"] != true {
			t.Errorf("truncated = %v, want true: a reduced diff is not the complete diff", r["truncated"])
		}
	})

	t.Run("git-file-diff marks the cut inside the diff text", func(t *testing.T) {
		var b strings.Builder
		for i := 0; i < 1500; i++ {
			fmt.Fprintf(&b, "-old %d\n+new %d\n", i, i)
		}
		r := prodResult(t, GitFileDiffOutput{File: "f.go", Diff: b.String(), LinesAdded: 1500})
		cr := compactGitFileDiff(r, nil, cfg)
		out, _ := cr.Writes[0].Value.(string)
		if !strings.Contains(out, "omitted") {
			t.Errorf("compacted diff does not disclose the omitted lines:\n%s", out)
		}
	})
}

// TestCompact_UnbalancedHunkCutIsDisclosed is the misinformation guard. When the
// per-hunk cap lands inside a run of one sign, the visible diff shows only that
// sign. Without a per-hunk note the hunk reads as a pure removal (or addition)
// even though the change was balanced, and the file tally no longer reconciles
// with the visible lines.
func TestCompact_UnbalancedHunkCutIsDisclosed(t *testing.T) {
	cfg := DefaultCompactorConfig()
	cfg.MaxDiffHunkLines = 20 // binding cap; MaxDiffLines (100) still passes

	var b strings.Builder
	// Pad so the diff clears the MaxDiffLines gate.
	b.WriteString("diff --git a/pad.go b/pad.go\n--- a/pad.go\n+++ b/pad.go\n@@ -1,200 +1,200 @@\n")
	for i := 0; i < 200; i++ {
		fmt.Fprintf(&b, "-p%d\n+p%d\n", i, i)
	}
	// 30 deletions followed by 30 additions: a 20-line cut lands inside the
	// deletions, so the additions are invisible.
	b.WriteString("diff --git a/z.go b/z.go\n--- a/z.go\n+++ b/z.go\n@@ -1,60 +1,60 @@\n")
	for i := 0; i < 30; i++ {
		fmt.Fprintf(&b, "-removed %d\n", i)
	}
	for i := 0; i < 30; i++ {
		fmt.Fprintf(&b, "+added %d\n", i)
	}

	out, applied := compactGitDiffText(b.String(), cfg)
	if !applied {
		t.Fatal("expected compaction")
	}

	// Locate the z.go hunk and check it discloses the withheld additions.
	zIdx := strings.Index(out, "diff --git a/z.go")
	if zIdx < 0 {
		t.Fatal("z.go section missing from output")
	}
	zSection := out[zIdx:]
	shownAdd := strings.Count(zSection, "\n+added")
	if shownAdd != 0 {
		t.Fatalf("fixture no longer skews the cut: %d additions visible", shownAdd)
	}
	if !strings.Contains(zSection, "truncated)") {
		t.Errorf("all visible changes in the z.go hunk are deletions and no note "+
			"says more follows; the hunk reads as a pure removal:\n%s", zSection)
	}
	if !strings.Contains(zSection, "additions truncated") {
		t.Errorf("the note must name the withheld additions; got:\n%s", zSection)
	}
}

// TestCompact_KeepsEveryFileInStatus pins the deliberate exception: the
// git-overview dirty-file lists are NOT capped, because hiding a dirty file
// misleads about the true repo state (rtk git_cmd.rs format_status_inner makes
// the same call). Only recent_commits is head-capped.
func TestCompact_KeepsEveryFileInStatus(t *testing.T) {
	cfg := DefaultCompactorConfig()
	files := make([]string, 300)
	for i := range files {
		files[i] = fmt.Sprintf("dirty%d.txt", i)
	}
	r := prodResult(t, GitOverviewOutput{
		Branch: "main", StagedFiles: files, UnstagedFiles: files, UntrackedFiles: files,
	})
	if cr := compactGitOverview(r, nil, cfg); cr != nil {
		applyCompaction(r, cr)
	}
	for _, key := range []string{"staged_files", "unstaged_files", "untracked_files"} {
		got, _ := r[key].([]any)
		if len(got) != len(files) {
			t.Errorf("%s truncated to %d of %d; dirty-file lists must stay whole",
				key, len(got), len(files))
		}
	}
	if r["branch"] != "main" {
		t.Errorf("branch = %v, want main", r["branch"])
	}
}

// TestCompact_NoOpBelowItsCap records that a cap above the tool's own source cap
// can never fire, so a pipeline that looks healthy can still be dead. recent_
// commits is capped at 40 by config while the tool fetches only 10
// (git_overview.go:72, `git log --oneline -10`), so that cap is unreachable.
func TestCompact_NoOpBelowItsCap(t *testing.T) {
	cfg := DefaultCompactorConfig()
	commits := make([]string, 10) // the tool's own maximum
	for i := range commits {
		commits[i] = fmt.Sprintf("abc%04d subject", i)
	}
	r := prodResult(t, GitOverviewOutput{Branch: "main", RecentCommits: commits})
	if cr := compactGitOverview(r, nil, cfg); cr != nil {
		t.Errorf("git-overview compacted at its source cap; MaxLogEntries=%d exceeds "+
			"the tool's 10-commit fetch, so this cap is unreachable in production",
			cfg.MaxLogEntries)
	}
}

// TestCompact_CapKeepsHeadNotSpread documents a real limitation of capping by
// count. A head cap keeps the first N matches in tool order, so when matches are
// grouped by file (which is how grep emits them) the kept slice can cover far
// fewer files than the search actually touched.
//
// This is a known information loss, not a bug being asserted as correct: the
// agent learns the true match count (total_matches) but not which files were
// dropped. rtk addresses the same problem by grouping matches per file and
// emitting a "+N more files" hint (tmp/rtk/src/cmds/system/search.rs). pi-go
// does not do that yet; this test exists so the gap is visible and any future
// fix has a baseline to change.
func TestCompact_CapKeepsHeadNotSpread(t *testing.T) {
	cfg := DefaultCompactorConfig()
	var ms []GrepMatch
	for f := 0; f < 50; f++ {
		for m := 0; m < 10; m++ {
			ms = append(ms, GrepMatch{File: fmt.Sprintf("file%02d.go", f), Line: m, Content: "x"})
		}
	}
	r := prodResult(t, GrepOutput{Matches: ms, TotalMatches: 500})
	applyCompaction(r, compactGrep(r, nil, cfg))
	got, _ := r["matches"].([]any)

	seen := map[string]bool{}
	for _, m := range got {
		e, _ := m.(map[string]any)
		f, _ := e["file"].(string)
		seen[f] = true
	}

	// The true total is still available, which is what makes the loss tolerable.
	if r["total_matches"] != float64(500) {
		t.Errorf("total_matches = %v, want 500", r["total_matches"])
	}
	if r["truncated"] != true {
		t.Errorf("truncated = %v, want true", r["truncated"])
	}

	// Record the actual coverage. If this ever improves, the numbers below are
	// the old baseline and should be updated deliberately.
	t.Logf("500 matches over 50 files -> %d kept, %d distinct files visible",
		len(got), len(seen))
	if len(seen) > 10 {
		t.Logf("file coverage improved to %d (was 10)", len(seen))
	}
}

// TestCompact_HunkNoteStaysWithItsHunk guards the attribution of a per-hunk
// disclosure. The note for a truncated hunk must sit next to that hunk, not be
// carried into a later one: otherwise a reader attributes one hunk's withheld
// lines to another, which is worse than no note at all.
func TestCompact_HunkNoteStaysWithItsHunk(t *testing.T) {
	cfg := DefaultCompactorConfig()
	cfg.MaxDiffLines = 10 // force truncation
	cfg.MaxDiffHunkLines = 3

	var b strings.Builder
	b.WriteString("diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n")
	b.WriteString("@@ -1,10 +1,10 @@\n") // hunk 1: 10 changes, capped to 3
	for i := 0; i < 10; i++ {
		fmt.Fprintf(&b, "-h1 del %d\n+h1 add %d\n", i, i)
	}
	b.WriteString("@@ -50,2 +50,2 @@\n") // hunk 2: fully shown
	b.WriteString("-h2 del 0\n+h2 add 0\n")

	out, applied := compactGitDiffText(b.String(), cfg)
	if !applied {
		t.Fatal("expected compaction")
	}

	iNote := strings.Index(out, "truncated)")
	iHunk2 := strings.Index(out, "@@ -50,2")
	if iNote < 0 {
		t.Fatal("no disclosure note emitted")
	}
	if iHunk2 < 0 {
		t.Fatal("second hunk header missing")
	}
	if iNote > iHunk2 {
		t.Errorf("the truncated hunk's note (offset %d) appears after the next hunk "+
			"header (offset %d), so one hunk's withheld lines are attributed to "+
			"another:\n%s", iNote, iHunk2, out)
	}
}

// TestCompact_RespectsConfigOptOuts pins the documented opt-out switches. Each
// was honored by the pipelines before the dispatch fix; dropping a gate would
// make a documented setting silently ineffective, which is a worse defect than
// the dead pipelines it replaced.
func TestCompact_RespectsConfigOptOuts(t *testing.T) {
	t.Run("group_search_output=false disables search compaction", func(t *testing.T) {
		cfg := DefaultCompactorConfig()
		cfg.GroupSearchOutput = false

		ms := make([]GrepMatch, 400)
		for i := range ms {
			ms[i] = GrepMatch{File: fmt.Sprintf("f%d.go", i), Line: i, Content: "x"}
		}
		if cr := compactGrep(prodResult(t, GrepOutput{Matches: ms, TotalMatches: 400}), nil, cfg); cr != nil {
			t.Error("grep compacted with GroupSearchOutput off")
		}

		fs := make([]string, 500)
		for i := range fs {
			fs[i] = fmt.Sprintf("f%d.go", i)
		}
		if cr := compactFind(prodResult(t, FindOutput{Files: fs, TotalFiles: 500}), nil, cfg); cr != nil {
			t.Error("find compacted with GroupSearchOutput off")
		}

		es := make([]LsEntry, 1000)
		for i := range es {
			es[i] = LsEntry{Name: fmt.Sprintf("f%d.go", i)}
		}
		if cr := compactLs(prodResult(t, LsOutput{Entries: es, TotalEntries: 1000}), nil, cfg); cr != nil {
			t.Error("ls compacted with GroupSearchOutput off")
		}
	})

	t.Run("compact_git_output=false disables only the semantic git rewrite", func(t *testing.T) {
		cfg := DefaultCompactorConfig()
		cfg.CompactGitOutput = false

		// The flag governs the git-specific transformations. Hard truncation at
		// MaxChars sits outside it — it is the byte-ceiling safety net and ran
		// unconditionally before this fix too, so it must keep running.
		var db strings.Builder
		for i := 0; i < 1500; i++ {
			fmt.Fprintf(&db, "-old %d\n+new %d\n", i, i)
		}
		cr := compactGitFileDiff(prodResult(t, GitFileDiffOutput{
			File: "f.go", Diff: db.String(), LinesAdded: 1500,
		}), nil, cfg)
		if cr != nil {
			out, _ := cr.Writes[0].Value.(string)
			if strings.Contains(out, "omitted from diff") {
				t.Error("semantic diff rewrite ran despite CompactGitOutput=false")
			}
			for _, tech := range cr.Techniques {
				if tech == "git-compact" {
					t.Error("git-compact technique applied despite CompactGitOutput=false")
				}
			}
		}

		// hunks and commits have no byte-ceiling fallback, so the gate is total.
		hs := make([]Hunk, 240)
		for i := range hs {
			hs[i] = Hunk{Header: "@@ -1 +1 @@", Content: "x", Added: 1, Removed: 1}
		}
		if cr := compactGitHunk(prodResult(t, GitHunkOutput{
			File: "f.go", Hunks: hs, TotalHunks: 240,
		}), nil, cfg); cr != nil {
			t.Error("git-hunk compacted with CompactGitOutput off")
		}

		commits := make([]string, 100)
		for i := range commits {
			commits[i] = fmt.Sprintf("abc%04d subject", i)
		}
		if cr := compactGitOverview(prodResult(t, GitOverviewOutput{
			Branch: "main", RecentCommits: commits,
		}), nil, cfg); cr != nil {
			t.Error("git-overview compacted with CompactGitOutput off")
		}
	})
}

// TestCompact_LargeHunkContentIsCapped covers the case a hunk-count cap misses.
// A hunk is not bounded by how many hunks there are: git-hunk can return a
// single hunk carrying hundreds of KB of Content, and capping only len(hunks)
// leaves that untouched. Each hunk's Content is capped independently, on a line
// boundary, with the withheld line count disclosed.
func TestCompact_LargeHunkContentIsCapped(t *testing.T) {
	cfg := DefaultCompactorConfig()
	var content strings.Builder
	for i := 0; i < 5000; i++ {
		fmt.Fprintf(&content, "+added line %d with plenty of content here\n", i)
	}
	hs := []Hunk{{Header: "@@ -1,1 +1,5001 @@", Content: content.String(), Added: 5000}}
	r := prodResult(t, GitHunkOutput{File: "big.go", Hunks: hs, TotalHunks: 1})

	before := jsonLen(r)
	cr := compactGitHunk(r, nil, cfg)
	if cr == nil {
		t.Fatalf("a single %d-byte hunk was left untouched (len(hunks)=1 is under the "+
			"hunk-count cap of %d)", content.Len(), cfg.MaxDiffLines)
	}
	applyCompaction(r, cr)
	after := jsonLen(r)
	if after >= before {
		t.Errorf("content cap did not shrink the result: %d -> %d", before, after)
	}
	t.Logf("single hunk %d bytes -> result %d -> %d", content.Len(), before, after)

	// The trimmed content must say it was trimmed.
	got, _ := r["hunks"].([]any)
	if len(got) != 1 {
		t.Fatalf("hunks = %d, want 1", len(got))
	}
	hm, _ := got[0].(map[string]any)
	s, _ := hm["content"].(string)
	if !strings.Contains(s, "truncated)") {
		t.Errorf("trimmed hunk content does not disclose the cut")
	}
	// The hunk's own counts stay exact — only the rendered lines were trimmed.
	if hm["added"] != float64(5000) {
		t.Errorf("added = %v, want 5000 (counts are never rewritten)", hm["added"])
	}
	// total_hunks is preserved.
	if r["total_hunks"] != float64(1) {
		t.Errorf("total_hunks = %v, want 1", r["total_hunks"])
	}
}
