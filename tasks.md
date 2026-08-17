# Athena improvement backlog

This file is **planned work**, not current behavior. Current behavior lives in
`docs/`. Standing rules live in `AGENTS.md`.

Decisions already made:

- Keep the Go engine. Do not invent a new architecture.
- The product TUI is TypeScript/Ink (`apps/tui`). Freeze Bubble Tea.
- The app must become event-driven so loading and progress are real.
- Soft-deleted notes must never enter RAG.
- Switching providers must not force a re-login when credentials are still valid.
- A ~2B local model is a first-class reliability target.
- Memory (goal, plan, task, compaction) is application-owned.
- Obsidian graph/orb styling is a first-class vault feature.
- Ink themes are a product requirement, not a later polish item.

How to use this file:

1. Work **your assigned queue only**. See **Agent pickup** below.
2. One logical change at a time.
3. When a task ships, set its status to `done` and update the matching `docs/`
   file in the same change.
4. Talk before coding on anything marked `design-first`.
5. Do not edit the other agent's files. If you are blocked by them, stop and say so.

Status values: `todo` · `design-first` · `in-progress` · `done`.

---

# Agent pickup

Say one of these to the agent:

- **"take Claude tasks"** / **"claude queue"** → do only rows with **Owner = Claude**
- **"take Grok tasks"** / **"grok queue"** → do only rows with **Owner = Grok**

Claude owns the **Go engine**: session, providers, vault, retrieval, tools,
small-model reliability, protocol schema, docs that describe engine behavior.

Grok owns the **TypeScript TUI**: Ink UI, EngineClient, themes, transcript,
palette, and protocol *client* types.

Do not start the other agent's queue. Shared seams (protocol events) are
owned by Claude on the Go/schema side and Grok on the TS/UI side. If the
schema is not there yet, Grok renders what exists and lists the gap.

**Claude start here:** D-01 → V-01 → P-01 → P-02 → F-03 → F-04 → E-01
**Grok start here:** (queue empty)

---

## 0. Docs truth

The next change should not re-learn a lie.

| ID | Task | Why | Where | Acceptance | Owner | Status |
| --- | --- | --- | --- | --- | --- |
| D-01 | Align docs with the code that exists today | Stale docs caused the dual-TUI and timeout confusion | `docs/chat`, `docs/tui`, `docs/configuration`, `docs/providers`, `apps/tui/README.md` | Docs say: Ink is default if built; turn timeout is 5 minutes; listing is the only exact shortcut; API keys may live in `provider-credentials.json`; embeddings can be OpenAI-compatible; `/clear` in Ink is view-only; “streaming” is activity + one final reply | Claude | done |
| D-02 | Keep `docs/` and `tasks.md` in the same change as behavior | Documentation is part of the implementation | every future PR | No merged change leaves `docs/` describing the old behavior | Claude | todo |

---

## 1. Foundations the TUI cannot fake

These are engine/protocol fixes. Doing TUI polish first will paper over them.

| ID | Task | Why | Where | Acceptance | Owner | Status |
| --- | --- | --- | --- | --- | --- |
| F-01 | Treat `protocol/athena.v1.schema.json` as the contract | Go, TS, and the schema already disagree (`connection`, `presets`, action fields, event types) | `protocol/`, `internal/transport/stdio` (Grok updates `apps/tui` types in F-02) | One schema lists every request/event; extra fields are explicit; a Go test fails if the server drifts | Claude | done |
| F-02 | Resolve engine RPCs on the terminal event | `EngineClient` currently completes on the first matching `requestId` (`turn.started` / first `activity`) | `apps/tui/src/engine/EngineClient.ts`, stdio tests | `submit` / `approve` / `oauth` promises settle on `turn.completed`, `plan.ready`, `plan.approved`, `error`, or `turn.failed` | Grok | done |
| F-03 | Add `session.reset` to the protocol | Ink `/clear` wipes the pane; engine history and pending plan/task remain | stdio server, `chat.Session`, Ink client | `/reset` (or `/clear --session`) clears engine history, pending plan, and pending task; `/clear` stays view-only and is labeled that way | Claude | done |
| F-04 | Release `Session.mu` around model I/O | A 5-minute lock stalls cancel, `/models`, and OAuth | `internal/chat/session.go`, stdio server | Cancel is accepted during a turn; `model.list` does not wait for generation; no deadlock if a UI callback touches session | Claude | done |
| F-05 | Keep an approved plan until resume finishes | `approvePlan` nils `pendingPlan` first; cancel/error loses the exact plan | `internal/chat/session.go` | After `/confirm`, cancel or failure leaves remaining work as a new plan or an explicit partial-apply state | Claude | done |
| F-06 | Drop the process-wide 1-hour engine deadline | `main` wraps `stdio.Serve` in a 3600s timeout | `cmd/athena/main.go` | Long sessions do not die at 60 minutes; turn timeout remains per turn | Claude | done |
| F-07 | Freeze the Go TUI | Two clients are why both feel unfinished | `internal/tui`, `docs/tui` | No new Bubble Tea features; docs call it fallback only | Claude | done |

