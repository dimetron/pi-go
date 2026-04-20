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
			name:  "JWT token in text",
			input: `id_token=eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.signature123`,
			want:  `id_token=***`,
		},
		{
			name:  "AWS access key id",
			input: `aws_access_key_id=AKIA1234567890ABCDEF`,
			want:  `aws_access_key_id=***`,
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
		{
			name:  "empty string",
			input: ``,
			want:  ``,
		},
		{
			name:  "GitHub OAuth token gho_",
			input: `token: gho_abcdefghijklmnopqrstuvwxyz1234`,
			want:  `token: ***`,
		},
		{
			name:  "access_key assignment",
			input: `ACCESS_KEY=abcdef123456deadbeef`,
			want:  `ACCESS_KEY=***`,
		},
		{
			name:  "secret_key assignment quoted",
			input: `secret_key="deadbeef12345678deadbeef"`,
			want:  `secret_key="***"`,
		},
		{
			name:  "auth_token assignment",
			input: `AUTH_TOKEN=mysupersecrettoken999`,
			want:  `AUTH_TOKEN=***`,
		},
		{
			name:  "bearer_token assignment",
			input: `BEARER_TOKEN=abcdefghijklmno12345678`,
			want:  `BEARER_TOKEN=***`,
		},
		{
			name:  "private_key assignment",
			input: `PRIVATE_KEY=abcdef1234567890abcd`,
			want:  `PRIVATE_KEY=***`,
		},
		{
			name:  "client_secret assignment",
			input: `CLIENT_SECRET=xyz789abcdef12345678`,
			want:  `CLIENT_SECRET=***`,
		},
		{
			name:  "passwd assignment",
			input: `passwd=abcdef123456789abcdef`,
			want:  `passwd=***`,
		},
		{
			name:  "mixed case with token",
			input: `TOKEN=abcdef123456789ABCDEF`,
			want:  `TOKEN=***`,
		},
		{
			name:  "short bearer token not redacted",
			input: `Authorization: Bearer short`,
			want:  `Authorization: Bearer short`,
		},
		{
			name:  "sk- too short not redacted",
			input: `key=sk-tooshort`,
			want:  `key=sk-tooshort`,
		},
		{
			name:  "ghp short not redacted",
			input: `ghp_short`,
			want:  `ghp_short`,
		},
		{
			name:  "AKIA wrong length not redacted",
			input: `AKIA12345`,
			want:  `AKIA12345`,
		},
		{
			name:  "export token with underscore suffix",
			input: `export MY_SECRET_TOKEN=abcdefghijklmno12345`,
			want:  `export MY_SECRET_TOKEN=***`,
		},
		{
			name:  "multiple secrets on same line",
			input: `API_KEY=abcdef123456deadbeef TOKEN=xyz789abcdef12345678`,
			want:  `API_KEY=*** TOKEN=***`,
		},
		{
			name:  "plain text unchanged",
			input: `The quick brown fox jumps over the lazy dog`,
			want:  `The quick brown fox jumps over the lazy dog`,
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

// TestRedactSecrets_Idempotent verifies redactSecrets is idempotent — running
// it twice produces the same output as running it once.
func TestRedactSecrets_Idempotent(t *testing.T) {
	inputs := []string{
		"API_KEY=abcdefghijklmno12345",
		"no secret",
		"Bearer abcdefghijklmno12345",
		"sk-abcdefghijklmnopqrstuvwxyz12345",
	}
	for _, in := range inputs {
		once := redactSecrets(in)
		twice := redactSecrets(once)
		if once != twice {
			t.Errorf("redactSecrets not idempotent for %q: once=%q twice=%q", in, once, twice)
		}
	}
}
