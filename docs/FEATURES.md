# Feature backlog

Proposals that came out of the code review, ordered by how much they change the
usefulness of the tool per unit of work. Nothing here is implemented yet.

## Tier 1 — biggest impact

### 1. Configurable key separator (`-separator`, default `/`)
Real-world Redis keyspaces almost always use `:` (`user:1:profile`), not `/`.
Today such a database shows up as one flat list of thousands of root entries,
which is exactly the situation the tool exists to fix. The separator is already
funnelled through `pkg/model/path.go`, so this is mostly a constant plus a flag,
plus a config field. Worth supporting several separators at once (`/` and `:`).

### 2. View (and edit) non-string types
Hash, list, set, zset and stream keys are currently listed and labelled but
cannot be inspected at all. Presenting a hash as a folder of fields, a list as an
indexed folder and a zset as `member = score` fits the existing tree UI and would
remove the single largest functional gap. Read-only support first, editing after.

### 3. Server-side filtering and lazy listing
`Ls` scans the whole keyspace for the root listing and caps the result at 100 000
keys. A filter box that feeds the SCAN `MATCH` pattern, plus incremental loading
as the user scrolls, removes the cap and makes large databases usable. This also
enables recursive search (see 7).

### 4. Read-only mode (`-read-only`)
A single flag that disables create/edit/delete/rename and says so in the frame
title. Cheap to implement, and the natural default for production endpoints.
Pairs well with a `confirm_writes` config option.

## Tier 2 — regular gaps

### 5. TTL management
The details pane shows the TTL; there is no way to set, extend or clear it.
A small dialog (`Ctrl+T`) mapping to `EXPIRE` / `PERSIST` closes the loop.

### 6. Single-key operations: copy, move, duplicate
**Implemented** as `F5`/`F6` between the two panes, for single keys and whole
folders. Copying a key onto a new name inside the *same* folder is still missing,
which is what "duplicate" would add.

### 7. Recursive search
Search matches only the entries of the current folder. A recursive variant
(`Ctrl+F`) that scans below the current prefix and shows full paths would make
"where is this key" answerable.

### 8. Multi-select and bulk actions
Tag entries with `Insert` (mc-style) and apply delete/move/export to the whole
selection.

### 9. Connection URL, TLS, Unix sockets, Sentinel and Cluster
`-url redis://user:pass@host:6379/2` and `rediss://` for TLS. The model already
holds a `redis.UniversalClient`, so Sentinel and Cluster need little more than
the option plumbing.

### 10. Credentials that do not land in `ps`
`-password` is visible to every user on the host. Add `-password-file`,
`REDIS_PASSWORD` support and an interactive prompt.

## Tier 3 — quality of life

- **Export / import**: dump a subtree to JSON (with type and TTL) and restore it;
  gives users a backup before a recursive delete.
- **Undo for delete**: move keys to a `__trash__/<timestamp>/` prefix instead of
  deleting immediately, with a restore command.
- **Sorting and size column**: sort by name, size (`MEMORY USAGE`) or TTL.
- **Server info pane**: `INFO` output, database picker (`Ctrl+D`), key counts.
- **Live refresh**: subscribe to keyspace notifications and refresh automatically.
- **Bookmarks and jump history**, remembered between sessions.
- **Themes and configurable key bindings** in the config file.
- **Mouse support**: `Application.EnableMouse(true)` plus click-to-select.
- **Raw command console** for the commands the UI does not cover.

## Engineering

- **CI**: GitHub Actions running `make vet test race` on every push and `make e2e`
  on pull requests; a coverage badge.
- **Releases**: goreleaser for Linux/macOS binaries, a `.deb`, and a Homebrew tap;
  the version is already injected through `pkg/view.Version`.
- **Fuzzing**: `splitChild` and the path helpers are pure functions over arbitrary
  key names and are a natural fit for `go test -fuzz`.
- **Benchmarks** for `Ls` against a keyspace of 1M keys, to keep the pipelined
  metadata loading honest.

---

# Round 2 — further ideas

A second pass, written after the hardening round. Each entry names the place in
the code it would hook into.

> **Implemented in 0.2.0:** A1 (background operations), A2 (folder statistics),
> B1 (format-aware viewer), B2 (`$EDITOR`), B3 (hex viewer), D1 (prefix memory
> report), D2 (two panes, including `Ctrl+D` for another database) and E1
> (headless commands), plus tier-2 item 6 of the first round. They are kept below
> for the rationale; everything else is still open.
>
> The largest remaining gaps are the configurable key separator (tier 1, item 1)
> and viewing non-string types (tier 1, item 2).

## A. Defects that are really missing features

### A1. Asynchronous operations with progress and cancel
Every `c.model.*` call in `pkg/controller/controller.go` runs inline on the tview
event loop. A root listing of a large keyspace blocks for as long as the scan
takes (up to the 10 s operation timeout) and the whole UI is frozen meanwhile —
including `Ctrl+Q`, which is handled on the same loop. Moving model calls onto a
goroutine that reports back through `App.QueueUpdateDraw`, with a spinner in the
list title, a key count that ticks up and `Esc` to cancel the context, is the
single biggest perceived-quality change available. It also makes the 100 000-key
listing cap unnecessary for interactive use.