---

## 2. Event-driven engine and loading

Goal: the TUI never invents a spinner story. It draws what the engine emits.

| ID | Task | Why | Where | Acceptance | Owner | Status |
| --- | --- | --- | --- | --- | --- |
| E-01 | Emit structured activity for every real step | Today the UI gets a footer string; Grok/Claude feel live because work is blocks | `internal/agent`, `internal/chat/events.go`, stdio | Each retrieve / read-tool / validate / write / verify step has `phase`, `tool`, `target`, `state` (`started` / `succeeded` / `failed`) | Claude | done |
| E-02 | Render activity as transcript blocks in Ink | A pulsing “Cooking” line is not an interactive TUI | `apps/tui` | Search, read, and write appear as foldable rows while the turn runs; they stay in the scrollback after | Grok | done |
| E-03 | Always attach the verified execution ledger to the user-visible outcome | A successful write can hide behind whatever `finish_run` wrote | `internal/chat`, protocol | After mutations, the user sees what actually happened (paths, created/updated), even if the model is terse or a 2B model stays quiet | Claude | done |
| E-04 | Add `response.delta` only after E-01/E-02 | Token streaming is optional; tool blocks are the 80% | protocol, session, Ink | Tokens can stream without breaking activity blocks; if deferred, docs must not say “streaming response text” | Grok | done |
| E-05 | Respawn or recover the engine child | One crash currently kills the Ink session | `EngineClient`, launcher | Unexpected engine exit shows a reconnecting state and `hello`s again; pending view state is explained, not silently faked | Grok | done |

---

## 3. TypeScript TUI product

Ink is the only interactive product. Build it like a small Claude Code / Grok
surface: scrollback first, then modes, then chrome.

| ID | Task | Why | Where | Acceptance | Owner | Status |
| --- | --- | --- | --- | --- | --- |
| U-01 | Split `apps/tui/src/index.tsx` | 965 lines in one component cannot become feature-rich | `apps/tui/src/` | App / transcript / composer / palette / plan card / connect / model picker are separate modules; no behavior change required in the first split | Grok | done |
| U-02 | Real command palette | Hints claim Tab-complete that does not exist | Ink composer | `/` lists commands; arrows move; Tab completes; Enter runs; Esc closes; the hint matches the key | Grok | done |
| U-03 | Fix `/cancel` vs Esc | Palette says “cancel the turn”; typed `/cancel` drops a plan/task | Ink + `docs/tui` | Esc cancels the active turn; `/cancel` is labeled “discard pending plan or question”; both work | Grok | done |
| U-04 | Plan review as a mode | Approval is the product, not a chat trick | Ink plan card | `plan.ready` focuses a card; `Y` / Enter approve, `N` / `R` reject, Esc back; composer does not treat `yes` as approve | Grok | done |
| U-05 | Engine-driven action summaries | Ink `describeAction` hardcodes action English | protocol + Ink | Engine sends display text; UI does not switch on every action type | Grok | done |
| U-06 | Markdown transcript | Flat plain text is why the TUI feels weak | Ink transcript | Assistant replies render markdown (headings, lists, fenced code); copy still uses source offsets | Grok | done |
| U-08 | Provider/model pickers from engine catalogs | Go TUI hardcodes presets; Ink wizard encodes connection shape | protocol `provider.list`, Ink | Picker shows every connected provider plus connect-new; fields come from the preset, not a TS switch | Grok | done |
| U-09 | Show engine diagnostics | stderr `"diagnostic"` is currently discarded | EngineClient, Ink | Ollama / OAuth / engine warnings are visible without mixing into JSON stdout | Grok | done |
| U-10 | Tests for the client | Only `transcript.test.ts` exists | `apps/tui` | EngineClient + event reducer tests against fixture NDJSON lines | Grok | done |
| U-11 | Help overlay and empty/error/offline states | A weak TUI fails silently | Ink | `/help` is a real overlay; empty vault, engine-down, and no-models states have copy and a next action | Grok | done |
| U-12 | Scrollback as an object | Grok/Claude feel good because you can move by turn | Ink transcript | PageUp/Down, jump previous/next user turn, fold/expand a tool block | Grok | done |
| U-13 | Allow `/doctor` `/models` `/compact` while a plan is pending | A pending plan currently blocks diagnostics | `internal/chat/session.go` | Review stays blocking for new goals; inspection commands still work | Claude | done |

---

## 3b. Themes (required)

Ink has hardcoded amber/cream. The Go TUI already has `midnight` / `ocean` /
`system`. Ship themes in Ink; do not leave them on the fallback client.

