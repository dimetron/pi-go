package session

import (
	"strings"
	"testing"
	"time"

	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"
)

func TestAutoCompactConfig_Decide(t *testing.T) {
	cfg := DefaultAutoCompactConfig()
	const window = 200_000

	tests := []struct {
		name       string
		bodyTokens int64
		window     int64
		want       CompactionAction
	}{
		{"idle", 1_000, window, CompactionNone},
		{"just below shed", 119_000, window, CompactionNone},
		{"at shed threshold", 120_000, window, CompactionShed},
		{"between stages", 150_000, window, CompactionShed},
		{"just below summarize", 179_000, window, CompactionShed},
		{"at summarize threshold", 180_000, window, CompactionSummarize},
		{"over window", 250_000, window, CompactionSummarize},
		{"unknown window disables", 150_000, 0, CompactionNone},
		{"zero body", 0, window, CompactionNone},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := cfg.Decide(tc.bodyTokens, tc.window); got != tc.want {
				t.Errorf("Decide(%d, %d) = %v, want %v", tc.bodyTokens, tc.window, got, tc.want)
			}
		})
	}
}

func TestAutoCompactConfig_DisabledNeverActs(t *testing.T) {
	cfg := DefaultAutoCompactConfig()
	cfg.Enabled = false
	if got := cfg.Decide(999_999, 200_000); got != CompactionNone {
		t.Errorf("disabled config returned %v, want none", got)
	}
}

func TestAutoCompactConfig_NormalizeClampsThresholds(t *testing.T) {
	// A config that would summarize before it sheds must be corrected, not
	// obeyed — otherwise the expensive stage fires first and the cheap one is
	// dead code.
	cfg := AutoCompactConfig{Enabled: true, ShedPercent: 90, SummarizePercent: 50}
	n := cfg.normalize()
	if n.ShedPercent > n.SummarizePercent {
		t.Errorf("shed (%d) must not exceed summarize (%d)", n.ShedPercent, n.SummarizePercent)
	}

	// Summarizing above 95% leaves no room for the summarization request.
	over := AutoCompactConfig{Enabled: true, SummarizePercent: 99}.normalize()
	if over.SummarizePercent != 95 {
		t.Errorf("SummarizePercent = %d, want clamp to 95", over.SummarizePercent)
	}

	// Zero fields fall back to defaults rather than to zero thresholds, which
	// would make every session compact immediately.
	empty := AutoCompactConfig{Enabled: true}.normalize()
	d := DefaultAutoCompactConfig()
	if empty.ShedPercent != d.ShedPercent || empty.SummarizePercent != d.SummarizePercent {
		t.Errorf("empty config normalized to %d/%d, want %d/%d",
			empty.ShedPercent, empty.SummarizePercent, d.ShedPercent, d.SummarizePercent)
	}
	if empty.KeepRecentEvents != d.KeepRecentEvents || empty.KeepUserMessageTokens != d.KeepUserMessageTokens {
		t.Error("empty config must inherit keep-* defaults")
	}
}

// --- shed stage ------------------------------------------------------------

func toolResultEvent(name, path, body string) *session.Event {
	ev := &session.Event{Timestamp: time.Unix(0, 0), Author: "tool"}
	ev.Content = &genai.Content{
		Role: string(genai.RoleUser),
		Parts: []*genai.Part{{
			FunctionResponse: &genai.FunctionResponse{
				Name:     name,
				Response: map[string]any{"file_path": path, "content": body},
			},
		}},
	}
	return ev
}

func userEvent(text string) *session.Event {
	ev := &session.Event{Timestamp: time.Unix(0, 0), Author: "user"}
	ev.Content = genai.NewContentFromText(text, genai.RoleUser)
	return ev
}

func responseBody(ev *session.Event) string {
	fr := ev.Content.Parts[0].FunctionResponse
	s, _ := fr.Response["content"].(string)
	return s
}

