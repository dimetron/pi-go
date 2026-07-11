# Supported Models

Models available through `pi model list`, organized by provider.
The latest 5 top generative/chat models per provider are listed below.

Last updated: 2026-07-11

## Anthropic

| # | Model Name | Description |
|---|------------|-------------|
| 1 | `claude-sonnet-5` | — |
| 2 | `claude-opus-4-8` | — |
| 3 | `claude-opus-4-7` | — |
| 4 | `claude-opus-4-6` | — |
| 5 | `claude-opus-4-5-20251101` | — |

> 10 models total. See `pi model list anthropic` for the full list.

## Gemini

| # | Model Name | Description |
|---|------------|-------------|
| 1 | `gemini-3.5-flash` | Gemini 3.5 Flash |
| 2 | `gemini-3.1-pro-preview` | Gemini 3.1 Pro Preview |
| 3 | `gemini-3.1-pro-preview-customtools` | Gemini 3.1 Pro Preview Custom Tools |
| 4 | `gemini-3.1-flash-lite` | Gemini 3.1 Flash Lite |
| 5 | `gemini-3.1-flash-lite-preview` | Gemini 3.1 Flash Lite Preview |

> 50 models total. Excludes embedding, imagen, lyria, veo, robotics, tts, and audio models.
> See `pi model list gemini` for the full list.

## Ollama

| # | Model Name | Description |
|---|------------|-------------|
| 1 | `minimax-m3:cloud` | — |
| 2 | `kimi-k2.7-code:cloud` | — |
| 3 | `glm-5.2:cloud` | — |
| 4 | `gemma4:12b-mlx` | — |
| 5 | `gemma4:e4b-mlx` | — |

> 6 models total. Excludes `embeddinggemma:latest` (not a chat model).
> See `pi model list ollama` for the full list.

## OpenAI

| Status | Note |
|--------|------|
| Unavailable | 403: missing `api.model.read` scope |

> Requires correct organization role and project permissions.
> See `pi model list openai` for details.

## Updating This File

```bash
pi model list
```

Re-run and update the tables above. Sort by version recency, keeping the
latest 5 generative/chat models per provider.