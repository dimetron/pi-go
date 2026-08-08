package tools

import (
	"errors"
	"strings"
	"testing"
)

func TestDeduper_FormatStats(t *testing.T) {
	d := NewResultDeduper()

	if got, want := d.FormatStats(), "No duplicate tool results elided this session."; got != want {
		t.Errorf("FormatStats() on a fresh deduper = %q, want %q", got, want)
	}

	args := map[string]any{"file_path": "/a/b.go"}
	body := bigContent("package main\n")
	d.apply("read", args, map[string]any{"content": body})
	d.apply("read", args, map[string]any{"content": body})

	got := d.FormatStats()
	for _, want := range []string{"Elided 1 duplicate tool result(s)", "bytes", "tokens"} {
		if !strings.Contains(got, want) {
			t.Errorf("FormatStats() = %q, want it to contain %q", got, want)
		}
	}
}

func TestDeduper_NilReceiverIsInert(t *testing.T) {
	// The deduper is optional wiring, so every exported method has to tolerate
	// a nil receiver rather than making each caller check.
	var d *ResultDeduper

	calls, bytes := d.Stats()
	if calls != 0 || bytes != 0 {
		t.Errorf("nil Stats() = (%d, %d), want (0, 0)", calls, bytes)
	}
	d.Reset() // must not panic
}

func TestBuildDedupCallback_ElidesRepeatThroughTheCallback(t *testing.T) {
	d := NewResultDeduper()
	cb := BuildDedupCallback(d)
	tl := &fakeCompactTool{name: "read"}
	args := map[string]any{"file_path": "/a/b.go"}
	body := bigContent("package main\n")

	first := map[string]any{"content": body}
	got, err := cb(nil, tl, args, first, nil)
	if err != nil {
		t.Fatalf("callback: %v", err)
	}
	if got["content"] != body {
		t.Error("first call must pass through unchanged")
	}

	second := map[string]any{"content": body}
	got, err = cb(nil, tl, args, second, nil)
	if err != nil {
		t.Fatalf("callback: %v", err)
	}
	elided, _ := got["content"].(string)
	if elided == body {
		t.Error("repeat call was not elided")
	}
	if !strings.Contains(elided, "identical to the result of the earlier read call") {
		t.Errorf("pointer text missing: %q", elided)
	}
}

func TestBuildDedupCallback_PassesThroughWhenNotApplicable(t *testing.T) {
	body := bigContent("package main\n")
	args := map[string]any{"file_path": "/a/b.go"}

	tests := []struct {
		name    string
		deduper *ResultDeduper
		result  map[string]any
		err     error
	}{
		{"nil deduper", nil, map[string]any{"content": body}, nil},
		{"tool errored", NewResultDeduper(), map[string]any{"content": body}, errors.New("boom")},
		{"nil result", NewResultDeduper(), nil, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cb := BuildDedupCallback(tc.deduper)
			tl := &fakeCompactTool{name: "read"}

			// Two identical calls; neither may be elided.
			for i := range 2 {
				got, err := cb(nil, tl, args, tc.result, tc.err)
				if err != nil {
					t.Fatalf("callback returned error: %v", err)
				}
				if tc.result == nil {
					if got != nil {
						t.Errorf("call %d: got %v, want nil passed through", i, got)
					}
					continue
				}
				if got["content"] != body {
					t.Errorf("call %d: content was modified: %v", i, got["content"])
				}
			}
		})
	}
}

func TestDeduper_ResetAlsoResetsCallNumbering(t *testing.T) {
	d := NewResultDeduper()
	args := map[string]any{"file_path": "/a/b.go"}
	body := bigContent("package main\n")

	d.apply("read", args, map[string]any{"content": body})
	d.Reset()

	// After a reset the earlier result is no longer in the conversation, so the
	// next identical call must pass through in full rather than pointing back.
	after := map[string]any{"content": body}
	d.apply("read", args, after)
	if after["content"] != body {
		t.Error("a repeat after Reset must pass through in full, not point at a dropped result")
	}

	// And the call counter restarts, so the next pointer says #1.
	repeat := map[string]any{"content": body}
	d.apply("read", args, repeat)
	got, _ := repeat["content"].(string)
	if !strings.Contains(got, "call #1") {
		t.Errorf("call numbering did not restart after Reset: %q", got)
	}
}

func TestCanonicalArgs(t *testing.T) {
	tests := []struct {
		name string
		args map[string]any
		want string
	}{
		{"empty", map[string]any{}, ""},
		{"nil", nil, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := canonicalArgs(tc.args); got != tc.want {
				t.Errorf("canonicalArgs(%v) = %q, want %q", tc.args, got, tc.want)
			}
		})
	}
}

func TestCanonicalArgs_StableAcrossKeyOrderAndNesting(t *testing.T) {
	a := canonicalArgs(map[string]any{"b": 2, "a": 1, "nested": map[string]any{"x": 1, "y": 2}})
	b := canonicalArgs(map[string]any{"nested": map[string]any{"y": 2, "x": 1}, "a": 1, "b": 2})
	if a != b {
		t.Errorf("canonicalArgs is not order-stable:\n%q\n%q", a, b)
	}

	different := canonicalArgs(map[string]any{"a": 1, "b": 3})
	if a == different {
		t.Error("different values produced the same canonical form")
	}
}

func TestCanonicalArgs_FallsBackForUnmarshalableValues(t *testing.T) {
	// A channel cannot be JSON-encoded; the key must still appear rather than
	// being silently dropped, which would collapse two distinct calls.
	got := canonicalArgs(map[string]any{"ch": make(chan int), "file_path": "/a.go"})
	if !strings.Contains(got, "ch=") {
		t.Errorf("unmarshalable key was dropped: %q", got)
	}
	if !strings.Contains(got, "file_path=") {
		t.Errorf("neighboring key was lost: %q", got)
	}
}

func TestPrimaryOutputField_SkipsEmptyAndNonStringValues(t *testing.T) {
	// A non-string or empty value in a higher-precedence field must not stop
	// the search at that field.
	field, content := primaryOutputField(map[string]any{"stdout": 42, "content": "", "output": "real"})
	if field != "output" || content != "real" {
		t.Errorf("primaryOutputField = (%q, %q), want (\"output\", \"real\")", field, content)
	}

	field, content = primaryOutputField(map[string]any{"unrelated": "x"})
	if field != "" || content != "" {
		t.Errorf("primaryOutputField with no payload = (%q, %q), want empty", field, content)
	}
}
