package piagent_test

import (
	"context"
	"fmt"
	"log"

	adkagent "google.golang.org/adk/v2/agent"
	adktool "google.golang.org/adk/v2/tool"

	"github.com/dimetron/pi-go/piagent"
)

// Example is the whole embed: build an agent, open a session, ask a question.
// Model, provider, credentials, tools, skills and project rules all come from
// pi-go's own configuration.
func Example() {
	ctx := context.Background()

	ag, err := piagent.New(ctx)
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
// specific directory and model, an application-specific instruction, and the
// optional subsystems turned off to keep the run cheap and self-contained.
func ExampleNew_options() {
	ctx := context.Background()

	ag, err := piagent.New(ctx,
		piagent.WithWorkingDir("/srv/checkout"),
		piagent.WithModel("claude-sonnet-5"),
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
	audit := func(_ adkagent.Context, t adktool.Tool, args, result map[string]any, toolErr error) (map[string]any, error) {
		if toolErr != nil {
			log.Printf("tool %s failed: %v", t.Name(), toolErr)
			return nil, nil
		}
		log.Printf("tool %s: %d args, %d result fields", t.Name(), len(args), len(result))
		return nil, nil
	}

	ag, err := piagent.New(context.Background(), piagent.WithAfterToolCallbacks(audit))
	if err != nil {
		log.Fatal(err)
	}
	defer ag.Close()
}

// ExampleAgent_Run streams a turn's events instead of waiting for the final
// text, which is what a UI wants.
func ExampleAgent_Run() {
	ctx := context.Background()

	ag, err := piagent.New(ctx)
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
