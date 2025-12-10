# redis-walker
(Proof-of-concept)


A terminal-based **Redis browser** with directory-like navigation, key editing, search, and filtering.  
Supports **virtual folders**, **multiline editing**, **jump-to-key**, and **exclusion rules** for noisy Redis prefixes.

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
| `-exclude-prefixes` | Comma-separated list of prefixes to hide |

### Example

```bash
redis-walker -host 10.0.0.5 -db 3 -exclude-prefixes "/pcp:,/debug:,/metrics:"
```

---

## Configuration File

Path: **`/etc/redis-walker/config.json`**

Config is **optional**.  
CLI flags **override** values in the config file.

### Example config:

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

Useful when Redis contains system/telemetry keys (pcp, metrics, exporters, etc.).

CLI:

```bash
redis-walker -exclude-prefixes "/pcp:,/pcp:context.name:,/values:"
```

Config:

```json
"exclude_prefixes": ["/pcp:", "/metrics:"]
```

---

## Notes

- Values are assumed to be **string**; non-string Redis types are listed but not displayed.  
- Directories are virtual: a key prefix `a/b/c` represents nested folders automatically.  
- Rename operations rewrite all keys under a prefix.

---

## License

MIT
