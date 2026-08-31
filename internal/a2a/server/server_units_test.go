package server

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	a2ataskstore "github.com/a2aproject/a2a-go/v2/a2asrv/taskstore"

	acpserver "github.com/dimetron/pi-go/internal/acp/server"
)

// discardLogger returns a logger that drops every record.
func discardLogger() *slog.Logger { return slog.New(slog.DiscardHandler) }

// drain collects every event an executor iterator yields.
func drain(seq func(func(a2a.Event, error) bool)) ([]a2a.Event, []error) {
	var events []a2a.Event
	var errs []error
	seq(func(e a2a.Event, err error) bool {
		events = append(events, e)
		errs = append(errs, err)
		return true
	})
	return events, errs
}

// states extracts the task state of every status-update event.
func states(events []a2a.Event) []a2a.TaskState {
	var out []a2a.TaskState
	for _, e := range events {
		if su, ok := e.(*a2a.TaskStatusUpdateEvent); ok {
			out = append(out, su.Status.State)
		}
	}
	return out
}

func newExecCtx(prompt string) *a2asrv.ExecutorContext {
	return &a2asrv.ExecutorContext{
		Message:   a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart(prompt)),
		TaskID:    "task-1",
		ContextID: "ctx-1",
	}
}

func TestExtractText(t *testing.T) {
	tests := []struct {
		name string
		msg  *a2a.Message
		want string
	}{
		{"nil message", nil, ""},
		{"no parts", a2a.NewMessage(a2a.MessageRoleUser), ""},
		{"single part", a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("hi")), "hi"},
		{
			"parts concatenated",
			a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("a"), a2a.NewTextPart("b")),
			"ab",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := extractText(tt.msg); got != tt.want {
				t.Errorf("extractText() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExecuteEmptyPrompt(t *testing.T) {
	e := &piExecutor{handler: acpserver.EchoPromptHandler, logger: discardLogger()}
	events, _ := drain(e.Execute(context.Background(), newExecCtx("   ")))

	if got := states(events); len(got) != 1 || got[0] != a2a.TaskStateFailed {
		t.Fatalf("states = %v, want [failed]", got)
	}
}

func TestExecuteHandlerError(t *testing.T) {
	wantErr := errors.New("model exploded")
	h := func(context.Context, acpserver.PromptTurn) (acpserver.PromptResult, error) {
		return acpserver.PromptResult{}, wantErr
	}
	e := &piExecutor{handler: h, logger: discardLogger()}
	events, _ := drain(e.Execute(context.Background(), newExecCtx("hello")))

	got := states(events)
	if len(got) != 2 || got[0] != a2a.TaskStateWorking || got[1] != a2a.TaskStateFailed {
		t.Fatalf("states = %v, want [working failed]", got)
	}
	last, ok := events[len(events)-1].(*a2a.TaskStatusUpdateEvent)
	if !ok || last.Status.Message == nil {
		t.Fatal("failed event carries no status message")
	}
	if txt := extractText(last.Status.Message); !strings.Contains(txt, wantErr.Error()) {
		t.Errorf("failure text = %q, want it to contain %q", txt, wantErr)
	}
}

func TestExecuteEmitsTaskWhenNotStored(t *testing.T) {
	e := &piExecutor{handler: acpserver.EchoPromptHandler, logger: discardLogger()}
	events, _ := drain(e.Execute(context.Background(), newExecCtx("hello")))

	if len(events) == 0 {
		t.Fatal("no events")
	}
	if _, ok := events[0].(*a2a.Task); !ok {
		t.Fatalf("first event = %T, want *a2a.Task", events[0])
	}
	if got := states(events); len(got) != 2 || got[1] != a2a.TaskStateCompleted {
		t.Fatalf("states = %v, want [working completed]", got)
	}
}

func TestExecuteSkipsTaskWhenStored(t *testing.T) {
	execCtx := newExecCtx("hello")
	execCtx.StoredTask = &a2a.Task{ID: execCtx.TaskID, ContextID: execCtx.ContextID}

	e := &piExecutor{handler: acpserver.EchoPromptHandler, logger: discardLogger()}
	events, _ := drain(e.Execute(context.Background(), execCtx))

	for _, ev := range events {
		if _, ok := ev.(*a2a.Task); ok {
			t.Fatal("emitted a Task even though one was already stored")
		}
	}
}

// TestExecuteStopsOnYieldFalse covers every early-return path taken when the
// consumer stops reading part-way through the event stream.
func TestExecuteStopsOnYieldFalse(t *testing.T) {
	for _, stopAfter := range []int{1, 2, 3, 4} {
		t.Run("stop-after-"+string(rune('0'+stopAfter)), func(t *testing.T) {
			e := &piExecutor{handler: acpserver.EchoPromptHandler, logger: discardLogger()}
			seen := 0
			e.Execute(context.Background(), newExecCtx("hello"))(func(a2a.Event, error) bool {
				seen++
				return seen < stopAfter
			})
			if seen > stopAfter {
				t.Fatalf("yielded %d events after asking to stop at %d", seen, stopAfter)
			}
		})
	}
}

func TestCancelEmitsCanceled(t *testing.T) {
	e := &piExecutor{handler: acpserver.EchoPromptHandler, logger: discardLogger()}
	events, _ := drain(e.Cancel(context.Background(), newExecCtx("hello")))

	if got := states(events); len(got) != 1 || got[0] != a2a.TaskStateCanceled {
		t.Fatalf("states = %v, want [canceled]", got)
	}
}

func TestTakeStoredTask(t *testing.T) {
	withMeta := func(v any) *a2a.Message {
		m := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("x"))
		m.TaskID = "task-1"
		m.ContextID = "ctx-1"
		m.Metadata = map[string]any{storedTaskMetadataKey: v}
		return m
	}

	t.Run("nil message", func(t *testing.T) {
		task, err := takeStoredTask(nil)
		if task != nil || err != nil {
			t.Fatalf("got (%v, %v), want (nil, nil)", task, err)
		}
	})

	t.Run("nil metadata", func(t *testing.T) {
		task, err := takeStoredTask(a2a.NewMessage(a2a.MessageRoleUser))
		if task != nil || err != nil {
			t.Fatalf("got (%v, %v), want (nil, nil)", task, err)
		}
	})

	t.Run("key absent", func(t *testing.T) {
		m := a2a.NewMessage(a2a.MessageRoleUser)
		m.Metadata = map[string]any{"other": 1}
		task, err := takeStoredTask(m)
		if task != nil || err != nil {
			t.Fatalf("got (%v, %v), want (nil, nil)", task, err)
		}
	})

	t.Run("value not an object", func(t *testing.T) {
		if _, err := takeStoredTask(withMeta("nope")); err == nil {
			t.Fatal("want error for non-object metadata value")
		}
	})

	t.Run("rejects non-continuation state", func(t *testing.T) {
		for _, state := range []string{"", string(a2a.TaskStateCompleted), string(a2a.TaskStateWorking), "bogus"} {
			v := map[string]any{"state": state}
			if _, err := takeStoredTask(withMeta(v)); err == nil {
				t.Errorf("state %q: want error", state)
			}
		}
	})

	t.Run("accepts continuation states", func(t *testing.T) {
		for _, state := range []a2a.TaskState{a2a.TaskStateInputRequired, a2a.TaskStateAuthRequired} {
			msg := withMeta(map[string]any{"state": string(state)})
			task, err := takeStoredTask(msg)
			if err != nil {
				t.Fatalf("state %q: %v", state, err)
			}
			if task.Status.State != state {
				t.Errorf("state = %q, want %q", task.Status.State, state)
			}
			if task.ID != "task-1" || task.ContextID != "ctx-1" {
				t.Errorf("task ids = (%q, %q), want (task-1, ctx-1)", task.ID, task.ContextID)
			}
			if _, still := msg.Metadata[storedTaskMetadataKey]; still {
				t.Error("metadata key was not consumed")
			}
		}
	})

	t.Run("decodes status message", func(t *testing.T) {
		v := map[string]any{
			"state": string(a2a.TaskStateInputRequired),
			"message": map[string]any{
				"role":  "agent",
				"parts": []any{map[string]any{"kind": "text", "text": "more input please"}},
			},
		}
		task, err := takeStoredTask(withMeta(v))
		if err != nil {
			t.Fatalf("takeStoredTask: %v", err)
		}
		if task.Status.Message == nil {
			t.Fatal("status message not decoded")
		}
		if got := extractText(task.Status.Message); got != "more input please" {
			t.Errorf("status text = %q, want %q", got, "more input please")
		}
	})

	t.Run("nil status message stays nil", func(t *testing.T) {
		v := map[string]any{"state": string(a2a.TaskStateInputRequired), "message": nil}
		task, err := takeStoredTask(withMeta(v))
		if err != nil {
			t.Fatalf("takeStoredTask: %v", err)
		}
		if task.Status.Message != nil {
			t.Error("want nil status message")
		}
	})

	t.Run("unmarshalable status message", func(t *testing.T) {
		v := map[string]any{"state": string(a2a.TaskStateInputRequired), "message": "not-an-object"}
		if _, err := takeStoredTask(withMeta(v)); err == nil {
			t.Fatal("want decode error")
		}
	})

	t.Run("unencodable status message", func(t *testing.T) {
		v := map[string]any{"state": string(a2a.TaskStateInputRequired), "message": make(chan int)}
		if _, err := takeStoredTask(withMeta(v)); err == nil {
			t.Fatal("want encode error")
		}
	})
}

func newSeedInterceptor() (*seedTaskInterceptor, a2ataskstore.Store) {
	store := a2ataskstore.NewInMemory(nil)
	return &seedTaskInterceptor{store: store}, store
}

func sendReq(msg *a2a.Message) *a2asrv.Request {
	return &a2asrv.Request{Payload: &a2a.SendMessageRequest{Message: msg}}
}

func TestSeedTaskInterceptorPassthrough(t *testing.T) {
	msgNoTask := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("x"))

	tests := []struct {
		name string
		req  *a2asrv.Request
	}{
		{"nil request", nil},
		{"wrong payload type", &a2asrv.Request{Payload: &a2a.GetTaskRequest{}}},
		{"nil message", &a2asrv.Request{Payload: &a2a.SendMessageRequest{}}},
		{"no task id", sendReq(msgNoTask)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			i, store := newSeedInterceptor()
			ctx, res, err := i.Before(context.Background(), nil, tt.req)
			if err != nil || res != nil || ctx == nil {
				t.Fatalf("Before() = (%v, %v, %v), want passthrough", ctx, res, err)
			}
			if _, err := store.Get(context.Background(), "task-1"); !errors.Is(err, a2a.ErrTaskNotFound) {
				t.Error("interceptor seeded a task it should have ignored")
			}
		})
	}
}

