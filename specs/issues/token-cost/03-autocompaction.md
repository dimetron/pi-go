# Topic 3 — Auto-compaction has effectively never run

## Research

Detecting compaction by its signature (a >30% drop in `promptTokenCount`
between consecutive calls):

```
sessions >= 5 turns:                       1,046
sessions showing a compaction drop:            9   (0.9%)
```

Zero drops in `minimax-m3:cloud` (266 sessions), `deepseek-v4-flash:0731-cloud`
(51), `gpt-5.5` (111), or `glm-5.2:cloud` (140).

**Caveat on the detection heuristic:** a >30% prompt drop is an imperfect proxy
for compaction. A shed pass may remove less than 30% of the prompt, and
subsequent context growth can mask even a larger removal in the next recorded
prompt count. So the 0.9% figure is a lower bound on how often compaction ran,
not a precise count. The root-cause analysis below (threshold above the observed
ceiling) is the stronger evidence; the drop heuristic alone cannot prove
shedding never ran. To confirm, replay `Decide` and `AutoCompact` against each
session's actual body/window values, or read persisted compaction outcomes.

### Root cause: threshold arithmetic

`DefaultAutoCompactConfig` (`internal/session/compaction.go:80-88`) sets
`ShedPercent: 60`. Against a ~1 M context window that triggers at 600k tokens.
**The largest context ever observed across all 1,404 sessions is 513,673
tokens.** The threshold is above the ceiling.

The `Decide` logic (`compaction.go:123-137`) is otherwise sound: it measures
`bodyTokens` (context after the stable cached prefix) against the window, and
returns `CompactionNone` when the window is unknown. The problem is purely the
default threshold value relative to real session sizes.

### Why `shed` matters most

`shed` is the cheap half. Per the design comment at `compaction.go:16-25`,
shedding drops payloads of tool results superseded by a later call on the same
target — **no LLM call required**. It has never run.

Simulating continuous shedding (stub superseded payloads as soon as they are
superseded, `KeepRecentEvents: 10` respected, 120-byte stub cost):

```
actual prompt tokens:          2,522,163,099
saved by continuous shedding:    170,543,907   (6.8%)
```

Per-session the win is much larger where it counts — 22.7%, 22.6%, 26.6%, 37.2%
on the heaviest sessions.

### The caveat that shapes the fix

Shedding rewrites history mid-prefix, which invalidates the cache suffix. Where
caching works, a cache read costs ~10% of a fresh token, so shedding *X* tokens
is only profitable when:

```
X × 0.1 × remaining_turns  >  0.9 × context_size
```

So shed should be **gated on observed cache behaviour** rather than run
unconditionally — which is why topic 2 (cache reporting) must be fixed first:
without it, the gate has no cache numbers to read on the 73% of Ollama traffic.

## Recommendations

1. **Lower `ShedPercent` so it can actually fire.** The observed ceiling is
   ~513k tokens. A shed threshold that triggers well below that — e.g. 40–50%
   of the window — makes shedding reachable. (Note: `BodyTokens()` is not
   corrupted by the cache gap — see 02 — so this tuning does not depend on the
   cache fix; only the shed *gate* in recommendation 3 does.)
2. **Make `shed` continuous, not threshold-gated.** The simulation shows the
   win comes from shedding superseded payloads *as soon as they are superseded*,
   not from waiting for a threshold. Consider running the shed pass on every
   turn (or every N turns) rather than only when `Decide` returns `CompactionShed`.
3. **Gate shed on observed cache behaviour.** Use `Tracker.LastCachedTokens()`
   / `CachePrefixTokens()` to decide whether shedding is profitable for the
   current route. On routes with no cache (the 73% from topic 2), shed freely;
   on well-cached routes, apply the profitability inequality above.
4. **Keep `summarize` as the expensive, rare fallback.** It invalidates the
   prompt cache and costs an LLM call; it should stay reserved for a nearly-full
   window. The current `SummarizePercent: 90` is reasonable once shed is doing
   its job.

## Expected impact

6.8% overall from continuous shedding, up to 22–37% on the heaviest sessions.
More importantly, it makes the compaction machinery actually reachable, so the
safety valve exists when a session does approach the window.

## Risk

Medium. Shedding rewrites history mid-prefix and can invalidate the cache suffix
on well-cached routes. Mitigate with recommendation 3 (gate on observed cache).
Sequencing: the shed *gate* must follow topic 2 so it has real cache numbers;
the threshold tuning itself does not depend on topic 2.
