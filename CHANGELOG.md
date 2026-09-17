# Changelog

All notable changes to this project are documented here.
The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

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
  appeared in the hint line.
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
