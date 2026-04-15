package tools

import (
	"testing"
)

func TestNewRestartTool(t *testing.T) {
	fn := func() {
	}

	tool, err := NewRestartTool(fn)
	if err != nil {
		t.Fatalf("NewRestartTool() error = %v", err)
	}
	if tool == nil {
		t.Fatal("NewRestartTool() returned nil")
	}
	if tool.Name() != "restart" {
		t.Errorf("Name() = %q, want 'restart'", tool.Name())
	}
}

func TestRestartInput(t *testing.T) {
	input := RestartInput{}
	// Empty struct - no fields to test
	_ = input
}

func TestRestartOutput(t *testing.T) {
	output := RestartOutput{Status: "restarting"}
	if output.Status != "restarting" {
		t.Errorf("Status = %q, want 'restarting'", output.Status)
	}
}

func TestRestartFunc(t *testing.T) {
	fn := RestartFunc(func() {})
	if fn == nil {
		t.Error("RestartFunc should not be nil")
	}
}
