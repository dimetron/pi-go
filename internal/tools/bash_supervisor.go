package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dimetron/pi-go/internal/procs"
)

const (
	// defaultIdleTimeout is how long a command may produce no output at all
	// before it is handed off to the background.
	//
	// The value is calibrated against real quiet commands rather than picked
	// round: a cold `go build ./...` on this repo prints nothing for ~28s, and
	// a cold test run is worse. A threshold near that would background ordinary
	// builds and cost a round trip every time. Ninety seconds clears them while
	// still reacting long before a hopeless command would otherwise sit there —
	// which matters most when the model sets a generous timeout, since the idle
	// check then fires minutes before the hard limit.
	//
	// Nothing is killed at the threshold, so erring low is cheap; erring low
	// enough to trip on every build is merely annoying.
	defaultIdleTimeout = 90 * time.Second

	// shortIdleTimeout is the threshold below which a caller-supplied
	// idle_timeout is treated as self-defeating and called out in the result.
	//
	// Nothing is overridden — a caller that genuinely wants a hair trigger
	// keeps it. But the heartbeat only samples every 5s, so any value below
	// that cannot fire sooner than 5s anyway, and values in that range
	// background every build in this repo on its first quiet moment.
	shortIdleTimeout = 15 * time.Second

	// heartbeatInterval is how often a running command reports that it is still
	// alive. It drives the "45s, no output" line in the UI, which is the whole
	// point of streaming — a stall you can see is not a hang.
	heartbeatInterval = 5 * time.Second

	// maxBackgroundProcs caps concurrently backgrounded commands. Handing off
	// instead of killing means nothing reaps a forgotten command, so the cap is
	// what stops a confused model from accumulating them without bound.
	maxBackgroundProcs = 8

	// backgroundMaxLifetime is the longest a backgrounded command may run
	// before it is killed outright. It is the last line of defense against the
	// exact leak this package exists to prevent.
	backgroundMaxLifetime = 30 * time.Minute
)

// OutputSink receives live activity from running shell commands.
//
// kind is one of "start", "output", "stderr", "heartbeat", "stall",
// "background" or "exit". Implementations must not block: the sink is called
// from the goroutines copying the command's pipes, and blocking there stalls
// the command itself. The TUI's implementation drops events when its channel
// is full, which is the correct trade — a dropped progress line costs nothing,
// a stalled pipe costs the command.
type OutputSink func(execID, kind, content string)

// BashSupervisor runs shell commands, streams their output while they run, and
// owns any command that outlives its foreground call.
//
// The unit of work is a process group, not a process. A shell command is a tree
// — pipelines, subshells, whatever the model wrote — and every operation here
// (cancel, timeout, kill) acts on the whole tree. That is the difference
// between a timeout that works and one that leaves orphans holding pipes.
type BashSupervisor struct {
	mu    sync.Mutex
	sink  OutputSink
	procs map[string]*bashProc
	seq   uint64

	// Tunables, overridable in tests.
	idleTimeout time.Duration
	heartbeat   time.Duration
	maxProcs    int
	maxLifetime time.Duration
}

// NewBashSupervisor returns a supervisor with no sink attached. Output is still
// captured and still streamed internally; it just goes nowhere visible until
// SetSink is called.
func NewBashSupervisor() *BashSupervisor {
	return &BashSupervisor{
		procs:       make(map[string]*bashProc),
		idleTimeout: defaultIdleTimeout,
		heartbeat:   heartbeatInterval,
		maxProcs:    maxBackgroundProcs,
		maxLifetime: backgroundMaxLifetime,
	}
}

// SetSink attaches a live-output destination. It may be called after tools are
// built, which matters because the UI channel the sink writes to is created
// later in startup than the tool registry.
func (s *BashSupervisor) SetSink(sink OutputSink) {
	s.mu.Lock()
	s.sink = sink
	s.mu.Unlock()
}

func (s *BashSupervisor) emit(id, kind, content string) {
	s.mu.Lock()
	sink := s.sink
	s.mu.Unlock()
	if sink != nil {
		sink(id, kind, content)
	}
}