func TestShed_DropsSupersededKeepsNewest(t *testing.T) {
	body := strings.Repeat("x", shedMinBytes*2)
	events := []*session.Event{
		toolResultEvent("read", "/a.go", body),
		toolResultEvent("read", "/a.go", body),
		toolResultEvent("read", "/a.go", body),
	}

	_, res := ShedSupersededToolResults(events, 0)
	if res.ResultsShed != 2 {
		t.Fatalf("ResultsShed = %d, want 2", res.ResultsShed)
	}
	if responseBody(events[2]) != body {
		t.Error("the newest result must survive in full")
	}
	for i := range 2 {
		if responseBody(events[i]) == body {
			t.Errorf("event %d should have been shed", i)
		}
		if !strings.Contains(responseBody(events[i]), "superseded") {
			t.Errorf("event %d stub text malformed: %q", i, responseBody(events[i]))
		}
	}
}

func TestShed_KeepsRecentTailUntouched(t *testing.T) {
	body := strings.Repeat("y", shedMinBytes*2)
	events := []*session.Event{
		toolResultEvent("read", "/a.go", body),
		toolResultEvent("read", "/a.go", body),
		toolResultEvent("read", "/a.go", body),
	}

	// keepRecent covers everything, so nothing may be shed.
	_, res := ShedSupersededToolResults(events, 3)
	if res.ResultsShed != 0 {
		t.Fatalf("ResultsShed = %d, want 0 when keepRecent covers all events", res.ResultsShed)
	}
	for i, ev := range events {
		if responseBody(ev) != body {
			t.Errorf("event %d was shed despite being in the protected tail", i)
		}
	}
}

func TestShed_DistinctTargetsNotCollapsed(t *testing.T) {
	body := strings.Repeat("z", shedMinBytes*2)
	events := []*session.Event{
		toolResultEvent("read", "/a.go", body),
		toolResultEvent("read", "/b.go", body),
	}
	_, res := ShedSupersededToolResults(events, 0)
	if res.ResultsShed != 0 {
		t.Fatalf("ResultsShed = %d, want 0 — different files are not supersessions", res.ResultsShed)
	}
}

func TestShed_LeavesErrorsAndSmallResults(t *testing.T) {
	small := "tiny"
	events := []*session.Event{
		toolResultEvent("read", "/a.go", small),
		toolResultEvent("read", "/a.go", small),
	}
	if _, res := ShedSupersededToolResults(events, 0); res.ResultsShed != 0 {
		t.Errorf("small results should not be shed, got %d", res.ResultsShed)
	}

	body := strings.Repeat("e", shedMinBytes*2)
	errEvents := []*session.Event{
		toolResultEvent("read", "/c.go", body),
		toolResultEvent("read", "/c.go", body),
	}
	for _, ev := range errEvents {
		ev.Content.Parts[0].FunctionResponse.Response["error"] = "denied"
	}
	if _, res := ShedSupersededToolResults(errEvents, 0); res.ResultsShed != 0 {
		t.Errorf("error results must never be shed, got %d", res.ResultsShed)
	}
}

func TestShed_EmptyAndNilSafe(t *testing.T) {
	if _, res := ShedSupersededToolResults(nil, 5); res.ResultsShed != 0 {
		t.Error("nil events must be handled without panic")
	}
	events := []*session.Event{nil, {}}
	if _, res := ShedSupersededToolResults(events, 0); res.ResultsShed != 0 {
		t.Error("nil/empty events must be skipped without panic")
	}
}

// toolCallEvent returns a synthetic FunctionCall event with the given ID and
// args. The real ADK path emits the call and its response in separate events
// tied together by ID; the response alone does not carry the path.
func toolCallEvent(id, name string, args map[string]any) *session.Event {
	ev := &session.Event{Timestamp: time.Unix(0, 0), Author: "pi"}
	ev.Content = &genai.Content{
		Role:  string(genai.RoleModel),
		Parts: []*genai.Part{{FunctionCall: &genai.FunctionCall{ID: id, Name: name, Args: args}}},
	}
	return ev
}

