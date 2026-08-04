package tools

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/tool"
)

// Repeat reads and searches are a large share of a long session's context: the
// agent re-reads a file it already read, and every copy is then re-sent on all
// subsequent turns. Measured over a day of pi-go sessions, 27% of read/search
// output bytes were byte-identical repeats of an earlier call.
//
// The deduper replaces those repeats with a one-line pointer back to the
// earlier call. It never skips execution and never elides on argument match
// alone — the tool runs, and the result is only replaced when it hashes
// identical to what the model was already shown. A changed file therefore
// always produces full content, so the model cannot be served stale bytes.

// dedupMinBytes is the size below which eliding is not worth the pointer text
// or the indirection it costs the model.
const dedupMinBytes = 512

// dedupTools are the read-only tools whose output is safe to elide on an exact
// repeat. Mutating tools (edit, write) and tools whose output is inherently
// time-varying are excluded — an identical hash there is meaningful, not noise.
var dedupTools = map[string]bool{
	"read":          true,
	"read_image":    true,
	"ripgrep":       true,
	"grep":          true,
	"find":          true,
	"ls":            true,
	"tree":          true,
	"git-overview":  true,
	"git-file-diff": true,
	"git-hunk":      true,
}

type dedupEntry struct {
	contentHash string
	callIndex   int
}

// ResultDeduper elides byte-identical repeats of read-only tool results within
// a single session. It is safe for concurrent use by parallel tool calls.
type ResultDeduper struct {
	mu    sync.Mutex
	seen  map[string]dedupEntry
	calls int

	savedCalls int
	savedBytes int
}

// NewResultDeduper creates an empty deduper.
func NewResultDeduper() *ResultDeduper {
	return &ResultDeduper{seen: make(map[string]dedupEntry)}
}

// Stats reports how many results were elided and how many bytes that saved on
// the turn they were elided. The lifetime saving is larger, because an elided
// result would otherwise have been re-sent on every later turn too.
func (d *ResultDeduper) Stats() (calls, bytes int) {
	if d == nil {
		return 0, 0
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.savedCalls, d.savedBytes
}

// FormatStats renders a human-readable summary for /rtk and /status.
func (d *ResultDeduper) FormatStats() string {
	calls, bytes := d.Stats()
	if calls == 0 {
		return "No duplicate tool results elided this session."
	}
	return fmt.Sprintf("Elided %d duplicate tool result(s), ~%d bytes (~%d tokens) of immediate context.",
		calls, bytes, bytes/4)
}

// Reset clears the dedup memory. Call this after compaction installs a fresh
// context window: the earlier results the pointers refer to are no longer in
// the conversation, so a pointer to them would dangle.
func (d *ResultDeduper) Reset() {
	if d == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.seen = make(map[string]dedupEntry)
	d.calls = 0
}

// BuildDedupCallback returns an AfterToolCallback that elides byte-identical
// repeat results. It must run AFTER the compactor so that both calls are
// compared in their final, post-compaction form — otherwise two results that
// compact to the same bytes would still be sent twice.
func BuildDedupCallback(d *ResultDeduper) llmagent.AfterToolCallback {
	return func(_ agent.Context, t tool.Tool, args, result map[string]any, err error) (map[string]any, error) {
		if d == nil || err != nil || result == nil {
			return result, nil
		}
		d.apply(t.Name(), args, result)
		return result, nil
	}
}

// apply replaces the result's output field with a pointer when this exact
// (tool, args, content) triple has already been shown to the model.
func (d *ResultDeduper) apply(toolName string, args, result map[string]any) {
	if !dedupTools[toolName] {
		return
	}
	// An error result is small and must always be shown in full.
	if _, isErr := result["error"]; isErr {
		return
	}

	field, content := primaryOutputField(result)
	if field == "" || len(content) < dedupMinBytes {
		return
	}

	key := toolName + "\x00" + canonicalArgs(args)
	hash := hashContent(content)

	d.mu.Lock()
	defer d.mu.Unlock()

	d.calls++
	current := d.calls

	prev, ok := d.seen[key]
	if !ok || prev.contentHash != hash {
		// First sighting, or the underlying content changed — record and let
		// the full result through.
		d.seen[key] = dedupEntry{contentHash: hash, callIndex: current}
		return
	}

	result[field] = fmt.Sprintf(
		"[identical to the result of the earlier %s call #%d in this session — "+
			"content is unchanged, %d bytes elided to save context. "+
			"Scroll back to that result rather than re-reading.]",
		toolName, prev.callIndex, len(content))

	d.savedCalls++
	d.savedBytes += len(content)
}

// primaryOutputField finds the result field carrying the bulk payload, using
// the same field precedence as applyCompaction.
func primaryOutputField(result map[string]any) (string, string) {
	for _, key := range []string{"stdout", "content", "output", "diff", "result", "data"} {
		v, ok := result[key]
		if !ok {
			continue
		}
		s, isStr := v.(string)
		if !isStr || s == "" {
			continue
		}
		return key, s
	}
	return "", ""
}

// canonicalArgs renders args in a stable form so that argument maps differing
// only in key order produce the same dedup key.
func canonicalArgs(args map[string]any) string {
	if len(args) == 0 {
		return ""
	}
	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteByte('=')
		// json.Marshal gives a stable rendering for nested values; fall back to
		// fmt for anything unmarshalable rather than dropping the key.
		if enc, err := json.Marshal(args[k]); err == nil {
			b.Write(enc)
		} else {
			fmt.Fprintf(&b, "%v", args[k])
		}
		b.WriteByte('\x00')
	}
	return b.String()
}

func hashContent(s string) string {
	sum := sha256.Sum256([]byte(s))
	return string(sum[:])
}
