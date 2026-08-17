# L-03 — OS keyring for API keys and OAuth tokens (design)

**Status:** proposal. Nothing here exists yet. `docs/` describes today; this file
describes a decision the owner has not made.

**Recommendation up front:** do not add a keyring dependency yet. Ship the four
cheap file-layer fixes in [Option A](#option-a--keep-files-and-close-the-real-gaps-cheapest).
The reasoning is in [§2](#2-threat-model) and [§6](#6-dependency-verdict).

**Scope:** `internal/config/credentials.go`, `internal/ai/codex_oauth.go`,
`internal/ai/xai_oauth.go`, `internal/ai/oauth_file.go`, `internal/appdirs`,
`docs/configuration/README.md`.

**Related rows:** `L-03` (this design and the implementation that would follow),
`S-*` (safety), `L-06` (leftover files in the repository).

**Why a proposal and not a patch:** `AGENTS.md` § Dependencies requires that a
new dependency come with why it is needed, what the alternatives are, and the
maintenance implications. Athena has five direct requires today. A keyring is a
new one, so the owner decides, not the implementer.

---

## 1. What exists today

### 1.1 API keys — one 0600 JSON file, written atomically

`internal/config/credentials.go` holds the whole story.

`CredentialStore` (line 16) is a mutex, a path, and `map[string]string` of
provider ID → key. `LoadCredentialStore` (line 22) resolves the path through
`appdirs.PrepareConfigFile("provider-credentials.json")`, returns an empty store
when the file does not exist, and otherwise stats it, checks the mode, reads and
unmarshals it.

`saveLocked` (line 126) is a correct atomic replace: `MkdirAll` + `Chmod` the
directory to `0700`, marshal, `os.CreateTemp` **in the same directory**, `Chmod`
the temporary file to `0600` before writing anything into it, write, `Sync`,
`Close`, `Rename`, then `Chmod 0600` on the final path. A crash cannot leave a
half-written credential file, and the secret is never briefly world-readable.

Both mutators roll back in memory when the write fails — `SaveAPIKey` restores
the previous value or deletes the new entry (lines 70-79), `DeleteAPIKey`
restores the deleted one (lines 97-105). RAM and disk cannot silently diverge.

`restrictCredentialsToOwner` (line 115) is the load-time permission check: if
`mode&0o077 != 0` it prints a warning to stderr naming the file and asking the
user to rotate, `Chmod`s back to `0600`, and continues. The comment explains why
it warns instead of refusing: the key is already exposed, so failing shut
protects nothing while locking the user out of every provider with no in-app way
to fix the mode. `docs/configuration/README.md` documents this behaviour and its
rationale under "If the credential file is readable by others".

Two loose ends in this file:

- **`DeleteAPIKey` (line 87) has no non-test caller.** The only references are
  `internal/config/config_test.go:232` and `:248`. `/connect` calls
  `SaveAPIKey` (`internal/chat/providers.go:397`) and nothing else. There is no
  command, no wizard step, no protocol request that removes a stored key. The
  documentation (`docs/configuration/README.md`, "Where credentials actually
  live") describes it as though a user could use it. It is an API, not a
  feature.
- **`credentialFilePath` (line 166) is dead**, including in tests.

### 1.2 The file is a cache over an environment-variable contract

`chat.BuildProvider` (`internal/chat/providers.go:515`) calls
`provider.SetAPIKey(credentials.APIKeys.APIKey(ProviderID(entry.Name)))` for the
Anthropic and OpenAI-compatible branches. When the store has nothing, that sets
the empty string, and the adapter falls back to the environment:
`openai_compatible.go:92-98` reads `os.Getenv(p.apiKeyEnv)` and errors naming the
variable if it is empty; `anthropic.go:57-59` does the same.

So `provider-credentials.json` never became the only way in. Anyone who does not
want a plaintext key on disk can already export the variable and never touch
`/connect`'s key field. This matters for the options below: the "no secret file
at all" path exists today and is documented.

### 1.3 OAuth tokens — three more files, same shape, one missing check

- `internal/ai/codex_oauth.go`: `openai-codex-auth.json` holds
  `CodexCredentials` (access token, refresh token, `expires_at`, account ID).
  `LoadCodexOAuth` (line 50) reads it plain; `save` (line 526) writes it through
  `writeOwnerOnlyJSON`. `Credentials` (line 464) rewrites the file on **every**
  refresh, so this path is hot, not write-once.
- `internal/ai/xai_oauth.go`: `xai-oauth.json` holds `XAICredentials`.
  `LoadXAIOAuth` (line 66) reads it; `saveLocked` (line 279) writes it through
  the same helper; `AccessToken` (line 223) refreshes and rewrites.
- `internal/ai/oauth_file.go:31`, `writeOwnerOnlyJSON`, is the same atomic
  0700-directory / 0600-file dance as `saveLocked`, deduplicated for the OAuth
  side.

**The asymmetry:** there is no `restrictCredentialsToOwner` equivalent on the
OAuth path. A `openai-codex-auth.json` or `xai-oauth.json` left at `0644` — by a
sloppy backup restore, a `cp` between machines, a permissive `umask` on some
older file — is read without a word and never re-tightened. The refresh token in
those files is worth more than most API keys: it mints access tokens until it is
revoked. This is a real bug today and no keyring is required to fix it.

### 1.4 The consent seam is the precedent this task should follow

`offerCLIImport` (`codex_oauth.go:73`), `PendingCLIImport` (line 98) and
`ResolveCLIImport` (line 107) exist because the OpenAI Codex CLI's
`~/.codex/auth.json` belongs to another application. The rule the code enforces:

- silence is not consent — without a recorded answer the CLI's tokens are never
  used, only *offered* (`o.pendingImport = true`, line 91);
- the answer is persisted in its own file, `openai-codex-import.json`
  (`codexImportDecisionPath`, line 127), specifically so that rewriting the
  tokens on every refresh cannot erase a "no";
- a caller that never asks is safe by default.

That is exactly the shape a keyring needs. A keyring is another store owned by
another component, it can prompt, and it can be declined. If B ever ships, it
reuses this pattern rather than inventing a second consent mechanism.

Note also what §1.4 implies for the threat model: when the import is approved,
the Codex CLI's own plaintext `~/.codex/auth.json` is still sitting there
(`loadCodexCLICredentials`, line 167, reads it directly). Athena moving *its*
copy into a keyring does not move that one.

### 1.5 Where the files land, and the copy migration leaves behind

`internal/appdirs/appdirs.go`: `ConfigFile` (line 17) resolves
`$XDG_CONFIG_HOME/athena/<name>`, falling back to `$HOME/.config` when the
variable is unset, empty, or relative (`xdgRoot`, line 38). All four secret files
sit in `~/.config/athena/` on a default install.

`PrepareConfigFile` (line 53) migrates from the former `second-brain` directory:
if the Athena path does not exist and a legacy file does, it copies it across
with the same 0700/0600 discipline — and **deliberately leaves the legacy copy
in place** as a recovery fallback (documented in `docs/configuration/README.md`).

For an ordinary config file that is a kindness. For a credential file it means a
second plaintext copy of the same secret survives, in a directory nothing checks
the permissions of, forever. Whoever ships secret storage has to decide about
that copy — see decision 6.

### 1.6 What the documentation already promises

`docs/configuration/README.md` lists all four files under "Local files", states
plainly that `provider-credentials.json` "*does* contain the key in plaintext",
documents the warn-and-tighten behaviour and why it warns rather than refuses,
and documents `DeleteAPIKey`. It also says "Do not log future credentials or
secrets." Any change under this design changes that page in the same commit.

---

## 2. Threat model

The `tasks.md` row says "0600 JSON files are enough on a single-user laptop;
shared machines are not." That is the claim to test.

### 2.1 What a keyring does not help with at all

**Any process running as you.** This is the dominant real threat and a keyring
does nothing about it. The freedesktop Secret Service protocol has no notion of
which application is asking: gnome-keyring, KWallet's `ksecretd`, and every
other implementation hand the secret to any process on the user's session bus
that requests it. A malicious `npm install` postinstall script, a compromised
editor extension, a curl-to-bash — each of them can read the keyring as easily
as it can read `~/.config/athena/provider-credentials.json`. There is no
improvement here, and it is the scenario that actually loses people's keys.

Worth saying out loud: Athena is itself a program with filesystem read tools and
a language model steering them. If that worries you, the mitigation is in
`internal/retrieval` and `internal/tools`, not in where the key is stored.

**Root.** Reads the file, reads `/proc/<pid>/mem`, reads the unlocked keyring
daemon's memory. Both storage schemes lose.

**Environment-variable keys.** `OPENAI_API_KEY` in a shell profile is visible in
`/proc/<pid>/environ` to the same UID, sits in shell history if exported
interactively, and is inherited by every child process Athena spawns
(`xdg-open`, the Ollama check). A keyring does not touch this path, which is
still the documented path for anyone who does not use `/connect`.

**The other copies.** `~/.codex/auth.json` (§1.4) and the legacy
`~/.config/second-brain/*` copy (§1.5) stay plaintext regardless.

### 2.2 What 0600 already handles

**Other unprivileged users on a shared machine** — the row's stated motivation.
`0600` on the file plus `0700` on `~/.config/athena` already denies them, and
`restrictCredentialsToOwner` repairs the case where the mode drifted. The one
gap is the OAuth files having no such check (§1.3), which is a twenty-line fix,
not a dependency.

So the row's own justification is mostly already satisfied. That is the single
most important finding in this document.

### 2.3 What a keyring genuinely adds

**Resistance to copying, not to access.** The real difference is that a 0600
file is *plaintext at rest* and therefore gets carried out of the machine by
things that are not attackers:

- a backup or dotfile sync that includes `~/.config` and lands in cloud storage;
- `tar czf home.tgz ~` handed to someone for debugging;
- a screenshare or a recorded terminal where somebody `cat`s a config directory;
- `grep -r sk- ~` by anyone, including a well-meaning script.

A keyring-stored secret is usually ciphertext on disk (`~/.local/share/keyrings`,
`kwalletd`'s store) and those flows copy the ciphertext.

**Powered-off laptop theft** — only if full-disk encryption is absent, and only
if the keyring is not auto-unlocked by the login password. On a typical GNOME or
KDE setup the login keyring *is* auto-unlocked at login, so this margin is
thinner than it sounds.

### 2.4 Summary

A keyring buys exfiltration-by-copy resistance and a better story. It does not
buy access control against the thing most likely to steal the key. On a
single-user Linux laptop with FDE, the improvement over the current 0600 files
is small. On a shared machine, 0600 already does the job the row credits the
keyring with.

---

## 3. Constraints any option has to respect

1. **Athena runs headless.** `cmd/athena/main.go:28` selects `engine` mode and
   line 156 runs `stdio.Serve` against stdin/stdout with the Ink client as
   parent. There is no terminal to prompt on and no desktop session to display
   a dialog in. Over SSH there is usually no session bus at all.
2. **Startup must not hang.** Today nothing in the credential path can block:
   every call is a `stat`, a `read`, or a `write`. Startup is already written to
   degrade rather than abort — an unusable provider entry warns on stderr and is
   skipped (`docs/configuration/README.md`, "Change guidance").
3. **No cgo.** `modernc.org/sqlite` is the pure-Go driver; the whole build is
   cgo-free today. Anything that reintroduces cgo on Linux is out.
4. **Files stay a fallback**, per the acceptance criterion. Whatever ships must
   still work in a container, in CI, and over SSH.
5. **Small dependency budget.** Five direct requires, four of them the Charm
   family, plus `yaml.v3` and `modernc.org/sqlite`.

---

## 4. Options

### Option A — keep files and close the real gaps (cheapest)

No dependency. Five small changes, all of which fix something broken today:

1. **Apply the permission check to the OAuth files.** Lift
   `restrictCredentialsToOwner` into a shared helper (or duplicate the eight
   lines) and call it from `LoadCodexOAuth`, `LoadXAIOAuth`, and
   `loadCodexImportDecision`. Refresh tokens deserve at least what API keys get.
2. **Give `DeleteAPIKey` a caller.** A `/forget <provider>` command, or a
   "remove stored key" option in `/connect`. Today a leaked key can only be
   overwritten, never removed, from inside Athena — and the documentation
   implies otherwise.
3. **Warn once when the legacy `~/.config/second-brain/<secret file>` copy still
   exists**, and offer to delete it. A migrated user has two plaintext copies
   and knows about one.
4. **Delete `credentialFilePath`** or give it the one caller it was written for.
5. **Rewrite the "shared machines" framing in the docs** with §2 of this
   document: what 0600 does and does not buy, in one short section, so the next
   person does not re-open this question from scratch.

Roughly fifty lines of Go plus documentation. No new failure mode, no new
module, and items 1 and 2 are defects independent of this design.

### Option B — keyring behind an interface, files as fallback

`SecretStore` interface with two implementations, keyring probed at startup,
files used when the probe fails. The dependency analysis is §6; the failure
modes are the reason this is not the recommendation.

Costs beyond the dependency:

- **The state space doubles.** A secret can be in the keyring, in the file, or
  in both with different values. "Which one wins" becomes a bug class, and the
  missing `DeleteAPIKey` caller (§1.1) becomes actively dangerous: a rotated key
  removed from one store and left in the other resurrects on the next run that
  falls back.
- **Migration is a one-way door.** Moving an existing file secret into the
  keyring, then deleting the file, means a user who later runs the same home
  directory over SSH has no credentials at all. Keeping the file means the
  keyring bought nothing (§2.3 is entirely about the file not existing).
- **An interface with one live implementation is speculative.** If the keyring
  does not ship, the interface should not either.

### Option C — passphrase-derived encrypted file, no new dependency

Go 1.26 has everything needed in the standard library: `crypto/pbkdf2` (added in
Go 1.24, verified locally with `go doc crypto/pbkdf2`), `crypto/aes`,
`cipher.NewGCM`, `crypto/rand`. A salt and nonce in the file header, key derived
from a passphrase, `provider-credentials.json` becomes
`provider-credentials.enc`. Zero modules added.

Why it is not the recommendation:

- Someone has to type the passphrase. Every start, in every mode. Engine mode
  (§3.1) has nowhere to ask, so unattended start dies.
- Cache the passphrase to fix that, and it lands in a 0600 file — back to the
  beginning with more code.
- Hand-rolled crypto in a note-taking application is a liability even when the
  primitives are stdlib. One reused nonce and the design is worse than plaintext
  with a warning.

Listed because it is the honest answer to "can this be done without a
dependency" — yes, and it is still the wrong trade.

### Option D — delegate to a command the user already trusts

One config field, borrowed from `git`'s credential helpers:

```yaml
providers:
  - name: OpenAI
    api_key_command: "pass show openai/api-key"
```

`BuildProvider` runs it, trims the output, and uses it as the key. Precedence:
stored key → `api_key_command` → `api_key_env`. Athena stores nothing.

- Roughly fifteen lines plus a `config.ProviderConfig` field and documentation.
- Works over SSH, in containers, in CI — `pass`, `gopass`, `age`, `1password
  --cli`, `sops`, anything.
- Covers the shared-machine case for the people who actually care about it,
  because those people already run a secret manager.
- Needs the same care `validateBaseURL` gets: the command is executed, so it is
  a trust boundary in `config.yaml`. Run it without a shell, do not interpolate,
  give it a timeout, and never log its output.

This is the dark horse. It gets most of the keyring's value for none of its
dependency and none of its failure modes.

---

## 5. Recommendation

**Do Option A now. Consider Option D as a separate small row. Do not add a
keyring dependency yet.**

The reasoning, in order of weight:

1. The threat model does not support it (§2). Against the dominant threat — any
   process running as the user — a keyring is exactly equivalent to a 0600 file.
   Against the threat the task row names — other users on a shared machine —
   0600 already wins.
2. It can make Athena worse in a way the current code cannot be worse. Today the
   credential path cannot hang. With a keyring it can (§6, `handlePrompt`), on
   precisely the machines where a keyring helps least.
3. The two genuine defects here are file-layer defects. Unchecked OAuth file
   permissions and an uncallable `DeleteAPIKey` are both broken *now*, and a
   keyring fixes neither. Fixing them is smaller than adding the dependency.
4. Nobody has asked. There is no reported user on a shared machine. Option A
   costs a day and closes real holes; Option B costs a dependency, a permanent
   fallback path, and a migration, to close a hole nobody is standing in.

Revisit Option B when a real user on a real shared machine asks, or when Athena
starts storing something a 0600 file genuinely cannot hold.

---

## 6. Dependency verdict

**Verdict: no new dependency today.** If Option B is approved later,
`github.com/zalando/go-keyring` is the right library, adopted with the
guardrails below. Details, so the decision is on evidence:

### The candidate: `github.com/zalando/go-keyring`

- **Version and freshness:** `v0.2.8`, tagged 2026-03-23 (from
  `proxy.golang.org/.../@latest`). Actively maintained, MIT licensed, small — one
  package plus a `secret_service` subpackage.
- **What it pulls in:** two new modules in the graph.
  `github.com/godbus/dbus/v5 v5.2.2` (2025-12-29, BSD-2-Clause, ~81 Go files,
  pure Go) and `github.com/danieljoos/wincred v1.2.3` (Windows-only code, but it
  still enters `go.mod`/`go.sum` and is fetched by `go mod download`).
  `golang.org/x/sys` is required at `v0.27.0`; Athena already has `v0.46.0`
  indirect, so no bump. Net: three new lines in `go.mod`, no cgo on Linux.
- **API surface:** `Set`, `Get`, `Delete`, `DeleteAll` over `(service, user)`.
  `ErrNotFound` is a distinct sentinel. Trivially wrappable.

### What it actually does on Linux

`keyring_unix.go` builds for `(dragonfly && cgo) || (freebsd && cgo) || linux ||
netbsd || openbsd` — the Linux path is pure Go, over D-Bus. It calls
`dbus.SessionBus()` (`secret_service/secret_service.go:51`) and talks to
`org.freedesktop.secrets`.

On this machine, `busctl --user list` shows `org.freedesktop.secrets` owned by
`ksecretd` — KDE's Secret Service implementation. So the owner's own laptop does
have a provider. KDE's shim is also historically the least uniform of the
implementations around prompting and collection aliases, which is worth a manual
test before trusting it.

**Headless / SSH — the recoverable case.** With no `DBUS_SESSION_BUS_ADDRESS`,
`dbus.SessionBus()` fails, or D-Bus autolaunches a bus on which nothing owns
`org.freedesktop.secrets` and the first method call returns
`org.freedesktop.DBus.Error.ServiceUnknown`. Both surface as an ordinary Go
error and fall back cleanly.

**Locked collection — the case that hangs.** `SecretService.Unlock`
(`secret_service.go:106`) delegates to `handlePrompt` (line 187), which does:

```go
signal := <-promptSignal
```

No timeout. No context. If the Secret Service returns a prompt object and
nothing ever displays it — an SSH session with a forwarded bus, a locked
`kwalletd` with `org.kde.secretprompter` unavailable, a desktop session that
died — Athena blocks forever at startup with no output and no way to tell what
happened. That is the concrete way this task makes Athena worse than it is
today. Any adoption must run every keyring call in a goroutine behind a hard
timeout and treat "timed out" as "no keyring", never as "fatal".

**Size limits.** `keyring.go:14-17` documents `ErrSetDataTooBig`: macOS caps the
combination of service, user and password at roughly 3000 bytes; Windows caps
the password at 2560 bytes. `CodexCredentials` is two JWTs plus an account ID.
Serialised as one blob, that can exceed the Windows limit. Store one entry per
field, or keep the ciphertext in a file with only the key in the keyring.

**Testability.** `keyring_mock.go` provides `MockInit()` and
`MockInitWithError(err)`, so the happy path and the failure path are both
testable without a D-Bus session. This is genuinely good and removes the usual
"can't test it in CI" objection.

### The alternative, rejected

`github.com/99designs/keyring` (the aws-vault library) is more capable — it has
`pass` and encrypted-file backends built in. It is also last tagged **v1.2.2 on
2022-12-19**, over three years stale, with nine direct requires including an
unversioned `github.com/godbus/dbus` v0 fork, `dvsekhvalnov/jose2go`,
`gsterjov/go-libsecret`, and `stretchr/testify` in the main module. Wrong trade
for a project with five direct dependencies.

### Maintenance implications if adopted

- Two more modules to watch for CVEs; `godbus/dbus` is the one that matters,
  since it parses messages off a socket.
- A permanent second code path (file fallback) that must be exercised in tests
  forever, because it is the path CI and every SSH user take.
- Platform-specific bug reports Athena cannot reproduce: "it asks for my
  password every start" is a gnome-keyring configuration issue that will arrive
  as an Athena issue.
- macOS is an `exec` of `/usr/bin/security` (`keyring_darwin.go:29`), not a
  library call — a different failure surface from Linux, with its own escaping
  concerns (the library ships `internal/shellescape` for exactly this).

---

## 7. Decisions only the owner can make

1. **Is the shared machine real?** Is there an actual multi-user box in the
   picture, or is "shared machines are not" a hypothetical? This determines
   whether L-03 has any value at all beyond §2.3's backup-hygiene argument.
2. **What does "no plaintext new default until this ships" forbid?** Since the
   row was written, `xai-oauth.json` shipped as a new plaintext secret file
   (added in `4927e04`). Either the clause means "no new *kind* of secret
   storage" and the row is intact, or it has already been violated and the
   acceptance line needs rewording. It currently blocks every future OAuth
   provider, which is probably not the intent.
3. **If Option B ever ships, what are the fallback semantics?** Keyring holds
   one key and the file another — which wins? Does a successful keyring write
   delete the file copy? (Suggested: keyring wins, the file copy is deleted on
   successful migration and never written again — but that makes the machine's
   secrets unreachable over SSH, which is the decision.)
4. **May a keyring prompt appear at startup, or only on first use after an
   explicit user action?** (Suggested: never at startup.)
5. **Is Athena Linux-only in practice?** If yes, the Windows branch is dead
   weight and Option D covers the same users for a fraction of the cost.
6. **May Athena delete the legacy `~/.config/second-brain/<secret file>` copy
   after migration?** The current rule is "leave the recovery copy". For a
   credential file that rule leaves a second plaintext key behind (§1.5). This
   is worth deciding independently of the keyring question.
7. **Does `/forget <provider>` (A2) get its own `tasks.md` row, or fold into
   L-03?** It is a user-visible feature, not a refactor, and it is the only item
   in Option A that changes the interface.

---

## 8. Implementation sketch (only if Option B is approved)

Ordering matters: Option A first regardless, because B inherits its bugs.

**Step 1 — the seam, no dependency.**
`internal/config/secretstore.go`:

```go
type SecretStore interface {
    Get(name string) (string, error) // returns "" and nil when absent
    Set(name, value string) error
    Delete(name string) error
}
```

Move today's file logic behind `fileStore` — it is already the whole of
`saveLocked` plus the map. `CredentialStore` keeps its public methods and
delegates. `internal/ai` stops calling `writeOwnerOnlyJSON` directly and takes a
`SecretStore` instead, so `CodexOAuth` and `XAIOAuth` never learn where their
tokens live. `BuildProvider` needs no change at all: it already only sees
`CredentialStore`.

**Step 2 — the keyring implementation.**
`keyringStore` wraps `zalando/go-keyring` with service `"athena"` and the
credential name as user. Every call goes through a helper that runs it in a
goroutine and selects against a two-second timer — this is not optional, see §6.
`keyring.ErrNotFound` maps to `("", nil)`. Check value length against the
documented 2560-byte limit before `Set` and return a clear error rather than the
library's.

**Step 3 — selection and probing.**
New config field `secret_storage: auto | keyring | file`, default `auto`. Under
`auto`, probe once at startup: a `Get` of a known-absent probe name behind the
same timeout. Any error, including the timeout, selects `fileStore` and prints
one line to stderr saying which store is in use and why. `keyring` fails loudly
if unavailable — someone who set it explicitly wants to know. `file` never
probes, which is what a headless install sets.

**Step 4 — migration, once, forward only.**
On the first successful keyring `Set` for a name that also exists in a file,
delete the file entry. Never maintain both. Warn about the legacy
`second-brain` copy at the same moment (decision 6).

**Step 5 — tests.**
`keyring.MockInit()` covers the happy path with no D-Bus. `MockInitWithError`
covers the fallback. One test must assert that a store whose calls block returns
the file store within the timeout instead of hanging — that is the regression
this whole section exists to prevent.

**Step 6 — documentation, same commit.**
`docs/configuration/README.md`: "Local files" and "Where credentials actually
live" both change. `docs/README.md` plan index. The `L-03` row in `tasks.md`.

### Not in scope

Key rotation, server-side OAuth revocation, encrypting `athena.db`, and the
`~/.codex/auth.json` file Athena reads but does not own.
