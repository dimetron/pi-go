# ACP — How the Agent Client Protocol Signals End of Task

*Research deep-dive — 2026-04-19*

This document explains how the **Agent Client Protocol (ACP)** signals the end of a task, in enough mechanical detail to
build or debug either side of the connection. It answers four angles: the wire-level mechanics, the distinction between
cancellation and completion, how to make a "permanent-looking" ACP subagent actually terminate work, and guidance for
clients that need to detect end-of-task reliably.

ACP is a JSON-RPC 2.0 protocol spoken over stdio (newline-delimited), designed by Zed Industries as an editor↔agent
equivalent of what MCP is for agent↔tools.

---

## 1. The unit of work: a *prompt turn*, not a *session*

ACP separates two lifetimes:

- **Session** — a long-lived conversational context created by `session/new` (or resumed via `session/load`). Sessions
  carry history, working directory, MCP configuration, and mode. **There is no `session/close` or `session/end` method
  in the spec.** A session ends implicitly when the underlying process connection terminates.
- **Prompt turn** — a single request/response pair inside a session, driven by `session/prompt`. *This* is the unit that
  has a well-defined, spec-level "end of task" signal.

So when people say "ACP signals end of task," they almost always mean **the prompt turn completing** — not the session
closing. This is the root cause of the "ACP subagent feels permanent" confusion: the session stays open by design, and
only the turn ends.

---

## 2. Anatomy of a prompt turn

The agent-side trait exposes five core methods: `initialize`, `authenticate`, `new_session` (`session/new`),
`load_session` (`session/load`), `prompt` (`session/prompt`), and `cancel` (`session/cancel`). Optional methods include
`set_session_mode` (`session/set_mode`) and vendor extensions.

A prompt turn looks like this on the wire:

```text
Client ── session/prompt (request) ───────────────────▶ Agent
        ◀── session/update (notification) ───── Agent   │
        ◀── session/update (notification) ───── Agent   │   (streamed
        ◀── session/request_permission (req) ── Agent   │    for the
        ── permission response ───────────────▶ Agent   │    whole turn)
        ◀── session/update (notification) ───── Agent   │
        ◀── session/prompt (response) ───────── Agent  END
```

Concretely:

1. **`session/prompt`** — a JSON-RPC **request** carrying `sessionId` and user content blocks (text / image / audio /
   resource). The agent owns the whole turn lifecycle until it responds.
2. **`session/update`** — JSON-RPC **notifications** streamed from the agent to the client during the turn. They carry
   message chunks, tool-call progress, plan updates, and mode changes. Notifications have no response — the client just
   observes.
3. **Client callbacks** — mid-turn the agent may request things back: `fs/read_text_file`, `fs/write_text_file`,
   `terminal/create`, `session/request_permission`, and so on. Each is a request-response round-trip nested inside the
   outer turn.
4. **`session/prompt` response** — the single authoritative "end of task" signal. It carries a **`stopReason`** field.

---

## 3. `stopReason` — the canonical end-of-task field

`stopReason` is a string enum on the `PromptResponse`. The spec defines exactly four values (serde lower_snake_case on
the wire):

| Value        | Meaning                                                                            |
|--------------|------------------------------------------------------------------------------------|
| `end_turn`   | The model finished its response without requesting more tools — normal completion. |
| `max_tokens` | The response was truncated by the model's token limit.                             |
| `refusal`    | The model refused to proceed (policy / safety).                                    |
| `cancelled`  | The turn was cancelled by the client via `session/cancel`.                         |

**This response is the one and only definitive signal.** A client that waits for `session/update` messages to "sound
complete" is doing it wrong — only the `session/prompt` response settles the turn. Everything before it is informational
streaming.

Note on spelling: it's `stopReason` (camelCase JSON key) with `end_turn` / `max_tokens` / `cancelled` (snake_case
values). British-English `cancelled` with two Ls.

---

## 4. Cancellation vs. completion — two different paths to the same response

ACP carefully separates the two.

**Completion** is the natural path. The agent keeps sending `session/update` notifications until it decides the turn is
done, then emits the `session/prompt` response with `stopReason` set to `end_turn`, `max_tokens`, or `refusal`.

**Cancellation** is client-initiated. The client sends a **`session/cancel` notification** (not a request — no response
expected on that message itself). On receipt, the agent MUST:

