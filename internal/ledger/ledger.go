package ledger

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
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
	// SoftSizeLimit is the threshold in bytes above which a warning is emitted
	// on stderr. Large entries degrade read performance for all callers.
	SoftSizeLimit = 1024
)

const (
	eventsFile = "events.cbor"
	typesFile  = "types.cbor"
	indexFile  = "index.cbor"
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
	// TypeIndex is an optional pre-loaded type index. When non-nil and Type != "",
	// it is used instead of loading index.cbor from disk (e.g. by the daemon).
	TypeIndex map[string][]int64
	// Scanned is an optional output counter. When non-nil, Query increments it
	// for each entry examined during the scan. This allows callers (e.g. the
	// daemon) to report operation stats without changing the return signature.
	Scanned *int
}

// BatchQuerySpec defines a single query within a batch operation.
type BatchQuerySpec struct {
	Type   string `json:"type"`
	Filter string `json:"filter"`
	Limit  int    `json:"limit"`
}

// BatchQueryResult holds the matched entries for one spec in a batch.
type BatchQueryResult struct {
	// Entries matched by this spec. Always a non-nil slice.
	Entries []*model.Entry `json:"entries"`
}

// CompactStats reports the results of a compaction.
type CompactStats struct {
	EntriesBefore int   `json:"entries_before"`
	EntriesAfter  int   `json:"entries_after"`
	BytesBefore   int64 `json:"bytes_before"`
	BytesAfter    int64 `json:"bytes_after"`
	// NoOp is true when every entry was already unique and no file rewrite
	// was performed.
	NoOp bool `json:"no_op"`
	// ExclusiveLockHeld is the wall-clock duration the exclusive lock was held
	// during compaction. With copy-on-write this covers only the merge +
	// rename + index rebuild phase, not the full read-deduplicate cycle.
	ExclusiveLockHeld time.Duration `json:"exclusive_lock_held_ns"`
}

// TypeInfo represents a type with optional metadata.
type TypeInfo struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Example     string `json:"example,omitempty"`
	CreatedAt   int64  `json:"created_at,omitempty"`
	Registered  bool   `json:"registered"`
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
	ip := filepath.Join(dir, indexFile)
	if err := InitIndexFile(ip); err != nil {
		return fmt.Errorf("init index file: %w", err)
	}
	iip := filepath.Join(dir, idIndexFile)
	if err := InitIDIndexFile(iip); err != nil {
		return fmt.Errorf("init id index file: %w", err)
	}
	return nil
}

// warnIfOversized prints a warning to stderr when the serialized entry data
// exceeds SoftSizeLimit bytes. The warning is informational; insert proceeds
// regardless.
func warnIfOversized(entry *model.Entry) {
	b, err := json.Marshal(entry.Data)
	if err != nil {
		return // can't measure, skip
	}
	if len(b) > SoftSizeLimit {
		fmt.Fprintf(os.Stderr, "warning: entry data is %d bytes (recommended max: 256); large entries degrade read performance\n", len(b))
	}
}

// Insert validates and appends an entry to the event log under an exclusive lock.
func (l *Ledger) Insert(entry *model.Entry) error {
	_, err := l.InsertIndexed(entry)
	return err
}

// InsertIndexed is like Insert but also returns the IndexEntry written to disk.
// Callers that maintain in-memory index caches (e.g. the daemon) can use the
// returned IndexEntry to update their caches without re-reading disk.
func (l *Ledger) InsertIndexed(entry *model.Entry) (*IndexEntry, error) {
	if err := entry.Validate(); err != nil {
		return nil, fmt.Errorf("validate entry: %w", err)
	}
	warnIfOversized(entry)

	fl, err := lock.AcquireExclusive(l.Dir, lock.DefaultTimeout)
	if err != nil {
		return nil, fmt.Errorf("lock: %w", err)
	}
	defer fl.Unlock()

	ep := filepath.Join(l.Dir, eventsFile)
	ip := filepath.Join(l.Dir, indexFile)
	ie, err := AppendEntryAndIndexEntry(ep, ip, entry)
	if err != nil {
		return nil, fmt.Errorf("insert indexed: %w", err)
	}
	return ie, nil
}

