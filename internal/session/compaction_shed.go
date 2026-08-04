package session

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"
)

// nowFunc is swappable in tests so compaction output is deterministic.
var nowFunc = time.Now

// shedMinBytes is the payload size below which shedding a result costs more in
// stub text and lost detail than it reclaims.
const shedMinBytes = 400

// ShedResult reports what a shedding pass reclaimed.
type ShedResult struct {
	EventsScanned  int
	ResultsShed    int
	BytesReclaimed int
}

// ShedSupersededToolResults drops the payload of tool results that a later call
// on the same target has superseded, replacing each with a short stub.
//
// This is the cheap compaction stage: no LLM call, no rewriting of the stable
// prefix, and the conversation's shape — every call and its response — is
// preserved, so tool-call/response pairing stays intact for providers that
// validate it.
//
// The most recent keepRecent events are never touched, so the model's immediate
// working set always survives in full. Only the newest result for a given
// (tool, args) is kept; older ones are stale by definition, since the newest
// call re-read the same target.
func ShedSupersededToolResults(events []*session.Event, keepRecent int) ([]*session.Event, ShedResult) {
	return ShedSupersededToolResultsWithDedup(events, keepRecent, nil)
}

// ShedSupersededToolResultsWithDedup is the form shed uses when a dedup callback
// has already produced pointer stubs in this session. dedupPointers is the set
// of payload strings the deduper wrote; any response whose primary field
// matches one of them is treated as a tiny pointer (not a candidate for
// shedding) so the *real* earlier result it points at is never replaced with a
// stub, which would dangle the pointer.
//
// Passing nil for dedupPointers preserves the original behavior.
func ShedSupersededToolResultsWithDedup(
	events []*session.Event, keepRecent int, dedupPointers map[string]bool,
) ([]*session.Event, ShedResult) {
	res := ShedResult{EventsScanned: len(events)}
	if len(events) == 0 {
		return events, res
	}
	if keepRecent < 0 {
		keepRecent = 0
	}

	cutoff := len(events) - keepRecent
	if cutoff <= 0 {
		return events, res
	}

	// Index every FunctionCall by its ID so a FunctionResponse can look up the
	// args it was answering. Without this, the read tool's response (which only
	// carries content/total_lines/truncated, not file_path) would key on
	// nothing and two unrelated reads would collapse.
	callsByID := indexFunctionCalls(events)

	// Walk backwards so the first sighting of a (tool, args) key is the newest
	// call — the one worth keeping.
	seen := make(map[string]bool)
	for i := cutoff - 1; i >= 0; i-- {
		ev := events[i]
		if ev == nil || ev.Content == nil {
			continue
		}
		for _, part := range ev.Content.Parts {
			fr := part.FunctionResponse
			if fr == nil || fr.Response == nil {
				continue
			}
			if isDedupPointer(fr, dedupPointers) {
				continue
			}
			key := fr.Name + "\x00" + responseTargetKey(fr, callsByID)
			if !seen[key] {
				seen[key] = true
				continue
			}
			if n := shedResponsePayload(fr); n > 0 {
				res.ResultsShed++
				res.BytesReclaimed += n
			}
		}
	}
	return events, res
}

// indexFunctionCalls walks the events once and records every FunctionCall by
// its ID. The result lets a response look up the arguments it was answering
// without rescanning the slice for every candidate.
func indexFunctionCalls(events []*session.Event) map[string]*genai.FunctionCall {
	out := make(map[string]*genai.FunctionCall)
	for _, ev := range events {
		if ev == nil || ev.Content == nil {
			continue
		}
		for _, part := range ev.Content.Parts {
			if part == nil || part.FunctionCall == nil {
				continue
			}
			fc := part.FunctionCall
			if fc.ID == "" {
				continue
			}
			// Keep the first sighting — a second FunctionCall with the same ID
			// is a duplicate, and the first is the authoritative pair partner.
			if _, dup := out[fc.ID]; !dup {
				out[fc.ID] = fc
			}
		}
	}
	return out
}

