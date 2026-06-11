# aztui

A [k9s](https://k9scli.io/)-style terminal UI for Azure. Browse your Azure
resources with vi keys, drill into them, and act on them — without leaving the
terminal and without waiting for the portal to load.

```
 ████  █████ █████ █   █ ███
█    █    █    █   █   █  █
██████   █     █   █   █  █
█    █  █      █   █   █  █
█    █ █████   █    ███  ███
```

## What it does today

- **Launcher home screen** — module tiles in a five-column grid (icon +
  live resource count, discovered tenant-wide via Azure Resource Graph),
  sorted by how many resources you actually have; modules with zero
  resources hide automatically (still reachable via `:`). Recently opened
  resources and a session-changes feed sit underneath. Tiles and recents
  are keyboard-driven and mouse-clickable.
- **Persistent header** — the logo and a full-width keys panel stay fixed at
  the top of every screen, k9s-style; the keys shown follow whatever view
  you're in. While background work runs (scaling a database, purging a
  queue, syncing stores), an **activity panel** appears top-right with an
  animated progress sweep and elapsed time per operation.
- **Toast notifications** — saves, completions, warnings, and errors pop up
  as color-coded toasts in the top-right corner, so you never miss a
  "saved" or a background scale finishing — wherever you are in the app.
- **App Configuration** — browse, filter, and inspect key-values; edit values
  in your `$EDITOR`; create, delete, and lock/unlock settings. Writes are
  ETag-guarded, so concurrent edits fail loudly instead of clobbering.
- **Service Bus** — the Service Bus Explorer feature set, with vi keys:
  queues/topics/subscriptions with live message counts, non-destructive
  message peeking (active + dead-letter), full message detail, compose and
  send messages as JSON in your `$EDITOR`, clone-and-resend, resubmit from
  the DLQ, purge, create/delete entities — plus a **live-tail** mode that
  follows new messages as they arrive, like `tail -f` for a queue.
- **App Configuration diff & sync** — press `D` in a store to compare it
  against any other store: see what differs, what only exists on one side,
  and sync selected entries in either direction with a confirmation first.
  Built for catching dev/staging/prod drift.
- **Key Vault** — browse secrets, view with the value masked until you ask
  for it, write new versions in your `$EDITOR`, create and soft-delete
  secrets.
- **SQL Database** — browse servers and their databases (SKU, tier,
  capacity, size, status) and scale a database with a vi-key slider that
  covers both purchasing models: DTU (Basic/Standard/Premium service
  objectives) and vCore (General Purpose, Serverless, Business Critical,
  Hyperscale). A **CPU line chart** (average + max from Azure Monitor,
  1h/24h/7d) sits directly under the slider, so the evidence for a resize
  is on the same screen as the control. Scaling runs in the background and
  flashes when it lands.