func (s *BashSupervisor) nextID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	return fmt.Sprintf("bg_%d", s.seq)
}

// bashProc is one running (or finished) shell command.
type bashProc struct {
	sup     *BashSupervisor
	id      string
	command string
	dir     string
	cmd     *exec.Cmd
	cancel  context.CancelFunc

	stdout *stream
	stderr *stream

	started time.Time
	lastOut atomic.Int64 // UnixNano of the most recent byte of output

	// The resolved limits this command runs under, kept so a handoff or a later
	// poll can report them. They are set once before the process starts and
	// never written again, so no synchronization is needed.
	timeout     time.Duration
	idleTimeout time.Duration

	done     chan struct{} // closed once the process has been reaped
	exitCode int
	waitErr  error
	killed   atomic.Bool

	// Read cursors for incremental reads by the model. Guarded by curMu rather
	// than by the streams' own locks: they belong to the reader, not the writer.
	curMu  sync.Mutex
	outCur int64
	errCur int64
}

func (p *bashProc) markOutput() {
	p.lastOut.Store(time.Now().UnixNano())
}

func (p *bashProc) idleFor() time.Duration {
	last := p.lastOut.Load()
	if last == 0 {
		return time.Since(p.started)
	}
	return time.Since(time.Unix(0, last))
}

func (p *bashProc) running() bool {
	select {
	case <-p.done:
		return false
	default:
		return true
	}
}

// exitStatus returns the reaped exit code and whether the command has actually
// been reaped yet.
//
// exitCode is written by the reaping goroutine in start, which closes done
// immediately afterwards. Receiving from done is therefore what establishes
// happens-before for the read; reading the field without checking races with
// that write, and yields a zero value that reads as a clean "exit 0" for a
// command that has not exited at all.
func (p *bashProc) exitStatus() (code int, reaped bool) {
	select {
	case <-p.done:
		return p.exitCode, true
	default:
		return 0, false
	}
}

// runRequest is one call into the supervisor.
type runRequest struct {
	dir         string
	command     string
	timeout     time.Duration
	idleTimeout time.Duration
}

// Run executes command and blocks until it finishes, stalls, or exceeds its
// timeout.
//
// It returns without killing anything. A command that outruns either limit is
// handed to the background and reported with a handle, because the alternative
// — killing it — throws away work the model may still want and tells it nothing
// about why. The model then chooses: read more output, or kill it.
//
// Canceling ctx (the user pressing Esc, the turn ending) does kill the command
// and everything it spawned, but only while it is still in the foreground. Once
// handed off it is the supervisor's, and outlives the turn.
func (s *BashSupervisor) Run(ctx context.Context, req runRequest) (BashOutput, error) {
	if req.timeout <= 0 {
		req.timeout = defaultBashTimeout
	}
	if req.idleTimeout <= 0 {
		req.idleTimeout = s.idleTimeout
	}
	// Never stall out before the hard limit would have fired anyway; a caller
	// asking for a 10s timeout does not want a 30s idle check.
	if req.idleTimeout > req.timeout {
		req.idleTimeout = req.timeout
	}

	p, err := s.start(ctx, req)
	if err != nil {
		return BashOutput{}, err
	}

	return s.supervise(ctx, p, req)
}

// start launches the command and the goroutine that reaps it.
//
// The command's own context is detached from ctx on purpose. Foreground
// cancellation is handled explicitly in supervise, which stops watching once
// the command is handed off — a command that survives its turn must not be
// killed by that turn ending.
func (s *BashSupervisor) start(ctx context.Context, req runRequest) (*bashProc, error) {
	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))

	p := &bashProc{
		sup:         s,
		id:          s.nextID(),
		command:     req.command,
		dir:         req.dir,
		cancel:      cancel,
		stdout:      newStream(streamCap),
		stderr:      newStream(streamCap),
		started:     time.Now(),
		timeout:     req.timeout,
		idleTimeout: req.idleTimeout,
		done:        make(chan struct{}),
	}

	cmd := exec.CommandContext(runCtx, "bash", "-c", req.command)
	cmd.Dir = req.dir
	cmd.Stdout = &sinkWriter{proc: p, stream: p.stdout, kind: "output"}
	cmd.Stderr = &sinkWriter{proc: p, stream: p.stderr, kind: "stderr"}
	procs.Isolate(cmd)
	p.cmd = cmd

	s.emit(p.id, "start", req.command)

	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("starting command: %w", err)
	}

	go func() {
		p.waitErr = cmd.Wait()
		p.exitCode = exitCodeOf(p.waitErr)
		close(p.done)
	}()

	return p, nil
}