| ID | Task | Why | Where | Acceptance | Owner | Status |
| --- | --- | --- | --- | --- | --- |
| T-01 | Central Ink theme tokens | Scattered hex codes will fight every new widget | `apps/tui/src/theme.ts` (new) | One theme object: bg, text, muted, accent, rail, error, success, border, spinner | Grok | done |
| T-02 | Ship `midnight`, `ocean`, and `system` | Parity with the documented Go themes | Ink + `docs/tui` | All three exist; `system` uses ANSI slots so the terminal palette wins | Grok | done |
| T-03 | `/theme` picker with live preview | Switching must feel like Grok, not a config edit | Ink palette | Arrows preview the whole chrome; Enter saves; Esc reverts | Grok | done |
| T-04 | Apply one theme to every surface | A themed transcript with an unthemed composer looks broken | composer, transcript, plan card, palette, pickers, activity, footer | No leftover hardcoded `#e4a853` / `#e9e2d0` outside the theme file | Grok | done |
| T-05 | Persist the chosen theme | Otherwise `/theme` is a toy | config or a small UI prefs file | Restart restores the last theme (same as M-05) | Grok | done |
| T-06 | 16-color fallback | Fancy truecolor themes die in tmux/SSH | theme tokens | `midnight` stays readable on 16-color; no unreadable dark-on-dark | Grok | done |

---

## 4. Provider switching without re-login

Current bug: `/models` lists only the **active** provider. After
“Restore Ollama”, Codex/Grok tokens are still on disk, but the only UI path
back is `/connect` → `RunDeviceLogin`.

| ID | Task | Why | Where | Acceptance | Owner | Status |
| --- | --- | --- | --- | --- | --- |
| P-01 | List every connected provider in `/models` | Users cannot switch back without reconnecting | `internal/chat/providers.go`, protocol, Ink | Picker groups models by provider; Ollama, Codex, and xAI appear if configured or if a valid token file exists | Claude | done |
| P-02 | Activate a saved OAuth provider without device-login | Tokens already persist in `openai-codex-auth.json` / `xai-oauth.json` | `ConnectOAuth` / `SelectModel` | Choosing Grok or Codex with a valid (or refreshable) token switches immediately; device-login runs only when needed | Claude | done |
| P-03 | One provider factory for startup and runtime | `main.go` and `Loop.Connect` both construct adapters; easy to restore in one place and miss the other | `cmd/athena`, `internal/chat/providers.go` | Startup and `/connect` share one builder; switching Ollama does not drop other provider entries from config | Claude | done |
| P-04 | Honest credential docs | Docs say keys are never stored; the UI can save them | `docs/configuration`, `docs/providers` | Docs describe env var, credential file, and OAuth files; re-login rules are written down | Claude | done |
| P-05 | Ask before importing `~/.codex/auth.json` | Silent reuse of another app’s tokens | `internal/ai/codex_oauth.go` | First use prompts; a declined import is remembered | Claude | done |

---

## 5. Reliable on a ~2B model

Athena must still plan, pause, and write correctly when the local model is
small. Frontier models are a bonus, not the baseline.

| ID | Task | Why | Where | Acceptance | Owner | Status |
| --- | --- | --- | --- | --- | --- |
| R-01 | Fixture suite of weak-model outputs | Reliability is untested against the actual failure shape | `internal/chat`, `internal/ai`, `internal/agent` | Tests cover: prose instead of tools, partial JSON, wrong action names, “I’ll create it” with no actions, extra confirmation questions, empty finish | Claude | done |
| R-02 | Keep fenced-JSON fallback as the 2B path | Native tools often fail on small Ollama templates | `read_tools.go`, `ai.ExtractActions` | One native-tool rejection is remembered for that model; the turn still completes via narrowed fenced JSON | Claude | done |
| R-03 | One validation correction, then a safe stop | Small models loop or narrate | `internal/agent`, `response_quality.go` | After the budgeted correction, the user sees a clear stop plus any verified work; dead `refineModelResponse` is deleted or wired, not both | Claude | done |
| R-04 | Exact, safe shortcuts for single-purpose ops | Docs promised folder shortcuts; only listing bypasses the model | `internal/chat` | Exact “list notes” stays; add only high-precision shortcuts that cannot misfire (no compound language) | Claude | done |
| R-05 | Continuation must keep tool/task facts | Length-stop compaction currently keeps only goal + partial answer | `read_tools.go` | The one allowed continuation still has vault reads, task JSON, and the action contract | Claude | done |
| R-06 | Shrink the prompt for small models | A 180-line system prompt plus full action catalog overwhelms 2B | `prompt.go`, `action_contract.go` | 2B / no-native-tools path gets the narrowed contract only; unknown goals still fail closed at the dispatcher | Claude | done |
| R-07 | Required-tool mode for OpenAI-compatible providers | Only Codex forces a tool decision today | `internal/ai/openai_compatible.go` | Mutation turns request `tool_choice=required` when the API supports it | Claude | done |

