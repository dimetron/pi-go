package sop

import (
	"fmt"
	"time"

	"google.golang.org/adk/v2/workflow"
)

// RecheckSignal is the output a looping stage emits to take its loop_back
// edge. A repair stage that fixes nothing does not emit it, and the loop ends.
const RecheckSignal = "RECHECK"

// NodeFactory builds the executable body for one stage.
//
// The compiler owns the graph — what runs, in what order, how often it retries,
// and what must be true before an edge is taken. The factory owns what a stage
// actually does. Splitting them is the point: the scheduling that used to live
// in a prompt ("Follow these phases in order", "on FAIL dispatch fix workers")
// becomes edges the scheduler walks, while the part an LLM should decide stays
// in the node body.
type NodeFactory interface {
	// AgentNode builds a node whose body is an LLM turn.
	AgentNode(stage Stage, cfg workflow.NodeConfig) (workflow.Node, error)
	// FunctionNode builds a deterministic node: validators, gates, git.
	FunctionNode(stage Stage, cfg workflow.NodeConfig) (workflow.Node, error)
	// ReviewNode builds a checkpoint — a human approval or an agent verdict.
	ReviewNode(stage Stage, review Review, cfg workflow.NodeConfig) (workflow.Node, error)
}

// Compiled is a SOP definition turned into a runnable graph.
type Compiled struct {
	Definition *Definition
	Edges      []workflow.Edge
	// Nodes is indexed by stage id, and by "<id>.review" for a stage's
	// review checkpoint.
	Nodes map[string]workflow.Node
	Order []string
}

// Compile turns a definition into workflow edges using factory for the node
// bodies. It lints first: compiling a definition with a dangling edge would
// produce a graph that silently stops early.
func Compile(def *Definition, factory NodeFactory) (*Compiled, error) {
	if findings := LintDefinition(def); !findings.OK() {
		return nil, fmt.Errorf("SOP %q does not lint:\n%s", def.SOP, findings.Format())
	}

	c := &Compiled{Definition: def, Nodes: map[string]workflow.Node{}}
	stages := def.AllStages()

	for _, s := range stages {
		if err := c.buildStage(s, def, factory); err != nil {
			return nil, err
		}
	}
	if err := c.wire(stages, def); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Compiled) buildStage(s Stage, def *Definition, factory NodeFactory) error {
	cfg := NodeConfigFor(s, def.Defaults)

	var (
		node workflow.Node
		err  error
	)
	if s.EffectiveKind() == "agent" {
		node, err = factory.AgentNode(s, cfg)
	} else {
		node, err = factory.FunctionNode(s, cfg)
	}
	if err != nil {
		return fmt.Errorf("stage %q: %w", s.ID, err)
	}
	if node == nil {
		return fmt.Errorf("stage %q: factory returned no node", s.ID)
	}
	c.Nodes[s.ID] = node
	c.Order = append(c.Order, s.ID)

	if s.Review == nil {
		return nil
	}
	// A review is its own node so the verdict can route an edge. Folding it
	// into the producing stage is what made the prose Verifier advisory.
	rcfg := NodeConfigFor(Stage{ID: s.ID + ".review", Timeout: s.Timeout}, def.Defaults)
	rnode, err := factory.ReviewNode(s, *s.Review, rcfg)
	if err != nil {
		return fmt.Errorf("stage %q review: %w", s.ID, err)
	}
	if rnode != nil {
		c.Nodes[s.ID+".review"] = rnode
		c.Order = append(c.Order, s.ID+".review")
	}
	return nil
}

