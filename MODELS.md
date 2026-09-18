# Supported Models

Models available through `pi model list`, organized by provider. Every model
each provider currently serves is listed below. Prices are USD per 1M tokens
(input/output), from the embedded models.dev snapshot; a `—` means the
snapshot has no entry for that model.

Last updated: 2026-09-18

## Providers

| Provider | Models | Credentials |
|---|---|---|
| [Anthropic](#anthropic) | 11 | `ANTHROPIC_API_KEY` |
| [OpenAI](#openai) | 124 | `OPENAI_API_KEY` |
| [Google Gemini](#google-gemini) | 43 | `GEMINI_API_KEY` |
| [Mistral](#mistral) | 34 | `MISTRAL_API_KEY` |
| [xAI](#xai) | 7 | `XAI_API_KEY` |
| [Ollama](#ollama) | 12 | local daemon; no key needed |
| [OpenRouter](#openrouter) | 445 | `OPENROUTER_API_KEY` |
| [Agentgateway](#agentgateway) | 25 | local gateway |
| [Azure](#azure) | 31 deployments | `AZUREOPENAI_API_KEY`; names are per-subscription |

Azure has no live model-listing API — enumerating deployments needs ARM
credentials, so `pi model list azure` prints the embedded catalog shown below.

The CLI also reports an owner per model. It is shown only for Gemini, where it
is the human-readable display name; Anthropic and xAI report nothing useful
(the provider name itself), and OpenAI reports `openai-internal` for just
`text-embedding-ada-002`, `tts-1` and `whisper-1`.

## Anthropic — 11 models

API key: `ANTHROPIC_API_KEY`.

| Model | Release | Price (in/out per 1M) |
|---|---|---|
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

## OpenAI — 124 models

API key: `OPENAI_API_KEY`.

| Model | Release | Price (in/out per 1M) |
|---|---|---|
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
| gpt-5.3-chat-latest | 2026-03-03 | $1.75/$14.00 |
| gpt-5.3-codex | 2026-02-05 | $1.75/$14.00 |
| gpt-5.2 | 2025-12-11 | $1.75/$14.00 |
| gpt-5.2-2025-12-11 | 2025-12-11 | $1.75/$14.00 |
| gpt-5.2-chat-latest | 2025-12-11 | $1.75/$14.00 |
| gpt-5.2-codex | 2025-12-11 | $1.75/$14.00 |
| gpt-5.2-pro | 2025-12-11 | $21.00/$168.00 |
| gpt-5.2-pro-2025-12-11 | 2025-12-11 | $21.00/$168.00 |
| gpt-5.1 | 2025-11-13 | $1.25/$10.00 |
| gpt-5.1-2025-11-13 | 2025-11-13 | $1.25/$10.00 |
| gpt-5.1-chat-latest | 2025-11-13 | $1.25/$10.00 |
| gpt-5.1-codex | 2025-11-13 | $1.25/$10.00 |
| gpt-5.1-codex-max | 2025-11-13 | $1.25/$10.00 |
| gpt-5.1-codex-mini | 2025-11-13 | $1.25/$10.00 |
| gpt-5-pro | 2025-10-06 | $15.00/$120.00 |
| gpt-5-pro-2025-10-06 | 2025-10-06 | $15.00/$120.00 |
| gpt-5 | 2025-08-07 | $1.25/$10.00 |
| gpt-5-2025-08-07 | 2025-08-07 | $1.25/$10.00 |
| gpt-5-chat-latest | 2025-08-07 | $1.25/$10.00 |
| gpt-5-codex | 2025-08-07 | $1.25/$10.00 |
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
| o3-deep-research | 2025-04-16 | $2.00/$8.00 |
| o3-deep-research-2025-06-26 | 2025-04-16 | $2.00/$8.00 |
| o4-mini | 2025-04-16 | $1.10/$4.40 |
| o4-mini-2025-04-16 | 2025-04-16 | $1.10/$4.40 |
| o4-mini-deep-research | 2025-04-16 | $1.10/$4.40 |
| o4-mini-deep-research-2025-06-26 | 2025-04-16 | $1.10/$4.40 |
| gpt-4.1 | 2025-04-14 | $2.00/$8.00 |
| gpt-4.1-2025-04-14 | 2025-04-14 | $2.00/$8.00 |
| gpt-4.1-mini | 2025-04-14 | $0.40/$1.60 |
| gpt-4.1-mini-2025-04-14 | 2025-04-14 | $0.40/$1.60 |
| gpt-4.1-nano | 2025-04-14 | $0.10/$0.40 |
| gpt-4.1-nano-2025-04-14 | 2025-04-14 | $0.10/$0.40 |
| o1-pro | 2025-03-19 | $150.00/$600.00 |
| o1-pro-2025-03-19 | 2025-03-19 | $150.00/$600.00 |
| o3-mini | 2024-12-20 | $1.10/$4.40 |
| o3-mini-2025-01-31 | 2024-12-20 | $1.10/$4.40 |
| o1 | 2024-12-05 | $15.00/$60.00 |
| o1-2024-12-17 | 2024-12-05 | $15.00/$60.00 |
| gpt-4o-2024-11-20 | 2024-11-20 | $2.50/$10.00 |
| gpt-4o-2024-08-06 | 2024-08-06 | $2.50/$10.00 |
| gpt-4o-mini | 2024-07-18 | $0.15/$0.60 |
| gpt-4o-mini-2024-07-18 | 2024-07-18 | $0.15/$0.60 |
| gpt-4o-mini-search-preview | 2024-07-18 | $0.15/$0.60 |
| gpt-4o-mini-search-preview-2025-03-11 | 2024-07-18 | $0.15/$0.60 |
| gpt-4o-mini-transcribe | 2024-07-18 | $0.15/$0.60 |
| gpt-4o-mini-transcribe-2025-03-20 | 2024-07-18 | $0.15/$0.60 |
| gpt-4o-mini-transcribe-2025-12-15 | 2024-07-18 | $0.15/$0.60 |
| gpt-4o-mini-tts | 2024-07-18 | $0.15/$0.60 |
| gpt-4o-mini-tts-2025-03-20 | 2024-07-18 | $0.15/$0.60 |
| gpt-4o-mini-tts-2025-12-15 | 2024-07-18 | $0.15/$0.60 |
| gpt-4o | 2024-05-13 | $2.50/$10.00 |
| gpt-4o-2024-05-13 | 2024-05-13 | $5.00/$15.00 |
| gpt-4o-search-preview | 2024-05-13 | $2.50/$10.00 |
| gpt-4o-search-preview-2025-03-11 | 2024-05-13 | $2.50/$10.00 |
| gpt-4o-transcribe | 2024-05-13 | $2.50/$10.00 |
| gpt-4o-transcribe-diarize | 2024-05-13 | $2.50/$10.00 |
| text-embedding-3-large | 2024-01-25 | $0.13/$— |
| text-embedding-3-small | 2024-01-25 | $0.02/$— |
| gpt-4 | 2023-11-06 | $30.00/$60.00 |
| gpt-4-0613 | 2023-11-06 | $30.00/$60.00 |
| gpt-4-turbo | 2023-11-06 | $10.00/$30.00 |
| gpt-4-turbo-2024-04-09 | 2023-11-06 | $10.00/$30.00 |
| gpt-3.5-turbo | 2023-03-01 | $0.50/$1.50 |
| gpt-3.5-turbo-0125 | 2023-03-01 | $0.50/$1.50 |
| gpt-3.5-turbo-1106 | 2023-03-01 | $0.50/$1.50 |
| gpt-3.5-turbo-instruct | 2023-03-01 | $0.50/$1.50 |
| gpt-3.5-turbo-instruct-0914 | 2023-03-01 | $0.50/$1.50 |
| text-embedding-ada-002 | 2022-12-15 | $0.10/$— |
| babbage-002 | — | — |
| chat-latest | — | — |
| chatgpt-image-latest | — | — |
| davinci-002 | — | — |
| gpt-6-astra | — | — |
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
| gpt-realtime-translate | — | — |
| gpt-realtime-whisper | — | — |
| gpt-transcribe | — | — |
| omni-moderation-2024-09-26 | — | — |
| omni-moderation-latest | — | — |
| sora-2 | — | — |
| sora-2-pro | — | — |
| tts-1 | — | — |
| tts-1-1106 | — | — |
| tts-1-hd | — | — |
| tts-1-hd-1106 | — | — |
| whisper-1 | — | — |

## Google Gemini — 43 models

API key: `GEMINI_API_KEY`.

| Model | Release | Price (in/out per 1M) | Display name |
|---|---|---|---|
| gemini-3.8-flash | 2026-09-02 | $0.75/$3.75 | Gemini 3.8 Flash |
| gemini-3.7-flash | 2026-08-13 | $0.75/$3.75 | Gemini 3.7 Flash |
| gemini-flash-latest | 2026-08-13 | $0.75/$3.75 | Gemini Flash Latest |
| gemini-3.5-flash-lite | 2026-07-21 | $0.30/$2.50 | Gemini 3.5 Flash Lite |
| gemini-3.6-flash | 2026-07-21 | $0.75/$3.75 | Gemini 3.6 Flash |
| gemini-flash-lite-latest | 2026-07-21 | $0.30/$2.50 | Gemini Flash-Lite Latest |
| gemini-3.1-flash-lite-image | 2026-06-30 | $0.25/$30.00 | Nano Banana 2 Lite |
| gemini-3-pro-image | 2026-05-28 | $2.00/$120.00 | Nano Banana Pro |
| gemini-3.1-flash-image | 2026-05-28 | $0.50/$60.00 | Nano Banana 2 |
| gemini-3.5-flash | 2026-05-19 | $1.50/$9.00 | Gemini 3.5 Flash |
| gemini-3.1-flash-lite | 2026-05-07 | $0.25/$1.50 | Gemini 3.1 Flash Lite |
| gemini-embedding-2 | 2026-04-22 | $0.20/$— | Gemini Embedding 2 |
| gemini-embedding-2-preview | 2026-04-22 | $0.20/$— | Gemini Embedding 2 Preview |
| deep-research-max-preview-04-2026 | 2026-04-21 | $2.00/$12.00 | Deep Research Max Preview (Apr-21-2026) |
| deep-research-preview-04-2026 | 2026-04-21 | $2.00/$12.00 | Deep Research Preview (Apr-21-2026) |
| lyria-3-clip-preview | 2026-03-25 | $—/$— | Lyria 3 Clip Preview |
| lyria-3-pro-preview | 2026-03-25 | $—/$— | Lyria 3 Pro Preview |
| gemini-3.1-flash-lite-preview | 2026-03-03 | $0.25/$1.50 | Gemini 3.1 Flash Lite Preview |
| gemini-3.1-flash-image-preview | 2026-02-26 | $0.50/$60.00 | Nano Banana 2 |
| gemini-3.1-pro-preview | 2026-02-19 | $2.00/$12.00 | Gemini 3.1 Pro Preview |
| gemini-3.1-pro-preview-customtools | 2026-02-19 | $2.00/$12.00 | Gemini 3.1 Pro Preview Custom Tools |
| gemini-3-flash-preview | 2025-12-17 | $0.50/$3.00 | Gemini 3 Flash Preview |
| gemini-3-pro-image-preview | 2025-11-20 | $2.00/$120.00 | Nano Banana Pro |
| gemini-2.5-computer-use-preview-10-2025 | 2025-10-07 | $1.25/$10.00 | Gemini 2.5 Computer Use Preview 10-2025 |
| gemini-2.5-flash-image | 2025-08-26 | $0.30/$30.00 | Nano Banana |
| gemini-2.5-flash | 2025-06-17 | $0.30/$2.50 | Gemini 2.5 Flash |
| gemini-2.5-flash-lite | 2025-06-17 | $0.10/$0.40 | Gemini 2.5 Flash-Lite |
| gemini-2.5-flash-native-audio-latest | 2025-06-17 | $0.30/$2.50 | Gemini 2.5 Flash Native Audio Latest |
| gemini-2.5-pro | 2025-06-17 | $1.25/$10.00 | Gemini 2.5 Pro |
| gemini-embedding-001 | 2025-05-20 | $0.15/$— | Gemini Embedding 001 |
| antigravity-preview-05-2026 | — | — | Antigravity Agent Preview |
| antigravity-preview-09-2026 | — | — | Antigravity Agent Preview |
| aqa | — | — | Model that performs Attributed Question Answering. |
| deep-research-pro-preview-12-2025 | — | — | Deep Research Pro Preview (Dec-12-2025) |
| gemini-3.5-transcribe | — | — | Gemini 3.5 Transcribe |
| gemini-3.5-transcribe-live | — | — | Gemini 3.5 Transcribe Live |
| gemini-omni-1.1-flash | — | — | Gemini Omni 1.1 Flash |
| gemini-pro-latest | — | — | Gemini Pro Latest |
| gemini-robotics-er-2-preview | — | — | Gemini Robotics-ER 2 Preview |
| gemma-4-26b-a4b-it | — | — | Gemma 4 26B A4B IT |
| gemma-4-31b-it | — | — | Gemma 4 31B IT |
| lyria-3.5 | — | — | Lyria 3.5 |
| nano-banana-pro-preview | — | — | Nano Banana Pro |

## Mistral — 34 models

API key: `MISTRAL_API_KEY`.

| Model | Release | Price (in/out per 1M) | Context | Capabilities |
|---|---|---|---|---|
| zai-glm-5-2 | 2026-06-13 | $1.40/$4.40 | 1.05M | completion_chat, function_calling |
| mistral-medium-2604 | 2026-04-29 | $1.50/$7.50 | 262K | completion_chat, function_calling, vision |
| mistral-medium-latest | 2026-04-29 | $1.50/$7.50 | 262K | completion_chat, function_calling, vision |
| mistral-small-2603 | 2026-03-16 | $0.15/$0.60 | 262K | completion_chat, function_calling, vision |
| mistral-small-latest | 2026-03-16 | $0.15/$0.60 | 262K | completion_chat, function_calling, vision |
| voxtral-small-latest | 2025-07-15 | $0.10/$0.30 | 32K | completion_chat, function_calling |
| magistral-medium-latest | 2025-03-17 | $2.00/$5.00 | 262K | completion_chat, function_calling, vision |
| magistral-small-latest | 2025-03-17 | $0.50/$1.50 | 262K | completion_chat, function_calling, vision |
| mistral-large-2512 | 2024-11-01 | $0.50/$1.50 | 262K | completion_chat, function_calling, fine_tuning, vision |
| mistral-large-latest | 2024-11-01 | $0.50/$1.50 | 262K | completion_chat, function_calling, fine_tuning, vision |
| ministral-3b-latest | 2024-10-01 | $0.04/$0.04 | 131K | completion_chat, function_calling, fine_tuning, vision |
| ministral-8b-latest | 2024-10-01 | $0.10/$0.10 | 262K | completion_chat, function_calling, fine_tuning, vision |
| codestral-latest | 2024-05-29 | $0.30/$0.90 | 256K | completion_chat, completion_fim, function_calling |
| codestral-2508 | — | — | 256K | completion_chat, completion_fim, function_calling |
| glm-5-2 | — | — | 1.05M | completion_chat, function_calling |
| labs-leanstral-1-5 | — | — | 262K | completion_chat, function_calling, vision |
| labs-leanstral-1-5-1 | — | — | 262K | completion_chat, function_calling, vision |
| ministral-14b-2512 | — | — | 262K | completion_chat, function_calling, fine_tuning, vision |
| ministral-14b-latest | — | — | 262K | completion_chat, function_calling, fine_tuning, vision |
| ministral-3b-2512 | — | — | 131K | completion_chat, function_calling, fine_tuning, vision |
| ministral-8b-2512 | — | — | 262K | completion_chat, function_calling, fine_tuning, vision |
| mistral-code-fim-latest | — | — | 256K | completion_chat, completion_fim, function_calling |
| mistral-code-latest | — | — | 256K | completion_chat, completion_fim, function_calling |
| mistral-medium | — | — | 262K | completion_chat, function_calling, vision |
| mistral-medium-3 | — | — | 262K | completion_chat, function_calling, vision |
| mistral-medium-3-5 | — | — | 262K | completion_chat, function_calling, vision |
| mistral-medium-3.5 | — | — | 262K | completion_chat, function_calling, vision |
| mistral-vibe-cli-fast | — | — | 262K | completion_chat, function_calling, vision |
| mistral-vibe-cli-latest | — | — | 262K | completion_chat, function_calling, vision |
| mistral-vibe-cli-with-tools | — | — | 262K | completion_chat, function_calling, vision |
| voxtral-small-2507 | — | — | 32K | completion_chat, function_calling |
| zai-glm-5 | — | — | 1.05M | completion_chat, function_calling |
| zai-glm-5-3 | — | — | 1.05M | completion_chat, function_calling |
| zai-glm-latest | — | — | 1.05M | completion_chat, function_calling |

## xAI — 7 models

API key: `XAI_API_KEY`.

| Model | Release | Price (in/out per 1M) |
|---|---|---|
| grok-4.6 | 2026-08-12 | $2.00/$6.00 |
| grok-4.5 | 2026-07-08 | $2.00/$6.00 |
| grok-4.3 | 2026-04-17 | $1.25/$2.50 |
| grok-build-0.1 | 2026-04-16 | $1.00/$2.00 |
| grok-4.20-0309-non-reasoning | 2026-03-09 | $1.25/$2.50 |
| grok-4.20-0309-reasoning | 2026-03-09 | $1.25/$2.50 |
| grok-4.20-multi-agent-0309 | 2026-03-09 | $1.25/$2.50 |

## Ollama — 12 models

Local daemon, plus Ollama Cloud (`*:cloud` tags). No API key for local models.

| Model | Price (in/out per 1M) | Context | Capabilities |
|---|---|---|---|
| all-minilm:latest | — | 512 | embedding |
| deepseek-v4-flash:0731-cloud | $0.22/$0.66 | 1.05M | completion, thinking, tools |
| deepseek-v4-flash:cloud | $0.22/$0.66 | 1.05M | completion, thinking, tools |
| deepseek-v4.1-flash:cloud | $0.15/$0.60 | 1.05M | completion, thinking, tools, vision |
| embeddinggemma:latest | — | 2K | embedding |
| gemma4:12b-mlx | — | — | completion, thinking, tools, vision |
| gemma4:31b-cloud | $0.14/$0.40 | 262K | completion, thinking, tools, vision |
| gemma4:cloud | $0.14/$0.40 | 262K | completion, thinking, tools, vision |
| glm-5.3-flash:cloud | $0.15/$0.50 | 1.05M | completion, thinking, tools, vision |
| glm-5.3:cloud | $1.40/$4.40 | 1.05M | completion, thinking, tools |
| kimi-k3:cloud | $3.00/$15.00 | 1.05M | completion, thinking, tools, vision |
| minimax-m3:cloud | $0.60/$2.40 | 524K | completion, thinking, tools, vision |

## OpenRouter — 445 models

API key: `OPENROUTER_API_KEY`.

| Model | Release | Price (in/out per 1M) |
|---|---|---|
| google/gemini-3.8-flash | 2026-09-02 | $0.75/$3.75 |
| google/gemini-3.8-flash:batch | 2026-09-02 | $0.75/$3.75 |
| meta/muse-spark-1.3 | 2026-09-02 | $1.25/$4.25 |
| meta/muse-spark-1.3-contributor | 2026-09-02 | $0.10/$0.20 |
| anthropic/claude-fable-5.1 | 2026-09-01 | $10.00/$50.00 |
| anthropic/claude-fable-5.1:batch | 2026-09-01 | $10.00/$50.00 |
| ibm-granite/granite-4.2-8b | 2026-08-31 | $0.10/$0.15 |
| tencent/hy4-preview | 2026-08-28 | $0.83/$2.50 |
| inclusionai/ling-3.0-flash-fin:free | 2026-08-27 | $—/$— |
| ~z-ai/glm-flash-latest | 2026-08-27 | $0.07/$0.25 |
| qwen/qwen3.8-flash | 2026-08-26 | $0.15/$0.47 |
| z-ai/glm-5.3-flash | 2026-08-26 | $0.07/$0.25 |
| z-ai/glm-5.3-flash:batch | 2026-08-26 | $0.07/$0.25 |
| deepseek/deepseek-v4-flash-vision-exp | 2026-08-21 | $0.22/$0.66 |
| deepseek/deepseek-v4-flash-vision-exp:batch | 2026-08-21 | $0.22/$0.66 |
| meta/muse-spark-1.2-contributor | 2026-08-21 | $0.10/$0.20 |
| tencent/hy-mt2-1.8b | 2026-08-20 | $0.04/$0.18 |
| tencent/hy-mt2-30b-a3b | 2026-08-20 | $0.07/$0.29 |
| tencent/hy-mt2-7b | 2026-08-19 | $0.07/$0.29 |
| ~z-ai/glm-latest | 2026-08-19 | $1.13/$3.56 |
| dots-studio/dots-3-note-preview:free | 2026-08-14 | $—/$— |
| qwen/qwen3.8-27b | 2026-08-14 | $0.42/$2.55 |
| qwen/qwen3.8-27b:free | 2026-08-14 | $0.42/$2.55 |
| z-ai/glm-5.3 | 2026-08-14 | $1.40/$4.40 |
| z-ai/glm-5.3:batch | 2026-08-14 | $1.40/$4.40 |
| google/gemini-3.7-flash | 2026-08-13 | $0.75/$3.75 |
| google/gemini-3.7-flash:batch | 2026-08-13 | $0.75/$3.75 |
| bytedance-seed/seed-2-1-turbo | 2026-08-12 | $0.50/$2.50 |
| deepseek/deepseek-v4-pro-0813 | 2026-08-12 | $0.66/$1.98 |
| deepseek/deepseek-v4-pro-0813:batch | 2026-08-12 | $0.66/$1.98 |
| qwen/qwen3.8-2.4t-a95b | 2026-08-12 | $2.00/$6.00 |
| qwen/qwen3.8-2.4t-a95b:batch | 2026-08-12 | $2.00/$6.00 |
| x-ai/grok-4.6 | 2026-08-12 | $2.00/$6.00 |
| liquid/lfm-2.5-2.6b:free | 2026-08-11 | $—/$— |
| nvidia/nemotron-3.5-lightning | 2026-08-11 | $0.08/$0.20 |
| nvidia/nemotron-3.5-lightning:free | 2026-08-11 | $—/$— |
| meta/muse-glimmer-30b | 2026-08-10 | $0.30/$1.10 |
| meta/muse-glimmer-30b:batch | 2026-08-10 | $0.30/$1.10 |
| upstage/solar-pro4 | 2026-08-10 | $0.03/$0.12 |
| meta/muse-spark-1.2 | 2026-08-05 | $1.25/$4.25 |
| qwen/qwen3.8-max-0902 | 2026-08-03 | $2.00/$6.00 |
| sakana/sakana-namazu | 2026-08-03 | $0.95/$4.00 |
| ~deepseek/deepseek-v4-flash-latest | 2026-08-01 | $0.05/$0.16 |
| deepseek/deepseek-v4-flash-0731 | 2026-07-31 | $0.07/$0.18 |
| deepseek/deepseek-v4-flash-0731:batch | 2026-07-31 | $0.07/$0.18 |
| deepseek/deepseek-v4-flash-0731:free | 2026-07-31 | $0.07/$0.18 |
| thinkingmachines/inkling-small | 2026-07-30 | $0.45/$1.20 |
| thinkingmachines/inkling-small:free | 2026-07-30 | $—/$— |
| anthropic/claude-opus-5 | 2026-07-24 | $5.00/$25.00 |
| anthropic/claude-opus-5:batch | 2026-07-24 | $5.00/$25.00 |
| inclusionai/ling-3.0-flash | 2026-07-23 | $0.02/$0.06 |
| inclusionai/ling-3.0-flash-fin | 2026-07-23 | $0.02/$0.06 |
| inclusionai/ling-3.0-flash-sante:free | 2026-07-23 | $0.02/$0.06 |
| inclusionai/ling-3.0-flash-vl | 2026-07-23 | $0.02/$0.06 |
| inclusionai/ling-3.0-flash-vl:free | 2026-07-23 | $0.02/$0.06 |
| google/gemini-3.5-flash-lite | 2026-07-21 | $0.30/$2.50 |
| google/gemini-3.5-flash-lite:batch | 2026-07-21 | $0.30/$2.50 |
| google/gemini-3.6-flash | 2026-07-21 | $0.75/$3.75 |
| google/gemini-3.6-flash:batch | 2026-07-21 | $0.75/$3.75 |
| poolside/laguna-s-2.1 | 2026-07-21 | $0.09/$0.18 |
| poolside/laguna-s-2.1:free | 2026-07-21 | $—/$— |
| meituan/longcat-2.0 | 2026-07-20 | $0.30/$1.20 |
| moonshotai/kimi-k3 | 2026-07-16 | $3.00/$15.00 |
| moonshotai/kimi-k3:batch | 2026-07-16 | $3.00/$15.00 |
| qwen/qwen3.7-flash | 2026-07-15 | $0.03/$0.13 |
| thinkingmachines/inkling | 2026-07-15 | $1.00/$4.05 |
| thinkingmachines/inkling:batch | 2026-07-15 | $1.00/$4.05 |
| thinkingmachines/inkling:free | 2026-07-15 | $—/$— |
| kwaipilot/kat-coder-pro-v2.5 | 2026-07-10 | $0.74/$2.96 |
| openai/gpt-5.6-luna | 2026-07-09 | $0.20/$1.20 |
| openai/gpt-5.6-luna-pro | 2026-07-09 | $0.20/$1.20 |
| openai/gpt-5.6-luna-pro:batch | 2026-07-09 | $0.20/$1.20 |
| openai/gpt-5.6-luna:batch | 2026-07-09 | $0.20/$1.20 |
| openai/gpt-5.6-sol | 2026-07-09 | $2.00/$10.00 |
| openai/gpt-5.6-sol-pro | 2026-07-09 | $2.00/$10.00 |
| openai/gpt-5.6-sol-pro:batch | 2026-07-09 | $2.00/$10.00 |
| openai/gpt-5.6-sol:batch | 2026-07-09 | $2.00/$10.00 |
| openai/gpt-5.6-terra | 2026-07-09 | $2.00/$12.00 |
| openai/gpt-5.6-terra-pro | 2026-07-09 | $2.00/$12.00 |
| openai/gpt-5.6-terra-pro:batch | 2026-07-09 | $2.00/$12.00 |
| openai/gpt-5.6-terra:batch | 2026-07-09 | $2.00/$12.00 |
| x-ai/grok-4.5 | 2026-07-08 | $2.00/$6.00 |
| ~x-ai/grok-latest | 2026-07-08 | $2.00/$6.00 |
| aion-labs/aion-3.0 | 2026-07-07 | $3.00/$6.00 |
| aion-labs/aion-3.0-mini | 2026-07-07 | $0.70/$1.40 |
| tencent/hy3 | 2026-07-06 | $0.13/$0.53 |
| poolside/laguna-xs-2.1 | 2026-07-02 | $0.06/$0.12 |
| poolside/laguna-xs-2.1:free | 2026-07-02 | $—/$— |
| anthropic/claude-sonnet-5 | 2026-06-30 | $2.00/$10.00 |
| anthropic/claude-sonnet-5:batch | 2026-06-30 | $2.00/$10.00 |
| google/gemini-3.1-flash-lite-image | 2026-06-30 | $0.25/$1.50 |
| cohere/north-mini-code:free | 2026-06-17 | $—/$— |
| sakana/fugu-ultra | 2026-06-15 | $5.00/$30.00 |
| sakana/fugu-ultra-v2 | 2026-06-15 | $5.00/$30.00 |
| z-ai/glm-5.2 | 2026-06-13 | $0.97/$3.04 |
| z-ai/glm-5.2:batch | 2026-06-13 | $0.97/$3.04 |
| z-ai/glm-5.2:free | 2026-06-13 | $—/$— |
| moonshotai/kimi-k2.7-code | 2026-06-12 | $0.66/$3.40 |
| anthropic/claude-fable-5 | 2026-06-09 | $10.00/$50.00 |
| anthropic/claude-fable-5:batch | 2026-06-09 | $10.00/$50.00 |
| ~anthropic/claude-fable-latest | 2026-06-09 | $10.00/$50.00 |
| nvidia/nemotron-3-ultra-550b-a55b | 2026-06-04 | $0.60/$2.40 |
| nvidia/nemotron-3-ultra-550b-a55b:free | 2026-06-04 | $—/$— |
| nvidia/nemotron-3.5-content-safety:free | 2026-06-04 | $—/$— |
| qwen/qwen3.7-plus | 2026-06-02 | $0.32/$1.28 |
| minimax/minimax-m3 | 2026-06-01 | $0.30/$1.20 |
| minimax/minimax-m3:batch | 2026-06-01 | $0.30/$1.20 |
| stepfun/step-3.7-flash | 2026-05-29 | $0.20/$1.15 |
| anthropic/claude-opus-4.8 | 2026-05-28 | $5.00/$25.00 |
| anthropic/claude-opus-4.8:batch | 2026-05-28 | $5.00/$25.00 |
| google/gemini-3-pro-image | 2026-05-28 | $2.00/$12.00 |
| google/gemini-3.1-flash-image | 2026-05-28 | $0.50/$3.00 |
| qwen/qwen3.7-max | 2026-05-21 | $1.48/$4.42 |
| google/gemini-3.5-flash | 2026-05-19 | $1.50/$9.00 |
| google/gemini-3.5-flash:batch | 2026-05-19 | $1.50/$9.00 |
| perceptron/perceptron-mk1 | 2026-05-12 | $0.15/$1.50 |
| google/gemini-3.1-flash-lite | 2026-05-07 | $0.25/$1.50 |
| google/gemini-3.1-flash-lite:batch | 2026-05-07 | $0.25/$1.50 |
| openai/gpt-chat-latest | 2026-05-05 | $5.00/$30.00 |
| mistralai/mistral-medium-3-5 | 2026-04-30 | $1.50/$7.50 |
| mistralai/mistral-medium-3-5:batch | 2026-04-30 | $1.50/$7.50 |
| nvidia/nemotron-3-nano-omni-30b-a3b-reasoning:free | 2026-04-28 | $—/$— |
| qwen/qwen3.5-plus-20260420 | 2026-04-27 | $0.30/$1.80 |
| qwen/qwen3.6-flash | 2026-04-27 | $0.19/$1.12 |
| ~anthropic/claude-haiku-latest | 2026-04-27 | $1.00/$5.00 |
| ~anthropic/claude-sonnet-latest | 2026-04-27 | $2.00/$10.00 |
| ~google/gemini-flash-latest | 2026-04-27 | $0.75/$3.75 |
| ~google/gemini-pro-latest | 2026-04-27 | $2.00/$12.00 |
| ~moonshotai/kimi-latest | 2026-04-27 | $2.55/$12.75 |
| ~openai/gpt-mini-latest | 2026-04-27 | $0.75/$4.50 |
| deepseek/deepseek-v4-flash | 2026-04-24 | $0.09/$0.17 |
| deepseek/deepseek-v4-pro | 2026-04-24 | $1.04/$2.07 |
| openai/gpt-5.5 | 2026-04-23 | $5.00/$30.00 |
| openai/gpt-5.5-pro | 2026-04-23 | $30.00/$180.00 |
| openai/gpt-5.5-pro:batch | 2026-04-23 | $30.00/$180.00 |
| openai/gpt-5.5:batch | 2026-04-23 | $5.00/$30.00 |
| qwen/qwen3.6-27b | 2026-04-22 | $0.60/$3.60 |
| xiaomi/mimo-v2.5 | 2026-04-22 | $0.14/$0.28 |
| xiaomi/mimo-v2.5-pro | 2026-04-22 | $0.43/$0.87 |
| moonshotai/kimi-k2.6 | 2026-04-21 | $0.95/$4.00 |
| openai/gpt-5.4-image-2 | 2026-04-21 | $8.00/$15.00 |
| ~anthropic/claude-opus-latest | 2026-04-21 | $5.00/$25.00 |
| qwen/qwen3.6-max-preview | 2026-04-20 | $1.03/$6.16 |
| tencent/hy3-preview | 2026-04-20 | $0.18/$0.60 |
| qwen/qwen3.6-35b-a3b | 2026-04-17 | $0.10/$0.90 |
| x-ai/grok-4.3 | 2026-04-17 | $1.25/$2.50 |
| x-ai/grok-4.3:batch | 2026-04-17 | $1.25/$2.50 |
| anthropic/claude-opus-4.7 | 2026-04-16 | $5.00/$25.00 |
| anthropic/claude-opus-4.7:batch | 2026-04-16 | $5.00/$25.00 |
| x-ai/grok-build-0.1 | 2026-04-16 | $1.00/$2.00 |
| meta/muse-spark-1.1 | 2026-04-08 | $1.25/$4.25 |
| z-ai/glm-5.1 | 2026-04-07 | $0.97/$3.04 |
| google/gemma-4-26b-a4b-it | 2026-04-02 | $0.07/$0.34 |
| google/gemma-4-26b-a4b-it:free | 2026-04-02 | $—/$— |
| google/gemma-4-31b-it | 2026-04-02 | $0.09/$0.34 |
| google/gemma-4-31b-it:free | 2026-04-02 | $—/$— |
| qwen/qwen3.6-plus | 2026-04-02 | $0.33/$1.95 |
| arcee-ai/trinity-large-thinking | 2026-04-01 | $0.25/$0.80 |
| z-ai/glm-5v-turbo | 2026-04-01 | $1.20/$4.00 |
| x-ai/grok-4.20 | 2026-03-31 | $1.25/$2.50 |
| x-ai/grok-4.20-multi-agent | 2026-03-31 | $1.25/$2.50 |
| kwaipilot/kat-coder-pro-v2 | 2026-03-27 | $0.30/$1.20 |
| google/lyria-3-clip-preview | 2026-03-25 | $—/$— |
| google/lyria-3-pro-preview | 2026-03-25 | $—/$— |
| rekaai/reka-edge | 2026-03-20 | $0.10/$0.10 |
| minimax/minimax-m2.7 | 2026-03-18 | $0.30/$1.20 |
| openai/gpt-5.4-mini | 2026-03-17 | $0.75/$4.50 |
| openai/gpt-5.4-mini:batch | 2026-03-17 | $0.75/$4.50 |
| openai/gpt-5.4-nano | 2026-03-17 | $0.20/$1.25 |
| openai/gpt-5.4-nano:batch | 2026-03-17 | $0.20/$1.25 |
| mistralai/mistral-small-2603 | 2026-03-16 | $0.15/$0.60 |
| mistralai/mistral-small-2603:batch | 2026-03-16 | $0.15/$0.60 |
| z-ai/glm-5-turbo | 2026-03-16 | $1.20/$4.00 |
| nvidia/nemotron-3-super-120b-a12b | 2026-03-11 | $0.09/$0.40 |
| nvidia/nemotron-3-super-120b-a12b:free | 2026-03-11 | $—/$— |
| openai/gpt-5.4 | 2026-03-05 | $2.50/$15.00 |
| openai/gpt-5.4-pro | 2026-03-05 | $30.00/$180.00 |
| openai/gpt-5.4-pro:batch | 2026-03-05 | $30.00/$180.00 |
| openai/gpt-5.4:batch | 2026-03-05 | $2.50/$15.00 |
| inception/mercury-2 | 2026-03-04 | $0.25/$0.75 |
| inception/mercury-2.5 | 2026-03-04 | $0.25/$0.75 |
| google/gemini-3.1-flash-lite-preview | 2026-03-03 | $0.25/$1.50 |
| google/gemini-3.1-flash-image-preview | 2026-02-26 | $0.50/$3.00 |
| qwen/qwen3.5-flash-02-23 | 2026-02-25 | $0.07/$0.26 |
| aion-labs/aion-2.0 | 2026-02-23 | $0.80/$1.60 |
| qwen/qwen3.5-122b-a10b | 2026-02-23 | $0.29/$2.40 |
| qwen/qwen3.5-27b | 2026-02-23 | $0.20/$1.56 |
| qwen/qwen3.5-35b-a3b | 2026-02-23 | $0.25/$1.25 |
| qwen/qwen3.5-9b | 2026-02-23 | $0.10/$0.15 |
| qwen/qwen3.5-9b:batch | 2026-02-23 | $0.10/$0.15 |
| google/gemini-3.1-pro-preview | 2026-02-19 | $2.00/$12.00 |
| google/gemini-3.1-pro-preview-customtools | 2026-02-19 | $2.00/$12.00 |
| google/gemini-3.1-pro-preview:batch | 2026-02-19 | $2.00/$12.00 |
| anthropic/claude-sonnet-4.6 | 2026-02-17 | $3.00/$15.00 |
| anthropic/claude-sonnet-4.6:batch | 2026-02-17 | $3.00/$15.00 |
| qwen/qwen3.5-plus-02-15 | 2026-02-16 | $0.26/$1.56 |
| qwen/qwen3.5-397b-a17b | 2026-02-15 | $0.55/$3.50 |
| bytedance-seed/seed-2.0-code | 2026-02-14 | $0.50/$3.00 |
| bytedance-seed/seed-2.0-lite | 2026-02-14 | $0.25/$2.00 |
| bytedance-seed/seed-2.0-mini | 2026-02-14 | $0.10/$0.40 |
| minimax/minimax-m2.5 | 2026-02-12 | $0.27/$1.08 |
| z-ai/glm-5 | 2026-02-12 | $0.60/$1.92 |
| qwen/qwen3-max-thinking | 2026-02-09 | $0.78/$3.90 |
| anthropic/claude-opus-4.6 | 2026-02-05 | $5.00/$25.00 |
| anthropic/claude-opus-4.6:batch | 2026-02-05 | $5.00/$25.00 |
| openai/gpt-5.3-codex | 2026-02-05 | $1.75/$14.00 |
| qwen/qwen3-coder-next | 2026-02-03 | $0.12/$0.80 |
| openrouter/free | 2026-02-01 | $—/$— |
| stepfun/step-3.5-flash | 2026-01-29 | $0.10/$0.30 |
| upstage/solar-pro-3 | 2026-01-27 | $0.15/$0.60 |
| minimax/minimax-m2-her | 2026-01-23 | $0.30/$1.20 |
| writer/palmyra-x5 | 2026-01-21 | $0.60/$6.00 |
| openai/gpt-audio | 2026-01-19 | $2.50/$10.00 |
| openai/gpt-audio-mini | 2026-01-19 | $0.60/$2.40 |
| z-ai/glm-4.7-flash | 2026-01-19 | $0.06/$0.40 |
| moonshotai/kimi-k2.5 | 2026-01 | $0.45/$2.25 |
| bytedance-seed/seed-1.6 | 2025-12-23 | $0.25/$2.00 |
| bytedance-seed/seed-1.6-flash | 2025-12-23 | $0.07/$0.30 |
| minimax/minimax-m2.1 | 2025-12-23 | $0.30/$1.20 |
| z-ai/glm-4.7 | 2025-12-22 | $0.40/$1.75 |
| google/gemini-3-flash-preview | 2025-12-17 | $0.50/$3.00 |
| google/gemini-3-flash-preview:batch | 2025-12-17 | $0.50/$3.00 |
| nvidia/nemotron-3-nano-30b-a3b | 2025-12-15 | $0.05/$0.20 |
| openai/gpt-5.2 | 2025-12-11 | $1.75/$14.00 |
| openai/gpt-5.2-codex | 2025-12-11 | $1.75/$14.00 |
| openai/gpt-5.2-pro | 2025-12-11 | $21.00/$168.00 |
| openai/gpt-5.2-pro:batch | 2025-12-11 | $21.00/$168.00 |
| openai/gpt-5.2:batch | 2025-12-11 | $1.75/$14.00 |
| openai/gpt-5.2-chat | 2025-12-10 | $1.75/$14.00 |
| mistralai/devstral-2512 | 2025-12-09 | $0.40/$2.00 |
| relace/relace-search | 2025-12-08 | $1.00/$3.00 |
| z-ai/glm-4.6v | 2025-12-08 | $0.30/$0.90 |
| amazon/nova-2-lite-v1 | 2025-12-02 | $0.30/$2.50 |
| mistralai/ministral-14b-2512 | 2025-12-02 | $0.20/$0.20 |
| mistralai/ministral-3b-2512 | 2025-12-02 | $0.10/$0.10 |
| mistralai/ministral-8b-2512 | 2025-12-02 | $0.15/$0.15 |
| mistralai/ministral-8b-2512:batch | 2025-12-02 | $0.15/$0.15 |
| deepseek/deepseek-chat | 2025-12-01 | $0.32/$0.89 |
| deepseek/deepseek-v3.2 | 2025-12-01 | $0.27/$0.40 |
| anthropic/claude-opus-4.5 | 2025-11-24 | $5.00/$25.00 |
| anthropic/claude-opus-4.5:batch | 2025-11-24 | $5.00/$25.00 |
| google/gemini-3-pro-image-preview | 2025-11-20 | $2.00/$12.00 |
| openai/gpt-5.1 | 2025-11-13 | $1.25/$10.00 |
| openai/gpt-5.1-codex | 2025-11-13 | $1.25/$10.00 |
| openai/gpt-5.1-codex-max | 2025-11-13 | $1.25/$10.00 |
| openai/gpt-5.1-codex-mini | 2025-11-13 | $0.25/$2.00 |
| openai/gpt-5.1:batch | 2025-11-13 | $1.25/$10.00 |
| moonshotai/kimi-k2-thinking | 2025-11-06 | $0.60/$2.50 |
| amazon/nova-premier-v1 | 2025-10-31 | $2.50/$12.50 |
| mistralai/voxtral-small-24b-2507 | 2025-10-30 | $0.10/$0.30 |
| perplexity/sonar-pro-search | 2025-10-30 | $3.00/$15.00 |
| openai/gpt-oss-safeguard-20b | 2025-10-29 | $0.07/$0.30 |
| minimax/minimax-m2 | 2025-10-27 | $0.26/$1.02 |
| qwen/qwen3-vl-32b-instruct | 2025-10-23 | $0.10/$0.42 |
| ibm-granite/granite-4.0-h-micro | 2025-10-20 | $0.02/$0.11 |
| openai/gpt-5-image-mini | 2025-10-16 | $2.50/$2.00 |
| anthropic/claude-haiku-4.5 | 2025-10-15 | $1.00/$5.00 |
| anthropic/claude-haiku-4.5:batch | 2025-10-15 | $1.00/$5.00 |
| openai/gpt-5-image | 2025-10-14 | $10.00/$10.00 |
| qwen/qwen3-vl-8b-instruct | 2025-10-14 | $0.12/$0.46 |
| qwen/qwen3-vl-8b-thinking | 2025-10-14 | $0.18/$2.10 |
| openai/gpt-5-pro | 2025-10-06 | $15.00/$120.00 |
| openai/gpt-5-pro:batch | 2025-10-06 | $15.00/$120.00 |
| qwen/qwen3-vl-30b-a3b-instruct | 2025-10-06 | $0.15/$0.60 |
| qwen/qwen3-vl-30b-a3b-thinking | 2025-10-06 | $0.20/$2.40 |
| z-ai/glm-4.6 | 2025-09-30 | $0.55/$2.20 |
| anthropic/claude-sonnet-4.5 | 2025-09-29 | $3.00/$15.00 |
| anthropic/claude-sonnet-4.5:batch | 2025-09-29 | $3.00/$15.00 |
| deepseek/deepseek-v3.2-exp | 2025-09-29 | $0.27/$0.41 |
| thedrummer/cydonia-24b-v4.1 | 2025-09-27 | $0.30/$0.50 |
| relace/relace-apply-3 | 2025-09-26 | $0.85/$1.25 |
| qwen/qwen3-max | 2025-09-23 | $0.78/$3.90 |
| qwen/qwen3-vl-235b-a22b-instruct | 2025-09-23 | $0.21/$1.90 |
| qwen/qwen3-vl-235b-a22b-thinking | 2025-09-23 | $0.40/$4.00 |
| deepseek/deepseek-v3.1-terminus | 2025-09-22 | $0.27/$1.00 |
| qwen/qwen-plus-2025-07-28 | 2025-09-08 | $0.26/$0.78 |
| moonshotai/kimi-k2-0905 | 2025-09-04 | $0.60/$2.50 |
| qwen/qwen3-next-80b-a3b-instruct | 2025-09 | $0.10/$1.10 |
| qwen/qwen3-next-80b-a3b-thinking | 2025-09 | $0.15/$1.20 |
| qwen/qwen3-30b-a3b-thinking-2507 | 2025-08-28 | $0.20/$2.40 |
| google/gemini-2.5-flash-image | 2025-08-26 | $0.30/$2.50 |
| nousresearch/hermes-4-405b | 2025-08-26 | $1.00/$3.00 |
| deepseek/deepseek-chat-v3.1 | 2025-08-21 | $0.25/$0.95 |
| mistralai/mistral-medium-3.1 | 2025-08-13 | $0.40/$2.00 |
| mistralai/mistral-medium-3.1:batch | 2025-08-13 | $0.40/$2.00 |
| z-ai/glm-4.5v | 2025-08-11 | $0.60/$1.80 |
| openai/gpt-5 | 2025-08-07 | $1.25/$10.00 |
| openai/gpt-5-mini | 2025-08-07 | $0.25/$2.00 |
| openai/gpt-5-mini:batch | 2025-08-07 | $0.25/$2.00 |
| openai/gpt-5-nano | 2025-08-07 | $0.05/$0.40 |
| openai/gpt-5-nano:batch | 2025-08-07 | $0.05/$0.40 |
| openai/gpt-5:batch | 2025-08-07 | $1.25/$10.00 |
| anthropic/claude-opus-4.1 | 2025-08-05 | $15.00/$75.00 |
| anthropic/claude-opus-4.1:batch | 2025-08-05 | $15.00/$75.00 |
| openai/gpt-oss-120b | 2025-08-05 | $0.04/$0.17 |
| openai/gpt-oss-120b:batch | 2025-08-05 | $0.04/$0.17 |
| openai/gpt-oss-20b | 2025-08-05 | $0.03/$0.13 |
| mistralai/codestral-2508 | 2025-08-01 | $0.30/$0.90 |
| mistralai/codestral-2508:batch | 2025-08-01 | $0.30/$0.90 |
| qwen/qwen3-30b-a3b-instruct-2507 | 2025-07-29 | $0.05/$0.19 |
| qwen/qwen3-coder-flash | 2025-07-28 | $0.20/$0.97 |
| z-ai/glm-4.5 | 2025-07-28 | $0.60/$2.20 |
| z-ai/glm-4.5-air | 2025-07-28 | $0.13/$0.85 |
| qwen/qwen3-235b-a22b-thinking-2507 | 2025-07-25 | $0.23/$2.30 |
| qwen/qwen3-coder | 2025-07-23 | $0.30/$1.00 |
| qwen/qwen3-coder-plus | 2025-07-23 | $0.65/$3.25 |
| bytedance/ui-tars-1.5-7b | 2025-07-22 | $0.10/$0.20 |
| qwen/qwen3-235b-a22b-2507 | 2025-07-21 | $0.09/$0.35 |
| moonshotai/kimi-k2 | 2025-07-11 | $0.57/$2.30 |
| cognitivecomputations/dolphin-mistral-24b-venice-edition | 2025-07-09 | $0.20/$0.90 |
| tencent/hunyuan-a13b-instruct | 2025-07-08 | $0.14/$0.57 |
| morph/morph-v3-fast | 2025-07-07 | $0.80/$1.20 |
| morph/morph-v3-large | 2025-07-07 | $0.90/$1.90 |
| baidu/ernie-4.5-vl-424b-a47b | 2025-06-30 | $0.42/$1.25 |
| mistralai/mistral-small-3.2-24b-instruct | 2025-06-20 | $0.07/$0.20 |
| google/gemini-2.5-flash | 2025-06-17 | $0.30/$2.50 |
| google/gemini-2.5-flash-lite | 2025-06-17 | $0.10/$0.40 |
| google/gemini-2.5-flash-lite:batch | 2025-06-17 | $0.10/$0.40 |
| google/gemini-2.5-flash:batch | 2025-06-17 | $0.30/$2.50 |
| google/gemini-2.5-pro | 2025-06-17 | $1.25/$10.00 |
| google/gemini-2.5-pro:batch | 2025-06-17 | $1.25/$10.00 |
| minimax/minimax-m1 | 2025-06-17 | $0.55/$2.20 |
| openai/o3-pro | 2025-06-10 | $20.00/$80.00 |
| google/gemini-2.5-pro-preview | 2025-06-05 | $1.25/$10.00 |
| deepseek/deepseek-r1-0528 | 2025-05-28 | $0.50/$2.15 |
| anthropic/claude-opus-4 | 2025-05-22 | $15.00/$75.00 |
| anthropic/claude-sonnet-4 | 2025-05-22 | $3.00/$15.00 |
| mistralai/mistral-medium-3 | 2025-05-07 | $0.40/$2.00 |
| meta-llama/llama-guard-4-12b | 2025-04-30 | $0.18/$0.18 |
| qwen/qwen3-14b | 2025-04-28 | $0.12/$0.24 |
| qwen/qwen3-30b-a3b | 2025-04-28 | $0.12/$0.50 |
| qwen/qwen3-8b | 2025-04-28 | $0.12/$0.46 |
| openai/o3 | 2025-04-16 | $2.00/$8.00 |
| openai/o3:batch | 2025-04-16 | $2.00/$8.00 |
| openai/o4-mini | 2025-04-16 | $1.10/$4.40 |
| openai/o4-mini-high | 2025-04-16 | $1.10/$4.40 |
| openai/o4-mini:batch | 2025-04-16 | $1.10/$4.40 |
| openai/gpt-4.1 | 2025-04-14 | $2.00/$8.00 |
| openai/gpt-4.1-mini | 2025-04-14 | $0.40/$1.60 |
| openai/gpt-4.1-mini:batch | 2025-04-14 | $0.40/$1.60 |
| openai/gpt-4.1-nano | 2025-04-14 | $0.10/$0.40 |
| openai/gpt-4.1-nano:batch | 2025-04-14 | $0.10/$0.40 |
| openai/gpt-4.1:batch | 2025-04-14 | $2.00/$8.00 |
| meta-llama/llama-4-maverick | 2025-04-05 | $0.20/$0.70 |
| meta-llama/llama-4-scout | 2025-04-05 | $0.10/$0.30 |
| qwen/qwen3-235b-a22b | 2025-04 | $0.46/$1.82 |
| qwen/qwen3-32b | 2025-04 | $0.08/$0.28 |
| qwen/qwen3-coder-30b-a3b-instruct | 2025-04 | $0.07/$0.28 |
| deepseek/deepseek-chat-v3-0324 | 2025-03-24 | $0.25/$1.00 |
| openai/o1-pro | 2025-03-19 | $150.00/$600.00 |
| mistralai/mistral-small-3.1-24b-instruct | 2025-03-17 | $0.35/$0.56 |
| cohere/command-a | 2025-03-13 | $2.50/$10.00 |
| google/gemma-3-12b-it | 2025-03-13 | $0.05/$0.15 |
| google/gemma-3-4b-it | 2025-03-13 | $0.05/$0.10 |
| google/gemma-3-27b-it | 2025-03-12 | $0.08/$0.45 |
| rekaai/reka-flash-3 | 2025-03-12 | $0.10/$0.20 |
| thedrummer/skyfall-36b-v2 | 2025-03-10 | $0.55/$0.80 |
| perplexity/sonar-deep-research | 2025-03-07 | $2.00/$8.00 |
| perplexity/sonar-pro | 2025-03-07 | $3.00/$15.00 |
| perplexity/sonar-reasoning-pro | 2025-03-07 | $2.00/$8.00 |
| mistralai/mistral-saba | 2025-02-17 | $0.20/$0.60 |
| openai/o3-mini-high | 2025-02-12 | $1.10/$4.40 |
| aion-labs/aion-rp-llama-3.1-8b | 2025-02-04 | $0.80/$1.60 |
| qwen/qwen2.5-vl-72b-instruct | 2025-02-01 | $0.80/$1.00 |
| mistralai/mistral-small-24b-instruct-2501 | 2025-01-30 | $0.05/$0.08 |
| perplexity/sonar | 2025-01-27 | $1.00/$1.00 |
| deepseek/deepseek-r1-distill-llama-70b | 2025-01-23 | $0.80/$0.80 |
| deepseek/deepseek-r1 | 2025-01-20 | $0.70/$2.50 |
| minimax/minimax-01 | 2025-01-15 | $0.20/$1.10 |
| microsoft/phi-4 | 2025-01-10 | $0.07/$0.14 |
| openai/o3-mini | 2024-12-20 | $1.10/$4.40 |
| openai/o3-mini:batch | 2024-12-20 | $1.10/$4.40 |
| sao10k/l3.3-euryale-70b | 2024-12-18 | $0.65/$0.75 |
| meta-llama/llama-3.3-70b-instruct | 2024-12-06 | $0.10/$0.32 |
| amazon/nova-lite-v1 | 2024-12-05 | $0.06/$0.24 |
| amazon/nova-micro-v1 | 2024-12-05 | $0.04/$0.14 |
| amazon/nova-pro-v1 | 2024-12-05 | $0.80/$3.20 |
| openai/o1 | 2024-12-05 | $15.00/$60.00 |
| cohere/command-r7b-12-2024 | 2024-12-02 | $0.04/$0.15 |
| openai/gpt-4o-2024-11-20 | 2024-11-20 | $2.50/$10.00 |
| mistralai/mistral-large-2407 | 2024-11-19 | $2.00/$6.00 |
| qwen/qwen-2.5-coder-32b-instruct | 2024-11-11 | $0.66/$1.00 |
| thedrummer/unslopnemo-12b | 2024-11-08 | $0.40/$0.40 |
| mistralai/mistral-large-2512:batch | 2024-11-01 | $0.50/$1.50 |
| anthracite-org/magnum-v4-72b | 2024-10-22 | $2.50/$5.00 |
| qwen/qwen-2.5-7b-instruct | 2024-10-16 | $0.10/$0.20 |
| meta-llama/llama-3.2-1b-instruct | 2024-09-25 | $0.03/$0.20 |
| meta-llama/llama-3.2-3b-instruct | 2024-09-25 | $0.05/$0.33 |
| qwen/qwen-2.5-72b-instruct | 2024-09-19 | $0.36/$0.40 |
| cohere/command-r-08-2024 | 2024-08-30 | $0.15/$0.60 |
| cohere/command-r-plus-08-2024 | 2024-08-30 | $2.50/$10.00 |
| sao10k/l3.1-euryale-70b | 2024-08-28 | $0.85/$0.85 |
| nousresearch/hermes-3-llama-3.1-70b | 2024-08-18 | $0.70/$0.70 |
| nousresearch/hermes-3-llama-3.1-405b | 2024-08-16 | $1.00/$1.00 |
| sao10k/l3-lunaris-8b | 2024-08-13 | $0.04/$0.05 |
| openai/gpt-4o-2024-08-06 | 2024-08-06 | $2.50/$10.00 |
| meta-llama/llama-3.1-70b-instruct | 2024-07-23 | $0.40/$0.40 |
| meta-llama/llama-3.1-8b-instruct | 2024-07-23 | $0.05/$0.08 |
| openai/gpt-4o-mini | 2024-07-18 | $0.15/$0.60 |
| openai/gpt-4o-mini-2024-07-18 | 2024-07-18 | $0.15/$0.60 |
| openai/gpt-4o-mini:batch | 2024-07-18 | $0.15/$0.60 |
| google/gemma-2-27b-it | 2024-07-13 | $0.65/$0.65 |
| mistralai/mistral-nemo | 2024-07-01 | $0.02/$0.03 |
| openai/gpt-4o | 2024-05-13 | $2.50/$10.00 |
| openai/gpt-4o-2024-05-13 | 2024-05-13 | $5.00/$15.00 |
| openai/gpt-4o:batch | 2024-05-13 | $2.50/$10.00 |
| mistralai/mixtral-8x22b-instruct | 2024-04-17 | $2.00/$6.00 |
| microsoft/wizardlm-2-8x22b | 2024-04-16 | $0.62/$0.62 |
| anthropic/claude-3-haiku | 2024-03-13 | $0.25/$1.25 |
| mistralai/mistral-large | 2024-02-26 | $2.00/$6.00 |
| openai/gpt-3.5-turbo-0613 | 2024-01-25 | $1.00/$2.00 |
| qwen/qwen-plus | 2024-01-25 | $0.26/$0.78 |
| openai/gpt-4 | 2023-11-06 | $30.00/$60.00 |
| openai/gpt-4-turbo | 2023-11-06 | $10.00/$30.00 |
| openai/gpt-4-turbo:batch | 2023-11-06 | $10.00/$30.00 |
| openai/gpt-3.5-turbo-instruct | 2023-09-28 | $1.50/$2.00 |
| openai/gpt-3.5-turbo-16k | 2023-08-28 | $3.00/$4.00 |
| mancer/weaver | 2023-08-02 | $0.40/$0.75 |
| undi95/remm-slerp-l2-13b | 2023-07-22 | $0.45/$0.65 |
| gryphe/mythomax-l2-13b | 2023-07-02 | $0.06/$0.06 |
| openai/gpt-3.5-turbo | 2023-03-01 | $0.50/$1.50 |
| openai/gpt-3.5-turbo:batch | 2023-03-01 | $0.50/$1.50 |
| deepseek/deepseek-v4.1-flash | — | — |
| inference-net/schematron-v2-small | — | — |
| inference-net/schematron-v2-turbo | — | — |
| nex-agi/nex-n2.5-mini:free | — | — |
| nex-agi/nex-n2.5-pro:free | — | — |
| nvidia/nemotron-3.5-content-safety | — | — |
| openai/gpt-6-astra | — | — |
| openai/gpt-6-astra-pro | — | — |
| openai/gpt-6-astra-pro:batch | — | — |
| openai/gpt-6-astra:batch | — | — |
| openrouter/auto | — | — |
| openrouter/auto-beta | — | — |
| openrouter/bodybuilder | — | — |
| openrouter/fusion | — | — |
| openrouter/pareto-code | — | — |
| sakana/fugu-max | — | — |
| unbiased/pareto | — | — |
| ~deepseek/deepseek-flash-latest | — | — |
| ~deepseek/deepseek-pro-latest | — | — |
| ~openai/gpt-astra-latest | — | — |
| ~openai/gpt-luna-latest | — | — |
| ~openai/gpt-sol-latest | — | — |
| ~openai/gpt-terra-latest | — | — |

## Agentgateway — 25 models

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
| `gpt-4` | 8K |
| `gpt-4-32k` | 32K |
| `gpt-4-turbo` | 128K |
| `gpt-4-turbo-128k` | 128K |
| `gpt-4.1` | 1.00M |
| `gpt-4.1-mini` | 1.00M |
| `gpt-4o` | 128K |
| `gpt-4o-autox` | 128K |
| `gpt-4o-mini` | 128K |
| `gpt-5` | 250K |
| `gpt-5-chat` | 128K |
| `gpt-5-mini` | 250K |
| `gpt-5.1` | 272K |
| `gpt-5.1-chat` | 112K |
| `gpt-5.1-codex` | 272K |
| `gpt-5.1-codex-max` | 272K |
| `gpt-5.1-codex-mini` | 272K |
| `gpt-5.2` | 272K |
| `gpt-5.2-chat` | 112K |
| `gpt-5.3-chat` | 272K |
| `gpt-5.3-codex` | 272K |
| `gpt-5.4` | 900K |
| `gpt-5.4-mini` | 272K |
| `gpt-5.5` | 900K |
| `gpt-5.6-luna` | 1.05M |
| `gpt-5.6-sol` | 1.05M |
| `gpt-5.6-terra` | 1.05M |
| `gpt-oss-120b` | 272K |
| `o1` | 200K |
| `o1-mini` | 128K |
| `o3-mini` | 200K |

## Updating This File

```bash
pi model list           # every provider with credentials configured
pi model list azure     # the embedded deployment catalog
```

Regenerate the tables from that output. The CLI sorts each provider's models
by release date (newest first), then by model ID; providers are listed in
`pi`'s own order.
