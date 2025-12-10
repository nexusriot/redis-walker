# redis-walker

A terminal-based **Redis browser** with directory-like navigation, key editing, search, filtering, and optional authentication.  
Supports **virtual folders**, **multiline editing**, **jump-to-key**, **config file**, and **exclude-prefix rules** for hiding noisy Redis prefixes.

---

## Features

- Navigate Redis keys as if they were files and directories
- View, edit, create, delete keys
- Rename directories (prefix rename)
- Multiline editor for large values
- Jump to a key (`Ctrl+J`)
- Search by prefix (`/` or `Ctrl+S`)
- Optional debug logging
- Optional exclusion of key prefixes (e.g. hide `/pcp:*` keys)
- Optional **Redis authentication** (username/password; ACL or classic `requirepass`)
- Loads configuration from `/etc/redis-walker/config.json` (optional)

---

## Installation

```bash
go install github.com/nexusriot/redis-walker@latest
```

Or build manually:

```bash
git clone https://github.com/nexusriot/redis-walker
cd redis-walker
go build -o redis-walker ./cmd/redis-walker
```

---

## Usage

```bash
redis-walker [flags]
```

### Flags

| Flag | Description |
|------|-------------|
| `-host` | Redis host (default: `127.0.0.1`) |
| `-port` | Redis port (default: `6379`) |
| `-db` | Redis DB index |
| `-debug` | Enable debug logs |
| `-username` | Redis username (ACL user, optional) |
| `-password` | Redis password (optional) |
| `-exclude-prefixes` | Comma-separated list of prefixes to hide |

### Examples

Connect to a simple password-protected Redis:

```bash
redis-walker -host 127.0.0.1 -password "secret"
```

Connect to an ACL user:

```bash
redis-walker   -host 10.0.0.5   -port 6379   -username "walker"   -password "supersecret"   -db 3   -exclude-prefixes "/pcp:,/debug:,/metrics:"
```

---

## Configuration File

Path: **`/etc/redis-walker/config.json`**

Config is **optional**.  
CLI flags **override** values in the config file.

### Example config (no auth):

```json
{
  "host": "127.0.0.1",
  "port": "6379",
  "db": 0,
  "debug": false,
  "exclude_prefixes": [
    "/pcp:",
    "/metrics:"
  ]
}
```

### Example config (with auth):

```json
{
  "host": "127.0.0.1",
  "port": "6379",
  "db": 0,
  "debug": true,
  "username": "walker",
  "password": "supersecret",
  "exclude_prefixes": [
    "/pcp:",
    "/metrics:"
  ]
}
```

> **Security note:** `password` is stored in plaintext in this file.  
> Prefer restricting permissions (e.g. `chmod 600 /etc/redis-walker/config.json`) or use CLI flags / environment-based wrappers where appropriate.

---

## Authentication Setup

redis-walker supports both:

### 1. `requirepass` (classic password)

Redis config:

```conf
requirepass your-secret-password
```

Connect:

```bash
redis-walker -password "your-secret-password"
```

---

### 2. Redis ACL users (username + password)

Example:

```redis
ACL SETUSER walker on >supersecret allkeys +@all
```

Connect:

```bash
redis-walker -username walker -password supersecret
```

If both username and password are empty, redis-walker connects **without authentication**.

---

## Key Bindings

| Action | Key |
|--------|-----|
| Quit | **Ctrl+Q** |
| Open folder / descend | **Enter** |
| Up to parent | **Backspace** |
| New key / directory | **Ctrl+N** |
| Edit key | **Ctrl+E** |
| Delete | **Del** |
| Search | **/** or **Ctrl+S** |
| Jump to key | **Ctrl+J** |
| Hotkeys help | **Ctrl+H** |

---

## Excluding Noisy Prefixes

Hide telemetry, metrics, PCP, or exporter keys:

```bash
redis-walker -exclude-prefixes "/pcp:,/pcp:context.name:,/values:"
```

Config equivalent:

```json
"exclude_prefixes": ["/pcp:", "/metrics:"]
```

---

## Notes

- Values are assumed to be **string**; non-string Redis types are listed but not displayed.
- Directories are virtual: a key prefix `a/b/c` represents nested folders automatically.
- Rename operations rewrite all keys under a prefix.
- Authentication is **optional**.

---

## License

MIT
