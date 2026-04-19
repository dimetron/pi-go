# Claude Models & Advisor Tool

> Source: [Advisor tool - Claude API Docs](https://platform.claude.com/docs/en/agents-and-tools/tool-use/advisor-tool)

---

## Advisor Tool (Beta)

Pair a faster executor model with a higher-intelligence advisor model that provides strategic guidance mid-generation.

The advisor tool lets a faster, lower-cost executor model consult a higher-intelligence advisor model mid-generation for
strategic guidance. The advisor reads the full conversation, produces a plan or course correction (typically 400 to 700
text tokens, 1,400 to 1,800 tokens total including thinking), and the executor continues with the task.

This pattern fits long-horizon agentic workloads (coding agents, computer use, multi-step research pipelines) where most
turns are mechanical but having an excellent plan is crucial. You get close to advisor-solo quality while the bulk of
token generation happens at executor-model rates.

**Beta header required:** `advisor-tool-2026-03-01`

### When to use it

Early benchmarks show meaningful gains for these configurations:

- You currently use Sonnet on complex tasks: Add Opus as the advisor for a quality lift at similar or lower total cost.
- You currently use Haiku and want a step up in intelligence: Add Opus as the advisor. Expect higher cost than Haiku
  alone, but lower than switching the executor to a larger model.

The advisor is a weaker fit for:

- Single-turn Q&A (nothing to plan)
- Pure pass-through model pickers where your users already choose their own cost and quality tradeoff
- Workloads where every turn genuinely requires the advisor model's full capability

---

## Model Compatibility

| Executor models                                | Advisor models                      |
|------------------------------------------------|-------------------------------------|
| Claude Haiku 4.5 (`claude-haiku-4-5-20251001`) | Claude Opus 4.7 (`claude-opus-4-7`) |
| Claude Sonnet 4.6 (`claude-sonnet-4-6`)        | Claude Opus 4.7 (`claude-opus-4-7`) |
| Claude Opus 4.6 (`claude-opus-4-6`)            | Claude Opus 4.7 (`claude-opus-4-7`) |
| Claude Opus 4.7 (`claude-opus-4-7`)            | Claude Opus 4.7 (`claude-opus-4-7`) |

> The advisor must be at least as capable as the executor.

---

## Quick Start

### curl

```bash
curl https://api.anthropic.com/v1/messages \
    --header "x-api-key: $ANTHROPIC_API_KEY" \
    --header "anthropic-version: 2023-06-01" \
    --header "anthropic-beta: advisor-tool-2026-03-01" \
    --header "content-type: application/json" \
    --data '{
        "model": "claude-sonnet-4-6",
        "max_tokens": 4096,
        "tools": [
            {
                "type": "advisor_20260301",
                "name": "advisor",
                "model": "claude-opus-4-7"
            }
        ],
        "messages": [{
            "role": "user",
            "content": "Build a concurrent worker pool in Go with graceful shutdown."
        }]
    }'
```

### Python

```python
import anthropic

client = anthropic.Anthropic()

response = client.beta.messages.create(
    model="claude-sonnet-4-6",
    max_tokens=4096,
    betas=["advisor-tool-2026-03-01"],
    tools=[
        {
            "type": "advisor_20260301",
            "name": "advisor",
            "model": "claude-opus-4-7",
        }
    ],
    messages=[
        {
            "role": "user",
            "content": "Build a concurrent worker pool in Go with graceful shutdown.",
        }
    ],
)

print(response)
```

### TypeScript

```typescript
import Anthropic from "@anthropic-ai/sdk";

const client = new Anthropic();

async function main() {
    const response = await client.beta.messages.create({
        model: "claude-sonnet-4-6",
        max_tokens: 4096,
        betas: ["advisor-tool-2026-03-01"],
        tools: [
            {
                type: "advisor_20260301",
                name: "advisor",
                model: "claude-opus-4-7"
            }
        ],
        messages: [
            {
                role: "user",
                content: "Build a concurrent worker pool in Go with graceful shutdown."
            }
        ]
    });

    console.log(response);
}

main().catch(console.error);
```

### Go

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/anthropics/anthropic-sdk-go"
)