1. Stop all in-flight language-model requests as fast as possible.
2. Abort any in-progress tool-call invocations.
3. Flush any pending `session/update` notifications already queued.
4. Finally, respond to the outstanding `session/prompt` request with `stopReason: "cancelled"`.

That last step is load-bearing. The cancel notification does **not** itself terminate the turn — it is a polite request.
The turn only ends when the *original* `session/prompt` request gets its response. Agents that fail to close out the
`session/prompt` with `cancelled` leave the client hung.

Complementary client behavior: the spec says the client SHOULD preemptively mark all non-finished tool calls for that
turn as cancelled the moment it sends `session/cancel`, and SHOULD still accept `session/update` tool-call updates
received after cancel (the agent may need to flush them). The agent MAY send such updates after receiving the cancel,
but MUST send them before the final `session/prompt` response.

---

## 5. Making an ACP "subagent" actually terminate work

This is the practical question behind the original ask. In Zed and similar clients, each "subagent" maps to an ACP
session. Sessions are intentionally persistent, so the subagent keeps existing. Only the *turn* ends. Options:

- **Let the turn end naturally** — wait for the `session/prompt` response with `end_turn`. The agent process and session
  stay alive; only that task is done. This is what ACP is designed for.
- **Send `session/cancel`** — if the turn is running too long or heading the wrong way. Then wait for the
  `session/prompt` response with `cancelled`. Don't send another `session/prompt` in the same session until the
  cancelled response arrives — the agent is still draining.
- **Kill the session by killing the process** — ACP has no `session/close`, so the only way to fully terminate a session
  is to drop the transport: close stdio, terminate the agent subprocess. Editors that expose "restart agent" are doing
  exactly this.
- **Create a fresh session per task** — if you want one-shot semantics, don't reuse a session. Call `session/new` for
  each task, run one `session/prompt`, then let the process tear down. That gives you Task-tool-like ephemeral behavior
  on top of a protocol built for persistence.

If you need ephemeral-by-design fan-out (parallel workers, isolated contexts, no memory carryover), don't model each
worker as a top-level ACP session at all. Use the Task tool *inside* a single ACP session — those invocations get
isolated 200k contexts and are truly one-shot.

---

## 6. Guidance for client implementers

If you're writing the client (editor) side:

- **Treat the `session/prompt` response as the authoritative end-of-task.** Don't infer completion from silence on
  `session/update`, and don't infer it from a tool call finishing.
- **Branch on `stopReason`.** `end_turn` → show result. `max_tokens` → offer "continue". `refusal` → surface the model's
  explanation. `cancelled` → confirm the user's interrupt landed.
- **Serialize prompts per session.** Don't issue `session/prompt` N+1 while N is still outstanding; the spec's cancel
  semantics assume one live turn per session.
- **On cancel, update UI optimistically** (mark tool calls cancelled immediately) but keep listening for late
  `session/update`s until the `session/prompt` response arrives.
- **Sessions are yours to manage.** If you want a subagent to "go away," tear down the process; the protocol will not do
  it for you.

---

## 7. TL;DR

End of task in ACP = **the `session/prompt` response arriving with a `stopReason`**. Completion is `end_turn` /
`max_tokens` / `refusal`; cancellation is `cancelled`, triggered by a `session/cancel` notification that the agent must
honor by eventually closing out the original `session/prompt`. Sessions themselves have no end-of-task signal — they
live until the connection dies. That's the design, and it's why ACP subagents feel "permanent."

---

## Sources

- [Agent Client Protocol — Prompt Turn](https://agentclientprotocol.com/protocol/prompt-turn) *(spec, egress-blocked
  from this sandbox but canonical)*
- [StopReason enum — docs.rs](https://docs.rs/agent-client-protocol-schema/latest/agent_client_protocol_schema/enum.StopReason.html)
- [Agent trait — docs.rs](https://docs.rs/agent-client-protocol/latest/agent_client_protocol/trait.Agent.html)
- [ACPex — Protocol Overview](https://hexdocs.pm/acpex/protocol_overview.html)
- [Hermes-agent ACP server-mode discussion (#569)](https://github.com/NousResearch/hermes-agent/issues/569)
- [agent-client-protocol GitHub repo](https://github.com/zed-industries/agent-client-protocol)
- [mcacp — MCP↔ACP bridge](https://github.com/Oortonaut/mcacp)