// supervise waits for the command on behalf of the caller, reporting progress
// and deciding what to do when it takes too long.
func (s *BashSupervisor) supervise(ctx context.Context, p *bashProc, req runRequest) (BashOutput, error) {
	deadline := time.NewTimer(req.timeout)
	defer deadline.Stop()
	tick := time.NewTicker(s.heartbeatInterval())
	defer tick.Stop()

	for {
		select {
		case <-p.done:
			return s.finish(p), nil

		case <-ctx.Done():
			// The turn was canceled while this command was still in the
			// foreground. Kill the whole group and let the caller report the
			// cancellation; nothing is backgrounded, because nobody is left to
			// ask about it.
			p.terminate()
			<-p.done
			return BashOutput{}, ctx.Err()

		case <-deadline.C:
			return s.background(p, fmt.Sprintf("still running after %s", roundDur(time.Since(p.started)))), nil

		case <-tick.C:
			idle := p.idleFor()
			s.emit(p.id, "heartbeat", fmt.Sprintf("%s elapsed, %s without output",
				roundDur(time.Since(p.started)), roundDur(idle)))
			if idle >= req.idleTimeout {
				s.emit(p.id, "stall", fmt.Sprintf("no output for %s", roundDur(idle)))
				return s.background(p, fmt.Sprintf("no output for %s", roundDur(idle))), nil
			}
		}
	}
}

func (s *BashSupervisor) heartbeatInterval() time.Duration {
	if s.heartbeat > 0 {
		return s.heartbeat
	}
	return heartbeatInterval
}

// finish builds the result for a command that ran to completion.
func (s *BashSupervisor) finish(p *bashProc) BashOutput {
	p.cancel()
	elapsed := roundDur(time.Since(p.started))
	s.emit(p.id, "exit", fmt.Sprintf("exit %d in %s", p.exitCode, elapsed))

	stdout := p.stdout.String()
	stderr := p.stderr.String()

	// SIGPIPE (exit 141 = signal 13 + 128) is benign — the consumer closed the
	// pipe before the producer finished writing. Treat it as success.
	exitCode := p.exitCode
	if exitCode == 141 {
		exitCode = 0
	}

	outStr, errStr := budgetStreams(stdout, stderr)
	out := BashOutput{
		Stdout:   outStr,
		Stderr:   errStr,
		ExitCode: exitCode,
		Note:     droppedNote(p.stdout.droppedBytes() + p.stderr.droppedBytes()),
	}
	// A failure to start at all (bad interpreter, missing cwd) surfaces through
	// waitErr rather than an exit status, and would otherwise read as a silent
	// success with no output.
	if p.waitErr != nil && exitCode == 0 {
		var exitErr *exec.ExitError
		if !errors.As(p.waitErr, &exitErr) {
			out.ExitCode = -1
			out.Stderr = strings.TrimSpace(out.Stderr + "\n" + p.waitErr.Error())
		}
	}
	return out
}

