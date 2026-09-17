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
Folders can be renamed but a single key cannot be moved or copied. `RENAME` and
`COPY` are one command each and reuse the rename dialog.

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
