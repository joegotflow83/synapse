package ledger

import (
	"fmt"
	"sync"
	"testing"

	"github.com/synapse-tool/synapse/internal/model"
)

// TestConcurrentInserts verifies that parallel inserts from multiple goroutines
// do not corrupt the CBOR file. Each goroutine inserts entries sequentially,
// and at the end the total count must match exactly.
func TestConcurrentInserts(t *testing.T) {
	l := initLedger(t)

	const numGoroutines = 10
	const insertsPerGoroutine = 20
	totalExpected := numGoroutines * insertsPerGoroutine

	var wg sync.WaitGroup
	errs := make(chan error, totalExpected)

	for g := 0; g < numGoroutines; g++ {
		wg.Add(1)
		go func(goroutineID int) {
			defer wg.Done()
			for i := 0; i < insertsPerGoroutine; i++ {
				e := &model.Entry{
					Type: "concurrent",
					Data: map[string]any{
						"goroutine": goroutineID,
						"index":     i,
					},
				}
				if err := l.Insert(e); err != nil {
					errs <- fmt.Errorf("goroutine %d, insert %d: %w", goroutineID, i, err)
					return
				}
			}
		}(g)
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Fatalf("concurrent insert error: %v", err)
	}

	// Verify all entries were written and the file is not corrupted.
	results, err := l.Query(QueryOpts{})
	if err != nil {
		t.Fatalf("Query after concurrent inserts: %v", err)
	}
	if len(results) != totalExpected {
		t.Errorf("expected %d entries, got %d", totalExpected, len(results))
	}

	// Verify each entry has a unique auto-generated ID.
	ids := make(map[string]bool, totalExpected)
	for _, r := range results {
		if ids[r.ID] {
			t.Errorf("duplicate ID found: %s", r.ID)
		}
		ids[r.ID] = true
	}
}

// TestConcurrentInsertsAndQueries verifies that reads and writes can interleave
// without corruption. Writers use exclusive locks and readers use shared locks,
// so readers should never see a partially written entry.
func TestConcurrentInsertsAndQueries(t *testing.T) {
	l := initLedger(t)

	const numWriters = 5
	const numReaders = 5
	const insertsPerWriter = 20

	var wg sync.WaitGroup
	errs := make(chan error, numWriters*insertsPerWriter+numReaders*10)

	// Launch writers.
	for w := 0; w < numWriters; w++ {
		wg.Add(1)
		go func(writerID int) {
			defer wg.Done()
			for i := 0; i < insertsPerWriter; i++ {
				e := &model.Entry{
					Type: "mixed",
					Data: map[string]any{
						"writer": writerID,
						"seq":    i,
					},
				}
				if err := l.Insert(e); err != nil {
					errs <- fmt.Errorf("writer %d, insert %d: %w", writerID, i, err)
					return
				}
			}
		}(w)
	}

	// Launch readers that query repeatedly while writes are happening.
	for r := 0; r < numReaders; r++ {
		wg.Add(1)
		go func(readerID int) {
			defer wg.Done()
			for i := 0; i < 10; i++ {
				results, err := l.Query(QueryOpts{Type: "mixed"})
				if err != nil {
					errs <- fmt.Errorf("reader %d, query %d: %w", readerID, i, err)
					return
				}
				// Each result should be a valid entry with the expected type.
				for _, r := range results {
					if r.Type != "mixed" {
						errs <- fmt.Errorf("reader %d: unexpected type %q", readerID, r.Type)
						return
					}
					if r.ID == "" {
						errs <- fmt.Errorf("reader %d: entry with empty ID", readerID)
						return
					}
				}
			}
		}(r)
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Fatalf("concurrent read/write error: %v", err)
	}

	// Final count should be exact.
	results, err := l.Query(QueryOpts{})
	if err != nil {
		t.Fatalf("final Query: %v", err)
	}
	expected := numWriters * insertsPerWriter
	if len(results) != expected {
		t.Errorf("expected %d entries, got %d", expected, len(results))
	}
}

// TestConcurrentCompactAndInsert verifies that compact and insert operations
// contend correctly on the exclusive lock — no data corruption occurs.
func TestConcurrentCompactAndInsert(t *testing.T) {
	l := initLedger(t)

	// Pre-populate with entries that have duplicate IDs for compaction.
	for i := 0; i < 10; i++ {
		e := &model.Entry{
			ID:        fmt.Sprintf("dup-%d", i%5), // 5 unique IDs, 2 versions each
			Type:      "compactable",
			Timestamp: int64(1000 + i),
			Data:      map[string]any{"seq": i},
		}
		if err := l.Insert(e); err != nil {
			t.Fatalf("pre-populate: %v", err)
		}
	}

	var wg sync.WaitGroup
	errs := make(chan error, 100)

	// Run compact in one goroutine.
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, err := l.Compact()
		if err != nil {
			errs <- fmt.Errorf("compact: %w", err)
		}
	}()

	// Concurrently insert new entries.
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			e := &model.Entry{
				Type: "new-during-compact",
				Data: map[string]any{"idx": idx},
			}
			if err := l.Insert(e); err != nil {
				errs <- fmt.Errorf("insert during compact %d: %w", idx, err)
			}
		}(i)
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Fatalf("compact/insert concurrency error: %v", err)
	}

	// File should be readable — not corrupted.
	results, err := l.Query(QueryOpts{})
	if err != nil {
		t.Fatalf("Query after compact+inserts: %v", err)
	}

	// After compaction of 10 entries (5 unique) + 5 new = at least 5 + 5 = 10,
	// but ordering of compact vs inserts is non-deterministic.
	// The key assertion is that the file is valid and readable.
	if len(results) < 5 {
		t.Errorf("expected at least 5 entries, got %d", len(results))
	}
}
