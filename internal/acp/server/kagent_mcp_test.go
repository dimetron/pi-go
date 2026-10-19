package server

import (
	"testing"
)

// The fixture is the shape a live kagent v1alpha3 controller injected into a
// harness workload, reduced to the fields this package reads. It is copied from
// the real KAGENT_CONFIG_JSON rather than invented: the whole point of the
// change is that the compiler — not this repo — owns this schema, so a
// hand-written fixture would encode the guess being tested.
const kagentConfigFixture = `{
  "runtime": {
    "control_plane_url": "http://kagent-controller.kagent.svc:8083",
    "name": "kagent-gh",
    "namespace": "kagent"
  },
  "agent": {
    "instruction": "You are kagent-gh.",
    "http_tools": [
      {
        "params": {
          "url": "https://api.githubcopilot.com/mcp/x/pull_requests/readonly",
          "headers": {"Authorization": "KAGENT_ENV[KAGENT_CREDENTIAL_060B0EA8A4C97BB6]"}
        }
      },
      {
        "params": {
          "url": "https://api.githubcopilot.com/mcp/x/repos/readonly",
          "headers": {"Authorization": "KAGENT_ENV[KAGENT_CREDENTIAL_060B0EA8A4C97BB6]"}
        }
      },
      {
        "params": {
          "url": "http://kagent-tools.kagent:8084/mcp",
          "timeout": "30s"
        },
        "tools": ["shell"]
      }
    ]
  }
}`

func TestKagentMCPToolsetsFromEnv_BuildsOneToolsetPerHTTPTool(t *testing.T) {
	ts := kagentMCPToolsetsFromEnv(func(k string) string {
		if k == kagentConfigEnv {
			return kagentConfigFixture
		}
		return ""
	})
	if len(ts) != 3 {
		t.Fatalf("expected 3 toolsets, got %d", len(ts))
	}
	names := map[string]bool{}
	for _, s := range ts {
		names[s.Name()] = true
	}
	for _, want := range []string{"pull_requests", "repos", "kagent-tools.kagent:8084"} {
		if !names[want] {
			t.Errorf("missing toolset %q, got %v", want, names)
		}
	}
}

func TestKagentMCPToolsetsFromEnv_NoConfigIsNil(t *testing.T) {
	if ts := kagentMCPToolsetsFromEnv(func(string) string { return "" }); ts != nil {
		t.Fatalf("expected nil for unset config, got %d toolsets", len(ts))
	}
}

// A malformed blob must not stop the harness from serving: the agent keeps its
// built-in tools and the turn still runs.
func TestKagentMCPToolsetsFromEnv_MalformedIsNil(t *testing.T) {
	ts := kagentMCPToolsetsFromEnv(func(string) string { return "{not json" })
	if ts != nil {
		t.Fatalf("expected nil for malformed config, got %d toolsets", len(ts))
	}
}

func TestKagentMCPToolsetsFromEnv_NoHTTPToolsIsNil(t *testing.T) {
	ts := kagentMCPToolsetsFromEnv(func(string) string {
		return `{"runtime":{"name":"x"},"agent":{"instruction":"hi"}}`
	})
	if ts != nil {
		t.Fatalf("expected nil when no http_tools, got %d toolsets", len(ts))
	}
}

// An entry with no URL cannot be dialed. It must be skipped rather than
// producing a toolset that fails on every call.
func TestKagentMCPToolsetsFromEnv_SkipsEmptyURL(t *testing.T) {
	ts := kagentMCPToolsetsFromEnv(func(string) string {
		return `{"agent":{"http_tools":[{"params":{"url":""}},{"params":{"url":"https://api.githubcopilot.com/mcp/x/issues/readonly"}}]}}`
	})
	if len(ts) != 1 {
		t.Fatalf("expected 1 toolset, got %d", len(ts))
	}
	if got := ts[0].Name(); got != "issues" {
		t.Errorf("toolset name = %q, want issues", got)
	}
}

func TestKagentServerName(t *testing.T) {
	cases := map[string]string{
		"https://api.githubcopilot.com/mcp/x/pull_requests/readonly": "pull_requests",
		"https://api.githubcopilot.com/mcp/x/repos/readonly":         "repos",
		"http://kagent-tools.kagent:8084/mcp":                        "kagent-tools.kagent:8084",
		"https://api.githubcopilot.com/mcp/":                         "api.githubcopilot.com",
		"https://example.invalid":                                    "example.invalid",
	}
	for in, want := range cases {
		if got := kagentServerName(in); got != want {
			t.Errorf("kagentServerName(%q) = %q, want %q", in, got, want)
		}
	}
}

