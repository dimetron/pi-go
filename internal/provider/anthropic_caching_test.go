package provider

import (
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
)

// TestApplyAnthropicCacheControl_ThreeBreakpoints verifies the three
// canonical cache_control breakpoints land on: last tool, last system
// block, and the last cacheable block of the last message.
func TestApplyAnthropicCacheControl_ThreeBreakpoints(t *testing.T) {
	// Tools: two simple tool defs.
	tools := []anthropic.ToolUnionParam{
		{OfTool: &anthropic.ToolParam{
			Name:        "read",
			Description: anthropic.String("read a file"),
			InputSchema: anthropic.ToolInputSchemaParam{Properties: map[string]any{}},
		}},
		{OfTool: &anthropic.ToolParam{
			Name:        "bash",
			Description: anthropic.String("run a command"),
			InputSchema: anthropic.ToolInputSchemaParam{Properties: map[string]any{}},
		}},
	}
	// System: two text blocks; only the LAST one gets the marker.
	system := []anthropic.TextBlockParam{
		{Text: "you are an agent."},
		{Text: "## rules\nbe terse."},
	}
	// Messages: a 3-turn conversation; the marker lands on the last
	// block of the last message.
	messages := []anthropic.MessageParam{
		{Role: anthropic.MessageParamRoleUser, Content: []anthropic.ContentBlockParamUnion{
			anthropic.NewTextBlock("first turn"),
		}},
		{Role: anthropic.MessageParamRoleAssistant, Content: []anthropic.ContentBlockParamUnion{
			anthropic.NewTextBlock("assistant reply"),
		}},
		{Role: anthropic.MessageParamRoleUser, Content: []anthropic.ContentBlockParamUnion{
			anthropic.NewTextBlock("latest user turn"),
		}},
	}
	params := &anthropic.MessageNewParams{
		Tools:    tools,
		System:   system,
		Messages: messages,
	}

	applyAnthropicCacheControl(params)

	// (1) Last tool must carry the marker; earlier tools must not.
	// GetCacheControl returns a non-nil pointer for any block that has a
	// cache_control field; the marker is "set" only when Type is non-empty.
	if cc := tools[1].GetCacheControl(); cc == nil || cc.Type == "" {
		t.Errorf("last tool missing cache_control marker, got %+v", cc)
	}
	if cc := tools[0].GetCacheControl(); cc != nil && cc.Type != "" {
		t.Errorf("first tool must not carry cache_control, got %+v", cc)
	}
	// (2) Last system block carries the marker; earlier ones do not.
	if cc := system[1].CacheControl; cc.Type == "" {
		t.Errorf("last system block missing cache_control marker, got %+v", cc)
	}
	if system[0].CacheControl.Type != "" {
		t.Errorf("first system block must not carry cache_control, got %+v", system[0].CacheControl)
	}
	// (3) Last block of last message carries the marker; nothing else does.
	lastMsg := messages[len(messages)-1]
	if cc := lastMsg.Content[len(lastMsg.Content)-1].GetCacheControl(); cc == nil || cc.Type == "" {
		t.Errorf("last block of last message missing cache_control, got %+v", cc)
	}
	if cc := messages[0].Content[0].GetCacheControl(); cc != nil && cc.Type != "" {
		t.Errorf("earlier messages must not carry cache_control, got %+v", cc)
	}
}

// TestApplyAnthropicCacheControl_SkipsThinkingBlocks verifies that when the
// last message ends in a thinking block (which cannot carry cache_control),
// the helper walks back to the nearest eligible block.
func TestApplyAnthropicCacheControl_SkipsThinkingBlocks(t *testing.T) {
	// Build a message whose last block is a thinking block; the marker
	// should land on the text block BEFORE the thinking block, not on
	// the thinking block itself.
	messages := []anthropic.MessageParam{
		{Role: anthropic.MessageParamRoleAssistant, Content: []anthropic.ContentBlockParamUnion{
			anthropic.NewTextBlock("thinking content"),
			anthropic.NewThinkingBlock("sig-abc", "let me think about this..."),
		}},
	}
	params := &anthropic.MessageNewParams{
		Messages: messages,
	}
	applyAnthropicCacheControl(params)

	lastMsg := messages[0]
	// Thinking block must NOT have cache_control set.
	if cc := lastMsg.Content[1].GetCacheControl(); cc != nil && cc.Type != "" {
		t.Errorf("thinking block must not carry cache_control, got %+v", cc)
	}
	// Text block BEFORE the thinking block SHOULD have the marker.
	if cc := lastMsg.Content[0].GetCacheControl(); cc == nil || cc.Type == "" {
		t.Errorf("text block before thinking should carry cache_control, got %+v", cc)
	}
}