- **Virtual Machines** — a per-VM dashboard with live power state, size,
  OS, and resolved private/public IPs. Start, restart, stop (deallocate) or
  power off in the background; manage **VM extensions** (install via an
  editor template, uninstall) and **VM applications** — browse every
  application in your compute galleries (filtered to the VM's OS), drill
  into versions, and install with a keypress; and **SSH into Linux VMs**
  with one key — pick
  private or public IP, enter a user, choose default keys, a key file, or
  password auth (ssh itself prompts; aztui never touches your password).
  The last user and key path are remembered.
- **Storage** — browse containers and blobs hierarchically, download blobs
  to `~/Downloads`, and peek storage queue messages (base64 + JSON decoded).
- **Session changes feed** — every mutation you make through aztui (edits,
  deletes, purges, syncs, scales) is listed on the home screen with a
  timestamp, so you can see at a glance what you touched this session.
- **Read-only mode** — `aztui --read-only` (or `:ro` at runtime) disables
  every mutating action so production can be browsed without risk. A
  `[READ-ONLY]` badge shows in the status bar while active.

## Install

```sh
brew install GoosieZA/tap/aztui
```

Or with Go:

```sh
go install github.com/GoosieZA/aztui/cmd/aztui@latest
```

Or from a checkout:

```sh
make build   # produces ./bin/aztui
```

## Auth

aztui authenticates with a credential chain: **Azure CLI first** (if you've
run `az login`, it just works), falling back to an **interactive browser**
sign-in whose tokens are cached persistently. No connection strings, no
config files.

The identity needs the usual data-plane roles on the resources you open
(e.g. *App Configuration Data Owner*, *Azure Service Bus Data Owner*) and
read access for Azure Resource Graph discovery.

## Keys

Vi everywhere. `?` shows the keys for whatever view you're in.

### Global

| Key | Action |
| --- | --- |
| `j` / `k` | move down / up |
| `g` / `G` | top / bottom |
| `ctrl+d` / `ctrl+u` | half page down / up |
| `/` | filter the current table |
| `:` | command line — `:home`, `:appconfig` (`:ac`), `:servicebus` (`:sb`), `:q` |
| `enter` | select / drill in |
| `y` | yank — copy the value under the cursor to the clipboard |
| `esc` | back |
| `?` | help |
| `ctrl+c` | quit |

`y` works wherever there's a value: App Configuration settings (lists,
details, cross-store view), Key Vault secrets, Service Bus message bodies,
storage queue messages, and resource IDs in resource lists.

On the home screen: `h`/`l` choose a module tile, `j`/`k` move into the
recents list, `1`-`9` open a recent directly, and tiles/recents respond to
mouse clicks.

### App Configuration

| Key | Action |
| --- | --- |
| `enter` | view setting detail |
| `e` | edit value in `$EDITOR` |
| `space` | select / deselect for bulk edit |
| `ctrl+a` | select all visible |
| `E` | bulk edit selection as JSON (portal-style advanced edit) |
| `D` | diff & sync against another store |
| `x` | this key across every store — one row per environment; `e` edits the value in any store right there (creating it where missing) |
| `n` | new setting (`$EDITOR`, YAML template) |
| `d` | delete setting |
| `L` | lock / unlock (read-only) |
| `R` | refresh |

In the diff view: `space`/`ctrl+a` select rows, `>` syncs the selection
source → target, `<` the other way, `i` shows identical entries, `enter`
compares values side by side. Syncing makes the receiving store match the
giving one for the selected rows — including deleting entries that only
exist on the receiving side — after an explicit confirmation.

Bulk edit opens the selected settings as a JSON array in your `$EDITOR` —
change values to update, add entries to create, remove entries to delete.
With **nothing selected, `E` is bulk add**: an empty array skeleton where
every entry you write gets created. A confirmation summarises the plan
before anything is written, and every update/delete is ETag-guarded
against concurrent changes.

**Key Vault references** render as `🔑 → vault/secret` instead of raw
JSON; opening one resolves the secret from the vault and shows either its
value or exactly which vault you lack access to. A 403 listing the store
itself gets a clear explanation too: seeing a store in Azure (ARM access)
and reading its data are separate grants — you need
**App Configuration Data Reader** for the latter.

### Service Bus

| Key | Action |
| --- | --- |
| `enter` | peek queue / open topic / view message |
| `x` | peek the dead-letter queue |
| `T` / `t` | live-tail (entity lists / current message view) |
| `m` | peek more messages |
| `s` | send a message (`$EDITOR`, JSON template) |
| `c` | clone selected message, edit, resend |
| `r` | resubmit selected DLQ message to the main entity |
| `n` | new queue / topic / subscription |
| `D` | delete entity |
| `P` | purge messages |
| `R` | refresh |

While tailing: `p` pauses/resumes, `f` re-enables follow after scrolling.
The tail is non-destructive — it peeks ahead of the entity's current end
sequence, so consumers are unaffected.

### Key Vault