func main() {
	client := anthropic.NewClient()

	response, err := client.Beta.Messages.New(context.TODO(), anthropic.BetaMessageNewParams{
		Model:     anthropic.ModelClaudeSonnet4_6,
		MaxTokens: 4096,
		Tools: []anthropic.BetaToolUnionParam{
			{OfAdvisorTool20260301: &anthropic.BetaAdvisorTool20260301Param{
				Model: anthropic.ModelClaudeOpus4_7,
			}},
		},
		Messages: []anthropic.BetaMessageParam{
			anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock("Build a concurrent worker pool in Go with graceful shutdown.")),
		},
		Betas: []anthropic.AnthropicBeta{
			anthropic.AnthropicBetaAdvisorTool2026_03_01,
		},
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(response)
}
```

---

## How It Works

When you add the advisor tool to your `tools` array, the executor model decides when to call it, just like any other
tool. When the executor invokes the advisor:

1. The executor emits a `server_tool_use` block with `name: "advisor"` and an empty `input`. The executor signals
   timing; the server supplies context.
2. Anthropic runs a separate inference pass on the advisor model server-side, passing the executor's full transcript.
   The advisor sees the system prompt, all tool definitions, all prior turns, and all prior tool results.
3. The advisor's response returns to the executor as an `advisor_tool_result` block.
4. The executor continues generating, informed by the advice.

All of this happens inside a single `/v1/messages` request. No extra round trips on your side.

The advisor itself runs without tools and without context management. Its thinking blocks are dropped before the result
returns; only the advice text reaches the executor.

---

## Tool Parameters

| Parameter  | Type    | Default   | Description                                                                                                                                                                                                                                            |
|------------|---------|-----------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `type`     | string  | required  | Must be `"advisor_20260301"`.                                                                                                                                                                                                                          |
| `name`     | string  | required  | Must be `"advisor"`.                                                                                                                                                                                                                                   |
| `model`    | string  | required  | The advisor model ID, such as `"claude-opus-4-7"`. Billed at this model's rates for the sub-inference.                                                                                                                                                 |
| `max_uses` | integer | unlimited | Maximum number of advisor calls allowed in a single request. Once the executor reaches this cap, further advisor calls return an `advisor_tool_result_error` with `error_code: "max_uses_exceeded"` and the executor continues without further advice. |
| `caching`  | object  | null      | Enables prompt caching for the advisor's own transcript across calls within a conversation. Shape: `{"type": "ephemeral", "ttl": "5m"                                                                                                                  | "1h"}`. |

---

## Response Structure

### Successful Advisor Call

```json
{
  "role": "assistant",
  "content": [
    {
      "type": "text",
      "text": "Let me consult the advisor on this."
    },
    {
      "type": "server_tool_use",
      "id": "srvtoolu_abc123",
      "name": "advisor",
      "input": {}
    },
    {
      "type": "advisor_tool_result",
      "tool_use_id": "srvtoolu_abc123",
      "content": {
        "type": "advisor_result",
        "text": "Use a channel-based coordination pattern. The tricky part is draining in-flight work during shutdown..."
      }
    },
    {
      "type": "text",
      "text": "Here's the implementation. I'm using a channel-based coordination pattern..."
    }
  ]
}
```

### Result Variants

| Variant                   | Fields              | Returned when                                                       |
|---------------------------|---------------------|---------------------------------------------------------------------|
| `advisor_result`          | `text`              | The advisor model returns plaintext (for example, Claude Opus 4.7). |
| `advisor_redacted_result` | `encrypted_content` | The advisor model returns encrypted output.                         |

### Error Results

```json
{
  "type": "advisor_tool_result",
  "tool_use_id": "srvtoolu_abc123",
  "content": {
    "type": "advisor_tool_result_error",
    "error_code": "overloaded"
  }
}
```

| `error_code`              | Meaning                                                     |
|---------------------------|-------------------------------------------------------------|
| `max_uses_exceeded`       | The request reached the `max_uses` cap.                     |
| `too_many_requests`       | The advisor sub-inference was rate-limited.                 |
| `overloaded`              | The advisor sub-inference hit capacity limits.              |
| `prompt_too_long`         | The transcript exceeded the advisor model's context window. |
| `execution_time_exceeded` | The advisor sub-inference timed out.                        |
| `unavailable`             | Any other advisor failure.                                  |

---

## Multi-turn Conversations

Pass the full assistant content, including `advisor_tool_result` blocks, back to the API on subsequent turns:

```python
messages.append({"role": "assistant", "content": response.content})
messages.append({"role": "user", "content": "Now add a max-in-flight limit of 10."})

response = client.beta.messages.create(
    model="claude-sonnet-4-6",
    max_tokens=4096,
    betas=["advisor-tool-2026-03-01"],
    tools=tools,
    messages=messages,
)
```

> **Important:** If you omit the advisor tool from `tools` on a follow-up turn while the message history still contains
`advisor_tool_result` blocks, the API returns a `400 invalid_request_error`.

### Conversation-level Cost Control

The advisor tool has no built-in conversation-level cap. To limit advisor calls across a conversation:

1. Count them client-side.
2. When you reach your ceiling, remove the advisor tool from your `tools` array.
3. Strip all `advisor_tool_result` blocks from your message history to avoid a `400 invalid_request_error`.

---

## Streaming

The advisor sub-inference does not stream. The executor's stream pauses while the advisor runs, then the full result
arrives in a single event.

- The `server_tool_use` block with `name: "advisor"` signals that an advisor call is starting.
- During the pause, the stream is quiet except for standard SSE `ping` keepalives emitted roughly every 30 seconds.
- When the advisor finishes, the `advisor_tool_result` arrives fully formed in a single `content_block_start` event (no
  deltas).
- A `message_delta` event follows with the updated `usage.iterations` array reflecting the advisor's token counts.

---

## Usage and Billing

Advisor calls run as a separate sub-inference billed at the advisor model's rates. Usage is reported in the
`usage.iterations[]` array:

```json
{
  "usage": {
    "input_tokens": 412,
    "cache_read_input_tokens": 0,
    "cache_creation_input_tokens": 0,
    "output_tokens": 531,
    "iterations": [
      {
        "type": "message",
        "input_tokens": 412,
        "cache_read_input_tokens": 0,
        "cache_creation_input_tokens": 0,
        "output_tokens": 89
      },
      {
        "type": "advisor_message",
        "model": "claude-opus-4-7",
        "input_tokens": 823,
        "cache_read_input_tokens": 0,
        "cache_creation_input_tokens": 0,
        "output_tokens": 1612
      },
      {
        "type": "message",
        "input_tokens": 1348,
        "cache_read_input_tokens": 412,
        "cache_creation_input_tokens": 0,
        "output_tokens": 442
      }
    ]
  }
}
```

> **Important:** Top-level `usage` fields reflect executor tokens only. Advisor tokens are not rolled into the top-level
> totals because they are billed at a different rate.
>
> Advisor output is typically 400 to 700 text tokens, or 1,400 to 1,800 tokens total including thinking. The cost
> savings come from the advisor not generating your full final output; the executor does that at its lower rate.
>
> The top-level `max_tokens` applies to executor output only. It does not bound advisor sub-inference tokens.

---

## Advisor Prompt Caching

There are two independent caching layers.

### Executor-side Caching

The `advisor_tool_result` block is cacheable like any other content block. A `cache_control` breakpoint placed after it
on a subsequent turn will hit.

### Advisor-side Caching

Set `caching` on the tool definition to enable prompt caching for the advisor's own transcript across calls:

```python
tools = [
    {
        "type": "advisor_20260301",
        "name": "advisor",
        "model": "claude-opus-4-7",
        "caching": {"type": "ephemeral", "ttl": "5m"},
    }
]
```

**When to enable it:** The cache write costs more than the reads save when the advisor is called two or fewer times per
conversation. Caching breaks even at roughly three advisor calls and improves from there.

**Keep it consistent:** Set `caching` once and leave it for the whole conversation. Toggling it off and on
mid-conversation causes cache misses.

> **Warning:** `clear_thinking` with a `keep` value other than `"all"` shifts the advisor's quoted transcript each turn,
> causing advisor-side cache misses. Set `keep: "all"` to preserve advisor cache stability.

---

## Combining with Other Tools

```python
tools = [
    {
        "type": "web_search_20250305",
        "name": "web_search",
        "max_uses": 5,
    },
    {
        "type": "advisor_20260301",
        "name": "advisor",
        "model": "claude-opus-4-7",
    },
    {
        "name": "run_bash",
        "description": "Run a bash command",
        "input_schema": {
            "type": "object",
            "properties": {"command": {"type": "string"}},
        },
    },
]
```

| Feature          | Interaction                                                                 |
|------------------|-----------------------------------------------------------------------------|
| Batch processing | Supported. `usage.iterations` is reported per item.                         |
| Token counting   | Returns the executor's first-iteration input tokens only.                   |
| Context editing  | `clear_tool_uses` is not yet fully compatible with advisor tool blocks.     |
| `pause_turn`     | A dangling advisor call ends the response with `stop_reason: "pause_turn"`. |

---

## Best Practices

### Prompting for Coding and Agent Tasks

The advisor tool ships with a built-in description that nudges the executor to call it near the start of complex tasks
and when it hits difficulty.

**Timing guidance:**

```
You have access to an `advisor` tool backed by a stronger reviewer model. It takes NO parameters — when you call advisor(), your entire conversation history is automatically forwarded. They see the task, every tool call you've made, every result you've seen.

Call advisor BEFORE substantive work — before writing, before committing to an interpretation, before building on an assumption. If the task requires orientation first (finding files, fetching a source, seeing what's there), do that, then call advisor. Orientation is not substantive work. Writing, editing, and declaring an answer are.

Also call advisor:
- When you believe the task is complete. BEFORE this call, make your deliverable durable.
- When stuck — errors recurring, approach not converging, results that don't fit.
- When considering a change of approach.

On tasks longer than a few steps, call advisor at least once before committing to an approach and once before declaring done.
```

**How to treat the advice:**

```
Give the advice serious weight. If you follow a step and it fails empirically, or you have primary-source evidence that contradicts a specific claim, adapt. A passing self-test is not evidence the advice is wrong.

If you've already retrieved data pointing one way and the advisor points another: don't silently switch. Surface the conflict in one more advisor call.
```

### Trimming Advisor Output Length

To reduce advisor output cost, prepend this to the system prompt:

```
The advisor should respond in under 100 words and use enumerated steps, not explanations.
```

In internal testing, this line cut total advisor output tokens by roughly 35 to 45 percent without changing call
frequency.

### Pairing with Effort Settings

For coding tasks, pairing a Sonnet executor at
medium [effort](https://platform.claude.com/docs/en/build-with-claude/effort) with an Opus advisor achieves intelligence
comparable to Sonnet at default effort, at lower cost. For maximum intelligence, keep the executor at default effort.

### Cost Control

- For conversation-level budgets, count advisor calls client-side.
- Enable `caching` only for conversations where you expect three or more advisor calls.

---

## Limitations

- Advisor output does not stream. Expect a pause in the stream while the sub-inference runs.
- No built-in conversation-level cap on advisor calls. Track and cap them client-side.
- `max_tokens` applies to executor output only. It does not bound advisor tokens.
- Anthropic Priority Tier is honored per model. Priority Tier on the executor model does not extend to the advisor.

---

## Zero Data Retention

This feature is eligible
for [Zero Data Retention (ZDR)](https://platform.claude.com/docs/en/build-with-claude/api-and-data-retention). When your
organization has a ZDR arrangement, data sent through this feature is not stored after the API response is returned.
