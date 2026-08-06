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