// toolResultForCall mirrors the production read tool: the response carries
// only content/total_lines/truncated, never the path. The path lives in the
// paired FunctionCall's args.
func toolResultForCall(id, name, body string) *session.Event {
	ev := &session.Event{Timestamp: time.Unix(0, 0), Author: "tool"}
	ev.Content = &genai.Content{
		Role: string(genai.RoleUser),
		Parts: []*genai.Part{{
			FunctionResponse: &genai.FunctionResponse{
				ID:       id,
				Name:     name,
				Response: map[string]any{"content": body, "total_lines": 10, "truncated": false},
			},
		}},
	}
	return ev
}

func TestShed_KeysByPairedCallArgs(t *testing.T) {
	body := strings.Repeat("z", shedMinBytes*2)
	// Two reads of different files. The response maps carry no path; the path
	// lives only on the paired FunctionCall. Without the paired-call lookup,
	// both responses would key on "" and the second would be shed.
	events := []*session.Event{
		toolCallEvent("c1", "read", map[string]any{"file_path": "/a.go"}),
		toolResultForCall("c1", "read", body),
		toolCallEvent("c2", "read", map[string]any{"file_path": "/b.go"}),
		toolResultForCall("c2", "read", body),
	}
	if _, res := ShedSupersededToolResults(events, 0); res.ResultsShed != 0 {
		t.Fatalf("ResultsShed = %d, want 0 — different file_path in the call must not collapse", res.ResultsShed)
	}
	for i, ev := range []*session.Event{events[1], events[3]} {
		if responseBody(ev) != body {
			t.Errorf("event %d was shed despite targeting a unique file", i)
		}
	}
}

func TestShed_SameCallArgsCollapse(t *testing.T) {
	body := strings.Repeat("y", shedMinBytes*2)
	events := []*session.Event{
		toolCallEvent("c1", "read", map[string]any{"file_path": "/a.go"}),
		toolResultForCall("c1", "read", body),
		toolCallEvent("c2", "read", map[string]any{"file_path": "/a.go"}),
		toolResultForCall("c2", "read", body),
	}
	_, res := ShedSupersededToolResults(events, 0)
	if res.ResultsShed != 1 {
		t.Fatalf("ResultsShed = %d, want 1 — same call args must collapse to one shed", res.ResultsShed)
	}
	if responseBody(events[1]) == body {
		t.Error("the older result should have been shed")
	}
	if responseBody(events[3]) != body {
		t.Error("the newer result must survive in full")
	}
}

func TestShed_LeavesDedupPointersAlone(t *testing.T) {
	body := strings.Repeat("p", shedMinBytes*2)
	pointer := "[identical to the result of the earlier read call #1 in this session — " +
		"content is unchanged, 1234 bytes elided to save context. " +
		"Scroll back to that result rather than re-reading.]"
	// The first read is full; the second was elided to a pointer by the deduper.
	events := []*session.Event{
		toolCallEvent("c1", "read", map[string]any{"file_path": "/a.go"}),
		toolResultForCall("c1", "read", body),
		toolCallEvent("c2", "read", map[string]any{"file_path": "/a.go"}),
		func() *session.Event {
			ev := &session.Event{Timestamp: time.Unix(0, 0), Author: "tool"}
			ev.Content = &genai.Content{
				Role: string(genai.RoleUser),
				Parts: []*genai.Part{{
					FunctionResponse: &genai.FunctionResponse{
						ID:       "c2",
						Name:     "read",
						Response: map[string]any{"content": pointer, "total_lines": 10, "truncated": false},
					},
				}},
			}
			return ev
		}(),
	}
	dedupPointers := map[string]bool{pointer: true}
	if _, res := ShedSupersededToolResultsWithDedup(events, 0, dedupPointers); res.ResultsShed != 0 {
		t.Fatalf("ResultsShed = %d, want 0 — the dedup pointer must not be touched", res.ResultsShed)
	}
	if responseBody(events[1]) != body {
		t.Error("the real earlier result must survive; shedding it would dangle the pointer")
	}
}

// --- summarizing rebuild ---------------------------------------------------

