package piagent_test

import (
	"context"
	"fmt"
	"log"

	adkagent "google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/model"
	adktool "google.golang.org/adk/v2/tool"

	"github.com/dimetron/pi-go/piagent"
)

// newModel stands in for the models package. piagent takes any ADK model.LLM
// and never builds one itself, so the examples below say nothing about where
// yours comes from.
func newModel(context.Context, string) (model.LLM, error) { return nil, nil }

// Example is the whole embed: bring a model, build an agent, ask a question.
// Tools, skills, project rules, subagents and sessions all come from pi-go's
// own conventions.
func Example() {
	ctx := context.Background()

	m, err := newModel(ctx, "claude-sonnet-5")
	if err != nil {
		log.Fatal(err)
	}

	ag, err := piagent.New(ctx, piagent.WithModel(m))
	if err != nil {
		log.Fatal(err)
	}
	defer ag.Close()

	sessionID, err := ag.NewSession(ctx)
	if err != nil {
		log.Fatal(err)
	}

	answer, err := ag.Ask(ctx, sessionID, "summarize this repository in one sentence")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(answer)
}

// ExampleNew_options shows the knobs an embedder usually reaches for: a
// specific directory, an application-specific instruction, and the optional
// subsystems turned off to keep the run cheap and self-contained.
func ExampleNew_options() {
	ctx := context.Background()

	m, err := newModel(ctx, "claude-sonnet-5")
	if err != nil {
		log.Fatal(err)
	}

	ag, err := piagent.New(ctx,
		piagent.WithModel(m),
		piagent.WithWorkingDir("/srv/checkout"),
		piagent.WithExtraInstruction("Answer as a release engineer. Never modify files under /srv/checkout/vendor."),
		piagent.WithLSP(piagent.LSPOff),
		piagent.WithMemory(false),
		piagent.WithPalace(false),
	)
	if err != nil {
		log.Fatal(err)
	}
	defer ag.Close()

	fmt.Println(len(ag.Tools()) > 0)
}

// ExampleWithAfterToolCallbacks audits every tool call. The callback returns
// nil to leave the result untouched; returning a map would replace the result
// for every later callback and for the model.
func ExampleWithAfterToolCallbacks() {
	ctx := context.Background()

	audit := func(_ adkagent.Context, t adktool.Tool, args, result map[string]any, toolErr error) (map[string]any, error) {
		if toolErr != nil {
			log.Printf("tool %s failed: %v", t.Name(), toolErr)
			return nil, nil
		}
		log.Printf("tool %s: %d args, %d result fields", t.Name(), len(args), len(result))
		return nil, nil
	}

	m, err := newModel(ctx, "claude-sonnet-5")
	if err != nil {
		log.Fatal(err)
	}

	ag, err := piagent.New(ctx, piagent.WithModel(m), piagent.WithAfterToolCallbacks(audit))
	if err != nil {
		log.Fatal(err)
	}
	defer ag.Close()
}

// ExampleAgent_Run streams a turn's events instead of waiting for the final
// text, which is what a UI wants.
func ExampleAgent_Run() {
	ctx := context.Background()

	m, err := newModel(ctx, "claude-sonnet-5")
	if err != nil {
		log.Fatal(err)
	}

	ag, err := piagent.New(ctx, piagent.WithModel(m))
	if err != nil {
		log.Fatal(err)
	}
	defer ag.Close()

	sessionID, err := ag.NewSession(ctx)
	if err != nil {
		log.Fatal(err)
	}

	for ev, err := range ag.Run(ctx, sessionID, "run the tests and report failures") {
		if err != nil {
			log.Fatal(err)
		}
		if ev == nil || ev.Content == nil {
			continue
		}
		for _, part := range ev.Content.Parts {
			switch {
			case part.FunctionCall != nil:
				fmt.Printf("calling %s\n", part.FunctionCall.Name)
			case part.Text != "":
				fmt.Print(part.Text)
			}
		}
	}
}
