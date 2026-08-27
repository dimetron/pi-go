package sop

import (
	"bytes"
	"fmt"
	"time"

	"gopkg.in/yaml.v3"
)

// A Definition is a Standard Operating Procedure expressed as data.
//
// The prose SOPs it replaces — DefaultPDDSOP and the /run coordinator contract
// — stated their rules in a Go string constant, which made every rule
// advisory. The session corpus is a catalog of those rules being declined:
// research delegated to subagents 4 times across 69 planning sessions, slices
// implemented inline by coordinators 302 bash calls to 8 worker spawns, and a
// Verifier whose verdict was PASS in 9 of 9 runs because nothing read it.
//
// Here the sequencing is a graph the scheduler walks, the review verdicts are
// edges, and the artifact rules are validators. What stays in prose is each
// node's own instruction, which is the part an LLM should be deciding.
type Definition struct {
	SOP         string    `yaml:"sop"`
	Version     int       `yaml:"version"`
	Description string    `yaml:"description"`
	Workspace   Workspace `yaml:"workspace"`
	Defaults    Defaults  `yaml:"defaults"`
	Preflight   []Stage   `yaml:"preflight"`
	Stages      []Stage   `yaml:"stages"`
}

// Workspace declares where a run's work lands and who cleans it up.
type Workspace struct {
	// Worktree is "per-run" or "none". Per-run means the graph owns one
	// worktree; slices get branches inside it rather than nested worktrees.
	Worktree string `yaml:"worktree"`
	Branch   string `yaml:"branch"`
	MergeOn  string `yaml:"merge_on"` // "success" | "never"
	// Cleanup is "always" or "on_success". "always" is a terminal node
	// reached from every exit path — the reason orphaned worktrees stop
	// accumulating.
	Cleanup string `yaml:"cleanup"`
}

// Defaults apply to any stage that does not override them.
type Defaults struct {
	Timeout     Duration `yaml:"timeout"`
	Retry       *Retry   `yaml:"retry"`
	OnRateLimit string   `yaml:"on_rate_limit"` // "throttle" | "fail"
	// MaxConcurrency bounds the whole graph. It is separate from the
	// per-process subagent pool, which multiplies rather than shares when
	// runs nest.
	MaxConcurrency int `yaml:"max_concurrency"`
}

// Retry is the per-stage retry policy. It maps onto workflow.RetryConfig.
type Retry struct {
	MaxAttempts  int      `yaml:"max_attempts"`
	InitialDelay Duration `yaml:"initial_delay"`
	MaxDelay     Duration `yaml:"max_delay"`
	Backoff      float64  `yaml:"backoff"`
	Jitter       float64  `yaml:"jitter"`
}

// Stage is one node, or one fan-out group, in the SOP graph.
type Stage struct {
	ID          string `yaml:"id"`
	Description string `yaml:"description"`

	// Kind is "agent" (an LLM does the work) or "function" (deterministic Go:
	// validators, gates, git). Empty means "agent" when Agent is set and
	// "function" otherwise.
	Kind  string `yaml:"kind"`
	Agent string `yaml:"agent"`

	Inputs   []string   `yaml:"inputs"`
	Produces []Produces `yaml:"produces"`

	// Validate holds stage-level rules that are not tied to one artifact —
	// preflight checks, for instance.
	Validate []string `yaml:"validate"`

	FanOut *FanOut `yaml:"fan_out"`
	Join   string  `yaml:"join"`

	Review *Review `yaml:"review"`

	// Routes maps an output value to the stage it advances to. This is what
	// turns a verdict into control flow instead of a sentence in a prompt.
	Routes   map[string]string `yaml:"routes"`
	LoopBack string            `yaml:"loop_back"`
	MaxCycle int               `yaml:"max_cycles"`

	// OnFail is "abort", "retry" or the id of a stage to jump to.
	OnFail string `yaml:"on_fail"`

	Timeout Duration `yaml:"timeout"`
	Retry   *Retry   `yaml:"retry"`

	// Gate is a shell command the engine runs after the stage body. Running it
	// here rather than asking the LLM to is what makes a "verified" slice
	// verified.
	Gate         string `yaml:"gate"`
	GatesFrom    string `yaml:"gates_from"`
	OutputSchema string `yaml:"output_schema"`
	Next         string `yaml:"next"`
}

// Produces declares an artifact a stage must write, and the rules it must
// satisfy. A failing rule keeps the graph on this stage.
type Produces struct {
	Path     string   `yaml:"path"`
	Validate []string `yaml:"validate"`
}

// FanOut declares that a stage runs once per item of a collection.
type FanOut struct {
	Over           string `yaml:"over"`
	Agent          string `yaml:"agent"`
	GroupBy        string `yaml:"group_by"`
	MaxConcurrency int    `yaml:"max_concurrency"`
	// Isolation is "sub_branch" or "none". Sub-branch keeps parallel workers
	// from seeing each other's event history.
	Isolation    string `yaml:"isolation"`
	OutputSchema string `yaml:"output_schema"`
}

// Review is a checkpoint between stages: a human approval or an agent verdict.
type Review struct {
	Kind          string `yaml:"kind"` // "human" | "agent"
	Prompt        string `yaml:"prompt"`
	Agent         string `yaml:"agent"`
	VerdictSchema string `yaml:"verdict_schema"`
}

// Duration is a YAML-friendly time.Duration accepting "20m", "5s", "1h30m".
type Duration time.Duration

// UnmarshalYAML parses a duration string.
func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	var s string
	if err := value.Decode(&s); err != nil {
		return fmt.Errorf("duration must be a string like \"20m\": %w", err)
	}
	if s == "" {
		*d = 0
		return nil
	}
	parsed, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("parsing duration %q: %w", s, err)
	}
	*d = Duration(parsed)
	return nil
}

// Duration returns the value as a time.Duration.
func (d Duration) Duration() time.Duration { return time.Duration(d) }

// MarshalYAML renders the duration back as a string.
func (d Duration) MarshalYAML() (any, error) {
	if d == 0 {
		return "", nil
	}
	return time.Duration(d).String(), nil
}

// ParseDefinition decodes a SOP definition. It rejects unknown fields: a typo
// in a stage key would otherwise silently disable the behavior it names.
func ParseDefinition(data []byte) (*Definition, error) {
	var def Definition
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&def); err != nil {
		return nil, fmt.Errorf("parsing SOP: %w", err)
	}
	return &def, nil
}

// Stage returns the stage with the given id.
func (d *Definition) Stage(id string) (Stage, bool) {
	for _, s := range d.AllStages() {
		if s.ID == id {
			return s, true
		}
	}
	return Stage{}, false
}

// AllStages returns preflight stages followed by the main stages.
func (d *Definition) AllStages() []Stage {
	out := make([]Stage, 0, len(d.Preflight)+len(d.Stages))
	out = append(out, d.Preflight...)
	return append(out, d.Stages...)
}

// EffectiveKind resolves a stage's kind, defaulting by whether an agent is set.
func (s Stage) EffectiveKind() string {
	switch {
	case s.Kind != "":
		return s.Kind
	case s.Agent != "" || s.FanOut != nil:
		return "agent"
	default:
		return "function"
	}
}

// AgentName returns the agent this stage (or its fan-out) dispatches to.
func (s Stage) AgentName() string {
	if s.FanOut != nil && s.FanOut.Agent != "" {
		return s.FanOut.Agent
	}
	return s.Agent
}
