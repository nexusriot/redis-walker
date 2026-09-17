# redis-walker

A terminal **Redis browser** that presents the keyspace as a directory tree: navigate,
view, edit, create, rename and delete keys with a two-pane, file-manager-style UI.

Built with [tview](https://github.com/rivo/tview) and [go-redis](https://github.com/redis/go-redis).

---

## Features

- Browse Redis keys as if they were files and folders (`app/cfg/db` → `/app/cfg/db`)
- Two-pane layout: the tree on the left, key details (type, size, TTL, value) on the right
- Create, edit and delete keys; rename a whole folder (prefix) in one step
- Multiline editor for large values
- Jump to any key or folder (`Ctrl+J`), search within the current folder (`/`)
- Non-string keys (hash, list, set, zset, stream) are listed and labelled, and are
  protected from being overwritten by the editor
- Optional authentication (ACL username + password, or classic `requirepass`)
- Optional exclusion of noisy key prefixes
- Optional config file, overridden by CLI flags

---

## Installation

```bash
go install github.com/nexusriot/redis-walker/cmd/redis-walker@latest
```

Or build from source:

```bash
git clone https://github.com/nexusriot/redis-walker
cd redis-walker
make build          # -> build/redis-walker
```

---

## Usage

```bash
redis-walker [flags]
```

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-host` | `127.0.0.1` | Redis host |
| `-port` | `6379` | Redis port |
| `-db` | `0` | Redis database index |
| `-username` | – | Redis ACL username (optional) |
| `-password` | – | Redis password (optional) |
| `-exclude-prefixes` | – | Comma-separated key prefixes to hide |
| `-config` | `/etc/redis-walker/config.json` | Config file path |
| `-debug` | `false` | Debug logging (`-debug` or `-debug=false`) |
| `-version` | | Print the version and exit |

Invalid values are rejected at start-up rather than silently ignored: `-db abc`
and `-port redis` are errors, not a fallback to database `0`.

### Examples

```bash
# password-protected server
redis-walker -host 127.0.0.1 -password "secret"

# ACL user, database 3, hiding telemetry keys
redis-walker -host 10.0.0.5 -username walker -password supersecret -db 3 \
             -exclude-prefixes "/pcp:,/debug:,/metrics:"
```

---

## Key bindings

| Action | Key |
|--------|-----|
| Move up/down | **↑ / ↓** |
| Open folder | **Enter** |
| Up to the parent folder | **Backspace** |
| Create key or folder | **Ctrl+N** |
| Edit value / rename folder | **Ctrl+E** |
| Delete (recursive for folders) | **Del** |
| Search in the current folder | **/** or **Ctrl+S** |
| Jump to a key or folder | **Ctrl+J** |
| Reload the current folder | **Ctrl+R** |
| Hotkey help | **F1** or **?** |
| Quit | **Ctrl+Q** |

In the value editor: **Ctrl+S** saves, **Esc** discards. `Ctrl+Q` quits the whole
application from anywhere, including the editor — unsaved changes are lost.

---

## How keys are mapped to the tree

The Redis keyspace is flat; redis-walker splits key names on `/` and shows the
segments as folders. The leading `/` of a displayed path is virtual:

| Redis key | Shown as | Belongs to folder |
|-----------|----------|-------------------|
| `session:42` | `/session:42` | `/` (root) |
| `app/cfg/db` | `/app/cfg/db` | `/app/cfg` |
| `/legacy/key` | `/legacy/key` | `/legacy` |

Every operation uses the **real** key, so keys written by other applications
(`session:42`, `user:1:name`, …) are edited and deleted exactly as they are —
redis-walker never invents a leading slash. Keys created with redis-walker are
written under the current folder: creating `db` inside `/app/cfg` writes the key
`app/cfg/db`.

Notes and limits:

- A name can be both a key and a folder (`app` and `app/cfg`); both are listed.
- An empty folder is kept visible by a placeholder key `<folder>/.dir`, which is
  hidden from the listing. It is removed together with the folder.
- Key names containing glob characters (`*`, `?`, `[`, `]`) are handled literally.
- Only `string` values can be viewed and edited. Other types are listed with their
  type in the list and the details pane, and the editor refuses to open them so
  that a hash or list can never be replaced by a string.
- Values that are not valid UTF-8 are shown escaped and cannot be edited, to avoid
  corrupting binary payloads.
- Editing a key keeps its TTL (`SET ... KEEPTTL`); renaming a folder uses `RENAME`,
  so types and TTLs survive the move.
- A listing is capped at 100 000 keys; the title shows `(truncated)` when the cap
  was hit. Values longer than 8 KiB are previewed in the details pane, while the
  editor always loads the full value.

---

## Configuration file

Path: `/etc/redis-walker/config.json` (override with `-config` or the
`REDIS_WALKER_CONFIG` environment variable). The file is optional; a missing or
empty file is not an error, and CLI flags override every value in it.

```json
{
  "host": "127.0.0.1",
  "port": "6379",
  "db": 0,
  "debug": false,
  "username": "walker",
  "password": "supersecret",
  "exclude_prefixes": ["/pcp:", "/metrics:"]
}
```

> **Security note:** the password is stored in plaintext. Restrict the file
> (`chmod 600 /etc/redis-walker/config.json`) or pass credentials as flags from a
> wrapper. redis-walker never logs the password itself.

---

## Authentication

**Classic `requirepass`:**

```conf
requirepass your-secret-password
```

```bash
redis-walker -password "your-secret-password"
```

**ACL user:**

```redis
ACL SETUSER walker on >supersecret allkeys +@all
```

```bash
redis-walker -username walker -password supersecret
```

With neither flag set, redis-walker connects without authentication. A wrong or
missing password fails immediately at start-up with the Redis error message.

The ACL user needs `SCAN`, `TYPE`, `TTL`, `STRLEN`, `GETRANGE`, `GET`, `SET`,
`EXISTS`, `DEL` and `RENAME`.

---

## Excluding noisy prefixes

```bash
redis-walker -exclude-prefixes "/pcp:,/pcp:context.name:,/values:"
```

```json
"exclude_prefixes": ["/pcp:", "/metrics:"]
```

Excluded keys are filtered out during the scan, so they never reach the UI and are
never deleted by a recursive folder delete.

---

## Development

```bash
make build     # build build/redis-walker with the version stamped in
make test      # unit tests (no Redis needed - miniredis is embedded)
make race      # unit tests with the race detector
make cover     # coverage summary
make vet fmt   # static checks / formatting
make e2e       # hermetic end-to-end suite in Docker (see below)
```

### Tests

* **Unit tests** cover the path/key mapping, the Redis model (against an in-process
  [miniredis](https://github.com/alicebob/miniredis)) and the controller. The
  controller tests run the real tview application on a `tcell` simulation screen,
  so listing, navigation, dialogs and key bindings are exercised as in a terminal.
* **End-to-end tests** (`test/e2e`, build tag `e2e`) run the complete application
  against real Redis servers in Docker and drive it purely through injected key
  presses, asserting both on the rendered screen and on the server state.

```bash
make e2e
# equivalently:
docker compose -f test/e2e/docker-compose.yml up --build \
    --abort-on-container-exit --exit-code-from tests
```

The compose stack starts two throw-away Redis servers (one anonymous, one with
`requirepass`) and a test container built from this repository; nothing outside
those containers is used and no state is left behind.

---

## Roadmap

Ideas for the next iterations are collected in [docs/FEATURES.md](docs/FEATURES.md).

---

## License

MIT
