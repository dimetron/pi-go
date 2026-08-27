package validate

import (
	"fmt"
	"strings"

	"github.com/dimetron/pi-go/internal/sop/specdoc"
)

// ArtifactContract binds an artifact to the rules it must satisfy.
type ArtifactContract struct {
	Artifact string   `json:"artifact"`
	Required bool     `json:"required"`
	Rules    []string `json:"rules"`
}

// Contract is the full set of checks for one gate in the pipeline.
type Contract struct {
	Name      string             `json:"name"`
	Artifacts []ArtifactContract `json:"artifacts"`
}

// PlanContract is what a finished /plan session must satisfy. It is the prose
// PDD SOP's own rules, made checkable: every phase produces its artifact, the
// plan's slices are sized and self-describing, and PROMPT.md carries gates and
// Done Criteria that are real rather than template text.
func PlanContract() Contract {
	return Contract{
		Name: "pdd-plan",
		Artifacts: []ArtifactContract{
			{Artifact: specdoc.RoughIdea, Required: true, Rules: []string{"non_empty"}},
			{Artifact: specdoc.Requirements, Required: true, Rules: []string{
				"non_empty",
				"min_qa(min: 3)",
			}},
			{Artifact: specdoc.Design, Required: true, Rules: []string{
				"non_empty",
				"max_lines(max: 2000)",
				`has_headings(["Acceptance Criteria", "Testing Strategy"])`,
				"acceptance_criteria_are_given_when_then",
				"research_at_least(min: 2)",
			}},
			{Artifact: specdoc.Outline, Required: true, Rules: []string{
				"non_empty",
				"max_lines(max: 120)",
				"lists_slices(min: 2)",
			}},
			{Artifact: specdoc.Plan, Required: true, Rules: []string{
				"non_empty",
				"max_lines(max: 2000)",
				"slices_are_checkboxes",
				"slice_count(min: 1, max: 25)",
				"outline_slices_match_plan_slices",
			}},
			{Artifact: specdoc.Prompt, Required: true, Rules: []string{
				"non_empty",
				`has_headings(["Objective", "Acceptance Criteria", "Implementation Slices", "Done Criteria", "Gates"])`,
				"every_slice_has([\"files\", \"verify\", \"parallel_safe\"])",
				"slice_budget(max_files: 10)",
				"gates_are_executable",
				"done_criteria(min: 3, no_placeholders: true)",
				"references_exist",
				"plan_slices_match_prompt_slices",
			}},
		},
	}
}

// RunPreflightContract is what /run requires before it will start. It is
// deliberately narrower than PlanContract: a spec written by hand, or planned
// under an older SOP, should still run if the two artifacts a run actually
// consumes are sound.
func RunPreflightContract() Contract {
	return Contract{
		Name: "run-preflight",
		Artifacts: []ArtifactContract{
			{Artifact: specdoc.Plan, Required: true, Rules: []string{
				"non_empty",
				"max_lines(max: 2000)",
				"slice_count(min: 1, max: 25)",
			}},
			{Artifact: specdoc.Prompt, Required: true, Rules: []string{
				"non_empty",
				"gates_are_executable",
				"slice_budget(max_files: 10)",
			}},
		},
	}
}

// Check runs a contract over a loaded spec. repoRoot is the checkout the spec
// lives in; rules that resolve paths or probe commands need it, and pass when
// it is empty.
func Check(spec *specdoc.Spec, repoRoot string, c Contract) Findings {
	var out Findings
	for _, ac := range c.Artifacts {
		content := spec.Files[ac.Artifact]
		if !spec.Has(ac.Artifact) {
			if ac.Required {
				out = append(out, Finding{
					Artifact: ac.Artifact,
					Rule:     "required",
					Severity: SeverityError,
					Message:  fmt.Sprintf("%s is missing", ac.Artifact),
					Fix:      "this phase of the SOP did not produce its artifact; complete it before continuing",
				})
			}
			continue
		}
		t := Target{Artifact: ac.Artifact, Content: content, Spec: spec, RepoRoot: repoRoot}
		for _, r := range ac.Rules {
			out = append(out, Apply(r, t)...)
		}
	}
	return out
}

