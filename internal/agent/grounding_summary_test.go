package agent

import (
	"strings"
	"testing"

	"google.golang.org/genai"
)

func webChunk(title, uri, domain string) *genai.GroundingChunk {
	return &genai.GroundingChunk{
		Web: &genai.GroundingChunkWeb{Title: title, URI: uri, Domain: domain},
	}
}

func TestGroundingQuery(t *testing.T) {
	if got := GroundingQuery(nil); got != "" {
		t.Errorf("GroundingQuery(nil) = %q, want empty", got)
	}
	gm := &genai.GroundingMetadata{WebSearchQueries: []string{"go version", "go releases"}}
	if got, want := GroundingQuery(gm), "go version, go releases"; got != want {
		t.Errorf("GroundingQuery() = %q, want %q", got, want)
	}
}

// The same search is repeated on every streamed chunk of the response it
// grounds, so the key must be stable for identical query sets and distinct for
// different ones — otherwise the search prints once per chunk.
func TestGroundingQueryKey(t *testing.T) {
	a := GroundingQueryKey([]string{"go version"})
	b := GroundingQueryKey([]string{"go version"})
	c := GroundingQueryKey([]string{"rust version"})
	if a != b {
		t.Errorf("identical query sets produced different keys: %q vs %q", a, b)
	}
	if a == c {
		t.Errorf("different query sets produced the same key: %q", a)
	}
}

func TestGroundingSummary(t *testing.T) {
	tests := []struct {
		name  string
		gm    *genai.GroundingMetadata
		want  string
		multi []string // substrings that must all appear
	}{
		{
			name: "nil metadata",
			gm:   nil,
			want: "",
		},
		{
			name: "grounded but no chunks",
			gm:   &genai.GroundingMetadata{WebSearchQueries: []string{"q"}},
			want: "no sources returned",
		},
		{
			name: "chunk without web metadata falls back to a count",
			gm: &genai.GroundingMetadata{
				WebSearchQueries: []string{"q"},
				GroundingChunks:  []*genai.GroundingChunk{{}, {}},
			},
			want: "2 source(s)",
		},
		{
			name: "label only — the URI is never shown in chat",
			gm: &genai.GroundingMetadata{
				GroundingChunks: []*genai.GroundingChunk{webChunk("Go Downloads", "https://go.dev/dl", "go.dev")},
			},
			want: "Go Downloads",
		},
		{
			name: "missing title falls back to domain",
			gm: &genai.GroundingMetadata{
				GroundingChunks: []*genai.GroundingChunk{webChunk("", "https://go.dev/dl", "go.dev")},
			},
			want: "go.dev",
		},
		{
			name: "no title or domain falls back to the URI host",
			gm: &genai.GroundingMetadata{
				GroundingChunks: []*genai.GroundingChunk{webChunk("", "https://go.dev/dl", "")},
			},
			want: "go.dev",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := GroundingSummary(tt.gm); got != tt.want {
				t.Errorf("GroundingSummary() = %q, want %q", got, tt.want)
			}
		})
	}
}

// Google Search grounding does not return real source URLs — it returns opaque
// ~200-char vertexaisearch redirect links. Printing one in the chat panel tells
// the reader nothing and soft-wraps across several rows, wrecking the layout.
// The chat must show the label only; the log keeps the full URI.
func TestGroundingSummaryOmitsRedirectURIs(t *testing.T) {
	redirect := "https://vertexaisearch.cloud.google.com/grounding-api-redirect/" +
		strings.Repeat("A1b2C3d4", 20)
	gm := &genai.GroundingMetadata{
		WebSearchQueries: []string{"current US President 2026"},
		GroundingChunks:  []*genai.GroundingChunk{webChunk("wikipedia.org", redirect, "")},
	}

	summary := GroundingSummary(gm)
	if summary != "wikipedia.org" {
		t.Errorf("GroundingSummary() = %q, want just the label", summary)
	}
	if strings.Contains(summary, "vertexaisearch") || strings.Contains(summary, "grounding-api-redirect") {
		t.Errorf("redirect URI leaked into the chat summary: %q", summary)
	}

	// ...but the log form keeps it, so the source is still reachable.
	sources := GroundingSources(gm)
	if !strings.Contains(sources, redirect) {
		t.Errorf("GroundingSources() dropped the URI; got %q", sources)
	}
	if !strings.Contains(sources, "wikipedia.org") {
		t.Errorf("GroundingSources() dropped the label; got %q", sources)
	}
}

// Every source is listed — nothing is capped or elided.
func TestGroundingSummaryListsAllSources(t *testing.T) {
	chunks := []*genai.GroundingChunk{
		webChunk("taipeitimes.com", "https://x/1", ""),
		webChunk("theguardian.com", "https://x/2", ""),
		webChunk("bbc.com", "https://x/3", ""),
		webChunk("reuters.com", "https://x/4", ""),
		webChunk("apnews.com", "https://x/5", ""),
		webChunk("npr.org", "https://x/6", ""),
		webChunk("dw.com", "https://x/7", ""),
	}
	got := GroundingSummary(&genai.GroundingMetadata{GroundingChunks: chunks})

	lines := strings.Split(got, "\n")
	if len(lines) != len(chunks) {
		t.Fatalf("got %d lines, want all %d sources:\n%s", len(lines), len(chunks), got)
	}
	if strings.Contains(got, "more source(s)") {
		t.Errorf("sources were elided, want all listed:\n%s", got)
	}
	for _, want := range []string{"taipeitimes.com", "dw.com", "npr.org"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing source %q in:\n%s", want, got)
		}
	}
}

// Gemini cites several distinct pages from the same site, and sets Title to the
// site's domain — so those arrive as identical labels. Printing them verbatim
// produced "kyivindependent.com kyivindependent.com kyivindependent.com". Show
// each site once, with a count, in first-seen order.
func TestGroundingSummaryDedupesRepeatedSites(t *testing.T) {
	got := GroundingSummary(&genai.GroundingMetadata{
		GroundingChunks: []*genai.GroundingChunk{
			webChunk("taipeitimes.com", "https://x/1", ""),
			webChunk("kyivindependent.com", "https://x/2", ""),
			webChunk("kyivindependent.com", "https://x/3", ""),
			webChunk("kyivindependent.com", "https://x/4", ""),
			webChunk("theguardian.com", "https://x/5", ""),
		},
	})

	want := "taipeitimes.com\nkyivindependent.com (3)\ntheguardian.com"
	if got != want {
		t.Errorf("GroundingSummary() =\n%s\n\nwant:\n%s", got, want)
	}
}
