# Synapse Agent Guide

You are using **Synapse** — a file-based, append-only ledger designed for multi-agent collaboration via a shared filesystem. This document is your complete reference for reading, writing, and querying shared state.

---

## What You Can Do

| Goal | Command |
|------|---------|
| Initialize a new ledger | `synapse init` |
| Write an entry | `synapse insert` |
| Read entries by type or filter | `synapse query` |
| Retrieve a specific entry by ID | `synapse get` |
| List all known entry types | `synapse list-types` |
| Register a type with metadata | `synapse create-type` |
| Export entries to a JSON file | `synapse export` |
| Deduplicate the ledger | `synapse compact` |

---

## Setup

### Data Directory

Every command requires a data directory. Set it once and all commands inherit it:

```bash
export SYNAPSE_DIR=/shared/synapse
```

Or pass it per command:

```bash
synapse --dir /shared/synapse <command>
```

### Initialize

Run once before any other command:

```bash
synapse init
```

If the directory already exists and you want to reset it:

```bash
synapse init --force   # WARNING: destroys existing data
```

---

## Writing Entries

### Basic Insert

```bash
synapse insert --type <type> --data '<json>'
```

Output: the assigned entry ID (a UUID v4).

**`--type` is required.** It classifies the entry. Use consistent type names across agents (e.g., `task`, `decision`, `observation`, `result`).

**`--data` is required.** Must be a valid JSON object string.

### Full Insert with All Fields

```bash
synapse insert \
  --type task \
  --data '{"title": "Deploy service", "status": "open", "priority": "high"}' \
  --agent-id agent-planner \
  --metadata '{"model": "claude-opus-4-6", "confidence": 0.9}' \
  --id my-custom-id
```

| Flag | Required | Purpose |
|------|----------|---------|
| `--type` | Yes | Entry classification |
| `--data` | Yes | JSON payload (your content) |
| `--agent-id` | No | Your agent identifier |
| `--metadata` | No | Agent-level metadata (model, confidence, etc.) |
| `--id` | No | Custom ID; auto-generated UUID v4 if omitted |

### Updating an Entry

Synapse is **append-only** — you cannot edit entries in place. To update, insert a new entry with the **same `--id`**:

```bash
# Original
synapse insert --type task --id task-001 --data '{"status": "open"}'

# Update
synapse insert --type task --id task-001 --data '{"status": "closed"}'
```

The latest entry (by timestamp) is the canonical version. Use `--history` in `get` to see all versions.

---

## Reading Entries

### Query by Type

```bash
synapse query --type task
```

### Query with Filters

Filters are space-separated clauses, all ANDed together:

```bash
# Single field filter
synapse query --type task --filter "status=open"

# Multiple conditions (AND)
synapse query --type task --filter "status=open priority=high"

# Date range
synapse query --filter "since:2024-01-01 until:2024-12-31"

# Combined
synapse query --type task --filter "status=open since:2024-06-01"
```

**Filter syntax:**

| Pattern | Example | Behavior |
|---------|---------|----------|
| `key=value` | `status=open` | Exact match on `data[key]` |
| `since:DATE` | `since:2024-01-01` | Timestamp ≥ start of day (UTC) |
| `until:DATE` | `until:2024-12-31` | Timestamp ≤ end of day (UTC) |

Dates accept `YYYY-MM-DD` or RFC3339 format.

### Limit Results

```bash
synapse query --type task --limit 10
```

### Output Formats

```bash
# JSON array (default)
synapse query --type task --format json

# JSONL — one entry per line, good for piping
synapse query --type task --format jsonl | jq '.id'
```

### Get a Specific Entry

```bash
# Latest version only
synapse get --id task-001

# All versions (ascending timestamp)
synapse get --id task-001 --history
```

Returns exit code 2 if the ID does not exist.

---

## Entry Structure

Every entry you read back will have this shape:

```json
{
  "id": "550e8400-e29b-41d4-a716-446655440000",
  "type": "task",
  "timestamp": 1700000000,
  "agent_id": "agent-planner",
  "data": {
    "title": "Deploy service",
    "status": "open"
  },
  "agent_metadata": {
    "model": "claude-opus-4-6",
    "confidence": 0.9
  }
}
```

Fields `agent_id`, `data`, and `agent_metadata` are omitted when empty.

---

## Type Management

Types let you and other agents agree on structure. They are optional but recommended.

### Register a Type

```bash
synapse create-type task \
  --description "A discrete unit of work" \
  --example '{"title": "string", "status": "open|in_progress|closed"}'
```

### List All Types

```bash
synapse list-types
```

Each result includes `registered: true|false`. Registered types have explicit metadata; unregistered types were discovered from existing entries.

---

## Maintenance

### Export

Save a snapshot of all entries (or a filtered subset) to a JSON file:

```bash
synapse export --output ./backup.json
synapse export --type task --output ./tasks.json
```

