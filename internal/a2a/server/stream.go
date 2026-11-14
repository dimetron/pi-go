package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	acp "github.com/coder/acp-go-sdk"

	"github.com/dimetron/pi-go/internal/acp/server/adapter"
)

// kagent's shared A2A metadata keys (kagent go/api/a2a/metadata.go). The
// kagent UI classifies a data part as a tool call or result by
// kagentPartTypeKey alone — a part without it renders as unrecognized data,
// not as a tool card — and orders a reloaded task by kagentTimelineKey when
// every entry carries one.
const (
	kagentPartTypeKey = "kagent.dev/a2a/part-type"
	kagentThoughtKey  = "kagent.dev/a2a/thought"
	kagentTimelineKey = "kagent.dev/a2a/timeline-position"
)

// ADK metadata keys, matching what kagent's ADK executor writes on A2A parts
// (google.golang.org/adk/v2/server/adka2a/v2). kagent no longer reads them;
// they are kept so ADK-based clients can still round-trip the parts.
const (
	adkMetaTypeKey    = "adk_type"
	adkMetaThoughtKey = "adk_thought"
)

const (
	partTypeFunctionCall     = "function_call"
	partTypeFunctionResponse = "function_response"
)

// a2aUpdater implements acpserver.SessionUpdater by translating ACP session
// updates into A2A TaskArtifactUpdateEvents, mirroring the artifacts kagent's
// Claude and Codex harnesses emit (kagent go/harness/runtime/a2a/executor.go):
//
//   - agent_message_chunk  → text artifact (append to one artifact per run)
//   - agent_thought_chunk  → text artifact marked thought (append)
//   - tool_call start      → data part {id, name, args}      part-type=function_call
//   - tool_call terminal   → data part {id, name, response}  part-type=function_response
//
// Events are pushed to a channel that piExecutor.Execute drains and yields to
// the A2A client, so streaming reaches the UI as it happens rather than in one
// artifact at the end of the turn.
type a2aUpdater struct {
	ctx     context.Context
	execCtx *a2asrv.ExecutorContext
	events  chan a2a.Event

	mu                sync.Mutex
	textArtifactID    a2a.ArtifactID
	thoughtArtifactID a2a.ArtifactID
	textPosition      time.Time       // timeline position of the open text artifact
	thoughtPosition   time.Time       // timeline position of the open thinking artifact
	textBuf           strings.Builder // accumulated assistant text for the open artifact
	thoughtBuf        strings.Builder // accumulated thinking for the open artifact
	streamedAnyText   bool
	toolNames         map[string]string // ACP call ID → tool name
	lastPosition      time.Time
}

func newA2AUpdater(ctx context.Context, execCtx *a2asrv.ExecutorContext) *a2aUpdater {
	return &a2aUpdater{
		ctx:       ctx,
		execCtx:   execCtx,
		events:    make(chan a2a.Event, 64),
		toolNames: map[string]string{},
	}
}

// Update implements acpserver.SessionUpdater.
func (u *a2aUpdater) Update(ctx context.Context, update acp.SessionUpdate) error {
	switch {
	case update.AgentMessageChunk != nil:
		return u.emitText(ctx, update.AgentMessageChunk.Content, false)
	case update.AgentThoughtChunk != nil:
		return u.emitText(ctx, update.AgentThoughtChunk.Content, true)
	case update.ToolCall != nil:
		return u.emitToolStart(ctx, update.ToolCall)
	case update.ToolCallUpdate != nil:
		return u.emitToolEnd(ctx, update.ToolCallUpdate)
	}
	return nil
}

// streamedText reports whether any assistant text artifact was emitted this
// turn. The executor uses it to decide whether the final artifact would
// duplicate text the client already received as chunks.
func (u *a2aUpdater) streamedText() bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.streamedAnyText
}

