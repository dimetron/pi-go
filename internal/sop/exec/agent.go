package exec

import (
	"fmt"

	adkagent "google.golang.org/adk/v2/agent"

	"github.com/dimetron/pi-go/internal/sop"
)

// Agent compiles a SOP definition with f and returns it as an ADK agent, ready
// to hand to runner.Config.Agent.
//
// It deliberately does not use agent/workflowagent.New, which looks like the
// obvious entry point. That constructor calls workflow.New(name, edges) with no
// options, so the SOP's defaults.max_concurrency would be silently dropped, and
// it keeps the *workflow.Workflow in an unexported wrapper. Building the
// workflow here keeps both the options and the handle; agent.Config.Run is the
// sanctioned hook for wrapping any Run function as an agent.
func Agent(def *sop.Definition, f sop.NodeFactory) (adkagent.Agent, *sop.Compiled, error) {
	compiled, err := sop.Compile(def, f)
	if err != nil {
		return nil, nil, err
	}
	wf, err := compiled.Workflow()
	if err != nil {
		return nil, nil, err
	}
	ag, err := adkagent.New(adkagent.Config{
		Name:        def.SOP,
		Description: def.Description,
		Run:         wf.Run,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("wrapping SOP %q as an agent: %w", def.SOP, err)
	}
	return ag, compiled, nil
}
