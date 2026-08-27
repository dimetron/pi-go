package sop

import (
	"fmt"
	"sort"
	"strings"

	"github.com/dimetron/pi-go/internal/sop/validate"
	"github.com/dimetron/pi-go/internal/subagent"
)

// LintDefinition statically checks a SOP definition before anything runs.
//
// The checks that matter most are the ones the prose SOPs could only ask for:
// a stage that dispatches to a [worktree] agent produces edits in a nested
// worktree that is never merged, so the slice silently produces nothing. The
// prose warns about this in two places, in bold. Here it is a compile error.
func LintDefinition(def *Definition) validate.Findings {
	var out validate.Findings
	add := func(rule, msg, fix string) {
		out = append(out, validate.Finding{
			Artifact: def.SOP + ".sop.yaml", Rule: rule,
			Severity: validate.SeverityError, Message: msg, Fix: fix,
		})
	}

	if def.SOP == "" {
		add("sop", "definition has no `sop:` name", "name it `plan` or `run`")
	}
	if def.Version != SOPVersion {
		add("version",
			fmt.Sprintf("SOP version %d, but this build implements %d", def.Version, SOPVersion),
			"a definition from another version may assert rules this build does not have")
	}
	if len(def.Stages) == 0 {
		add("stages", "definition has no stages", "a SOP with no stages schedules nothing")
	}

	out = append(out, lintStageIDs(def)...)
	out = append(out, lintReferences(def)...)
	out = append(out, lintAgents(def)...)
	out = append(out, lintRules(def)...)
	out = append(out, lintWorkspace(def)...)
	return out
}

func lintStageIDs(def *Definition) validate.Findings {
	var out validate.Findings
	seen := map[string]bool{}
	for _, s := range def.AllStages() {
		switch {
		case s.ID == "":
			out = append(out, stageFinding(def, s, "stage_id", "stage has no id",
				"every stage needs an id; edges and routes address stages by it"))
		case seen[s.ID]:
			out = append(out, stageFinding(def, s, "stage_id",
				fmt.Sprintf("duplicate stage id %q", s.ID),
				"ids must be unique: a duplicate silently shadows the earlier stage"))
		}
		seen[s.ID] = true
	}
	return out
}

// lintReferences catches an edge pointing at a stage that does not exist. A
// dangling `next`, `routes` target or `loop_back` is a graph that stops early
// — the failure mode of the 36-second coordinator that spawned one worker and
// stopped, but detectable before the run rather than after it.
func lintReferences(def *Definition) validate.Findings {
	var out validate.Findings
	for _, s := range def.AllStages() {
		refs := map[string]string{}
		if s.Next != "" {
			refs["next"] = s.Next
		}
		if s.LoopBack != "" {
			refs["loop_back"] = s.LoopBack
		}
		if s.OnFail != "" && s.OnFail != "abort" && s.OnFail != "retry" {
			refs["on_fail"] = s.OnFail
		}
		if s.Join != "" {
			// A join names the collected output, not a stage; nothing to check.
			delete(refs, "join")
		}
		for verdict, target := range s.Routes {
			refs["routes."+verdict] = target
		}
		for field, target := range refs {
			if _, ok := def.Stage(target); !ok {
				out = append(out, stageFinding(def, s, "dangling_edge",
					fmt.Sprintf("stage %q has %s: %q, which is not a stage", s.ID, field, target),
					"an edge to a nonexistent stage ends the graph early"))
			}
		}
		if s.LoopBack != "" && s.MaxCycle <= 0 {
			out = append(out, stageFinding(def, s, "unbounded_loop",
				fmt.Sprintf("stage %q loops back to %q with no max_cycles", s.ID, s.LoopBack),
				"an unbounded repair loop cannot terminate; set max_cycles"))
		}
	}
	return out
}

