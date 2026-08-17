# Providers

## User flow

`/connect` adds a chat provider and saves its configuration. `/models` lists
every connected provider so you can switch between them, and selects a model.

## What `/models` shows

- **The active provider**: its live catalog, fetched from the service.
- **Every other connected provider**: the model already saved for it, as one
  row. Selecting that row switches provider; re-open `/models` to see that
  provider's full catalog.

Only the active provider's catalog is fetched. Querying every remote service
would add a network round trip per provider to a keystroke-latency path, and
one unreachable service would stall the whole picker.

If the active provider is unreachable, its catalog is replaced by its saved
model and the other providers are still listed, so a broken connection cannot
trap you on it. The failure is reported on standard error. `/models` errors
only when nothing at all is connected.

A provider counts as connected when it has a config entry, or — for the OAuth
providers — when its token file exists. Connecting once is enough to survive a
config reset.

## Credential storage

API keys are never written to `config.yaml`. `config.yaml` holds the
environment variable name; the value is read at request time from either the
environment or `~/.config/athena/provider-credentials.json`, which stores keys
entered through `/connect` in plaintext at mode `0600`.

Running `/connect` again on a provider that already has a stored key — to
change its model, say — with the key field left blank keeps that stored key.
The adapter is rebuilt from the store, not from the empty field. Replace a key
by typing the new one; the store holds one key per provider.

OAuth providers keep their tokens in `~/.config/athena/openai-codex-auth.json`
(ChatGPT Plus/Pro) and `~/.config/athena/xai-oauth.json` (Grok Pro/SuperGrok).

## Re-login rules

Token files persist and are reused across restarts, so an OAuth provider does
not ask for a device login every time Athena starts. Both adapters refresh an
expired access token themselves on their next request.

Device login runs **only when it is needed**:

- Picking a connected provider in `/models` switches to it immediately. No
  device login. If the provider exists only as a token file, Athena rebuilds it
  from that token.
- `/connect` on an OAuth provider first tries the stored session. If it is
  valid or refreshable, Athena reuses it and reports that it did. Device login
  runs only when that check fails — including when it fails for a transient
  network reason, since Athena cannot tell that apart from a revoked token.

Switching to Ollama does not delete the other providers' credentials or their
config entries.

### The Codex CLI's tokens are not adopted silently

If Athena has no `openai-codex-auth.json` of its own while the OpenAI Codex CLI
is signed in (`~/.codex/auth.json`), Athena *offers* that import rather than
taking it. Those tokens belong to another application, so silence is not
consent. `CodexOAuth.PendingCLIImport` reports a waiting offer, and
`ResolveCLIImport(approve)` records the answer in
`~/.config/athena/openai-codex-import.json`. The answer lives in its own file so
that signing in, refreshing, or signing out — each of which rewrites the token
file — cannot make Athena forget a "no".

No interface asks the question yet, so in practice Athena never uses the Codex
CLI's tokens: an unanswered offer is left unused, and `/connect` on ChatGPT
Plus/Pro runs its own device login. Approving the import once makes Athena adopt
the CLI's credentials on later starts as well.

## Supported provider shapes

| Provider | Adapter | Connection details |
| --- | --- | --- |
| Ollama | built-in local API | host and locally pulled model |
| ChatGPT Plus / Pro | Codex Responses adapter | local OAuth device login and subscription model catalog |
| OpenAI, xAI, OpenRouter | OpenAI-compatible Chat Completions | `/v1` base URL, API-key environment variable, model |
| Grok Pro / SuperGrok | OpenAI-compatible chat with xAI OAuth | local OAuth device login |
| Anthropic | native Messages API | `/v1` base URL, `ANTHROPIC_API_KEY`, model |
| LM Studio, vLLM, llama.cpp, LocalAI | custom OpenAI-compatible API | local `/v1` base URL, optional key, model |

## Embedding providers

Embeddings are configured separately from chat, in the `embedding_provider`
block of `config.yaml`. They default to the local Ollama `embed_model`, and can
instead point at any OpenAI-compatible `/embeddings` endpoint. See
[Configuration](../configuration/README.md#embeddings).

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

## Forcing a tool decision

A turn that changes the vault must end in a tool call, not in prose that
promises one. Small local models often answer "I'll create that for you!"
instead of calling the tool, and the turn produces nothing.

The Codex adapter and the OpenAI-compatible adapter both send
`tool_choice: "required"` on that path. Everything else — reading, answering —
is sent without it.

`tool_choice` is not implemented by every OpenAI-compatible server; Ollama, LM
Studio, llama.cpp and vLLM differ, and a server that does not know the field
rejects the whole request. So the OpenAI-compatible adapter:

1. sends `tool_choice: "required"` on the first mutation-planning request;
2. on a `400` or `422` retries the same request without the field, so an
   unsupported field never costs the turn;
3. remembers that per model, so later mutation turns skip the field instead of
   paying a failed request each time.

Only `400` and `422` count as a rejection — those are how these servers report
a field they do not implement. Auth, rate-limit and `5xx` failures stay fatal.
If the retry also fails, the field was not the problem: the original error is
reported and required mode stays on for that model.

Providers without a native required-tool mode simply do not implement
`RequiredToolProvider`, and `internal/chat` sends an ordinary tool request.

## Adding a provider

1. Decide whether it uses OpenAI-compatible Chat Completions or a distinct
   API. Use the existing `OpenAICompatibleProvider` for the former.
2. For a distinct wire format, create a provider in `internal/ai` that
   implements `ChatProvider`. Keep protocol mapping there; chat policy and
   vault tools must stay in `internal/chat`.
3. Add a `/connect` preset through `internal/chat/providers.go`; both UIs obtain
   their provider choices from the session boundary.
4. Teach the type to `chat.BuildProvider`, the single place a saved
   `config.ProviderConfig` becomes a live adapter. Startup and `/connect` both
   call it, so a type added there is restored on startup and connectable at
   runtime without a second registration. Providers are keyed by
   `chat.ProviderID(name)` everywhere — config, credential store, and picker.
5. Add request/response tests with a mock `http.RoundTripper`, including tool
   calls if the provider supports them.

## Tool queue guarantee

Athena accepts up to 24 read-tool calls in a model turn. It executes them in
application-owned batches of four and attaches every result before asking the
model to continue. This does not guarantee that an external service succeeds,
but it guarantees Athena does not discard accepted calls because a model forgot
them.
