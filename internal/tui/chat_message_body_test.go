package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestHangingContentWidth(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		width int
		want  int
	}{
		// The marker ("> ", "💭 ", "✖ ") costs three cells on the first line and
		// the continuation lines re-indent under it, so the wrap width is three
		// less than the pane.
		{name: "wide pane reserves the marker", width: 120, want: 117},
		{name: "exactly at the floor", width: 23, want: 20},
		{name: "below the floor stays at 20", width: 10, want: 20},
		{name: "zero width stays at 20", width: 0, want: 20},
		{name: "negative width stays at 20", width: -5, want: 20},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := hangingContentWidth(tt.width); got != tt.want {
				t.Errorf("hangingContentWidth(%d) = %d, want %d", tt.width, got, tt.want)
			}
		})
	}
}

func TestRenderMessageBodyByRole(t *testing.T) {
	t.Parallel()

	in := messageRenderInput{palette: darkPalette, bullet: "◉ "}

	tests := []struct {
		name string
		msg  message
		want string // substring of the ANSI-stripped body; "" means no output
	}{
		{name: "user", msg: message{role: "user", content: "hello"}, want: "> hello"},
		{name: "thinking", msg: message{role: "thinking", content: "pondering"}, want: "💭 pondering"},
		{name: "assistant", msg: message{role: "assistant", content: "hi"}, want: "hi"},
		{name: "tool", msg: message{role: "tool", tool: "bash", content: "ok"}, want: "bash"},
		{name: "empty thinking renders nothing", msg: message{role: "thinking"}, want: ""},
		{name: "empty assistant renders nothing", msg: message{role: "assistant"}, want: ""},
		// An unrecognized role must claim no lines at all: the kinds slice is
		// built from the line count, so a stray line would desync the minimap.
		{name: "unknown role renders nothing", msg: message{role: "banana", content: "x"}, want: ""},
		{name: "empty role renders nothing", msg: message{content: "x"}, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := NewChatModel(nil)
			c.Width = 80
			c.ToolDisplay.Width = 80

			got := ansi.Strip(c.renderMessageBody(&tt.msg, in))
			if tt.want == "" {
				if got != "" {
					t.Errorf("renderMessageBody() = %q, want empty", got)
				}
				return
			}
			if !strings.Contains(got, tt.want) {
				t.Errorf("renderMessageBody() = %q, want it to contain %q", got, tt.want)
			}
		})
	}
}

// The streaming placeholder only stands in for an assistant turn that has not
// produced its first token yet.
func TestRenderAssistantMessageStreamingPlaceholder(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		content   string
		streaming bool
		want      string
	}{
		{name: "empty and streaming shows an ellipsis", content: "", streaming: true, want: "..."},
		{name: "empty and idle shows nothing", content: "", streaming: false, want: ""},
		{name: "content wins over the placeholder", content: "real", streaming: true, want: "real"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := NewChatModel(nil)
			c.Width = 80
			msg := message{role: "assistant", content: tt.content}
			got := ansi.Strip(c.renderAssistantMessage(&msg,
				messageRenderInput{palette: darkPalette, bullet: "◉ ", streaming: tt.streaming}))

			if tt.want == "" {
				if got != "" {
					t.Errorf("renderAssistantMessage() = %q, want empty", got)
				}
				return
			}
			if !strings.Contains(got, tt.want) {
				t.Errorf("renderAssistantMessage() = %q, want it to contain %q", got, tt.want)
			}
		})
	}
}

// The style flags are independent booleans on the message, so their precedence
// has to be pinned: error outranks warning outranks tally outranks pre-rendered.
func TestAssistantBodyFlagPrecedence(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		msg  message
		want string
	}{
		{name: "plain", msg: message{}, want: "◉ plain text"},
		{name: "pre-rendered skips markdown", msg: message{preRendered: true}, want: "◉ plain text"},
		{name: "meta", msg: message{isMeta: true}, want: "Σ plain text"},
		{name: "warning", msg: message{isWarning: true}, want: "⚠ plain text"},
		{name: "error", msg: message{isError: true}, want: "✖ plain text"},
		{name: "error beats warning", msg: message{isError: true, isWarning: true}, want: "✖ plain text"},
		{name: "warning beats meta", msg: message{isWarning: true, isMeta: true}, want: "⚠ plain text"},
		{name: "meta beats pre-rendered", msg: message{isMeta: true, preRendered: true}, want: "Σ plain text"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := NewChatModel(nil) // nil renderer: markdown passes through
			c.Width = 80
			got := ansi.Strip(c.assistantBody(&tt.msg, "plain text",
				messageRenderInput{palette: darkPalette, bullet: "◉ "}))
			if got != tt.want {
				t.Errorf("assistantBody() = %q, want %q", got, tt.want)
			}
		})
	}
}

// A provider error arrives as one very long JSON line. It has to wrap to the
// pane and hang-indent under the ✖, or the terminal truncates it and the user
// learns nothing about the failure.
func TestRenderErrorBodyWrapsAndIndents(t *testing.T) {
	t.Parallel()

	content := strings.Repeat("failure detail ", 40)
	got := ansi.Strip(renderErrorBody(content, darkPalette, 60))
	lines := strings.Split(got, "\n")

	if len(lines) < 2 {
		t.Fatalf("expected the error to wrap, got one line: %q", got)
	}
	if !strings.HasPrefix(lines[0], "✖ ") {
		t.Errorf("first line %q does not start with the error marker", lines[0])
	}
	for i, line := range lines[1:] {
		if !strings.HasPrefix(line, "   ") {
			t.Errorf("continuation line %d is not indented under the marker: %q", i+1, line)
		}
		if w := ansi.StringWidth(line); w > hangingContentWidth(60)+3 {
			t.Errorf("continuation line %d is %d cells wide, wider than the pane: %q", i+1, w, line)
		}
	}
}

// Reasoning blocks can run long, so only the tail is shown.
func TestRenderThinkingMessageShowsOnlyTheTail(t *testing.T) {
	t.Parallel()

	c := NewChatModel(nil)
	c.Width = 80

	msg := message{role: "thinking", content: "l1\nl2\nl3\nl4\nl5\nl6\nl7\nl8"}
	got := ansi.Strip(c.renderThinkingMessage(&msg, darkPalette))

	for _, dropped := range []string{"l1", "l2"} {
		if strings.Contains(got, dropped) {
			t.Errorf("expected %q to be trimmed from the head, got:\n%s", dropped, got)
		}
	}
	for _, kept := range []string{"l3", "l4", "l5", "l6", "l7", "l8"} {
		if !strings.Contains(got, kept) {
			t.Errorf("expected %q to survive, got:\n%s", kept, got)
		}
	}
}

// The rule above a user message separates it from what came before, so the
// first message in a transcript must not draw one.
func TestRenderUserMessageSeparator(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		separator string
		wantRule  bool
	}{
		{name: "first message has no rule", separator: "", wantRule: false},
		{name: "later message draws the rule", separator: strings.Repeat("─", 20), wantRule: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := NewChatModel(nil)
			c.Width = 80
			msg := message{role: "user", content: "hello"}
			got := ansi.Strip(c.renderUserMessage(&msg,
				messageRenderInput{palette: darkPalette, separator: tt.separator}))

			if hasRule := strings.Contains(got, "─"); hasRule != tt.wantRule {
				t.Errorf("separator drawn = %v, want %v; got %q", hasRule, tt.wantRule, got)
			}
			if !strings.Contains(got, "> hello") {
				t.Errorf("missing the prompt marker in %q", got)
			}
		})
	}
}
