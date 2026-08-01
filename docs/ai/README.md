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

## Read-only tool loop

Before producing its final answer, a tool-capable model may make up to four
rounds of native read-only tool calls. Each round allows at most four calls;
each call has a ten-second deadline and returns bounded JSON to the model.
The available tools are `search_notes`, `get_note`, `list_notes`,
`list_folders`, and `find_notes_by_title`.

Tool execution is owned by `internal/chat`, while `internal/retrieval` owns
the vault reads. The model never receives direct storage or filesystem access.
Write actions remain on the existing validated dispatcher path.

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

## Reliability boundary

Before dispatch, every model-supplied action is checked for a known type and
its required fields. A turn has a two-minute deadline; tools have their own
ten-second read or one-minute write deadline. Read-only inspections may retry
once, while writes are never retried automatically.

Each attempt is written to SQLite's local `action_audit` table with the JSON
arguments, outcome, and error. Successful note and simple folder writes are
re-read and verified before Athena reports success. Batches remain partially
successful by design, so each action has an independent audit outcome.

## Adding an action

1. Add fields and documentation to `ai.Action`.
2. Advertise precise JSON in the system prompt.
3. Register one handler in `cmd/athena/main.go`.
4. Put business behavior in the appropriate domain, usually `notes`.
5. Test extraction and execution separately.
