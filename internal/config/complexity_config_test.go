package config

import (
	"encoding/json"
	"reflect"
	"sort"
	"testing"
)

// This file pins the behavior parseMCPServers encoded before it was split into
// parseMCPServerObject, parseMCPServerArray and applyMCPTransport. It parses
// user-authored config, so the malformed-input paths matter as much as the
// happy ones: every wrong-typed field below used to be swallowed by a failing
// type assertion, and must still be.

// sortServers gives the object format a deterministic order to compare against.
// Go map iteration is unordered, so parseMCPServerObject's output order is not
// part of the contract — its contents are.
func sortServers(s []MCPServer) []MCPServer {
	sort.Slice(s, func(i, j int) bool { return s[i].Name < s[j].Name })
	return s
}

// mustJSON parses a config fragment the way LoadFrom would, so the tests feed
// parseMCPServers exactly the `any` shapes encoding/json produces rather than
// hand-built maps that could differ.
func mustJSON(t *testing.T, s string) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatalf("bad test fixture %q: %v", s, err)
	}
	return v
}

func TestParseMCPServersFormats(t *testing.T) {
	tests := []struct {
		name string
		in   string // JSON, as it appears under "mcpServers"
		want []MCPServer
	}{
		// ---- Claude Desktop object format ----
		{
			name: "object format takes the name from the key",
			in:   `{"fs": {"command": "npx", "args": ["-y", "server-fs"]}}`,
			want: []MCPServer{{Name: "fs", Command: "npx", Args: []string{"-y", "server-fs"}}},
		},
		{
			name: "object format url transport with headers",
			in:   `{"api": {"url": "https://example.test/mcp", "headers": {"Authorization": "Bearer t"}}}`,
			want: []MCPServer{{Name: "api", URL: "https://example.test/mcp", Headers: map[string]string{"Authorization": "Bearer t"}}},
		},
		{
			name: "object format keeps every well-formed entry",
			in:   `{"a": {"command": "a"}, "b": {"url": "https://b.test"}}`,
			want: []MCPServer{{Name: "a", Command: "a"}, {Name: "b", URL: "https://b.test"}},
		},
		{
			// The comment in the source calls these "disabled servers": an entry
			// with neither a command nor a URL has no transport to dial.
			name: "object format skips an entry with neither command nor url",
			in:   `{"off": {"args": ["--x"]}, "on": {"command": "run"}}`,
			want: []MCPServer{{Name: "on", Command: "run"}},
		},
		{
			name: "object format skips an entry whose value is not an object",
			in:   `{"bogus": "just a string", "ok": {"command": "run"}}`,
			want: []MCPServer{{Name: "ok", Command: "run"}},
		},
		{
			name: "object format skips a null entry",
			in:   `{"bogus": null, "ok": {"command": "run"}}`,
			want: []MCPServer{{Name: "ok", Command: "run"}},
		},
		{
			name: "object format skips an entry that is an array",
			in:   `{"bogus": ["command", "run"], "ok": {"command": "run"}}`,
			want: []MCPServer{{Name: "ok", Command: "run"}},
		},
		{
			name: "empty object yields an empty, non-nil slice",
			in:   `{}`,
			want: []MCPServer{},
		},

		// ---- legacy array format ----
		{
			name: "array format takes the name from the element",
			in:   `[{"name": "fs", "command": "npx", "args": ["-y"]}]`,
			want: []MCPServer{{Name: "fs", Command: "npx", Args: []string{"-y"}}},
		},
		{
			name: "array format url transport with headers",
			in:   `[{"name": "api", "url": "https://example.test", "headers": {"X-K": "v"}}]`,
			want: []MCPServer{{Name: "api", URL: "https://example.test", Headers: map[string]string{"X-K": "v"}}},
		},
		{
			name: "array format preserves element order",
			in:   `[{"name": "b", "command": "b"}, {"name": "a", "command": "a"}]`,
			want: []MCPServer{{Name: "b", Command: "b"}, {Name: "a", Command: "a"}},
		},
		{
			name: "array format skips an element with neither command nor url",
			in:   `[{"name": "off"}, {"name": "on", "command": "run"}]`,
			want: []MCPServer{{Name: "on", Command: "run"}},
		},
		{
			// This is the branch parseMCPServerArray turned from a wrapping
			// `if ok { … }` into a guard `continue`; a non-object element is
			// dropped and must not stop the rest being read.
			name: "array format skips non-object elements and keeps going",
			in:   `["nope", 42, null, {"name": "on", "command": "run"}, true]`,
			want: []MCPServer{{Name: "on", Command: "run"}},
		},
		{
			// A nameless entry with a real transport is still usable, so it
			// survives with an empty Name rather than being dropped.
			name: "array format keeps an entry with no name but a command",
			in:   `[{"command": "run"}]`,
			want: []MCPServer{{Name: "", Command: "run"}},
		},
		{
			name: "empty array yields an empty, non-nil slice",
			in:   `[]`,
			want: []MCPServer{},
		},

		// ---- wrong-typed fields, in both formats ----
		{
			name: "non-string command is ignored, dropping the entry",
			in:   `{"x": {"command": 5}}`,
			want: []MCPServer{},
		},
		{
			name: "non-string url is ignored, dropping the entry",
			in:   `{"x": {"url": ["https://a"]}}`,
			want: []MCPServer{},
		},
		{
			name: "non-array args is ignored but the entry survives on its command",
			in:   `{"x": {"command": "run", "args": "not-a-list"}}`,
			want: []MCPServer{{Name: "x", Command: "run"}},
		},
		{
			name: "non-object headers is ignored but the entry survives",
			in:   `{"x": {"url": "https://a", "headers": "nope"}}`,
			want: []MCPServer{{Name: "x", URL: "https://a"}},
		},
		{
			name: "non-string name in the array format is ignored",
			in:   `[{"name": 7, "command": "run"}]`,
			want: []MCPServer{{Name: "", Command: "run"}},
		},
		{
			// toStringSlice keeps the slot but leaves a non-string element
			// empty, so the positional args line up with what was configured.
			name: "non-string args elements become empty strings",
			in:   `{"x": {"command": "run", "args": ["-y", 5, null]}}`,
			want: []MCPServer{{Name: "x", Command: "run", Args: []string{"-y", "", ""}}},
		},
		{
			// toStringMap drops non-string values, and an all-non-string map
			// collapses to nil rather than an empty map.
			name: "headers with no string values collapse to nil",
			in:   `{"x": {"url": "https://a", "headers": {"n": 1}}}`,
			want: []MCPServer{{Name: "x", URL: "https://a"}},
		},
		{
			name: "headers keep only the string values",
			in:   `{"x": {"url": "https://a", "headers": {"n": 1, "s": "keep"}}}`,
			want: []MCPServer{{Name: "x", URL: "https://a", Headers: map[string]string{"s": "keep"}}},
		},
		{
			name: "empty headers object collapses to nil",
			in:   `{"x": {"url": "https://a", "headers": {}}}`,
			want: []MCPServer{{Name: "x", URL: "https://a"}},
		},
		{
			name: "empty args array yields an empty, non-nil slice",
			in:   `{"x": {"command": "run", "args": []}}`,
			want: []MCPServer{{Name: "x", Command: "run", Args: []string{}}},
		},
		{
			// An empty-string command is indistinguishable from an absent one,
			// so the entry is skipped as having no transport.
			name: "empty string command does not count as a transport",
			in:   `{"x": {"command": ""}}`,
			want: []MCPServer{},
		},
		{
			name: "both command and url are kept when both are present",
			in:   `{"x": {"command": "run", "url": "https://a"}}`,
			want: []MCPServer{{Name: "x", Command: "run", URL: "https://a"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseMCPServers(mustJSON(t, tt.in))
			if !reflect.DeepEqual(sortServers(got), sortServers(tt.want)) {
				t.Errorf("parseMCPServers(%s) = %+v, want %+v", tt.in, got, tt.want)
			}
		})
	}
}

// The two formats above are the only ones parseMCPServers recognizes; anything
// else — including a config file that set mcpServers to a scalar — yields nil
// rather than a panic or an empty slice.
func TestParseMCPServersUnsupportedShapes(t *testing.T) {
	tests := []struct {
		name string
		in   any
	}{
		{"nil", nil},
		{"string", "servers.json"},
		{"number", float64(3)},
		{"bool", true},
		{"typed nil map", map[string]any(nil)},
		{"typed nil slice", []any(nil)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseMCPServers(tt.in)
			// A nil map or slice still takes its format branch and returns the
			// empty slice that branch builds; only the unsupported types are
			// nil. Either way the caller sees no servers.
			if len(got) != 0 {
				t.Errorf("parseMCPServers(%v) = %+v, want no servers", tt.in, got)
			}
		})
	}
	if got := parseMCPServers(nil); got != nil {
		t.Errorf("parseMCPServers(nil) = %+v, want nil", got)
	}
	if got := parseMCPServers("nope"); got != nil {
		t.Errorf("parseMCPServers(string) = %+v, want nil", got)
	}
}