| Key | Action |
| --- | --- |
| `enter` | view secret (value masked) |
| `v` | reveal / mask the value |
| `e` | write a new version in `$EDITOR` |
| `n` | new secret |
| `d` | soft-delete secret |
| `R` | refresh |

### SQL Database

| Key | Action |
| --- | --- |
| `enter` / `s` | scale view: slider + CPU line graph (`1`/`2`/`3` = 1h/24h/7d range) |
| `h` / `l` | smaller / larger size on the slider |
| `g` / `G` | smallest / largest |
| `t` / `T` | cycle tier (Basic/Standard/Premium or GP/Serverless/BC/Hyperscale) |
| `m` | switch DTU ↔ vCore purchasing model |
| `enter` | apply (after an explicit confirmation) |
| `R` | refresh |

### Virtual Machines

| Key | Action |
| --- | --- |
| `s` | SSH (Linux) — address, user, and auth form, then a real ssh session |
| `p` | start |
| `r` | restart |
| `x` | stop (deallocate — billing stops) |
| `X` | power off (keeps allocation — still billed) |
| `e` | extensions: `n` install via `$EDITOR` template, `d` uninstall |
| `a` | applications: `n` browse galleries & install, `d` remove |
| `R` | refresh |

### Storage

| Key | Action |
| --- | --- |
| `enter` | browse container / open folder / **preview blob** / peek queue |
| `s` | download selected blob to `~/Downloads` |
| `R` | refresh |

Blob previews render right in the terminal: text and JSON (pretty-printed,
first 256 KB) directly, and **images as truecolor half-block pixels** — no
terminal graphics protocol needed, works everywhere.

Messages are composed as JSON: `body` may be any JSON value — a string is
sent verbatim, anything else is sent as its JSON encoding.

> **Note on DLQ resubmit:** Service Bus has no "receive by sequence number",
> so resubmit receives and scans the DLQ under peek-lock (up to 1000
> messages), re-sends the target, completes it, and abandons the rest back
> onto the DLQ.

## Architecture

aztui is modular by design — each resource type is a self-contained module:

```
cmd/aztui/            entrypoint; imports modules for side effects
internal/app/         root model: view stack, command line, status bar, home
internal/ui/          shared components: vi-key table, $EDITOR, confirm, help
internal/auth/        az cli → browser credential chain
internal/azure/       Resource Graph discovery
internal/config/      recents persistence (~/.config/aztui)
internal/modules/     the Module interface + registry
  appconfig/          App Configuration (incl. diff/sync, bulk edit, cross-store view)
  keyvault/           Key Vault secrets
  servicebus/         Service Bus (incl. live-tail)
  sql/                SQL databases + scale slider
  storage/            blobs (incl. terminal previews) + queues
  vm/                 Virtual Machines (power, extensions, apps, SSH)
```

### Writing a module

Implement the `modules.Module` interface and register it from `init()`:

```go
type Module interface {
    ID() string              // command name, e.g. "keyvault"
    Aliases() []string       // e.g. {"kv"}
    Title() string           // "Key Vault"
    Icon() string            // glyph for the home-screen tile, e.g. "🔑"
    ResourceTypes() []string // lowercase ARM types it owns
    Open(ctx Context, res azure.Resource) (tea.Model, error)
}
```

Add a blank import in `cmd/aztui/main.go` and you're done: your module gets
a tile on the home screen, your resources are discovered, your command works
from `:`, and your view gets the navigation stack, status bar, filtering,
and help overlay for free.

## Roadmap

- Container Apps / App Service modules (settings, restart, log streaming)
- Log Analytics module (KQL query runner)
- Key Vault: keys, certificates, deleted-secret recovery
- App Configuration: revisions, snapshots, feature flags as first-class views
- Storage: blob upload/delete, queue send
- Batch DLQ resubmit, scheduled messages, copy-to-clipboard
- Themes / config file for keymaps

PRs welcome.

## License

MIT — see [LICENSE](LICENSE).
