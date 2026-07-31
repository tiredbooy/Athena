# Chat workflow

## Ownership

`internal/chat` owns one complete turn: safe shortcuts, retrieval, model call,
action execution, history, and user-facing errors. The TUI only renders events
and sends user input.

## Turn flow

```text
user input
  → exact safe shortcut when applicable
  → vault catalog + semantic context
  → one streamed model plan/reply
  → extract and execute actions
  → answer + per-action outcome
  → save concise history
```

An empty visible reply is explicit: the UI says that the model returned no
visible answer and that no vault changes were made. Action outcomes are always
shown, because a model confirmation does not prove execution succeeded.

## Current shortcuts

Exact single-purpose folder creation/deletion can bypass model JSON. Compound
requests stay agentic and go through the planning model.

## Refactoring rule for the new TUI

The current loop still prints with `fmt`. Before Bubble Tea becomes the main
UI, separate turn orchestration from printing so it emits typed events:
`status`, `answer`, `action_result`, `error`, and `complete`.