// Describe renders the contract as instruction text for the agent that must
// satisfy it.
//
// This is the half that makes validation a loop rather than a gate: the
// planner is told the exact rules its artifacts are checked against, in the
// same session where the findings come back. A contract the producer cannot
// see is just a way to fail late.
func (c Contract) Describe() string {
	var b strings.Builder
	b.WriteString("## Artifact Contract (machine-checked)\n\n")
	b.WriteString("Every artifact below is validated by code before the plan is accepted.\n")
	b.WriteString("A failing check keeps the planning session open and returns the finding to you.\n\n")
	for _, ac := range c.Artifacts {
		req := "optional"
		if ac.Required {
			req = "required"
		}
		fmt.Fprintf(&b, "- `%s` (%s)\n", ac.Artifact, req)
		for _, r := range ac.Rules {
			fmt.Fprintf(&b, "  - %s\n", describeRule(r))
		}
	}
	return b.String()
}

// describeRule renders one rule spec in prose, falling back to the spec itself
// for rules with no gloss so a newly registered rule is still shown.
func describeRule(spec string) string {
	name, args, err := ParseRule(spec)
	if err != nil {
		return spec
	}
	switch name {
	case "non_empty":
		return "must exist and hold content"
	case "max_lines":
		return fmt.Sprintf("at most %d lines (the read tool's window — a longer file reaches a worker one page at a time)", args.Int("max", 2000))
	case "has_headings":
		return "must have these sections: " + strings.Join(quoteAll(args.Items()), ", ")
	case "min_qa":
		return fmt.Sprintf("record at least %d clarifying questions and their answers (warning only)", args.Int("min", 3))
	case "research_at_least":
		return fmt.Sprintf("at least %d files under research/ — dispatch parallel `explore` subagents rather than reading the codebase into this session", args.Int("min", 2))
	case "lists_slices":
		return fmt.Sprintf("list at least %d slices", args.Int("min", 2))
	case "slices_are_checkboxes":
		return "slices must be `- [ ] Step N: <title>` checkboxes, not headings — /run ticks these to track progress"
	case "slice_count":
		return fmt.Sprintf("between %d and %d slices; a longer plan means the feature must be split into sequential specs", args.Int("min", 1), args.Int("max", 25))
	case "every_slice_has":
		return "every slice must state " + strings.Join(quoteAll(args.Items()), ", ") + " — a worker cannot see this conversation"
	case "slice_budget":
		return fmt.Sprintf("no slice may name more than %d files; oversized slices exhaust a worker's context mid-slice", args.Int("max_files", 10))
	case "gates_present":
		return fmt.Sprintf("at least %d gate command(s)", args.Int("min", 1))
	case "gates_are_executable":
		return "gate commands must be this project's real commands: no `<placeholders>`, and the program must exist"
	case "done_criteria":
		return fmt.Sprintf("at least %d Done Criteria, each an outcome checkable by reading code or running a command — no template text", args.Int("min", 3))
	case "no_placeholders":
		return "no unfilled `<template>` text"
	case "references_exist":
		return "every path under `## Reference` must exist"
	case "plan_slices_match_prompt_slices":
		return "plan.md and PROMPT.md must describe the same slices — PROMPT.md is what /run executes"
	case "outline_slices_match_plan_slices":
		return "plan.md must not drop slices the outline approved"
	case "acceptance_criteria_are_given_when_then":
		return "acceptance criteria phrased as Given / When / Then"
	case "no_solution_language":
		return "research states facts about the code as it is, not proposals (warning only)"
	default:
		return spec
	}
}

func quoteAll(items []string) []string {
	out := make([]string, len(items))
	for i, s := range items {
		out[i] = "`" + s + "`"
	}
	return out
}
