package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestHyperlinkURLs(t *testing.T) {
	const codecov = "https://app.codecov.io/gh/dimetron/pi-go/pull/96"
	got := hyperlinkURLs("codecov/project: " + codecov + ".")
	want := "codecov/project: " + osc8Open + codecov + "\x1b\\" + codecov + osc8Close + "."
	if got != want {
		t.Fatalf("hyperlinkURLs() = %q, want %q", got, want)
	}
	if plain := ansi.Strip(got); plain != "codecov/project: "+codecov+"." {
		t.Fatalf("stripped hyperlink = %q", plain)
	}
}

func TestHyperlinkURLsLeavesInvalidAndNonHTTPURLsAlone(t *testing.T) {
	input := "file:///tmp/report http:// missing https://"
	if got := hyperlinkURLs(input); got != input {
		t.Fatalf("hyperlinkURLs() = %q, want %q", got, input)
	}
}

func TestRenderMarkdownEmitsTerminalHyperlink(t *testing.T) {
	c := NewChatModel(nil)
	c.UpdateRenderer(80)
	const link = "https://app.codecov.io/gh/dimetron/pi-go/pull/96"
	got := c.RenderMarkdown("codecov/project: " + link)
	if !strings.Contains(got, osc8Open+link+"\x1b\\") || !strings.Contains(got, osc8Close) {
		t.Fatalf("RenderMarkdown() did not hyperlink URL: %q", got)
	}
}

func TestHyperlinkRenderedURLsPreservesStyledMultilineText(t *testing.T) {
	first := "https://first.example/path"
	second := "https://second.example/path"
	styled := "before " + first + ".\nafter " + second
	styled = "\x1b[31m" + styled + "\x1b[0m"

	got := hyperlinkRenderedURLs(styled)
	if plain := ansi.Strip(got); plain != "before "+first+".\nafter "+second {
		t.Fatalf("stripped hyperlinks = %q", plain)
	}
	if strings.Count(got, osc8Open+"https://") != 2 || strings.Count(got, osc8Close) != 2 {
		t.Fatalf("link envelopes = %q, want two", got)
	}
	if strings.Contains(got, osc8Open+first+".\x1b\\") {
		t.Fatalf("punctuation was included in hyperlink target: %q", got)
	}
}
