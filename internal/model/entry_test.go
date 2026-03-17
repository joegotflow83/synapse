package model

import (
	"testing"
	"time"

	"github.com/fxamacker/cbor/v2"
)

func TestValidate_RequiresType(t *testing.T) {
	e := &Entry{}
	if err := e.Validate(); err == nil {
		t.Fatal("expected error for empty type")
	}
}

func TestValidate_AutoGeneratesID(t *testing.T) {
	e := &Entry{Type: "task"}
	if err := e.Validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if e.ID == "" {
		t.Fatal("expected auto-generated ID")
	}
	// UUID v4 format: 8-4-4-4-12
	if len(e.ID) != 36 {
		t.Fatalf("expected UUID v4 format, got %q", e.ID)
	}
}

func TestValidate_PreservesExplicitID(t *testing.T) {
	e := &Entry{Type: "task", ID: "my-custom-id"}
	if err := e.Validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if e.ID != "my-custom-id" {
		t.Fatalf("expected ID to be preserved, got %q", e.ID)
	}
}

func TestValidate_AutoSetsTimestamp(t *testing.T) {
	before := time.Now().Unix()
	e := &Entry{Type: "task"}
	if err := e.Validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	after := time.Now().Unix()
	if e.Timestamp < before || e.Timestamp > after {
		t.Fatalf("timestamp %d not in range [%d, %d]", e.Timestamp, before, after)
	}
}

func TestValidate_PreservesExplicitTimestamp(t *testing.T) {
	e := &Entry{Type: "task", Timestamp: 1700000000}
	if err := e.Validate(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if e.Timestamp != 1700000000 {
		t.Fatalf("expected timestamp to be preserved, got %d", e.Timestamp)
	}
}

func TestCBOR_RoundTrip(t *testing.T) {
	original := &Entry{
		ID:        "test-id",
		Type:      "task",
		Timestamp: 1700000000,
		AgentID:   "agent-1",
		Data: map[string]any{
			"title":  "Do something",
			"status": "open",
		},
		AgentMetadata: map[string]any{
			"tags": []any{"important"},
		},
	}

	data, err := cbor.Marshal(original)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var decoded Entry
	if err := cbor.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if decoded.ID != original.ID {
		t.Errorf("ID: got %q, want %q", decoded.ID, original.ID)
	}
	if decoded.Type != original.Type {
		t.Errorf("Type: got %q, want %q", decoded.Type, original.Type)
	}
	if decoded.Timestamp != original.Timestamp {
		t.Errorf("Timestamp: got %d, want %d", decoded.Timestamp, original.Timestamp)
	}
	if decoded.AgentID != original.AgentID {
		t.Errorf("AgentID: got %q, want %q", decoded.AgentID, original.AgentID)
	}
	if decoded.Data["title"] != original.Data["title"] {
		t.Errorf("Data[title]: got %v, want %v", decoded.Data["title"], original.Data["title"])
	}
}

func TestCBOR_ISOTimestampRoundTrip(t *testing.T) {
	// Simulate a CBOR entry where timestamp is an ISO 8601 string
	// (as might be written by an external agent directly to the CBOR file).
	raw := map[string]any{
		"id":        "iso-test",
		"type":      "note",
		"timestamp": "2024-06-15T12:30:00Z",
		"data":      map[string]any{"msg": "hello"},
	}
	data, err := cbor.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal raw: %v", err)
	}

	var decoded Entry
	if err := cbor.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal with ISO timestamp: %v", err)
	}

	if decoded.ID != "iso-test" {
		t.Errorf("ID: got %q, want %q", decoded.ID, "iso-test")
	}
	// 2024-06-15T12:30:00Z in Unix seconds
	expected := int64(1718454600)
	if decoded.Timestamp != expected {
		t.Errorf("Timestamp: got %d, want %d", decoded.Timestamp, expected)
	}
}

func TestCBOR_DateOnlyTimestamp(t *testing.T) {
	raw := map[string]any{
		"id":        "date-only-test",
		"type":      "note",
		"timestamp": "2024-01-01",
	}
	data, err := cbor.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal raw: %v", err)
	}

	var decoded Entry
	if err := cbor.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal with date-only timestamp: %v", err)
	}

	// 2024-01-01T00:00:00Z in Unix seconds
	expected := int64(1704067200)
	if decoded.Timestamp != expected {
		t.Errorf("Timestamp: got %d, want %d", decoded.Timestamp, expected)
	}
}

func TestCBOR_InvalidISOTimestamp(t *testing.T) {
	raw := map[string]any{
		"id":        "bad-ts",
		"type":      "note",
		"timestamp": "not-a-date",
	}
	data, err := cbor.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal raw: %v", err)
	}

	var decoded Entry
	err = cbor.Unmarshal(data, &decoded)
	if err == nil {
		t.Fatal("expected error for invalid ISO timestamp string")
	}
}

func TestCBOR_OmitEmpty(t *testing.T) {
	e := &Entry{
		ID:        "test-id",
		Type:      "task",
		Timestamp: 1700000000,
	}

	data, err := cbor.Marshal(e)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	// Decode into a raw map to verify omitempty fields are absent
	var raw map[string]any
	if err := cbor.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if _, ok := raw["agent_id"]; ok {
		t.Error("expected agent_id to be omitted when empty")
	}
	if _, ok := raw["data"]; ok {
		t.Error("expected data to be omitted when nil")
	}
	if _, ok := raw["agent_metadata"]; ok {
		t.Error("expected agent_metadata to be omitted when nil")
	}
}
