package session

import (
	"testing"

	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"
)

// These tests pin the branch structure that callOrphansResponseOnTail and
// responseOrphansCallOnCompacted encoded before they were collapsed onto the
// shared firstPartID/rangeHasPartID pair. Both directions matter: they are
// what stops a tool call reaching the model with no result, or a result with
// no call, after a compaction cut.

// cxCall builds an event carrying a single FunctionCall part with the given ID.
func cxCall(id string) *session.Event {
	ev := &session.Event{Author: "pi"}
	ev.Content = &genai.Content{
		Role:  string(genai.RoleModel),
		Parts: []*genai.Part{{FunctionCall: &genai.FunctionCall{ID: id, Name: "read"}}},
	}
	return ev
}

// cxResp builds an event carrying a single FunctionResponse part with the ID.
func cxResp(id string) *session.Event {
	ev := &session.Event{}
	ev.Content = &genai.Content{
		Role:  string(genai.RoleUser),
		Parts: []*genai.Part{{FunctionResponse: &genai.FunctionResponse{ID: id, Name: "read"}}},
	}
	return ev
}

// cxText builds a plain text event: no call, no response.
func cxText(s string) *session.Event {
	ev := &session.Event{Author: "pi"}
	ev.Content = genai.NewContentFromText(s, genai.RoleModel)
	return ev
}

// cxParts builds an event from explicit parts, so tests can construct the
// nil-part and empty-ID shapes the walkers have to skip.
func cxParts(parts ...*genai.Part) *session.Event {
	ev := &session.Event{Author: "pi"}
	ev.Content = &genai.Content{Role: string(genai.RoleModel), Parts: parts}
	return ev
}

