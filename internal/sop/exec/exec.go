// Package exec supplies the executable NodeFactory the SOP compiler has been
// waiting for.
//
// The compiler owns the graph — what runs, in what order, how often it retries,
// what must be true before an edge is taken. This package owns the mechanics of
// being a node in that graph: emitting the routing event the scheduler reads,
// bounding loops the engine will not bound itself, and reporting the lifecycle
// a UI needs. What a stage actually *does* is a StageRunner, injected by the
// caller, so the graph can be exercised without a provider, a worktree, or a
// running agent.
//
// Three engine facts shape everything here, all verified against adk v2.2.0:
//
//   - Routing matches session.Event.Routes, never a node's return value. A body
//     that returns "PASS" sets Event.Output and matches no edge at all.
//   - A returned error does not take the on_fail edge. It marks the node failed,
//     applies RetryConfig, then fails the whole workflow. Failure that the SOP
//     models as a route must therefore be reported as a value, not an error.
//   - Nothing counts loop activations, so max_cycles is ours to enforce.
package exec

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/session"
	"google.golang.org/adk/v2/workflow"

	"github.com/dimetron/pi-go/internal/sop"
)

// StageRunner performs one stage's work.
//
// It returns the route the stage produced rather than an error, because the
// engine treats an error as fatal to the whole run: a gate that fails is a
// FAIL route, not a Go error. Reserve the error return for a stage that could
// not be attempted at all.
type StageRunner interface {
	RunStage(ctx context.Context, req StageRequest) (StageOutcome, error)
}

// StageRequest is what a runner is asked to do.
type StageRequest struct {
	Stage sop.Stage
	// Review is set when this activation is a stage's review checkpoint
	// rather than the stage body itself.
	Review *sop.Review
	// Input is the predecessor's output, or the workflow input for the entry
	// stage.
	Input any
	// Cycle is 1 on a stage's first activation and increments each time the
	// graph routes back to it.
	Cycle int
}

// StageOutcome is what a runner produced.
type StageOutcome struct {
	// Route is the value the scheduler matches against this stage's outgoing
	// edges: "PASS", "FAIL", sop.RecheckSignal. Empty means "take the forward
	// path", which the compiler wires as the Default route for any stage that
	// can also fail over.
	Route string
	// Output is handed to the successor as its input.
	Output any
}

// RunnerFunc adapts a function to StageRunner.
type RunnerFunc func(ctx context.Context, req StageRequest) (StageOutcome, error)

func (f RunnerFunc) RunStage(ctx context.Context, req StageRequest) (StageOutcome, error) {
	return f(ctx, req)
}

// Factory builds executable nodes. It implements sop.NodeFactory.
type Factory struct {
	Runner StageRunner

	mu sync.Mutex
	// cycles counts activations per (invocation, node) so a loop can be
	// bounded. It cannot live on the node or in a plain field: one factory
	// serves concurrent invocations, and the engine resets NodeState.Attempt
	// on success, so retries and loop activations are different things.
	cycles map[cycleKey]int
}

type cycleKey struct {
	invocation string
	node       string
}

// NewFactory returns a factory whose nodes run r.
func NewFactory(r StageRunner) *Factory {
	return &Factory{Runner: r, cycles: map[cycleKey]int{}}
}

// CycleBudgetError is returned when a stage is activated more times than its
// max_cycles allows. It is deliberately fatal: a loop that will not converge
// must stop the run rather than spin.
type CycleBudgetError struct {
	Stage string
	Max   int
}

func (e *CycleBudgetError) Error() string {
	return fmt.Sprintf("stage %q exceeded max_cycles (%d): the loop is not converging", e.Stage, e.Max)
}

func (f *Factory) AgentNode(s sop.Stage, cfg workflow.NodeConfig) (workflow.Node, error) {
	return f.node(s.ID, s, nil, cfg), nil
}

func (f *Factory) FunctionNode(s sop.Stage, cfg workflow.NodeConfig) (workflow.Node, error) {
	return f.node(s.ID, s, nil, cfg), nil
}

func (f *Factory) ReviewNode(s sop.Stage, r sop.Review, cfg workflow.NodeConfig) (workflow.Node, error) {
	review := r
	return f.node(s.ID+".review", s, &review, cfg), nil
}

