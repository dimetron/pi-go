package tui

import "testing"

func TestFaceRenderer_DefaultMood(t *testing.T) {
	fr := NewFaceRenderer()
	if fr.GetMood() != MoodIdle {
		t.Errorf("expected MoodIdle, got %v", fr.GetMood())
	}
}

func TestFaceRenderer_SetMood(t *testing.T) {
	fr := NewFaceRenderer()

	tests := []struct {
		mood     AgentMood
		wantEyes string
	}{
		{MoodIdle, "◕ ◕"},
		{MoodThinking, "◔ ◕"},
		{MoodProcessing, "◑ ◑"},
		{MoodToolCall, "▸ ◂"},
		{MoodSpeaking, "◕ ◡"},
		{MoodHappy, "✧ ✧"},
		{MoodSad, "◡ ◡"},
	}

	for _, tt := range tests {
		fr.SetMood(tt.mood)
		got := fr.Eyes()
		if got != tt.wantEyes {
			t.Errorf("Eyes() for %v = %q, want %q", tt.mood, got, tt.wantEyes)
		}
	}
}

func TestAgentMood_String(t *testing.T) {
	tests := []struct {
		mood AgentMood
		want string
	}{
		{MoodIdle, "idle"},
		{MoodThinking, "thinking"},
		{MoodProcessing, "processing"},
		{MoodToolCall, "tool_call"},
		{MoodSpeaking, "speaking"},
		{MoodHappy, "happy"},
		{MoodSad, "sad"},
		{AgentMood(999), "unknown"},
	}

	for _, tt := range tests {
		got := tt.mood.String()
		if got != tt.want {
			t.Errorf("String() for %v = %q, want %q", tt.mood, got, tt.want)
		}
	}
}

func TestAgentMood_Eyes(t *testing.T) {
	// Unknown mood falls back to idle eyes
	got := AgentMood(999).Eyes()
	if got != "◕ ◕" {
		t.Errorf("Eyes() for unknown mood = %q, want %q", got, "◕ ◕")
	}
}

func TestFaceRenderer_ThreadSafety(t *testing.T) {
	fr := NewFaceRenderer()
	done := make(chan bool)

	for i := 0; i < 100; i++ {
		go func(n int) {
			mood := AgentMood(n % 7)
			fr.SetMood(mood)
			_ = fr.GetMood()
			_ = fr.Eyes()
			done <- true
		}(i)
	}

	for i := 0; i < 100; i++ {
		<-done
	}
}
