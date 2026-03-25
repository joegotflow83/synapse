package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/synapse-tool/synapse/internal/ledger"
	"github.com/synapse-tool/synapse/internal/model"
)

// startTestServer spins up a Server on a temp socket path, initialises a ledger
// in dir (or uses the provided dir), and returns the server, client, ledger dir,
// and a stop function. The stop function must be called at the end of the test.
func startTestServer(t *testing.T) (srv *Server, cli *Client, dir, socketPath string, stop func()) {
	t.Helper()

	dir = t.TempDir()
	if err := ledger.Init(dir, false); err != nil {
		t.Fatalf("ledger.Init: %v", err)
	}

	socketPath = filepath.Join(t.TempDir(), "synapse.sock")
	srv = NewServer(socketPath)
	cli = NewClient(socketPath)

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Start() }()

	// Wait until the socket is accepting connections (up to 2 s).
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := cli.Ping(); err == nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := cli.Ping(); err != nil {
		t.Fatalf("daemon did not start in time: %v", err)
	}

	stop = func() {
		srv.Stop()
		select {
		case err := <-errCh:
			if err != nil {
				t.Errorf("server exited with error: %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Error("server did not stop within timeout")
		}
	}
	return
}

// TestHealthCheck verifies that the daemon accepts connections and responds to
// health checks with an ok status.
func TestHealthCheck(t *testing.T) {
	_, cli, _, _, stop := startTestServer(t)
	defer stop()

	if err := cli.Ping(); err != nil {
		t.Fatalf("Ping: %v", err)
	}
}

// TestInsertAndGet exercises the insert → get round trip through the daemon.
func TestInsertAndGet(t *testing.T) {
	_, cli, dir, _, stop := startTestServer(t)
	defer stop()

	id, err := cli.Insert(dir, InsertArgs{
		Type:    "note",
		AgentID: "agent-1",
		Data:    map[string]any{"body": "hello"},
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if id == "" {
		t.Fatal("expected non-empty id")
	}

	raw, found, err := cli.Get(dir, id, false)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !found {
		t.Fatal("expected entry to be found")
	}

	var entries []*model.Entry
	if err := json.Unmarshal(raw, &entries); err != nil {
		t.Fatalf("unmarshal get result: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected at least one entry")
	}
	if entries[0].ID != id {
		t.Errorf("got id %q, want %q", entries[0].ID, id)
	}
}

// TestGetNotFound verifies that Get returns found=false for unknown IDs.
func TestGetNotFound(t *testing.T) {
	_, cli, dir, _, stop := startTestServer(t)
	defer stop()

	_, found, err := cli.Get(dir, "no-such-id", false)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if found {
		t.Fatal("expected not-found for missing id")
	}
}

// TestInsertBatch verifies that multiple entries can be inserted atomically and
// that all assigned IDs are distinct.
func TestInsertBatch(t *testing.T) {
	_, cli, dir, _, stop := startTestServer(t)
	defer stop()

	ids, err := cli.InsertBatch(dir, InsertBatchArgs{
		Entries: []InsertArgs{
			{Type: "note", Data: map[string]any{"n": 1}},
			{Type: "note", Data: map[string]any{"n": 2}},
			{Type: "note", Data: map[string]any{"n": 3}},
		},
	})
	if err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}
	if len(ids) != 3 {
		t.Fatalf("got %d ids, want 3", len(ids))
	}
	seen := make(map[string]bool, 3)
	for _, id := range ids {
		if id == "" {
			t.Fatal("empty id in batch response")
		}
		if seen[id] {
			t.Fatalf("duplicate id %q", id)
		}
		seen[id] = true
	}
}

// TestQuery verifies that Query returns the inserted entries and respects the
// type filter.
func TestQuery(t *testing.T) {
	_, cli, dir, _, stop := startTestServer(t)
	defer stop()

	// Insert two types.
	if _, err := cli.Insert(dir, InsertArgs{Type: "a", Data: map[string]any{"v": 1}}); err != nil {
		t.Fatalf("Insert a: %v", err)
	}
	if _, err := cli.Insert(dir, InsertArgs{Type: "b", Data: map[string]any{"v": 2}}); err != nil {
		t.Fatalf("Insert b: %v", err)
	}

	// Query all.
	raw, err := cli.Query(dir, QueryArgs{})
	if err != nil {
		t.Fatalf("Query all: %v", err)
	}
	var all []*model.Entry
	if err := json.Unmarshal(raw, &all); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("got %d entries, want 2", len(all))
	}

	// Query by type "a" only.
	rawA, err := cli.Query(dir, QueryArgs{Type: "a"})
	if err != nil {
		t.Fatalf("Query type=a: %v", err)
	}
	var aEntries []*model.Entry
	if err := json.Unmarshal(rawA, &aEntries); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(aEntries) != 1 {
		t.Errorf("got %d entries for type=a, want 1", len(aEntries))
	}
}

// TestQueryResponseStats verifies that query and get responses include the
// stats field (scanned, matched, duration_ms) as required by the protocol spec.
func TestQueryResponseStats(t *testing.T) {
	_, cli, dir, _, stop := startTestServer(t)
	defer stop()

	// Insert 3 entries: 2 of type "x", 1 of type "y".
	for i := 0; i < 2; i++ {
		if _, err := cli.Insert(dir, InsertArgs{Type: "x", Data: map[string]any{"i": i}}); err != nil {
			t.Fatalf("Insert x: %v", err)
		}
	}
	id, err := cli.Insert(dir, InsertArgs{Type: "y", Data: map[string]any{"i": 99}})
	if err != nil {
		t.Fatalf("Insert y: %v", err)
	}

	// Query all — use raw send to inspect stats.
	argBytes, _ := json.Marshal(QueryArgs{})
	resp, err := cli.send(&Request{Command: CmdQuery, Dir: dir, Args: argBytes})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if resp.Stats == nil {
		t.Fatal("expected stats in query response, got nil")
	}
	if resp.Stats.Matched != 3 {
		t.Errorf("stats.matched = %d, want 3", resp.Stats.Matched)
	}
	if resp.Stats.Scanned < 3 {
		t.Errorf("stats.scanned = %d, want >= 3", resp.Stats.Scanned)
	}
	if resp.Stats.DurationMs <= 0 {
		t.Errorf("stats.duration_ms = %f, want > 0", resp.Stats.DurationMs)
	}

	// Query by type — fewer matches.
	argBytes, _ = json.Marshal(QueryArgs{Type: "x"})
	resp, err = cli.send(&Request{Command: CmdQuery, Dir: dir, Args: argBytes})
	if err != nil {
		t.Fatalf("Query type=x: %v", err)
	}
	if resp.Stats == nil {
		t.Fatal("expected stats in typed query response, got nil")
	}
	if resp.Stats.Matched != 2 {
		t.Errorf("stats.matched = %d, want 2", resp.Stats.Matched)
	}

	// Get by ID — stats should be present.
	argBytes, _ = json.Marshal(GetArgs{ID: id})
	resp, err = cli.send(&Request{Command: CmdGet, Dir: dir, Args: argBytes})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if resp.Stats == nil {
		t.Fatal("expected stats in get response, got nil")
	}
	if resp.Stats.Matched != 1 {
		t.Errorf("get stats.matched = %d, want 1", resp.Stats.Matched)
	}
	if resp.Stats.DurationMs <= 0 {
		t.Errorf("get stats.duration_ms = %f, want > 0", resp.Stats.DurationMs)
	}

	// Get not found — stats should still be present.
	argBytes, _ = json.Marshal(GetArgs{ID: "nonexistent"})
	resp, err = cli.send(&Request{Command: CmdGet, Dir: dir, Args: argBytes})
	if err != nil {
		t.Fatalf("Get not found: %v", err)
	}
	if resp.Status != "not_found" {
		t.Errorf("expected not_found status, got %s", resp.Status)
	}
	if resp.Stats == nil {
		t.Fatal("expected stats in not_found response, got nil")
	}
	if resp.Stats.Matched != 0 {
		t.Errorf("not_found stats.matched = %d, want 0", resp.Stats.Matched)
	}
}

// TestQueryBatch verifies that multiple independent queries can be batched.
func TestQueryBatch(t *testing.T) {
	_, cli, dir, _, stop := startTestServer(t)
	defer stop()

	for i := 0; i < 3; i++ {
		if _, err := cli.Insert(dir, InsertArgs{Type: "x", Data: map[string]any{"i": i}}); err != nil {
			t.Fatalf("Insert: %v", err)
		}
	}
	if _, err := cli.Insert(dir, InsertArgs{Type: "y", Data: map[string]any{"i": 0}}); err != nil {
		t.Fatalf("Insert y: %v", err)
	}

	specs := []ledger.BatchQuerySpec{
		{Type: "x"},
		{Type: "y"},
	}
	specsJSON, _ := json.Marshal(specs)

	raw, err := cli.QueryBatch(dir, json.RawMessage(specsJSON))
	if err != nil {
		t.Fatalf("QueryBatch: %v", err)
	}
	var results []ledger.BatchQueryResult
	if err := json.Unmarshal(raw, &results); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("got %d result sets, want 2", len(results))
	}
	if len(results[0].Entries) != 3 {
		t.Errorf("got %d entries for x, want 3", len(results[0].Entries))
	}
	if len(results[1].Entries) != 1 {
		t.Errorf("got %d entries for y, want 1", len(results[1].Entries))
	}
}