// background hands a still-running command to the supervisor and returns what
// it has produced so far.
//
// The reason string is written for the model, not for a log: it has to be
// specific enough to act on, since "no output for 30s" and "still running after
// 2m0s" call for different responses.
func (s *BashSupervisor) background(p *bashProc, reason string) BashOutput {
	s.register(p)
	s.emit(p.id, "background", reason)

	stdout, outCur, _ := p.stdout.since(0)
	stderr, errCur, _ := p.stderr.since(0)
	p.curMu.Lock()
	p.outCur, p.errCur = outCur, errCur
	p.curMu.Unlock()

	note := fmt.Sprintf(
		"Command %s and was moved to the background; it is still running. "+
			"Read more with bash_output(handle=%q) or stop it with bash_kill(handle=%q). "+
			"Output above is everything produced so far.",
		reason, p.id, p.id)
	if strings.TrimSpace(stdout) == "" && strings.TrimSpace(stderr) == "" {
		note += " It has produced no output at all — if that is unexpected, the command is probably too broad (a filesystem-wide scan, or a prompt waiting for input) and should be killed and narrowed."
	}
	note += " " + limitsHint(p.timeout, p.idleTimeout)

	outStr, errStr := budgetStreams(stdout, stderr)
	if d := droppedNote(p.stdout.droppedBytes() + p.stderr.droppedBytes()); d != "" {
		note += " " + d
	}

	return BashOutput{
		Stdout:      outStr,
		Stderr:      errStr,
		ExitCode:    -1,
		Running:     true,
		Handle:      p.id,
		Elapsed:     roundDur(time.Since(p.started)).String(),
		Idle:        roundDur(p.idleFor()).String(),
		Timeout:     p.timeout.String(),
		IdleTimeout: p.idleTimeout.String(),
		Note:        note,
	}
}

// limitsHint states the limits a command ran under and what to do about them.
//
// It exists because the handoff is otherwise a dead end for the caller: the
// note says the command went quiet, but not that the threshold it crossed was
// one the caller chose. A one-second idle_timeout backgrounds every build,
// lint, and test run in this repo on its first quiet moment, and the only
// visible consequence is a handle the model then polls dozens of times. Naming
// the limit turns that into a one-line fix.
func limitsHint(timeout, idle time.Duration) string {
	hint := fmt.Sprintf("Limits for this command: timeout %s, idle_timeout %s.",
		roundDur(timeout), roundDur(idle))
	if idle < shortIdleTimeout {
		hint += fmt.Sprintf(" An idle_timeout under %s backgrounds ordinary builds and"+
			" test runs the moment they go quiet — pass a larger idle_timeout"+
			" (or omit it for the %s default) rather than polling the handle.",
			roundDur(shortIdleTimeout), roundDur(defaultIdleTimeout))
	}
	return hint
}

// budgetStreams applies the standard cleanup to both streams.
//
// The byte cap here is a backstop only: the stream buffers are already bounded
// at the same size and drop from the front, so what arrives is at most one
// buffer's worth. What actually goes missing is reported by droppedNote.
func budgetStreams(stdout, stderr string) (string, string) {
	return redactSecrets(truncateOutput(stdout)),
		redactSecrets(truncateOutput(stripRuntimeNoise(stderr)))
}

// droppedNote describes output that aged out of the buffer, in terms the model
// can act on, or "" when nothing was lost.
//
// Without this the loss is invisible: the buffers keep the most recent 256KB
// and discard everything before it, so a command that printed a gigabyte
// returns a plausible-looking transcript that silently begins in the middle.
func droppedNote(dropped int64) string {
	if dropped <= 0 {
		return ""
	}
	return fmt.Sprintf(
		"%d earlier bytes were discarded to stay within the output buffer — what you see starts mid-stream, "+
			"and the beginning of this command's output is gone. Re-run with a narrower command "+
			"(grep, head, --quiet) if you need it.",
		dropped)
}

// register adds p to the background set, enforcing the cap and arming the
// lifetime backstop.
func (s *BashSupervisor) register(p *bashProc) {
	s.mu.Lock()
	if len(s.procs) >= s.capacity() {
		if victim := oldestLocked(s.procs); victim != nil {
			delete(s.procs, victim.id)
			// Kill outside the lock; terminate can block briefly on signaling.
			defer func() {
				victim.terminate()
				s.emit(victim.id, "exit", "killed: too many background commands")
			}()
		}
	}
	s.procs[p.id] = p
	lifetime := s.maxLifetime
	s.mu.Unlock()

	if lifetime <= 0 {
		lifetime = backgroundMaxLifetime
	}
	go s.reap(p, lifetime)
}

func (s *BashSupervisor) capacity() int {
	if s.maxProcs > 0 {
		return s.maxProcs
	}
	return maxBackgroundProcs
}

