# Supported Models

Models available through `pi model list`, organized by provider.
The latest 5 top generative/chat models per provider are listed below.

Last updated: 2026-09-05

## Anthropic

| # | Model Name | Description |
|---|------------|-------------|
| 1 | `claude-fable-5-1` | — |
| 2 | `claude-opus-5` | — |
| 3 | `claude-sonnet-5` | — |
| 4 | `claude-fable-5` | — |
| 5 | `claude-opus-4-8` | — |

> 11 models total. See `pi model list anthropic` for the full list.

## Gemini

| # | Model Name | Description |
|---|------------|-------------|
| 1 | `gemini-3.8-flash` | Gemini 3.8 Flash |
| 2 | `gemini-3.7-flash` | Gemini 3.7 Flash |
| 3 | `gemini-flash-latest` | Gemini Flash Latest |
| 4 | `gemini-3.5-flash-lite` | Gemini 3.5 Flash Lite |
| 5 | `gemini-3.6-flash` | Gemini 3.6 Flash |

> 43 models total. Excludes embedding, imagen, lyria, veo, robotics, tts, and audio models.
> See `pi model list gemini` for the full list.

## Ollama

| # | Model Name | Description |
|---|------------|-------------|
| 1 | `deepseek-v4-flash:cloud` | — |
| 2 | `gemma4:cloud` | — |
| 3 | `minimax-m3:cloud` | — |
| 4 | `kimi-k3:cloud` | — |
| 5 | `gemma4:12b-mlx` | — |

> 9 models total (chat). Excludes `embeddinggemma:latest` (not a chat model).
> See `pi model list ollama` for the full list.

## OpenAI

| # | Model Name | Description |
|---|------------|-------------|
| 1 | `gpt-5.6-luna` | — |
| 2 | `gpt-5.6-sol` | — |
| 3 | `gpt-5.6-terra` | — |
| 4 | `gpt-5.5` | — |
| 5 | `gpt-5.5-2026-04-23` | — |

> 13 models total. See `pi model list openai` for the full list.

## Updating This File

```bash
pi model list
```

Re-run and update the tables above. Sort by version recency, keeping the
latest 5 generative/chat models per provider.
