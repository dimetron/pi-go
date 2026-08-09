# Topic 6 — Long sessions are not routed to subagents

## Research

78 sessions with more than 100 turns hold **49.6% of all prompt tokens**
(1,256,963,127 of 2,536,063,043).

| turns | sessions | prompt tokens | share | avg/session |
|---|---|---|---|---|
| 1–10 | 673 | 64,192,731 | 2.5% | 95,382 |
| 11–30 | 356 | 225,895,893 | 8.9% | 634,539 |
| 31–60 | 204 | 454,765,021 | 17.9% | 2,229,240 |
| 61–100 | 93 | 534,246,271 | 21.1% | 5,744,583 |
| **100+** | **78** | **1,256,963,127** | **49.6%** | 16,114,911 |

Those same 78 sessions made **61 subagent calls** between them, against 407
across all shorter sessions. The sessions that most need work pushed into a
child context use that mechanism least.

The subagent infrastructure exists and is wired into the interactive path:
`subagent.NewOrchestrator` (`interactive.go:278`), agent discovery via
`subagent.DiscoverAgents` (`interactive.go:217`), and ACP subagent event capture
(`interactive.go:468`). The gap is that nothing *encourages* the model to use it
in long sessions.

## Recommendations

1. **Add a system-prompt rule for long sessions.** When a session exceeds a
   turn threshold (e.g. 60 turns), instruct the model to push self-contained
   work — a bounded refactor, a search, a test run — into a subagent context so
   the parent context stops growing quadratically. This is a prompt-level change
   in `agent.LoadInstructionParts` / `agent.SystemInstruction`.
2. **Make the threshold dynamic.** The 60-turn figure is a starting point; the
   real signal is context growth. Consider gating on `Tracker.BodyTokens()` /
   `ContextPercentUsed()` (topic 2) rather than a raw turn count, so the rule
   fires when the context is actually under pressure.
3. **Surface subagent availability in the instruction.** The model may not know
   subagents are available or when they are appropriate. Explicitly list the
   subagent tool and the conditions under which to use it.
4. **Measure the effect.** The `pi tokens` replay (topic 7) should break out
   subagent usage per session so the routing rule's impact on the 49.6% is
   directly observable.

## Expected impact

Attacks the 49.6% concentration directly. Pushing work into a child context stops
the parent's quadratic growth — the same mechanism that makes batching (topic 1)
effective, applied at the session level.

## Risk

Medium. Subagent calls have their own overhead (a fresh context, no parent
history), so over-routing could hurt. The rule should be conservative and
gated on actual context pressure (recommendation 2) rather than a hard turn
count.
