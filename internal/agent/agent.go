// Package agent sets up the ADK Go agent loop with tools, system prompt,
// and runner for the pi-go coding agent.
package agent

import (
	"context"
	"fmt"
	"iter"
	"os"
	"path/filepath"

	adkagent "google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/model"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/adk/tool"
	"google.golang.org/genai"
)

// re-export callback types for use by CLI without importing llmagent directly.
type (
	BeforeToolCallback = llmagent.BeforeToolCallback
	AfterToolCallback  = llmagent.AfterToolCallback
)

const (
	// AppName is the ADK application name used for session management.
	AppName = "pi-go"

	// DefaultUserID is the default user ID for local single-user sessions.
	DefaultUserID = "local"
)

// SystemInstruction is the default system prompt for the coding agent.
const SystemInstruction = `You are pi-go, a coding agent that helps users with software engineering tasks.

You have access to tools for reading, writing, and editing files, running shell commands,
and searching codebases. Use these tools to assist the user effectively.

Guidelines:
- Read files before modifying them to understand existing code.
- Make minimal, focused changes — do not over-engineer.
- Prefer editing existing files over creating new ones.
- Write safe, secure code — avoid introducing vulnerabilities.
- When running shell commands, use appropriate timeouts.
- Explain your reasoning briefly when it helps the user understand your approach.
`

// Config holds configuration for creating a new Agent.
type Config struct {
	// Model is the LLM provider to use (implements model.LLM).
	Model model.LLM

	// Tools are the tools available to the agent.
	Tools []tool.Tool

	// Toolsets are additional tool providers (e.g. MCP toolsets).
	Toolsets []tool.Toolset

	// Instruction overrides the default system instruction.
	// If empty, SystemInstruction is used.
	Instruction string

	// SessionService overrides the default in-memory session service.
	// If nil, an in-memory service is created.
	SessionService session.Service

	// BeforeToolCallbacks run before each tool execution.
	BeforeToolCallbacks []BeforeToolCallback

	// AfterToolCallbacks run after each tool execution.
	AfterToolCallbacks []AfterToolCallback
}

// Agent wraps an ADK Runner and session management for the coding agent.
type Agent struct {
	runner         *runner.Runner
	sessionService session.Service
}

// New creates a new Agent with the given configuration.
func New(cfg Config) (*Agent, error) {
	instruction := cfg.Instruction
	if instruction == "" {
		instruction = SystemInstruction
	}

	// Add working directory context to the instruction.
	cwd, err := os.Getwd()
	if err == nil {
		instruction += fmt.Sprintf("\nCurrent working directory: %s\n", cwd)
	}

	// Create the LLM agent.
	llmAgent, err := llmagent.New(llmagent.Config{
		Name:                "pi",
		Description:         "A coding agent that helps with software engineering tasks.",
		Model:               cfg.Model,
		Instruction:         instruction,
		Tools:               cfg.Tools,
		Toolsets:            cfg.Toolsets,
		BeforeToolCallbacks: cfg.BeforeToolCallbacks,
		AfterToolCallbacks:  cfg.AfterToolCallbacks,
	})
	if err != nil {
		return nil, fmt.Errorf("creating LLM agent: %w", err)
	}

	// Set up session service.
	sessionSvc := cfg.SessionService
	if sessionSvc == nil {
		sessionSvc = session.InMemoryService()
	}

	// Create the runner.
	r, err := runner.New(runner.Config{
		AppName:        AppName,
		Agent:          llmAgent,
		SessionService: sessionSvc,
	})
	if err != nil {
		return nil, fmt.Errorf("creating runner: %w", err)
	}

	return &Agent{
		runner:         r,
		sessionService: sessionSvc,
	}, nil
}

// CreateSession creates a new session and returns its ID.
func (a *Agent) CreateSession(ctx context.Context) (string, error) {
	resp, err := a.sessionService.Create(ctx, &session.CreateRequest{
		AppName: AppName,
		UserID:  DefaultUserID,
	})
	if err != nil {
		return "", fmt.Errorf("creating session: %w", err)
	}
	return resp.Session.ID(), nil
}

// Run sends a user message and returns an iterator over agent events.
// The caller should iterate over the returned sequence to process events.
func (a *Agent) Run(ctx context.Context, sessionID string, userMessage string) iter.Seq2[*session.Event, error] {
	msg := genai.NewContentFromText(userMessage, genai.RoleUser)
	return a.runner.Run(ctx, DefaultUserID, sessionID, msg, adkagent.RunConfig{})
}

// RunStreaming sends a user message with SSE streaming enabled.
func (a *Agent) RunStreaming(ctx context.Context, sessionID string, userMessage string) iter.Seq2[*session.Event, error] {
	msg := genai.NewContentFromText(userMessage, genai.RoleUser)
	return a.runner.Run(ctx, DefaultUserID, sessionID, msg, adkagent.RunConfig{
		StreamingMode: adkagent.StreamingModeSSE,
	})
}

// LoadInstruction attempts to load an AGENTS.md file from the working directory
// and appends its content to the base instruction.
func LoadInstruction(baseInstruction string) string {
	cwd, err := os.Getwd()
	if err != nil {
		return baseInstruction
	}

	agentsFile := filepath.Join(cwd, ".pi-go", "AGENTS.md")
	data, err := os.ReadFile(agentsFile)
	if err != nil {
		return baseInstruction
	}

	return baseInstruction + "\n\n# Project Rules\n\n" + string(data)
}
