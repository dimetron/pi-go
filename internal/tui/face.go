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
	MoodProcessing: "◑ ◑",
	MoodToolCall:   "▸ ◂",
	MoodSpeaking:   "◕ ◡",
	MoodHappy:      "✧ ✧",
	MoodSad:        "◡ ◡",
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