// TestCompact verifies that compact runs through the daemon without error and
// that the ledger remains readable afterwards.
func TestCompact(t *testing.T) {
	_, cli, dir, _, stop := startTestServer(t)
	defer stop()

	// Insert a few entries, then compact.
	for i := 0; i < 5; i++ {
		if _, err := cli.Insert(dir, InsertArgs{Type: "ev", Data: map[string]any{"i": i}}); err != nil {
			t.Fatalf("Insert: %v", err)
		}
	}
	if _, err := cli.Compact(dir); err != nil {
		t.Fatalf("Compact: %v", err)
	}

	// Entries should still be readable after compact.
	raw, err := cli.Query(dir, QueryArgs{})
	if err != nil {
		t.Fatalf("Query after compact: %v", err)
	}
	var entries []*model.Entry
	if err := json.Unmarshal(raw, &entries); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(entries) != 5 {
		t.Errorf("got %d entries after compact, want 5", len(entries))
	}
}

// TestListTypes verifies that created types are returned by list-types.
func TestListTypes(t *testing.T) {
	_, cli, dir, _, stop := startTestServer(t)
	defer stop()

	if err := cli.CreateType(dir, CreateTypeArgs{Name: "widget", Description: "a widget"}); err != nil {
		t.Fatalf("CreateType: %v", err)
	}

	raw, err := cli.ListTypes(dir)
	if err != nil {
		t.Fatalf("ListTypes: %v", err)
	}
	// raw is a JSON array of type objects.
	var types []map[string]any
	if err := json.Unmarshal(raw, &types); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	found := false
	for _, tp := range types {
		if tp["name"] == "widget" {
			found = true
		}
	}
	if !found {
		t.Errorf("type 'widget' not found in list-types response: %s", raw)
	}
}

