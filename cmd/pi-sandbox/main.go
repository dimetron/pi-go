package main

import (
	"bufio"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"strings"
	"sync"
	"syscall"
)

//go:embed pi-profile.sb
var profile string

const logPredicate = `eventMessage CONTAINS "sandbox" AND eventMessage CONTAINS "deny"`

func main() {
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(os.Stderr, "pi-sandbox: cannot determine HOME: %v\n", err)
		os.Exit(1)
	}

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "pi-sandbox: cannot determine CWD: %v\n", err)
		os.Exit(1)
	}

	// Strip (param ...) declarations — sandbox-exec doesn't support them,
	// the same way the sed pipeline in pi-profile.sb usage comment works.
	paramDecl := regexp.MustCompile(`(?m)^\(param\s+.*\)\s*$\n?`)
	resolved := paramDecl.ReplaceAllString(profile, "")

	// Substitute (param "HOME") and (param "CWD") with actual values.
	resolved = strings.ReplaceAll(resolved, `(param "HOME")`, fmt.Sprintf("%q", home))
	resolved = strings.ReplaceAll(resolved, `(param "CWD")`, fmt.Sprintf("%q", cwd))

	// Locate the pi binary.
	piBin, err := exec.LookPath("pi")
	if err != nil {
		fmt.Fprintf(os.Stderr, "pi-sandbox: cannot find 'pi' in PATH: %v\n", err)
		os.Exit(1)
	}

	// Forward signals so pi can handle them (e.g. SIGINT for graceful shutdown).
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Open sandbox.log for appending violations.
	logFile, err := os.OpenFile("sandbox.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pi-sandbox: cannot open sandbox.log: %v\n", err)
		os.Exit(1)
	}
	defer logFile.Close()

	// Start tailing sandbox denial logs in the background.
	var logWg sync.WaitGroup
	logWg.Add(1)
	logCmd := exec.CommandContext(ctx, "log", "stream",
		"--style", "compact",
		"--predicate", logPredicate,
	)
	logPipe, err := logCmd.StdoutPipe()
	if err != nil {
		fmt.Fprintf(os.Stderr, "pi-sandbox: log stream pipe: %v\n", err)
		os.Exit(1)
	}
	logCmd.Stderr = os.Stderr
	if err := logCmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "pi-sandbox: log stream start: %v\n", err)
		os.Exit(1)
	}
	go func() {
		defer logWg.Done()
		scanner := bufio.NewScanner(logPipe)
		for scanner.Scan() {
			line := scanner.Text()
			// Skip the "Filtering the log data..." header line.
			if strings.HasPrefix(line, "Filtering") {
				continue
			}
			fmt.Fprintf(os.Stderr, "\033[33m[sandbox-deny]\033[0m %s\n", line)
			fmt.Fprintln(logFile, line)
		}
	}()

	// Run sandbox-exec as a child process so we can tail logs alongside it.
	args := []string{"-p", resolved, piBin}
	args = append(args, os.Args[1:]...)
	piCmd := exec.Command("sandbox-exec", args...)
	piCmd.Stdin = os.Stdin
	piCmd.Stdout = os.Stdout
	piCmd.Stderr = os.Stderr

	// Relay signals to the sandboxed child.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	go func() {
		for sig := range sigCh {
			if piCmd.Process != nil {
				_ = piCmd.Process.Signal(sig)
			}
		}
	}()

	exitCode := 0
	if err := piCmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			fmt.Fprintf(os.Stderr, "pi-sandbox: exec failed: %v\n", err)
			exitCode = 1
		}
	}

	// Stop log tailer and wait for it to drain.
	cancel()
	_ = logCmd.Wait()
	logWg.Wait()

	os.Exit(exitCode)
}