// emitText streams one text chunk, following the artifact protocol kagent's
// own ADK runtime emits (google.golang.org/adk/v2 server/adka2a/v2, and the
// run captured in kagent's ui/src/api/chat/a2aGrpcChatClient.ts):
//
//	artifactUpdate  id=A  "alpha"              (no append)  first chunk
//	artifactUpdate  id=A  " beta"              append:true  delta
//	artifactUpdate  id=A  " gamma"             append:true  delta
//	artifactUpdate  id=A  "alpha beta gamma"   lastChunk    full replacement
//
// The deltas are what let the UI stream a reply token by token: it yields a
// "delta" for an append and grows one message. The closing frame replaces the
// artifact with the whole text, which both settles the rendered message and
// leaves the stored task holding a single part — reloading history then shows
// one message rather than one per token.
func (u *a2aUpdater) emitText(ctx context.Context, content acp.ContentBlock, thought bool) error {
	text := ""
	if content.Text != nil {
		text = content.Text.Text
	}
	if text == "" {
		return nil
	}

	u.mu.Lock()
	buf, id := &u.textBuf, u.textArtifactID
	if thought {
		buf, id = &u.thoughtBuf, u.thoughtArtifactID
	}
	buf.WriteString(text)

	// The chunk itself, not the accumulation: an append adds to what was sent.
	var ev *a2a.TaskArtifactUpdateEvent
	if id == "" {
		ev = a2a.NewArtifactEvent(u.execCtx, a2a.NewTextPart(text))
		position := u.nextPositionLocked()
		setTimelinePosition(ev.Artifact, position)
		if thought {
			u.thoughtArtifactID, u.thoughtPosition = ev.Artifact.ID, position
		} else {
			u.textArtifactID, u.textPosition = ev.Artifact.ID, position
			u.streamedAnyText = true
		}
	} else {
		ev = a2a.NewArtifactUpdateEvent(u.execCtx, id, a2a.NewTextPart(text))
	}
	if thought {
		markThought(ev.Artifact.Parts[0])
	}
	u.mu.Unlock()

	return u.emit(ctx, ev)
}

// closeTextArtifacts settles the open text and thinking artifacts: each is
// replaced with the full text streamed into it and marked as the last chunk,
// so the next chunk starts a new artifact.
//
// Called at every tool-call boundary and once more when the turn ends. Without
// the boundary calls, text produced after a tool call would keep growing the
// artifact opened before it and render above the tool card instead of below;
// without the replacement, the stored task keeps one part per token.
func (u *a2aUpdater) closeTextArtifacts(ctx context.Context) error {
	u.mu.Lock()
	type pending struct {
		id       a2a.ArtifactID
		text     string
		thought  bool
		position time.Time
	}
	var closing []pending
	if u.textArtifactID != "" {
		closing = append(closing, pending{u.textArtifactID, u.textBuf.String(), false, u.textPosition})
	}
	if u.thoughtArtifactID != "" {
		closing = append(closing, pending{u.thoughtArtifactID, u.thoughtBuf.String(), true, u.thoughtPosition})
	}
	u.textArtifactID, u.thoughtArtifactID = "", ""
	u.textBuf.Reset()
	u.thoughtBuf.Reset()
	u.mu.Unlock()

	for _, c := range closing {
		if c.text == "" {
			continue
		}
		ev := a2a.NewArtifactUpdateEvent(u.execCtx, c.id, a2a.NewTextPart(c.text))
		// Replace rather than append: this frame carries the whole text.
		ev.Append = false
		ev.LastChunk = true
		// A replacement replaces the stored artifact's metadata too, so the
		// position the first frame set has to be carried again.
		setTimelinePosition(ev.Artifact, c.position)
		if c.thought {
			markThought(ev.Artifact.Parts[0])
		}
		if err := u.emit(ctx, ev); err != nil {
			return err
		}
	}
	return nil
}

// emitToolStart streams a tool invocation as a function_call data part,
// named by the registered tool name as kagent's Claude and Codex harnesses
// name theirs. The ACP title folds the main argument into a display string
// ("git status -s" for a bash call); it is only the fallback for a start that
// does not carry the name, since the card already shows the arguments.
func (u *a2aUpdater) emitToolStart(ctx context.Context, tc *acp.SessionUpdateToolCall) error {
	name, _ := tc.Meta[adapter.ToolNameMetaKey].(string)
	if name == "" {
		name = tc.Title
	}
	if name == "" {
		name = "tool"
	}

	if err := u.closeTextArtifacts(ctx); err != nil {
		return err
	}

	args, err := jsonValue(tc.RawInput)
	if err != nil {
		return fmt.Errorf("a2a: encode tool %q args: %w", name, err)
	}
	if args == nil {
		args = map[string]any{}
	}

	u.mu.Lock()
	u.toolNames[string(tc.ToolCallId)] = name
	u.mu.Unlock()

	return u.emitToolArtifact(ctx, partTypeFunctionCall, map[string]any{
		"id":   string(tc.ToolCallId),
		"name": name,
		"args": args,
	})
}