---

## 6. Memory and state

“Memory” here means: Athena remembers the **goal, answers, plan, and useful
history** so the next turn is not a guess. The vault is long-term memory.
Private chat transcripts stay out of the vault unless the user opts in.

| ID | Task | Why | Where | Acceptance | Owner | Status |
| --- | --- | --- | --- | --- | --- |
| M-01 | Durable in-session task/plan memory | Already mostly designed; pending task is cleared too aggressively in some error paths | `internal/chat/task_state.go`, `session.go` | Error/cancel restores the pending task; a new unrelated goal requires `/cancel` or an explicit “new request” | Claude | todo |
| M-02 | Compaction keeps a factual memory, not a vibe | Auto-compact after ~12k chars can drop the active goal’s facts | `internal/chat/compact.go` | Compacted history always retains: original active goal, pending answers, last verified actions, recent turns | Claude | done |
| M-03 | Session snapshot (design-first) | Restart currently forgets everything; users want it to “remember” | `internal/chat`, config | Written design: what is stored, where, retention, how to wipe; default remains no private transcript file | Claude | design-first |
| M-04 | Opt-in session restore after M-03 | Implementation of the design | chat + protocol + Ink | `/sessions` or auto-restore last session; pending plan IDs remain single-use; user can start fresh | Claude | todo |
| M-05 | Provider + model + theme remembered across restarts | Config already stores active provider; UI chrome does not | config, Ink | Last provider, model, and theme come back; OAuth is reused (P-02), not re-asked | Grok | done |

---

## 7. Retrieval, trash, and vault reliability

| ID | Task | Why | Where | Acceptance | Owner | Status |
| --- | --- | --- | --- | --- | --- |
| V-01 | Exclude trashed notes from search and context | Soft-delete leaks into RAG today | `internal/retrieval`, `internal/storage` | `SearchNotes` / `BuildContext` never return `trashed_from != ''`; test covers it | Claude | done |
| V-02 | Drop or tombstone trash chunks | Leaving vectors in the table wastes work and invites bugs | `internal/notes` trash/restore | Trash removes or hides chunks; restore re-embeds or undeletes | Claude | done |
| V-03 | Wire `Reindex` as a real job | Switching embed models silently poisons search; `jobs` table is unused | `notes.Service.Reindex`, `JobStore`, `/doctor` or `/reindex` | User can rebuild vectors; search refuses or warns on embed-model/dimension mismatch | Claude | done |
| V-04 | Compensating undo on file+DB writes | Create/move can orphan a file or leave a stale path | `internal/notes` | Failed DB insert after file create deletes the new file; failed DB update after move moves the file back | Claude | done |
| V-05 | Inventory without loading every body | `NoteStore.All()` pulls full `content` for catalogs | `internal/storage`, retrieval | Catalog/list/graph sync do not load note bodies | Claude | done |
| V-06 | Write type / done / title into frontmatter | Tasks and rename are invisible or stale in Obsidian | `internal/notes` | Markdown remains portable; rename updates YAML title; duplicate book keeps book YAML | Claude | done |
| V-07 | Startup vault reconcile (after V-06) | Obsidian edits never re-index; untracked files stay invisible | `internal/notes` | Scan adds untracked `.md` (except indexes), flags missing files; no silent overwrite | Claude | done |
| V-08 | SQLite WAL + busy timeout + FK on every connection | Default locking will hurt once UI + engine + search overlap | `internal/storage/sqlite.go` | Documented pragmas; FK cannot silently turn off on a pooled connection | Claude | done |

---

## 8. Obsidian graph / orb styling

Users should be able to say “make the work orb better” or “add projects” and
get a reliable graph change. Today this is a thin `set_folder_colors` /
`set_graph_node_size` plus keyword routing on “color/graph”.

| ID | Task | Why | Where | Acceptance | Owner | Status |
| --- | --- | --- | --- | --- | --- |
| G-01 | Map natural orb language to typed actions | “Make X orb better” does not contain “color” and can miss the contract | `action_contract.go`, prompt, notes graph | Phrases like “orb”, “graph”, “style this folder”, “make X stand out”, “add X to the graph” advertise the graph actions | Claude | done |
| G-02 | Richer orb style actions | Color-hash + one global node size is not “style X better” | `internal/notes/graph.go`, `internal/tools/policy.go` | Can set a named folder’s color, size, and include-children; “better” has a deterministic default (contrast against siblings, not a random hash) | Claude | done |
| G-03 | “Add X” in a graph context creates the folder + index + color | Users mean “put this on the graph”, not only mkdir | notes + dispatcher | After review, the folder exists, the index note exists, and an orb color is assigned; missing-parent is explicit | Claude | done |
| G-04 | Do not overwrite user-chosen Obsidian colors | Already partly true; keep it as a test-backed invariant | `graph.go`, tests | Athena never replaces a valid user color; it only fills missing Athena groups or applies an explicit request | Claude | done |
| G-05 | Show graph results in the TUI | Otherwise the user has to open Obsidian to know if it worked | events + Ink | Verified graph actions report folder, color, and size in the transcript | Grok | done |

