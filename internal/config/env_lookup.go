package config

import (
	"os"
	"path/filepath"
	"strings"
)

// LookupEnv resolves one setting from the process environment and from the
// .env files pi already reads, returning the value and where it came from.
//
// It exists because pi has two ways to hold a credential and only one of them
// was reachable from a command's own flag handling. `pi login` writes keys to
// ~/.pi-go/.env, config substitution reads .env files for MCP server URLs — yet
// a command calling os.Getenv directly sees none of that, so a key that pi
// itself saved reads as "not set" unless the user also exported it. Anything
// asking for a credential should ask here.
//
// Precedence is the process environment first, then the nearest project file,
// then the home file: an explicit export for one run must beat a file, and a
// project's own key must beat the account-wide one. Files searched, in order:
//
//	<cwd or a parent>/.pi-go/.env    — the project key
//	<cwd or a parent>/.env           — the plain dotenv a project may already have
//	~/.pi-go/.env                    — what `pi login` writes
//
// names are tried in order, so a caller can accept an alias
// (GEMINI_API_KEY, then GOOGLE_API_KEY) in one call. The returned source is
// meant for a log line telling the operator which file supplied the value —
// never the value itself.
func LookupEnv(names ...string) (value, source string) {
	return LookupEnvFrom(".", names...)
}

// LookupEnvFrom is LookupEnv rooted at an explicit directory, which is what a
// server serving a project other than its own working directory needs.
func LookupEnvFrom(cwd string, names ...string) (value, source string) {
	for _, name := range names {
		if v := strings.TrimSpace(os.Getenv(name)); v != "" {
			return v, "environment"
		}
	}
	for _, path := range envFileCandidates(cwd) {
		file := make(map[string]string)
		mergeEnvFile(file, path)
		for _, name := range names {
			// mergeEnvFile splits on the first "=" and keeps the key verbatim,
			// so a line written `export FOO=bar` is stored under "export FOO".
			// Both spellings are common in a hand-written .env.
			for _, key := range []string{name, "export " + name} {
				if v := unquoteEnvValue(file[key]); v != "" {
					return v, path
				}
			}
		}
	}
	return "", ""
}

// envFileCandidates lists the .env files to consult, nearest first.
func envFileCandidates(cwd string) []string {
	var paths []string
	if p := findNearestProjectFile(cwd, filepath.Join(".pi-go", ".env")); p != "" {
		paths = append(paths, p)
	}
	if p := findNearestProjectFile(cwd, ".env"); p != "" {
		paths = append(paths, p)
	}
	if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths, filepath.Join(home, ".pi-go", ".env"))
	}
	return paths
}

// unquoteEnvValue strips the quoting hand-written .env files carry.
// mergeEnvFile is deliberately literal — it feeds ${VAR} substitution, where a
// quoted value is the user's problem — but a credential sent straight to a
// provider cannot keep its quotes.
func unquoteEnvValue(v string) string {
	v = strings.TrimSpace(v)
	if len(v) >= 2 {
		if (v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'') {
			v = v[1 : len(v)-1]
		}
	}
	return strings.TrimSpace(v)
}