// TestApplyAnthropicCacheControl_EmptySections verifies the helper does
// not panic when Tools, System, or Messages are empty (or any combination).
func TestApplyAnthropicCacheControl_EmptySections(t *testing.T) {
	cases := []struct {
		name   string
		params anthropic.MessageNewParams
	}{
		{"empty", anthropic.MessageNewParams{}},
		{"only tools", anthropic.MessageNewParams{
			Tools: []anthropic.ToolUnionParam{{OfTool: &anthropic.ToolParam{Name: "x", InputSchema: anthropic.ToolInputSchemaParam{Properties: map[string]any{}}}}},
		}},
		{"only system", anthropic.MessageNewParams{
			System: []anthropic.TextBlockParam{{Text: "x"}},
		}},
		{"only messages", anthropic.MessageNewParams{
			Messages: []anthropic.MessageParam{
				{Role: anthropic.MessageParamRoleUser, Content: []anthropic.ContentBlockParamUnion{anthropic.NewTextBlock("hi")}},
			},
		}},
		{"system+tools no messages", anthropic.MessageNewParams{
			Tools:  []anthropic.ToolUnionParam{{OfTool: &anthropic.ToolParam{Name: "x", InputSchema: anthropic.ToolInputSchemaParam{Properties: map[string]any{}}}}},
			System: []anthropic.TextBlockParam{{Text: "x"}},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := tc.params
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("applyAnthropicCacheControl panicked on %s: %v", tc.name, r)
				}
			}()
			applyAnthropicCacheControl(&p)
		})
	}
}

// TestApplyAnthropicCacheControl_Idempotent verifies running the helper
// twice does not double-stamp and does not corrupt earlier markers.
func TestApplyAnthropicCacheControl_Idempotent(t *testing.T) {
	tools := []anthropic.ToolUnionParam{
		{OfTool: &anthropic.ToolParam{Name: "x", InputSchema: anthropic.ToolInputSchemaParam{Properties: map[string]any{}}}},
	}
	params := &anthropic.MessageNewParams{
		Tools: tools,
	}
	applyAnthropicCacheControl(params)
	first := *tools[0].GetCacheControl()
	applyAnthropicCacheControl(params)
	second := *tools[0].GetCacheControl()
	if first.Type != second.Type {
		t.Errorf("marker changed after second apply: first=%v second=%v", first, second)
	}
}

// TestApplyAnthropicCacheControlBeta_MirrorsStandard exercises the beta
// path (advisor tool). The breakpoint strategy must be identical.
func TestApplyAnthropicCacheControlBeta_MirrorsStandard(t *testing.T) {
	tools := []anthropic.BetaToolUnionParam{
		{OfTool: &anthropic.BetaToolParam{Name: "read", InputSchema: anthropic.BetaToolInputSchemaParam{Properties: map[string]any{}}}},
		{OfTool: &anthropic.BetaToolParam{Name: "bash", InputSchema: anthropic.BetaToolInputSchemaParam{Properties: map[string]any{}}}},
	}
	system := []anthropic.BetaTextBlockParam{
		{Text: "you are an agent."},
		{Text: "## rules\nbe terse."},
	}
	messages := []anthropic.BetaMessageParam{
		{Role: anthropic.BetaMessageParamRoleUser, Content: []anthropic.BetaContentBlockParamUnion{anthropic.NewBetaTextBlock("first")}},
		{Role: anthropic.BetaMessageParamRoleAssistant, Content: []anthropic.BetaContentBlockParamUnion{anthropic.NewBetaTextBlock("reply")}},
		{Role: anthropic.BetaMessageParamRoleUser, Content: []anthropic.BetaContentBlockParamUnion{anthropic.NewBetaTextBlock("latest")}},
	}
	params := &anthropic.BetaMessageNewParams{
		Tools:    tools,
		System:   system,
		Messages: messages,
	}
	applyAnthropicCacheControlBeta(params)

	if cc := tools[1].GetCacheControl(); cc == nil || cc.Type == "" {
		t.Errorf("beta: last tool missing marker, got %+v", cc)
	}
	if cc := tools[0].GetCacheControl(); cc != nil && cc.Type != "" {
		t.Errorf("beta: first tool must not carry marker, got %+v", cc)
	}
	if cc := system[1].CacheControl; cc.Type == "" {
		t.Errorf("beta: last system block missing marker, got %+v", cc)
	}
	if cc := system[0].CacheControl.Type; cc != "" {
		t.Errorf("beta: first system block must not carry marker, got %+v", cc)
	}
	last := messages[len(messages)-1]
	if cc := last.Content[len(last.Content)-1].GetCacheControl(); cc == nil || cc.Type == "" {
		t.Errorf("beta: last block of last message missing marker, got %+v", cc)
	}
}

// TestShouldDisablePromptCache covers the shared opt-out helper.
func TestShouldDisablePromptCache(t *testing.T) {
	if shouldDisablePromptCache(nil) {
		t.Error("nil opts must not disable caching")
	}
	if shouldDisablePromptCache(&LLMOptions{}) {
		t.Error("default opts (zero value) must not disable caching")
	}
	if !shouldDisablePromptCache(&LLMOptions{DisablePromptCaching: true}) {
		t.Error("DisablePromptCaching=true must disable caching")
	}
}
