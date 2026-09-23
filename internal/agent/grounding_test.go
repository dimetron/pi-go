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
			tool, ok := GeminiGroundingTool(info.Provider, info.Model)
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
		model    string
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
		// The gateway case: the provider name is the same for every upstream,
		// so the model is what decides. A gemini route grounds, and the
		// gateway's other upstreams must not — a grounding tool sent to one of
		// them is dropped silently rather than rejected.
		{name: "agentgateway gemini route", provider: "agentgateway", model: "gemini/gemini-2.5-flash", env: "", isSet: false, wantTool: true},
		{name: "agentgateway bare gemini id", provider: "agentgateway", model: "gemini-2.5-flash", env: "", isSet: false, wantTool: true},
		{name: "agentgateway ollama route", provider: "agentgateway", model: "ollama/qwen3.5:4b-mlx", env: "", isSet: false, wantTool: false},
		{name: "agentgateway openai route", provider: "agentgateway", model: "gpt-5.6-luna", env: "", isSet: false, wantTool: false},
		{name: "agentgateway virtual model", provider: "agentgateway", model: "ollama-deepseek", env: "", isSet: false, wantTool: false},
		{name: "agentgateway gemini route but grounded off", provider: "agentgateway", model: "gemini/gemini-2.5-flash", env: "1", isSet: true, wantTool: false},
		{name: "agentgateway with empty model", provider: "agentgateway", model: "", env: "", isSet: false, wantTool: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.isSet {
				t.Setenv(groundingEnvVar, tt.env)
			} else {
				t.Setenv(groundingEnvVar, "")
			}

			tool, ok := GeminiGroundingTool(tt.provider, tt.model)
			if tt.wantTool {
				if !ok {
					t.Fatalf("GeminiGroundingTool(%q, %q) returned ok == false, want true", tt.provider, tt.model)
				}
				if tool == nil {
					t.Fatal("GeminiGroundingTool() returned nil tool, want non-nil")
				}
				if tool.Name() != "google_search" {
					t.Errorf("tool.Name() = %q, want 'google_search'", tool.Name())
				}
			} else {
				if ok {
					t.Errorf("GeminiGroundingTool(%q, %q) returned ok == true, want false", tt.provider, tt.model)
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

// TestGatewayRouteSpeaksGeminiAgreesWithProvider pins the two copies of the
// gateway route-to-protocol rule together.
//
// GatewayRouteSpeaksGemini is a deliberate second implementation of
// internal/provider.GatewayRoutesToGemini: piagent imports this package and
// TestPiagentStaysIsolated forbids internal/provider in its transitive graph,
// so the predicate cannot be imported and has to be restated. Duplication is
// only safe when something fails if the two drift, and this is that something.
//
// It matters because the two answers are consumed for different purposes that
// must agree: provider picks the native client (get this wrong and the request
// 400s, loudly), while this decides whether to register a server-side search
// (get this wrong and the tool is silently dropped in conversion, so the model
// answers from its own priors — a wrong answer that looks like a right one).
// They are also reached through different flags: `--model
// agentgateway/gemini/gemini-2.5-flash` strips the first segment before either
// sees it, so both must handle the "gemini/..." form.
func TestGatewayRouteSpeaksGeminiAgreesWithProvider(t *testing.T) {
	// A table wide enough to include the shapes each helper has to reject, not
	// just the ones it accepts — a predicate that returns true for everything
	// would agree on the positive cases alone.
	models := []string{
		"gemini/gemini-2.5-flash",
		"gemini-2.5-flash",
		"  GEMINI-2.5-Flash  ",
		"gemini-3.5-pro",
		"gemini/",
		"gemini-",
		// Discriminators for the exact shape of the rule. These start with
		// "gemini" but match neither "gemini/" nor "gemini-", so a rule that
		// widened to a bare "gemini" prefix — the tempting "simplification" —
		// answers differently for these and only these. Without them the table
		// agrees even when the two rules have already diverged.
		"gemini3-pro",
		"geminiflash",
		"ollama/qwen3.5:4b-mlx",
		"gpt-5.6-luna",
		"claude-opus-5",
		"ollama-deepseek",
		"pi-fast",
		"",
	}

	for _, model := range models {
		t.Run(model, func(t *testing.T) {
			mine := GatewayRouteSpeaksGemini(model)
			theirs := provider.GatewayRoutesToGemini(model)
			if mine != theirs {
				t.Errorf("GatewayRouteSpeaksGemini(%q) = %v but provider.GatewayRoutesToGemini(%q) = %v — "+
					"the routing rule and the grounding rule have drifted apart; update both",
					model, mine, model, theirs)
			}
		})
	}
}

// TestGroundsViaGeminiIgnoresModelForDirectProvider pins that the direct gemini
// provider grounds regardless of the model name, so a caller that has only the
// provider (the eval inventory) keeps working, while the gateway case is
// strictly model-gated.
func TestGroundsViaGeminiIgnoresModelForDirectProvider(t *testing.T) {
	if !GroundsViaGemini("gemini", "") {
		t.Error(`GroundsViaGemini("gemini", "") = false; the direct provider must not need a model name`)
	}
	if !GroundsViaGemini("gemini", "gemini-2.5-flash") {
		t.Error(`GroundsViaGemini("gemini", "gemini-2.5-flash") = false, want true`)
	}
	// The whole point of the gateway branch: same provider, and the model
	// decides. An empty model must not be read as "ground it anyway".
	if GroundsViaGemini("agentgateway", "") {
		t.Error(`GroundsViaGemini("agentgateway", "") = true; with no model there is no evidence ` +
			`the route reaches Gemini, so grounding must stay off`)
	}
	if !GroundsViaGemini("agentgateway", "gemini/gemini-2.5-flash") {
		t.Error(`GroundsViaGemini("agentgateway", "gemini/gemini-2.5-flash") = false, want true`)
	}
	if GroundsViaGemini("agentgateway", "ollama/qwen3.5:4b-mlx") {
		t.Error(`GroundsViaGemini("agentgateway", "ollama/...") = true; an ollama route must not ground`)
	}
}
