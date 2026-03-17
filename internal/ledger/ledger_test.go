package ledger

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/synapse-tool/synapse/internal/model"
	"github.com/synapse-tool/synapse/internal/types"
)

// initLedger creates an initialized Synapse directory in a temp dir and returns a Ledger.
func initLedger(t *testing.T) *Ledger {
	t.Helper()
	dir := t.TempDir()
	if err := Init(dir, false); err != nil {
		t.Fatalf("Init: %v", err)
	}
	l, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return l
}

func TestOpen_UninitializedDir(t *testing.T) {
	dir := t.TempDir()
	_, err := Open(dir)
	if err == nil {
		t.Fatal("expected error for uninitialized directory")
	}
}

func TestInit_CreatesFiles(t *testing.T) {
	dir := t.TempDir()
	if err := Init(dir, false); err != nil {
		t.Fatalf("Init: %v", err)
	}
	// Verify both files exist by opening the ledger.
	if _, err := Open(dir); err != nil {
		t.Fatalf("Open after Init: %v", err)
	}
}

func TestInit_RejectsReinitWithoutForce(t *testing.T) {
	dir := t.TempDir()
	if err := Init(dir, false); err != nil {
		t.Fatalf("Init: %v", err)
	}
	err := Init(dir, false)
	if err == nil {
		t.Fatal("expected error when reinitializing without --force")
	}
}

func TestInit_AllowsForceReinit(t *testing.T) {
	dir := t.TempDir()
	if err := Init(dir, false); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := Init(dir, true); err != nil {
		t.Fatalf("Init with force: %v", err)
	}
}

func TestInsertAndQuery_RoundTrip(t *testing.T) {
	l := initLedger(t)

	entry := &model.Entry{
		Type: "task",
		Data: map[string]any{"title": "Buy milk"},
	}
	if err := l.Insert(entry); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	// Entry should have auto-generated ID and timestamp after insert.
	if entry.ID == "" {
		t.Fatal("expected auto-generated ID")
	}

	results, err := l.Query(QueryOpts{})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].ID != entry.ID {
		t.Errorf("ID: got %q, want %q", results[0].ID, entry.ID)
	}
	if results[0].Data["title"] != "Buy milk" {
		t.Errorf("Data[title]: got %v, want 'Buy milk'", results[0].Data["title"])
	}
}

func TestQuery_FilterByType(t *testing.T) {
	l := initLedger(t)

	for _, typ := range []string{"task", "note", "task"} {
		e := &model.Entry{Type: typ, Data: map[string]any{"t": typ}}
		if err := l.Insert(e); err != nil {
			t.Fatalf("Insert: %v", err)
		}
	}

	results, err := l.Query(QueryOpts{Type: "task"})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(results))
	}
}

func TestQuery_WithLimit(t *testing.T) {
	l := initLedger(t)

	for i := 0; i < 5; i++ {
		e := &model.Entry{Type: "event"}
		if err := l.Insert(e); err != nil {
			t.Fatalf("Insert: %v", err)
		}
	}

	results, err := l.Query(QueryOpts{Limit: 3})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 results with limit, got %d", len(results))
	}
}

func TestQuery_WithFilter(t *testing.T) {
	l := initLedger(t)

	e1 := &model.Entry{Type: "task", Data: map[string]any{"status": "open"}}
	e2 := &model.Entry{Type: "task", Data: map[string]any{"status": "closed"}}
	if err := l.Insert(e1); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if err := l.Insert(e2); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	results, err := l.Query(QueryOpts{Filter: "status=open"})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Data["status"] != "open" {
		t.Errorf("got status=%v, want 'open'", results[0].Data["status"])
	}
}

func TestInsert_RejectsEmptyType(t *testing.T) {
	l := initLedger(t)
	e := &model.Entry{Data: map[string]any{"key": "val"}}
	err := l.Insert(e)
	if err == nil {
		t.Fatal("expected error for empty type")
	}
}

func TestGet_WithoutHistory_ReturnsLatest(t *testing.T) {
	l := initLedger(t)

	// Insert two versions of the same ID with different timestamps.
	e1 := &model.Entry{ID: "versioned", Type: "doc", Timestamp: 1000, Data: map[string]any{"v": "1"}}
	e2 := &model.Entry{ID: "versioned", Type: "doc", Timestamp: 2000, Data: map[string]any{"v": "2"}}
	if err := l.Insert(e1); err != nil {
		t.Fatalf("Insert v1: %v", err)
	}
	if err := l.Insert(e2); err != nil {
		t.Fatalf("Insert v2: %v", err)
	}

	results, err := l.Get("versioned", false)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result (latest), got %d", len(results))
	}
	if results[0].Data["v"] != "2" {
		t.Errorf("expected latest version (v=2), got v=%v", results[0].Data["v"])
	}
}

func TestGet_WithHistory_ReturnsAllVersions(t *testing.T) {
	l := initLedger(t)

	e1 := &model.Entry{ID: "versioned", Type: "doc", Timestamp: 1000, Data: map[string]any{"v": "1"}}
	e2 := &model.Entry{ID: "versioned", Type: "doc", Timestamp: 2000, Data: map[string]any{"v": "2"}}
	e3 := &model.Entry{ID: "versioned", Type: "doc", Timestamp: 1500, Data: map[string]any{"v": "1.5"}}
	for _, e := range []*model.Entry{e1, e2, e3} {
		if err := l.Insert(e); err != nil {
			t.Fatalf("Insert: %v", err)
		}
	}

	results, err := l.Get("versioned", true)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 versions, got %d", len(results))
	}
	// Should be sorted by timestamp ascending.
	if results[0].Timestamp != 1000 || results[1].Timestamp != 1500 || results[2].Timestamp != 2000 {
		t.Errorf("timestamps not ascending: %d, %d, %d",
			results[0].Timestamp, results[1].Timestamp, results[2].Timestamp)
	}
}