// InsertBatch validates all entries, then acquires a single exclusive lock and
// appends them all in one pass. This is significantly faster than calling
// Insert in a loop because the lock is acquired only once. Atomicity is
// all-or-nothing: all entries are validated before the lock is acquired, so
// if validation fails no entries are written.
func (l *Ledger) InsertBatch(entries []*model.Entry) error {
	_, err := l.InsertBatchIndexed(entries)
	return err
}

// InsertBatchIndexed is like InsertBatch but also returns the IndexEntries
// written to disk, in the same order as entries. Callers that maintain
// in-memory index caches (e.g. the daemon) can use the returned entries to
// update their caches without re-reading disk.
func (l *Ledger) InsertBatchIndexed(entries []*model.Entry) ([]*IndexEntry, error) {
	for i, entry := range entries {
		if err := entry.Validate(); err != nil {
			return nil, fmt.Errorf("entry %d: validate: %w", i, err)
		}
		warnIfOversized(entry)
	}

	fl, err := lock.AcquireExclusive(l.Dir, lock.DefaultTimeout)
	if err != nil {
		return nil, fmt.Errorf("lock: %w", err)
	}
	defer fl.Unlock()

	ep := filepath.Join(l.Dir, eventsFile)
	ip := filepath.Join(l.Dir, indexFile)

	offsets, err := AppendEntries(ep, entries)
	if err != nil {
		return nil, fmt.Errorf("append entries: %w", err)
	}

	ies := make([]*IndexEntry, len(entries))
	for i, entry := range entries {
		ies[i] = &IndexEntry{ID: entry.ID, Type: entry.Type, Offset: offsets[i]}
	}
	if err := AppendIndexEntries(ip, ies); err != nil {
		return nil, fmt.Errorf("append index entries: %w", err)
	}
	return ies, nil
}

// seekReadThreshold is the number of offsets above which queryByOffsets falls
// back to reading the entire file into memory for sequential read performance.
const seekReadThreshold = 5000

// queryByOffsets decodes entries at the given byte offsets from ep and applies
// filter clauses and limit. Returns an error if any seek fails so the caller
// can fall back to a full scan.
func queryByOffsets(ep string, offsets []int64, clauses []query.Clause, limit int, scanned *int) ([]*model.Entry, error) {
	// Use the bulk-read path only for large unlimited queries where most of
	// the file will be read anyway. Otherwise seek to individual offsets so
	// that --limit N can stop early without loading the entire file.
	if limit == 0 && len(offsets) > seekReadThreshold {
		return queryByOffsetsBulk(ep, offsets, clauses, limit, scanned)
	}
	return queryByOffsetsSeek(ep, offsets, clauses, limit, scanned)
}

// queryByOffsetsSeek opens the file once and seeks to each offset, decoding
// one entry at a time. This avoids reading the entire file when only a few
// entries are needed (e.g., --limit 1).
func queryByOffsetsSeek(ep string, offsets []int64, clauses []query.Clause, limit int, scanned *int) ([]*model.Entry, error) {
	f, err := os.Open(ep)
	if err != nil {
		return nil, fmt.Errorf("open file: %w", err)
	}
	defer f.Close()

	var results []*model.Entry
	for _, offset := range offsets {
		if limit > 0 && len(results) >= limit {
			break
		}
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			return nil, fmt.Errorf("seek to offset %d: %w", offset, err)
		}
		var entry model.Entry
		dec := cborDecMode.NewDecoder(f)
		if err := dec.Decode(&entry); err != nil {
			return nil, fmt.Errorf("decode entry at offset %d: %w", offset, err)
		}
		if scanned != nil {
			*scanned++
		}
		if len(clauses) > 0 && !query.Evaluate(&entry, clauses) {
			continue
		}
		results = append(results, &entry)
	}
	return results, nil
}

// queryByOffsetsBulk reads the entire file into memory then decodes entries at
// the given offsets. More efficient than seeking when most of the file will be
// read anyway (large offset lists without a limit).
func queryByOffsetsBulk(ep string, offsets []int64, clauses []query.Clause, limit int, scanned *int) ([]*model.Entry, error) {
	data, err := os.ReadFile(ep)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}

	var results []*model.Entry
	for _, offset := range offsets {
		if offset < 0 || int(offset) >= len(data) {
			return nil, fmt.Errorf("offset %d out of range (file size %d)", offset, len(data))
		}
		var entry model.Entry
		dec := cborDecMode.NewDecoder(bytes.NewReader(data[offset:]))
		if err := dec.Decode(&entry); err != nil {
			return nil, fmt.Errorf("decode entry at offset %d: %w", offset, err)
		}
		if scanned != nil {
			*scanned++
		}
		if len(clauses) > 0 && !query.Evaluate(&entry, clauses) {
			continue
		}
		results = append(results, &entry)
		if limit > 0 && len(results) >= limit {
			break
		}
	}
	return results, nil
}

