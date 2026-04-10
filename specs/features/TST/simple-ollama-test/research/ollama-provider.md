# Ollama Provider Integration

## How pi-go connects to Ollama

- Models ending with `:cloud` suffix trigger Ollama routing
- Uses Anthropic-compatible API at `http://localhost:11434` by default
- Base URL can be overridden via `--url` flag or `ANTHROPIC_BASE_URL` env var
- API key is optional when baseURL is set (Ollama compatibility)

## Model: minimax-m2.5:cloud

- The `:cloud` suffix causes pi-go to route through Ollama
- Model name `minimax-m2.5` must be available in the local Ollama instance
- Ollama exposes an Anthropic-compatible API endpoint

## Known Issues

1. **Schema validation**: LLMs may send extra properties in tool calls — pi-go has lenient schema validation to handle this
2. **Type coercion**: LLMs may send strings instead of integers — pi-go coerces types automatically
3. **Tool parameter format**: Tree tool `depth` parameter may be sent as string by some models — handled by registry coercion