// reap kills a background command that overstays, and releases its slot when it
// exits on its own.
func (s *BashSupervisor) reap(p *bashProc, lifetime time.Duration) {
	timer := time.NewTimer(lifetime)
	defer timer.Stop()
	select {
	case <-p.done:
		p.cancel()
		s.emit(p.id, "exit", fmt.Sprintf("exit %d in %s", p.exitCode, roundDur(time.Since(p.started))))
	case <-timer.C:
		p.terminate()
		s.emit(p.id, "exit", fmt.Sprintf("killed after %s (background lifetime limit)", roundDur(lifetime)))
	}
}

// oldestLocked returns the longest-running entry, preferring one that has
// already exited — evicting a finished command costs nothing, evicting a live
// one destroys work.
func oldestLocked(m map[string]*bashProc) *bashProc {
	var oldestDone, oldestLive *bashProc
	for _, p := range m {
		if p.running() {
			if oldestLive == nil || p.started.Before(oldestLive.started) {
				oldestLive = p
			}
			continue
		}
		if oldestDone == nil || p.started.Before(oldestDone.started) {
			oldestDone = p
		}
	}
	if oldestDone != nil {
		return oldestDone
	}
	return oldestLive
}

// terminate stops the command and everything it spawned, and does not return
// until it has been reaped or the wait delay has elapsed.
func (p *bashProc) terminate() {
	p.killed.Store(true)
	_ = procs.Kill(p.cmd)
	// cancel() also runs the exec cancellation path, which is what enforces
	// WaitDelay and force-closes pipes a grandchild is still holding.
	p.cancel()
}

func (s *BashSupervisor) lookup(handle string) (*bashProc, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.procs[handle]
	return p, ok
}

func (s *BashSupervisor) forget(handle string) {
	s.mu.Lock()
	delete(s.procs, handle)
	s.mu.Unlock()
}

// Handles lists the background commands the supervisor currently knows about,
// oldest first.
func (s *BashSupervisor) Handles() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.procs))
	for id := range s.procs {
		out = append(out, id)
	}
	slices.Sort(out)
	return out
}

// KillAll stops every background command. Call it when the session ends: a
// backgrounded command has no other owner, and leaving it running is precisely
// the leak this supervisor exists to prevent.
func (s *BashSupervisor) KillAll() {
	s.mu.Lock()
	all := make([]*bashProc, 0, len(s.procs))
	for _, p := range s.procs {
		all = append(all, p)
	}
	s.procs = make(map[string]*bashProc)
	s.mu.Unlock()

	for _, p := range all {
		p.terminate()
	}
}

// readOutput returns everything produced since the caller's last read.
//
// A finished command is forgotten once its exit status has been reported, so a
// handle is good for exactly one final read. Holding finished commands
// indefinitely would turn the cap into a queue of corpses.
func (s *BashSupervisor) readOutput(handle string, wait time.Duration) (BashStatus, error) {
	p, ok := s.lookup(handle)
	if !ok {
		return BashStatus{}, fmt.Errorf("unknown handle %q (running: %s)", handle, strings.Join(s.Handles(), ", "))
	}

	if wait > 0 {
		p.awaitChange(wait)
	}

	p.curMu.Lock()
	outStr, outNext, outDropped := p.stdout.since(p.outCur)
	errStr, errNext, errDropped := p.stderr.since(p.errCur)
	p.outCur, p.errCur = outNext, errNext
	p.curMu.Unlock()

	budgetedOut, budgetedErr := budgetStreams(outStr, errStr)
	st := BashStatus{
		Handle:      handle,
		Command:     p.command,
		Running:     p.running(),
		Stdout:      budgetedOut,
		Stderr:      budgetedErr,
		Elapsed:     roundDur(time.Since(p.started)).String(),
		Timeout:     p.timeout.String(),
		IdleTimeout: p.idleTimeout.String(),
	}
	// Here the drop is measured against this reader's own cursor, not the whole
	// stream: it counts output this caller had not yet collected when the buffer
	// aged it out, which is the loss it can actually do something about.
	if dropped := outDropped + errDropped; dropped > 0 {
		st.Note = droppedNote(dropped)
	}
	if st.Running {
		st.Idle = roundDur(p.idleFor()).String()
		if outStr == "" && errStr == "" {
			st.Note = strings.TrimSpace(st.Note + " No new output since the last read.")
		}
		return st, nil
	}

	st.ExitCode = p.exitCode
	if p.killed.Load() {
		st.Note = strings.TrimSpace(st.Note + " The command was killed.")
	}
	s.forget(handle)
	return st, nil
}

