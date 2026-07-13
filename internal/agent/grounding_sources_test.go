package agent

import (
	"strings"
	"testing"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

// A chunk whose Web block carries no Title and no Domain falls back to the
// URI's host, and — when the URI has no host to parse — to the raw URI itself.
func TestGroundingSourceLabelFallbacks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		web  *genai.GroundingChunkWeb
		want string
	}{
		{
			name: "title wins",
			web:  &genai.GroundingChunkWeb{Title: "wikipedia.org", Domain: "d", URI: "https://x.test/a"},
			want: "wikipedia.org",
		},
		{
			name: "domain when title empty",
			web:  &genai.GroundingChunkWeb{Domain: "example.com", URI: "https://x.test/a"},
			want: "example.com",
		},
		{
			name: "uri host when title and domain empty",
			web:  &genai.GroundingChunkWeb{URI: "https://host.test/path?q=1"},
			want: "host.test",
		},
		{
			name: "raw uri when it has no host",
			web:  &genai.GroundingChunkWeb{URI: "not-a-url"},
			want: "not-a-url",
		},
		{
			name: "empty web yields empty label",
			web:  &genai.GroundingChunkWeb{},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := groundingSourceLabel(tt.web); got != tt.want {
				t.Errorf("groundingSourceLabel() = %q, want %q", got, tt.want)
			}
		})
	}
}

// Chunks that produce no label at all are skipped, but they are still real
// sources — so rather than claiming none were returned, the summary reports the
// count.
func TestGroundingSummaryCountsUnlabelableSources(t *testing.T) {
	t.Parallel()

	gm := &genai.GroundingMetadata{
		GroundingChunks: []*genai.GroundingChunk{
			{Web: &genai.GroundingChunkWeb{}},
			{Web: &genai.GroundingChunkWeb{}},
		},
	}
	if got, want := GroundingSummary(gm), "2 source(s)"; got != want {
		t.Errorf("GroundingSummary() = %q, want %q", got, want)
	}
}

func TestGroundingSources(t *testing.T) {
	t.Parallel()

	t.Run("nil metadata", func(t *testing.T) {
		t.Parallel()
		if got := GroundingSources(nil); got != "" {
			t.Errorf("GroundingSources(nil) = %q, want empty", got)
		}
	})

	// Unlike GroundingSummary, this form keeps the redirect URIs: it feeds the
	// trace log, where width does not matter.
	t.Run("label and uri per line", func(t *testing.T) {
		t.Parallel()
		gm := &genai.GroundingMetadata{
			GroundingChunks: []*genai.GroundingChunk{
				{Web: &genai.GroundingChunkWeb{Title: "a.test", URI: "https://redirect/1"}},
				{Web: &genai.GroundingChunkWeb{Title: "b.test", URI: "https://redirect/2"}},
			},
		}
		want := "a.test — https://redirect/1\nb.test — https://redirect/2"
		if got := GroundingSources(gm); got != want {
			t.Errorf("GroundingSources() = %q, want %q", got, want)
		}
	})

	// A chunk with no URI still names its source; nil chunks and chunks without
	// a Web block contribute nothing rather than a blank line.
	t.Run("skips nil chunks and uriless chunks keep their label", func(t *testing.T) {
		t.Parallel()
		gm := &genai.GroundingMetadata{
			GroundingChunks: []*genai.GroundingChunk{
				nil,
				{Web: nil},
				{Web: &genai.GroundingChunkWeb{Title: "no-uri.test"}},
			},
		}
		if got, want := GroundingSources(gm), "no-uri.test"; got != want {
			t.Errorf("GroundingSources() = %q, want %q", got, want)
		}
	})
}

// The whole point of groundingTool: it must set
// IncludeServerSideToolInvocations even when the request arrives with no Config
// at all, since without that flag Gemini rejects a request that mixes the
// built-in search with function declarations.
func TestGroundingToolProcessRequestBuildsMissingConfig(t *testing.T) {
	t.Parallel()

	req := &model.LLMRequest{} // no Config, no ToolConfig
	if err := (groundingTool{}).ProcessRequest(nil, req); err != nil {
		t.Fatalf("ProcessRequest: %v", err)
	}

	if req.Config == nil {
		t.Fatal("ProcessRequest left Config nil")
	}
	if req.Config.ToolConfig == nil {
		t.Fatal("ProcessRequest left ToolConfig nil")
	}
	got := req.Config.ToolConfig.IncludeServerSideToolInvocations
	if got == nil || !*got {
		t.Error("IncludeServerSideToolInvocations not set to true")
	}
	// The built-in search itself must still have been added.
	if len(req.Config.Tools) == 0 {
		t.Error("ProcessRequest added no tools")
	}
}

// GroundingQuery joins the queries a single search ran; the key form is what
// callers dedupe on, since GroundingMetadata repeats on every streamed chunk.
func TestGroundingQueryAndKeyAgreeOnMultipleQueries(t *testing.T) {
	t.Parallel()

	gm := &genai.GroundingMetadata{WebSearchQueries: []string{"go generics", "go 1.26"}}

	if got, want := GroundingQuery(gm), "go generics, go 1.26"; got != want {
		t.Errorf("GroundingQuery() = %q, want %q", got, want)
	}
	key := GroundingQueryKey(gm.WebSearchQueries)
	if !strings.Contains(key, "\x00") {
		t.Errorf("GroundingQueryKey() = %q, want NUL-joined", key)
	}
	if key == GroundingQueryKey([]string{"go generics go 1.26"}) {
		t.Error("key must not collide with a single query of the joined text")
	}
}
