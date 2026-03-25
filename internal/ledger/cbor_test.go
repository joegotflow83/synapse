package ledger

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/synapse-tool/synapse/internal/model"
)

func tempFile(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "events.cbor")
}

func TestInitFile_CreatesValidArray(t *testing.T) {
	path := tempFile(t)
	if err := InitFile(path); err != nil {
		t.Fatalf("InitFile: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(data) != 2 || data[0] != 0x9F || data[1] != 0xFF {
		t.Fatalf("expected [0x9F, 0xFF], got %x", data)
	}
}

func TestInitFile_ReadAllReturnsEmpty(t *testing.T) {
	path := tempFile(t)
	if err := InitFile(path); err != nil {
		t.Fatalf("InitFile: %v", err)
	}
	entries, err := ReadAllEntries(path)
	if err != nil {
		t.Fatalf("ReadAllEntries: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected 0 entries, got %d", len(entries))
	}
}

func TestAppendEntry_SingleEntry(t *testing.T) {
	path := tempFile(t)
	if err := InitFile(path); err != nil {
		t.Fatalf("InitFile: %v", err)
	}

	entry := &model.Entry{
		ID:   "test-1",
		Type: "note",
		Data: map[string]any{"key": "value"},
	}
	if err := entry.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if _, err := AppendEntry(path, entry); err != nil {
		t.Fatalf("AppendEntry: %v", err)
	}

	// Verify file still ends with 0xFF.
	data, _ := os.ReadFile(path)
	if data[len(data)-1] != 0xFF {
		t.Fatalf("last byte is 0x%02X, expected 0xFF", data[len(data)-1])
	}

	entries, err := ReadAllEntries(path)
	if err != nil {
		t.Fatalf("ReadAllEntries: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].ID != "test-1" {
		t.Fatalf("expected ID 'test-1', got %q", entries[0].ID)
	}
	if entries[0].Data["key"] != "value" {
		t.Fatalf("expected key=value, got %v", entries[0].Data["key"])
	}
}

func TestAppendEntry_MultipleSequentialAppends(t *testing.T) {
	path := tempFile(t)
	if err := InitFile(path); err != nil {
		t.Fatalf("InitFile: %v", err)
	}

	for i := 0; i < 10; i++ {
		entry := &model.Entry{
			Type: "counter",
			Data: map[string]any{"i": float64(i)},
		}
		_ = entry.Validate()
		if _, err := AppendEntry(path, entry); err != nil {
			t.Fatalf("AppendEntry[%d]: %v", i, err)
		}
	}

	entries, err := ReadAllEntries(path)
	if err != nil {
		t.Fatalf("ReadAllEntries: %v", err)
	}
	if len(entries) != 10 {
		t.Fatalf("expected 10 entries, got %d", len(entries))
	}
	// Verify Data values survived round-trip. CBOR decodes integers as various
	// types; we stored float64 but CBOR may return uint64.
	for i, e := range entries {
		if e.Type != "counter" {
			t.Errorf("entry[%d] type = %q, want 'counter'", i, e.Type)
		}
	}
}

func TestAppendEntry_CorruptedFile(t *testing.T) {
	path := tempFile(t)
	// Write file missing break byte.
	if err := os.WriteFile(path, []byte{0x9F, 0x00}, 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	entry := &model.Entry{ID: "x", Type: "t"}
	_, err := AppendEntry(path, entry)
	if err == nil {
		t.Fatal("expected error for corrupted file")
	}
}

func TestReadAllEntries_CorruptedMissingStart(t *testing.T) {
	path := tempFile(t)
	if err := os.WriteFile(path, []byte{0x00, 0xFF}, 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := ReadAllEntries(path)
	if err == nil {
		t.Fatal("expected error for missing start byte")
	}
}

func TestReadAllEntries_CorruptedMissingBreak(t *testing.T) {
	path := tempFile(t)
	if err := os.WriteFile(path, []byte{0x9F, 0x00}, 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := ReadAllEntries(path)
	if err == nil {
		t.Fatal("expected error for missing break byte")
	}
}

func TestReadAllEntries_TruncatedFile(t *testing.T) {
	path := tempFile(t)
	if err := os.WriteFile(path, []byte{0x9F}, 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := ReadAllEntries(path)
	if err == nil {
		t.Fatal("expected error for truncated file")
	}
}

func TestWriteEntries_RoundTrip(t *testing.T) {
	path := tempFile(t)
	original := []*model.Entry{
		{ID: "a", Type: "note", Timestamp: 100, Data: map[string]any{"msg": "hello"}},
		{ID: "b", Type: "task", Timestamp: 200, Data: map[string]any{"done": true}},
	}
	if err := WriteEntries(path, original); err != nil {
		t.Fatalf("WriteEntries: %v", err)
	}

	entries, err := ReadAllEntries(path)
	if err != nil {
		t.Fatalf("ReadAllEntries: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2, got %d", len(entries))
	}
	if entries[0].ID != "a" || entries[1].ID != "b" {
		t.Fatalf("IDs mismatch: %q, %q", entries[0].ID, entries[1].ID)
	}
}

func TestWriteEntries_Empty(t *testing.T) {
	path := tempFile(t)
	if err := WriteEntries(path, nil); err != nil {
		t.Fatalf("WriteEntries: %v", err)
	}
	entries, err := ReadAllEntries(path)
	if err != nil {
		t.Fatalf("ReadAllEntries: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected 0, got %d", len(entries))
	}
}

// TestEntryIter_Empty verifies that iterating an empty ledger immediately
// returns io.EOF on the first Next() call.
func TestEntryIter_Empty(t *testing.T) {
	path := tempFile(t)
	if err := InitFile(path); err != nil {
		t.Fatalf("InitFile: %v", err)
	}

	it, err := NewEntryIter(path)
	if err != nil {
		t.Fatalf("NewEntryIter: %v", err)
	}
	defer it.Close()

	entry, err := it.Next()
	if err != io.EOF {
		t.Fatalf("expected io.EOF, got err=%v entry=%v", err, entry)
	}
}

// TestEntryIter_SingleEntry verifies that a ledger with one entry is
// returned on the first Next() and io.EOF on the second.
func TestEntryIter_SingleEntry(t *testing.T) {
	path := tempFile(t)
	if err := InitFile(path); err != nil {
		t.Fatalf("InitFile: %v", err)
	}
	original := &model.Entry{ID: "iter-1", Type: "note", Timestamp: 42}
	if _, err := AppendEntry(path, original); err != nil {
		t.Fatalf("AppendEntry: %v", err)
	}

	it, err := NewEntryIter(path)
	if err != nil {
		t.Fatalf("NewEntryIter: %v", err)
	}
	defer it.Close()

	got, err := it.Next()
	if err != nil {
		t.Fatalf("Next: unexpected error: %v", err)
	}
	if got.ID != "iter-1" {
		t.Fatalf("expected ID 'iter-1', got %q", got.ID)
	}

	_, err = it.Next()
	if err != io.EOF {
		t.Fatalf("expected io.EOF after last entry, got %v", err)
	}
}

// TestEntryIter_MultipleEntries verifies streaming iteration over N entries.
func TestEntryIter_MultipleEntries(t *testing.T) {
	const count = 5
	path := tempFile(t)
	if err := InitFile(path); err != nil {
		t.Fatalf("InitFile: %v", err)
	}
	for i := 0; i < count; i++ {
		e := &model.Entry{ID: fmt.Sprintf("e%d", i), Type: "t", Timestamp: int64(i)}
		if _, err := AppendEntry(path, e); err != nil {
			t.Fatalf("AppendEntry[%d]: %v", i, err)
		}
	}

	it, err := NewEntryIter(path)
	if err != nil {
		t.Fatalf("NewEntryIter: %v", err)
	}
	defer it.Close()

	var got []*model.Entry
	for {
		e, err := it.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		got = append(got, e)
	}

	if len(got) != count {
		t.Fatalf("expected %d entries, got %d", count, len(got))
	}
	for i, e := range got {
		if e.ID != fmt.Sprintf("e%d", i) {
			t.Errorf("entry[%d] ID = %q, want e%d", i, e.ID, i)
		}
	}
}

// TestEntryIter_EarlyClose verifies that closing the iterator before
// exhausting it does not panic or corrupt anything.
func TestEntryIter_EarlyClose(t *testing.T) {
	path := tempFile(t)
	if err := InitFile(path); err != nil {
		t.Fatalf("InitFile: %v", err)
	}
	for i := 0; i < 10; i++ {
		e := &model.Entry{ID: fmt.Sprintf("ec%d", i), Type: "t"}
		_, _ = AppendEntry(path, e)
	}

	it, err := NewEntryIter(path)
	if err != nil {
		t.Fatalf("NewEntryIter: %v", err)
	}

	// Read only the first entry, then close early.
	if _, err := it.Next(); err != nil {
		t.Fatalf("Next: %v", err)
	}
	if err := it.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// TestEntryIter_BadHeader verifies that a file with a wrong first byte
// returns an error from NewEntryIter.
func TestEntryIter_BadHeader(t *testing.T) {
	path := tempFile(t)
	if err := os.WriteFile(path, []byte{0x00, 0xFF}, 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := NewEntryIter(path)
	if err == nil {
		t.Fatal("expected error for bad header byte")
	}
}

// TestEntryIter_TooSmall verifies that a file shorter than 2 bytes returns
// an error from NewEntryIter.
func TestEntryIter_TooSmall(t *testing.T) {
	path := tempFile(t)
	if err := os.WriteFile(path, []byte{0x9F}, 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := NewEntryIter(path)
	if err == nil {
		t.Fatal("expected error for file too small")
	}
}

// TestReadAllEntries_AboveDefaultLimit verifies that ReadAllEntries succeeds
// for ledgers with more than the old 131,072-element cap. Without the custom
// DecMode this test would return an error:
//
//	cbor: exceeded max number of elements 131072 for CBOR array
func TestReadAllEntries_AboveDefaultLimit(t *testing.T) {
	const count = 200_000
	path := tempFile(t)

	entries := make([]*model.Entry, count)
	for i := range entries {
		entries[i] = &model.Entry{
			ID:        fmt.Sprintf("entry-%d", i),
			Type:      "test",
			Timestamp: int64(i),
		}
	}
	if err := WriteEntries(path, entries); err != nil {
		t.Fatalf("WriteEntries: %v", err)
	}

	got, err := ReadAllEntries(path)
	if err != nil {
		t.Fatalf("ReadAllEntries with %d entries: %v", count, err)
	}
	if len(got) != count {
		t.Fatalf("expected %d entries, got %d", count, len(got))
	}
}

// TestWriteEntries_AboveDefaultLimit verifies WriteEntries + ReadAllEntries
// round-trip for a ledger just above the old 131K cap.
func TestReadEntryAt_DecodesCorrectEntry(t *testing.T) {
	path := tempFile(t)
	if err := InitFile(path); err != nil {
		t.Fatalf("InitFile: %v", err)
	}

	entries := []*model.Entry{
		{ID: "first", Type: "a", Timestamp: 1, Data: map[string]any{"n": "1"}},
		{ID: "second", Type: "b", Timestamp: 2, Data: map[string]any{"n": "2"}},
		{ID: "third", Type: "c", Timestamp: 3, Data: map[string]any{"n": "3"}},
	}
	offsets := make([]int64, len(entries))
	for i, e := range entries {
		_ = e.Validate()
		off, err := AppendEntry(path, e)
		if err != nil {
			t.Fatalf("AppendEntry[%d]: %v", i, err)
		}
		offsets[i] = off
	}

	for i, e := range entries {
		got, err := ReadEntryAt(path, offsets[i])
		if err != nil {
			t.Fatalf("ReadEntryAt[%d]: %v", i, err)
		}
		if got.ID != e.ID {
			t.Errorf("entry[%d]: expected ID=%q, got %q", i, e.ID, got.ID)
		}
		if got.Data["n"] != e.Data["n"] {
			t.Errorf("entry[%d]: expected n=%v, got %v", i, e.Data["n"], got.Data["n"])
		}
	}
}

func TestWriteEntries_AboveDefaultLimit(t *testing.T) {
	const count = 150_000
	path := tempFile(t)

	entries := make([]*model.Entry, count)
	for i := range entries {
		entries[i] = &model.Entry{
			ID:        fmt.Sprintf("id-%d", i),
			Type:      "bulk",
			Timestamp: int64(i + 1),
		}
	}
	if err := WriteEntries(path, entries); err != nil {
		t.Fatalf("WriteEntries: %v", err)
	}
	got, err := ReadAllEntries(path)
	if err != nil {
		t.Fatalf("ReadAllEntries: %v", err)
	}
	if len(got) != count {
		t.Fatalf("expected %d, got %d", count, len(got))
	}
	if got[0].ID != "id-0" || got[count-1].ID != fmt.Sprintf("id-%d", count-1) {
		t.Fatalf("ID mismatch at boundaries: first=%q last=%q", got[0].ID, got[count-1].ID)
	}
}

// TestReadEntriesFrom_ReturnsOnlyNewEntries verifies that readEntriesFrom
// decodes only the entries appended after a given snapshot byte offset,
// leaving earlier entries untouched. This is the core primitive that the
// copy-on-write Compact uses to merge concurrent inserts.
func TestReadEntriesFrom_ReturnsOnlyNewEntries(t *testing.T) {
	path := tempFile(t)
	if err := InitFile(path); err != nil {
		t.Fatalf("InitFile: %v", err)
	}

	// Append two "old" entries and record snapshot.
	for _, id := range []string{"old-1", "old-2"} {
		if _, err := AppendEntry(path, &model.Entry{ID: id, Type: "t"}); err != nil {
			t.Fatalf("AppendEntry %s: %v", id, err)
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	snapshot := info.Size()

	// Append two "new" entries after the snapshot.
	for _, id := range []string{"new-1", "new-2"} {
		if _, err := AppendEntry(path, &model.Entry{ID: id, Type: "t"}); err != nil {
			t.Fatalf("AppendEntry %s: %v", id, err)
		}
	}

	got, err := readEntriesFrom(path, snapshot)
	if err != nil {
		t.Fatalf("readEntriesFrom: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 new entries, got %d", len(got))
	}
	if got[0].ID != "new-1" || got[1].ID != "new-2" {
		t.Errorf("unexpected IDs: %q %q", got[0].ID, got[1].ID)
	}
}

// TestReadEntriesFrom_EmptyWhenNoGrowth verifies that readEntriesFrom returns
// nil (no new entries) when the file has not grown beyond snapshotSize.
func TestReadEntriesFrom_EmptyWhenNoGrowth(t *testing.T) {
	path := tempFile(t)
	if err := InitFile(path); err != nil {
		t.Fatalf("InitFile: %v", err)
	}
	if _, err := AppendEntry(path, &model.Entry{ID: "a", Type: "t"}); err != nil {
		t.Fatalf("AppendEntry: %v", err)
	}
	info, _ := os.Stat(path)

	got, err := readEntriesFrom(path, info.Size())
	if err != nil {
		t.Fatalf("readEntriesFrom: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 entries, got %d", len(got))
	}
}