// awaitChange blocks until the command produces output, exits, or the wait
// elapses. Waiting on the streams' notify channels means a poll costs one
// round trip instead of a spin.
func (p *bashProc) awaitChange(wait time.Duration) {
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-p.done:
	case <-p.stdout.wait():
	case <-p.stderr.wait():
	case <-timer.C:
	}
}

// killHandle stops a background command and returns its final state.
func (s *BashSupervisor) killHandle(handle string) (BashStatus, error) {
	p, ok := s.lookup(handle)
	if !ok {
		return BashStatus{}, fmt.Errorf("unknown handle %q (running: %s)", handle, strings.Join(s.Handles(), ", "))
	}
	p.terminate()

	// Give the reaper a moment so the reported exit status is the real one
	// rather than "still running".
	select {
	case <-p.done:
	case <-time.After(procs.DefaultWaitDelay):
	}

	p.curMu.Lock()
	outStr, outNext, _ := p.stdout.since(p.outCur)
	errStr, errNext, _ := p.stderr.since(p.errCur)
	p.outCur, p.errCur = outNext, errNext
	p.curMu.Unlock()

	exitCode, reaped := p.exitStatus()
	budgetedOut, budgetedErr := budgetStreams(outStr, errStr)
	st := BashStatus{
		Handle:   handle,
		Command:  p.command,
		Running:  false,
		ExitCode: exitCode,
		Stdout:   budgetedOut,
		Stderr:   budgetedErr,
		Elapsed:  roundDur(time.Since(p.started)).String(),
		Note:     "Killed, along with every process it spawned.",
	}
	// The wait delay and the exec package's own WaitDelay are the same
	// duration, so losing that race is expected rather than exotic: a
	// descendant still holding a pipe keeps Wait blocked for exactly as long
	// as we are willing to wait for it. Say so instead of reporting the zero
	// value as a clean exit.
	if !reaped {
		st.ExitCode = -1
		st.Note = "Killed, along with every process it spawned. It had not been reaped " +
			roundDur(procs.DefaultWaitDelay).String() + " after the signal, so no exit status is available."
	}
	s.forget(handle)
	return st, nil
}

// sinkWriter fans one output channel into the retained buffer, the idle clock,
// and the live sink.
//
// It splits on newlines before emitting because the sink feeds a UI that
// renders one event per row: forwarding raw pipe chunks would put half-lines in
// the display and split words across rows at arbitrary boundaries.
type sinkWriter struct {
	proc    *bashProc
	stream  *stream
	kind    string
	partial []byte
}

func (w *sinkWriter) Write(b []byte) (int, error) {
	n, _ := w.stream.Write(b)
	w.proc.markOutput()

	w.partial = append(w.partial, b...)
	for {
		i := bytes.IndexByte(w.partial, '\n')
		if i < 0 {
			break
		}
		line := strings.TrimRight(string(w.partial[:i]), "\r")
		w.partial = w.partial[i+1:]
		if strings.TrimSpace(line) != "" {
			w.proc.sup.emit(w.proc.id, w.kind, line)
		}
	}
	// Don't let a command that never emits a newline grow this without bound.
	if len(w.partial) > 8192 {
		w.proc.sup.emit(w.proc.id, w.kind, string(w.partial))
		w.partial = w.partial[:0]
	}
	return n, nil
}

func exitCodeOf(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}

// roundDur trims durations to something a human reads at a glance: "1m30s",
// not "1m30.004283ms".
func roundDur(d time.Duration) time.Duration {
	switch {
	case d >= time.Minute:
		return d.Round(time.Second)
	case d >= time.Second:
		return d.Round(100 * time.Millisecond)
	default:
		return d.Round(time.Millisecond)
	}
}
