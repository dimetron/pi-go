package tools

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"testing/fstest"
)

func TestTreeConstants(t *testing.T) {
	if defaultTreeDepth != 3 {
		t.Errorf("defaultTreeDepth = %d, want 3", defaultTreeDepth)
	}
	if maxTreeDepth != 10 {
		t.Errorf("maxTreeDepth = %d, want 10", maxTreeDepth)
	}
	if maxTreeEntries != 500 {
		t.Errorf("maxTreeEntries = %d, want 500", maxTreeEntries)
	}
	if treeConnector != "├── " {
		t.Errorf("treeConnector = %q, want '├── '", treeConnector)
	}
	if treeLastConnector != "└── " {
		t.Errorf("treeLastConnector = %q, want '└── '", treeLastConnector)
	}
	if treeIndent != "│   " {
		t.Errorf("treeIndent = %q, want '│   '", treeIndent)
	}
	if treeLastIndent != "    " {
		t.Errorf("treeLastIndent = %q, want '    '", treeLastIndent)
	}
}

func TestTreeInput(t *testing.T) {
	input := TreeInput{
		Path:  "/tmp",
		Depth: 5,
	}
	if input.Path != "/tmp" {
		t.Errorf("Path = %q", input.Path)
	}
	if input.Depth != 5 {
		t.Errorf("Depth = %d", input.Depth)
	}
}

func TestTreeOutput(t *testing.T) {
	output := TreeOutput{
		Tree:  "dir\n├── file.go\n",
		Dirs:  1,
		Files: 1,
	}
	if output.Dirs != 1 {
		t.Errorf("Dirs = %d", output.Dirs)
	}
	if output.Files != 1 {
		t.Errorf("Files = %d", output.Files)
	}
}

func TestBuildTree_Basic(t *testing.T) {
	// Create a simple in-memory filesystem
	fsys := fstest.MapFS{
		"file1.go":           &fstest.MapFile{},
		"dir1/file2.go":      &fstest.MapFile{},
		"dir1/dir2/file3.go": &fstest.MapFile{},
	}

	var b strings.Builder
	dirs, files, count := 0, 0, 0
	buildTree(fsys, ".", "", 3, &b, &dirs, &files, &count)

	output := b.String()
	if !strings.Contains(output, "file1.go") {
		t.Error("expected output to contain file1.go")
	}
	if !strings.Contains(output, "dir1/") {
		t.Error("expected output to contain dir1/")
	}
	if dirs < 2 {
		t.Errorf("expected at least 2 dirs, got %d", dirs)
	}
	if files < 2 {
		t.Errorf("expected at least 2 files, got %d", files)
	}
}

func TestBuildTree_DepthLimit(t *testing.T) {
	fsys := fstest.MapFS{
		"root.go":            &fstest.MapFile{},
		"a/file1.go":         &fstest.MapFile{},
		"a/b/file2.go":       &fstest.MapFile{},
		"a/b/c/file3.go":     &fstest.MapFile{},
		"a/b/c/d/file4.go":   &fstest.MapFile{},
		"a/b/c/d/e/file5.go": &fstest.MapFile{},
	}

	var b strings.Builder
	dirs, files, count := 0, 0, 0
	// depth 2 should not descend into a/b/c or deeper
	buildTree(fsys, ".", "", 2, &b, &dirs, &files, &count)

	output := b.String()
	if strings.Contains(output, "file5.go") {
		t.Error("file5.go should not appear with depth 2")
	}
}

func TestBuildTree_EntryLimit(t *testing.T) {
	// Create more files than maxTreeEntries
	fsys := make(fstest.MapFS)
	for i := 0; i < 600; i++ {
		fsys[strings.Repeat("a", i/10)+strings.Repeat("b", i%10)+".txt"] = &fstest.MapFile{}
	}

	var b strings.Builder
	dirs, files, count := 0, 0, 0
	buildTree(fsys, ".", "", 10, &b, &dirs, &files, &count)

	output := b.String()
	if !strings.Contains(output, "(truncated)") {
		t.Error("expected output to contain (truncated)")
	}
	if count != 500 {
		t.Errorf("expected count to be 500 (maxTreeEntries), got %d", count)
	}
}

func TestBuildTree_SkipsHiddenDirs(t *testing.T) {
	fsys := fstest.MapFS{
		"file1.go":         &fstest.MapFile{},
		".hidden/file2.go": &fstest.MapFile{},
		"visible/file3.go": &fstest.MapFile{},
	}

	var b strings.Builder
	dirs, files, count := 0, 0, 0
	buildTree(fsys, ".", "", 3, &b, &dirs, &files, &count)

	output := b.String()
	if strings.Contains(output, ".hidden") {
		t.Error(".hidden directory should be skipped")
	}
	if !strings.Contains(output, "visible/") {
		t.Error("visible directory should be included")
	}
}

func TestBuildTree_SkipsSkippedDirs(t *testing.T) {
	fsys := fstest.MapFS{
		"file1.go":           &fstest.MapFile{},
		"node_modules/a.txt": &fstest.MapFile{},
		"vendor/b.txt":       &fstest.MapFile{},
		"src/c.txt":          &fstest.MapFile{},
	}

	var b strings.Builder
	dirs, files, count := 0, 0, 0
	buildTree(fsys, ".", "", 3, &b, &dirs, &files, &count)

	output := b.String()
	if strings.Contains(output, "node_modules") {
		t.Error("node_modules should be skipped")
	}
	if strings.Contains(output, "vendor") {
		t.Error("vendor should be skipped")
	}
	if !strings.Contains(output, "src/") {
		t.Error("src should be included")
	}
}