func TestSeedTaskInterceptorSeedsSubmittedTask(t *testing.T) {
	i, store := newSeedInterceptor()
	msg := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("x"))
	msg.TaskID = "task-1"
	msg.ContextID = "ctx-1"

	if _, _, err := i.Before(context.Background(), nil, sendReq(msg)); err != nil {
		t.Fatalf("Before: %v", err)
	}
	task, err := store.Get(context.Background(), "task-1")
	if err != nil {
		t.Fatalf("task was not seeded: %v", err)
	}
	if task.Task.ContextID != "ctx-1" {
		t.Errorf("ContextID = %q, want ctx-1", task.Task.ContextID)
	}
}

func TestSeedTaskInterceptorSeedsFromMetadata(t *testing.T) {
	i, store := newSeedInterceptor()
	msg := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("x"))
	msg.TaskID = "task-1"
	msg.ContextID = "ctx-1"
	msg.Metadata = map[string]any{
		storedTaskMetadataKey: map[string]any{"state": string(a2a.TaskStateInputRequired)},
	}

	if _, _, err := i.Before(context.Background(), nil, sendReq(msg)); err != nil {
		t.Fatalf("Before: %v", err)
	}
	task, err := store.Get(context.Background(), "task-1")
	if err != nil {
		t.Fatalf("task was not seeded: %v", err)
	}
	if task.Task.Status.State != a2a.TaskStateInputRequired {
		t.Errorf("state = %q, want input-required", task.Task.Status.State)
	}
}

