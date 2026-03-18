package subagent

import (
	"os"
	"strings"
)

// DefaultEnvAllowlist is the set of environment variable prefixes and exact names
// that are passed through to subagent processes. Everything else is filtered out
// for security — subagents should not inherit secrets, credentials, or tokens
// unless explicitly allowed.
var DefaultEnvAllowlist = []string{
	// System essentials.
	"HOME",
	"USER",
	"LOGNAME",
	"SHELL",
	"PATH",
	"LANG",
	"LC_", // prefix: LC_ALL, LC_CTYPE, etc.
	"TERM",
	"TMPDIR",
	"XDG_", // prefix: XDG_CONFIG_HOME, etc.
	"EDITOR",
	"VISUAL",

	// Go toolchain.
	"GOPATH",
	"GOROOT",
	"GOBIN",
	"GOPROXY",
	"GONOSUMCHECK",
	"GONOSUMDB",
	"GOPRIVATE",
	"GOFLAGS",
	"CGO_ENABLED",

	// Git.
	"GIT_", // prefix: GIT_DIR, GIT_WORK_TREE, GIT_AUTHOR_*, etc.
	"SSH_AUTH_SOCK",

	// pi-go specific.
	"PI_", // prefix: PI_SUBAGENT_TIMEOUT_MS, PI_SUBAGENT_CONCURRENCY, etc.

	// LLM provider API keys and base URLs — required for subagent pi processes.
	"ANTHROPIC_API_KEY",
	"ANTHROPIC_AUTH_TOKEN",
	"ANTHROPIC_BASE_URL",
	"OPENAI_API_KEY",
	"OPENAI_BASE_URL",
	"GOOGLE_API_KEY",
	"GEMINI_API_KEY",
	"GEMINI_BASE_URL",
	"OLLAMA_HOST", // Ollama server address override.

	// Common dev tools.
	"DOCKER_HOST",
	"KUBECONFIG",
	"NODE_PATH",
	"PYTHONPATH",
}

// FilterEnv returns a filtered copy of the current process environment,
// keeping only variables that match the allowlist. Entries are matched by
// exact name or by prefix (allowlist entries ending with "_" match any
// variable starting with that prefix).
func FilterEnv(allowlist []string) []string {
	if allowlist == nil {
		allowlist = DefaultEnvAllowlist
	}

	env := os.Environ()
	filtered := make([]string, 0, len(env)/2)

	for _, entry := range env {
		name, _, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		if matchesAllowlist(name, allowlist) {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}

// matchesAllowlist checks if an env var name matches any entry in the allowlist.
// Allowlist entries ending with "_" are treated as prefixes.
func matchesAllowlist(name string, allowlist []string) bool {
	for _, entry := range allowlist {
		if strings.HasSuffix(entry, "_") {
			// Prefix match.
			if strings.HasPrefix(name, entry) {
				return true
			}
		} else {
			// Exact match.
			if name == entry {
				return true
			}
		}
	}
	return false
}