### A2. Folder statistics
`fillDetails` prints three lines for a folder and stops. Selecting a folder could
show: number of keys, number of sub-folders, total value bytes, a breakdown by
type, how many keys carry a TTL and the soonest expiry. A cheap version reuses
the listing already in memory; the full version needs A1 so the aggregate scan
runs in the background.

## B. Working with values

### B1. Format-aware viewer and editor
Most string values in a real Redis are JSON. Detecting the encoding and offering
a decoded view — pretty-printed JSON/XML, gzip and base64 decoding, msgpack —
with a `Tab` toggle between raw and decoded, would change the day-to-day feel of
the tool more than any new command. Two rules make it safe: never write back the
decoded form unless the user explicitly asks, and validate JSON before saving so
a broken document cannot be stored by accident.

### B2. Open in `$EDITOR`
Suspend the application (`App.Suspend`), write the value to a temp file, run the
user's editor, read it back and `Set` it. Small amount of code, and it gives the
value editor everything vim/emacs users expect. `Ctrl+Shift+E` next to `Ctrl+E`.

### B3. Hex viewer for binary values
Values that are not valid UTF-8 are deliberately not editable, but they are also
not *viewable* — the details pane shows escaped bytes. A proper hex/ASCII dump
pane would make binary payloads (protobuf, compressed blobs) inspectable without
risking corruption.

### B4. Watch a key
Poll the selected key on an interval and redraw the details pane on change, so a
counter, a lock or a rate-limit bucket can be observed live. Natural companion to
the keyspace-notification refresh already in the backlog.

## C. Finding things

### C1. Type-to-filter
The current search is a modal dialog with autocomplete and a jump. An inline
incremental filter that narrows the visible list as characters are typed (and
restores on `Esc`) is faster for the common case and can reuse `c.ordered`.

### C2. Background key index and fuzzy jump
`Ctrl+J` resolves an exact path through `Model.Resolve`. With a key index built
in the background (bounded, refreshed on demand) the same dialog could do fuzzy
matching over the whole keyspace, ranked by frecency of previous visits, which is
what people actually want when they type a half-remembered key name.

### C3. Virtual views
Folders are derived from key names; the same mechanism can present derived views:
"expiring within an hour", "keys larger than 1 MB", "keys by type". Useful for
cache debugging and it costs no new UI.

## D. Operations

### D1. Prefix memory report
Aggregate `MEMORY USAGE` per folder and show the top prefixes by size. "What is
filling up this Redis" is the question operators actually arrive with, and today
the answer needs a separate tool. Works well as a modal report over the current
subtree.

### D2. Two-pane mode
The layout is already a list plus a panel. A second browser pane over a second
connection — another database, another server — with `F5` copy and `F6` move
between panes turns the tool into a Midnight-Commander for Redis. This is the
most distinctive feature on the list and subsumes the single-key copy/move idea
from the first round.

### D3. Diff
Compare two keys, or the same key across two endpoints (staging vs production),
side by side. Pairs with D2 and with the format-aware viewer: a JSON-aware diff
is far more readable than a byte diff.

### D4. Command log and dry run
A toggleable pane showing the Redis commands the UI issues, plus `-dry-run` that
logs writes without executing them and an optional audit file. It makes the tool
trustworthy against production and doubles as documentation of what each action
does.

### D5. Production guards
Beyond a blanket read-only mode: `protected_prefixes` in the config that require
typing the key name to delete, a red frame and a banner for endpoints marked as
production, automatic read-only when `INFO replication` reports a replica, and
graceful degradation when the ACL user gets `NOPERM` — hide the actions that
cannot succeed instead of showing an error after the fact.

### D6. Pub/Sub and stream tailing
Subscribe to a channel pattern and show messages live; tail a stream with
`XRANGE`/`XREAD` and show consumer groups and lag. Both are debugging tasks that
currently force a switch to `redis-cli`.

### D7. Server-friendly scanning
`-scan-count` and `-scan-sleep` to bound the load a recursive scan puts on a busy
production instance, and a warning when a listing would scan more than N keys.

## E. Beyond the TUI

### E1. Headless subcommands
`redis-walker ls /app/cfg`, `cat`, `set`, `rm`, `tree`, with `--json` output, all
reusing `pkg/model`. The model is already a clean library with its own tests, so
this is mostly flag plumbing — and it makes the tool usable from scripts and CI,
not only interactively.

### E2. Connection profiles
Named endpoints in the config (`profiles: { prod: {...}, staging: {...} }`),
selected with `-profile prod` or a picker bound to `Ctrl+O`, including per-profile
exclude rules and a per-profile read-only flag. Prerequisite for D2 and D3.

### E3. Session resume
Remember the last endpoint and folder per profile and return there on the next
start.
