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
| ChatGPT Plus / Pro | Codex Responses adapter | local OAuth device login and subscription model catalog |
| OpenAI, xAI, OpenRouter | OpenAI-compatible Chat Completions | `/v1` base URL, API-key environment variable, model |
| Grok Pro / SuperGrok | OpenAI-compatible chat with xAI OAuth | local OAuth device login |
| Anthropic | native Messages API | `/v1` base URL, `ANTHROPIC_API_KEY`, model |
| LM Studio, vLLM, llama.cpp, LocalAI | custom OpenAI-compatible API | local `/v1` base URL, optional key, model |

## Ollama context windows

Ollama can start a model with a smaller runtime context than the maximum stored
in the model metadata. Athena initially respects that low-memory default. If
Ollama rejects a chat request because the input is larger, Athena retries once
with the smallest power-of-two `num_ctx` that fits the input plus a response
reserve, and remembers that size for later requests to the same model.

Automatic growth is capped at 16,384 tokens. This lets ordinary 4K local
models stay inexpensive while preventing a large prompt from silently
reserving unbounded RAM or VRAM. A request that still cannot fit returns an
explicit error instead of repeatedly increasing memory use.

## Adding a provider

1. Decide whether it uses OpenAI-compatible Chat Completions or a distinct
   API. Use the existing `OpenAICompatibleProvider` for the former.
2. For a distinct wire format, create a provider in `internal/ai` that
   implements `ChatProvider`. Keep protocol mapping there; chat policy and
   vault tools must stay in `internal/chat`.
3. Add a `/connect` preset through `internal/chat/providers.go`; both UIs obtain
   their provider choices from the session boundary.
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
