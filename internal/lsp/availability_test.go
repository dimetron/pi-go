package lsp

import (
	"slices"
	"testing"
)

// newManagerWithAvailable builds a Manager with a hand-set availability map,
// so the gate can be tested without depending on what is installed on the host.
func newManagerWithAvailable(available map[string]bool) *Manager {
	return &Manager{
		languages:   map[string]*LanguageConfig{},
		servers:     map[string]*Server{},
		diagnostics: map[string][]Diagnostic{},
		available:   available,
	}
}

func TestAnyAvailable(t *testing.T) {
	tests := []struct {
		name      string
		available map[string]bool
		want      bool
	}{
		{"nil map", nil, false},
		{"empty map", map[string]bool{}, false},
		{"all false", map[string]bool{"go": false, "rust": false}, false},
		{"one true", map[string]bool{"go": true, "rust": false}, true},
		{"all true", map[string]bool{"go": true, "rust": true}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := newManagerWithAvailable(tt.available).AnyAvailable(); got != tt.want {
				t.Fatalf("AnyAvailable() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAvailableLanguages(t *testing.T) {
	tests := []struct {
		name      string
		available map[string]bool
		want      []string
	}{
		{"nil map", nil, []string{}},
		{"skips false entries", map[string]bool{"go": true, "rust": false}, []string{"go"}},
		{"sorted", map[string]bool{"rust": true, "go": true, "python": true}, []string{"go", "python", "rust"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := newManagerWithAvailable(tt.available).AvailableLanguages()
			if !slices.Equal(got, tt.want) {
				t.Fatalf("AvailableLanguages() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestAvailableLanguagesAgreesWithAnyAvailable pins the two accessors together:
// the gate reads AnyAvailable, diagnostics read the list, and they must never
// disagree about whether anything is installed.
func TestAvailableLanguagesAgreesWithAnyAvailable(t *testing.T) {
	for _, available := range []map[string]bool{
		nil,
		{},
		{"go": false},
		{"go": true},
		{"go": true, "rust": false},
	} {
		m := newManagerWithAvailable(available)
		if got, want := m.AnyAvailable(), len(m.AvailableLanguages()) > 0; got != want {
			t.Fatalf("available=%v: AnyAvailable()=%v but AvailableLanguages()=%v",
				available, got, m.AvailableLanguages())
		}
	}
}
