package tools

import (
	"strings"
	"sync"
	"testing"
)

func bigContent(seed string) string {
	return strings.Repeat(seed, dedupMinBytes/len(seed)+2)
}

func TestDeduper_ElidesExactRepeat(t *testing.T) {
	d := NewResultDeduper()
	args := map[string]any{"file_path": "/a/b.go"}
	body := bigContent("package main\n")

	first := map[string]any{"content": body}
	d.apply("read", args, first)
	if first["content"] != body {
		t.Fatal("first call must pass through unchanged")
	}

	second := map[string]any{"content": body}
	d.apply("read", args, second)
	got, _ := second["content"].(string)
	if got == body {
		t.Fatal("exact repeat should have been elided")
	}
	if !strings.Contains(got, "identical to the result of the earlier read call #1") {
		t.Fatalf("pointer text missing or malformed: %q", got)
	}

	calls, bytes := d.Stats()
	if calls != 1 || bytes != len(body) {
		t.Fatalf("Stats() = (%d, %d), want (1, %d)", calls, bytes, len(body))
	}
}

func TestDeduper_ChangedContentPassesThrough(t *testing.T) {
	d := NewResultDeduper()
	args := map[string]any{"file_path": "/a/b.go"}

	original := bigContent("package main\n")
	edited := bigContent("package other\n")

	d.apply("read", args, map[string]any{"content": original})

	second := map[string]any{"content": edited}
	d.apply("read", args, second)
	if second["content"] != edited {
		t.Fatal("changed content must never be elided — the model would be served stale bytes")
	}

	// The changed content is now the recorded baseline, so a third identical
	// read elides against the edit, not the original.
	third := map[string]any{"content": edited}
	d.apply("read", args, third)
	if third["content"] == edited {
		t.Fatal("repeat of the edited content should elide")
	}

	// And a read that returns the original bytes again is a change once more.
	fourth := map[string]any{"content": original}
	d.apply("read", args, fourth)
	if fourth["content"] != original {
		t.Fatal("reverting content is a change and must pass through")
	}
}

func TestDeduper_DifferentArgsAreDistinct(t *testing.T) {
	d := NewResultDeduper()
	body := bigContent("same bytes\n")

	d.apply("read", map[string]any{"file_path": "/a.go"}, map[string]any{"content": body})

	other := map[string]any{"content": body}
	d.apply("read", map[string]any{"file_path": "/b.go"}, other)
	if other["content"] != body {
		t.Fatal("identical bytes from a different file must not be elided")
	}
}

func TestDeduper_ArgKeyOrderIsStable(t *testing.T) {
	d := NewResultDeduper()
	body := bigContent("x\n")

	d.apply("read", map[string]any{"file_path": "/a.go", "offset": 1, "limit": 50},
		map[string]any{"content": body})

	// Same args, built in a different order.
	second := map[string]any{"content": body}
	d.apply("read", map[string]any{"limit": 50, "file_path": "/a.go", "offset": 1}, second)
	if second["content"] == body {
		t.Fatal("argument key order must not affect the dedup key")
	}
}

func TestDeduper_SkipsSmallAndNonDedupTools(t *testing.T) {
	d := NewResultDeduper()
	small := "tiny"

	d.apply("read", map[string]any{"p": 1}, map[string]any{"content": small})
	second := map[string]any{"content": small}
	d.apply("read", map[string]any{"p": 1}, second)
	if second["content"] != small {
		t.Fatal("results below dedupMinBytes should not be elided")
	}

	body := bigContent("mutating\n")
	d.apply("write", map[string]any{"p": 1}, map[string]any{"content": body})
	w := map[string]any{"content": body}
	d.apply("write", map[string]any{"p": 1}, w)
	if w["content"] != body {
		t.Fatal("mutating tools must never be deduped")
	}
}

func TestDeduper_ErrorResultsAlwaysPassThrough(t *testing.T) {
	d := NewResultDeduper()
	args := map[string]any{"file_path": "/a.go"}
	body := bigContent("boom\n")

	d.apply("read", args, map[string]any{"content": body, "error": "denied"})
	second := map[string]any{"content": body, "error": "denied"}
	d.apply("read", args, second)
	if second["content"] != body {
		t.Fatal("error results must always be shown in full")
	}
}

func TestDeduper_ResetClearsPointers(t *testing.T) {
	d := NewResultDeduper()
	args := map[string]any{"file_path": "/a.go"}
	body := bigContent("y\n")

	d.apply("read", args, map[string]any{"content": body})
	d.Reset()

	// After compaction the earlier result is gone from the conversation, so the
	// next read must send full content rather than a dangling pointer.
	after := map[string]any{"content": body}
	d.apply("read", args, after)
	if after["content"] != body {
		t.Fatal("Reset must clear dedup memory so pointers cannot dangle")
	}
}

func TestDeduper_ConcurrentApplyIsRaceFree(t *testing.T) {
	d := NewResultDeduper()
	body := bigContent("parallel\n")

	var wg sync.WaitGroup
	for i := range 32 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			args := map[string]any{"file_path": "/a.go", "n": i % 4}
			d.apply("read", args, map[string]any{"content": body})
		}(i)
	}
	wg.Wait()

	if calls, _ := d.Stats(); calls == 0 {
		t.Fatal("expected some elisions across 32 calls over 4 distinct arg sets")
	}
}

func TestPrimaryOutputField_Precedence(t *testing.T) {
	tests := []struct {
		name      string
		result    map[string]any
		wantField string
	}{
		{"stdout wins", map[string]any{"stdout": "a", "content": "b"}, "stdout"},
		{"content", map[string]any{"content": "b"}, "content"},
		{"output", map[string]any{"output": "c"}, "output"},
		{"diff", map[string]any{"diff": "d"}, "diff"},
		{"empty string skipped", map[string]any{"stdout": "", "output": "c"}, "output"},
		{"non-string skipped", map[string]any{"stdout": 42, "output": "c"}, "output"},
		{"none", map[string]any{"exit_code": 0}, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := primaryOutputField(tc.result)
			if got != tc.wantField {
				t.Errorf("primaryOutputField = %q, want %q", got, tc.wantField)
			}
		})
	}
}