func TestSeedTaskInterceptorPropagatesMetadataError(t *testing.T) {
	i, _ := newSeedInterceptor()
	msg := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("x"))
	msg.TaskID = "task-1"
	msg.Metadata = map[string]any{storedTaskMetadataKey: map[string]any{"state": string(a2a.TaskStateCompleted)}}

	if _, _, err := i.Before(context.Background(), nil, sendReq(msg)); err == nil {
		t.Fatal("want error for invalid stored task state")
	}
}

func TestSeedTaskInterceptorLeavesExistingTask(t *testing.T) {
	i, store := newSeedInterceptor()
	ctx := context.Background()
	existing := &a2a.Task{
		ID:        "task-1",
		ContextID: "ctx-original",
		Status:    a2a.TaskStatus{State: a2a.TaskStateWorking},
	}
	if _, err := store.Create(ctx, existing); err != nil {
		t.Fatalf("seed existing: %v", err)
	}

	msg := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("x"))
	msg.TaskID = "task-1"
	msg.ContextID = "ctx-different"
	if _, _, err := i.Before(ctx, nil, sendReq(msg)); err != nil {
		t.Fatalf("Before: %v", err)
	}

	task, err := store.Get(ctx, "task-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if task.Task.ContextID != "ctx-original" {
		t.Errorf("ContextID = %q, want the stored task to be left alone", task.Task.ContextID)
	}
}

