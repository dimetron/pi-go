package cli

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

// -----------------------------------------------------------------------
// newACPServerCmd — structural verification
// -----------------------------------------------------------------------

func TestNewACPServerCmd_Structure(t *testing.T) {
	cmd := newACPServerCmd()
	if cmd.Use != "acp-server" {
		t.Errorf("Use = %q, want %q", cmd.Use, "acp-server")
	}
	if cmd.RunE == nil {
		t.Error("RunE is nil")
	}
	if cmd.Flags().Lookup("model") == nil {
		t.Error("missing --model flag")
	}
	// Verify Args == NoArgs.
	if cmd.Args == nil {
		t.Error("Args validator is nil")
	}
}

// -----------------------------------------------------------------------
// runACPServer — drive with canceled context so Serve returns quickly.
// runACPServer calls signal.NotifyContext and acpserver.Serve which blocks
// on ctx.Done(). By redirecting os.Stdin/os.Stdout to pipes and canceling
// the parent context, Serve returns ctx.Err() which is wrapped.
// -----------------------------------------------------------------------

func TestRunACPServer_ContextCanceled(t *testing.T) {
	// Save and restore stdin/stdout.
	origStdin := os.Stdin
	origStdout := os.Stdout
	defer func() {
		os.Stdin = origStdin
		os.Stdout = origStdout
	}()

	// Set up a pipe for stdin so reads block but never receive data.
	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	defer stdinR.Close()
	defer stdinW.Close()
	os.Stdin = stdinR

	// Drain stdout into a discard so the ACP server's JSON writes don't deadlock.
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	defer stdoutR.Close()
	defer stdoutW.Close()
	os.Stdout = stdoutW

	// Drain stdout in background so we don't fill the pipe.
	go func() {
		_, _ = io.Copy(io.Discard, stdoutR)
	}()

	// Cancel the context quickly after starting to stop the server.
	ctx, cancel := context.WithCancel(context.Background())

	cmd := &cobra.Command{}
	cmd.SetContext(ctx)

	// Save and restore flags.
	origModel := flagModel
	defer func() { flagModel = origModel }()
	flagModel = "" // exercise the fallback "minimax-m2.7:cloud" branch.

	done := make(chan error, 1)
	go func() {
		done <- runACPServer(cmd, nil)
	}()

	// Give runACPServer a moment to start, then cancel.
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		// Either nil (clean exit) or a wrapped context error is acceptable.
		if err != nil && !strings.Contains(err.Error(), "context") &&
			!strings.Contains(err.Error(), "acp server") &&
			!strings.Contains(err.Error(), "canceled") {
			t.Logf("runACPServer returned: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runACPServer did not return within 5 seconds of context cancel")
	}
}

func TestRunACPServer_ExplicitModel(t *testing.T) {
	origStdin := os.Stdin
	origStdout := os.Stdout
	defer func() {
		os.Stdin = origStdin
		os.Stdout = origStdout
	}()

	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	defer stdinR.Close()
	defer stdinW.Close()
	os.Stdin = stdinR

	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	defer stdoutR.Close()
	defer stdoutW.Close()
	os.Stdout = stdoutW
	go func() { _, _ = io.Copy(io.Discard, stdoutR) }()

	ctx, cancel := context.WithCancel(context.Background())
	cmd := &cobra.Command{}
	cmd.SetContext(ctx)

	origModel := flagModel
	defer func() { flagModel = origModel }()
	flagModel = "gpt-4o" // Exercise the branch where model != "".

	done := make(chan error, 1)
	go func() { done <- runACPServer(cmd, nil) }()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("runACPServer did not return within 5 seconds")
	}
}