func TestGet_NotFound_ReturnsNil(t *testing.T) {
	l := initLedger(t)

	results, err := l.Get("nonexistent", false)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if results != nil {
		t.Fatalf("expected nil for not-found ID, got %d entries", len(results))
	}
}

func TestCompact_DeduplicatesByID(t *testing.T) {
	l := initLedger(t)

	// Insert 3 versions of ID "x" and 1 of ID "y".
	entries := []*model.Entry{
		{ID: "x", Type: "task", Timestamp: 100, Data: map[string]any{"v": "1"}},
		{ID: "x", Type: "task", Timestamp: 300, Data: map[string]any{"v": "3"}},
		{ID: "x", Type: "task", Timestamp: 200, Data: map[string]any{"v": "2"}},
		{ID: "y", Type: "note", Timestamp: 150, Data: map[string]any{"msg": "hello"}},
	}
	for _, e := range entries {
		if err := l.Insert(e); err != nil {
			t.Fatalf("Insert: %v", err)
		}
	}

	stats, err := l.Compact()
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}

	if stats.EntriesBefore != 4 {
		t.Errorf("EntriesBefore: got %d, want 4", stats.EntriesBefore)
	}
	if stats.EntriesAfter != 2 {
		t.Errorf("EntriesAfter: got %d, want 2", stats.EntriesAfter)
	}
	if stats.BytesAfter >= stats.BytesBefore {
		t.Errorf("expected bytes to decrease: before=%d, after=%d", stats.BytesBefore, stats.BytesAfter)
	}

	// Verify only latest versions survive.
	results, err := l.Query(QueryOpts{})
	if err != nil {
		t.Fatalf("Query after compact: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 entries after compact, got %d", len(results))
	}

	// Find entry "x" and verify it's the latest version.
	for _, r := range results {
		if r.ID == "x" {
			if r.Timestamp != 300 {
				t.Errorf("expected x timestamp=300 (latest), got %d", r.Timestamp)
			}
			if r.Data["v"] != "3" {
				t.Errorf("expected x v=3, got %v", r.Data["v"])
			}
		}
	}

	// After compact, --history should return only the surviving entry.
	history, err := l.Get("x", true)
	if err != nil {
		t.Fatalf("Get history after compact: %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("expected 1 entry for x after compact, got %d", len(history))
	}
}

func TestCompact_CreatesBackup(t *testing.T) {
	l := initLedger(t)

	e := &model.Entry{ID: "a", Type: "task", Timestamp: 100}
	if err := l.Insert(e); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	if _, err := l.Compact(); err != nil {
		t.Fatalf("Compact: %v", err)
	}

	// Check backup file exists.
	bakPath := filepath.Join(l.Dir, eventsFile+".bak")
	if _, err := readFileSize(bakPath); err != nil {
		t.Fatalf("backup file should exist: %v", err)
	}
}

func TestListTypes_MergesRegisteredAndDiscovered(t *testing.T) {
	l := initLedger(t)

	// Register a type via types.cbor.
	tp := filepath.Join(l.Dir, typesFile)
	if err := types.CreateType(tp, "task", "A task to do", `{"status":"open"}`); err != nil {
		t.Fatalf("CreateType: %v", err)
	}

	// Insert entries with types "task" (registered) and "note" (discovered only).
	e1 := &model.Entry{Type: "task", Data: map[string]any{"title": "Do stuff"}}
	e2 := &model.Entry{Type: "note", Data: map[string]any{"msg": "hello"}}
	if err := l.Insert(e1); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if err := l.Insert(e2); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	typeInfos, err := l.ListTypes()
	if err != nil {
		t.Fatalf("ListTypes: %v", err)
	}

	if len(typeInfos) != 2 {
		t.Fatalf("expected 2 types, got %d", len(typeInfos))
	}

	// Types should be sorted by name.
	typeMap := make(map[string]TypeInfo)
	for _, ti := range typeInfos {
		typeMap[ti.Name] = ti
	}

	// "task" should be registered with metadata.
	taskInfo, ok := typeMap["task"]
	if !ok {
		t.Fatal("expected 'task' type")
	}
	if !taskInfo.Registered {
		t.Error("expected 'task' to be marked as registered")
	}
	if taskInfo.Description != "A task to do" {
		t.Errorf("task description: got %q, want 'A task to do'", taskInfo.Description)
	}

	// "note" should be discovered only (not registered).
	noteInfo, ok := typeMap["note"]
	if !ok {
		t.Fatal("expected 'note' type")
	}
	if noteInfo.Registered {
		t.Error("expected 'note' to NOT be marked as registered")
	}
	if noteInfo.Description != "" {
		t.Errorf("note description: got %q, want empty", noteInfo.Description)
	}
}

func TestListTypes_EmptyLedger(t *testing.T) {
	l := initLedger(t)

	typeInfos, err := l.ListTypes()
	if err != nil {
		t.Fatalf("ListTypes: %v", err)
	}
	if len(typeInfos) != 0 {
		t.Fatalf("expected 0 types, got %d", len(typeInfos))
	}
}

// readFileSize is a helper that stats a file and returns its size.
func readFileSize(path string) (int64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}