// node builds one dynamic node. Dynamic rather than function-typed because the
// body needs `emit`: it publishes the lifecycle event a UI reads and the
// routing event the scheduler reads, and only a dynamic (or emitting) node can
// publish more than its return value.
func (f *Factory) node(name string, s sop.Stage, review *sop.Review, cfg workflow.NodeConfig) workflow.Node {
	body := func(ctx agent.Context, in any, emit func(*session.Event) error) (any, error) {
		cycle, err := f.enter(ctx, name, s)
		if err != nil {
			return nil, err
		}

		// Node start is not an engine event, so a UI cannot see a stage begin
		// unless the node says so. This is the sidebar's "running" signal.
		if err := emit(lifecycleEvent(ctx, name, cycle)); err != nil {
			return nil, err
		}

		out, err := f.Runner.RunStage(ctx, StageRequest{
			Stage: s, Review: review, Input: in, Cycle: cycle,
		})
		if err != nil {
			return nil, fmt.Errorf("stage %q: %w", name, err)
		}

		// The routing event. At most one per activation — a second is
		// ErrMultipleRoutingEvents and fails the node.
		ev := session.NewEvent(ctx, ctx.InvocationID())
		if out.Route != "" {
			ev.Routes = []string{out.Route}
		}
		ev.Output = out.Output
		if err := emit(ev); err != nil {
			return nil, err
		}

		// nil suppresses the node's own terminal event; the emitted one above
		// already carries the output and the route.
		return nil, nil
	}

	return workflow.NewDynamicNode[any, any](name, body, refuseToRetryBudget(cfg))
}

// refuseToRetryBudget stops the engine retrying a cycle-budget refusal.
//
// Without it a non-converging loop costs the SOP's whole retry policy before
// the run fails — for the run SOP, three attempts with a 10s initial delay and
// 2x backoff, so ~40 seconds of waiting to re-learn what the first refusal
// already established. The budget is deterministic per activation: retrying
// re-runs the same refusal.
func refuseToRetryBudget(cfg workflow.NodeConfig) workflow.NodeConfig {
	if cfg.RetryConfig == nil {
		return cfg
	}
	rc := *cfg.RetryConfig
	prev := rc.ShouldRetry
	rc.ShouldRetry = func(err error) bool {
		var budget *CycleBudgetError
		if errors.As(err, &budget) {
			return false
		}
		if prev != nil {
			return prev(err)
		}
		// Mirror the engine's default: input validation is deterministic per
		// activation too.
		return !errors.Is(err, workflow.ErrInputValidation)
	}
	cfg.RetryConfig = &rc
	return cfg
}

// enter records an activation and enforces the stage's cycle budget.
func (f *Factory) enter(ctx agent.Context, node string, s sop.Stage) (int, error) {
	key := cycleKey{invocation: ctx.InvocationID(), node: node}

	f.mu.Lock()
	f.cycles[key]++
	n := f.cycles[key]
	f.mu.Unlock()

	if s.MaxCycle > 0 && n > s.MaxCycle {
		return n, &CycleBudgetError{Stage: node, Max: s.MaxCycle}
	}
	return n, nil
}

// Forget releases the cycle counters for one invocation. A long-lived process
// that never calls it leaks one small entry per node per run.
func (f *Factory) Forget(invocationID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for k := range f.cycles {
		if k.invocation == invocationID {
			delete(f.cycles, k)
		}
	}
}

// LifecycleAuthor is the author stamped on the "stage started" events this
// package emits, so a consumer can tell them from model output.
const LifecycleAuthor = "sop.stage"

// lifecycleEvent announces that a stage has begun. It carries no Routes, so it
// is not a routing event and cannot be mistaken for one.
func lifecycleEvent(ctx agent.Context, node string, cycle int) *session.Event {
	ev := session.NewEvent(ctx, ctx.InvocationID())
	ev.Author = LifecycleAuthor
	ev.Branch = node
	if cycle > 1 {
		ev.Branch = fmt.Sprintf("%s#%d", node, cycle)
	}
	return ev
}

// StageStarted reports the stage a lifecycle event announces, and whether ev is
// one at all. A UI switches its "running" marker on this.
func StageStarted(ev *session.Event) (stage string, ok bool) {
	if ev == nil || ev.Author != LifecycleAuthor {
		return "", false
	}
	name := ev.Branch
	for i := range name {
		if name[i] == '#' {
			return name[:i], true
		}
	}
	return name, true
}
