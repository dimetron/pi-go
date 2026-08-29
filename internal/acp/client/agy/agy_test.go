package agy

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
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
// antigravity-acp registry entry, for every platform the entry covers rather
// than only the one running the test. The registry declares agy_acp_server.exe
// on Windows and .par elsewhere, and --uid= on Linux alone.
func TestPlatformDefaults(t *testing.T) {
	tests := []struct {
		goos     string
		wantName string
		wantArgs []string
	}{
		{goos: "darwin", wantName: "agy_acp_server.par"},
		{goos: "linux", wantName: "agy_acp_server.par", wantArgs: []string{"--uid="}},
		{goos: "windows", wantName: "agy_acp_server.exe"},
	}

	for _, tt := range tests {
		t.Run(tt.goos, func(t *testing.T) {
			if got := binaryNameFor(tt.goos); got != tt.wantName {
				t.Errorf("binaryNameFor(%q) = %q, want %q", tt.goos, got, tt.wantName)
			}
			if got := defaultArgsFor(tt.goos); fmt.Sprint(got) != fmt.Sprint(tt.wantArgs) {
				t.Errorf("defaultArgsFor(%q) = %v, want %v", tt.goos, got, tt.wantArgs)
			}
		})
	}

	// The running platform's values are what the package actually uses.
	if got := binaryName(); got != binaryNameFor(runtime.GOOS) {
		t.Errorf("binaryName() = %q, want %q", got, binaryNameFor(runtime.GOOS))
	}
	if got := defaultArgs(); fmt.Sprint(got) != fmt.Sprint(defaultArgsFor(runtime.GOOS)) {
		t.Errorf("defaultArgs() = %v, want %v", got, defaultArgsFor(runtime.GOOS))
	}
}

// TestDefaultBinaryPathsForPlatforms pins the search order on every platform:
// the install directory first, the bare name (the only entry that reaches
// exec.LookPath) last, and no Unix system path on Windows.
func TestDefaultBinaryPathsForPlatforms(t *testing.T) {
	const home = "/home/someone"

	for _, goos := range []string{"darwin", "linux", "windows"} {
		t.Run(goos, func(t *testing.T) {
			paths := defaultBinaryPathsFor(goos, home)
			name := binaryNameFor(goos)

			if want := filepath.Join(home, ".pi-go", "acp", "agy", name); paths[0] != want {
				t.Errorf("first entry = %q, want the install dir %q", paths[0], want)
			}
			if last := paths[len(paths)-1]; last != name {
				t.Errorf("last entry = %q, want the bare name %q", last, name)
			}
			for i, p := range paths[:len(paths)-1] {
				if filepath.Dir(p) == "." {
					t.Errorf("entry %d = %q; only the last entry may be a bare name", i, p)
				}
			}

			hasUnixBin := slices.Contains(paths, filepath.Join("/usr/local/bin", name))
			if goos == "windows" && hasUnixBin {
				t.Errorf("windows search list %v should not carry a Unix system path", paths)
			}
			if goos != "windows" && !hasUnixBin {
				t.Errorf("%s search list %v should carry /usr/local/bin", goos, paths)
			}
		})
	}
}

// TestDefaultBinaryPathsForNoHome verifies an unresolvable home directory
// drops only the entries derived from it, leaving a usable search list.
func TestDefaultBinaryPathsForNoHome(t *testing.T) {
	paths := defaultBinaryPathsFor("linux", "")
	want := []string{filepath.Join("/usr/local/bin", "agy_acp_server.par"), "agy_acp_server.par"}
	if fmt.Sprint(paths) != fmt.Sprint(want) {
		t.Errorf("defaultBinaryPathsFor(linux, \"\") = %v, want %v", paths, want)
	}
}

// TestStartReportsUnresolvedBinary verifies Start surfaces the resolution
// error rather than spawning something unexpected.
func TestStartReportsUnresolvedBinary(t *testing.T) {
	orig := DefaultBinaryPaths
	t.Cleanup(func() { DefaultBinaryPaths = orig })
	DefaultBinaryPaths = []string{filepath.Join(t.TempDir(), "no-such-agy")}

	if _, err := (Runner{}).Start(context.Background(), RunRequest{Prompt: "hi"}); err == nil {
		t.Fatal("expected an error when the binary cannot be resolved")
	}
}

// TestFindBinarySkipsEmptyEntries verifies an empty entry is skipped rather
// than resolved, so a misconfigured list cannot spawn the wrong thing.
func TestFindBinarySkipsEmptyEntries(t *testing.T) {
	path, err := findBinary([]string{"", "ls"})
	if err != nil {
		t.Fatalf("findBinary: %v", err)
	}
	if path == "" {
		t.Fatal("expected the non-empty entry to resolve")
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

// TestResolveCommandBlankEnvOverride verifies a whitespace-only
// PI_ACP_AGY_CMD is treated as unset. Splitting it yields no fields, and
// without this it would leave the binary empty and reach exec as a spawn
// of "" — an error far from its cause.
func TestResolveCommandBlankEnvOverride(t *testing.T) {
	t.Setenv(envACPAgyCmd, "   ")

	// An absolute path is resolved by stat rather than by exec.LookPath, so
	// the expected value is exact on every platform — a PATH lookup would
	// come back as "ls.exe" on Windows.
	installed := filepath.Join(t.TempDir(), binaryName())
	if err := os.WriteFile(installed, []byte("stub"), 0o755); err != nil {
		t.Fatalf("write stub binary: %v", err)
	}

	orig := DefaultBinaryPaths
	t.Cleanup(func() { DefaultBinaryPaths = orig })
	DefaultBinaryPaths = []string{installed}

	binary, _, err := (Runner{}).resolveCommand(RunRequest{Prompt: "hi"})
	if err != nil {
		t.Fatalf("resolveCommand error: %v", err)
	}
	if binary != installed {
		t.Errorf("binary = %q, want %q resolved from DefaultBinaryPaths", binary, installed)
	}
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

// TestBinaryPathsPreferInstallDirOverPATH pins the search order: the bare name
// resolves through exec.LookPath, so it must come last. If it came first, a
// binary earlier on the caller's PATH would shadow the one the installer put
// in ~/.pi-go/acp/agy, making the resolved server depend on the environment
// rather than on what was installed.
func TestBinaryPathsPreferInstallDirOverPATH(t *testing.T) {
	if got := DefaultBinaryPaths[len(DefaultBinaryPaths)-1]; got != BinaryName {
		t.Errorf("last search entry = %q, want the bare name %q so PATH is the last resort", got, BinaryName)
	}
	// Directory-qualified rather than absolute: on Windows an absolute path
	// needs a drive letter, and the entries built from the home directory are
	// the ones that matter here. What must not appear before the last entry is
	// a second bare name, since that would reach exec.LookPath too.
	for i, path := range DefaultBinaryPaths[:len(DefaultBinaryPaths)-1] {
		if filepath.Dir(path) == "." {
			t.Errorf("entry %d = %q; every entry before the bare name must name a directory", i, path)
		}
	}
	if strings.Contains(DefaultBinaryPaths[0], filepath.Join(".pi-go", "acp", "agy")) {
		return
	}
	// Only a home directory that cannot be resolved should drop the install
	// dir from the front, and then the list is the fixed system paths.
	if home, err := os.UserHomeDir(); err == nil {
		t.Errorf("first search entry = %q, want the install dir under %s", DefaultBinaryPaths[0], home)
	}
}