---

## 9. Architecture cleanup (after the product holes)

Do these once the user-facing gaps above are honest. They reduce gravity; they
are not a rewrite.

| ID | Task | Why | Where | Acceptance | Owner | Status |
| --- | --- | --- | --- | --- | --- |
| A-01 | Move `verifyWrite` out of `main.go` | Composition root owns note invariants today | `cmd/athena/main.go` → `internal/notes` or `internal/tools` | `main` registers handlers; verification sits next to the domain | Claude | done |
| A-02 | Delete unused leftovers | Empty `internal/embeddings`, unused `Loop.Run`, dead `refineModelResponse`, `data/second-brain.db` if not a fixture | those paths | No dead package/path without a comment pointing at a scheduled task | Claude | done |
| A-03 | Split `read_tools.go` | 664 lines mixing schemas, loop, fallback, execution | `internal/chat` | Three files by responsibility; tests still pass | Claude | done |
| A-04 | Single `providerID` helper | Copied in `main.go` and `chat/providers.go` | config or one shared func | One implementation | Claude | done |
| A-05 | HTTP timeouts on every chat/embed client | Clients are `&http.Client{}` with no Timeout | `internal/ai` | Hung dials die with the turn context plus a client timeout; token-bearing requests do not follow arbitrary redirects | Claude | done |
| A-06 | Validate `/connect` base URL | Any `http(s)` host currently gets the API key | `internal/chat/providers.go` | Only `https` or loopback `http`; bad URLs are rejected before save | Claude | done |

---

## 10. Safety and vault edges

| ID | Task | Why | Where | Acceptance | Owner | Status |
| --- | --- | --- | --- | --- | --- |
| S-01 | Default review for create/move/rename | Remote models plus unreviewed writes is a prompt-injection hole | `internal/tools/policy.go` | Those writes wait for a plan card unless an explicit allowlist says otherwise | Claude | done |
| S-02 | Warn when vault text will leave the machine | Remote providers receive full notes with no notice | connect UX, docs | First remote connect states that inventory/search/get_note go to that provider | Grok | done |
| S-03 | Slug collision is not “already exists” | `Go Slices` vs `Go: Slices` returns the other note as created=false | `internal/notes`, `utils.Slugify` | Different titles that share a slug error with both titles | Claude | done |
| S-04 | Archive and trash cannot stack | Both flags can be set; paths can nest | `internal/notes` | Archive-if-trashed and trash-if-archived are rejected | Claude | done |
| S-05 | Book default folder is code, not a prompt convention | Docs say `books/reading`; empty folder writes vault root | `notes.CreateBook`, docs | New books land in `books/reading` unless the user names another existing folder | Claude | done |
| S-06 | Fix docs vs mkdir | Docs say create makes parents; code requires the folder to exist | `docs/notes` or `CreateNote` | Code and docs agree; pick one rule and test it | Claude | done |
| S-07 | `MoveNote` uses the same dest-exists check as `MoveFile` | `os.Rename` can clobber a disk-only file | `internal/notes` | Dest file without a DB row is refused | Claude | done |
| S-08 | Empty-trash / permanent delete (design-first) | Trash is unbounded; no way to finish a delete | notes + tools + Ink | Written rule: what is deleted, what is confirm-only, what is irreversible | Claude | design-first |

---

## 11. Tests that prove the product

| ID | Task | Why | Where | Acceptance | Owner | Status |
| --- | --- | --- | --- | --- | --- |
| I-01 | Session tests: submit → write → finish; approve-then-cancel; listing shortcut | Those paths are untested | `internal/chat` | Mocked provider; no live Ollama | Claude | done |
| I-02 | Storage + search tests | No NoteStore / cosine / trash-leak tests | `internal/storage`, `internal/retrieval` | Encode/decode, dim mismatch → 0, trash excluded, `ReplaceAll` rollback | Claude | done |
| I-03 | Parser/chunker tests | Zero parser tests | `internal/parser` | CRLF frontmatter, overlap, empty body → title chunk | Claude | done |
| I-04 | Anthropic + embedding mock-transport tests | Those adapters are untested | `internal/ai` | Same RoundTripper style as Codex | Claude | done |
| I-05 | Doctor tests | `/doctor` has no tests | `internal/chat/doctor.go` | Vault unwritable and missing embed model are reported | Claude | done |

---

## 12. Product entry

