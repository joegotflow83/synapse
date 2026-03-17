package types

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInitFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "types.cbor")

	if err := InitFile(path); err != nil {
		t.Fatalf("InitFile: %v", err)
	}

	// File should exist and be readable as an empty map.
	types, err := ReadTypes(path)
	if err != nil {
		t.Fatalf("ReadTypes after init: %v", err)
	}
	if len(types) != 0 {
		t.Errorf("expected empty map, got %d entries", len(types))
	}
}

func TestCreateAndListTypes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "types.cbor")

	if err := InitFile(path); err != nil {
		t.Fatalf("InitFile: %v", err)
	}

	// Create a type.
	if err := CreateType(path, "task", "A task entry", `{"title":"example"}`); err != nil {
		t.Fatalf("CreateType: %v", err)
	}

	// List and verify.
	types, err := ListTypes(path)
	if err != nil {
		t.Fatalf("ListTypes: %v", err)
	}
	if len(types) != 1 {
		t.Fatalf("expected 1 type, got %d", len(types))
	}

	meta, ok := types["task"]
	if !ok {
		t.Fatal("type 'task' not found")
	}
	if meta.Description != "A task entry" {
		t.Errorf("description = %q, want %q", meta.Description, "A task entry")
	}
	if meta.Example != `{"title":"example"}` {
		t.Errorf("example = %q, want %q", meta.Example, `{"title":"example"}`)
	}
	if meta.CreatedAt == 0 {
		t.Error("created_at should be non-zero")
	}
}

func TestCreateTypeMultiple(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "types.cbor")

	if err := InitFile(path); err != nil {
		t.Fatalf("InitFile: %v", err)
	}

	if err := CreateType(path, "task", "Tasks", ""); err != nil {
		t.Fatalf("CreateType task: %v", err)
	}
	if err := CreateType(path, "note", "Notes", ""); err != nil {
		t.Fatalf("CreateType note: %v", err)
	}

	types, err := ListTypes(path)
	if err != nil {
		t.Fatalf("ListTypes: %v", err)
	}
	if len(types) != 2 {
		t.Fatalf("expected 2 types, got %d", len(types))
	}
	if _, ok := types["task"]; !ok {
		t.Error("type 'task' not found")
	}
	if _, ok := types["note"]; !ok {
		t.Error("type 'note' not found")
	}
}

func TestCreateTypeOverwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "types.cbor")

	if err := InitFile(path); err != nil {
		t.Fatalf("InitFile: %v", err)
	}

	if err := CreateType(path, "task", "Old description", ""); err != nil {
		t.Fatalf("CreateType: %v", err)
	}
	if err := CreateType(path, "task", "New description", ""); err != nil {
		t.Fatalf("CreateType overwrite: %v", err)
	}

	types, err := ListTypes(path)
	if err != nil {
		t.Fatalf("ListTypes: %v", err)
	}
	if len(types) != 1 {
		t.Fatalf("expected 1 type, got %d", len(types))
	}
	if types["task"].Description != "New description" {
		t.Errorf("description = %q, want %q", types["task"].Description, "New description")
	}
}

func TestReadTypesFileNotFound(t *testing.T) {
	_, err := ReadTypes("/nonexistent/types.cbor")
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

func TestReadTypesCorruptedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "types.cbor")

	if err := os.WriteFile(path, []byte("not valid cbor"), 0644); err != nil {
		t.Fatalf("write corrupt file: %v", err)
	}

	_, err := ReadTypes(path)
	if err == nil {
		t.Error("expected error for corrupted file")
	}
}