// emitToolEnd streams a tool result as a function_response data part.
//
// Only a terminal update is a result. ACP also sends non-terminal updates for
// a running call — a subagent's card is re-sent with each nested call it
// makes — and treating those as results rendered a nameless result card per
// nested call and orphaned the real one.
func (u *a2aUpdater) emitToolEnd(ctx context.Context, tu *acp.SessionToolCallUpdate) error {
	if tu.Status == nil ||
		(*tu.Status != acp.ToolCallStatusCompleted && *tu.Status != acp.ToolCallStatusFailed) {
		return nil
	}

	if err := u.closeTextArtifacts(ctx); err != nil {
		return err
	}

	u.mu.Lock()
	name := u.toolNames[string(tu.ToolCallId)]
	delete(u.toolNames, string(tu.ToolCallId))
	u.mu.Unlock()
	if name == "" {
		name = "tool"
	}

	result, err := jsonValue(tu.RawOutput)
	if err != nil {
		return fmt.Errorf("a2a: encode tool %q result: %w", name, err)
	}
	// The shape kagent's harness writes and its tool card reads.
	response := map[string]any{"result": result}
	if *tu.Status == acp.ToolCallStatusFailed {
		response["isError"] = true
	}
	return u.emitToolArtifact(ctx, partTypeFunctionResponse, map[string]any{
		"id":       string(tu.ToolCallId),
		"name":     name,
		"response": response,
	})
}

// emitToolArtifact emits one tool part as a complete artifact of its own,
// tagged with both kagent's part-type key and the legacy ADK one.
func (u *a2aUpdater) emitToolArtifact(ctx context.Context, partType string, data map[string]any) error {
	part := a2a.NewDataPart(data)
	part.SetMeta(kagentPartTypeKey, partType)
	part.SetMeta(adkMetaTypeKey, partType)

	ev := a2a.NewArtifactEvent(u.execCtx, part)
	ev.LastChunk = true
	u.mu.Lock()
	setTimelinePosition(ev.Artifact, u.nextPositionLocked())
	u.mu.Unlock()
	return u.emit(ctx, ev)
}

// nextPositionLocked returns a strictly increasing timeline position, so two
// artifacts emitted within one clock tick still sort in emission order.
// Caller must hold mu.
func (u *a2aUpdater) nextPositionLocked() time.Time {
	position := time.Now().UTC()
	if !position.After(u.lastPosition) {
		position = u.lastPosition.Add(time.Nanosecond)
	}
	u.lastPosition = position
	return position
}

// nextPosition is nextPositionLocked for a caller that does not hold mu.
func (u *a2aUpdater) nextPosition() time.Time {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.nextPositionLocked()
}

// timelineLayout is RFC 3339 with a fixed nine-digit fraction. The kagent UI
// orders positions by string comparison, and time.RFC3339Nano trims trailing
// zeros — ".33171Z" would sort before ".331709Z". Fixed width keeps string
// order equal to time order, and the value still parses as RFC3339Nano.
const timelineLayout = "2006-01-02T15:04:05.000000000Z07:00"

// setTimelinePosition records kagent's task-timeline ordering key.
func setTimelinePosition(carrier a2a.MetadataCarrier, position time.Time) {
	carrier.SetMeta(kagentTimelineKey, position.UTC().Format(timelineLayout))
}

// markThought tags a text part as reasoning rather than the answer, under
// kagent's key and the legacy ADK one.
func markThought(part *a2a.Part) {
	part.SetMeta(kagentThoughtKey, true)
	part.SetMeta(adkMetaThoughtKey, true)
}

// jsonValue converts v to plain JSON values. Tool inputs and results can be Go
// structs, and an A2A data part is converted to a protobuf Struct on the gRPC
// path, which accepts only maps, slices and scalars.
func jsonValue(v any) (any, error) {
	if v == nil {
		return nil, nil
	}
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var out any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// emit pushes one event to the executor's drain channel, aborting when the
// run context is canceled (the client stopped pulling).
func (u *a2aUpdater) emit(ctx context.Context, ev a2a.Event) error {
	select {
	case u.events <- ev:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
