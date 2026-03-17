package ledger

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/synapse-tool/synapse/internal/lock"
	"github.com/synapse-tool/synapse/internal/model"
	"github.com/synapse-tool/synapse/internal/query"
	"github.com/synapse-tool/synapse/internal/types"
)

const (
	eventsFile = "events.cbor"
	typesFile  = "types.cbor"
)

// Ledger is the main facade for interacting with a Synapse data directory.
type Ledger struct {
	Dir string
}

// QueryOpts controls filtering and limiting for Query operations.
type QueryOpts struct {
	Type    string
	Filter  string
	Limit   int
}

// CompactStats reports the results of a compaction.
type CompactStats struct {
	EntriesBefore int
	EntriesAfter  int
	BytesBefore   int64
	BytesAfter    int64
}

// TypeInfo represents a type with optional metadata.
type TypeInfo struct {
	Name        string
	Description string
	Example     string
	CreatedAt   int64
	Registered  bool // true if in types.cbor, false if only discovered
}

// Open validates that the directory is an initialized Synapse directory
// and returns a Ledger handle.
func Open(dir string) (*Ledger, error) {
	ep := filepath.Join(dir, eventsFile)
	if _, err := os.Stat(ep); os.IsNotExist(err) {
		return nil, fmt.Errorf("directory %q is not initialized (missing %s)", dir, eventsFile)
	} else if err != nil {
		return nil, fmt.Errorf("stat events file: %w", err)
	}
	return &Ledger{Dir: dir}, nil
}

// Init creates and initializes a new Synapse data directory.
// If force is false and the directory already contains events.cbor, it returns an error.
// Per the storage engine spec, init acquires an exclusive lock.
func Init(dir string, force bool) error {
	ep := filepath.Join(dir, eventsFile)
	tp := filepath.Join(dir, typesFile)

	// Create directory first so the lock file can be placed inside it.
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create directory: %w", err)
	}

	fl, err := lock.AcquireExclusive(dir, lock.DefaultTimeout)
	if err != nil {
		return fmt.Errorf("lock: %w", err)
	}
	defer fl.Unlock()

	if !force {
		if _, err := os.Stat(ep); err == nil {
			return fmt.Errorf("directory %q is already initialized (use --force to reinitialize)", dir)
		}
	}

	if err := InitFile(ep); err != nil {
		return fmt.Errorf("init events file: %w", err)
	}
	if err := types.InitFile(tp); err != nil {
		return fmt.Errorf("init types file: %w", err)
	}
	return nil
}

// Insert validates and appends an entry to the event log under an exclusive lock.
func (l *Ledger) Insert(entry *model.Entry) error {
	if err := entry.Validate(); err != nil {
		return fmt.Errorf("validate entry: %w", err)
	}

	fl, err := lock.AcquireExclusive(l.Dir, lock.DefaultTimeout)
	if err != nil {
		return fmt.Errorf("lock: %w", err)
	}
	defer fl.Unlock()

	ep := filepath.Join(l.Dir, eventsFile)
	if err := AppendEntry(ep, entry); err != nil {
		return fmt.Errorf("append entry: %w", err)
	}
	return nil
}

// Query reads entries, filters by type and filter clauses, and applies a limit.
func (l *Ledger) Query(opts QueryOpts) ([]*model.Entry, error) {
	fl, err := lock.AcquireShared(l.Dir, lock.DefaultTimeout)
	if err != nil {
		return nil, fmt.Errorf("lock: %w", err)
	}
	defer fl.Unlock()

	ep := filepath.Join(l.Dir, eventsFile)
	entries, err := ReadAllEntries(ep)
	if err != nil {
		return nil, fmt.Errorf("read entries: %w", err)
	}

	var clauses []query.Clause
	if opts.Filter != "" {
		clauses, err = query.Parse(opts.Filter)
		if err != nil {
			return nil, fmt.Errorf("parse filter: %w", err)
		}
	}

	var results []*model.Entry
	for _, e := range entries {
		if opts.Type != "" && e.Type != opts.Type {
			continue
		}
		if len(clauses) > 0 && !query.Evaluate(e, clauses) {
			continue
		}
		results = append(results, e)
		if opts.Limit > 0 && len(results) >= opts.Limit {
			break
		}
	}

	return results, nil
}

// Get retrieves entries by ID. If history is false, returns only the latest
// version (highest timestamp). If history is true, returns all versions
// ordered by timestamp ascending.
func (l *Ledger) Get(id string, history bool) ([]*model.Entry, error) {
	fl, err := lock.AcquireShared(l.Dir, lock.DefaultTimeout)
	if err != nil {
		return nil, fmt.Errorf("lock: %w", err)
	}
	defer fl.Unlock()

	ep := filepath.Join(l.Dir, eventsFile)
	entries, err := ReadAllEntries(ep)
	if err != nil {
		return nil, fmt.Errorf("read entries: %w", err)
	}

	var matches []*model.Entry
	for _, e := range entries {
		if e.ID == id {
			matches = append(matches, e)
		}
	}

	if len(matches) == 0 {
		return nil, nil
	}

	// Sort by timestamp ascending.
	sort.Slice(matches, func(i, j int) bool {
		return matches[i].Timestamp < matches[j].Timestamp
	})

	if history {
		return matches, nil
	}
	// Return only the latest (last after ascending sort).
	return []*model.Entry{matches[len(matches)-1]}, nil
}

