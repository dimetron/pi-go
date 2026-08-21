package config

import (
	"os"
	"path/filepath"
	"testing"
)

// writeEnv creates dir/rel containing body and returns the file path.
func writeEnv(t *testing.T, dir, rel, body string) string {
	t.Helper()
	path := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

func TestLookupEnvPrefersProcessEnvironment(t *testing.T) {
	dir := t.TempDir()
	writeEnv(t, dir, ".pi-go/.env", "PI_TEST_KEY=from-file\n")
	t.Setenv("PI_TEST_KEY", "from-environment")

	// An explicit export for one run must beat a file.
	got, source := LookupEnvFrom(dir, "PI_TEST_KEY")
	if got != "from-environment" {
		t.Errorf("value = %q, want the exported one", got)
	}
	if source != "environment" {
		t.Errorf("source = %q, want %q", source, "environment")
	}
}

// The point of the whole helper: a key `pi login` saved must be found without
// the user also exporting it.
func TestLookupEnvReadsProjectFile(t *testing.T) {
	dir := t.TempDir()
	path := writeEnv(t, dir, ".pi-go/.env", "# a comment\nGEMINI_API_KEY=AIzaSyFromFile\n")
	t.Setenv("GEMINI_API_KEY", "")

	got, source := LookupEnvFrom(dir, "GEMINI_API_KEY")
	if got != "AIzaSyFromFile" {
		t.Errorf("value = %q", got)
	}
	if source != path {
		t.Errorf("source = %q, want %q", source, path)
	}
}

// A project that keeps a plain .env should not have to duplicate it under
// .pi-go/ just for voice.
func TestLookupEnvReadsPlainDotEnv(t *testing.T) {
	dir := t.TempDir()
	writeEnv(t, dir, ".env", "GEMINI_API_KEY=AIzaSyPlain\n")
	t.Setenv("GEMINI_API_KEY", "")

	if got, _ := LookupEnvFrom(dir, "GEMINI_API_KEY"); got != "AIzaSyPlain" {
		t.Errorf("value = %q", got)
	}
}

func TestLookupEnvProjectFileBeatsPlainDotEnv(t *testing.T) {
	dir := t.TempDir()
	writeEnv(t, dir, ".pi-go/.env", "GEMINI_API_KEY=AIzaSyPiGo\n")
	writeEnv(t, dir, ".env", "GEMINI_API_KEY=AIzaSyPlain\n")
	t.Setenv("GEMINI_API_KEY", "")

	if got, _ := LookupEnvFrom(dir, "GEMINI_API_KEY"); got != "AIzaSyPiGo" {
		t.Errorf("value = %q, want pi's own file to win", got)
	}
}

// The file is walked up from the working directory, the same way pi finds every
// other project file.
func TestLookupEnvWalksUp(t *testing.T) {
	dir := t.TempDir()
	writeEnv(t, dir, ".pi-go/.env", "GEMINI_API_KEY=AIzaSyParent\n")
	deep := filepath.Join(dir, "a", "b", "c")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	t.Setenv("GEMINI_API_KEY", "")

	if got, _ := LookupEnvFrom(deep, "GEMINI_API_KEY"); got != "AIzaSyParent" {
		t.Errorf("value = %q", got)
	}
}

// A hand-written .env carries quoting and `export` that a credential sent to a
// provider must not keep.
func TestLookupEnvUnwrapsWrittenForms(t *testing.T) {
	tests := []struct {
		name string
		line string
		want string
	}{
		{"bare", "K=plain", "plain"},
		{"double quoted", `K="quoted"`, "quoted"},
		{"single quoted", "K='quoted'", "quoted"},
		{"export prefix", "export K=exported", "exported"},
		{"export and quotes", `export K="both"`, "both"},
		{"padded", "K =  spaced  ", "spaced"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			writeEnv(t, dir, ".pi-go/.env", tt.line+"\n")
			t.Setenv("K", "")
			if got, _ := LookupEnvFrom(dir, "K"); got != tt.want {
				t.Errorf("value = %q, want %q", got, tt.want)
			}
		})
	}
}

// One call accepts a key's aliases, in order.
func TestLookupEnvTriesAliasesInOrder(t *testing.T) {
	dir := t.TempDir()
	writeEnv(t, dir, ".pi-go/.env", "GOOGLE_API_KEY=AIzaSyFallback\n")
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("GOOGLE_API_KEY", "")

	if got, _ := LookupEnvFrom(dir, "GEMINI_API_KEY", "GOOGLE_API_KEY"); got != "AIzaSyFallback" {
		t.Errorf("value = %q, want the alias to be found", got)
	}
}

func TestLookupEnvMissing(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PI_DEFINITELY_UNSET_KEY", "")

	value, source := LookupEnvFrom(dir, "PI_DEFINITELY_UNSET_KEY")
	if value != "" || source != "" {
		t.Errorf("LookupEnvFrom() = (%q, %q), want empty", value, source)
	}
}
