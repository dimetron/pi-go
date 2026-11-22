package palace

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"
)

// --- palace-kg-rules ---

// KGRulesToolInput defines parameters for the palace-kg-rules tool.
type KGRulesToolInput struct {
	// store (save a named program), solve (answer a goal), check (report
	// constraint violations), or materialize (write a stored program's
	// conclusions as derived facts).
	Action string `json:"action"`
	// The program name (store, materialize; or to solve/check a stored one).
	Name string `json:"name,omitempty"`
	// The rules text (store; or inline for solve/check).
	Program string `json:"program,omitempty"`
	// datalog (default), n3, or elpi (solve only: clauses like "reports X Y :- tm.triple X manager Y.").
	Syntax string `json:"syntax,omitempty"`
	// solve: the goal, e.g. reports(alice, Y).
	Goal string `json:"goal,omitempty"`
	// materialize: preview without writing.
	DryRun bool `json:"dry_run,omitempty"`
	// Only facts in this named graph (solve, check, materialize).
	Graph string `json:"graph,omitempty"`
}

// KGRulesToolOutput contains the result.
type KGRulesToolOutput struct {
	Content string `json:"content"`
}

func newPalaceKGRulesTool(p *Palace) (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name: "palace-kg-rules",
		Description: "Reason over the knowledge graph with rules (Datalog, or N3 with syntax=n3). Example program: " +
			"'reports(X, Y) :- manager(X, Y). reports(X, Z) :- manager(X, Y), reports(Y, Z).' and a constraint " +
			"':- age(X, A), A < 0.'. Each stored predicate p(S, O) reads facts (S p O). solve answers a goal; " +
			"check lists constraint violations with the facts behind them; store saves a named program; " +
			"materialize writes its conclusions as derived facts that are withdrawn automatically when a premise " +
			"or the program changes.",
	}, func(ctx agent.Context, input KGRulesToolInput) (KGRulesToolOutput, error) {
		return palaceKGRulesHandler(ctx, p, input)
	})
}

func rulesErr(err error) (KGRulesToolOutput, error) {
	return KGRulesToolOutput{Content: fmt.Sprintf("Error: %v", err)}, nil
}

func palaceKGRulesHandler(ctx context.Context, p *Palace, input KGRulesToolInput) (KGRulesToolOutput, error) {
	kg := p.KG()
	syntax := input.Syntax
	if syntax == "" {
		syntax = "datalog"
	}
	v := kg.Now()
	if input.Graph != "" {
		v = v.InGraph(input.Graph)
	}
	author, session := txMeta(ctx)
	program := func() (*Program, error) {
		if input.Program != "" {
			return ParseRules(syntax, input.Program)
		}
		if input.Name == "" {
			return nil, fmt.Errorf("program or name is required")
		}
		prog, _, err := kg.LoadProgram(ctx, input.Name)
		return prog, err
	}
	switch input.Action {
	case "store":
		if input.Name == "" || input.Program == "" {
			return KGRulesToolOutput{Content: "Error: name and program are required"}, nil
		}
		var eid int64
		_, err := kg.Transact(ctx, TxOptions{Author: author, Source: session, Reason: "store rules"}, func(w *KGTx) error {
			var err error
			eid, err = w.StoreProgram(input.Name, syntax, input.Program)
			return err
		})
		if err != nil {
			return rulesErr(err)
		}
		return KGRulesToolOutput{Content: fmt.Sprintf("Stored program %s as e%d.", input.Name, eid)}, nil
	case "solve":
		if input.Goal == "" {
			return KGRulesToolOutput{Content: "Error: goal is required"}, nil
		}
		if syntax == "elpi" {
			return elpiSolve(ctx, v, input)
		}
		prog, err := program()
		if err != nil {
			return rulesErr(err)
		}
		sols, err := v.Solve(ctx, prog, input.Goal, RuleOptions{})
		if err != nil {
			return rulesErr(err)
		}
		if len(sols) == 0 {
			return KGRulesToolOutput{Content: "No solutions."}, nil
		}
		var sb strings.Builder
		fmt.Fprintf(&sb, "%d solution(s):\n", len(sols))
		for _, s := range sols {
			fmt.Fprintf(&sb, "- %s (from %s)\n", formatBindings(s.Bindings), formatEIDs(s.Support))
		}
		return KGRulesToolOutput{Content: sb.String()}, nil
	case "check":
		prog, err := program()
		if err != nil {
			return rulesErr(err)
		}
		vs, err := v.Check(ctx, prog, RuleOptions{})
		if err != nil {
			return rulesErr(err)
		}
		if len(vs) == 0 {
			return KGRulesToolOutput{Content: "No constraint violations."}, nil
		}
		var sb strings.Builder
		fmt.Fprintf(&sb, "%d violation(s):\n", len(vs))
		for _, x := range vs {
			fmt.Fprintf(&sb, "- %s: %s (facts %s)\n", x.Rule, formatBindings(x.Bindings), formatEIDs(x.Witnesses))
		}
		return KGRulesToolOutput{Content: sb.String()}, nil
	case "materialize":
		if input.Name == "" {
			return KGRulesToolOutput{Content: "Error: name of a stored program is required"}, nil
		}
		rep, err := kg.Materialize(ctx, TxOptions{Author: author, Source: session, Reason: "materialize " + input.Name,
			DryRun: input.DryRun}, input.Name, v, RuleOptions{})
		if err != nil {
			return rulesErr(err)
		}
		head := "Materialized"
		if input.DryRun {
			head = "Dry run, nothing written. Would materialize"
		}
		return KGRulesToolOutput{Content: fmt.Sprintf("%s %s: %d new, %d already derived, %d widened, %d withdrawn.",
			head, input.Name, len(rep.Derived), len(rep.Existing), len(rep.Widened), len(rep.Reconciled))}, nil
	}
	return KGRulesToolOutput{Content: "Error: action must be store, solve, check or materialize"}, nil
}

func formatBindings(b map[string]Value) string {
	keys := make([]string, 0, len(b))
	for k := range b {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k + "=" + b[k].String()
	}
	return strings.Join(parts, ", ")
}

func formatEIDs(eids []int64) string {
	parts := make([]string, len(eids))
	for i, e := range eids {
		parts[i] = fmt.Sprintf("e%d", e)
	}
	return strings.Join(parts, " ")
}

func elpiSolve(ctx context.Context, v View, input KGRulesToolInput) (KGRulesToolOutput, error) {
	prog, err := ParseElpi(input.Program)
	if err != nil {
		return rulesErr(err)
	}
	res, err := v.Elpi(ctx, prog, input.Goal, ElpiOptions{})
	if err != nil {
		return rulesErr(err)
	}
	if len(res.Rows) == 0 {
		return KGRulesToolOutput{Content: "No solutions."}, nil
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "%d solution(s), %d steps:\n", len(res.Rows), res.FuelUsed)
	for _, r := range res.Rows {
		fmt.Fprintf(&sb, "- %s\n", formatBindings(r))
	}
	return KGRulesToolOutput{Content: sb.String()}, nil
}