| ID | Task | Why | Where | Acceptance | Owner | Status |
| --- | --- | --- | --- | --- | --- |
| C-01 | Root `README.md` | There is no project README | repo root | What Athena is, install, `athena` vs `--legacy-tui`, link to `docs/` and `tasks.md` | Grok | done |
| C-02 | Config validation + XDG | Empty paths are accepted; XDG dirs are ignored | `internal/config`, `internal/appdirs` | Missing vault/db path fails load; honor `XDG_CONFIG_HOME` / `XDG_DATA_HOME` | Claude | done |
| C-03 | Credential delete / refuse world-readable secret files | Store can save keys; cannot rotate; 0644 is not re-checked | `internal/config/credentials.go` | Delete API; load fails or warns if mode is too open | Claude | done |

---

## Suggested first slice (split)

Claude: **D-01 → V-01 → P-01+P-02 → F-03 → F-04 → E-01**
Grok: queue empty (through L-01 done)

They can run in parallel. Grok does not wait on Claude except that E-02
gets better after E-01 exists.

---

## 13. Later (last)

Do these after the product is honest. They are real, not filler. Order in
this table is the order to start them.

| ID | Task | Why | Where | Acceptance | Owner | Status |
| --- | --- | --- | --- | --- | --- |
| L-01 | Vim mode in the Ink TUI | Power-user nav like Grok; useless until scrollback is an object (U-12) | Ink, UI prefs | Opt-in `/vim-mode`; `j`/`k` move turns, `g`/`G` top/bottom, `i` focuses the composer; default stays simple arrows | Grok | done |
| L-02 | Vector index for search | Brute-force cosine is fine for a personal vault until inventory and trash are fixed (V-01, V-05) | `internal/storage`, `internal/retrieval` | Profile first; add an index only after search is a measured bottleneck; reindex (V-03) still works | Claude | done |
| L-03 | OS keyring for API keys and OAuth tokens | 0600 JSON files are enough on a single-user laptop; shared machines are not | `internal/config`, `internal/ai` | Secrets can live in the platform keyring; files remain the fallback; no plaintext new default until this ships | Claude | todo |
| L-04 | Permanent delete / empty trash | Implements `S-08` after that design exists | notes, tools, Ink | `/empty-trash` or an explicit action; double confirm; irreversible; never hooked to a 2B-model impulse | Claude | todo |
| L-05 | Multi-device / synced vault | Absolute DB paths, no file watcher, and slug identity will break a shared or git-synced vault | notes, storage, docs | Written design first: relative paths, reconcile on start, no silent overwrite; then implementation. Not a Dropbox hack | Claude | design-first |
| L-06 | User vault and database never live in the git checkout | `data/second-brain.db` is in this repo today. **Do not delete it now.** Fresh-install defaults already use `~/Athena` and `~/.local/share/athena/athena.db`, but a clone still looks like the project is the vault | `data/`, `.gitignore`, `docs/configuration`, install docs | Keep the existing `data/` file until a later cleanup PR. New installs never write vault or SQLite inside the source tree. Docs say user data is under home. `.gitignore` ignores local `*.db` / vault copies so they cannot be committed by accident. A future cleanup PR may remove `data/second-brain.db` after that is true | Claude | done |

---

# Claude tasks

Do this list if the user says **take Claude tasks**.

You own the engine. Stay in Go + `docs/` + `protocol/athena.v1.schema.json`.
Do not rebuild the Ink app.

| Order | ID | What you are doing |
| --- | --- | --- |
| ~~1~~ | ~~D-01~~ | **done** — `docs/` matches the code (5-min turn, Ink default, stored keys, no fake shortcuts). |
| ~~2~~ | ~~V-01~~ | **done** — `ChunkStore.Searchable` keeps trashed notes out of search/RAG; test covers it. |
| ~~3~~ | ~~P-01 + P-02~~ | **done** — `/models` lists every connected provider; saved OAuth sessions activate without another device login. |
| ~~4~~ | ~~F-03~~ | **done** — `session.reset` request + `Session.Reset`. Ink `/reset` now sends it. |
| ~~5~~ | ~~F-04~~ | **done** — split `turnMu` (turn serialization) from `mu` (state); `/models` and inspection stay live mid-turn. |
| ~~6~~ | ~~E-01~~ | **done** — retrieve / read-tool / validate / execute / verify steps carry `phase`, `tool`, `target`, `state`; schema documents them. |
| then | ~~F-01~~ F-05, F-06, F-07 | **F-01 done** — schema is the contract, drift test enforces it, actions carry `summary`. Remaining: plan-not-lost, drop 1-hour engine timeout, freeze Bubble Tea. |
| then | P-03 P-04 P-05 | One provider factory, honest credential docs, ask before `~/.codex` import. |
| then | ~~E-03~~ | **done** — ledger rides every outcome and the terminal protocol event. |
| then | U-13 | Allow `/doctor` `/models` `/compact` while a plan is pending (`session.go`). |
| then | R-* | 2B-model reliability: fixtures, JSON fallback, keep facts on continue, smaller prompt. |
| then | M-01 M-02 M-03 M-04 | In-session memory, compaction, then design/restore (talk first on M-03). |
| then | V-02–V-08 | Trash chunks, reindex job, file+DB undo, inventory without bodies, frontmatter, reconcile, SQLite pragmas. |
| then | G-01, ~~G-02~~, G-03, G-04 | **G-02 done** — explicit `color`, sibling-contrast default, reported per folder. Remaining: orb language (G-01), add-to-graph (G-03), colour invariant test (G-04). Leave G-05 to Grok. |
| then | A-* I-* C-02 C-03 S-* except S-02 | Cleanup, tests, config, safety. S-08 is design-first. |
| last | L-02 L-03 L-04 L-05 L-06 | Vector index, keyring, empty trash, multi-device, vault/DB not in repo. Do not delete `data/second-brain.db` until L-06 says so. |