// lintAgents rejects unknown agents and, critically, [worktree] agents.
func lintAgents(def *Definition) validate.Findings {
	bundled, err := subagent.LoadBundledAgents()
	if err != nil {
		return nil // cannot check; not a reason to reject the definition
	}
	byName := make(map[string]subagent.AgentConfig, len(bundled))
	names := make([]string, 0, len(bundled))
	for _, a := range bundled {
		byName[a.Name] = a
		names = append(names, a.Name)
	}
	sort.Strings(names)

	var out validate.Findings
	for _, s := range def.AllStages() {
		name := s.AgentName()
		if name == "" {
			if s.Review != nil && s.Review.Kind == "agent" {
				name = s.Review.Agent
			}
			if name == "" {
				continue
			}
		}
		cfg, ok := byName[name]
		if !ok {
			out = append(out, stageFinding(def, s, "unknown_agent",
				fmt.Sprintf("stage %q dispatches to unknown agent %q", s.ID, name),
				"known agents: "+strings.Join(names, ", ")))
			continue
		}
		if cfg.Worktree {
			out = append(out, stageFinding(def, s, "worktree_agent",
				fmt.Sprintf("stage %q dispatches to %q, which runs in its own worktree", s.ID, name),
				"a [worktree] agent's edits land in a nested worktree that is never merged, "+
					"so the stage silently produces nothing — use `worker` or `quick-task`, "+
					"which edit the current directory"))
		}
		if s.Review != nil && s.Review.Kind == "agent" && s.Review.Agent != "" && len(s.Routes) == 0 {
			out = append(out, stageFinding(def, s, "unrouted_verdict",
				fmt.Sprintf("stage %q has an agent review but no routes", s.ID),
				"a verdict nothing routes on is a verdict nothing acts on — this is why "+
					"the prose Verifier reported PASS in 9 of 9 runs"))
		}
	}
	return out
}

// lintRules checks every validator named in the definition is registered.
func lintRules(def *Definition) validate.Findings {
	var out validate.Findings
	check := func(s Stage, specs []string, where string) {
		for _, spec := range specs {
			name, _, err := validate.ParseRule(spec)
			if err != nil {
				out = append(out, stageFinding(def, s, "bad_rule",
					fmt.Sprintf("stage %q %s: %v", s.ID, where, err), ""))
				continue
			}
			if _, ok := validate.Lookup(name); !ok {
				out = append(out, stageFinding(def, s, "unknown_rule",
					fmt.Sprintf("stage %q %s names unknown validator %q", s.ID, where, name),
					"registered validators: "+strings.Join(sortedRules(), ", ")))
			}
		}
	}
	for _, s := range def.AllStages() {
		check(s, s.Validate, "validate")
		for _, p := range s.Produces {
			check(s, p.Validate, "produces "+p.Path)
		}
	}
	return out
}

func lintWorkspace(def *Definition) validate.Findings {
	var out validate.Findings
	w := def.Workspace
	if w.Worktree != "" && w.Worktree != "per-run" && w.Worktree != "none" {
		out = append(out, validate.Finding{
			Artifact: def.SOP + ".sop.yaml", Rule: "workspace", Severity: validate.SeverityError,
			Message: fmt.Sprintf("workspace.worktree = %q, want \"per-run\" or \"none\"", w.Worktree),
		})
	}
	if w.Worktree == "per-run" && w.Cleanup != "always" {
		out = append(out, validate.Finding{
			Artifact: def.SOP + ".sop.yaml", Rule: "workspace", Severity: validate.SeverityWarn,
			Message: fmt.Sprintf("workspace.cleanup = %q with a per-run worktree", w.Cleanup),
			Fix: "cleanup: always makes teardown a terminal node reached from every exit path; " +
				"anything else leaks a worktree whenever a run does not end cleanly",
		})
	}
	return out
}

func stageFinding(def *Definition, s Stage, rule, msg, fix string) validate.Finding {
	return validate.Finding{
		Artifact: def.SOP + ".sop.yaml",
		Rule:     rule,
		Severity: validate.SeverityError,
		Message:  msg,
		Fix:      fix,
	}
}

func sortedRules() []string {
	names := validate.RuleNames()
	sort.Strings(names)
	return names
}