// isDedupPointer reports whether the response's primary field is a dedup
// pointer text the deduper wrote. Pointers are tiny and carry no payload of
// their own — shedding them or shedding the real result they point at are both
// wrong, so we treat them as out-of-scope for the shed pass.
func isDedupPointer(fr *genai.FunctionResponse, dedupPointers map[string]bool) bool {
	if len(dedupPointers) == 0 || fr == nil || fr.Response == nil {
		return false
	}
	for _, key := range []string{"content", "stdout", "output", "result"} {
		v, ok := fr.Response[key]
		if !ok {
			continue
		}
		s, isStr := v.(string)
		if !isStr {
			continue
		}
		if dedupPointers[s] {
			return true
		}
	}
	return false
}

// responseTargetKey derives a stable identity for what a tool result describes.
// It prefers the paired FunctionCall's args (since the response map often
// omits identifying fields like file_path for read/grep) and only falls back
// to the response map when no paired call is available.
func responseTargetKey(fr *genai.FunctionResponse, callsByID map[string]*genai.FunctionCall) string {
	if fr != nil && fr.ID != "" {
		if fc, ok := callsByID[fr.ID]; ok && fc != nil {
			return canonicalCallKey(fc)
		}
	}
	// Fall back to the response map: file-oriented tools in ADK sometimes echo
	// the target path back in the response, and a synthetic event without a
	// paired call can still be keyed by its identifying fields.
	if fr != nil {
		return responseMapKey(fr.Response)
	}
	return ""
}

// canonicalCallKey produces a stable per-call key from the FunctionCall's name
// and the args that identify its target (file_path/path/pattern/command plus
// offset/limit for read).
func canonicalCallKey(fc *genai.FunctionCall) string {
	var b strings.Builder
	b.WriteString(fc.Name)
	b.WriteByte('\x00')
	for _, key := range []string{"file_path", "path", "filePath", "pattern", "command", "offset", "limit"} {
		v, ok := fc.Args[key]
		if !ok {
			continue
		}
		b.WriteString(key)
		b.WriteByte('=')
		enc, err := json.Marshal(v)
		if err != nil {
			fmt.Fprintf(&b, "%v", v)
		} else {
			b.Write(enc)
		}
		b.WriteByte('\x00')
	}
	return b.String()
}