// applyMCPTransport is shared by both formats, so it is worth pinning on its
// own: it must only ever set the four transport fields, and must leave a field
// untouched — not zero it — when the key is absent or wrongly typed.
func TestApplyMCPTransportLeavesUnsetFieldsAlone(t *testing.T) {
	// Start from a fully populated server, then apply an object that names
	// nothing. Every field must survive.
	srv := MCPServer{
		Name:    "keep-me",
		Command: "keep-cmd",
		Args:    []string{"keep-arg"},
		URL:     "keep-url",
		Headers: map[string]string{"keep": "header"},
	}
	before := srv

	applyMCPTransport(&srv, map[string]any{"unrelated": "value"})
	if !reflect.DeepEqual(srv, before) {
		t.Errorf("applyMCPTransport with no known keys changed %+v to %+v", before, srv)
	}

	applyMCPTransport(&srv, map[string]any{
		"command": 1, "args": "x", "url": false, "headers": []any{"y"},
	})
	if !reflect.DeepEqual(srv, before) {
		t.Errorf("applyMCPTransport with wrongly typed keys changed %+v to %+v", before, srv)
	}

	// Name is never read from the object, even when the object has one.
	applyMCPTransport(&srv, map[string]any{"name": "other", "command": "new-cmd"})
	if srv.Name != "keep-me" {
		t.Errorf("applyMCPTransport overwrote Name with %q", srv.Name)
	}
	if srv.Command != "new-cmd" {
		t.Errorf("applyMCPTransport did not set Command, got %q", srv.Command)
	}
}

// The two format helpers must agree on everything except where the name comes
// from — that equivalence is what let the shared applyMCPTransport be extracted.
func TestMCPServerFormatsAgreeOnTransport(t *testing.T) {
	object := parseMCPServers(mustJSON(t, `{"fs": {"command": "npx", "args": ["-y", "s"], "url": "https://u", "headers": {"H": "v"}}}`))
	array := parseMCPServers(mustJSON(t, `[{"name": "fs", "command": "npx", "args": ["-y", "s"], "url": "https://u", "headers": {"H": "v"}}]`))
	if !reflect.DeepEqual(object, array) {
		t.Errorf("object format %+v and array format %+v disagree", object, array)
	}
}
