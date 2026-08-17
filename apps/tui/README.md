# Athena TUI

This package is the **primary** TypeScript/Ink client for `athena engine`. It
never touches the vault or the database directly; everything goes over the
stdio protocol.

`src/index.tsx` mounts the app. `src/App.tsx` talks to the engine and wires
keyboard input. Transcript, composer, command palette, plan card, connect
wizard, and model picker are separate modules under `src/ui/`.

## Build and run

Install dependencies and run the type check from this directory:

```sh
npm install
npm run check
```

To run the preview after building, make the Go binary available as `athena`
or point `ATHENA_ENGINE` at its path:

```sh
ATHENA_ENGINE=/path/to/athena npm run build
ATHENA_ENGINE=/path/to/athena npm start
```

The Go process is always started with the `engine` argument.

From the repository root, `go run ./cmd/athena` starts this app whenever it has
been built. If it has not been built, the launcher prints a warning and falls
back to the legacy Bubble Tea TUI. Use `--tui` to require the Ink app instead
of falling back, or `--legacy-tui` to force Bubble Tea:

```sh
go run ./cmd/athena              # Ink if built, else Bubble Tea
go run ./cmd/athena --tui        # Ink or fail
go run ./cmd/athena --legacy-tui # Bubble Tea
```

## Commands

`/help`, `/clear`, `/reset`, `/doctor`, `/models`, `/connect`, `/theme`,
`/compact`, `/cancel`, `/vim-mode`.

`/vim-mode` is opt-in (saved in `ui.json`). Default is ordinary arrows and
a focused composer. With it on, Esc enters normal mode: `j`/`k` move by
user turn, `g`/`G` jump top/bottom, `i` returns to the composer.

`/help` is an overlay: shortcuts and every command, Esc to close. It does
not add a transcript message.

An empty pane is never silent. Connecting, a ready empty session, no
connected model, and a dead engine each have copy and a next action.
`/help`, `/clear`, and `/theme` still work when the engine is down.

Typing `/` opens the command palette. Arrow keys move, Tab completes the
highlighted command, Enter runs it, and Esc closes the palette.

`/models` opens the engine-backed model picker, grouped by connected
provider, with a final **Connect a new provider** row. Enter on a model
switches; that last row opens `/connect`. `/connect` asks only for the
fields the preset still needs. The first remote connect warns that
inventory, search, and `get_note` leave this machine.

`/clear` is view-only — it empties the visible transcript and nothing else.
`/reset` sends `session.reset` so the engine also forgets history, a pending
plan, and a pending clarification. Cancel an in-flight turn before resetting.

`Esc` cancels the active turn. `/cancel` discards a pending plan or
clarification question; it does not cancel the in-flight turn.

A plan card is a review mode: `Y` or Enter applies, `N` or `R` discards, Esc
returns to the composer without deciding. Tab focuses the card again. Typing
`yes` does not approve.

Search, read, and write activity appear as foldable work blocks in the
transcript. Click a block or press Ctrl+O to expand or fold the last one.
Approved graph / orb changes leave `Graph · folder · size` lines in the
scrollback. Hex color is shown only if the engine sends a `color` field.

Engine stderr warnings show as `engine` lines in the transcript. They stay
off the JSON stdout stream.

`/theme` previews `midnight`, `ocean`, and `system`. Arrows change the whole
chrome, Enter saves, Esc reverts. The last theme, provider, and model are
restored from `~/.config/athena/ui.json` (or
`$XDG_CONFIG_HOME/athena/ui.json`). After hello, if the saved model is not
what the engine reported, Ink sends `model.select` and reuses any saved
OAuth session. On a 16-color terminal, hex themes fall back to ANSI names
and skip a painted background so midnight stays readable.

## Scrolling and copy

In a real terminal, use Page Up/Page Down or the mouse wheel to scroll the
transcript. Ctrl+↑ / Ctrl+↓ jump to the previous or next user turn and pin
that prompt at the top. Click a work block or press Ctrl+O to fold or
expand the activity on the visible turn. Drag across visible transcript
text—including error messages—to copy it. Scrolling and selection share the
same source-row mapping, so copied text keeps the correct message offsets.
Assistant replies style markdown headings, lists, and fenced code on those
source rows; copied text still includes the original punctuation. Athena uses
SGR mouse tracking and OSC 52, then clears the selection after the copy.
Terminals without OSC 52 support may ignore the clipboard request.

## Tests

`npm test` compiles, then runs Node's test runner on `dist/`. EngineClient
and the session reducer replay recorded NDJSON streams from `testdata/`
(hello, a completed turn, a plan, a failed turn, plan approval, OAuth,
`response.delta`, session reset, and invalid lines).

The reducer will append `response.delta` tokens without folding work
blocks. The engine does not emit those events yet.

If the engine process exits, the TUI reconnects and hellos again. The
pane keeps the old transcript as view-only text and says the new
engine session does not have the pending plan or turn.
