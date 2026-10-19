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
