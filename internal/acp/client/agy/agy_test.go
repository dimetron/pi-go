package agy

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestRunnerStartRejectsEmptyPrompt verifies that an empty prompt is rejected.
func TestRunnerStartRejectsEmptyPrompt(t *testing.T) {
	runner := Runner{}
	_, err := runner.Start(context.Background(), RunRequest{})
	if err == nil {
		t.Fatal("expected error for empty prompt")
	}
}

// TestRunnerFindsBinary verifies binary lookup works correctly.
func TestRunnerFindsBinary(t *testing.T) {
	// Test that findBinary returns error when binary not found.
	_, err := findBinary([]string{"nonexistent-binary-xyz"})
	if err == nil {
		t.Fatal("expected error for nonexistent binary")
	}

	// Test that it finds an existing binary in PATH.
	path, err := findBinary([]string{"ls"})
	if err != nil {
		t.Fatalf("findBinary(ls) failed: %v", err)
	}
	if path == "" {
		t.Fatal("expected non-empty path for ls")
	}
}

// TestRunRequestValidation verifies prompt validation through Start.
func TestRunRequestValidation(t *testing.T) {
	tests := []struct {
		name    string
		req     RunRequest
		wantErr bool
	}{
		{
			name:    "empty prompt",
			req:     RunRequest{Prompt: ""},
			wantErr: true,
		},
		{
			name:    "whitespace only prompt",
			req:     RunRequest{Prompt: "   "},
			wantErr: true,
		},
		{
			name: "valid prompt with binary",
			req: RunRequest{
				Prompt:  "Hello",
				Command: []string{"/bin/true"},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := Runner{}
			_, err := runner.Start(context.Background(), tt.req)
			if tt.wantErr && err == nil {
				t.Errorf("Start() error = nil, wantErr true")
			}
		})
	}
}

// TestResolveCommandDefaultArgs verifies the platform's registry arguments are
// applied when only the binary is set.
func TestResolveCommandDefaultArgs(t *testing.T) {
	r := Runner{Binary: "agy_acp_server.par"}
	binary, args, err := r.resolveCommand(RunRequest{Prompt: "hi"})
	if err != nil {
		t.Fatalf("resolveCommand error: %v", err)
	}
	if binary != "agy_acp_server.par" {
		t.Fatalf("binary = %q, want agy_acp_server.par", binary)
	}
	if fmt.Sprint(args) != fmt.Sprint(DefaultArgs) {
		t.Fatalf("args = %v, want %v", args, DefaultArgs)
	}
}

// TestPlatformDefaults verifies the binary name and argument list match the
// antigravity-acp registry entry for the running platform.
func TestPlatformDefaults(t *testing.T) {
	wantName := "agy_acp_server.par"
	if runtime.GOOS == "windows" {
		wantName = "agy_acp_server.exe"
	}
	if got := binaryName(); got != wantName {
		t.Errorf("binaryName() = %q, want %q", got, wantName)
	}

	var wantArgs []string
	if runtime.GOOS == "linux" {
		wantArgs = []string{"--uid="}
	}
	if fmt.Sprint(defaultArgs()) != fmt.Sprint(wantArgs) {
		t.Errorf("defaultArgs() = %v, want %v", defaultArgs(), wantArgs)
	}
}

// TestResolveCommandHonorsCommandOverride verifies a test Command override is
// passed verbatim without default argument injection.
func TestResolveCommandHonorsCommandOverride(t *testing.T) {
	r := Runner{}
	binary, args, err := r.resolveCommand(RunRequest{
		Prompt:  "hi",
		Command: []string{"/bin/echo", "x"},
	})
	if err != nil {
		t.Fatalf("resolveCommand error: %v", err)
	}
	if binary != "/bin/echo" || fmt.Sprint(args) != fmt.Sprint([]string{"x"}) {
		t.Fatalf("binary=%q args=%v, want /bin/echo [x]", binary, args)
	}
}

// TestResolveCommandEnvOverride verifies PI_ACP_AGY_CMD overrides binary and
// args.
func TestResolveCommandEnvOverride(t *testing.T) {
	t.Run("binary and args", func(t *testing.T) {
		t.Setenv(envACPAgyCmd, "/bin/echo --foo")
		r := Runner{}
		binary, args, err := r.resolveCommand(RunRequest{Prompt: "hi"})
		if err != nil {
			t.Fatalf("resolveCommand error: %v", err)
		}
		if binary != "/bin/echo" || fmt.Sprint(args) != fmt.Sprint([]string{"--foo"}) {
			t.Fatalf("binary=%q args=%v, want /bin/echo [--foo]", binary, args)
		}
	})

	t.Run("bare binary falls back to default args", func(t *testing.T) {
		t.Setenv(envACPAgyCmd, "/bin/echo")
		r := Runner{}
		binary, args, err := r.resolveCommand(RunRequest{Prompt: "hi"})
		if err != nil {
			t.Fatalf("resolveCommand error: %v", err)
		}
		if binary != "/bin/echo" || fmt.Sprint(args) != fmt.Sprint(DefaultArgs) {
			t.Fatalf("binary=%q args=%v, want /bin/echo %v", binary, args, DefaultArgs)
		}
	})
}

// TestResolveCommandMissingBinary verifies a helpful error when no binary is
// found in the default paths.
func TestResolveCommandMissingBinary(t *testing.T) {
	// Point the default lookup at a nonexistent absolute path only.
	orig := DefaultBinaryPaths
	t.Cleanup(func() { DefaultBinaryPaths = orig })
	DefaultBinaryPaths = []string{filepath.Join(t.TempDir(), "no-such-agy")}

	r := Runner{}
	_, _, err := r.resolveCommand(RunRequest{Prompt: "hi"})
	if err == nil {
		t.Fatal("expected error when binary is missing")
	}
	if !strings.Contains(err.Error(), BinaryName) {
		t.Errorf("error %q should name the missing binary %q", err, BinaryName)
	}
}

// TestBinaryPaths verifies default binary paths are reasonable and include the
// directory install-agy-acp.sh extracts the registry archive into.
func TestBinaryPaths(t *testing.T) {
	if len(DefaultBinaryPaths) == 0 {
		t.Fatal("DefaultBinaryPaths should not be empty")
	}
	for _, path := range DefaultBinaryPaths {
		if path == "" {
			t.Error("DefaultBinaryPaths contains empty string")
		}
	}
	var found bool
	for _, path := range DefaultBinaryPaths {
		if strings.Contains(path, filepath.Join(".pi-go", "acp", "agy")) {
			found = true
		}
	}
	if !found {
		t.Errorf("DefaultBinaryPaths %v should include ~/.pi-go/acp/agy", DefaultBinaryPaths)
	}
}
