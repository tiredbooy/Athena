# Configuration and startup

## Ownership

- `cmd/athena/main.go` composes dependencies and registers action handlers. It
  wires post-write verification in but does not define it: the invariants live
  with the domain in `internal/notes/verify.go` (A-01).
- `internal/config` loads and saves local YAML configuration.

## Startup flow

1. Load `$XDG_CONFIG_HOME/athena/config.yaml` (default
   `~/.config/athena/config.yaml`), creating defaults if absent and failing if
   an existing file has no `vault_path` or `db_path`.
2. Create the vault and SQLite parent directory.
3. Open SQLite and apply safe schema migrations.
4. Ensure Ollama is running, but only when it is actually needed — that is,
   when embeddings are local, or when the active chat provider is Ollama.
5. Construct storage, retrieval, notes, AI, dispatcher, and chat services.
6. Rebuild the Obsidian folder graph, then reconcile the vault against SQLite.

## Startup vault reconcile

The vault is a real folder the user also edits in Obsidian, so the filesystem
and SQLite drift apart between runs. After `SyncFolderGraph`, `main` calls
`notes.Service.ReconcileVault`, which indexes untracked `.md` files, repoints
rows whose file moved, and **reports** — never deletes — the rest. See
[the notes guide](../notes/README.md) for what it will and will not settle on
its own.

Both halves are warnings on stderr, never fatal: a vault Athena cannot
reconcile must not stop it from starting, because the user needs it running to
fix the vault. A scan that fails part-way still reports what it found, so the
failure and the findings are both visible.

The report names every flagged file with its reason, and gives indexed and
repaired files as counts — those need no decision. At most ten names per group
are printed, so a vault-wide move cannot bury the rest of startup.

The process context carries no deadline, so a session can stay open for hours.
`SIGINT` and `SIGTERM` cancel it, which stops in-flight turns instead of leaving
work running behind a dead terminal. Only a single turn is bounded, by
`chat.TurnTimeout` (five minutes).

In engine mode this matters more than it looks: `stdio.Serve` blocks reading
standard input, so a cancelled context alone cannot end it. Athena closes stdin
once the context is done, which makes the reader return and lets Serve drain.
A read error after that cancellation is the shutdown, not a failure.

## Configuration fields

`vault_path`, `db_path`, `ollama_host`, `chat_model`, and `embed_model` are
stored in YAML. `providers` and `active_provider` optionally configure chat
providers using OpenAI's compatible `/v1` API shape. The default vault and
database live under the user's home directory, not inside this repository.
Fresh installations use `~/Athena` for the vault and
`~/.local/share/athena/athena.db` for SQLite.

`vault_path` and `db_path` are required. If an existing config file leaves
either one empty, `config.Load` fails and names the file and the field. They
are deliberately *not* repaired to defaults the way the Ollama fields are: an
empty path resolves against the process working directory, so silently
substituting a default would either create a second empty vault somewhere the
user never chose or point Athena at a different database than the one holding
their notes. Losing sight of the real data is worse than refusing to start.

Athena previously stored its configuration and credentials in
`~/.config/second-brain`. On first use of each known file, Athena copies it to
`~/.config/athena` and uses the new location from then on. The old copy remains
as a recovery fallback. Values inside an existing config—including a legacy
vault or database path—are preserved, so Athena never relocates user notes or
database data implicitly.

## Where the files live (XDG)

`internal/appdirs` resolves every Athena file through the XDG base directory
variables:

- `XDG_CONFIG_HOME` (default `~/.config`) holds `athena/` configuration and
  credential files, including the legacy `second-brain/` directory Athena
  migrates from.
- `XDG_DATA_HOME` (default `~/.local/share`) holds `athena/athena.db` for fresh
  installations.

A variable that is unset, empty, or *relative* is ignored and the `$HOME`
default is used instead — the specification requires an absolute path, and
honouring a relative one would scatter Athena's state through whatever
directory it was started from. `vault_path` is not an XDG location: it is a
document directory the user picks, defaulting to `~/Athena`.

The paths below assume the defaults.

## Local files

- `~/.config/athena/config.yaml`: normal configuration
- `~/.config/athena/provider-credentials.json`: API keys entered through the
  `/connect` wizard, stored in plaintext with mode `0600`
- `~/.config/athena/openai-codex-auth.json`: OpenAI subscription OAuth tokens
- `~/.config/athena/openai-codex-import.json`: the recorded yes/no to adopting
  the Codex CLI's `~/.codex/auth.json`; kept apart from the tokens so rewriting
  them cannot erase a "no"
- `~/.config/athena/xai-oauth.json`: xAI OAuth tokens
- `~/.local/share/athena/athena.db`: fresh-install SQLite database, including
  note indexes and the action audit log
- `~/Athena`: fresh-install note vault

Conversation history, pending plans, and pending clarification tasks are held
in memory and disappear when the session exits or is cleared; they do not have
a local task-state file. See [Chat workflow](../chat/README.md) for the task
lifecycle and privacy rationale.

