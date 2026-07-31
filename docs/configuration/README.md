# Configuration and startup

## Ownership

- `cmd/athena/main.go` composes dependencies and registers action handlers.
- `internal/config` loads and saves local YAML configuration.

## Startup flow

1. Load `~/.config/second-brain/config.yaml`, creating defaults if absent.
2. Create the vault and SQLite parent directory.
3. Open SQLite and apply safe schema migrations.
4. Ensure Ollama is running.
5. Construct storage, retrieval, notes, AI, dispatcher, and chat services.

## Configuration fields

`vault_path`, `db_path`, `ollama_host`, `chat_model`, and `embed_model` are
stored in YAML. The default vault and database live under the user's home
directory, not inside this repository.

## Change guidance

- Add an action handler in `buildDispatcher` whenever an action is advertised
  in `internal/ai/prompt.go`.
- Keep startup wiring in `main`; do not put note business rules there.
- Do not log future credentials or secrets.