func TestFunctionPartIDReaders(t *testing.T) {
	callPart := &genai.Part{FunctionCall: &genai.FunctionCall{ID: "c1"}}
	respPart := &genai.Part{FunctionResponse: &genai.FunctionResponse{ID: "r1"}}
	textPart := &genai.Part{Text: "hello"}

	tests := []struct {
		name string
		id   partIDFunc
		part *genai.Part
		want string
	}{
		{"call reader on nil part", functionCallID, nil, ""},
		{"call reader on text part", functionCallID, textPart, ""},
		{"call reader on response part", functionCallID, respPart, ""},
		{"call reader on call part", functionCallID, callPart, "c1"},
		{"call reader on empty call ID", functionCallID, &genai.Part{FunctionCall: &genai.FunctionCall{}}, ""},
		{"response reader on nil part", functionResponseID, nil, ""},
		{"response reader on text part", functionResponseID, textPart, ""},
		{"response reader on call part", functionResponseID, callPart, ""},
		{"response reader on response part", functionResponseID, respPart, "r1"},
		{"response reader on empty response ID", functionResponseID, &genai.Part{FunctionResponse: &genai.FunctionResponse{}}, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.id(tc.part); got != tc.want {
				t.Errorf("id(part) = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestFirstPartID(t *testing.T) {
	tests := []struct {
		name string
		ev   *session.Event
		id   partIDFunc
		want string
	}{
		{"nil event", nil, functionCallID, ""},
		{"nil content", &session.Event{Author: "pi"}, functionCallID, ""},
		{"no parts", cxParts(), functionCallID, ""},
		{"nil part is skipped", cxParts(nil, &genai.Part{FunctionCall: &genai.FunctionCall{ID: "c2"}}), functionCallID, "c2"},
		{
			"empty ID is skipped, next non-empty wins",
			cxParts(
				&genai.Part{FunctionCall: &genai.FunctionCall{ID: ""}},
				&genai.Part{FunctionCall: &genai.FunctionCall{ID: "c3"}},
			),
			functionCallID,
			"c3",
		},
		{
			"first non-empty wins over later parts",
			cxParts(
				&genai.Part{FunctionCall: &genai.FunctionCall{ID: "first"}},
				&genai.Part{FunctionCall: &genai.FunctionCall{ID: "second"}},
			),
			functionCallID,
			"first",
		},
		{"wrong kind yields nothing", cxCall("c4"), functionResponseID, ""},
		{"response event read as response", cxResp("r4"), functionResponseID, "r4"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := firstPartID(tc.ev, tc.id); got != tc.want {
				t.Errorf("firstPartID = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRangeHasPartID(t *testing.T) {
	// Index:      0        1          2                3        4
	events := []*session.Event{cxCall("A"), cxText("x"), nil, cxResp("A"), {}}

	tests := []struct {
		name    string
		lo, hi  int
		id      partIDFunc
		want    string
		wantHit bool
	}{
		{"response present in range", 2, 5, functionResponseID, "A", true},
		{"response outside range", 0, 2, functionResponseID, "A", false},
		{"call present in range", 0, 3, functionCallID, "A", true},
		{"call not in range", 1, 5, functionCallID, "A", false},
		{"unknown ID never matches", 0, 5, functionCallID, "Z", false},
		{"empty range", 3, 3, functionResponseID, "A", false},
		{"inverted range scans nothing", 4, 1, functionResponseID, "A", false},
		{"nil event and nil content are skipped", 2, 3, functionResponseID, "A", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := rangeHasPartID(events, tc.lo, tc.hi, tc.id, tc.want); got != tc.wantHit {
				t.Errorf("rangeHasPartID(%d, %d, %q) = %v, want %v", tc.lo, tc.hi, tc.want, got, tc.wantHit)
			}
		})
	}
}

func TestCallOrphansResponseOnTail(t *testing.T) {
	tests := []struct {
		name     string
		events   []*session.Event
		splitIdx int
		want     bool
	}{
		{
			name:     "call at the edge with its response on the tail",
			events:   []*session.Event{cxText("ask"), cxCall("A"), cxResp("A")},
			splitIdx: 2,
			want:     true,
		},
		{
			name:     "call at the edge with its response also compacted",
			events:   []*session.Event{cxCall("A"), cxResp("A"), cxText("done")},
			splitIdx: 2,
			want:     false,
		},
		{
			name:     "call with no response anywhere",
			events:   []*session.Event{cxCall("A"), cxText("done")},
			splitIdx: 1,
			want:     false,
		},
		{
			name:     "tail response belongs to a different call",
			events:   []*session.Event{cxCall("A"), cxResp("B")},
			splitIdx: 1,
			want:     false,
		},
		{
			name:     "last compacted event is not a call",
			events:   []*session.Event{cxText("ask"), cxResp("A")},
			splitIdx: 1,
			want:     false,
		},
		{
			name:     "call with an empty ID cannot be paired",
			events:   []*session.Event{cxCall(""), cxResp("")},
			splitIdx: 1,
			want:     false,
		},
		{
			name:     "nil event at the edge",
			events:   []*session.Event{nil, cxResp("A")},
			splitIdx: 1,
			want:     false,
		},
		{
			name:     "nil content at the edge",
			events:   []*session.Event{{Author: "pi"}, cxResp("A")},
			splitIdx: 1,
			want:     false,
		},
		{
			name:     "index below zero",
			events:   []*session.Event{cxCall("A"), cxResp("A")},
			splitIdx: 0,
			want:     false,
		},
		{
			name:     "index at or past the end",
			events:   []*session.Event{cxCall("A")},
			splitIdx: 1,
			want:     false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			n := len(tc.events)
			got := callOrphansResponseOnTail(tc.events, tc.splitIdx-1, tc.splitIdx, n)
			if got != tc.want {
				t.Errorf("callOrphansResponseOnTail = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestResponseOrphansCallOnCompacted(t *testing.T) {
	tests := []struct {
		name     string
		events   []*session.Event
		splitIdx int
		want     bool
	}{
		{
			name:     "response at the edge with its call compacted",
			events:   []*session.Event{cxCall("A"), cxResp("A")},
			splitIdx: 1,
			want:     true,
		},
		{
			name:     "response at the edge with its call also on the tail",
			events:   []*session.Event{cxText("ask"), cxResp("A"), cxCall("A")},
			splitIdx: 1,
			want:     false,
		},
		{
			name:     "compacted call belongs to a different response",
			events:   []*session.Event{cxCall("B"), cxResp("A")},
			splitIdx: 1,
			want:     false,
		},
		{
			name:     "first tail event is not a response",
			events:   []*session.Event{cxCall("A"), cxText("done")},
			splitIdx: 1,
			want:     false,
		},
		{
			name:     "response with an empty ID cannot be paired",
			events:   []*session.Event{cxCall(""), cxResp("")},
			splitIdx: 1,
			want:     false,
		},
		{
			name:     "nil event at the edge",
			events:   []*session.Event{cxCall("A"), nil},
			splitIdx: 1,
			want:     false,
		},
		{
			name:     "nil content at the edge",
			events:   []*session.Event{cxCall("A"), {Author: "pi"}},
			splitIdx: 1,
			want:     false,
		},
		{
			name:     "nil event on the compacted side is skipped",
			events:   []*session.Event{nil, cxResp("A")},
			splitIdx: 1,
			want:     false,
		},
		{
			name:     "split index past the end",
			events:   []*session.Event{cxCall("A")},
			splitIdx: 1,
			want:     false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := responseOrphansCallOnCompacted(tc.events, tc.splitIdx); got != tc.want {
				t.Errorf("responseOrphansCallOnCompacted = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestAdvanceToCleanBoundary_ShiftsPastBothOrphanKinds exercises the two
// detectors through their only caller, including the case where the boundary
// has to walk past several consecutive pairs and the case where no clean cut
// exists at all.
func TestAdvanceToCleanBoundary_ShiftsPastBothOrphanKinds(t *testing.T) {
	tests := []struct {
		name     string
		events   []*session.Event
		splitIdx int
		want     int
	}{
		{
			// Interleaved pairs force the loop to take several steps and to
			// alternate between the two detectors: call(A)/call(B) each drag
			// the edge right, then resp(B) at the edge drags it right again
			// because its call is on the compacted side.
			name:     "walks past interleaved pairs, alternating detectors",
			events:   []*session.Event{cxCall("A"), cxCall("B"), cxResp("A"), cxResp("B")},
			splitIdx: 1,
			want:     4,
		},
		{
			name:     "stops at the first clean cut between two pairs",
			events:   []*session.Event{cxText("ask"), cxCall("A"), cxResp("A"), cxCall("B"), cxResp("B"), cxText("done")},
			splitIdx: 2,
			want:     3,
		},
		{
			name:     "already clean",
			events:   []*session.Event{cxText("a"), cxCall("A"), cxResp("A"), cxText("b")},
			splitIdx: 3,
			want:     3,
		},
		{
			name:     "no clean cut runs to the end",
			events:   []*session.Event{cxCall("A"), cxResp("A")},
			splitIdx: 1,
			want:     2,
		},
		{
			name:     "unpaired call needs no shift",
			events:   []*session.Event{cxCall("A"), cxText("b"), cxText("c")},
			splitIdx: 1,
			want:     1,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := advanceToCleanBoundary(tc.events, tc.splitIdx); got != tc.want {
				t.Errorf("advanceToCleanBoundary(%d) = %d, want %d", tc.splitIdx, got, tc.want)
			}
		})
	}
}
