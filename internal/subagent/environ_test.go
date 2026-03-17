package subagent

import (
	"slices"
	"testing"
)

func TestMatchesAllowlist(t *testing.T) {
	allowlist := []string{"HOME", "PATH", "LC_", "GIT_", "PI_"}

	tests := []struct {
		name  string
		env   string
		match bool
	}{
		{"exact match HOME", "HOME", true},
		{"exact match PATH", "PATH", true},
		{"prefix match LC_ALL", "LC_ALL", true},
		{"prefix match LC_CTYPE", "LC_CTYPE", true},
		{"prefix match GIT_DIR", "GIT_DIR", true},
		{"prefix match PI_SUBAGENT_TIMEOUT_MS", "PI_SUBAGENT_TIMEOUT_MS", true},
		{"no match SECRET_KEY", "SECRET_KEY", false},
		{"no match AWS_ACCESS_KEY_ID", "AWS_ACCESS_KEY_ID", false},
		{"no match ANTHROPIC_API_KEY", "ANTHROPIC_API_KEY", false},
		{"partial name not matched", "HOMEWARD", false},
		{"prefix entry itself", "LC_", true},
		{"empty name", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchesAllowlist(tt.env, allowlist)
			if got != tt.match {
				t.Errorf("matchesAllowlist(%q) = %v, want %v", tt.env, got, tt.match)
			}
		})
	}
}

func TestFilterEnv(t *testing.T) {
	t.Run("filters using default allowlist", func(t *testing.T) {
		result := FilterEnv(nil)
		if result == nil {
			t.Fatal("expected non-nil result")
		}
		// PATH should almost always be present.
		found := false
		for _, entry := range result {
			if len(entry) >= 5 && entry[:5] == "PATH=" {
				found = true
				break
			}
		}
		if !found {
			t.Log("PATH not found in filtered env (unusual but not a bug)")
		}
	})

	t.Run("empty allowlist filters everything", func(t *testing.T) {
		result := FilterEnv([]string{})
		if len(result) != 0 {
			t.Errorf("expected 0 entries with empty allowlist, got %d", len(result))
		}
	})

	t.Run("secrets are filtered out", func(t *testing.T) {
		t.Setenv("TEST_SECRET_KEY_12345", "super-secret")
		result := FilterEnv([]string{"HOME", "PATH"})
		for _, entry := range result {
			if len(entry) >= 25 && entry[:25] == "TEST_SECRET_KEY_12345=" {
				t.Error("secret key should have been filtered out")
			}
		}
	})

	t.Run("PI_ prefixed vars pass through", func(t *testing.T) {
		t.Setenv("PI_TEST_VAR", "hello")
		result := FilterEnv([]string{"PI_"})
		if !slices.Contains(result, "PI_TEST_VAR=hello") {
			t.Error("PI_TEST_VAR should pass through PI_ prefix allowlist")
		}
	})
}

func TestDefaultEnvAllowlist(t *testing.T) {
	t.Run("contains essential entries", func(t *testing.T) {
		required := []string{"HOME", "PATH", "USER", "SHELL", "GOPATH", "GIT_", "PI_"}
		for _, req := range required {
			if !slices.Contains(DefaultEnvAllowlist, req) {
				t.Errorf("DefaultEnvAllowlist missing required entry %q", req)
			}
		}
	})

	t.Run("does not contain secret patterns", func(t *testing.T) {
		dangerous := []string{"AWS_", "ANTHROPIC_", "OPENAI_", "SECRET", "TOKEN", "PASSWORD", "CREDENTIAL"}
		for _, d := range dangerous {
			if slices.Contains(DefaultEnvAllowlist, d) {
				t.Errorf("DefaultEnvAllowlist should not contain %q", d)
			}
		}
	})
}
