package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func typeRunes(im *InputModel, s string) {
	for _, r := range s {
		im.HandleKey(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

func TestInput_NewlineKeysInsertLineBreak(t *testing.T) {
	keys := map[string]tea.KeyPressMsg{
		"shift+enter": {Code: tea.KeyEnter, Mod: tea.ModShift},
		"alt+enter":   {Code: tea.KeyEnter, Mod: tea.ModAlt},
		"ctrl+j":      {Code: 'j', Mod: tea.ModCtrl},
	}
	for name, k := range keys {
		t.Run(name, func(t *testing.T) {
			im := NewInputModel(nil, nil, nil, "")
			typeRunes(&im, "ab")
			if cmd := im.HandleKey(k); cmd != nil {
				t.Fatalf("newline key must not submit")
			}
			typeRunes(&im, "cd")
			if im.Text != "ab\ncd" {
				t.Fatalf("Text = %q, want %q", im.Text, "ab\ncd")
			}
			if im.CursorPos != 5 {
				t.Fatalf("CursorPos = %d, want 5", im.CursorPos)
			}
		})
	}
}

func TestInput_EnterSubmitsWholeMultilineText(t *testing.T) {
	im := NewInputModel(nil, nil, nil, "")
	typeRunes(&im, "one")
	im.HandleKey(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModShift})
	typeRunes(&im, "two")
	cmd := im.HandleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Enter should submit")
	}
	msg, ok := cmd().(InputSubmitMsg)
	if !ok || msg.Text != "one\ntwo" {
		t.Fatalf("submit = %#v, want text %q", msg, "one\ntwo")
	}
	if im.Text != "" {
		t.Fatalf("input not cleared: %q", im.Text)
	}
}

func TestInput_PasteKeepsNewlinesAndGrows(t *testing.T) {
	im := NewInputModel(nil, nil, nil, "")
	im.SetWidth(40)
	im.InsertText("a\nb\nc")
	if im.Text != "a\nb\nc" || im.CursorPos != 5 {
		t.Fatalf("Text=%q CursorPos=%d", im.Text, im.CursorPos)
	}
	if rows := strings.Count(im.View(false), "\n") + 1; rows != 3 {
		t.Fatalf("view rows = %d, want 3", rows)
	}
}

func TestInput_CursorPosAddressesAcrossLines(t *testing.T) {
	im := NewInputModel(nil, nil, nil, "")
	im.SetText("ab\ncd")
	im.CursorPos = 4 // between c and d
	im.ensureInput()
	typeRunes(&im, "X")
	if im.Text != "ab\ncXd" {
		t.Fatalf("Text = %q", im.Text)
	}
}

func TestInput_RowEdgesForHistoryRouting(t *testing.T) {
	im := NewInputModel(nil, nil, nil, "")
	im.SetWidth(40)
	if !im.AtFirstRow() || !im.AtLastRow() {
		t.Fatal("single-line input is both first and last row")
	}
	im.SetText("a\nb")
	if im.AtFirstRow() || !im.AtLastRow() {
		t.Fatal("cursor at end of two lines: last row only")
	}
	im.HandleKey(tea.KeyPressMsg{Code: tea.KeyUp})
	if !im.AtFirstRow() || im.AtLastRow() {
		t.Fatal("after Up: first row only")
	}
}
