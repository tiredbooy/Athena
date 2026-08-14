# AI planning and action execution

## Ownership

- `internal/agent` owns the bounded decide, validate, act, observe, and verify
  lifecycle.
- `internal/ai` owns provider adapters, prompts, action decoding, and embedding
  clients.
- `internal/chat` supplies conversation state, read tools, task-specific action
  contracts, and model decisions to the agent runner.
- `internal/ai/prompt.go` defines the model contract.
- `internal/ai/tools.go` extracts action JSON from model output.
- `internal/tools` executes action plans.

## Bounded agent lifecycle

One user turn can contain several bounded model decisions. Athena may read vault
facts, validate a proposal, return one correction, execute approved work, and
ask the model to evaluate verified results. The default run budget is six
decisions, four action batches, 24 actions, two consecutive validation failures,
and one execution attempt per exact write target.

The application owns continuation and stop conditions. The model never executes
vault operations itself and cannot declare an unverified write successful.

## Read-only tool loop

Before producing its final answer, a tool-capable model may make up to four
rounds of native read-only tool calls. Each round allows at most four calls;
each call has a ten-second deadline and returns bounded JSON to the model.
The read tools cover semantic search, note reads by ID or path, folder and tag
inventory, note links, duplicate titles, daily notes, and exact book-catalog
lookup. `lookup_book` returns exact metadata separately from a possible title
suggestion; a suggestion must be confirmed by the user.

Tool execution is owned by `internal/chat`, while `internal/retrieval` owns
the vault reads. The model never receives direct storage or filesystem access.
Write actions remain on the existing validated dispatcher path.

## Typed and narrowed proposals

`propose_actions` uses action-specific JSON Schema variants. For example,
`create_folder` requires `folder`, while `ensure_folders` requires `paths`.
Athena narrows the advertised variants to the action families relevant to the
active goal. Unknown goals retain the full safe mutation contract.

The same reduced action list and required-field summary is injected into the
active turn as application-owned context. Ollama models that cannot use native
tools therefore receive the narrowed contract in the fenced-JSON fallback too.

This is a reliability boundary for both remote and small local models: invalid
field combinations are discouraged by the provider schema and still rejected
by `internal/tools` before execution.

## Safer writes and review

`append_note` adds a paragraph without replacing prior content.
`replace_section` changes one Markdown section only when its
`expected_content` matches the current section body; this prevents a stale
model plan from overwriting a later user edit. Full `update_note` remains
available only for an explicit whole-note replacement.

Bulk plans and destructive/broad changes are previewed instead of executed.
The user must enter `/confirm` to apply the pending plan or `/cancel` to
discard it. No action is dispatched before confirmation.

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

Executed attempts are written to SQLite's local `action_audit` table with the
JSON arguments, outcome, and error. Preflight-rejected proposals are recorded
with outcome `rejected`, so repeated malformed plans can be diagnosed without
guessing their original fields. Successful writes are re-read and verified
before Athena reports success. Batches remain partially successful by design,
so each action has an independent audit outcome.

## Action policy

`internal/tools` is the single source of action policy. Each built-in action
declares its kind (`read`, `write`, or `destructive`), timeout, retry safety,
review requirement, and whether it may run concurrently. Only read-only
actions retry automatically or run in parallel. A multi-action batch containing
a write requires review. Folder creation, graph relationships, destructive
actions, and whole-note replacement require review even when proposed alone.

## Adding an action

1. Add fields and documentation to `ai.Action`.
2. Add its action-specific proposal schema and task-family routing.
3. Register one handler in `cmd/athena/main.go`.
4. Put business behavior in the appropriate domain, usually `notes`.
5. Test schema, validation, execution, verification, and the relevant
   conversational handoff.
6. Update this document and any affected domain document.
