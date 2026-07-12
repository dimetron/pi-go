package agent

import (
	"testing"

	"google.golang.org/adk/v2/tool/geminitool"

	"github.com/dimetron/pi-go/internal/provider"
)

// GeminiGroundingTool gates on the provider name, but users select a model
// (`--model gemini-3.5-flash`). Nothing tied the two together, so a model added
// to the provider table under the wrong provider would silently lose grounding
// with every unit test still green. Pin the models we ship end to end: model
// flag → resolved provider → grounding enabled.
//
// Verified live against the Gemini API for both models: each run produced
// GroundingMetadata with real webSearchQueries, confirming Google Search
// actually executed and that the built-in search tool coexists with pi's
// custom function tools.
func TestGroundingEnabledForShippedGeminiModels(t *testing.T) {
	t.Setenv(groundingEnvVar, "")

	for _, model := range []string{
		"gemini-3.5-flash",
		"gemini-2.5-flash",
		"gemini-3.5-pro",
		"gemini-2.5-pro",
	} {
		t.Run(model, func(t *testing.T) {
			info, err := provider.Resolve(model)
			if err != nil {
				t.Fatalf("Resolve(%q): %v", model, err)
			}
			if info.Provider != "gemini" {
				t.Fatalf("Resolve(%q).Provider = %q, want %q — grounding gates on this",
					model, info.Provider, "gemini")
			}
			tool, ok := GeminiGroundingTool(info.Provider)
			if !ok {
				t.Fatalf("grounding disabled for %q (provider %q), want enabled", model, info.Provider)
			}
			// Must be our wrapper, not the bare geminitool.GoogleSearch: the
			// wrapper is what sets include_server_side_tool_invocations, without
			// which Gemini 400s as soon as pi's function tools are also present.
			if _, isGrounding := tool.(groundingTool); !isGrounding {
				t.Fatalf("grounding tool for %q = %T, want agent.groundingTool", model, tool)
			}
			if tool.Name() != GroundingToolName {
				t.Errorf("grounding tool name = %q, want %q", tool.Name(), GroundingToolName)
			}
		})
	}
}

func TestGroundingDisabled(t *testing.T) {
	tests := []struct {
		name  string
		env   string
		isSet bool
		want  bool
	}{
		{name: "unset", env: "", isSet: false, want: false},
		{name: "empty", env: "", isSet: true, want: false},
		{name: "truthy 1", env: "1", isSet: true, want: true},
		{name: "truthy true", env: "true", isSet: true, want: true},
		{name: "truthy TRUE", env: "TRUE", isSet: true, want: true},
		{name: "truthy True", env: "True", isSet: true, want: true},
		{name: "truthy yes", env: "yes", isSet: true, want: true},
		{name: "truthy YES", env: "YES", isSet: true, want: true},
		{name: "truthy on", env: "on", isSet: true, want: true},
		{name: "truthy On", env: "On", isSet: true, want: true},
		{name: "truthy space tolerance", env: " 1 ", isSet: true, want: true},
		{name: "non-truthy 0", env: "0", isSet: true, want: false},
		{name: "non-truthy false", env: "false", isSet: true, want: false},
		{name: "non-truthy no", env: "no", isSet: true, want: false},
		{name: "non-truthy off", env: "off", isSet: true, want: false},
		{name: "non-truthy garbage", env: "garbage", isSet: true, want: false},
		{name: "non-truthy 2", env: "2", isSet: true, want: false},
		{name: "non-truthy enabled", env: "enabled", isSet: true, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.isSet {
				t.Setenv(groundingEnvVar, tt.env)
			} else {
				// Clean environment just in case
				t.Setenv(groundingEnvVar, "")
			}
			got := groundingDisabled()
			if got != tt.want {
				t.Errorf("groundingDisabled() = %v, want %v (env = %q, isSet = %v)", got, tt.want, tt.env, tt.isSet)
			}
		})
	}
}

func TestGeminiGroundingTool(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		env      string
		isSet    bool
		wantTool bool
	}{
		{name: "gemini env unset", provider: "gemini", env: "", isSet: false, wantTool: true},
		{name: "gemini env 1", provider: "gemini", env: "1", isSet: true, wantTool: false},
		{name: "gemini env true", provider: "gemini", env: "true", isSet: true, wantTool: false},
		{name: "gemini env 0", provider: "gemini", env: "0", isSet: true, wantTool: true},
		{name: "anthropic env unset", provider: "anthropic", env: "", isSet: false, wantTool: false},
		{name: "openai env unset", provider: "openai", env: "", isSet: false, wantTool: false},
		{name: "ollama env unset", provider: "ollama", env: "", isSet: false, wantTool: false},
		{name: "empty provider env unset", provider: "", env: "", isSet: false, wantTool: false},
		{name: "anthropic env 1", provider: "anthropic", env: "1", isSet: true, wantTool: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.isSet {
				t.Setenv(groundingEnvVar, tt.env)
			} else {
				t.Setenv(groundingEnvVar, "")
			}

			tool, ok := GeminiGroundingTool(tt.provider)
			if tt.wantTool {
				if !ok {
					t.Fatalf("GeminiGroundingTool(%q) returned ok == false, want true", tt.provider)
				}
				if tool == nil {
					t.Fatal("GeminiGroundingTool() returned nil tool, want non-nil")
				}
				if tool.Name() != "google_search" {
					t.Errorf("tool.Name() = %q, want 'google_search'", tool.Name())
				}
			} else {
				if ok {
					t.Errorf("GeminiGroundingTool(%q) returned ok == true, want false", tt.provider)
				}
				if tool != nil {
					t.Errorf("GeminiGroundingTool() returned non-nil tool %+v, want nil", tool)
				}
			}
		})
	}
}

func TestGeminiGroundingTool_NamesADKInterface(t *testing.T) {
	name := (geminitool.GoogleSearch{}).Name()
	if name != "google_search" {
		t.Errorf("GoogleSearch{}.Name() = %q, want 'google_search'", name)
	}
}
