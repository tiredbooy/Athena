# Terminal user interface

## Ownership

`internal/tui` renders Athena's local Bubble Tea interface. It does not own
notes, model calls, or action execution; it sends input to `chat.Session` and
renders status, streamed text, and final action outcomes.

## Current workspace

- Full-screen Bubble Tea application with Bubbles textarea and viewport.
- Compact one-line composer by default, styled as a command bar rather than an
  editor pane.
- Streaming response text, spinner, and factual engine status below the input.
- Conversation entries use role labels and thin themed rails, making streamed
  responses, user prompts, and failures easy to scan without bulky cards.
- `Esc` cancels an active request.
- `/clear` clears only the visible pane; `/reset` also clears session history.
- Selectable command palette: arrows choose, Tab completes, Escape dismisses.
- `/models` and `/model` query/select local Ollama chat models.
- `/theme` opens a second selectable choice for `midnight`, `ocean`, and
  `system`.

## Themes

- `midnight` is the default purple dark presentation.
- `ocean` changes accent, message, input, and border colors to cyan/blue.
- `system` uses ANSI color slots for hierarchy (cyan accent, blue borders,
  muted text, and red errors). Those slots come from the terminal palette, so
  the appearance follows the user's terminal theme without becoming flat.

Theme styling must be applied to the composer, textarea focus/cursor, spinner,
messages, borders, header, and muted text together. Do not add a fixed color
in `View`, because it would override user theme selection.