func TestBuildCard(t *testing.T) {
	t.Run("default card", func(t *testing.T) {
		t.Setenv("KAGENT_AGENT_CARD_JSON", "")
		card := buildCard("127.0.0.1:8085")
		if card.Name != "pi-go" {
			t.Errorf("Name = %q, want pi-go", card.Name)
		}
		if len(card.Skills) == 0 {
			t.Error("default card must advertise at least one skill")
		}
		if !card.Capabilities.Streaming {
			t.Error("default card should advertise streaming")
		}
		if len(card.SupportedInterfaces) != 1 {
			t.Fatalf("interfaces = %d, want 1", len(card.SupportedInterfaces))
		}
		if got := card.SupportedInterfaces[0].URL; got != "http://127.0.0.1:8085" {
			t.Errorf("URL = %q, want http://127.0.0.1:8085", got)
		}
	})

	t.Run("injected card wins", func(t *testing.T) {
		t.Setenv("KAGENT_AGENT_CARD_JSON", `{"name":"injected","version":"9.9.9"}`)
		card := buildCard("127.0.0.1:8085")
		if card.Name != "injected" || card.Version != "9.9.9" {
			t.Errorf("card = %+v, want the injected identity", card)
		}
	})

	t.Run("malformed injected card falls back", func(t *testing.T) {
		t.Setenv("KAGENT_AGENT_CARD_JSON", `{"name":`)
		if card := buildCard("127.0.0.1:8085"); card.Name != "pi-go" {
			t.Errorf("Name = %q, want the built-in fallback", card.Name)
		}
	})

	t.Run("whitespace is treated as unset", func(t *testing.T) {
		t.Setenv("KAGENT_AGENT_CARD_JSON", "   ")
		if card := buildCard("127.0.0.1:8085"); card.Name != "pi-go" {
			t.Errorf("Name = %q, want the built-in fallback", card.Name)
		}
	})
}

// TestExecuteRecoversHandlerPanic covers the recover() path added with
// streaming: a panicking prompt turn must surface as a failed task rather
// than taking the server down.
func TestExecuteRecoversHandlerPanic(t *testing.T) {
	h := func(context.Context, acpserver.PromptTurn) (acpserver.PromptResult, error) {
		panic("boom")
	}
	e := &piExecutor{handler: h, logger: discardLogger()}
	events, _ := drain(e.Execute(context.Background(), newExecCtx("hello")))

	got := states(events)
	if len(got) != 2 || got[1] != a2a.TaskStateFailed {
		t.Fatalf("states = %v, want [working failed]", got)
	}
	last := events[len(events)-1].(*a2a.TaskStatusUpdateEvent)
	if txt := extractText(last.Status.Message); !strings.Contains(txt, "handler panicked") {
		t.Errorf("failure text = %q, want it to mention the panic", txt)
	}
}

// TestExecuteReturnsOnContextCancel covers the ctx.Done() branch of the
// streaming loop: a canceled request context stops the iterator.
func TestExecuteReturnsOnContextCancel(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })

	h := func(ctx context.Context, _ acpserver.PromptTurn) (acpserver.PromptResult, error) {
		select {
		case <-ctx.Done():
			return acpserver.PromptResult{}, ctx.Err()
		case <-release:
			return acpserver.PromptResult{}, nil
		}
	}
	e := &piExecutor{handler: h, logger: discardLogger()}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan []a2a.TaskState, 1)
	go func() {
		events, _ := drain(e.Execute(ctx, newExecCtx("hello")))
		done <- states(events)
	}()

	// Let the executor emit Task and Working, then cancel mid-turn.
	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case got := <-done:
		if len(got) == 0 || got[0] != a2a.TaskStateWorking {
			t.Fatalf("states = %v, want the turn to stop after working", got)
		}
		for _, s := range got {
			if s == a2a.TaskStateCompleted {
				t.Error("turn completed despite a canceled context")
			}
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Execute did not return after context cancel")
	}
}

// TestExecuteStopsWhileStreaming covers the cancel() taken when the consumer
// stops reading part-way through streamed artifact events.
func TestExecuteStopsWhileStreaming(t *testing.T) {
	e := &piExecutor{handler: streamingHandler, logger: discardLogger()}

	seen := 0
	e.Execute(context.Background(), newExecCtx("hello"))(func(a2a.Event, error) bool {
		seen++
		// Stop once past Task + Working, i.e. during the streamed artifacts.
		return seen < 3
	})

	if seen != 3 {
		t.Fatalf("yielded %d events, want the iterator to stop at 3", seen)
	}
}