// wire builds the edges. A stage with a review runs stage → review → successor,
// so the checkpoint sits on the path rather than beside it.
func (c *Compiled) wire(stages []Stage, def *Definition) error {
	b := workflow.NewEdgeBuilder()

	// The engine needs an explicit entry edge. The first stage is the entry
	// point: preflight when the SOP declares one, so a spec that cannot
	// succeed is rejected before any worktree is created.
	if len(stages) > 0 {
		b.Add(workflow.Start, c.Nodes[stages[0].ID])
	}

	for _, s := range stages {
		from := c.Nodes[s.ID]
		if r, ok := c.Nodes[s.ID+".review"]; ok {
			b.Add(from, r)
			from = r
		}

		switch {
		case len(s.Routes) > 0:
			routes := make(map[string]workflow.Node, len(s.Routes))
			for verdict, target := range s.Routes {
				to, ok := c.Nodes[target]
				if !ok {
					return fmt.Errorf("stage %q routes %q to unknown stage %q", s.ID, verdict, target)
				}
				routes[verdict] = to
			}
			b.AddRoutes(from, routes)
		case s.LoopBack != "":
			to, ok := c.Nodes[s.LoopBack]
			if !ok {
				return fmt.Errorf("stage %q loops back to unknown stage %q", s.ID, s.LoopBack)
			}
			// A loop-back is conditional, not a barrier: the target already has
			// an unconditional predecessor on the forward path, and a JoinNode
			// there would wait for the repair branch on every first pass. The
			// stage emits RecheckSignal when it has something to re-verify.
			b.AddRoute(from, to, workflow.StringRoute(RecheckSignal))
		case s.Next != "":
			to, ok := c.Nodes[s.Next]
			if !ok {
				return fmt.Errorf("stage %q advances to unknown stage %q", s.ID, s.Next)
			}
			b.Add(from, to)
		}

		// A failure path that names a stage is an edge too: it is how a gate
		// failure reaches the repair stage without the coordinator deciding to.
		if target := s.OnFail; target != "" && target != "abort" && target != "retry" {
			to, ok := c.Nodes[target]
			if !ok {
				return fmt.Errorf("stage %q fails over to unknown stage %q", s.ID, target)
			}
			b.AddRoute(from, to, workflow.StringRoute("FAIL"))
		}
	}

	c.Edges = b.Build()
	if len(c.Edges) == 0 && len(stages) > 1 {
		return fmt.Errorf("SOP %q compiled to no edges: no stage declares next, routes or loop_back", def.SOP)
	}
	return nil
}

// NodeConfigFor derives a node's runtime configuration from its stage and the
// definition's defaults.
//
// This is where the SOP's declared bounds become the engine's: a per-stage
// timeout instead of one blanket 60-minute spawn timeout, and retry with
// backoff and jitter instead of a counter that re-sends the whole briefing.
func NodeConfigFor(s Stage, defaults Defaults) workflow.NodeConfig {
	cfg := workflow.NodeConfig{}

	timeout := s.Timeout.Duration()
	if timeout == 0 {
		timeout = defaults.Timeout.Duration()
	}
	cfg.Timeout = timeout

	retry := s.Retry
	if retry == nil {
		retry = defaults.Retry
	}
	if retry != nil {
		cfg.RetryConfig = retryConfig(*retry)
	}

	if s.FanOut != nil {
		cfg.ParallelWorker = true
	}
	return cfg
}

// retryConfig maps the SOP's retry block onto the engine's, starting from the
// engine defaults so an unset field keeps a sensible value rather than zero.
func retryConfig(r Retry) *workflow.RetryConfig {
	cfg := workflow.DefaultRetryConfig()
	if r.MaxAttempts > 0 {
		cfg.MaxAttempts = r.MaxAttempts
	}
	if d := r.InitialDelay.Duration(); d > 0 {
		cfg.InitialDelay = d
	}
	if d := r.MaxDelay.Duration(); d > 0 {
		cfg.MaxDelay = d
	}
	if r.Backoff > 0 {
		cfg.BackoffFactor = r.Backoff
	}
	if r.Jitter > 0 {
		cfg.Jitter = r.Jitter
	}
	return cfg
}

// Workflow assembles the compiled edges into a runnable workflow.
func (c *Compiled) Workflow() (*workflow.Workflow, error) {
	opts := []workflow.Option{}
	if n := c.Definition.Defaults.MaxConcurrency; n > 0 {
		opts = append(opts, workflow.WithMaxConcurrency(n))
	}
	wf, err := workflow.New(c.Definition.SOP, c.Edges, opts...)
	if err != nil {
		return nil, fmt.Errorf("building workflow %q: %w", c.Definition.SOP, err)
	}
	return wf, nil
}

// Timeouts reports each stage's effective timeout, for inspection and tests.
func (c *Compiled) Timeouts() map[string]time.Duration {
	out := map[string]time.Duration{}
	for _, s := range c.Definition.AllStages() {
		out[s.ID] = NodeConfigFor(s, c.Definition.Defaults).Timeout
	}
	return out
}