// responseMapKey is the legacy fallback: read identifying fields out of the
// response map, since older code paths and synthetic events put the path there.
func responseMapKey(resp map[string]any) string {
	for _, key := range []string{"file_path", "path", "filePath", "pattern", "command"} {
		if v, ok := resp[key]; ok {
			if s, isStr := v.(string); isStr && s != "" {
				return key + "=" + s
			}
		}
	}
	// No named identifier was present. Key by a stable hash of the response's
	// non-payload fields so two different targets don't collapse, while a
	// genuine repeat (same content, no identifying fields) still does. A pure
	// content hash would over-collapse: two synthetic "ok" results would
	// match. A field-set hash is the right grain: distinct (field set, value
	// set) pairs are distinct.
	keys := make([]string, 0, len(resp))
	for k := range resp {
		if isPayloadField(k) {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		enc, err := json.Marshal(resp[k])
		if err != nil {
			continue
		}
		b.WriteString(k)
		b.WriteByte('=')
		b.Write(enc)
		b.WriteByte('\x00')
	}
	// If we collected no fields at all, the response carried only payload
	// data — fall back to a content hash so synthetic repeats still collapse
	// while truly different payloads (which would produce different
	// responseMapKey strings anyway) don't.
	if b.Len() == 0 {
		sum := sha256.Sum256([]byte(fmt.Sprintf("%v", resp)))
		return "hash=" + string(sum[:])
	}
	return b.String()
}

func isPayloadField(k string) bool {
	switch k {
	case "stdout", "content", "output", "diff", "result", "data":
		return true
	}
	return false
}

// shedResponsePayload replaces the bulk field of a tool response with a stub,
// returning the number of bytes reclaimed. Errors are left intact — they are
// small, and a shed error message would strand the model with no explanation
// for a failure it still has to handle.
func shedResponsePayload(fr *genai.FunctionResponse) int {
	if _, isErr := fr.Response["error"]; isErr {
		return 0
	}
	for _, key := range []string{"stdout", "content", "output", "diff", "result", "data"} {
		v, ok := fr.Response[key]
		if !ok {
			continue
		}
		s, isStr := v.(string)
		if !isStr || len(s) < shedMinBytes {
			continue
		}
		fr.Response[key] = fmt.Sprintf(
			"[superseded — a later %s call re-read this target; %d bytes dropped to reclaim context. "+
				"Use the newer result below, or call %s again if you need this content.]",
			fr.Name, len(s), fr.Name)
		return len(s)
	}
	return 0
}

// BuildSummarizedEvents performs the Codex-style rebuild: the initial context
// prefix, then the user's own messages newest-first up to keepUserTokens, then
// a single summary event.
//
// Tool calls and results are dropped entirely — that is the point, they are the
// bulk. User messages are preserved because they carry intent that a summary
// paraphrases lossily, and losing the user's own words is the failure mode that
// makes a compacted session feel like it forgot what was asked.
func BuildSummarizedEvents(events []*session.Event, summary string, keepUserTokens int) []*session.Event {
	if keepUserTokens <= 0 {
		keepUserTokens = DefaultAutoCompactConfig().KeepUserMessageTokens
	}

	var prefix []*session.Event
	if len(events) > 0 && isInitialContext(events[0]) {
		prefix = append(prefix, events[0])
	}

	// Walk user messages newest-first, taking whole messages while they fit.
	var kept []*session.Event
	budget := keepUserTokens
	for i := len(events) - 1; i >= 0; i-- {
		ev := events[i]
		if !isUserMessage(ev) || (len(prefix) > 0 && ev == prefix[0]) {
			continue
		}
		cost := estimateEventTokens([]*session.Event{ev})
		if cost > budget {
			continue
		}
		budget -= cost
		kept = append(kept, ev)
	}
	// Restore chronological order.
	for i, j := 0, len(kept)-1; i < j; i, j = i+1, j-1 {
		kept[i], kept[j] = kept[j], kept[i]
	}

	if strings.TrimSpace(summary) == "" {
		summary = "(no summary available)"
	}
	summaryEvent := &session.Event{
		ID:        "compaction-summary",
		Timestamp: nowFunc(),
		Author:    "system",
	}
	summaryEvent.Content = genai.NewContentFromText(
		SummaryPrefix+"\n\n"+summary, genai.RoleUser)

	out := make([]*session.Event, 0, len(prefix)+len(kept)+1)
	out = append(out, prefix...)
	out = append(out, kept...)
	out = append(out, summaryEvent)
	return out
}

// isUserMessage reports whether the event is a plain text message the user
// wrote, as opposed to a model turn or a tool exchange.
func isUserMessage(ev *session.Event) bool {
	if ev == nil || ev.Content == nil {
		return false
	}
	if ev.Author != "" && ev.Author != "user" {
		return false
	}
	if ev.Author == "" && ev.Content.Role != string(genai.RoleUser) {
		return false
	}
	hasText := false
	for _, part := range ev.Content.Parts {
		if part.FunctionCall != nil || part.FunctionResponse != nil {
			return false
		}
		if part.Text != "" {
			hasText = true
		}
	}
	return hasText
}

// isInitialContext reports whether the event is the session's opening context
// block, which must survive compaction so the rebuilt window keeps its
// grounding — and, because it is byte-stable, stays cacheable.
func isInitialContext(ev *session.Event) bool {
	if ev == nil || ev.Content == nil {
		return false
	}
	return ev.Author == "system" || ev.ID == "compaction-summary"
}
