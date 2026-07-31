# Terminal user interface

## Ownership

`internal/tui` renders Athena's local Bubble Tea interface. It does not own
notes, model calls, or action execution; it sends input to `chat.Session` and
renders status, streamed text, and final action outcomes.

## Current workspace

- Full-screen Bubble Tea application with Bubbles textarea and viewport.
- Streaming response text, spinner, and factual engine status below the input.
- `Esc` cancels an active request.
- `/clear` clears only the visible pane; `/reset` also clears session history.
- Selectable command palette: arrows choose, Tab completes, Escape dismisses.
- `/models` and `/model` query/select local Ollama chat models.
- `/theme` opens a second selectable choice for `midnight`, `ocean`, and
  `system`.

## Themes

- `midnight` is the default purple dark presentation.
- `ocean` changes accent, message, input, and border colors to cyan/blue.
- `system` removes Athena color overrides so the terminal's own colors drive
  the interface.

Theme styling must be applied to the composer, textarea focus/cursor, spinner,
messages, borders, header, and muted text together. Do not add a fixed color
in `View`, because it would override user theme selection.