func TestBuildTree_DirError(t *testing.T) {
	// Use a non-existent directory - should not panic
	fsys := fstest.MapFS{}
	var b strings.Builder
	dirs, files, count := 0, 0, 0
	buildTree(fsys, "nonexistent", "", 3, &b, &dirs, &files, &count)

	// Should handle error gracefully (no panic, empty output)
	if b.Len() != 0 {
		t.Errorf("expected empty output for nonexistent dir, got %q", b.String())
	}
}

func TestBuildTree_NestedDirs(t *testing.T) {
	// Test with a simpler nested structure that works with fstest.MapFS
	fsys := fstest.MapFS{
		"file0.go":                 &fstest.MapFile{},
		"sub/file1.go":             &fstest.MapFile{},
		"sub/deep/file2.go":        &fstest.MapFile{},
		"sub/deep/deeper/file3.go": &fstest.MapFile{},
	}

	var b strings.Builder
	dirs, files, count := 0, 0, 0
	buildTree(fsys, ".", "", 5, &b, &dirs, &files, &count)

	// Should see at least some files in nested structure
	if files < 3 {
		t.Errorf("expected at least 3 files, got %d", files)
	}
}

func TestBuildTree_DepthZero(t *testing.T) {
	fsys := fstest.MapFS{
		"file1.go":      &fstest.MapFile{},
		"dir1/file2.go": &fstest.MapFile{},
	}

	var b strings.Builder
	dirs, files, count := 0, 0, 0
	buildTree(fsys, ".", "", 0, &b, &dirs, &files, &count)

	// Depth 0 should not recurse but can still list root-level items
	// With depth 0, we should not see files in subdirs
	output := b.String()
	if strings.Contains(output, "file2.go") {
		t.Error("file2.go should not appear with depth 0")
	}
}

func TestTreeHandler(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(dir+"/src/deep", 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.MkdirAll(dir+"/node_modules/pkg", 0o755); err != nil {
		t.Fatalf("mkdir node_modules: %v", err)
	}
	if err := os.WriteFile(dir+"/main.go", []byte("package main"), 0o644); err != nil {
		t.Fatalf("write main: %v", err)
	}
	if err := os.WriteFile(dir+"/src/lib.go", []byte("package src"), 0o644); err != nil {
		t.Fatalf("write lib: %v", err)
	}
	if err := os.WriteFile(dir+"/src/deep/hidden.go", []byte("package deep"), 0o644); err != nil {
		t.Fatalf("write hidden: %v", err)
	}
	if err := os.WriteFile(dir+"/node_modules/pkg/index.js", []byte("ignored"), 0o644); err != nil {
		t.Fatalf("write ignored: %v", err)
	}

	sb := testSandbox(t, dir)
	out, err := treeHandler(sb, TreeInput{Depth: 1})
	if err != nil {
		t.Fatalf("treeHandler: %v", err)
	}
	if out.Dirs != 1 || out.Files != 1 {
		t.Fatalf("counts dirs/files = %d/%d, want 1/1", out.Dirs, out.Files)
	}
	if !strings.Contains(out.Tree, "main.go") || !strings.Contains(out.Tree, "src/") {
		t.Fatalf("tree missing expected entries:\n%s", out.Tree)
	}
	if strings.Contains(out.Tree, "node_modules") || strings.Contains(out.Tree, "hidden.go") {
		t.Fatalf("tree included skipped/deep entries:\n%s", out.Tree)
	}
}

func TestTreeHandlerDefaultsClampsAndErrors(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(dir+"/a/b/c", 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(dir+"/a/b/c/file.txt", []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	sb := testSandbox(t, dir)
	out, err := treeHandler(sb, TreeInput{Path: ".", Depth: maxTreeDepth + 99})
	if err != nil {
		t.Fatalf("treeHandler: %v", err)
	}
	if !strings.Contains(out.Tree, "file.txt") {
		t.Fatalf("clamped deep tree missing file:\n%s", out.Tree)
	}
	if _, err := treeHandler(sb, TreeInput{Path: "../outside"}); err == nil {
		t.Fatal("expected path outside sandbox error")
	}
}

func TestTreeHandlerTruncatedSummary(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < maxTreeEntries+5; i++ {
		path := fmt.Sprintf("%s/file-%03d.txt", dir, i)
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	sb := testSandbox(t, dir)
	out, err := treeHandler(sb, TreeInput{Depth: 1})
	if err != nil {
		t.Fatalf("treeHandler: %v", err)
	}
	if !strings.Contains(out.Tree, "(truncated)") {
		t.Fatalf("expected truncated summary:\n%s", out.Tree)
	}
}

func TestNewTreeTool(t *testing.T) {
	sb := &Sandbox{}
	tool, err := newTreeTool(sb)
	if err != nil {
		t.Fatalf("newTreeTool() error = %v", err)
	}
	if tool == nil {
		t.Fatal("newTreeTool() returned nil")
	}
	name := tool.Name()
	if name != "tree" {
		t.Errorf("Name() = %q, want 'tree'", name)
	}
}
