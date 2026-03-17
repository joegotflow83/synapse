# Synapse

A lightweight, file-based, append-only ledger designed as a persistence layer for multi-agent LLM collaboration. Synapse enables autonomous agents to share state via a shared filesystem volume with a CLI-first interface for easy shell and script integration.

## Features

- **Append-only ledger** — entries are never deleted, only new versions appended
- **CBOR storage** — efficient binary serialization via CBOR
- **Multi-version entries** — retrieve full history of any entry by ID
- **Type registry** — register types with metadata or discover them from entries
- **Flexible querying** — filter by type, field values, and date ranges
- **Concurrent access** — file-level locking (shared reads, exclusive writes)
- **Compaction** — deduplicate entries while preserving order, with automatic backup
- **Multiple output formats** — JSON array or JSONL (newline-delimited)

## Installation

```bash
git clone https://github.com/joegotflow83/synapse.git
cd synapse
go build -o synapse ./cmd/synapse
```

## Usage

All commands accept a `--dir` flag (or `SYNAPSE_DIR` env var) to specify the data directory. Default: `./synapse`.

### Initialize a data directory

```bash
synapse init
synapse init --dir /data/synapse --force   # reinitialize existing directory
```

### Insert an entry

```bash
synapse insert --type task --data '{"title": "Review PR", "status": "open"}'
synapse insert --type task --id my-custom-id --data '{"title": "Deploy"}' --agent-id agent-1
```

Returns the entry ID on stdout.

### Get an entry by ID

```bash
synapse get --id <entry-id>
synapse get --id <entry-id> --history          # all versions, oldest first
synapse get --id <entry-id> --format jsonl
```

### Query entries

```bash
synapse query --type task
synapse query --type task --filter "status=open"
synapse query --filter "since:2024-01-01 until:2024-12-31"
synapse query --type task --filter "status=open" --limit 10 --format jsonl
```

Filter tokens are space-separated and ANDed together:
- `key=value` — match a field in `data`
- `since:DATE` — entries at or after date (`YYYY-MM-DD` or ISO 8601)
- `until:DATE` — entries at or before date

### Export entries to JSON

```bash
synapse export --output ./backup.json
synapse export --type task --output ./tasks.json
```

### Manage types

```bash
# List all known types
synapse list-types
synapse list-types --format jsonl

# Register a type with metadata
synapse create-type task \
  --description "A unit of work" \
  --example '{"title": "string", "status": "open|closed"}'
```

### Compact the ledger

Removes older versions of entries with the same ID, keeping only the latest. Creates a backup at `events.cbor.bak` before compacting.

```bash
synapse compact
```

## Data Model

### Entry

| Field            | Type              | Description                              |
|------------------|-------------------|------------------------------------------|
| `id`             | string            | Unique identifier (UUID v4 if not set)   |
| `type`           | string            | Entry type (required)                    |
| `timestamp`      | int64             | Unix seconds (auto-set to now)           |
| `agent_id`       | string            | Identifier of the writing agent          |
| `data`           | map[string]any    | Arbitrary JSON payload                   |
| `agent_metadata` | map[string]any    | Agent-specific metadata                  |

### Storage

| File             | Description                              |
|------------------|------------------------------------------|
| `events.cbor`    | Append-only log of all entries           |
| `types.cbor`     | Registered type metadata                 |
| `events.cbor.bak`| Backup created before compaction         |

## Exit Codes

| Code | Meaning              |
|------|----------------------|
| 0    | Success              |
| 1    | Validation / general error |
| 2    | Not found            |
| 3    | Lock error           |
| 4    | Integrity error      |

## Dependencies

- [spf13/cobra](https://github.com/spf13/cobra) — CLI framework
- [fxamacker/cbor](https://github.com/fxamacker/cbor) — CBOR encoding
- [gofrs/flock](https://github.com/gofrs/flock) — file locking
- [google/uuid](https://github.com/google/uuid) — UUID generation

## License

MIT
