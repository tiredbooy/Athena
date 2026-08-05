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
