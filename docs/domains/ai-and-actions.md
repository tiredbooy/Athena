# AI and actions domain

## Ownership

- `internal/ai/client.go` talks to the local Ollama HTTP API.
- `internal/ai/prompt.go` defines model behavior and the advertised action
  schema.
- `internal/ai/tools.go` extracts fenced JSON actions from model output.
- `internal/tools` dispatches actions to registered application handlers.

## Chat protocol

Athena uses streamed Ollama chat responses. The current local-model protocol
asks the model to append a fenced `action` block containing one JSON action.
The extractor tolerates closed and unclosed fences and braces within JSON
strings. It removes valid action blocks from user-visible text before dispatch.

This is not native model tool calling. Small local models may omit or misspell
an action. The chat domain therefore handles a small set of exact, safe folder
commands directly.

## Action outcomes

Dispatch always returns a result for every recognized action. The chat UI
prints those results, including errors, because a model's confirmation is only
an intention—not proof that a filesystem or database change succeeded.

## Adding an action

1. Add fields/documentation to `ai.Action`.
2. Advertise precise JSON in the system prompt.
3. Register one handler in `cmd/athena/main.go`.
4. Implement business behavior in the correct domain, usually `notes`.
5. Test extraction and the service behavior separately.
