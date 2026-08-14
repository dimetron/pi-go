package pimodels_test

import (
	"context"
	"fmt"
	"log"

	"github.com/dimetron/pi-go/pimodels"
)

// The common case: name a model, get a client. The provider is inferred from
// the name and the key comes from that provider's environment variable.
func ExampleNew() {
	ctx := context.Background()

	m, err := pimodels.New(ctx, "gpt-5.6-luna")
	if err != nil {
		log.Fatal(err)
	}

	// m is an ADK model.LLM. Hand it to any ADK agent — including pi-go's,
	// which takes it as piagent.WithModel(m).
	_ = m
}

// Point the client at a gateway or a self-hosted OpenAI-compatible server. The
// explicit endpoint wins over anything inferred from the model name.
func ExampleNew_gateway() {
	ctx := context.Background()

	m, err := pimodels.New(ctx, "gpt-5.6-luna",
		pimodels.WithBaseURL("https://llm-gateway.internal/v1"),
		pimodels.WithAPIKey("tenant-key"),
		pimodels.WithHeaders(map[string]string{"X-Tenant": "acme"}),
	)
	if err != nil {
		log.Fatal(err)
	}
	_ = m
}

// Resolve answers "where would this request actually go" without building a
// client or needing a credential — worth doing at startup, so a misconfigured
// model name fails before the first request rather than during it.
func ExampleResolve() {
	info, err := pimodels.Resolve("claude-sonnet-5")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(info.Provider)
	// Output: anthropic
}

// A local Ollama model needs no credential at all.
func ExampleResolve_ollama() {
	info, err := pimodels.Resolve("ollama/gemma4:e4b")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(info.Provider, info.Model, info.Ollama)
	// Output: ollama gemma4:e4b true
}

// Report a missing credential precisely, rather than letting the first request
// fail with a provider-specific auth error.
func ExampleAPIKeyEnvVar() {
	info, err := pimodels.Resolve("grok-4.6")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("set %s to use %s\n", pimodels.APIKeyEnvVar(info.Provider), info.Provider)
	// Output: set XAI_API_KEY to use xai
}

// ContextWindow is how an embedder decides what fits before sending it.
func ExampleContextWindow() {
	fmt.Println(pimodels.ContextWindow("gemini-3.7-flash") > 0)
	// Output: true
}