// Compact deduplicates entries by ID, keeping only the entry with the latest
// timestamp for each unique ID. It writes to a temp file, backs up the original,
// and atomically renames.
func (l *Ledger) Compact() (CompactStats, error) {
	fl, err := lock.AcquireExclusive(l.Dir, lock.DefaultTimeout)
	if err != nil {
		return CompactStats{}, fmt.Errorf("lock: %w", err)
	}
	defer fl.Unlock()

	ep := filepath.Join(l.Dir, eventsFile)

	// Get original file size.
	origInfo, err := os.Stat(ep)
	if err != nil {
		return CompactStats{}, fmt.Errorf("stat events file: %w", err)
	}

	entries, err := ReadAllEntries(ep)
	if err != nil {
		return CompactStats{}, fmt.Errorf("read entries: %w", err)
	}

	// Deduplicate: keep latest timestamp per ID.
	latest := make(map[string]*model.Entry)
	for _, e := range entries {
		if existing, ok := latest[e.ID]; !ok || e.Timestamp >= existing.Timestamp {
			latest[e.ID] = e
		}
	}

	// Collect surviving entries preserving original order of the kept entry.
	seen := make(map[string]bool)
	var surviving []*model.Entry
	for _, e := range entries {
		if latest[e.ID] == e && !seen[e.ID] {
			surviving = append(surviving, e)
			seen[e.ID] = true
		}
	}

	// Write to temp file.
	tmpPath := ep + ".tmp"
	if err := WriteEntries(tmpPath, surviving); err != nil {
		return CompactStats{}, fmt.Errorf("write temp file: %w", err)
	}

	// Get compacted file size.
	tmpInfo, err := os.Stat(tmpPath)
	if err != nil {
		return CompactStats{}, fmt.Errorf("stat temp file: %w", err)
	}

	// Backup original.
	bakPath := ep + ".bak"
	if err := os.Rename(ep, bakPath); err != nil {
		return CompactStats{}, fmt.Errorf("backup original: %w", err)
	}

	// Atomic rename.
	if err := os.Rename(tmpPath, ep); err != nil {
		// Try to restore from backup.
		_ = os.Rename(bakPath, ep)
		return CompactStats{}, fmt.Errorf("rename temp to events: %w", err)
	}

	return CompactStats{
		EntriesBefore: len(entries),
		EntriesAfter:  len(surviving),
		BytesBefore:   origInfo.Size(),
		BytesAfter:    tmpInfo.Size(),
	}, nil
}

// ListTypes returns all known types by merging registered types from types.cbor
// with types discovered by scanning entries in events.cbor.
func (l *Ledger) ListTypes() ([]TypeInfo, error) {
	fl, err := lock.AcquireShared(l.Dir, lock.DefaultTimeout)
	if err != nil {
		return nil, fmt.Errorf("lock: %w", err)
	}
	defer fl.Unlock()

	tp := filepath.Join(l.Dir, typesFile)
	registered, err := types.ReadTypes(tp)
	if err != nil {
		// If types.cbor doesn't exist, treat as empty.
		if os.IsNotExist(err) {
			registered = make(map[string]types.TypeMeta)
		} else {
			return nil, fmt.Errorf("read types: %w", err)
		}
	}

	ep := filepath.Join(l.Dir, eventsFile)
	entries, err := ReadAllEntries(ep)
	if err != nil {
		return nil, fmt.Errorf("read entries: %w", err)
	}

	// Collect discovered types.
	discovered := make(map[string]bool)
	for _, e := range entries {
		if e.Type != "" {
			discovered[e.Type] = true
		}
	}

	// Merge: registered types get their metadata, discovered-only get empty metadata.
	allNames := make(map[string]bool)
	for name := range registered {
		allNames[name] = true
	}
	for name := range discovered {
		allNames[name] = true
	}

	var result []TypeInfo
	for name := range allNames {
		info := TypeInfo{Name: name}
		if meta, ok := registered[name]; ok {
			info.Description = meta.Description
			info.Example = meta.Example
			info.CreatedAt = meta.CreatedAt
			info.Registered = true
		}
		result = append(result, info)
	}

	// Sort by name for consistent output.
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})

	return result, nil
}

// CreateType registers a type under an exclusive lock (write operation per spec).
func (l *Ledger) CreateType(name, description, example string) error {
	fl, err := lock.AcquireExclusive(l.Dir, lock.DefaultTimeout)
	if err != nil {
		return fmt.Errorf("lock: %w", err)
	}
	defer fl.Unlock()

	tp := filepath.Join(l.Dir, typesFile)
	if err := types.CreateType(tp, name, description, example); err != nil {
		return fmt.Errorf("create type: %w", err)
	}
	return nil
}

// EventsPath returns the full path to the events.cbor file.
func (l *Ledger) EventsPath() string {
	return filepath.Join(l.Dir, eventsFile)
}

// TypesPath returns the full path to the types.cbor file.
func (l *Ledger) TypesPath() string {
	return filepath.Join(l.Dir, typesFile)
}

// Now is a package-level variable for testing time-dependent behavior.
var Now = time.Now
