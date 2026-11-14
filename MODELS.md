# Supported Models

Models available through `pi model list`, organized by provider. Every model
each provider currently serves is listed below. Prices are USD per 1M tokens
(input/output), from the embedded models.dev snapshot; a `—` means the
snapshot has no entry for that model.

Last updated: 2026-11-14

## Providers

| Provider | Models | Credentials |
|---|---|---|
| [Anthropic](#anthropic) | 15 models | `ANTHROPIC_API_KEY` |
| [OpenAI](#openai) | 89 models | `OPENAI_API_KEY` |
| [Google Gemini](#google-gemini) | 47 models | `GEMINI_API_KEY` |
| [Mistral](#mistral) | 38 models | `MISTRAL_API_KEY` |
| [xAI](#xai) | 8 models | `XAI_API_KEY` |
| [Ollama](#ollama) | 18 models | local daemon; no key needed |
| [OpenRouter](#openrouter) | 240 models | `OPENROUTER_API_KEY` |
| [Agentgateway](#agentgateway) | 25 routes | local gateway |
| [Azure](#azure) | 31 deployments | `AZUREOPENAI_API_KEY`; names are per-subscription |

Azure has no live model-listing API — enumerating deployments needs ARM
credentials, so `pi model list azure` prints the embedded catalog shown below.

The CLI also reports an owner per model. It is shown only for Gemini, where it
is the human-readable display name; Anthropic and xAI report nothing useful
(the provider name itself), and OpenAI reports `openai-internal` for just
`text-embedding-ada-002`, `tts-1` and `whisper-1`.

## Anthropic — 15 models

API key: `ANTHROPIC_API_KEY`.

| Model | Release | Price (in/out per 1M) |
|---|---|---|
| claude-fable-5-2 | 2026-11-09 | $8.00/$40.00 |
| claude-haiku-5-5 | 2026-10-07 | $0.10/$0.50 |
| claude-sonnet-5-5 | 2026-09-28 | $2.00/$10.00 |
| claude-opus-5-5 | 2026-09-22 | $4.00/$20.00 |
| claude-fable-5-1 | 2026-09-01 | $10.00/$50.00 |
| claude-opus-5 | 2026-07-24 | $5.00/$25.00 |
| claude-sonnet-5 | 2026-06-29 | $2.00/$10.00 |
| claude-fable-5 | 2026-06-07 | $10.00/$50.00 |
| claude-opus-4-8 | 2026-05-28 | $5.00/$25.00 |
| claude-opus-4-7 | 2026-04-14 | $5.00/$25.00 |
| claude-sonnet-4-6 | 2026-02-17 | $3.00/$15.00 |
| claude-opus-4-6 | 2026-02-04 | $5.00/$25.00 |
| claude-opus-4-5-20251101 | 2025-11-24 | $5.00/$25.00 |
| claude-haiku-4-5-20251001 | 2025-10-15 | $1.00/$5.00 |
| claude-sonnet-4-5-20250929 | 2025-09-29 | $3.00/$15.00 |

## OpenAI — 89 models

API key: `OPENAI_API_KEY`.

| Model | Release | Price (in/out per 1M) |
|---|---|---|
| gpt-6.1-terra | 2026-10-26 | $1.00/$5.00 |
| gpt-6.1-sol | 2026-09-29 | $2.00/$10.00 |
| gpt-6-luna | 2026-09-22 | $0.10/$0.50 |
| gpt-6-sol | 2026-09-22 | $2.00/$10.00 |
| gpt-6-astra | 2026-09-04 | $10.00/$50.00 |
| gpt-5.6-luna | 2026-07-09 | $0.20/$1.20 |
| gpt-5.6-sol | 2026-07-09 | $4.00/$20.00 |
| gpt-5.6-terra | 2026-07-09 | $2.00/$12.00 |
| gpt-realtime-2.1 | 2026-07-06 | $4.00/$24.00 |
| gpt-realtime-2.1-mini | 2026-07-06 | $4.00/$24.00 |
| gpt-5.5 | 2026-04-23 | $5.00/$30.00 |
| gpt-5.5-2026-04-23 | 2026-04-23 | $5.00/$30.00 |
| gpt-5.5-pro | 2026-04-23 | $30.00/$180.00 |
| gpt-5.5-pro-2026-04-23 | 2026-04-23 | $30.00/$180.00 |
| gpt-5.4-mini | 2026-03-17 | $0.75/$4.50 |
| gpt-5.4-mini-2026-03-17 | 2026-03-17 | $0.75/$4.50 |
| gpt-5.4-nano | 2026-03-17 | $0.20/$1.25 |
| gpt-5.4-nano-2026-03-17 | 2026-03-17 | $0.20/$1.25 |
| gpt-5.4 | 2026-03-05 | $2.50/$15.00 |
| gpt-5.4-2026-03-05 | 2026-03-05 | $2.50/$15.00 |
| gpt-5.4-pro | 2026-03-05 | $30.00/$180.00 |
| gpt-5.4-pro-2026-03-05 | 2026-03-05 | $30.00/$180.00 |
| gpt-5.3-codex | 2026-02-05 | $1.75/$14.00 |
| chatgpt-image-latest | 2025-12-16 | $5.00/$10.00 |
| gpt-5.2 | 2025-12-11 | $1.75/$14.00 |
| gpt-5.2-2025-12-11 | 2025-12-11 | $1.75/$14.00 |
| gpt-5.2-pro | 2025-12-11 | $21.00/$168.00 |
| gpt-5.2-pro-2025-12-11 | 2025-12-11 | $21.00/$168.00 |
| gpt-image-1.5 | 2025-11-25 | $5.00/$10.00 |
| gpt-5.1 | 2025-11-13 | $1.25/$10.00 |
| gpt-5.1-2025-11-13 | 2025-11-13 | $1.25/$10.00 |
| gpt-5-pro | 2025-10-06 | $15.00/$120.00 |
| gpt-5-pro-2025-10-06 | 2025-10-06 | $15.00/$120.00 |
| gpt-5 | 2025-08-07 | $1.25/$10.00 |
| gpt-5-2025-08-07 | 2025-08-07 | $1.25/$10.00 |
| gpt-5-mini | 2025-08-07 | $0.25/$2.00 |
| gpt-5-mini-2025-08-07 | 2025-08-07 | $0.25/$2.00 |
| gpt-5-nano | 2025-08-07 | $0.05/$0.40 |
| gpt-5-nano-2025-08-07 | 2025-08-07 | $0.05/$0.40 |
| gpt-5-search-api | 2025-08-07 | $1.25/$10.00 |
| gpt-5-search-api-2025-10-14 | 2025-08-07 | $1.25/$10.00 |
| o3-pro | 2025-06-10 | $20.00/$80.00 |
| o3-pro-2025-06-10 | 2025-06-10 | $20.00/$80.00 |
| o3 | 2025-04-16 | $2.00/$8.00 |
| o3-2025-04-16 | 2025-04-16 | $2.00/$8.00 |
| gpt-4.1 | 2025-04-14 | $2.00/$8.00 |
| gpt-4.1-2025-04-14 | 2025-04-14 | $2.00/$8.00 |
| gpt-4.1-mini | 2025-04-14 | $0.40/$1.60 |
| gpt-4.1-mini-2025-04-14 | 2025-04-14 | $0.40/$1.60 |
| gpt-4o-2024-11-20 | 2024-11-20 | $2.50/$10.00 |
| gpt-4o-2024-08-06 | 2024-08-06 | $2.50/$10.00 |
| gpt-4o-mini | 2024-07-18 | $0.15/$0.60 |
| gpt-4o-mini-2024-07-18 | 2024-07-18 | $0.15/$0.60 |
| gpt-4o-mini-transcribe | 2024-07-18 | $0.15/$0.60 |
| gpt-4o-mini-transcribe-2025-03-20 | 2024-07-18 | $0.15/$0.60 |
| gpt-4o-mini-transcribe-2025-12-15 | 2024-07-18 | $0.15/$0.60 |
| gpt-4o-mini-tts | 2024-07-18 | $0.15/$0.60 |
| gpt-4o-mini-tts-2025-03-20 | 2024-07-18 | $0.15/$0.60 |
| gpt-4o-mini-tts-2025-12-15 | 2024-07-18 | $0.15/$0.60 |
| gpt-4o | 2024-05-13 | $2.50/$10.00 |
| gpt-4o-transcribe | 2024-05-13 | $2.50/$10.00 |
| gpt-4o-transcribe-diarize | 2024-05-13 | $2.50/$10.00 |
| text-embedding-3-large | 2024-01-25 | $0.13/$— |
| text-embedding-3-small | 2024-01-25 | $0.02/$— |
| text-embedding-ada-002 | 2022-12-15 | $0.10/$— |
| chat-latest | — | — |
| gpt-audio | — | — |
| gpt-audio-1.5 | — | — |
| gpt-audio-2025-08-28 | — | — |
| gpt-audio-mini | — | — |
| gpt-audio-mini-2025-12-15 | — | — |
| gpt-live-1 | — | — |
| gpt-live-transcribe | — | — |
| gpt-realtime | — | — |
| gpt-realtime-1.5 | — | — |
| gpt-realtime-2 | — | — |
| gpt-realtime-2025-08-28 | — | — |
| gpt-realtime-mini | — | — |
| gpt-realtime-mini-2025-12-15 | — | — |
| gpt-realtime-translate | — | — |
| gpt-realtime-whisper | — | — |
| gpt-transcribe | — | — |
| omni-moderation-2024-09-26 | — | — |
| omni-moderation-latest | — | — |
| tts-1 | — | — |
| tts-1-1106 | — | — |
| tts-1-hd | — | — |
| tts-1-hd-1106 | — | — |
| whisper-1 | — | — |

## Google Gemini — 47 models

API key: `GEMINI_API_KEY`.

| Model | Release | Price (in/out per 1M) |
|---|---|---|
| gemini-4-hydrogen | 2026-11-12 | $1.50/$7.50 |
| gemini-4-carbon | 2026-11-10 | $2.00/$10.00 |
| gemini-4-argon | 2026-10-13 | $2.00/$10.00 |
| gemini-3.8-flash | 2026-09-02 | $0.75/$3.75 |
| gemini-3.8-flash-lite-tts | 2026-09-02 | $0.75/$3.75 |
| gemini-3.8-flash-tts | 2026-09-02 | $0.75/$3.75 |
| gemini-3.7-flash | 2026-08-13 | $0.75/$3.75 |
| gemini-flash-latest | 2026-08-13 | $0.75/$3.75 |
| gemini-3.5-flash-lite | 2026-07-21 | $0.30/$2.50 |
| gemini-3.6-flash | 2026-07-21 | $0.75/$3.75 |
| gemini-flash-lite-latest | 2026-07-21 | $0.30/$2.50 |
| gemini-3.1-flash-lite-image | 2026-06-30 | $0.25/$30.00 |
| gemini-3-pro-image | 2026-05-28 | $2.00/$120.00 |
| gemini-3-pro-image-preview | 2026-05-28 | $2.00/$120.00 |
| gemini-3.1-flash-image | 2026-05-28 | $0.50/$60.00 |
| gemini-3.1-flash-image-preview | 2026-05-28 | $0.50/$60.00 |
| gemini-3.5-flash | 2026-05-19 | $1.50/$9.00 |
| gemini-3.1-flash-lite | 2026-05-07 | $0.25/$1.50 |
| deep-research-max-preview-04-2026 | 2026-04-21 | $2.00/$12.00 |
| deep-research-preview-04-2026 | 2026-04-21 | $2.00/$12.00 |
| gemma-4-26b-a4b-it | 2026-04-02 | $—/$— |
| gemma-4-31b-it | 2026-04-02 | $—/$— |
| lyria-3-clip-preview | 2026-03-25 | $—/$— |
| lyria-3-pro-preview | 2026-03-25 | $—/$— |
| gemini-3.1-flash-lite-preview | 2026-03-03 | $0.25/$1.50 |
| gemini-3.1-pro-preview | 2026-02-19 | $2.00/$12.00 |
| gemini-3.1-pro-preview-customtools | 2026-02-19 | $2.00/$12.00 |
| gemini-pro-latest | 2026-02-19 | $2.00/$12.00 |
| gemini-3-flash-preview | 2025-12-17 | $0.50/$3.00 |
| gemini-2.5-computer-use-preview-10-2025 | 2025-10-07 | $1.25/$10.00 |
| gemini-2.5-flash-image | 2025-08-26 | $0.30/$30.00 |
| gemini-2.5-flash | 2025-06-17 | $0.30/$2.50 |
| gemini-2.5-flash-lite | 2025-06-17 | $0.10/$0.40 |
| gemini-2.5-pro | 2025-06-17 | $1.25/$10.00 |
| gemini-embedding-001 | 2025-05-20 | $0.15/$— |
| antigravity-preview-05-2026 | — | — |
| antigravity-preview-09-2026 | — | — |
| antigravity-preview-latest | — | — |
| deep-research-pro-preview-12-2025 | — | — |
| gemini-3.5-transcribe | — | — |
| gemini-nano-banana-2.1 | — | — |
| gemini-omni-1.1-flash | — | — |
| gemini-omni-1.2-flash | — | — |
| gemini-omni-flash-preview | — | — |
| gemini-robotics-er-2-preview | — | — |
| lyria-3.5 | — | — |
| nano-banana-pro-preview | — | — |

## Mistral — 38 models

API key: `MISTRAL_API_KEY`.

| Model | Release | Price (in/out per 1M) | Context | Capabilities |
|---|---|---|---|---|
| deepseek-v4-1-flash-1017 | 2026-10-17 | $0.30/$1.20 | 1.05M | completion_chat, function_calling, vision |
| mistral-large-4 | 2026-10-06 | $0.70/$2.10 | 1.05M | completion_chat, function_calling, vision |
| mistral-large-4-0 | 2026-10-06 | $0.70/$2.10 | 1.05M | completion_chat, function_calling, vision |
| mistral-large-latest | 2026-10-06 | $0.70/$2.10 | 1.05M | completion_chat, function_calling, vision |
| zai-glm-5-3-flash | 2026-08-26 | $0.15/$0.50 | 1.05M | completion_chat, function_calling, vision |
| zai-glm-5-3 | 2026-08-14 | $1.40/$4.40 | 1.05M | completion_chat, function_calling |
| labs-leanstral-1-5-1 | 2026-06-30 | $—/$— | 262K | completion_chat, function_calling, vision |
| glm-5-2 | 2026-06-13 | $1.40/$4.40 | 1.05M | completion_chat, function_calling |
| zai-glm-5-2 | 2026-06-13 | $1.40/$4.40 | 1.05M | completion_chat, function_calling |
| mistral-medium-2604 | 2026-04-29 | $1.50/$7.50 | 262K | completion_chat, function_calling, vision |
| mistral-medium-latest | 2026-04-29 | $1.50/$7.50 | 262K | completion_chat, function_calling, vision |
| mistral-small-2603 | 2026-03-16 | $0.15/$0.60 | 262K | completion_chat, function_calling, vision |
| mistral-small-latest | 2026-03-16 | $0.15/$0.60 | 262K | completion_chat, function_calling, vision |
| ministral-14b-2512 | 2025-12-02 | $0.20/$0.20 | 262K | completion_chat, function_calling, fine_tuning, vision |
| ministral-3b-2512 | 2025-12-02 | $0.10/$0.10 | 131K | completion_chat, function_calling, fine_tuning, vision |
| ministral-8b-2512 | 2025-12-02 | $0.15/$0.15 | 262K | completion_chat, function_calling, fine_tuning, vision |
| codestral-2508 | 2025-07-30 | $0.30/$0.90 | 256K | completion_chat, completion_fim, function_calling |
| voxtral-small-2507 | 2025-07-15 | $0.10/$0.40 | 32K | completion_chat, function_calling |
| voxtral-small-latest | 2025-07-15 | $0.10/$0.40 | 32K | completion_chat, function_calling |
| magistral-medium-latest | 2025-03-17 | $2.00/$5.00 | 262K | completion_chat, function_calling, vision |
| mistral-large-2512 | 2024-11-01 | $0.50/$1.50 | 262K | completion_chat, function_calling, fine_tuning, vision |
| ministral-3b-latest | 2024-10-01 | $0.10/$0.10 | 131K | completion_chat, function_calling, fine_tuning, vision |
| ministral-8b-latest | 2024-10-01 | $0.15/$0.15 | 262K | completion_chat, function_calling, fine_tuning, vision |
| codestral-latest | 2024-05-29 | $0.30/$0.90 | 256K | completion_chat, completion_fim, function_calling |
| labs-leanstral-1-5 | — | — | 262K | completion_chat, function_calling, vision |
| magistral-small-latest | — | — | 262K | completion_chat, function_calling, vision |
| ministral-14b-latest | — | — | 262K | completion_chat, function_calling, fine_tuning, vision |
| mistral-code-fim-latest | — | — | 256K | completion_chat, completion_fim, function_calling |
| mistral-code-latest | — | — | 256K | completion_chat, completion_fim, function_calling |
| mistral-medium | — | — | 262K | completion_chat, function_calling, vision |
| mistral-medium-3 | — | — | 262K | completion_chat, function_calling, vision |
| mistral-medium-3-5 | — | — | 262K | completion_chat, function_calling, vision |
| mistral-medium-3.5 | — | — | 262K | completion_chat, function_calling, vision |
| mistral-vibe-cli-fast | — | — | 262K | completion_chat, function_calling, vision |
| mistral-vibe-cli-latest | — | — | 262K | completion_chat, function_calling, vision |
| mistral-vibe-cli-with-tools | — | — | 262K | completion_chat, function_calling, vision |
| zai-glm-5 | — | — | 1.05M | completion_chat, function_calling |
| zai-glm-latest | — | — | 1.05M | completion_chat, function_calling |

## xAI — 8 models

API key: `XAI_API_KEY`.

| Model | Release | Price (in/out per 1M) |
|---|---|---|
| grok-4.7 | 2026-09-21 | $2.00/$6.00 |
| grok-4.6 | 2026-08-12 | $2.00/$6.00 |
| grok-4.5 | 2026-07-08 | $2.00/$6.00 |
| grok-4.3 | 2026-04-17 | $1.25/$2.50 |
| grok-build-0.1 | 2026-04-16 | $1.00/$2.00 |
| grok-4.20-0309-non-reasoning | 2026-03-09 | $1.25/$2.50 |
| grok-4.20-0309-reasoning | 2026-03-09 | $1.25/$2.50 |
| grok-4.20-multi-agent-0309 | 2026-03-09 | $1.25/$2.50 |

## Ollama — 18 models

Local daemon, plus Ollama Cloud (`*:cloud` tags). No API key for local models.

| Model | Price (in/out per 1M) | Context | Capabilities |
|---|---|---|---|
| all-minilm:latest | — | 512 | embedding |
| beam:cloud | $0.12/$0.40 | 1.05M | completion, thinking, tools |
| deepseek-v4-flash:0731-cloud | — | 1.05M | completion, thinking, tools |
| deepseek-v4-flash:cloud | — | 1.05M | completion, thinking, tools |
| deepseek-v4.1-flash:1017-cloud | $0.15/$0.60 | 1.05M | completion, thinking, tools, vision |
| deepseek-v4.1-flash:cloud | $0.15/$0.60 | 1.05M | completion, thinking, tools, vision |
| embeddinggemma:latest | — | 2K | embedding |
| gemma4:31b-cloud | $0.14/$0.40 | 262K | completion, thinking, tools, vision |
| gemma4:cloud | $0.14/$0.40 | 262K | completion, thinking, tools, vision |
| glm-5.3-flash:cloud | $0.15/$0.50 | 1.05M | completion, thinking, tools, vision |
| glm-5.3:cloud | $1.40/$4.40 | 1.05M | completion, thinking, tools |
| kimi-k3:cloud | $3.00/$15.00 | 1.05M | completion, thinking, tools, vision |
| minimax-m3.1-flash:cloud | $0.15/$0.60 | 1.00M | completion, thinking, tools, vision |
| minimax-m3:cloud | $0.60/$2.40 | 524K | completion, thinking, tools, vision |
| qwen3.5:0.8b | — | 262K | completion, thinking, tools, vision |
| tev1:4b | — | 2K | decision |
| tev1:4b-mxfp8 | — | 2K | decision |
| tev1:latest | — | 2K | decision |

## OpenRouter — 240 models

API key: `OPENROUTER_API_KEY`.

| Model | Release | Price (in/out per 1M) |
|---|---|---|
| google/gemini-4-hydrogen | 2026-11-12 | $1.50/$7.50 |
| google/gemini-4-hydrogen:batch | 2026-11-12 | $1.50/$7.50 |
| google/gemini-4-carbon | 2026-11-10 | $2.00/$10.00 |
| google/gemini-4-carbon:batch | 2026-11-10 | $2.00/$10.00 |
| anthropic/claude-fable-5.2 | 2026-11-09 | $8.00/$40.00 |
| anthropic/claude-fable-5.2:batch | 2026-11-09 | $8.00/$40.00 |
| meta/muse-ember | 2026-11-09 | $0.10/$0.50 |
| google/gemini-omni-1.2-flash | 2026-11-05 | $1.50/$9.00 |
| z-ai/glm-5.3-flashx-legal | 2026-11-02 | $0.37/$1.25 |
| openai/gpt-6.1-terra | 2026-10-26 | $1.00/$5.00 |
| openai/gpt-6.1-terra-pro | 2026-10-26 | $1.00/$5.00 |
| openai/gpt-6.1-terra-pro:batch | 2026-10-26 | $1.00/$5.00 |
| openai/gpt-6.1-terra:batch | 2026-10-26 | $1.00/$5.00 |
| moonshotai/kimi-k3.1-pro | 2026-10-23 | $2.00/$10.00 |
| minimax/minimax-m3.1-flash | 2026-10-19 | $0.07/$0.30 |
| deepseek/deepseek-v4.1-flash-1017 | 2026-10-17 | $0.04/$0.50 |
| meta/muse-spark-open | 2026-10-15 | $1.25/$4.25 |
| google/gemini-4-argon | 2026-10-13 | $2.00/$10.00 |
| google/gemini-4-argon:batch | 2026-10-13 | $2.00/$10.00 |
| meta/muse-wonder | 2026-10-12 | $4.00/$20.00 |
| anthropic/claude-haiku-5.5 | 2026-10-07 | $0.10/$0.50 |
| anthropic/claude-haiku-5.5:batch | 2026-10-07 | $0.10/$0.50 |
| google/gemini-nano-banana-2.1 | 2026-10-06 | $1.50/$7.50 |
| mistralai/mistral-large-4-0 | 2026-10-06 | $0.70/$2.10 |
| openai/gpt-6.1-sol | 2026-09-29 | $2.00/$10.00 |
| openai/gpt-6.1-sol-pro | 2026-09-29 | $2.00/$10.00 |
| openai/gpt-6.1-sol-pro:batch | 2026-09-29 | $2.00/$10.00 |
| openai/gpt-6.1-sol:batch | 2026-09-29 | $2.00/$10.00 |
| anthropic/claude-sonnet-5.5 | 2026-09-28 | $2.00/$10.00 |
| anthropic/claude-sonnet-5.5:batch | 2026-09-28 | $2.00/$10.00 |
| anthropic/claude-opus-5.5 | 2026-09-22 | $4.00/$20.00 |
| anthropic/claude-opus-5.5:batch | 2026-09-22 | $4.00/$20.00 |
| fireworks/ember-1 | 2026-09-22 | $3.00/$15.00 |
| openai/gpt-6-luna | 2026-09-22 | $0.10/$0.50 |
| openai/gpt-6-luna-pro | 2026-09-22 | $0.10/$0.50 |
| openai/gpt-6-luna-pro:batch | 2026-09-22 | $0.10/$0.50 |
| openai/gpt-6-luna:batch | 2026-09-22 | $0.10/$0.50 |
| openai/gpt-6-sol | 2026-09-22 | $2.00/$10.00 |
| openai/gpt-6-sol-pro | 2026-09-22 | $2.00/$10.00 |
| openai/gpt-6-sol-pro:batch | 2026-09-22 | $2.00/$10.00 |
| openai/gpt-6-sol:batch | 2026-09-22 | $2.00/$10.00 |
| x-ai/grok-4.7 | 2026-09-21 | $2.00/$6.00 |
| x-ai/grok-4.7:batch | 2026-09-21 | $2.00/$6.00 |
| z-ai/glm-5.3-flashx | 2026-09-18 | $0.37/$1.25 |
| ~deepseek/deepseek-flash-latest | 2026-09-14 | $0.04/$0.50 |
| ~openai/gpt-astra-latest | 2026-09-11 | $10.00/$50.00 |
| ~openai/gpt-luna-latest | 2026-09-11 | $0.10/$0.50 |
| ~openai/gpt-sol-latest | 2026-09-11 | $2.00/$10.00 |
| ~openai/gpt-terra-latest | 2026-09-11 | $1.00/$5.00 |
| deepseek/deepseek-v4.1-flash | 2026-09-10 | $0.30/$1.20 |
| openai/gpt-6-astra | 2026-09-04 | $10.00/$50.00 |
| openai/gpt-6-astra-pro | 2026-09-04 | $10.00/$50.00 |
| openai/gpt-6-astra-pro:batch | 2026-09-04 | $10.00/$50.00 |
| openai/gpt-6-astra:batch | 2026-09-04 | $10.00/$50.00 |
| google/gemini-3.8-flash | 2026-09-02 | $0.75/$3.75 |
| google/gemini-3.8-flash:batch | 2026-09-02 | $0.75/$3.75 |
| meta/muse-spark-1.3 | 2026-09-02 | $1.25/$4.25 |
| anthropic/claude-fable-5.1 | 2026-09-01 | $10.00/$50.00 |
| anthropic/claude-fable-5.1:batch | 2026-09-01 | $10.00/$50.00 |
| google/gemini-omni-1.1-flash | 2026-08-27 | $1.50/$9.00 |
| ~z-ai/glm-flash-latest | 2026-08-27 | $0.04/$0.08 |
| z-ai/glm-5.3-flash | 2026-08-26 | $0.04/$0.08 |
| ~z-ai/glm-latest | 2026-08-19 | $0.07/$1.40 |
| qwen/qwen3.8-27b | 2026-08-14 | $0.15/$1.88 |
| z-ai/glm-5.3 | 2026-08-14 | $0.08/$3.60 |
| google/gemini-3.7-flash | 2026-08-13 | $0.75/$3.75 |
| google/gemini-3.7-flash:batch | 2026-08-13 | $0.75/$3.75 |
| x-ai/grok-4.6 | 2026-08-12 | $2.00/$6.00 |
| x-ai/grok-4.6:batch | 2026-08-12 | $2.00/$6.00 |
| nvidia/nemotron-3.5-lightning | 2026-08-11 | $0.07/$0.20 |
| ~deepseek/deepseek-v4-flash-latest | 2026-08-01 | $0.04/$0.30 |
| anthropic/claude-opus-5 | 2026-07-24 | $5.00/$25.00 |
| anthropic/claude-opus-5:batch | 2026-07-24 | $5.00/$25.00 |
| google/gemini-3.5-flash-lite | 2026-07-21 | $0.30/$2.50 |
| google/gemini-3.5-flash-lite:batch | 2026-07-21 | $0.30/$2.50 |
| google/gemini-3.6-flash | 2026-07-21 | $0.75/$3.75 |
| google/gemini-3.6-flash:batch | 2026-07-21 | $0.75/$3.75 |
| moonshotai/kimi-k3 | 2026-07-16 | $0.19/$16.00 |
| openai/gpt-5.6-luna | 2026-07-09 | $0.20/$1.20 |
| openai/gpt-5.6-luna-pro | 2026-07-09 | $0.20/$1.20 |
| openai/gpt-5.6-luna-pro:batch | 2026-07-09 | $0.20/$1.20 |
| openai/gpt-5.6-luna:batch | 2026-07-09 | $0.20/$1.20 |
| openai/gpt-5.6-sol | 2026-07-09 | $4.00/$20.00 |
| openai/gpt-5.6-sol-pro | 2026-07-09 | $4.00/$20.00 |
| openai/gpt-5.6-sol-pro:batch | 2026-07-09 | $4.00/$20.00 |
| openai/gpt-5.6-sol:batch | 2026-07-09 | $4.00/$20.00 |
| openai/gpt-5.6-terra | 2026-07-09 | $2.00/$12.00 |
| openai/gpt-5.6-terra-pro | 2026-07-09 | $2.00/$12.00 |
| openai/gpt-5.6-terra-pro:batch | 2026-07-09 | $2.00/$12.00 |
| openai/gpt-5.6-terra:batch | 2026-07-09 | $2.00/$12.00 |
| x-ai/grok-4.5 | 2026-07-08 | $2.00/$6.00 |
| ~x-ai/grok-latest | 2026-07-08 | $2.00/$6.00 |
| anthropic/claude-sonnet-5 | 2026-06-30 | $2.00/$10.00 |
| google/gemini-3.1-flash-lite-image | 2026-06-30 | $0.25/$1.50 |
| z-ai/glm-5.2 | 2026-06-13 | $0.007/$6.80 |
| anthropic/claude-fable-5 | 2026-06-09 | $10.00/$50.00 |
| ~anthropic/claude-fable-latest | 2026-06-09 | $8.00/$40.00 |
| minimax/minimax-m3 | 2026-06-01 | $0.30/$1.20 |
| anthropic/claude-opus-4.8 | 2026-05-28 | $5.00/$25.00 |
| google/gemini-3-pro-image | 2026-05-28 | $2.00/$12.00 |
| google/gemini-3.1-flash-image | 2026-05-28 | $0.50/$3.00 |
| google/gemini-3.5-flash | 2026-05-19 | $1.50/$9.00 |
| google/gemini-3.5-flash:batch | 2026-05-19 | $1.50/$9.00 |
| google/gemini-3.1-flash-lite | 2026-05-07 | $0.25/$1.50 |
| google/gemini-3.1-flash-lite:batch | 2026-05-07 | $0.25/$1.50 |
| openai/gpt-chat-latest | 2026-05-05 | $5.00/$30.00 |
| mistralai/mistral-medium-3-5 | 2026-04-30 | $1.50/$7.50 |
| mistralai/mistral-medium-3-5:batch | 2026-04-30 | $1.50/$7.50 |
| ~anthropic/claude-haiku-latest | 2026-04-27 | $0.10/$0.50 |
| ~anthropic/claude-sonnet-latest | 2026-04-27 | $2.00/$10.00 |
| ~google/gemini-flash-latest | 2026-04-27 | $1.50/$7.50 |
| ~google/gemini-pro-latest | 2026-04-27 | $2.00/$10.00 |
| ~moonshotai/kimi-latest | 2026-04-27 | $0.18/$11.01 |
| ~openai/gpt-mini-latest | 2026-04-27 | $0.75/$4.50 |
| deepseek/deepseek-v4-flash | 2026-04-24 | $0.09/$0.18 |
| deepseek/deepseek-v4-pro | 2026-04-24 | $0.90/$1.50 |
| openai/gpt-5.5 | 2026-04-23 | $5.00/$30.00 |
| openai/gpt-5.5-pro | 2026-04-23 | $30.00/$180.00 |
| openai/gpt-5.4-image-2 | 2026-04-21 | $8.00/$15.00 |
| ~anthropic/claude-opus-latest | 2026-04-21 | $4.00/$20.00 |
| x-ai/grok-4.3 | 2026-04-17 | $1.25/$2.50 |
| x-ai/grok-4.3:batch | 2026-04-17 | $1.25/$2.50 |
| anthropic/claude-opus-4.7 | 2026-04-16 | $5.00/$25.00 |
| x-ai/grok-build-0.1 | 2026-04-16 | $1.00/$2.00 |
| z-ai/glm-5.1 | 2026-04-07 | $1.40/$4.40 |
| google/gemma-4-26b-a4b-it | 2026-04-02 | $0.09/$0.30 |
| google/gemma-4-26b-a4b-it:free | 2026-04-02 | $—/$— |
| google/gemma-4-31b-it:free | 2026-04-02 | $—/$— |
| z-ai/glm-5v-turbo | 2026-04-01 | $1.20/$4.00 |
| x-ai/grok-4.20 | 2026-03-31 | $1.25/$2.50 |
| x-ai/grok-4.20-multi-agent | 2026-03-31 | $1.25/$2.50 |
| google/lyria-3-clip-preview | 2026-03-25 | $—/$— |
| google/lyria-3-pro-preview | 2026-03-25 | $—/$— |
| minimax/minimax-m2.7 | 2026-03-18 | $0.21/$0.84 |
| openai/gpt-5.4-mini | 2026-03-17 | $0.75/$4.50 |
| openai/gpt-5.4-nano | 2026-03-17 | $0.20/$1.25 |
| mistralai/mistral-small-2603 | 2026-03-16 | $0.15/$0.60 |
| z-ai/glm-5-turbo | 2026-03-16 | $1.20/$4.00 |
| openai/gpt-5.4 | 2026-03-05 | $2.50/$15.00 |
| openai/gpt-5.4-pro | 2026-03-05 | $30.00/$180.00 |
| google/gemini-3.1-flash-lite-preview | 2026-03-03 | $0.25/$1.50 |
| google/gemini-3.1-flash-image-preview | 2026-02-26 | $0.50/$3.00 |
| google/gemini-3.1-pro-preview | 2026-02-19 | $2.00/$12.00 |
| google/gemini-3.1-pro-preview-customtools | 2026-02-19 | $2.00/$12.00 |
| google/gemini-3.1-pro-preview:batch | 2026-02-19 | $2.00/$12.00 |
| anthropic/claude-sonnet-4.6 | 2026-02-17 | $3.00/$15.00 |
| z-ai/glm-5 | 2026-02-12 | $1.00/$3.20 |
| anthropic/claude-opus-4.6 | 2026-02-05 | $5.00/$25.00 |
| openai/gpt-5.3-codex | 2026-02-05 | $1.75/$14.00 |
| openrouter/free | 2026-02-01 | $—/$— |
| moonshotai/kimi-k2.5 | 2026-01-27 | $0.50/$2.50 |
| z-ai/glm-4.7 | 2025-12-22 | $0.60/$2.20 |
| google/gemini-3-flash-preview | 2025-12-17 | $0.50/$3.00 |
| google/gemini-3-flash-preview:batch | 2025-12-17 | $0.50/$3.00 |
| openai/gpt-5.2 | 2025-12-11 | $1.75/$14.00 |
| openai/gpt-5.2-codex | 2025-12-11 | $1.75/$14.00 |
| openai/gpt-5.2-pro | 2025-12-11 | $21.00/$168.00 |
| mistralai/devstral-2512 | 2025-12-09 | $0.40/$2.00 |
| z-ai/glm-4.6v | 2025-12-08 | $0.30/$0.90 |
| amazon/nova-2-lite-v1 | 2025-12-02 | $0.30/$2.50 |
| mistralai/ministral-14b-2512 | 2025-12-02 | $0.20/$0.20 |
| mistralai/ministral-3b-2512 | 2025-12-02 | $0.10/$0.10 |
| mistralai/ministral-8b-2512 | 2025-12-02 | $0.15/$0.15 |
| mistralai/mistral-large-2512 | 2025-12-02 | $0.50/$1.50 |
| anthropic/claude-opus-4.5 | 2025-11-24 | $5.00/$25.00 |
| google/gemini-3-pro-image-preview | 2025-11-20 | $2.00/$12.00 |
| openai/gpt-5.1 | 2025-11-13 | $1.25/$10.00 |
| openai/gpt-5.1-codex | 2025-11-13 | $1.25/$10.00 |
| openai/gpt-5.1-codex-max | 2025-11-13 | $1.25/$10.00 |
| openai/gpt-5.1-codex-mini | 2025-11-13 | $0.25/$2.00 |
| perplexity/sonar-pro-search | 2025-10-30 | $3.00/$15.00 |
| openai/gpt-oss-safeguard-20b | 2025-10-29 | $0.07/$0.30 |
| openai/gpt-5-image-mini | 2025-10-16 | $2.50/$2.00 |
| anthropic/claude-haiku-4.5 | 2025-10-15 | $1.00/$5.00 |
| openai/gpt-5-image | 2025-10-14 | $10.00/$10.00 |
| openai/gpt-5-pro | 2025-10-06 | $15.00/$120.00 |
| openai/gpt-audio-mini | 2025-10-06 | $0.60/$2.40 |
| z-ai/glm-4.6 | 2025-09-30 | $0.50/$2.00 |
| anthropic/claude-sonnet-4.5 | 2025-09-29 | $3.00/$15.00 |
| openai/gpt-audio | 2025-08-28 | $2.50/$10.00 |
| google/gemini-2.5-flash-image | 2025-08-26 | $0.30/$2.50 |
| mistralai/mistral-medium-3.1 | 2025-08-12 | $0.40/$2.00 |
| z-ai/glm-4.5v | 2025-08-11 | $0.60/$1.80 |
| openai/gpt-5 | 2025-08-07 | $1.25/$10.00 |
| openai/gpt-5-mini | 2025-08-07 | $0.25/$2.00 |
| openai/gpt-5-nano | 2025-08-07 | $0.05/$0.40 |
| openai/gpt-5-nano:batch | 2025-08-07 | $0.05/$0.40 |
| anthropic/claude-opus-4.1 | 2025-08-05 | $15.00/$75.00 |
| openai/gpt-oss-120b | 2025-08-05 | $0.15/$0.60 |
| openai/gpt-oss-20b | 2025-08-05 | $0.03/$0.13 |
| qwen/qwen3-coder-30b-a3b-instruct | 2025-07-31 | $0.15/$0.60 |
| mistralai/codestral-2508 | 2025-07-30 | $0.30/$0.90 |
| z-ai/glm-4.5 | 2025-07-28 | $0.60/$2.20 |
| z-ai/glm-4.5-air | 2025-07-28 | $0.20/$1.10 |
| mistralai/voxtral-small-24b-2507 | 2025-07-15 | $0.10/$0.30 |
| mistralai/mistral-small-3.2-24b-instruct | 2025-06-20 | $0.10/$0.30 |
| google/gemini-2.5-flash | 2025-06-17 | $0.30/$2.50 |
| google/gemini-2.5-flash-lite | 2025-06-17 | $0.10/$0.40 |
| google/gemini-2.5-pro | 2025-06-17 | $1.25/$10.00 |
| openai/o3-pro | 2025-06-10 | $20.00/$80.00 |
| google/gemini-2.5-pro-preview | 2025-06-05 | $1.25/$10.00 |
| mistralai/mistral-medium-3 | 2025-05-07 | $0.40/$2.00 |
| amazon/nova-premier-v1 | 2025-04-30 | $2.50/$12.50 |
| writer/palmyra-x5 | 2025-04-28 | $0.60/$6.00 |
| openai/o3 | 2025-04-16 | $2.00/$8.00 |
| openai/gpt-4.1 | 2025-04-14 | $2.00/$8.00 |
| openai/gpt-4.1-mini | 2025-04-14 | $0.40/$1.60 |
| openai/gpt-4.1-nano | 2025-04-14 | $0.10/$0.40 |
| perplexity/sonar-deep-research | 2025-03-07 | $2.00/$8.00 |
| perplexity/sonar-pro | 2025-03-07 | $3.00/$15.00 |
| perplexity/sonar-reasoning-pro | 2025-03-07 | $2.00/$8.00 |
| mistralai/mistral-saba | 2025-02-17 | $0.20/$0.60 |
| perplexity/sonar | 2025-01-27 | $1.00/$1.00 |
| meta-llama/llama-3.3-70b-instruct | 2024-12-06 | $0.71/$0.71 |
| amazon/nova-lite-v1 | 2024-12-05 | $0.06/$0.24 |
| amazon/nova-micro-v1 | 2024-12-05 | $0.04/$0.14 |
| amazon/nova-pro-v1 | 2024-12-05 | $0.80/$3.20 |
| openai/gpt-4o-2024-11-20 | 2024-11-20 | $2.50/$10.00 |
| openai/gpt-4o-2024-08-06 | 2024-08-06 | $2.50/$10.00 |
| mistralai/mistral-large-2407 | 2024-07-24 | $2.00/$6.00 |
| meta-llama/llama-3.1-70b-instruct | 2024-07-23 | $0.72/$0.72 |
| meta-llama/llama-3.1-8b-instruct | 2024-07-23 | $0.02/$0.04 |
| mistralai/mistral-nemo | 2024-07-18 | $0.03/$0.03 |
| openai/gpt-4o-mini | 2024-07-18 | $0.15/$0.60 |
| openai/gpt-4o-mini-2024-07-18 | 2024-07-18 | $0.15/$0.60 |
| openai/gpt-4o | 2024-05-13 | $2.50/$10.00 |
| openai/gpt-4o-2024-05-13 | 2024-05-13 | $5.00/$15.00 |
| mistralai/mixtral-8x22b-instruct | 2024-04-17 | $2.00/$6.00 |
| mistralai/mistral-large | 2024-02-26 | $2.00/$6.00 |
| openai/gpt-3.5-turbo-0613 | 2024-01-25 | $1.00/$2.00 |
| openai/gpt-4 | 2023-11-06 | $30.00/$60.00 |
| openai/gpt-3.5-turbo-16k | 2023-08-28 | $3.00/$4.00 |
| nvidia/switchyard | — | — |
| openrouter/auto | — | — |
| openrouter/auto-beta | — | — |
| openrouter/auto-code | — | — |
| openrouter/bodybuilder | — | — |
| openrouter/fusion | — | — |
| openrouter/pareto-code | — | — |
| typesafe/jev-router | — | — |

## Agentgateway — 25 routes

Local gateway on `localhost:4000`. Entries are routing patterns, not fixed models.

| Model | Context |
|---|---|
| anthropic/* | — |
| claude-* | — |
| gemini-* | 1.05M |
| gemini/* | — |
| gpt-* | — |
| grok-* | — |
| mistral/* | — |
| o3* | — |
| ollama-cloud/* | — |
| ollama-deepseek | 1.00M |
| ollama-deepseek-balanced | 1.00M |
| ollama-dsf4 | 1.00M |
| ollama-gemma4 | — |
| ollama-glm-flash | — |
| ollama-minimax | — |
| ollama/* | — |
| ollama1/* | — |
| ollama2/* | — |
| ollama3/* | — |
| openai/* | — |
| opencode/* | — |
| openrouter/* | — |
| pi-default | — |
| pi-fast | 1.00M |
| xai/* | — |

## Azure — 31 deployments

Deployment names are chosen per subscription; select one with
`--model azure/<deployment>`. An uncataloged name still works — its window
falls back to the OpenAI entry it is named after, or override it with
`"contextWindow"` in `config.json`.

| Deployment | Context |
|---|---|
| gpt-4 | 8K |
| gpt-4-32k | 32K |
| gpt-4-turbo | 128K |
| gpt-4-turbo-128k | 128K |
| gpt-4.1 | 1.00M |
| gpt-4.1-mini | 1.00M |
| gpt-4o | 128K |
| gpt-4o-autox | 128K |
| gpt-4o-mini | 128K |
| gpt-5 | 250K |
| gpt-5-chat | 128K |
| gpt-5-mini | 250K |
| gpt-5.1 | 272K |
| gpt-5.1-chat | 112K |
| gpt-5.1-codex | 272K |
| gpt-5.1-codex-max | 272K |
| gpt-5.1-codex-mini | 272K |
| gpt-5.2 | 272K |
| gpt-5.2-chat | 112K |
| gpt-5.3-chat | 272K |
| gpt-5.3-codex | 272K |
| gpt-5.4 | 900K |
| gpt-5.4-mini | 272K |
| gpt-5.5 | 900K |
| gpt-5.6-luna | 1.05M |
| gpt-5.6-sol | 1.05M |
| gpt-5.6-terra | 1.05M |
| gpt-oss-120b | 272K |
| o1 | 200K |
| o1-mini | 128K |
| o3-mini | 200K |

## Updating This File

```bash
make generate-models-md   # regenerate every section from live providers
```

The Agentgateway section is generated from `hack/agentgateway/config.yaml`
rather than the live gateway: the gateway answers with whatever the local
config routes to, which is machine state rather than repo truth.
