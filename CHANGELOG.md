# Changelog

All notable changes to this project are documented here.
The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [0.2.0] - 2026-09-19

### Added (features)

- **Background operations.** Every Redis call now runs on a goroutine and reports
  back through the UI queue. A large listing no longer freezes the application:
  the status line shows what is running and how many keys were scanned, and `Esc`
  cancels it. Only one operation runs at a time, so the connection is never used
  concurrently.
- **Value decoding and hex view.** The details pane pretty prints JSON and XML and
  unwraps gzip, zlib and base64, including nested combinations
  (`base64 -> gzip -> json`). `Ctrl+V` cycles decoded / raw / hex, and binary
  values start in hex. Decoding never rewrites what is stored, and an edit that
  would break a previously valid JSON document is refused. `Ctrl+F` re-indents a
  document in the editor.
- **Two browser panes.** `F9` shows a second pane, `Tab` switches, `F5` copies and
  `F6` moves the selection into the other pane's folder. `Ctrl+D` connects a pane
  to another database. Transfers preserve type and TTL by using `COPY` on the same
  server, `DUMP`/`RESTORE` across connections and `RENAME`/`RENAMENX` for a move
  inside one database.
- **Folder analysis.** `Ctrl+A` walks a subtree and reports key and folder counts,
  memory use (`MEMORY USAGE`, estimated when unsupported), the type breakdown, how
  many keys expire, and the largest child prefixes and keys. The numbers stay in
  the details pane of that folder, which previously showed nothing useful.
- **Edit in `$EDITOR`.** `Ctrl+O` suspends the UI, opens the value in
  `$VISUAL`/`$EDITOR`/`vi` (or `-editor`) and saves the result if it changed.
- **Non-interactive commands.** `ls`, `tree`, `cat`, `set`, `rm` and `stat`, with
  `-json` output, reusing the same model as the browser. `set` writes to an
  existing key however the path is spelled and creates new keys without the
  virtual leading slash.
- `Model` gained `Stats`, `CopyKey`, `CopyDir`, `MoveKey`, `MoveDir`, `SortNodes`
  and progress reporting; every method now takes a `context.Context`.
- New package `pkg/format` for encoding detection, pretty printing and hex dumps.

### Fixed (data-integrity)

- **Keys were listed under a made-up path.** Any key that does not start with `/`
  (`session:42`, `user:1:name`, …) was shown as `/session:42` and every operation
  used that fabricated name: editing created a *second* key, deleting silently did
  nothing. Nodes now carry the real Redis key and all mutations use it.
- **SCAN patterns were not escaped.** A key containing `*`, `?`, `[` or `]` made
  the `MATCH` pattern select unrelated keys — a recursive delete of `cache[1]/`
  could remove `cache1/` and `cacheX/`. Prefixes are now escaped, and every key
  returned by the server is re-checked against the prefix.
- **Renaming a folder destroyed data.** The old implementation did `GET` + `SET`
  per key, which replaced hashes, lists, sets and streams with an empty string and
  dropped every TTL. It now uses `RENAME`, which preserves type, value and TTL.
- **Editing a key dropped its TTL.** Values are written with `SET ... KEEPTTL`.
- **The editor could overwrite non-string keys.** Opening a hash showed an empty
  value and saving replaced the hash with a string. Non-string keys are now
  labelled with their type and the editor refuses to open them; the model rejects
  a `Set` on a non-string key as well.
- **The editor truncated large values.** It used the value cached by the listing.
  It now re-reads the full value before opening, and refuses to edit values that
  are not valid UTF-8 so binary payloads cannot be corrupted.
- **A failed listing left the previous folder's entries on screen** under the new
  folder's title.

### Fixed (usability)

- `-debug` can be used without `=true`; `flag.Var` needs `IsBoolFlag`, so the flag
  previously failed with "flag needs an argument".
- An invalid `-db`/`-port` is reported instead of silently falling back to `0`.
- An empty config file is no longer reported as a broken config.
- Search jumped to the wrong entry: the dialog sorted by display name while the
  list sorted by an internal key. Both now share one ordering.
- Values containing tview markup (`[red]…`) were rendered as colours; values and
  key names are escaped, and control characters are shown as `\xNN`.
- `[Enter]`, `[Backspace]` and `[Del]` were swallowed by the tag parser and never
  appeared in the hint line, which is now split over two rows so it fits.
- The error dialog was 8×3 characters and unreadable; dialogs were resized and the
  error page no longer collides with other modals.
- Recursive delete no longer sends one huge `DEL`; keys are deleted in batches.
- `DelDir` also removes the folder's own key (`a` next to `a/b`).
- `RenameDir` refuses to move a folder into itself.

### Added

- Details pane shows the Redis type, the value size, the TTL and a truncation
  notice for long values.
- `Ctrl+R` reloads the current folder.
- `-version` and `-config` flags, plus the `REDIS_WALKER_CONFIG` environment
  variable; the version is injected at build time.
- Listings are capped (100 000 keys) and the cap is shown as `(truncated)` in the
  title instead of growing without bound.
- `Model.Close`, `Model.Resolve` and an `Options` struct; `NewWithClient` allows
  embedding the model with any `redis.UniversalClient`.
- Unit tests for the path mapping, the model (miniredis), the controller (real
  tview on a simulation screen), the config resolution and the CLI.
- A hermetic end-to-end suite (`make e2e`) that drives the whole TUI against real
  Redis servers in Docker, including an authenticated one.
- `Makefile`, `docs/FEATURES.md`, this changelog and a rewritten `README.md`.

### Changed

- Keys created by redis-walker are written relative to the folder being shown and
  no longer get a leading `/` injected: creating `db` in the root writes `db`, not
  `/db`. Existing keys that start with `/` keep working unchanged.
- Listings load values through two pipelines instead of one round trip per key,
  and only fetch an 8 KiB preview.
- The per-operation timeout is 60 seconds instead of 10, because a long operation
  can now be cancelled by hand.
- The key hints are rendered from a table through `tview.Escape` and split over
  two rows, so no key name can be swallowed by the tag parser again.
- `make build` only overrides the compiled-in version when a git tag exists,
  instead of stamping a commit hash into `-version`.

### Removed

- Unused API: `Model.Addr`, `Model.Databases`, `Model.TTLOf`,
  `format.CompactJSON`, `View.Header`, `View.Active`, `Controller.Busy`,
  `Controller.Down` and `Controller.ValueMode`.
- Duplicated logic: the listing order lives in `model.SortNodes` and is used by
  both the browser and the command line, the "include the folder's own key" step
  is one helper, the input dialogs share `View.NewPrompt`, the delete dialog is
  built from `View.NewConfirm`, and both test suites drive the UI through the new
  `internal/uitest` package.