func TestBuildSummarizedEvents_KeepsUserMessagesDropsTools(t *testing.T) {
	body := strings.Repeat("q", 4000)
	events := []*session.Event{
		userEvent("first request"),
		toolResultEvent("read", "/a.go", body),
		userEvent("second request"),
		toolResultEvent("read", "/b.go", body),
	}

	out := BuildSummarizedEvents(events, "did some work", 20000)

	var texts []string
	for _, ev := range out {
		for _, p := range ev.Content.Parts {
			if p.FunctionResponse != nil {
				t.Fatal("tool results must not survive a summarizing rebuild")
			}
			if p.Text != "" {
				texts = append(texts, p.Text)
			}
		}
	}
	joined := strings.Join(texts, "\n")
	for _, want := range []string{"first request", "second request", "did some work"} {
		if !strings.Contains(joined, want) {
			t.Errorf("rebuilt events missing %q; got:\n%s", want, joined)
		}
	}
	if !strings.Contains(joined, SummaryPrefix) {
		t.Error("summary must carry the prefix that tells the model where it came from")
	}
}

func TestBuildSummarizedEvents_UserMessageBudgetKeepsNewest(t *testing.T) {
	// Each message costs ~250 tokens; a 300-token budget admits exactly one,
	// and it must be the newest.
	big := strings.Repeat("w", 1000)
	events := []*session.Event{
		userEvent("oldest " + big),
		userEvent("newest " + big),
	}

	out := BuildSummarizedEvents(events, "summary", 300)

	var joined string
	for _, ev := range out {
		for _, p := range ev.Content.Parts {
			joined += p.Text
		}
	}
	if !strings.Contains(joined, "newest") {
		t.Error("the newest user message must be kept when the budget is tight")
	}
	if strings.Contains(joined, "oldest") {
		t.Error("the oldest user message should have been dropped by the budget")
	}
}

func TestBuildSummarizedEvents_ChronologicalOrder(t *testing.T) {
	events := []*session.Event{
		userEvent("alpha"), userEvent("beta"), userEvent("gamma"),
	}
	out := BuildSummarizedEvents(events, "s", 20000)

	var order []string
	for _, ev := range out {
		for _, p := range ev.Content.Parts {
			if strings.HasPrefix(p.Text, "alpha") || strings.HasPrefix(p.Text, "beta") || strings.HasPrefix(p.Text, "gamma") {
				order = append(order, p.Text)
			}
		}
	}
	want := []string{"alpha", "beta", "gamma"}
	if len(order) != len(want) {
		t.Fatalf("kept %d user messages, want %d", len(order), len(want))
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order = %v, want %v", order, want)
		}
	}
}

func TestBuildSummarizedEvents_EmptySummaryIsMarked(t *testing.T) {
	out := BuildSummarizedEvents([]*session.Event{userEvent("hi")}, "   ", 20000)
	last := out[len(out)-1]
	if !strings.Contains(last.Content.Parts[0].Text, "(no summary available)") {
		t.Error("an empty summary must be marked, not silently rendered as blank")
	}
}

func TestIsUserMessage(t *testing.T) {
	if !isUserMessage(userEvent("hi")) {
		t.Error("plain user text should be a user message")
	}
	if isUserMessage(toolResultEvent("read", "/a.go", "body")) {
		t.Error("a tool result is not a user message")
	}
	modelEv := &session.Event{Author: "pi"}
	modelEv.Content = genai.NewContentFromText("hi", genai.RoleModel)
	if isUserMessage(modelEv) {
		t.Error("a model turn is not a user message")
	}
	if isUserMessage(nil) {
		t.Error("nil must not be a user message")
	}
}

func TestDegradedSummary_StatesLossAndListsFiles(t *testing.T) {
	ev := &session.Event{Author: "pi"}
	ev.Content = &genai.Content{Parts: []*genai.Part{{
		FunctionCall: &genai.FunctionCall{Name: "read", Args: map[string]any{"file_path": "/x/y.go"}},
	}}}

	got := degradedSummary([]*session.Event{ev}, nil)
	if !strings.Contains(got, "lost") {
		t.Error("a degraded summary must say plainly that detail was lost")
	}
	if !strings.Contains(got, "/x/y.go") {
		t.Error("a degraded summary should still leave a trail of files touched")
	}
}
