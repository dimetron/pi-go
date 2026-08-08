package provider

import "testing"

// feed runs a whole delta sequence through one splitter and returns the
// concatenated reasoning and answer text, flush included.
func feed(deltas ...string) (thinking, text string) {
	var s thinkSplitter
	for _, d := range deltas {
		th, tx := s.split(d)
		thinking += th
		text += tx
	}
	th, tx := s.flush()
	return thinking + th, text + tx
}

func TestThinkSplitter(t *testing.T) {
	tests := []struct {
		name     string
		deltas   []string
		thinking string
		text     string
	}{
		{
			name:   "tag-free content passes through untouched",
			deltas: []string{"Let me look at ", "the session data."},
			text:   "Let me look at the session data.",
		},
		{
			name:     "complete block in one delta",
			deltas:   []string{"<think>weighing options</think>The answer is 42."},
			thinking: "weighing options",
			text:     "The answer is 42.",
		},
		{
			name:     "block spanning many deltas",
			deltas:   []string{"<think>step one ", "step two", "</think>", "Done."},
			thinking: "step one step two",
			text:     "Done.",
		},
		{
			name:     "open tag straddling a chunk boundary",
			deltas:   []string{"prefix <", "think>hidden</think> suffix"},
			thinking: "hidden",
			text:     "prefix  suffix",
		},
		{
			name:     "close tag straddling a chunk boundary",
			deltas:   []string{"<think>hidden</", "think>visible"},
			thinking: "hidden",
			text:     "visible",
		},
		{
			name:     "close tag split one byte at a time",
			deltas:   []string{"<think>a", "<", "/", "t", "h", "i", "n", "k", ">", "b"},
			thinking: "a",
			text:     "b",
		},
		{
			// deepseek-v4-flash emitted this: the opener never arrived, so the
			// text before the stray closer has already been streamed as the
			// answer and only the tag itself can be removed.
			name:   "orphan close tag drops the tag but keeps the text",
			deltas: []string{"crikey</think>Let me look at the session data."},
			text:   "crikeyLet me look at the session data.",
		},
		{
			name:     "unterminated block flushes as reasoning",
			deltas:   []string{"<think>cut off mid-thought"},
			thinking: "cut off mid-thought",
		},
		{
			name:   "lone angle bracket is not held back forever",
			deltas: []string{"a < b"},
			text:   "a < b",
		},
		{
			name:     "two blocks in one stream",
			deltas:   []string{"<think>one</think>mid<think>two</think>end"},
			thinking: "onetwo",
			text:     "midend",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			thinking, text := feed(tc.deltas...)
			if thinking != tc.thinking {
				t.Errorf("thinking = %q, want %q", thinking, tc.thinking)
			}
			if text != tc.text {
				t.Errorf("text = %q, want %q", text, tc.text)
			}
		})
	}
}

// A trailing partial tag must not be emitted as text before the next delta can
// complete it, or the caller sees a "<" flicker that is later contradicted.
func TestThinkSplitterHoldsPartialTag(t *testing.T) {
	var s thinkSplitter
	thinking, text := s.split("answer <thi")
	if thinking != "" {
		t.Errorf("thinking = %q, want empty", thinking)
	}
	if text != "answer " {
		t.Errorf("text = %q, want %q", text, "answer ")
	}

	thinking, text = s.split("nk>secret</think>rest")
	if thinking != "secret" {
		t.Errorf("thinking = %q, want %q", thinking, "secret")
	}
	if text != "rest" {
		t.Errorf("text = %q, want %q", text, "rest")
	}
}
