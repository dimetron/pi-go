package provider

import "strings"

// Ollama normally hands reasoning back out of band, in Message.Thinking. Some
// models routed through it do not get that treatment — the chat template
// leaves <think>...</think> in the ordinary content stream, and the tags then
// reach the user as literal text. That was seen with deepseek-v4-flash:
// "crikey</think>Let me look at the actual session data..." rendered as the
// model's answer.
const (
	thinkOpen  = "<think>"
	thinkClose = "</think>"
)

// thinkSplitter separates inline <think> blocks from answer text across a
// stream of deltas.
//
// It is a no-op on tag-free content, so it can sit unconditionally in the
// streaming path: a model whose reasoning already arrives in Message.Thinking
// simply never trips it.
//
// Two properties matter for streaming. A tag may straddle a chunk boundary, so
// a trailing partial tag is held back in carry rather than emitted as text and
// corrected later. And a close tag with no matching open — the case above,
// where the template emitted the opener before the response began — only has
// its tag dropped; the text before it is not retroactively reclassified as
// reasoning, because it has already been yielded to the caller.
type thinkSplitter struct {
	inThink bool
	carry   string
}

// split consumes one content delta and reports the reasoning and answer text
// it contained. Either return may be empty.
func (s *thinkSplitter) split(delta string) (thinking, text string) {
	buf := s.carry + delta
	s.carry = ""

	var think, out strings.Builder
	for buf != "" {
		if s.inThink {
			i := strings.Index(buf, thinkClose)
			if i < 0 {
				held := danglingTagPrefix(buf)
				think.WriteString(buf[:len(buf)-len(held)])
				s.carry = held
				break
			}
			think.WriteString(buf[:i])
			buf = buf[i+len(thinkClose):]
			s.inThink = false
			continue
		}

		i := strings.Index(buf, thinkOpen)
		if i < 0 {
			// A close tag without an open one: drop the tag, keep the text.
			if j := strings.Index(buf, thinkClose); j >= 0 {
				out.WriteString(buf[:j])
				buf = buf[j+len(thinkClose):]
				continue
			}
			held := danglingTagPrefix(buf)
			out.WriteString(buf[:len(buf)-len(held)])
			s.carry = held
			break
		}
		out.WriteString(buf[:i])
		buf = buf[i+len(thinkOpen):]
		s.inThink = true
	}

	return think.String(), out.String()
}

// flush returns whatever the splitter was holding back when the stream ended,
// classified by the state it ended in. An unterminated <think> block yields its
// remainder as reasoning rather than silently dropping it.
func (s *thinkSplitter) flush() (thinking, text string) {
	held := s.carry
	s.carry = ""
	if s.inThink {
		return held, ""
	}
	return "", held
}

// danglingTagPrefix returns the suffix of buf that could still grow into a
// think tag once the next delta arrives, or "" when none can.
func danglingTagPrefix(buf string) string {
	for _, tag := range [...]string{thinkClose, thinkOpen} {
		// The longest candidate first: "</think" before "<".
		for n := len(tag) - 1; n > 0; n-- {
			if n <= len(buf) && buf[len(buf)-n:] == tag[:n] {
				return buf[len(buf)-n:]
			}
		}
	}
	return ""
}
