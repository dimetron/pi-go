package tools

import "testing"

func TestRedactSecrets(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "API_KEY assignment",
			input: `export API_KEY=sk-1234567890abcdef1234567890`,
			want:  `export API_KEY=***`,
		},
		{
			name:  "quoted secret",
			input: `ANTHROPIC_API_KEY="sk-ant-abcdef1234567890abcdef1234"`,
			want:  `ANTHROPIC_API_KEY="***"`,
		},
		{
			name:  "OpenAI key in text",
			input: `Your key is sk-abcdefghijklmnopqrstuvwxyz`,
			want:  `Your key is ***`,
		},
		{
			name:  "GitHub PAT",
			input: `token: ghp_abcdefghijklmnopqrstuvwxyz1234`,
			want:  `token: ***`,
		},
		{
			name:  "Bearer token",
			input: `Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.abcdef`,
			want:  `Authorization: Bearer ***`,
		},
		{
			name:  "no secrets",
			input: `just normal output with no secrets`,
			want:  `just normal output with no secrets`,
		},
		{
			name:  "short values not redacted",
			input: `KEY=short`,
			want:  `KEY=short`,
		},
		{
			name:  "password in env",
			input: `export PASSWORD=mysupersecretpassword123`,
			want:  `export PASSWORD=***`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := redactSecrets(tt.input)
			if got != tt.want {
				t.Errorf("redactSecrets(%q)\n  got:  %q\n  want: %q", tt.input, got, tt.want)
			}
		})
	}
}
