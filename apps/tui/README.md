# Athena TUI

This package is the TypeScript/Ink client for `athena engine`.

The first migration slice contains only the typed protocol boundary and
`EngineClient`. It deliberately does not access the vault or database. UI
components will be added after the engine protocol is exercised end to end.

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

The Go process is always started with the `engine` argument. The legacy Go
TUI remains the default `athena` experience until feature parity is reached.

In a real terminal, drag across transcript text to copy it. Athena uses SGR
mouse tracking and OSC 52, then clears the selection after the copy. Terminals
without OSC 52 support may ignore the clipboard request.

From the repository root, the automatic launcher is:

```sh
go run ./cmd/athena
```

Use `go run ./cmd/athena --legacy-tui` to force Bubble Tea, or
`go run ./cmd/athena --tui` to require the built Ink app instead of falling
back when it is unavailable.
