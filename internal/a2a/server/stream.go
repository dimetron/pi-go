package server

import (
	"context"
	"strings"
	"sync"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	acp "github.com/coder/acp-go-sdk"
)

// ADK metadata keys, matching what kagent's ADK executor writes on A2A parts
// (google.golang.org/adk/v2/server/adka2a/v2). The kagent UI classifies data
// parts by shape ({name,args} vs {name,response}) rather than by these keys,
// but keeping them lets ADK-based clients round-trip the parts.
const (
	adkMetaTypeKey    = "adk_type"
	adkMetaThoughtKey = "adk_thought"
)

// a2aUpdater implements acpserver.SessionUpdater by translating ACP session
// updates into A2A TaskArtifactUpdateEvents, mirroring how kagent's ADK
// executor converts ADK session events to A2A artifacts:
//
//   - agent_message_chunk  → text artifact (append to one artifact per turn)
//   - agent_thought_chunk  → text artifact marked adk_thought (append)
//   - tool_call start      → data part {name, args, id}  adk_type=function_call
//   - tool_call update     → data part {name, response}  adk_type=function_response
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
	textBuf           strings.Builder // accumulated assistant text for the open artifact
	thoughtBuf        strings.Builder // accumulated thinking for the open artifact
	streamedAnyText   bool
	toolNames         map[string]string // ACP call ID → tool name
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
		if thought {
			u.thoughtArtifactID = ev.Artifact.ID
		} else {
			u.textArtifactID = ev.Artifact.ID
			u.streamedAnyText = true
		}
	} else {
		ev = a2a.NewArtifactUpdateEvent(u.execCtx, id, a2a.NewTextPart(text))
	}
	if thought {
		ev.Artifact.Parts[0].SetMeta(adkMetaThoughtKey, true)
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
		id      a2a.ArtifactID
		text    string
		thought bool
	}
	var closing []pending
	if u.textArtifactID != "" {
		closing = append(closing, pending{u.textArtifactID, u.textBuf.String(), false})
	}
	if u.thoughtArtifactID != "" {
		closing = append(closing, pending{u.thoughtArtifactID, u.thoughtBuf.String(), true})
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
		if c.thought {
			ev.Artifact.Parts[0].SetMeta(adkMetaThoughtKey, true)
		}
		if err := u.emit(ctx, ev); err != nil {
			return err
		}
	}
	return nil
}

// emitToolStart streams a tool invocation as a function_call data part. The
// ACP update carries the human-readable title (e.g. "bash git status -s"),
// which is what the kagent UI shows as the card header; the raw args ride in
// RawInput. The title is remembered so the matching result carries the same
// name.
func (u *a2aUpdater) emitToolStart(ctx context.Context, tc *acp.SessionUpdateToolCall) error {
	name := tc.Title
	if name == "" {
		name = "tool"
	}

	if err := u.closeTextArtifacts(ctx); err != nil {
		return err
	}

	u.mu.Lock()
	u.toolNames[string(tc.ToolCallId)] = name
	u.mu.Unlock()

	part := a2a.NewDataPart(map[string]any{
		"name": name,
		"args": tc.RawInput,
		"id":   string(tc.ToolCallId),
	})
	part.SetMeta(adkMetaTypeKey, "function_call")
	return u.emit(ctx, a2a.NewArtifactEvent(u.execCtx, part))
}

// emitToolEnd streams a tool result as a function_response data part.
func (u *a2aUpdater) emitToolEnd(ctx context.Context, tu *acp.SessionToolCallUpdate) error {
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

	part := a2a.NewDataPart(map[string]any{
		"name":     name,
		"response": tu.RawOutput,
	})
	part.SetMeta(adkMetaTypeKey, "function_response")
	return u.emit(ctx, a2a.NewArtifactEvent(u.execCtx, part))
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
