package cursor

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	shared "github.com/dimetron/pi-go/internal/acp"
)

func TestRunnerStartRejectsEmptyPrompt(t *testing.T) {
	runner := Runner{}
	if _, err := runner.Start(context.Background(), RunRequest{}); err == nil {
		t.Fatal("expected error for empty prompt")
	}
}

func TestRunnerFindsBinary(t *testing.T) {
	if _, err := findBinary([]string{"nonexistent-binary-xyz"}); err == nil {
		t.Fatal("expected error for nonexistent binary")
	}
	path, err := findBinary([]string{"ls"})
	if err != nil {
		t.Fatalf("findBinary(ls) failed: %v", err)
	}
	if path == "" {
		t.Fatal("expected non-empty path for ls")
	}
}

func TestRunRequestValidation(t *testing.T) {
	tests := []struct {
		name    string
		req     RunRequest
		wantErr bool
	}{
		{"empty prompt", RunRequest{Prompt: ""}, true},
		{"whitespace only prompt", RunRequest{Prompt: "   "}, true},
		{"valid prompt with binary", RunRequest{
			Prompt:  "Hello",
			Command: []string{"/bin/true"},
		}, false},
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

func TestBuildArgs(t *testing.T) {
	tests := []struct {
		name              string
		req               RunRequest
		includeSubcommand bool
		want              []string
	}{
		{
			name:              "defaults with subcommand",
			req:               RunRequest{Prompt: "hi"},
			includeSubcommand: true,
			want:              []string{"acp"},
		},
		{
			name:              "with endpoint",
			req:               RunRequest{Prompt: "hi", Endpoint: "https://api2.cursor.sh"},
			includeSubcommand: true,
			want:              []string{"-e", "https://api2.cursor.sh", "acp"},
		},
		{
			name:              "with api key",
			req:               RunRequest{Prompt: "hi", APIKey: "sk-123"},
			includeSubcommand: true,
			want:              []string{"--api-key", "sk-123", "acp"},
		},
		{
			name:              "with auth token",
			req:               RunRequest{Prompt: "hi", AuthToken: "tok-abc"},
			includeSubcommand: true,
			want:              []string{"--auth-token", "tok-abc", "acp"},
		},
		{
			name:              "all auth flags",
			req:               RunRequest{Prompt: "hi", APIKey: "k", AuthToken: "t", Endpoint: "https://ep"},
			includeSubcommand: true,
			want:              []string{"-e", "https://ep", "--api-key", "k", "--auth-token", "t", "acp"},
		},
		{
			name:              "no subcommand when test overrides argv",
			req:               RunRequest{Prompt: "hi", APIKey: "k"},
			includeSubcommand: false,
			want:              []string{"--api-key", "k"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildArgs(tt.req, tt.includeSubcommand)
			if !slices.Equal(got, tt.want) {
				t.Errorf("buildArgs() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDefaultBinaryPathsPrefersAgent(t *testing.T) {
	if len(DefaultBinaryPaths) == 0 {
		t.Fatal("DefaultBinaryPaths is empty")
	}
	// Cursor's official installer creates `agent` at ~/.local/bin/agent and
	// `cursor-agent` as a legacy alias; `agent` is the canonical name.
	if DefaultBinaryPaths[0] != "agent" {
		t.Errorf("first path should be 'agent', got %q", DefaultBinaryPaths[0])
	}
	if !slices.Contains(DefaultBinaryPaths, "cursor-agent") {
		t.Errorf("DefaultBinaryPaths should keep 'cursor-agent' as legacy fallback; got %v", DefaultBinaryPaths)
	}
	// The Cursor installer's canonical path is $HOME/.local/bin/agent.
	homeAgent := filepath.Join(".local", "bin", "agent")
	if !slices.Contains(DefaultBinaryPaths, homeAgent) {
		t.Errorf("DefaultBinaryPaths should include %q (Cursor install location); got %v", homeAgent, DefaultBinaryPaths)
	}
}

func TestRunnerStartUsesCommandField(t *testing.T) {
	scriptDir := t.TempDir()
	scriptPath := filepath.Join(scriptDir, "echo_args.sh")
	if err := os.WriteFile(scriptPath, []byte(`#!/bin/bash
printf '{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":1,"agentInfo":{"name":"test","version":"1.0"}}}\n'
printf '{"jsonrpc":"2.0","id":2,"result":{"sessionId":"test-session"}}\n'
printf '{"jsonrpc":"2.0","id":3,"result":{"stopReason":"end_turn"}}\n'
exit 0
`), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	runner := Runner{}
	req := RunRequest{
		Prompt:  "test",
		Command: []string{"/bin/bash", scriptPath},
	}
	session, err := runner.Start(context.Background(), req)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer session.Cancel()

	select {
	case <-session.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("session did not complete in time")
	}
}

func TestRunnerStartBinaryNotFound(t *testing.T) {
	runner := Runner{Binary: "/nonexistent/path/to/cursor-agent-xyz123"}
	_, err := runner.Start(context.Background(), RunRequest{Prompt: "test"})
	if err == nil {
		t.Fatal("expected error for nonexistent binary")
	}
	if !strings.Contains(err.Error(), "finding cursor-agent") {
		t.Errorf("error should wrap 'finding cursor-agent', got: %v", err)
	}
}

func TestRunnerStartSubprocessFails(t *testing.T) {
	runner := Runner{Binary: "sh"}
	session, err := runner.Start(context.Background(), RunRequest{
		Prompt:  "test",
		Command: []string{"sh", "-c", "exit 1"}, // Exit immediately after start
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	select {
	case <-session.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("session did not complete in time")
	}

	result := session.Wait()
	if result.Status != shared.StatusError {
		t.Fatalf("result.Status = %s, want %s (error=%q)", result.Status, shared.StatusError, result.Error)
	}
	if strings.TrimSpace(result.Error) == "" {
		t.Fatal("expected non-empty error when subprocess exits immediately")
	}
}

func TestResolveCommand(t *testing.T) {
	// Create a real binary on disk so findBinary succeeds for Binary and default branches.
	binDir := t.TempDir()
	binPath := filepath.Join(binDir, "mybin")
	if err := os.WriteFile(binPath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}

	tests := []struct {
		name                 string
		runner               Runner
		req                  RunRequest
		envSet               func(t *testing.T)
		wantBinary           string
		wantArgs             []string
		wantErr              bool
		errSubstr            string
		overrideDefaultPaths bool // force DefaultBinaryPaths to a guaranteed-missing value
	}{
		{
			name:       "binary set uses findBinary and appends acp subcommand",
			runner:     Runner{Binary: binPath},
			req:        RunRequest{Prompt: "hi"},
			wantBinary: binPath,
			wantArgs:   []string{"acp"},
		},
		{
			name:       "binary set with endpoint and apikey",
			runner:     Runner{Binary: binPath},
			req:        RunRequest{Prompt: "hi", Endpoint: "https://ep", APIKey: "k"},
			wantBinary: binPath,
			wantArgs:   []string{"-e", "https://ep", "--api-key", "k", "acp"},
		},
		{
			name:      "binary set but not found returns error",
			runner:    Runner{Binary: "/nonexistent/path/to/binary-xyz"},
			req:       RunRequest{Prompt: "hi"},
			wantErr:   true,
			errSubstr: "finding cursor-agent",
		},
		{
			name:       "command single element defaults to acp subcommand (no extra args)",
			runner:     Runner{},
			req:        RunRequest{Prompt: "hi", Command: []string{"/bin/true"}},
			wantBinary: "/bin/true",
			wantArgs:   []string{},
		},
		{
			name:       "command multiple elements used verbatim",
			runner:     Runner{},
			req:        RunRequest{Prompt: "hi", Command: []string{"/bin/echo", "hello", "world"}},
			wantBinary: "/bin/echo",
			wantArgs:   []string{"hello", "world"},
		},
		{
			name: "env var bare binary defaults to acp subcommand",
			envSet: func(t *testing.T) {
				t.Setenv("PI_ACP_CURSOR_CMD", "/bin/true")
			},
			runner:     Runner{},
			req:        RunRequest{Prompt: "hi"},
			wantBinary: "/bin/true",
			wantArgs:   []string{"acp"},
		},
		{
			name: "env var binary with args used as-is",
			envSet: func(t *testing.T) {
				t.Setenv("PI_ACP_CURSOR_CMD", "/bin/echo hello world")
			},
			runner:     Runner{},
			req:        RunRequest{Prompt: "hi"},
			wantBinary: "/bin/echo",
			wantArgs:   []string{"hello", "world"},
		},
		{
			name: "env var binary with args and buildArgs options ignored",
			envSet: func(t *testing.T) {
				t.Setenv("PI_ACP_CURSOR_CMD", "/bin/echo foo")
			},
			runner:     Runner{},
			req:        RunRequest{Prompt: "hi", Endpoint: "https://ignored", APIKey: "k"},
			wantBinary: "/bin/echo",
			wantArgs:   []string{"foo"},
		},
		{
			name:       "binary field takes precedence over command field",
			runner:     Runner{Binary: binPath},
			req:        RunRequest{Prompt: "hi", Command: []string{"/bin/echo", "ignored"}},
			wantBinary: binPath,
			wantArgs:   []string{"acp"},
		},
		{
			name:   "binary field takes precedence over env var",
			runner: Runner{Binary: binPath},
			req:    RunRequest{Prompt: "hi"},
			envSet: func(t *testing.T) {
				t.Setenv("PI_ACP_CURSOR_CMD", "/bin/echo ignored")
			},
			wantBinary: binPath,
			wantArgs:   []string{"acp"},
		},
		{
			name:   "command field takes precedence over env var",
			runner: Runner{},
			req:    RunRequest{Prompt: "hi", Command: []string{"/bin/echo", "fromcmd"}},
			envSet: func(t *testing.T) {
				t.Setenv("PI_ACP_CURSOR_CMD", "/bin/echo fromenv")
			},
			wantBinary: "/bin/echo",
			wantArgs:   []string{"fromcmd"},
		},
		// The "default fallback uses DefaultBinaryPaths" branch is exercised
		// separately in TestResolveCommandUsesDefaultBinaryPaths because it
		// depends on the host having cursor-agent (or `agent`) installed.
		// The error counterpart — no binary in DefaultBinaryPaths — is
		// covered here by overriding DefaultBinaryPaths to a guaranteed-
		// missing value, so the test is deterministic on any machine.
		{
			name:                 "default fallback returns error when DefaultBinaryPaths has no match",
			runner:               Runner{},
			req:                  RunRequest{Prompt: "hi"},
			wantErr:              true,
			errSubstr:            "finding cursor-agent",
			overrideDefaultPaths: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Ensure env var is unset by default unless the test sets it.
			t.Setenv("PI_ACP_CURSOR_CMD", "")
			if tt.envSet != nil {
				tt.envSet(t)
			}

			// For the "no match" branch, redirect DefaultBinaryPaths to a
			// value that is guaranteed to be missing so the test does not
			// depend on the host environment.
			if tt.overrideDefaultPaths {
				origPaths := DefaultBinaryPaths
				DefaultBinaryPaths = []string{"definitely-not-a-real-binary-xyz123"}
				t.Cleanup(func() { DefaultBinaryPaths = origPaths })
			}

			binary, args, err := tt.runner.resolveCommand(tt.req)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("resolveCommand() error = nil, want error")
				}
				if tt.errSubstr != "" && !strings.Contains(err.Error(), tt.errSubstr) {
					t.Errorf("resolveCommand() error = %v, want substring %q", err, tt.errSubstr)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveCommand() unexpected error: %v", err)
			}
			if tt.wantBinary != "" && binary != tt.wantBinary {
				t.Errorf("resolveCommand() binary = %q, want %q", binary, tt.wantBinary)
			}
			if tt.wantBinary == "" && binary == "" {
				t.Error("resolveCommand() binary is empty, want non-empty")
			}
			if !slices.Equal(args, tt.wantArgs) {
				t.Errorf("resolveCommand() args = %v, want %v", args, tt.wantArgs)
			}
		})
	}
}

// TestResolveCommandUsesDefaultBinaryPaths verifies the DefaultBinaryPaths
// branch in resolveCommand: when no Binary, no Command, and no env override is
// set, the resolver falls back to DefaultBinaryPaths and appends the `acp`
// subcommand. The test is best-effort: if cursor-agent (or the `agent`
// fallback) is not installed on the host, resolution will fail and we skip
// the assertions — this matches the convention used by the claudecode and
// gemini resolveCommand tests, since CI runners do not have these CLIs.
func TestResolveCommandUsesDefaultBinaryPaths(t *testing.T) {
	t.Setenv("PI_ACP_CURSOR_CMD", "")

	r := Runner{}
	binary, args, err := r.resolveCommand(RunRequest{Prompt: "hi"})
	if err != nil {
		// cursor-agent / agent not installed on this host — skip.
		t.Skipf("DefaultBinaryPaths lookup failed (cursor-agent likely not installed): %v", err)
	}
	if binary == "" {
		t.Fatal("resolveCommand() binary is empty, want non-empty")
	}
	if !slices.Contains(args, ACPSubcommand) {
		t.Errorf("resolveCommand() args = %v, want to contain %q", args, ACPSubcommand)
	}
}

func TestFindBinaryEdgeCases(t *testing.T) {
	t.Run("empty string is skipped", func(t *testing.T) {
		_, err := findBinary([]string{"", "nonexistent-binary-xyz-abc"})
		if err == nil {
			t.Fatal("expected error when all entries are empty or not found")
		}
	})

	t.Run("empty string followed by valid PATH binary", func(t *testing.T) {
		got, err := findBinary([]string{"", "ls"})
		if err != nil {
			t.Fatalf("findBinary() error: %v", err)
		}
		if got == "" {
			t.Fatal("expected non-empty path")
		}
	})

	t.Run("absolute path exists", func(t *testing.T) {
		got, err := findBinary([]string{"/bin/ls"})
		if err != nil {
			t.Fatalf("findBinary(/bin/ls) error: %v", err)
		}
		if got != "/bin/ls" {
			t.Errorf("findBinary(/bin/ls) = %q, want /bin/ls", got)
		}
	})

	t.Run("absolute path does not exist skips to next", func(t *testing.T) {
		got, err := findBinary([]string{"/nonexistent/abs/path", "ls"})
		if err != nil {
			t.Fatalf("findBinary() error: %v", err)
		}
		if got == "" {
			t.Fatal("expected non-empty path")
		}
	})

	t.Run("relative path exists", func(t *testing.T) {
		dir := t.TempDir()
		rel := filepath.Join(dir, "relbin")
		if err := os.WriteFile(rel, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatalf("write file: %v", err)
		}
		// Use a relative path by cd-ing into the dir
		oldDir, err := os.Getwd()
		if err != nil {
			t.Fatalf("getwd: %v", err)
		}
		defer os.Chdir(oldDir)
		if err := os.Chdir(dir); err != nil {
			t.Fatalf("chdir: %v", err)
		}
		got, err := findBinary([]string{"./relbin"})
		if err != nil {
			t.Fatalf("findBinary(./relbin) error: %v", err)
		}
		if got != "./relbin" {
			t.Errorf("findBinary(./relbin) = %q, want ./relbin", got)
		}
	})

	t.Run("relative path not found skips to next", func(t *testing.T) {
		got, err := findBinary([]string{"./nonexistent-rel-xyz", "ls"})
		if err != nil {
			t.Fatalf("findBinary() error: %v", err)
		}
		if got == "" {
			t.Fatal("expected non-empty path")
		}
	})

	t.Run("all entries empty returns error", func(t *testing.T) {
		_, err := findBinary([]string{"", "", ""})
		if err == nil {
			t.Fatal("expected error when all entries are empty")
		}
	})
}
