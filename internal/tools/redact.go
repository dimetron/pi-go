package tools

import "regexp"

// secretPatterns matches common API key/secret assignment patterns.
// Each pattern captures a prefix (key name + delimiter) and the secret value.
var secretPatterns = []*regexp.Regexp{
	// KEY=value or KEY="value" or KEY='value' (shell exports, env files)
	regexp.MustCompile(`(?i)((?:api[_-]?key|secret[_-]?key|access[_-]?key|auth[_-]?token|bearer[_-]?token|private[_-]?key|client[_-]?secret|password|passwd|token|secret)\s*=\s*"?)([^"'\s]{8,})("?)`),
	// export KEY=value
	regexp.MustCompile(`(?i)(export\s+\w*(?:key|token|secret|password|passwd)\w*\s*=\s*"?)([^"'\s]{8,})("?)`),
	// Bearer <token> in output
	regexp.MustCompile(`(?i)(Bearer\s+)(\S{8,})`),
	// sk-... (OpenAI-style keys)
	regexp.MustCompile(`()(sk-[a-zA-Z0-9]{20,})`),
	// ghp_... (GitHub PATs)
	regexp.MustCompile(`()(ghp_[a-zA-Z0-9]{20,})`),
	// gho_... (GitHub OAuth)
	regexp.MustCompile(`()(gho_[a-zA-Z0-9]{20,})`),
	// anthropic keys (sk-ant-...)
	regexp.MustCompile(`()(sk-ant-[a-zA-Z0-9-]{20,})`),
}

// redactSecrets replaces known API key/secret values with "***" in the given string.
func redactSecrets(s string) string {
	for _, re := range secretPatterns {
		s = re.ReplaceAllString(s, "${1}***${3}")
	}
	return s
}
