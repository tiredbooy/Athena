# Athena

Athena is a local vault assistant. It reads and writes Markdown notes (Obsidian-compatible), searches them, and talks to a chat model. The Go process is the engine: chat, tools, vault, providers, and memory policy. The product UI is a TypeScript/Ink TUI that only renders engine events and sends requests.

Default data locations:

- vault: `~/Athena`
- database: `~/.local/share/athena/athena.db`
- config: `~/.config/athena/config.yaml`

User notes and SQLite do not live inside this repository.

## Install

Needs Go, Node.js, and npm.

```sh
make install          # ~/.local/bin/athena + Ink TUI
# or from the repo without installing:
make run              # build and run
```

`make install` puts the binary on `PATH` if `~/.local/bin` is on your `PATH`. Override the prefix with `PREFIX=…`.

## Which UI starts

| Command | Result |
| --- | --- |
| `athena` | Ink TUI if `apps/tui` has been built; otherwise a warning and the Bubble Tea fallback |
| `athena --tui` | Ink TUI, or exit with an error |
| `athena --legacy-tui` | Frozen Bubble Tea fallback only |
| `athena engine` | No UI. JSON-lines protocol on stdin/stdout |

“Built” means `cd apps/tui && npm install && npm run build` (or `make tui`).

```sh
make run              # Ink if built, else Bubble Tea
make run-tui          # Ink or fail
make run-legacy       # Bubble Tea
make run-engine       # stdio engine only
```

The Ink TUI is the product. Do not add features to `internal/tui`.

## Docs and backlog

- [Documentation](docs/README.md) — current behavior, by domain
- [TUI](docs/tui/README.md) — Ink client, commands, launch flags
- [tasks.md](tasks.md) — planned work (Claude = engine, Grok = Ink TUI)

`docs/` describes what exists now. `tasks.md` and `docs/plans/` describe work that does not exist yet.