---

# Grok tasks

Do this list if the user says **take Grok tasks**.

You own the Ink TUI. Stay in `apps/tui/`. Do not rewrite chat/notes/tools
policy. If the engine event you need is missing, render what exists and name
the Claude ID that must land first.

| Order | ID | What you are doing |
| --- | --- | --- |
| 1 | F-02 | `EngineClient` waits for the **last** event (`turn.completed` / `plan.ready` / `error`), not the first `requestId`. Done. |
| ~~2~~ | ~~U-01~~ | **done** — Ink source is App + `src/ui/` transcript / composer / palette / plan / connect / model picker. |
| ~~3~~ | ~~U-02 + U-03~~ | **done** — `/` palette (arrows, Tab, Enter, Esc). Esc cancels the turn; `/cancel` discards a plan or question. |
| ~~4~~ | ~~E-02~~ | **done** — search/read/write activity is foldable transcript blocks. Richer step states still need Claude E-01. |
| ~~5~~ | ~~T-01–T-05~~ | **done** — `theme.ts` tokens, midnight/ocean/system, `/theme` live preview, applied everywhere, persisted in `ui.json`. |
| ~~then~~ | ~~T-06~~ | **done** — 16-color midnight/ocean use ANSI slots and skip a painted background. |
| ~~then~~ | ~~U-04~~ | **done** — plan card is a focused mode (Y/Enter, N/R, Esc back). Typed yes is not approve. |
| ~~then~~ | ~~U-05~~ | **done** — plan card uses `summary` when present; otherwise type → target. No action-type switch. Engine still needs Claude F-01 to send `summary`. |
| ~~then~~ | ~~U-06~~ | **done** — assistant replies style headings, lists, and fenced code; copy still uses source offsets. |
| ~~then~~ | ~~U-08~~ | **done** — `/models` groups connected providers and offers connect-new; `/connect` fields come from the preset. |
| ~~then~~ | ~~U-09~~ | **done** — stderr diagnostics are line-buffered and shown as `engine` transcript rows. |
| ~~then~~ | ~~U-10~~ | **done** — EngineClient and `reduceEvent` replay NDJSON fixtures in `apps/tui/testdata`. |
| ~~then~~ | ~~U-11~~ | **done** — `/help` overlay; connecting / vault / no-models / engine-down cards have copy and a next action. |
| ~~then~~ | ~~U-12~~ | **done** — PageUp/Down, Ctrl+↑↓ user-turn jump, Ctrl+O folds the visible turn's work block. |
| ~~then~~ | ~~E-04~~ | **done** — Ink appends `response.delta` without folding work blocks. Engine emit is still Claude (`F-01` / session). |
| ~~then~~ | ~~E-05~~ | **done** — unexpected engine exit reconnects and hellos; old plan/turn IDs are dropped and explained. |
| ~~then~~ | ~~M-05~~ | **done** — `ui.json` stores theme + last provider/model; hello restores via `model.select` without device-login. |
| ~~then~~ | ~~G-05~~ | **done** — transcript reports graph folder and size; hex color waits on Claude `G-02` / `E-03`. |
| ~~then~~ | ~~S-02~~ | **done** — first remote `/connect` warns that inventory/search/get_note leave the machine. |
| ~~then~~ | ~~C-01~~ | **done** — root README: what Athena is, install, `athena` vs `--legacy-tui`, links to `docs/` and `tasks.md`. |
| ~~last~~ | ~~L-01~~ | **done** — opt-in `/vim-mode`; Esc normal, i insert, j/k turns, g/G top/bottom. |

Do not take Claude rows. Do not add features to `internal/tui`.

---

# Handoff: Claude → Grok

