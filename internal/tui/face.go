package tui

import "sync"

// AgentMood represents the current emotional state of the agent.
type AgentMood int

const (
	MoodIdle       AgentMood = iota // Default waiting state
	MoodThinking                    // Processing/reasoning
	MoodProcessing                  // Tool execution
	MoodToolCall                    // About to call a tool
	MoodSpeaking                    // Producing text output
	MoodHappy                       // Task completed successfully
	MoodSad                         // Error or task failed
)

// moodEyes maps each mood to a simple eyes string for the status bar.
var moodEyes = map[AgentMood]string{
	MoodIdle:       "◕ ◕",
	MoodThinking:   "◔ ◕",
	MoodProcessing: "◔ ◔",
	MoodToolCall:   "▸ ◂",
	MoodSpeaking:   "◕ ◡",
	MoodHappy:      "✧ ✧",
	MoodSad:        "◡ ◡",
}

// mascotEars is the mascot's top row. ASCII slashes rather than ╱ ╲ (U+2571,
// U+2572): the box-drawing diagonals are East Asian Ambiguous, so a terminal
// resolving ambiguous width as wide draws this row two cells over budget and
// shoves the whole sidebar — and with it every row of the top section — out of
// alignment. In a monospace grid the ASCII pair is indistinguishable.
const mascotEars = ` /\___/\` + "\n"

// moodMascot maps each mood to a full mascot face (multi-line).
//
// Every glyph here is East Asian Neutral or Narrow, so the face measures the
// same in every terminal regardless of mood. The mascot sits in the sidebar's
// first rows, which is why a mood-dependent width shows up as "the top of the
// frame shifts sometimes": ★ (U+2605) and ◑ (U+25D1) are ambiguous, so the happy
// and processing faces were a cell wider than the rest. See
// TestMascotGlyphsAreWidthSafe.
var moodMascot = map[AgentMood]string{
	MoodIdle: mascotEars +
		"   ( ◕ ◕ )\n" +
		`    / π \`,
	MoodThinking: mascotEars +
		"   ( ◔ ◕ )\n" +
		`    / ~ \`,
	MoodProcessing: mascotEars +
		"   ( ◔ ◔ )\n" +
		`    / ⚙ \`,
	MoodToolCall: mascotEars +
		"   ( ▸ ◂ )\n" +
		`    / ⇢ \`,
	MoodSpeaking: mascotEars +
		"   ( ◕ ◡ )\n" +
		`    / ~ \`,
	MoodHappy: mascotEars +
		"   ( ✧ ✧ )\n" +
		`    / ⋆ \`,
	MoodSad: mascotEars +
		"   ( ◡ ◡ )\n" +
		`    / ∙ \`,
}

// String returns a human-readable name for the mood.
func (m AgentMood) String() string {
	switch m {
	case MoodIdle:
		return "idle"
	case MoodThinking:
		return "thinking"
	case MoodProcessing:
		return "processing"
	case MoodToolCall:
		return "tool_call"
	case MoodSpeaking:
		return "speaking"
	case MoodHappy:
		return "happy"
	case MoodSad:
		return "sad"
	default:
		return "unknown"
	}
}

// Eyes returns the eyes string for this mood.
func (m AgentMood) Eyes() string {
	if e, ok := moodEyes[m]; ok {
		return e
	}
	return moodEyes[MoodIdle]
}

// Mascot returns the full mascot face for this mood.
func (m AgentMood) Mascot() string {
	if f, ok := moodMascot[m]; ok {
		return f
	}
	return moodMascot[MoodIdle]
}

// FaceRenderer tracks the agent's current mood (thread-safe).
type FaceRenderer struct {
	mu   sync.RWMutex
	mood AgentMood
}

// NewFaceRenderer creates a new face renderer with default idle mood.
func NewFaceRenderer() *FaceRenderer {
	return &FaceRenderer{mood: MoodIdle}
}

// SetMood changes the agent's current mood.
func (f *FaceRenderer) SetMood(mood AgentMood) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mood = mood
}

// GetMood returns the current mood.
func (f *FaceRenderer) GetMood() AgentMood {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.mood
}

// Eyes returns the eyes string for the current mood.
func (f *FaceRenderer) Eyes() string {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.mood.Eyes()
}

// Mascot returns the full mascot face for the current mood.
func (f *FaceRenderer) Mascot() string {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.mood.Mascot()
}
