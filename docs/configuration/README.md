# Configuration and startup

## Ownership

- `cmd/athena/main.go` composes dependencies and registers action handlers.
- `internal/config` loads and saves local YAML configuration.

## Startup flow

1. Load `~/.config/athena/config.yaml`, creating defaults if absent.
2. Create the vault and SQLite parent directory.
3. Open SQLite and apply safe schema migrations.
4. Ensure Ollama is running for local embeddings and vault search.
5. Construct storage, retrieval, notes, AI, dispatcher, and chat services.

## Configuration fields

`vault_path`, `db_path`, `ollama_host`, `chat_model`, and `embed_model` are
stored in YAML. `providers` and `active_provider` optionally configure chat
providers using OpenAI's compatible `/v1` API shape. The default vault and
database live under the user's home directory, not inside this repository.
Fresh installations use `~/Athena` for the vault and
`~/.local/share/athena/athena.db` for SQLite.

Athena previously stored its configuration and credentials in
`~/.config/second-brain`. On first use of each known file, Athena copies it to
`~/.config/athena` and uses the new location from then on. The old copy remains
as a recovery fallback. Values inside an existing config—including a legacy
vault or database path—are preserved, so Athena never relocates user notes or
database data implicitly.

## Local files

- `~/.config/athena/config.yaml`: normal configuration
- `~/.config/athena/provider-credentials.json`: API keys entered through the UI
- `~/.config/athena/openai-codex-auth.json`: OpenAI subscription OAuth tokens
- `~/.config/athena/xai-oauth.json`: xAI OAuth tokens
- `~/.local/share/athena/athena.db`: fresh-install SQLite database, including
  note indexes and the action audit log
- `~/Athena`: fresh-install note vault

Conversation history, pending plans, and pending clarification tasks are held
in memory and disappear when the session exits or is cleared; they do not have
a local task-state file. See [Chat workflow](../chat/README.md) for the task
lifecycle and privacy rationale.

Use `/models` to navigate models with the arrow keys. Its final option opens
`/connect`, which can add OpenAI, Anthropic, xAI/Grok, OpenRouter, Ollama, or a
custom OpenAI-compatible service. The
connection stores a provider name, base URL, model, and an API-key *environment
variable name* (for example `OPENAI_API_KEY`)—never the key itself. Export the
key before starting Athena:

```bash
export OPENAI_API_KEY='...'
athena
```

Chat provider selection is independent from embeddings for now. Athena still
uses the configured local Ollama embedding model to search and index the vault;
switching that provider safely requires rebuilding stored vectors.

Athena keeps the native Anthropic Messages API adapter separate from the
OpenAI-compatible adapter. This prevents provider-specific tool-call formats
from leaking into the rest of the application.

See [the provider extension guide](../providers/README.md) for provider setup
and tool queue limits.

## Change guidance

- Add an action handler in `buildDispatcher` whenever an action is advertised
  in `internal/ai/prompt.go`.
- Keep startup wiring in `main`; do not put note business rules there.
- Do not log future credentials or secrets.