Claude's engine queue is finished except for the eight rows below. Every other
Claude row is `done`, verified against `go build ./... && go vet ./... && go
test ./... -count=1 -race` on a quiet tree (15 packages, all green).

**Read this before picking one up.** These are not "the leftovers Claude got
bored of". Six of the eight are blocked on a decision the repository owner has
to make, and two of those decisions are irreversible once shipped. Building
them without an answer means guessing at what the owner wants and then living
with it.

## Blocked on an owner decision — do not implement yet

| ID | Why it is blocked | What is already written |
| --- | --- | --- |
| M-03 | `design-first`. Privacy call: how much of a private transcript goes on disk, and whether restore is automatic or an explicit `/resume`. Auto-restore reprints your last conversation into a screen-shared terminal with nobody consenting at that moment. | `docs/plans/session-restore.md` — full proposal, 7 questions |
| M-04 | Implements whatever M-03 decides. Cannot start first. | — |
| S-08 | `design-first`. Recommends permanent delete be **user-only forever** — a typed `/trash empty confirm <count>`, with no `empty_trash` action existing anywhere, so a weak model has no name for it and fails closed if it invents one. If the owner agrees, that belongs in `AGENTS.md` as a standing rule. | `docs/plans/permanent-delete.md` — 5 questions |
| L-04 | Implements S-08. Irreversible by definition. Cannot start first. | — |
| L-05 | `design-first`. Two irreversible commitments: a visible `athena_id:` in every note's frontmatter (removable only before other devices sync), and the local database becoming disposable. Genuinely blocked on V-06's read half besides. | `docs/plans/multi-device-vault.md` — 8 questions |
| L-03 | Needs a new dependency, which `AGENTS.md` says must be justified first. The analysis recommends **not building it yet**: against the dominant threat (any process running as you) a Secret Service keyring is equivalent to a 0600 file, and it introduces a startup hang over SSH that does not exist today. | `docs/plans/secret-storage.md` — 7 questions, dependency verdict |

## Genuinely unfinished

**M-01 (second half)** — "a new unrelated goal requires `/cancel`". The restore
half is done. The other half was deliberately not implemented: a pending
question is often answered with a bare noun phrase ("Science Fiction", "the
chapter three one") that is lexically indistinguishable from a short new
request, so any similarity heuristic sometimes reclassifies an answer as a new
goal and strands the user mid-task. Doing it properly needs an **explicit typed
intent**, not prose sniffing — a "new request" affordance in the Ink composer,
or a `session.newGoal` protocol request. That spans `apps/tui` (yours) and the
protocol schema (Claude's side of the seam). **This is the one row where Grok
is the right owner**, because the answer lives in the UI.

**D-02** — not a unit of work. It is the standing rule that `docs/` ships in the
same change as behaviour. It stays `todo` forever by design; treat it as a
review checklist item, not a task.

## Engine-side notes for the Ink client

Landed this session and not yet consumed by `apps/tui`:

- `session.reset` exists in the protocol; the Ink client never sends it, so
  `/clear` is still view-only while engine history, a pending plan and a pending
  task all survive it (`grep -rn "session.reset" apps/tui/src` returns nothing).
- `plan.approve` settling on `error` is **not** terminal — a `plan.ready` for the
  re-staged remainder follows it. A client that stops on the error leaves an
  approved plan reachable in the engine and invisible in the UI.
- `ledger` now rides `turn.completed`, `turn.cancelled`, `turn.error` and
  `plan.approved` alike, so an interrupted turn can still show what it wrote.
- `/reindex` works but is listed in neither `internal/tui/bubble.go`'s help nor
  `apps/tui/src/ui/palette.ts`.
- `apps/tui/src/protocol/types.ts` declares `fields?: string[]` on the provider
  preset. The engine can never send it — `chat.ProviderPreset` has no such field
  and the schema sets `additionalProperties: false`, enforced both ways by
  `internal/transport/stdio/schema_test.go`. Dead branch in the connect wizard.

## Known engine gaps, left open on purpose

Not assigned to Grok — `AGENTS.md` keeps engine policy with Claude. Recorded so
they are not lost:

- `retrieval.SearchNotes` has a narrow window: a note trashed between
  `SearchSimilar` and `GetByID` inside one call is still returned. Closing it
  means choosing skip-vs-abort inside search.
- Search does not consult `IndexHealth` before answering, so a stale embed model
  degrades results silently; `/doctor` warns but search does not.
- `cmd/athena/main.go` is 524 lines — under 829 after A-01, still over the
  `AGENTS.md` 500-line threshold. The remainder is `buildDispatcher`'s ~290
  lines of handler closures.
- `internal/ai/oauth_file.go` has no load-time permission check, unlike
  `credentials.go`. The OAuth files hold refresh tokens, which are worth more
  than an API key.
- A note already in a stacked archive+trash state from before S-04's guard is
  not repaired by it; the guard only refuses new ones.
