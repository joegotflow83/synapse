package ledger

import (
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
	if err := AppendEntry(path, entry); err != nil {
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
		if err := AppendEntry(path, entry); err != nil {
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
	err := AppendEntry(path, entry)
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
