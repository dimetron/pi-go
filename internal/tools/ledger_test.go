package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWrite_RefusesUnreadOverwrite is the case the ledger exists for: replacing
// a file the agent never looked at destroys content that was never in the
// transcript, so nothing can recover it.
func TestWrite_RefusesUnreadOverwrite(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)
	ledger := NewReadLedger()
	path := writeFile(t, dir, "existing.go", "package main\n\nfunc main() {}\n")

	_, err := writeHandlerWithLedger(sb, WriteInput{FilePath: path, Content: "clobbered\n"}, ledger)
	if err == nil {
		t.Fatal("overwriting an unread file should be refused")
	}
	if !strings.Contains(err.Error(), "has not been read yet") {
		t.Errorf("error %q does not name the actual problem", err)
	}

	// The file must be untouched.
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "func main") {
		t.Error("the refused write modified the file anyway")
	}
}

// TestWrite_RefusesPartiallyReadOverwrite checks the distinction that makes the
// refusal accurate: "you have not read this file" is wrong and confusing when
// the agent has read 2000 of 5000 lines.
func TestWrite_RefusesPartiallyReadOverwrite(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)
	ledger := NewReadLedger()

	var b strings.Builder
	for i := range 3000 {
		b.WriteString("line\n")
		_ = i
	}
	path := writeFile(t, dir, "big.go", b.String())

	out, err := readHandlerWithLedger(sb, ReadInput{FilePath: path}, ledger)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !out.Truncated {
		t.Fatal("expected the window to stop early, so the view is partial")
	}

	_, err = writeHandlerWithLedger(sb, WriteInput{FilePath: path, Content: "clobbered\n"}, ledger)
	if err == nil {
		t.Fatal("overwriting a partially-read file should be refused")
	}
	if !strings.Contains(err.Error(), "only part of") {
		t.Errorf("error %q does not distinguish a partial view from no view", err)
	}
	if strings.Contains(err.Error(), "has not been read yet") {
		t.Errorf("error misleadingly claims the file was never read: %q", err)
	}
}

func TestWrite_AllowsOverwriteAfterFullRead(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)
	ledger := NewReadLedger()
	path := writeFile(t, dir, "small.go", "package main\n")

	if _, err := readHandlerWithLedger(sb, ReadInput{FilePath: path}, ledger); err != nil {
		t.Fatalf("read: %v", err)
	}
	if _, err := writeHandlerWithLedger(sb, WriteInput{FilePath: path, Content: "package other\n"}, ledger); err != nil {
		t.Fatalf("overwrite after a full read should be allowed: %v", err)
	}

	data, _ := os.ReadFile(path)
	if string(data) != "package other\n" {
		t.Errorf("file = %q, want %q", string(data), "package other\n")
	}
}

// TestWrite_AllowsCreatingNewFile keeps the guard from turning into pointless
// friction: there is nothing to destroy in a file that does not exist.
func TestWrite_AllowsCreatingNewFile(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)
	ledger := NewReadLedger()

	path := filepath.Join(dir, "brand-new.go")
	if _, err := writeHandlerWithLedger(sb, WriteInput{FilePath: path, Content: "package main\n"}, ledger); err != nil {
		t.Fatalf("creating a new file should not require a read: %v", err)
	}
}

// TestWrite_RefusesAfterTheFileChangedUnderneath covers the stale-view case:
// the agent read the file, something else changed it, and the agent's view is
// now of bytes that no longer exist.
func TestWrite_RefusesAfterTheFileChangedUnderneath(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)
	ledger := NewReadLedger()
	path := writeFile(t, dir, "raced.go", "package main\n")

	if _, err := readHandlerWithLedger(sb, ReadInput{FilePath: path}, ledger); err != nil {
		t.Fatalf("read: %v", err)
	}

	// Someone else edits it.
	if err := os.WriteFile(path, []byte("package main\n\nvar Added = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := writeHandlerWithLedger(sb, WriteInput{FilePath: path, Content: "clobbered\n"}, ledger)
	if err == nil {
		t.Fatal("overwriting a file that changed since the read should be refused")
	}
	if !strings.Contains(err.Error(), "changed on disk") {
		t.Errorf("error %q does not explain the staleness", err)
	}
}

// TestEdit_DoesNotTripItsOwnStaleCheck guards against friction with no safety
// gained: the agent's own edit must not force a re-read before it can write.
func TestEdit_DoesNotTripItsOwnStaleCheck(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)
	ledger := NewReadLedger()
	path := writeFile(t, dir, "target.go", "package main\n\nconst A = 1\n")

	if _, err := readHandlerWithLedger(sb, ReadInput{FilePath: path}, ledger); err != nil {
		t.Fatalf("read: %v", err)
	}
	if _, err := editHandlerWithLedger(sb, EditInput{
		FilePath:  path,
		OldString: "const A = 1",
		NewString: "const A = 2",
	}, ledger); err != nil {
		t.Fatalf("edit: %v", err)
	}

	if _, err := writeHandlerWithLedger(sb, WriteInput{FilePath: path, Content: "package main\n"}, ledger); err != nil {
		t.Errorf("the agent's own edit forced a pointless re-read: %v", err)
	}
}

// TestLedger_TouchDoesNotGrantAView: refreshing a stat must not turn a file
// the agent never read into one it may overwrite.
func TestLedger_TouchDoesNotGrantAView(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)
	ledger := NewReadLedger()
	path := writeFile(t, dir, "unseen.go", "package main\n")

	info, err := sb.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	ledger.Touch(path, info)

	if err := ledger.CheckOverwrite(path, info); err == nil {
		t.Error("Touch granted an overwrite for a file that was never read")
	}
}

// TestLedger_FullReadSupersedesPartial: reading a window and then the whole
// file must leave a full view, not a partial one.
func TestLedger_FullReadSupersedesPartial(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)
	ledger := NewReadLedger()
	path := writeFile(t, dir, "f.go", "a\nb\nc\nd\n")

	// A windowed read starting past line 1 is a partial view.
	if _, err := readHandlerWithLedger(sb, ReadInput{FilePath: path, Offset: 3}, ledger); err != nil {
		t.Fatalf("read: %v", err)
	}
	info, _ := sb.Stat(path)
	if err := ledger.CheckOverwrite(path, info); err == nil {
		t.Fatal("a view starting at line 3 should not authorize an overwrite")
	}

	// Now read it from the top, in full.
	if _, err := readHandlerWithLedger(sb, ReadInput{FilePath: path}, ledger); err != nil {
		t.Fatalf("read: %v", err)
	}
	if err := ledger.CheckOverwrite(path, info); err != nil {
		t.Errorf("a full read should authorize the overwrite: %v", err)
	}
}

// TestLedger_NilIsInert keeps every direct handler call (and every existing
// test) working without a ledger.
func TestLedger_NilIsInert(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)
	path := writeFile(t, dir, "x.go", "package main\n")

	if _, err := writeHandler(sb, WriteInput{FilePath: path, Content: "replaced\n"}); err != nil {
		t.Errorf("writeHandler without a ledger should not gate: %v", err)
	}
}
