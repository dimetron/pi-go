package sop

import (
	"fmt"
	"iter"
	"sort"
	"strings"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/session"
	"google.golang.org/adk/v2/workflow"
)

// DescribeFactory builds a graph whose nodes carry each stage's identity and
// configuration but do no work.
//
// It exists so a SOP can be compiled, linted and inspected without a provider,
// a worktree or a running agent — which is what makes the definitions testable
// and what lets `pi` report the shape of a SOP before executing one. Phase 3
// supplies the factory that runs real agents; the graph it produces is the
// same graph.
type DescribeFactory struct{}

func (DescribeFactory) AgentNode(s Stage, cfg workflow.NodeConfig) (workflow.Node, error) {
	return newDescribeNode(s.ID, describeStage(s, "agent"), cfg), nil
}

func (DescribeFactory) FunctionNode(s Stage, cfg workflow.NodeConfig) (workflow.Node, error) {
	return newDescribeNode(s.ID, describeStage(s, "function"), cfg), nil
}

func (DescribeFactory) ReviewNode(s Stage, r Review, cfg workflow.NodeConfig) (workflow.Node, error) {
	what := "human approval: " + r.Prompt
	if r.Kind == "agent" {
		what = "agent review by " + r.Agent
	}
	return newDescribeNode(s.ID+".review", what, cfg), nil
}

// describeStage renders one stage in a line, naming the things that used to be
// prose the model could decline: what it dispatches to, what it must produce,
// and what gate proves it.
func describeStage(s Stage, kind string) string {
	var parts []string
	parts = append(parts, kind)
	if a := s.AgentName(); a != "" {
		parts = append(parts, "agent="+a)
	}
	if s.FanOut != nil {
		parts = append(parts, fmt.Sprintf("fan_out over %s (max %d)", s.FanOut.Over, s.FanOut.MaxConcurrency))
	}
	for _, p := range s.Produces {
		parts = append(parts, "produces "+p.Path+" ["+strings.Join(p.Validate, ", ")+"]")
	}
	if s.Gate != "" {
		parts = append(parts, "gate="+s.Gate)
	}
	if s.Description != "" {
		parts = append(parts, s.Description)
	}
	return strings.Join(parts, "; ")
}

// describeNode is a workflow.Node that reports itself and completes.
type describeNode struct {
	workflow.BaseNode
}

func newDescribeNode(name, description string, cfg workflow.NodeConfig) workflow.Node {
	return &describeNode{BaseNode: workflow.NewBaseNode(name, description, cfg)}
}

func (n *describeNode) Run(ctx agent.Context, input any) iter.Seq2[*session.Event, error] {
	return func(yield func(*session.Event, error) bool) {
		yield(nil, fmt.Errorf("node %q is a description only; compile with a real NodeFactory to execute", n.Name()))
	}
}

// Describe renders a compiled SOP as text: the stages in order, their bounds,
// and the edges between them.
func (c *Compiled) Describe() string {
	var b strings.Builder
	fmt.Fprintf(&b, "SOP %q v%d — %s\n", c.Definition.SOP, c.Definition.Version, c.Definition.Description)
	fmt.Fprintf(&b, "workspace: worktree=%s branch=%s merge_on=%s cleanup=%s\n",
		c.Definition.Workspace.Worktree, c.Definition.Workspace.Branch,
		c.Definition.Workspace.MergeOn, c.Definition.Workspace.Cleanup)
	fmt.Fprintf(&b, "max concurrency: %d\n\n", c.Definition.Defaults.MaxConcurrency)

	b.WriteString("Stages:\n")
	timeouts := c.Timeouts()
	for _, s := range c.Definition.AllStages() {
		fmt.Fprintf(&b, "  %-14s %-9s timeout=%s", s.ID, s.EffectiveKind(), timeouts[s.ID])
		cfg := NodeConfigFor(s, c.Definition.Defaults)
		if cfg.RetryConfig != nil {
			fmt.Fprintf(&b, " retry=%dx", cfg.RetryConfig.MaxAttempts)
		}
		if s.FanOut != nil {
			fmt.Fprintf(&b, " fan_out=%s", s.FanOut.Over)
		}
		b.WriteString("\n")
	}

	b.WriteString("\nEdges:\n")
	lines := make([]string, 0, len(c.Edges))
	for _, e := range c.Edges {
		label := ""
		if e.Route != nil {
			label = fmt.Sprintf(" [%v]", e.Route)
		}
		lines = append(lines, fmt.Sprintf("  %s -> %s%s", e.From.Name(), e.To.Name(), label))
	}
	sort.Strings(lines)
	b.WriteString(strings.Join(lines, "\n"))
	b.WriteString("\n")
	return b.String()
}
