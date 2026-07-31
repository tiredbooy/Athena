# AI planning and action execution

## Ownership

- `internal/ai/client.go` streams Ollama chat and embedding requests.
- `internal/ai/prompt.go` defines the model contract.
- `internal/ai/tools.go` extracts action JSON from model output.
- `internal/tools` executes action plans.

## One model, many operations

Athena makes one planning chat request per user turn. The resulting actions may
then perform many filesystem and database operations. Chat and embedding calls
share one inference lock, so a batch never starts competing Ollama jobs on a
small GPU.

## Accepted action plan formats

The extractor accepts one action per fenced `action` block, an array of
actions, or an `{ "actions": [...] }` envelope. This tolerance is intentional
for weaker local models.

## Batch rules

An action with `id` can declare `depends_on`. When every action has a unique
ID, independent ready actions execute with up to four workers. A failed action
does not stop unrelated actions; its dependents are skipped with an explicit
error. Results preserve input order for stable UI rendering.

Actions without valid unique IDs run sequentially. `ActionResult` includes
`action`, `message`, and a JSON-safe `error` string.

## Adding an action

1. Add fields and documentation to `ai.Action`.
2. Advertise precise JSON in the system prompt.
3. Register one handler in `cmd/athena/main.go`.
4. Put business behavior in the appropriate domain, usually `notes`.
5. Test extraction and execution separately.