Use `/models` to navigate models with the arrow keys. It lists every connected
provider, so switching back to one you already authenticated does not need
another login — see [Providers](../providers/README.md#what-models-shows). Its
final option opens `/connect`, which can add
ChatGPT Plus/Pro (Codex OAuth), OpenAI, Anthropic, Grok Pro/SuperGrok (xAI
OAuth), the xAI API, OpenRouter, Ollama, or a custom OpenAI-compatible service.

### The source tree is never a vault

A fresh install writes nothing into a clone of this repository. `defaultConfig`
resolves the vault to `$HOME/Athena` and the database through
`appdirs.DataFile`, and `appdirs` falls back to `$HOME` — never the process
working directory — when an XDG variable is unset or relative. Running Athena
from the source directory does not change any of that.

`data/second-brain.db` in this repository is a leftover from the older layout.
No code path reads it and it is not a test fixture, and it is still deliberately
left in place: `L-06` in `tasks.md` says to keep it until a later cleanup PR,
because nobody has established that it is not a real database somebody still
wants. That cleanup PR has no owning task yet. Until it happens the file is
history, not evidence that the project directory holds your notes.

If you deliberately point `vault_path` or `db_path` inside the checkout — a
scratch vault while developing, say — `.gitignore` keeps the result out of
commits: `*.db` with SQLite's `-wal`/`-shm` sidecars, `*.sqlite`/`*.sqlite3`,
and the `data/`, `Athena/`, and `vault/` directories at the repository root.
Those rules do not untrack `data/second-brain.db`, because git ignores only
files it is not already following; changing or deleting it still shows up in
`git status`.

## Where credentials actually live

`config.yaml` never holds a secret. It stores the provider name, base URL,
model, and an API-key *environment variable name* (for example
`OPENAI_API_KEY`). The value itself comes from one of two places:

1. **The environment.** Export the variable before starting Athena:

   ```bash
   export OPENAI_API_KEY='...'
   athena
   ```

2. **`provider-credentials.json`.** If you type an API key into the `/connect`
   wizard, Athena saves it to `~/.config/athena/provider-credentials.json` with
   mode `0600` and reads it back on later runs. This file *does* contain the
   key in plaintext.

A `/connect` that leaves the key field blank keeps whatever key is already
stored for that provider rather than clearing it.

OAuth providers store their tokens in their own files (see **Local files**
above). Those are reused across restarts. Athena does not adopt another
application's tokens without an answer — see
[Providers](../providers/README.md#the-codex-clis-tokens-are-not-adopted-silently).

`CredentialStore.DeleteAPIKey(provider)` removes a stored key from the file, so
a leaked or rotated credential can be taken out rather than overwritten with a
placeholder. Deleting a provider that has no stored key succeeds.

### If the credential file is readable by others

`provider-credentials.json` is written with mode `0600`. If it is found with
any group or world permission bit set at load time, Athena prints a warning to
stderr naming the file and asking you to rotate the key, tightens the mode back
to `0600`, and continues.

It warns rather than refusing to load on purpose. The key has already been
exposed to anyone with an account on the machine, so failing shut protects
nothing; it would only lock the user out of every configured provider with no
way to fix the permissions from inside Athena. Treat the warning as a signal to
rotate that key — tightening the mode does not un-leak it.

See [Providers](../providers/README.md) for the re-login rules.

## Embeddings

Embeddings default to the local Ollama model in `embed_model`, but they are not
locked to it. An `embedding_provider` block switches the vault to any
OpenAI-compatible `/embeddings` endpoint:

```yaml
embedding_provider:
  type: openai_compatible
  name: OpenAI
  base_url: https://api.openai.com/v1
  api_key_env: OPENAI_API_KEY
  model: text-embedding-3-small
```

When `embedding_provider.type` is `openai_compatible`, startup does not require
a running Ollama for embeddings.

Chat provider selection stays independent from this. Changing the embedding
model or provider invalidates every stored vector — Athena does not yet rebuild
them for you (`V-03` in `tasks.md`), so search quality degrades silently until
it does.

Athena keeps the native Anthropic Messages API adapter separate from the
OpenAI-compatible adapter. This prevents provider-specific tool-call formats
from leaking into the rest of the application.

See [the provider extension guide](../providers/README.md) for provider setup
and tool queue limits.

## Saving configuration

`config.Load` records the file it read from, and `Config.Save` writes back to
that same path. A `Config` built in code has no source file, and `Save` returns
an error rather than falling back to the user's real config path. Use
`Config.SaveTo(path)` when a caller genuinely owns the destination.

This guard exists because the fallback was destructive: a test fixture that
called `Save` overwrote a live `~/.config/athena/config.yaml`.

## Change guidance

- Add an action handler in `buildDispatcher` whenever an action is advertised
  in `internal/ai/prompt.go`. Expensive whole-vault work is the exception: the
  index rebuild is wired as the `/reindex` user command instead, so a weak local
  model cannot propose it. `main` passes `notes.Service` to `chat.Loop` and
  gains no rebuild logic of its own.
- `main` calls `TrackJobsIn` when it builds the notes service. Without it a
  rebuild still works but records nothing, and `/doctor` can no longer tell
  which embedding model built the vectors.
- Post-write verification is a note rule, not wiring. `main` registers
  `notes.Service.VerifyWrite` for every type in `notes.VerifiedWriteActions()`
  and nothing more; a new invariant is added in `internal/notes/verify.go`, not
  here. See [notes](../notes/README.md).
- Keep startup wiring in `main`; do not put note business rules there. Which
  adapter a saved provider entry becomes is one such rule: `main` calls
  `chat.BuildProvider`, the same builder `/connect` uses, instead of knowing
  provider types itself. A single unusable entry warns on stderr and is skipped
  rather than aborting startup — refusing to run over one bad provider leaves
  the user no way in to fix it.
- Do not log future credentials or secrets.