// Query reads entries, filters by type and filter clauses, and applies a limit.
// When a type filter is specified, it uses the on-disk type index for an
// O(matches) seek path instead of a full O(N) scan. Falls back to full scan
// when no type filter is given or when the index is missing/corrupt.
func (l *Ledger) Query(opts QueryOpts) ([]*model.Entry, error) {
	fl, err := lock.AcquireShared(l.Dir, lock.DefaultTimeout)
	if err != nil {
		return nil, fmt.Errorf("lock: %w", err)
	}
	defer fl.Unlock()

	ep := filepath.Join(l.Dir, eventsFile)

	var clauses []query.Clause
	if opts.Filter != "" {
		clauses, err = query.Parse(opts.Filter)
		if err != nil {
			return nil, fmt.Errorf("parse filter: %w", err)
		}
	}

	// Fast path: use type index when a type filter is specified.
	// Prefer a pre-loaded in-memory index (opts.TypeIndex) over disk reads.
	if opts.Type != "" {
		typeIdx := opts.TypeIndex
		if typeIdx == nil {
			ip := filepath.Join(l.Dir, indexFile)
			typeIdx, _ = LoadTypeIndex(ip)
		}
		if len(typeIdx) > 0 {
			offsets, exists := typeIdx[opts.Type]
			if !exists {
				// Index is populated but type is absent — no entries for this type.
				return nil, nil
			}
			results, err := queryByOffsets(ep, offsets, clauses, opts.Limit, opts.Scanned)
			if err == nil {
				return results, nil
			}
			// Stale index — fall through to full scan.
		}
	}

	// Index-backed path for untyped queries with filter or limit.
	if opts.Type == "" && (opts.Filter != "" || opts.Limit > 0) {
		ip := filepath.Join(l.Dir, indexFile)
		allOffsets, _ := LoadAllOffsets(ip)
		if len(allOffsets) > 0 {
			results, err := queryByOffsets(ep, allOffsets, clauses, opts.Limit, opts.Scanned)
			if err == nil {
				return results, nil
			}
			// Stale index — fall through to full scan.
		}
	}

	// Full scan: used when no type filter, or when index is empty/stale.
	iter, err := NewEntryIter(ep)
	if err != nil {
		return nil, fmt.Errorf("open entries: %w", err)
	}
	defer iter.Close()

	var results []*model.Entry
	for {
		e, err := iter.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read entry: %w", err)
		}
		if opts.Scanned != nil {
			*opts.Scanned++
		}
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

// QueryBatch executes multiple query specs under a single shared lock,
// amortizing the lock + file-open cost across all specs. Specs that have
// a type filter use the type index when available; remaining specs that
// require a full scan share a single streaming pass over the event log.
// Results are returned in the same order as the input specs.
func (l *Ledger) QueryBatch(specs []BatchQuerySpec) ([]BatchQueryResult, error) {
	return l.QueryBatchWithTypeIndex(specs, nil)
}

// QueryBatchWithTypeIndex is like QueryBatch but uses a pre-loaded type index
// instead of reading index.cbor from disk. When typeIdx is nil, it falls back
// to loading from disk (identical behaviour to QueryBatch).
func (l *Ledger) QueryBatchWithTypeIndex(specs []BatchQuerySpec, typeIdx map[string][]int64) ([]BatchQueryResult, error) {
	fl, err := lock.AcquireShared(l.Dir, lock.DefaultTimeout)
	if err != nil {
		return nil, fmt.Errorf("lock: %w", err)
	}
	defer fl.Unlock()

	ep := filepath.Join(l.Dir, eventsFile)

	// parsedSpec holds the pre-parsed filter clauses for a single spec.
	type parsedSpec struct {
		typ     string
		clauses []query.Clause
		limit   int
	}

	parsed := make([]parsedSpec, len(specs))
	for i, s := range specs {
		var clauses []query.Clause
		if s.Filter != "" {
			clauses, err = query.Parse(s.Filter)
			if err != nil {
				return nil, fmt.Errorf("spec %d: parse filter: %w", i, err)
			}
		}
		parsed[i] = parsedSpec{typ: s.Type, clauses: clauses, limit: s.Limit}
	}

	results := make([]BatchQueryResult, len(specs))
	for i := range results {
		results[i].Entries = make([]*model.Entry, 0)
	}

	// Fast path: use the type index for specs that have a type filter.
	// Prefer a pre-loaded in-memory index over disk reads.
	if typeIdx == nil {
		ip := filepath.Join(l.Dir, indexFile)
		typeIdx, _ = LoadTypeIndex(ip)
	}
	needsScan := make([]int, 0, len(specs))

	for i, ps := range parsed {
		if ps.typ != "" && len(typeIdx) > 0 {
			offsets, exists := typeIdx[ps.typ]
			if !exists {
				// Type absent from index — no entries; result is already empty.
				continue
			}
			entries, qErr := queryByOffsets(ep, offsets, ps.clauses, ps.limit, nil)
			if qErr == nil {
				if entries != nil {
					results[i].Entries = entries
				}
				continue
			}
			// Stale/corrupt index — fall through to full scan.
		}
		needsScan = append(needsScan, i)
	}

	if len(needsScan) == 0 {
		return results, nil
	}

	// Single streaming pass for all specs that need a full scan.
	iter, err := NewEntryIter(ep)
	if err != nil {
		return nil, fmt.Errorf("open entries: %w", err)
	}
	defer iter.Close()

	for {
		e, iterErr := iter.Next()
		if iterErr == io.EOF {
			break
		}
		if iterErr != nil {
			return nil, fmt.Errorf("read entry: %w", iterErr)
		}
		for _, i := range needsScan {
			ps := parsed[i]
			if ps.limit > 0 && len(results[i].Entries) >= ps.limit {
				continue
			}
			if ps.typ != "" && e.Type != ps.typ {
				continue
			}
			if len(ps.clauses) > 0 && !query.Evaluate(e, ps.clauses) {
				continue
			}
			results[i].Entries = append(results[i].Entries, e)
		}
	}

	return results, nil
}

// Get retrieves entries by ID. If history is false, returns only the latest
// version (highest timestamp). If history is true, returns all versions
// ordered by timestamp ascending.
//
// When history is false, Get uses the on-disk index for an O(1) seek directly
// to the entry's byte offset, avoiding a full file scan. If the index is
// empty or missing (pre-index ledger or corrupt file), it falls back to a
// streaming full scan. History queries always use the full scan because the
// index stores only one offset per ID (the last-written entry).
func (l *Ledger) Get(id string, history bool) ([]*model.Entry, error) {
	return l.GetWithIDIndex(id, history, nil)
}

// GetWithIDIndex is like Get but uses a pre-loaded ID index instead of reading
// index.cbor from disk. When idIdx is nil, it falls back to loading from disk
// (identical behaviour to Get). When idIdx is non-nil, it is treated as
// authoritative: an absent ID means the entry does not exist.
func (l *Ledger) GetWithIDIndex(id string, history bool, idIdx map[string]*IndexEntry) ([]*model.Entry, error) {
	fl, err := lock.AcquireShared(l.Dir, lock.DefaultTimeout)
	if err != nil {
		return nil, fmt.Errorf("lock: %w", err)
	}
	defer fl.Unlock()

	ep := filepath.Join(l.Dir, eventsFile)

	// Fast path: use index for non-history queries.
	if !history {
		if idIdx == nil {
			// Probe id_index.bin first for O(log N) binary search.
			iip := filepath.Join(l.Dir, idIndexFile)
			if offset, found, err := LookupIDIndex(iip, id); err == nil && found {
				entry, err := ReadEntryAt(ep, offset)
				if err == nil {
					return []*model.Entry{entry}, nil
				}
				// Stale offset — fall through to LoadIndex.
			}
			// id_index.bin miss or absent — fall back to full CBOR index.
			ip := filepath.Join(l.Dir, indexFile)
			idIdx, _ = LoadIndex(ip)
		}
		if len(idIdx) > 0 {
			ie, ok := idIdx[id]
			if !ok {
				// Index is populated but this ID is absent — entry does not exist.
				return nil, nil
			}
			entry, err := ReadEntryAt(ep, ie.Offset)
			if err == nil {
				return []*model.Entry{entry}, nil
			}
			// Index seek failed (e.g. stale offset after manual edit); fall
			// through to full scan below.
		}
	}

	// Full scan: required for history=true, or when index is empty/corrupt.
	iter, err := NewEntryIter(ep)
	if err != nil {
		return nil, fmt.Errorf("read entries: %w", err)
	}
	defer iter.Close()

	var matches []*model.Entry
	for {
		e, err := iter.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read entries: %w", err)
		}
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
// timestamp for each unique ID. It uses a copy-on-write strategy to minimise
// exclusive lock hold time:
//
//  1. Acquire a *shared* lock and snapshot the file size.
//  2. Two-pass streaming deduplication writes survivors to events.cbor.tmp
//     (memory: O(unique IDs × ~40 bytes), not O(N × entry_size)).
//  3. Release the shared lock.
//  4. Acquire an *exclusive* lock.
//  5. If events.cbor grew during step 2 (concurrent inserts), read the new
//     entries and append them to the temp file so they are not lost.
//  6. Backup the original, atomically rename temp, rebuild index.cbor.
//  7. Release the exclusive lock.
//
// The exclusive lock is held only for the rename (~µs) in the common case,
// instead of the full read-write cycle.
func (l *Ledger) Compact() (CompactStats, error) {
	ep := filepath.Join(l.Dir, eventsFile)
	tmpPath := ep + ".tmp"

	// ── Phase 1: shared lock — read and deduplicate ──────────────────────────
	sharedFL, err := lock.AcquireShared(l.Dir, lock.DefaultTimeout)
	if err != nil {
		return CompactStats{}, fmt.Errorf("shared lock: %w", err)
	}

	origInfo, err := os.Stat(ep)
	if err != nil {
		sharedFL.Unlock()
		return CompactStats{}, fmt.Errorf("stat events file: %w", err)
	}
	snapshotSize := origInfo.Size()

	// Pass 1: build maxTS (id → max timestamp) and lastMaxPos (id → stream
	// position of the entry with that max timestamp). Memory stays
	// O(unique IDs × ~40 bytes) rather than O(N × entry_size).
	iter, err := NewEntryIter(ep)
	if err != nil {
		sharedFL.Unlock()
		return CompactStats{}, fmt.Errorf("open entry iter: %w", err)
	}
	maxTS := make(map[string]int64)
	lastMaxPos := make(map[string]int)
	totalEntries := 0
	streamPos := 0
	for {
		e, err := iter.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			iter.Close()
			sharedFL.Unlock()
			return CompactStats{}, fmt.Errorf("read entry (pass 1): %w", err)
		}
		if ts, seen := maxTS[e.ID]; !seen || e.Timestamp >= ts {
			maxTS[e.ID] = e.Timestamp
			lastMaxPos[e.ID] = streamPos
		}
		totalEntries++
		streamPos++
	}
	iter.Close()

	uniqueIDs := len(maxTS)

	// No-op detection: every entry is already the latest version of its ID,
	// so compact would produce an identical file. Skip the rewrite entirely.
	// Release the shared lock before returning — no exclusive lock needed.
	if uniqueIDs == totalEntries {
		sharedFL.Unlock()
		return CompactStats{
			EntriesBefore: totalEntries,
			EntriesAfter:  uniqueIDs,
			BytesBefore:   snapshotSize,
			BytesAfter:    snapshotSize,
			NoOp:          true,
		}, nil
	}

	// Pass 2: stream entries again, writing only the survivor for each ID
	// directly to the temp file. Peak memory stays O(unique IDs × ~40 bytes).
	indexEntries, err := streamWriteSurvivorEntries(ep, tmpPath, lastMaxPos)
	if err != nil {
		sharedFL.Unlock()
		return CompactStats{}, fmt.Errorf("write temp file: %w", err)
	}

	// Release the shared lock before acquiring the exclusive lock, giving
	// concurrent readers and writers a chance to proceed.
	sharedFL.Unlock()

	// ── Phase 2: exclusive lock — merge concurrent appends + rename ──────────
	excFL, err := lock.AcquireExclusive(l.Dir, lock.DefaultTimeout)
	if err != nil {
		os.Remove(tmpPath)
		return CompactStats{}, fmt.Errorf("exclusive lock: %w", err)
	}
	exclusiveStart := time.Now()
	defer excFL.Unlock()

	// Build a mutable index map from the survivors written in phase 1.
	tempIndexMap := make(map[string]*IndexEntry, len(indexEntries))
	for _, ie := range indexEntries {
		tempIndexMap[ie.ID] = ie
	}

	// Check whether events.cbor was modified while we held the shared lock.
	currentInfo, err := os.Stat(ep)
	if err != nil {
		os.Remove(tmpPath)
		return CompactStats{}, fmt.Errorf("stat events file (exclusive): %w", err)
	}

	if currentInfo.Size() > snapshotSize {
		// New entries were appended during our shared-lock read phase. Read
		// them and append them to the temp file so they are not lost.
		newEntries, err := readEntriesFrom(ep, snapshotSize)
		if err != nil {
			os.Remove(tmpPath)
			return CompactStats{}, fmt.Errorf("read new entries: %w", err)
		}
		for _, e := range newEntries {
			offset, err := AppendEntry(tmpPath, e)
			if err != nil {
				os.Remove(tmpPath)
				return CompactStats{}, fmt.Errorf("append new entry to temp: %w", err)
			}
			// Update index: if this ID already has a survivor in the temp
			// file, the new entry (appended later) supersedes it.
			tempIndexMap[e.ID] = &IndexEntry{ID: e.ID, Type: e.Type, Offset: offset}
		}
		totalEntries += len(newEntries)
	}

	// Get compacted (+ merged) file size.
	tmpInfo, err := os.Stat(tmpPath)
	if err != nil {
		os.Remove(tmpPath)
		return CompactStats{}, fmt.Errorf("stat temp file: %w", err)
	}

	// Backup original.
	bakPath := ep + ".bak"
	if err := os.Rename(ep, bakPath); err != nil {
		os.Remove(tmpPath)
		return CompactStats{}, fmt.Errorf("backup original: %w", err)
	}

	// Atomic rename: temp becomes events.cbor.
	if err := os.Rename(tmpPath, ep); err != nil {
		_ = os.Rename(bakPath, ep) // attempt restore
		return CompactStats{}, fmt.Errorf("rename temp to events: %w", err)
	}

	// Rebuild index.cbor from the merged index map.
	finalIndex := make([]*IndexEntry, 0, len(tempIndexMap))
	for _, ie := range tempIndexMap {
		finalIndex = append(finalIndex, ie)
	}
	ip := filepath.Join(l.Dir, indexFile)
	if err := WriteIndex(ip, finalIndex); err != nil {
		return CompactStats{}, fmt.Errorf("rebuild index: %w", err)
	}
	iip := filepath.Join(l.Dir, idIndexFile)
	if err := WriteIDIndex(iip, finalIndex); err != nil {
		return CompactStats{}, fmt.Errorf("rebuild id index: %w", err)
	}

	return CompactStats{
		EntriesBefore:     totalEntries,
		EntriesAfter:      len(tempIndexMap),
		BytesBefore:       snapshotSize,
		BytesAfter:        tmpInfo.Size(),
		ExclusiveLockHeld: time.Since(exclusiveStart),
	}, nil
}

// Reindex rebuilds index.cbor from scratch by performing a full scan of
// events.cbor. This is used to recover from index drift (e.g. after a crash
// that left the index stale) or to create an index for a pre-index ledger.
// Returns the number of index entries written.
func (l *Ledger) Reindex() (int, error) {
	fl, err := lock.AcquireExclusive(l.Dir, lock.DefaultTimeout)
	if err != nil {
		return 0, fmt.Errorf("lock: %w", err)
	}
	defer fl.Unlock()

	ep := filepath.Join(l.Dir, eventsFile)
	ip := filepath.Join(l.Dir, indexFile)

	indexEntries, err := buildIndexFromScan(ep)
	if err != nil {
		return 0, fmt.Errorf("scan events: %w", err)
	}

	if err := WriteIndex(ip, indexEntries); err != nil {
		return 0, fmt.Errorf("write index: %w", err)
	}
	iip := filepath.Join(l.Dir, idIndexFile)
	if err := WriteIDIndex(iip, indexEntries); err != nil {
		return 0, fmt.Errorf("write id index: %w", err)
	}
	return len(indexEntries), nil
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

	// Stream entries to collect discovered types without loading all into memory.
	ep := filepath.Join(l.Dir, eventsFile)
	iter, err := NewEntryIter(ep)
	if err != nil {
		return nil, fmt.Errorf("read entries: %w", err)
	}
	defer iter.Close()

	discovered := make(map[string]bool)
	for {
		e, err := iter.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read entries: %w", err)
		}
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