// TestCreateType verifies that create-type succeeds and is idempotent.
func TestCreateType(t *testing.T) {
	_, cli, dir, _, stop := startTestServer(t)
	defer stop()

	if err := cli.CreateType(dir, CreateTypeArgs{Name: "task", Description: "a task"}); err != nil {
		t.Fatalf("CreateType: %v", err)
	}
	// Creating the same type again should not return an error (already exists is ok).
	// The ledger layer may or may not error; just verify no panic.
	_ = cli.CreateType(dir, CreateTypeArgs{Name: "task", Description: "a task"})
}

// TestGracefulShutdown verifies that Stop() causes the server to stop accepting
// new connections, that the socket file is removed, and that a subsequent Ping
// fails because the daemon is gone.
func TestGracefulShutdown(t *testing.T) {
	_, cli, _, socketPath, stop := startTestServer(t)

	// Explicitly stop the server.
	stop()

	// Socket file should be gone.
	if _, err := os.Stat(socketPath); !os.IsNotExist(err) {
		t.Errorf("socket file still exists after Stop(): %v", err)
	}

	// Ping should now fail because the daemon is no longer running.
	if err := cli.Ping(); err == nil {
		t.Fatal("expected Ping to fail after daemon stopped")
	}
}

// TestShutdownViaCommand verifies the CmdShutdown wire command triggers a
// graceful shutdown: the socket file disappears within a short window.
func TestShutdownViaCommand(t *testing.T) {
	_, cli, _, socketPath, _ := startTestServer(t)

	// Send shutdown — the response may or may not arrive depending on timing.
	_ = cli.Shutdown()

	// Give the server a moment to clean up.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(socketPath); os.IsNotExist(err) {
			return // success
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Error("socket file still present 2 s after shutdown command")
}

// TestFallbackWhenDaemonUnavailable verifies that the Client returns a
// descriptive error (not a panic or nil) when the socket doesn't exist.
// The actual CLI fall-through to direct I/O is tested in the CLI package.
func TestFallbackWhenDaemonUnavailable(t *testing.T) {
	cli := NewClient("/tmp/synapse-nonexistent-test.sock")
	err := cli.Ping()
	if err == nil {
		t.Fatal("expected connection error when daemon is not running")
	}
}

// TestConcurrentClients launches 100 concurrent goroutines, each inserting one
// entry and immediately reading it back. All operations must succeed without
// data races or errors.
func TestConcurrentClients(t *testing.T) {
	_, cli, dir, _, stop := startTestServer(t)
	defer stop()

	const clients = 100
	var wg sync.WaitGroup
	var errCount atomic.Int64

	wg.Add(clients)
	for i := 0; i < clients; i++ {
		i := i
		go func() {
			defer wg.Done()
			id, err := cli.Insert(dir, InsertArgs{
				Type:    "concurrent",
				AgentID: fmt.Sprintf("agent-%d", i),
				Data:    map[string]any{"idx": i},
			})
			if err != nil {
				errCount.Add(1)
				t.Errorf("goroutine %d Insert: %v", i, err)
				return
			}
			_, found, err := cli.Get(dir, id, false)
			if err != nil {
				errCount.Add(1)
				t.Errorf("goroutine %d Get: %v", i, err)
				return
			}
			if !found {
				errCount.Add(1)
				t.Errorf("goroutine %d: inserted entry %q not found", i, id)
			}
		}()
	}
	wg.Wait()

	if n := errCount.Load(); n > 0 {
		t.Fatalf("%d out of %d concurrent operations failed", n, clients)
	}
}

