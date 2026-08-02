# Providers

## User flow

`/connect` adds a chat provider and saves its non-secret configuration.
`/models` retrieves models for the active provider and selects one. If the
picker reports no models, the connection details or credentials are likely
wrong; reconnect with `/connect`.

API keys are never written to `config.yaml`. Athena stores the environment
variable name and reads its value when making a request.

## Supported provider shapes

| Provider | Adapter | Connection details |
| --- | --- | --- |
| Ollama | built-in local API | host and locally pulled model |
| OpenAI, xAI, OpenRouter | OpenAI-compatible Chat Completions | `/v1` base URL, API-key environment variable, model |
| Anthropic | native Messages API | `/v1` base URL, `ANTHROPIC_API_KEY`, model |
| LM Studio, vLLM, llama.cpp, LocalAI | custom OpenAI-compatible API | local `/v1` base URL, optional key, model |

## Adding a provider

1. Decide whether it uses OpenAI-compatible Chat Completions or a distinct
   API. Use the existing `OpenAICompatibleProvider` for the former.
2. For a distinct wire format, create a provider in `internal/ai` that
   implements `ChatProvider`. Keep protocol mapping there; chat policy and
   vault tools must stay in `internal/chat`.
3. Add a `/connect` preset in `internal/tui/bubble.go` with a display name,
   base URL, credential environment variable, and conservative default model.
4. Register the provider type in `cmd/athena/main.go` and
   `internal/chat/providers.go` so saved configuration restores it on startup.
5. Add request/response tests with a mock `http.RoundTripper`, including tool
   calls if the provider supports them.

## Tool queue guarantee

Athena accepts up to 24 read-tool calls in a model turn. It executes them in
application-owned batches of four and attaches every result before asking the
model to continue. This does not guarantee that an external service succeeds,
but it guarantees Athena does not discard accepted calls because a model forgot
them.