### Compact

Deduplicate the ledger — keeps only the latest version of each entry ID:

```bash
synapse compact
```

Output example: `Compaction complete: 100 entries -> 50 entries, 5000 bytes saved`

Run this periodically if you write many updates to the same IDs.

---

## Exit Codes

Check exit codes in scripts to handle errors correctly:

| Code | Meaning | Example Trigger |
|------|---------|----------------|
| `0` | Success | — |
| `1` | Validation / general error | Missing required flag, invalid JSON, bad filter |
| `2` | Not found / not initialized | ID not found, directory not initialized |
| `3` | Lock timeout | Another process holds the lock for > 5 seconds |
| `4` | Data corruption | CBOR file damaged |

```bash
synapse get --id task-001
if [ $? -eq 2 ]; then
  echo "Entry does not exist yet"
fi
```

---

## Concurrency Behavior

Synapse is safe for concurrent multi-agent access:

- **Reads** (query, get, list-types) use **shared locks** — multiple agents can read simultaneously.
- **Writes** (insert, compact, create-type) use **exclusive locks** — serialized across agents.
- Lock timeout is **5 seconds**. If you get exit code 3, the ledger is under heavy write contention — retry after a short delay.
- Never delete or truncate `events.cbor` or `types.cbor` directly — use `init --force` for a full reset.

---

## Best Practices

### Always Tag Your Agent

Include `--agent-id` on every insert so other agents can filter by source:

```bash
synapse insert --type observation --agent-id agent-researcher \
  --data '{"finding": "API latency spiked at 14:32 UTC"}'
```

### Use Consistent Type Names

Agree on type names across agents before starting. Register them upfront:

```bash
synapse create-type task        --description "Work to be done"
synapse create-type decision    --description "A resolved choice"
synapse create-type observation --description "A factual note about the environment"
synapse create-type result      --description "Output from a completed task"
```

### Capture Your Agent's Identity in Metadata

```bash
synapse insert --type result \
  --agent-id agent-executor \
  --metadata '{"model": "claude-sonnet-4-6", "task_id": "task-001"}' \
  --data '{"output": "Done", "exit_code": 0}'
```

### Use `--format jsonl` for Pipelines

JSONL is easier to process line-by-line:

```bash
synapse query --type task --filter "status=open" --format jsonl \
  | jq -r '.id' \
  | while read id; do
      echo "Processing $id"
    done
```

### Update by Reinserting with the Same ID

```bash
TASK_ID=$(synapse insert --type task --data '{"status":"open","title":"Analyze logs"}')
echo "Created: $TASK_ID"

# Later, mark it done:
synapse insert --type task --id "$TASK_ID" --data '{"status":"closed","title":"Analyze logs"}'
```

### Run Compact After Heavy Update Workloads

If your workflow involves many updates to the same IDs, compact to reduce storage and query time:

```bash
synapse compact
```

### Use `--limit` for Performance

Full scans are O(n). When you only need recent or top-k entries, always pass `--limit`:

```bash
synapse query --type observation --limit 20
```

### Check Initialization Before Starting

If your agent may run in a fresh environment:

```bash
synapse query --type task > /dev/null 2>&1
if [ $? -eq 2 ]; then
  synapse init
fi
```

---

## Common Patterns

### Task Queue (Producer / Consumer)

**Producer:**
```bash
TASK_ID=$(synapse insert --type task --agent-id agent-planner \
  --data '{"title": "Summarize document", "status": "pending", "doc": "report.pdf"}')
```

**Consumer:**
```bash
synapse query --type task --filter "status=pending" --format jsonl \
  | jq -c '.' \
  | while IFS= read -r entry; do
      id=$(echo "$entry" | jq -r '.id')
      # Process task...
      synapse insert --type task --id "$id" \
        --data "{\"status\": \"done\", \"title\": \"Summarize document\"}"
    done
```

### Shared Decision Log

```bash
synapse insert --type decision --agent-id agent-architect \
  --data '{"choice": "Use PostgreSQL", "rationale": "ACID compliance required", "alternatives": ["SQLite", "MySQL"]}'
```

### Cross-Agent Handoff

```bash
# Agent A signals Agent B
synapse insert --type handoff \
  --agent-id agent-a \
  --data '{"to": "agent-b", "task_id": "task-001", "context": "Analysis complete, ready for review"}'

# Agent B polls for work addressed to it
synapse query --type handoff --filter "to=agent-b" --format jsonl
```

---

## File Layout

```
$SYNAPSE_DIR/
├── events.cbor      # Append-only entry log (CBOR indefinite-length array)
├── types.cbor       # Registered type metadata (CBOR map)
└── .synapse.lock    # OS-level lock file (do not delete while agents are running)
```

Do not manually edit these files. Use `synapse export` for backups and `synapse compact` for maintenance.