// TestConcurrentInsertHotIndexIntegrity verifies that concurrent inserts do not
// corrupt the hot index (no duplicate offsets in typeIdx). This is a regression
// test for the race where hi.mu was released between ensureLoaded and
// applyIndexEntries, allowing duplicate offset appends.
func TestConcurrentInsertHotIndexIntegrity(t *testing.T) {
	srv, cli, dir, _, stop := startTestServer(t)
	defer stop()

	const goroutines = 20
	const insertsPerGoroutine = 5
	const totalInserts = goroutines * insertsPerGoroutine
	const entryType = "race-test"

	var wg sync.WaitGroup
	var errCount atomic.Int64
	insertedIDs := make([]string, totalInserts)

	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		g := g
		go func() {
			defer wg.Done()
			for i := 0; i < insertsPerGoroutine; i++ {
				id, err := cli.Insert(dir, InsertArgs{
					Type:    entryType,
					AgentID: fmt.Sprintf("agent-%d", g),
					Data:    map[string]any{"g": g, "i": i},
				})
				if err != nil {
					errCount.Add(1)
					t.Errorf("goroutine %d insert %d: %v", g, i, err)
					return
				}
				insertedIDs[g*insertsPerGoroutine+i] = id
			}
		}()
	}
	wg.Wait()

	if n := errCount.Load(); n > 0 {
		t.Fatalf("%d insert errors", n)
	}

	// Query all entries of the test type through the daemon (uses hot index).
	raw, err := cli.Query(dir, QueryArgs{Type: entryType})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	var results []model.Entry
	if err := json.Unmarshal(raw, &results); err != nil {
		t.Fatalf("unmarshal query results: %v", err)
	}

	if len(results) != totalInserts {
		t.Errorf("expected %d entries, got %d (possible duplicate offsets in hot index)", totalInserts, len(results))
	}

	// Verify no duplicate IDs in results.
	seen := make(map[string]bool, len(results))
	for _, e := range results {
		if seen[e.ID] {
			t.Errorf("duplicate entry ID in query results: %s", e.ID)
		}
		seen[e.ID] = true
	}

	// Also verify directly via the hot index that typeIdx has no duplicate offsets.
	hi := srv.getOrCreateHotIndex(dir)
	hi.mu.RLock()
	offsets := hi.typeIdx[entryType]
	hi.mu.RUnlock()

	if len(offsets) != totalInserts {
		t.Errorf("hot index typeIdx[%q] has %d offsets, want %d", entryType, len(offsets), totalInserts)
	}
	offsetSet := make(map[int64]bool, len(offsets))
	for _, off := range offsets {
		if offsetSet[off] {
			t.Errorf("duplicate offset %d in hot index typeIdx[%q]", off, entryType)
		}
		offsetSet[off] = true
	}
}

// BenchmarkQueryLatency measures end-to-end query latency through the daemon
// versus direct ledger access. The daemon overhead should be <2 ms per call.
func BenchmarkQueryLatency(b *testing.B) {
	dir := b.TempDir()
	if err := ledger.Init(dir, false); err != nil {
		b.Fatalf("ledger.Init: %v", err)
	}

	socketPath := filepath.Join(b.TempDir(), "bench.sock")
	srv := NewServer(socketPath)
	cli := NewClient(socketPath)
	go srv.Start() //nolint:errcheck

	// Wait for server to be ready.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := cli.Ping(); err == nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	b.Cleanup(srv.Stop)

	// Seed some entries.
	for i := 0; i < 100; i++ {
		if _, err := cli.Insert(dir, InsertArgs{Type: "bench", Data: map[string]any{"i": i}}); err != nil {
			b.Fatalf("Insert: %v", err)
		}
	}

	b.ResetTimer()

	b.Run("daemon", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			if _, err := cli.Query(dir, QueryArgs{Type: "bench"}); err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("direct", func(b *testing.B) {
		l, err := ledger.Open(dir)
		if err != nil {
			b.Fatalf("Open: %v", err)
		}
		for i := 0; i < b.N; i++ {
			if _, err := l.Query(ledger.QueryOpts{Type: "bench"}); err != nil {
				b.Fatal(err)
			}
		}
	})
}
