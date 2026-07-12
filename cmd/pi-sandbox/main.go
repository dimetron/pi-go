package main

import (
	"bufio"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
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

// paramDecl matches the "(param ...)" declarations in pi-profile.sb.
// sandbox-exec does not accept them, so they are stripped before use — the same
// thing the sed pipeline in the profile's usage comment does.
var paramDecl = regexp.MustCompile(`(?m)^\(param\s+.*\)\s*$\n?`)

// resolveProfile turns the embedded profile into one sandbox-exec will accept:
// the (param ...) declarations are removed and the (param "HOME") / (param
// "CWD") references are replaced with quoted literals.
func resolveProfile(profile, home, cwd string) string {
	resolved := paramDecl.ReplaceAllString(profile, "")
	resolved = strings.ReplaceAll(resolved, `(param "HOME")`, fmt.Sprintf("%q", home))
	resolved = strings.ReplaceAll(resolved, `(param "CWD")`, fmt.Sprintf("%q", cwd))
	return resolved
}

// isNoiseLogLine reports whether a line from `log stream` is the header chatter
// rather than an actual sandbox denial.
func isNoiseLogLine(line string) bool {
	return strings.HasPrefix(line, "Filtering")
}

// exitCodeFor maps the error from running the sandboxed child to a process exit
// code: a clean run is 0, a child that exited non-zero forwards its own code,
// and a failure to exec at all is 1.
func exitCodeFor(err error, stderr io.Writer) int {
	if err == nil {
		return 0
	}
	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
		return exitErr.ExitCode()
	}
	fmt.Fprintf(stderr, "pi-sandbox: exec failed: %v\n", err)
	return 1
}

// config is everything run needs from the outside world. The command names are
// injected so the launcher can be driven with stubs in tests instead of
// actually spawning sandbox-exec and the macOS log stream.
type config struct {
	profile    string
	piName     string   // binary to look up on PATH ("pi")
	sandboxCmd string   // "sandbox-exec"
	logCmd     []string // macOS log stream command
	logPath    string   // where denials are appended
	args       []string // args forwarded to the sandboxed binary
	stdin      io.Reader
	stdout     io.Writer
	stderr     io.Writer
}

func defaultConfig() config {
	return config{
		profile:    profile,
		piName:     "pi",
		sandboxCmd: "sandbox-exec",
		logCmd:     []string{"log", "stream", "--style", "compact", "--predicate", logPredicate},
		logPath:    "sandbox.log",
		args:       os.Args[1:],
		stdin:      os.Stdin,
		stdout:     os.Stdout,
		stderr:     os.Stderr,
	}
}

func main() {
	os.Exit(run(context.Background(), defaultConfig()))
}

// run launches the sandboxed binary and tails sandbox denials alongside it,
// returning the exit code.
func run(ctx context.Context, cfg config) int {
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(cfg.stderr, "pi-sandbox: cannot determine HOME: %v\n", err)
		return 1
	}
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(cfg.stderr, "pi-sandbox: cannot determine CWD: %v\n", err)
		return 1
	}

	resolved := resolveProfile(cfg.profile, home, cwd)

	piBin, err := exec.LookPath(cfg.piName)
	if err != nil {
		fmt.Fprintf(cfg.stderr, "pi-sandbox: cannot find %q in PATH: %v\n", cfg.piName, err)
		return 1
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	logFile, err := os.OpenFile(cfg.logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		fmt.Fprintf(cfg.stderr, "pi-sandbox: cannot open %s: %v\n", cfg.logPath, err)
		return 1
	}
	defer logFile.Close()

	// Tail sandbox denial logs in the background.
	var logWg sync.WaitGroup
	logCmd := exec.CommandContext(ctx, cfg.logCmd[0], cfg.logCmd[1:]...)
	logPipe, err := logCmd.StdoutPipe()
	if err != nil {
		fmt.Fprintf(cfg.stderr, "pi-sandbox: log stream pipe: %v\n", err)
		return 1
	}
	logCmd.Stderr = cfg.stderr
	if err := logCmd.Start(); err != nil {
		fmt.Fprintf(cfg.stderr, "pi-sandbox: log stream start: %v\n", err)
		return 1
	}
	logWg.Add(1)
	go func() {
		defer logWg.Done()
		scanner := bufio.NewScanner(logPipe)
		for scanner.Scan() {
			line := scanner.Text()
			if isNoiseLogLine(line) {
				continue
			}
			fmt.Fprintf(cfg.stderr, "\033[33m[sandbox-deny]\033[0m %s\n", line)
			fmt.Fprintln(logFile, line)
		}
	}()

	// Run the sandboxed child so we can tail logs alongside it.
	args := append([]string{"-p", resolved, piBin}, cfg.args...)
	piCmd := exec.Command(cfg.sandboxCmd, args...)
	piCmd.Stdin = cfg.stdin
	piCmd.Stdout = cfg.stdout
	piCmd.Stderr = cfg.stderr

	// Relay signals to the sandboxed child.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(sigCh)
	go func() {
		for sig := range sigCh {
			if piCmd.Process != nil {
				_ = piCmd.Process.Signal(sig)
			}
		}
	}()

	exitCode := exitCodeFor(piCmd.Run(), cfg.stderr)

	// Stop the log tailer and let it drain.
	cancel()
	_ = logCmd.Wait()
	logWg.Wait()

	return exitCode
}
