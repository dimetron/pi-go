package tui

import (
	"strings"
	"testing"

	"github.com/dimetron/pi-go/internal/extension"
)

func TestSlashCommand_CustomCommandExpandsArguments(t *testing.T) {
	m := newTestModel(t)
	m.cfg.Commands = []extension.Command{{
		Name: "review",
		Body: "Review $1.\n\nAll: $ARGUMENTS",
	}}

	m.handleSlashCommand("/review main.go extra")

	var user string
	for _, msg := range m.chatModel.Messages {
		if msg.role == "user" {
			user = msg.content
		}
	}
	if user != "Review main.go.\n\nAll: main.go extra" {
		t.Fatalf("user message = %q", user)
	}
	if !m.running {
		t.Fatal("expected the expanded prompt to start a turn")
	}
}

func TestSlashCommand_BuiltinBeatsCustomCommand(t *testing.T) {
	m := newTestModel(t)
	m.cfg.Commands = []extension.Command{{
		Name: "help",
		Body: "this must not run",
	}}

	m.handleSlashCommand("/help")

	last := m.chatModel.Messages[len(m.chatModel.Messages)-1]
	if strings.Contains(last.content, "this must not run") {
		t.Fatalf("built-in /help ran the custom command: %q", last.content)
	}
	if !strings.Contains(last.content, "Commands") {
		t.Fatalf("expected built-in help, got %q", last.content)
	}
}

func TestSlashCommand_CustomCommandBeatsSkill(t *testing.T) {
	m := newTestModel(t)
	m.cfg.Commands = []extension.Command{{
		Name: "review",
		Body: "Command body for $ARGUMENTS",
	}}
	m.cfg.Skills = []extension.Skill{{
		Name:        "review",
		Instruction: "skill instruction",
	}}

	m.handleSlashCommand("/review the diff")

	var user string
	for _, msg := range m.chatModel.Messages {
		if msg.role == "user" {
			user = msg.content
		}
		if msg.role == "tool" && msg.tool == "skill" {
			t.Fatal("skill activation ran instead of the custom command")
		}
	}
	if user != "Command body for the diff" {
		t.Fatalf("user message = %q", user)
	}
}

func TestSlashCommand_EmptyCustomCommand(t *testing.T) {
	m := newTestModel(t)
	m.cfg.Commands = []extension.Command{{Name: "blank", Body: "   "}}

	m.handleSlashCommand("/blank")

	if m.running {
		t.Fatal("empty command should not start a turn")
	}
	last := m.chatModel.Messages[len(m.chatModel.Messages)-1]
	if !strings.Contains(last.content, "empty prompt") {
		t.Fatalf("message = %q", last.content)
	}
}

func TestFormatHelp_IncludesCustomCommand(t *testing.T) {
	m := newTestModel(t)
	m.cfg.Commands = []extension.Command{
		{Name: "review", Description: "Review a change", ArgumentHint: "[path]"},
		{Name: "help", Description: "shadowed by the built-in"},
	}
	m.cfg.Skills = []extension.Skill{{Name: "review", Description: "skill should hide"}}

	help := m.formatHelp()
	if !strings.Contains(help, "/review [path]") || !strings.Contains(help, "Review a change") {
		t.Fatalf("help missing custom command: %q", help)
	}
	if strings.Contains(help, "shadowed by the built-in") {
		t.Fatal("help listed a custom command that a built-in hides")
	}
	if strings.Contains(help, "skill should hide") {
		t.Fatal("help listed a skill overridden by a custom command")
	}
}

func TestComplete_CustomCommand(t *testing.T) {
	commands := []extension.Command{
		{Name: "review", Description: "Review a change"},
		{Name: "help", Description: "hidden"},
	}
	result := Complete("/rev", nil, "", commands)
	if len(result.Candidates) != 1 || result.Candidates[0].Text != "/review" {
		t.Fatalf("candidates = %+v", result.Candidates)
	}

	// /help stays the single built-in match; the custom command is not a second hit.
	help := Complete("/help", nil, "", commands)
	if len(help.Candidates) != 1 || help.Candidates[0].Text != "/help" {
		t.Fatalf("/help candidates = %+v", help.Candidates)
	}
	if help.Candidates[0].Description == "hidden" {
		t.Fatal("custom /help replaced the built-in description")
	}
}
