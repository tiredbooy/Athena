# Athena engine protocol

`athena engine` reads and writes one JSON object per line. It is a local child
process protocol: standard output is reserved for JSON events and diagnostics
are written to standard error.

The initial v1 slice supports:

- `engine.hello`
- `session.submit`
- `session.cancel`
- `plan.approve`
- `plan.reject`

Every request needs `version: 1`, a client-generated `requestId`, and a
`type`. A submitted turn receives a `turn.started` event, zero or more
`activity` events, then either `turn.completed`, `turn.cancelled`, or
`turn.failed`. Write plans arrive as `plan.ready` with an engine-generated
`planId`; that ID is single-use and is invalid after rejection, approval, or a
session reset.

Example:

```json
{"version":1,"requestId":"r1","type":"engine.hello"}
{"version":1,"requestId":"r1","type":"engine.ready","message":"Athena engine is ready"}
{"version":1,"requestId":"r2","type":"session.submit","turnId":"t1","input":"create a work folder"}
```

See `athena.v1.schema.json` for the language-neutral envelope. The next
migration slice adds models, provider connection, and doctor requests once the
Ink client has a working conversation, cancellation, and approval flow.
