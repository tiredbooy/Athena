# Chat and terminal UI domain

## Ownership

`internal/chat` owns one complete turn: classify safe shortcuts, retrieve
context, call the model, extract actions, dispatch them, and update history.
`internal/tui` owns terminal-only spinner and markdown rendering.

## Turn flow

```text
user input
  → exact safe shortcut (folder commands / generic listing when applicable)
  → build vault catalog and semantic context
  → stream model reply
  → extract and run actions
  → print answer plus actual action results
  → save concise outcome in chat history
```

Qualified note questions must not use the generic listing shortcut. For
example, “what notes do I have for books I haven't finished?” is sent through
retrieval with the full catalog, allowing the model to filter it. Only an
unqualified “what notes do I have?” receives the instant complete listing.

## Current status display

The loader shows the latest real activity supplied by retrieval, including a
vault-relative filename for selected notes. It then changes to “Thinking about
your question” while waiting for the model. If a model reply has no displayable
text, the UI prints an explicit fallback instead of ending silently.

## Existing shortcuts

Exact single-purpose folder requests such as `create folder projects/athena`
and `delete folder archive` bypass model action JSON. Compound requests still
go to the model because they require context and may involve several actions.

## UI boundary

The current chat loop prints with `fmt`, so it is a CLI adapter as well as
orchestration. Before adding another frontend, extract the turn orchestration
into an application service that emits structured events; see planned work.