// The template's systemPrompt is the only way a BYO harness receives the prompt
// a user wrote — the Harness spec carries none and no config file does either.
// Without this the run silently falls back to pi-go's built-in instruction,
// which is exactly the bug this covers.
func TestKagentInstructionFromEnv(t *testing.T) {
	getenv := func(v string) func(string) string {
		return func(k string) string {
			if k == kagentConfigEnv {
				return v
			}
			return ""
		}
	}
	if got := kagentInstructionFromEnv(getenv(kagentConfigFixture)); got != "You are kagent-gh." {
		t.Errorf("instruction = %q, want the compiled template prompt", got)
	}
	if got := kagentInstructionFromEnv(getenv("")); got != "" {
		t.Errorf("unset config should yield \"\", got %q", got)
	}
	if got := kagentInstructionFromEnv(getenv("{not json")); got != "" {
		t.Errorf("malformed config should yield \"\", got %q", got)
	}
	if got := kagentInstructionFromEnv(getenv(`{"agent":{"http_tools":[]}}`)); got != "" {
		t.Errorf("config without an instruction should yield \"\", got %q", got)
	}
}

// An explicit override outranks the template: an operator setting --system or
// PI_SYSTEM must not be overridden by a deployment's AgentTemplate.
func TestInstructionPrecedence(t *testing.T) {
	withTemplate := func(string) string { return kagentConfigFixture }
	noTemplate := func(string) string { return "" }

	// The built-in prompt carries a runtime-environment preamble whose content
	// depends on the process environment, so these cases assert the contract
	// (template used or not) rather than comparing whole strings.
	const templatePrompt = "You are kagent-gh."
	cases := []struct {
		name     string
		rt       RuntimeConfig
		env      func(string) string
		wantTpl  bool
		wantText string
	}{
		{"explicit wins over template", RuntimeConfig{System: "explicit", KagentTools: true}, withTemplate, false, "explicit"},
		{"template used when no explicit", RuntimeConfig{KagentTools: true}, withTemplate, true, ""},
		{"builtin when no template", RuntimeConfig{KagentTools: true}, noTemplate, false, ""},
		// An ACP editor backend is not a kagent harness, so the variable's
		// presence alone must not replace its instruction.
		{"acp backend ignores template", RuntimeConfig{KagentTools: false}, withTemplate, false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveInstruction(tc.rt, tc.env)
			if tc.wantText != "" {
				if got != tc.wantText {
					t.Errorf("got %q, want %q", got, tc.wantText)
				}
				return
			}
			if tc.wantTpl {
				if got != templatePrompt {
					t.Errorf("expected the template prompt, got %q", truncate(got, 80))
				}
				return
			}
			// Built-in fallback: must not be the template prompt, and must be set.
			if got == "" {
				t.Error("expected a non-empty instruction")
			}
			if got == templatePrompt {
				t.Error("template prompt leaked into a context that must not use it")
			}
		})
	}
}

// truncate keeps a failure message readable when the value is a whole prompt.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// A nil getenv means "read the real environment", which is how production calls
// the loaders. Covering it keeps the production call path exercised rather than
// only the injected-function path the other tests use.
func TestKagentLoadersNilGetenvUseProcessEnv(t *testing.T) {
	t.Setenv(kagentConfigEnv, kagentConfigFixture)

	if got := kagentInstructionFromEnv(nil); got != "You are kagent-gh." {
		t.Errorf("instruction via nil getenv = %q, want the compiled prompt", got)
	}
	if got := kagentMCPToolsetsFromEnv(nil); len(got) != 3 {
		t.Errorf("toolsets via nil getenv = %d, want 3", len(got))
	}

	t.Setenv(kagentConfigEnv, "")
	if got := kagentInstructionFromEnv(nil); got != "" {
		t.Errorf("instruction with unset env = %q, want empty", got)
	}
	if got := kagentMCPToolsetsFromEnv(nil); got != nil {
		t.Errorf("toolsets with unset env = %d, want nil", len(got))
	}
}

// A URL whose every path segment is a skip marker has no capability name to
// borrow; the trimmed URL is the honest fallback rather than an empty name.
func TestKagentServerName_AllSegmentsSkipped(t *testing.T) {
	if got := kagentServerName("readonly"); got != "readonly" {
		t.Errorf("kagentServerName(readonly) = %q, want the fallback", got)
	}
	if got := kagentServerName("mcp/"); got != "mcp" {
		t.Errorf("kagentServerName(mcp/) = %q, want mcp", got)
	}
}
