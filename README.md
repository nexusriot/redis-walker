# redis-walker

A terminal **Redis browser** that presents the keyspace as a directory tree: navigate,
view, edit, create, rename and delete keys with a two-pane, file-manager-style UI.

Built with [tview](https://github.com/rivo/tview) and [go-redis](https://github.com/redis/go-redis).

---

## Features

- Browse Redis keys as if they were files and folders (`app/cfg/db` → `/app/cfg/db`)
- Key details: type, size, TTL and the value, with **JSON/XML/gzip/base64 decoding**
  and a **hex viewer** for binary payloads
- **Two browser panes** (`F9`) over the same server, another database or another
  host, with **copy** (`F5`) and **move** (`F6`) between them
- **Folder analysis** (`Ctrl+A`): key count, memory use, type breakdown and the
  largest prefixes and keys of a subtree
- Create, edit and delete keys; rename a whole folder (prefix) in one step
- Multiline editor for large values, or edit in **`$EDITOR`** (`Ctrl+O`)
- Every Redis call runs **in the background**: the UI stays responsive on a large
  keyspace, shows scan progress and can be cancelled with `Esc`
- Jump to any key or folder (`Ctrl+J`), search within the current folder (`/`)
- Non-string keys (hash, list, set, zset, stream) are listed and labelled, and are
  protected from being overwritten by the editor
- **Non-interactive commands** (`ls`, `tree`, `cat`, `set`, `rm`, `stat`) with
  `-json` output, so the same tool works in scripts
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
redis-walker [flags]            # start the browser
redis-walker [flags] <command>  # run a single command and exit
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
| `-editor` | `$VISUAL`, `$EDITOR`, `vi` | Editor used by `Ctrl+O` |
| `-json` | `false` | Machine readable output for the commands |
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
| Edit the value in `$EDITOR` | **Ctrl+O** |
| Delete (recursive for folders) | **Del** |
| Search in the current folder | **/** or **Ctrl+S** |
| Jump to a key or folder | **Ctrl+J** |
| Analyze a folder | **Ctrl+A** |
| Switch the value view (decoded / raw / hex) | **Ctrl+V** |
| Reload the current folder | **Ctrl+R** |
| Show or hide the second pane | **F9** or **Ctrl+W** |
| Switch to the other pane | **Tab** or **Shift+Tab** |
| Copy / move to the other pane | **F5** / **F6** |
| Connect the pane to another database | **Ctrl+D** |
| Cancel the running operation | **Esc** |
| Hotkey help | **F1** or **?** |
| Quit | **Ctrl+Q** |

In the value editor: **Ctrl+S** saves, **Ctrl+F** re-indents a JSON document and
**Esc** discards. `Ctrl+Q` quits the whole application from anywhere, including
the editor — unsaved changes are lost.

### Background operations and cancelling

Listing, deleting, copying and analysing all run on a background connection. The
bottom line shows what is running and how far it got (`12345 keys scanned`), the
UI keeps responding, and **Esc** cancels the operation. Only one operation runs
at a time; starting another while one is in flight is refused rather than queued.

### Value views

The details pane decodes what it can: a JSON document is pretty printed, XML is
re-indented, and gzip, zlib and base64 wrappers are unwrapped, including nested
combinations (`base64 -> gzip -> json`). **Ctrl+V** cycles between the decoded
view, the raw bytes and a hex dump; binary values start in hex. Decoding only
changes what is *shown* — the stored value is never rewritten, and saving an
edit that breaks a previously valid JSON document is refused.

### Two panes

**F9** opens a second browser next to the first. **Tab** switches between them,
**F5** copies the selected key or folder into the other pane's directory and
**F6** moves it. Both panes can point at different folders, and with **Ctrl+D**
at different databases of the server, so a subtree can be copied from database 0
to database 3 in one keystroke. Transfers use `COPY` on the same server and
`DUMP`/`RESTORE` across connections, so types and TTLs survive.

### Folder analysis

**Ctrl+A** walks the selected folder and reports how many keys it holds, how much
memory they use (`MEMORY USAGE`, estimated from the value length when the server
does not support it), the breakdown by type, how many keys expire, and the
largest child prefixes and individual keys. The numbers stay in the details pane
of that folder until they are re-read.

---

## Command line

Every command takes the same connection flags as the browser.

```bash
redis-walker ls /app                  # direct children of a folder
redis-walker tree /app                # recursive listing
redis-walker cat /app/cfg/db          # write a value to stdout
redis-walker set /app/cfg/db postgres # store a value
echo -n "$PAYLOAD" | redis-walker set /app/blob -   # value from stdin
redis-walker rm /app/cfg/db           # delete a key
redis-walker rm /app/cache/           # delete a folder recursively
redis-walker stat /app                # keys, memory and the largest entries
```

Add `-json` for machine readable output:

```bash
redis-walker -json ls /app | jq -r '.entries[] | select(.dir) | .path'
redis-walker -json stat /app | jq '.Bytes'
```

A path given on the command line follows the same rule as in the browser: the
leading `/` is virtual. `cat /app/name` reads the key `app/name`, and `set`
writes to an existing key whichever way it is spelled, creating new keys without
the leading slash.

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
- Copying or moving a key preserves its type and TTL: `COPY` is used on the same
  server, `DUMP`/`RESTORE` across servers, and `RENAME`/`RENAMENX` for a move
  inside one database. A transfer never overwrites an existing key.
- Each operation is bounded by a 60 second timeout; `Esc` cancels earlier.

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

Browsing and editing need `SCAN`, `TYPE`, `TTL`, `PTTL`, `STRLEN`, `GETRANGE`,
`GET`, `SET`, `EXISTS`, `DEL`, `RENAME` and `RENAMENX`. Copying between panes
additionally needs `COPY`, or `DUMP` and `RESTORE` when the two panes are on
different servers. The folder analysis uses `MEMORY USAGE`; without it the sizes
are estimated from the value length and reported as `(estimated)`.

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
make build     # build build/redis-walker (a git tag, if any, becomes the version)
make test      # unit tests (no Redis needed - miniredis is embedded)
make race      # unit tests with the race detector
make cover     # coverage summary
make vet fmt   # static checks / formatting
make e2e       # hermetic end-to-end suite in Docker (see below)
```

### Layout

| Package | Contents |
|---------|----------|
| `cmd/redis-walker` | flag parsing, the non-interactive commands |
| `pkg/config` | config file, flag precedence |
| `pkg/model` | the Redis layer: listing, editing, copying, statistics |
| `pkg/format` | encoding detection, pretty printing, hex dump |
| `pkg/view` | tview widgets and the layout |
| `pkg/controller` | key bindings, background jobs, the two panes |
| `internal/uitest` | drives a tview app on a simulated terminal (tests only) |

### Tests

* **Unit tests** cover the path/key mapping, the value decoding, the Redis model
  (against an in-process [miniredis](https://github.com/alicebob/miniredis)), the
  controller and the command line. The controller tests run the real tview
  application on a `tcell` simulation screen through `internal/uitest`, so
  listing, navigation, dialogs, background operations and key bindings are
  exercised as in a terminal.
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
