# Athena engine protocol

`athena engine` reads and writes one JSON object per line. It is a local child
process protocol: standard output is reserved for JSON events and diagnostics
are written to standard error.

The initial v1 slice supports:

- `engine.hello`
- `session.submit`
- `session.cancel`
- `session.reset`
- `plan.approve`
- `plan.reject`
- `model.list`
- `model.select`
- `provider.list`
- `provider.connect`
- `provider.oauth.start`

Every request needs `version: 1`, a client-generated `requestId`, and a
`type`. A submitted turn receives a `turn.started` event, zero or more
`activity` events, then either a `response` with the complete reply or a
`plan.ready`, and finally `turn.completed`. A cancelled turn ends in
`turn.cancelled` instead; a failed one emits `turn.error` carrying the detail
and then `turn.failed`, so a client that stops on `turn.failed` has already
seen the reason. Write plans arrive as `plan.ready` with an engine-generated
`planId`; that ID is single-use and is invalid after rejection, approval, or a
session reset.

## The schema is the contract

`athena.v1.schema.json` lists every request type, every event type, and every
field on the wire. Both objects set `additionalProperties: false`, so an extra
field is a schema change, not a quiet addition.

`internal/transport/stdio/schema_test.go` enforces it. The tests reflect over
the Go wire structs and compare their JSON tags to the schema's properties, and
compare the request/event type enums to the names the server actually uses.
Adding a Go field, renaming a tag, or emitting a new event type without
updating the schema fails the build — in both directions, so a schema entry the
server never sends fails too.

Change the schema in the same commit as the Go change.

## Actions carry their own display text

Every action in a `plan.ready` event has a `summary` written by the engine, for
example `Creating note "Quarterly plan"`. Render it. A client should not
reimplement a switch over action types to describe a plan — that switch drifts
the moment the engine gains an action.

The other action fields stay available for clients that want structure (a
`color` for a graph orb, `folder`, `note_id`), but `summary` is the label.

`plan.approved` repeats no actions — it carries the ledger of what ran. A
client describing an applied plan reuses the actions it was already shown.

## An interrupted approval re-stages its remaining work

`plan.approve` normally settles on `plan.approved`. If applying the plan is
cancelled or fails part-way, it settles on `error` — but that is **not** the end
of the exchange. The engine keeps the user's approval alive: the actions its
execution ledger never recorded as succeeded come back as a **new** single-use
plan, and the server emits a `plan.ready` for it immediately after the `error`,
carrying the ledger of what the interrupted attempt did verify.

A client must therefore keep listening after an `error` on `plan.approve`.
Treating the error as terminal would leave that plan reachable in the engine and
invisible in the UI, so the user could neither resume nor discard work they had
already approved.

The re-staged plan has a different `planId`. Plan IDs stay single-use, so the
original ID is dead and a second `/confirm` can never repeat a verified change.
If the ledger already verified every approved action there is nothing to
re-stage, and the `error` is the whole of it.

A successful approval can also be followed by a `plan.ready`: the run resumes
after the approved actions, and any further high-risk step it decides on is
staged as its own plan rather than hidden inside the approval. Either way, a
client keeps listening past the event that settled `plan.approve`.

## Execution ledger

A turn that changed the vault reports what actually happened in `ledger`, on
the terminal event (`turn.completed`, and `plan.approved` after an approval).
Each record is `{action, target, status, message?, error?}` with `status` of
`succeeded` or `failed`.

This is the verified record from the tool layer, not the model's account of
itself. It is present even when the model's reply is terse, wrong, or empty, so
a client can always show what the vault did. A read-only turn has no `ledger`.

## Activity events

An `activity` event is one factual step. It carries typed fields so a client
renders work instead of guessing at it:

| Field | Meaning |
| --- | --- |
| `phase` | The kind of work: `retrieving`, `planning`, `reading`, `searching`, `embedding`, `provider_wait`, `validating`, `approval`, `executing`, `observing`, `verifying`, `replanning`, `completed`, and the legacy `working`. The JSON schema's enum is authoritative; this list mirrors it. |
| `state` | `started`, `succeeded`, or `failed`. |
| `tool` | The read tool or action type that ran, when the step ran one. |
| `target` | What it acted on: a vault path, a note reference, or a query. |
| `run_id`, `step` | Which agent run and step the event belongs to. |
| `path`, `provider`, `model` | Extra detail when the step has it: the vault path a read resolved, and the provider and chat model a `provider_wait` step is waiting on. |
| `message` | A human-readable fallback line. |

**Do not parse `message`.** It exists for plain-text fallbacks only; its wording
is not a contract. Every decision a client makes should come from `phase`,
`state`, `tool`, and `target`.

A step that starts real work emits `started` and later `succeeded` or `failed`
with the same `phase`/`tool`/`target`, so a client can pair them — that holds
for `retrieving`, read tools under `reading`, `validating`, and `executing`.
Three phases are one-sided today: `approval` and `verifying` emit only
`started` (the run hands control back to the user, or moves straight on to the
next planning step), and `observing` emits only `succeeded`. Purely
informational lines (retrieval sub-progress, for example) carry a `phase` and a
`message` but no `state`; they are detail within a step, not a step.

Failures are reported honestly: a batch whose actions all failed emits
`executing` with `state: "failed"`, never `succeeded`.

## Requests refused during a turn

While a turn is running, the engine refuses `model.select`, `provider.connect`,
`provider.oauth.start`, and `session.reset` with an `error` event naming the
reason. Each would change state the running turn is already using. Cancel the
turn first. `plan.approve` and `plan.reject` are refused the same way: the plan
belongs to the run that is still going.

`model.list` is read-only and is answered immediately, even mid-turn.

## `session.reset`

Clears engine-side conversation state: history back to the system prompt, any
pending plan, and any pending clarification question. It replies with a
`session.reset` event.

A client clearing its own transcript is **not** a reset. Doing only that leaves
the engine remembering a conversation the user believes is gone, so a view-only
clear must be labeled as such. Reset is refused while a turn is running —
cancel it first — because it would otherwise truncate history the turn is still
appending to.

Example:

```json
{"version":1,"requestId":"r1","type":"engine.hello"}
{"version":1,"requestId":"r1","type":"engine.ready","message":"Athena engine is ready"}
{"version":1,"requestId":"r2","type":"session.submit","turnId":"t1","input":"create a work folder"}
```

See `athena.v1.schema.json` for the language-neutral envelope. Model discovery
and selection stay in the Go application layer; the Ink client only renders
the returned options and sends the selected provider/model identity back.
