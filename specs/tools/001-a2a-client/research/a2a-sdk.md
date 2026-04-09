c# A2A Go SDK v2 — Research Findings

Repository: `github.com/a2aproject/a2a-go/v2`

## Key Packages

- `github.com/a2aproject/a2a-go/v2/a2a` — Protocol types
- `github.com/a2aproject/a2a-go/v2/a2aclient` — Client SDK
- `github.com/a2aproject/a2a-go/v2/a2asrv` — Server SDK

## Core Protocol Types

```go
// Task lifecycle
type TaskID    string  // NewTaskID() → UUIDv4
type ContextID string  // NewContextID() → UUIDv4
type TaskState int     // Unspecified, AuthRequired, Canceled, Completed, Failed, InputRequired, Rejected, Submitted, Working

// Messages
type Message struct {
    ID      string
    Role    MessageRole  // Unspecified, Agent, User
    Parts   []*Part      // content
    TaskID  TaskID
    ContextID string
}

type Part struct {
    Content PartContent  // sealed interface: *Text, *URL, *Raw, *Data
}

// Factories
a2a.NewTextPart(text string) *Part
a2a.NewDataPart(value any) *Part
a2a.NewMessage(role MessageRole, parts ...*Part) *Message

// Streaming events
type Event interface { isEvent() }  // sealed interface
// Implementations:
// *a2a.Message                   // terminal
// *a2a.Task                      // terminal (non-streaming completion)
// *a2a.TaskStatusUpdateEvent     // state change
// *a2a.TaskArtifactUpdateEvent   // artifact update
```

## Creating an A2A Client

```go
// From AgentCard
client, err := a2aclient.NewFromCard(ctx, card, opts...)

// From endpoint list
client, err := a2aclient.NewFromEndpoints(ctx, endpoints, opts...)

// Options
a2aclient.WithConfig(a2aclient.Config{AcceptedOutputModes: []string{"text/plain"}})
a2aclient.WithJSONRPCTransport(&http.Client{Timeout: 30*time.Second})
a2aclient.WithCallInterceptors(interceptors...)
a2aclient.WithLoggingInterceptor(...)
```

## Sending Messages

```go
// Non-streaming
msg := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("hello"))
result, err := client.SendMessage(ctx, &a2a.SendMessageRequest{Message: msg})
// result is *a2a.Task or *a2a.Message

// Streaming (iter.Seq2)
for event, err := range client.SendStreamingMessage(ctx, req) {
    switch e := event.(type) {
    case *a2a.TaskStatusUpdateEvent:
    case *a2a.TaskArtifactUpdateEvent:
    case *a2a.Message:  // terminal
    case *a2a.Task:    // terminal (non-streaming completion)
    }
}
```

## Event Processing Rules

- `Message` event — always terminal (stops further processing)
- `Task` event — terminal for non-streaming completion
- `TaskStatusUpdateEvent` — terminal only if `State.Terminal()` is true (Completed, Failed, Canceled, Rejected)
- `TaskArtifactUpdateEvent` — never terminal

## Helper Methods on *Part

```go
part.Text() (string, bool)
part.Data() (any, bool)
```

## Auth Patterns

```go
// Server declares security requirements in AgentCard
// Client uses AuthInterceptor + InMemoryCredentialsStore
credStore := a2aclient.NewInMemoryCredentialsStore()
credStore.Set(sessionID, a2a.SecuritySchemeName("bearer"), a2aclient.AuthCredential("token"))
client, _ := a2aclient.NewFromCard(ctx, card,
    a2aclient.WithCallInterceptors(&a2aclient.AuthInterceptor{Service: credStore}),
)
```
