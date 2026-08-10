package tools

import (
	"strings"
	"testing"

	"google.golang.org/adk/v2/tool"
)

func findTool(t *testing.T, tools []tool.Tool, name string) tool.Tool {
	t.Helper()
	for _, tl := range tools {
		if d, ok := tl.(interface{ Name() string }); ok && d.Name() == name {
			return tl
		}
	}
	t.Fatalf("tool %q not found", name)
	return nil
}

// TestCoreTools_ReadRepairsParameterAliases exercises the alias map as the
// registry actually applies it. Each spelling the model reaches for and the
// tool rejects costs a full turn on a schema error, so the repair is worth
// more than the line it takes.
func TestCoreTools_ReadRepairsParameterAliases(t *testing.T) {
	sb := testSandbox(t, t.TempDir())

	tools, err := CoreTools(sb)
	if err != nil {
		t.Fatalf("CoreTools: %v", err)
	}
	ct, ok := findTool(t, tools, "read").(*coercingTool)
	if !ok {
		t.Fatal("read tool is not wrapped for alias repair")
	}

	for _, alias := range []string{"path", "filePath", "filepath", "absolutePath", "target_file", "file"} {
		t.Run(alias, func(t *testing.T) {
			args := map[string]any{alias: "/tmp/x.go"}
			ct.aliasArgs(args)
			if got := args["file_path"]; got != "/tmp/x.go" {
				t.Errorf("%q was not repaired to file_path (args=%v)", alias, args)
			}
			if _, stillThere := args[alias]; stillThere {
				t.Errorf("%q survived alongside file_path (args=%v)", alias, args)
			}
		})
	}

	for _, alias := range []string{"start_line", "startLine"} {
		args := map[string]any{alias: 42}
		ct.aliasArgs(args)
		if got := args["offset"]; got != 42 {
			t.Errorf("%q was not repaired to offset (args=%v)", alias, args)
		}
	}
}

// TestWithReadLedger_SharesOneLedgerAcrossTools is the wiring check: read and
// write must consult the same ledger, or the guard is trivially defeated by
// reading with one tool and writing with another.
func TestWithReadLedger_SharesOneLedgerAcrossTools(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)
	path := writeFile(t, dir, "shared.go", "package main\n")

	ledger := NewReadLedger()
	if _, err := CoreTools(sb, WithReadLedger(ledger)); err != nil {
		t.Fatalf("CoreTools: %v", err)
	}

	// Nothing read yet: the overwrite is refused.
	_, err := writeHandlerWithLedger(sb, WriteInput{FilePath: path, Content: "clobbered\n"}, ledger)
	if err == nil {
		t.Fatal("overwriting an unread file should be refused")
	}
	if !strings.Contains(err.Error(), "has not been read yet") {
		t.Errorf("unexpected refusal: %v", err)
	}

	// A read recorded on the shared ledger authorizes the write.
	if _, err := readHandlerWithLedger(sb, ReadInput{FilePath: path}, ledger); err != nil {
		t.Fatalf("read: %v", err)
	}
	if _, err := writeHandlerWithLedger(sb, WriteInput{FilePath: path, Content: "replaced\n"}, ledger); err != nil {
		t.Errorf("write after a read on the same ledger was refused: %v", err)
	}
}

// TestCoreTools_DefaultsToAPrivateLedger keeps callers that never opt in
// working, with the guard still active within one tool set.
func TestCoreTools_DefaultsToAPrivateLedger(t *testing.T) {
	sb := testSandbox(t, t.TempDir())

	tools, err := CoreTools(sb)
	if err != nil {
		t.Fatalf("CoreTools without options should succeed: %v", err)
	}
	if findTool(t, tools, "read") == nil || findTool(t, tools, "write") == nil {
		t.Fatal("core tool set is missing read or write")
	}
}
