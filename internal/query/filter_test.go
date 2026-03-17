package query

import (
	"testing"
	"time"

	"github.com/synapse-tool/synapse/internal/model"
)

func TestParseEmpty(t *testing.T) {
	clauses, err := Parse("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(clauses) != 0 {
		t.Fatalf("expected 0 clauses, got %d", len(clauses))
	}
}

func TestParseKeyValue(t *testing.T) {
	clauses, err := Parse("status=open priority=high")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(clauses) != 2 {
		t.Fatalf("expected 2 clauses, got %d", len(clauses))
	}
	if clauses[0].Type != ClauseKeyValue || clauses[0].Key != "status" || clauses[0].Value != "open" {
		t.Errorf("clause 0: got %+v", clauses[0])
	}
	if clauses[1].Type != ClauseKeyValue || clauses[1].Key != "priority" || clauses[1].Value != "high" {
		t.Errorf("clause 1: got %+v", clauses[1])
	}
}

func TestParseSinceUntil(t *testing.T) {
	clauses, err := Parse("since:2024-01-15 until:2024-06-30")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(clauses) != 2 {
		t.Fatalf("expected 2 clauses, got %d", len(clauses))
	}
	if clauses[0].Type != ClauseSince {
		t.Errorf("expected ClauseSince, got %v", clauses[0].Type)
	}
	expectedSince := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC).Unix()
	if clauses[0].Time != expectedSince {
		t.Errorf("since time: expected %d, got %d", expectedSince, clauses[0].Time)
	}
	if clauses[1].Type != ClauseUntil {
		t.Errorf("expected ClauseUntil, got %v", clauses[1].Type)
	}
	// until:2024-06-30 should be end of day (23:59:59)
	expectedUntil := time.Date(2024, 6, 30, 23, 59, 59, 0, time.UTC).Unix()
	if clauses[1].Time != expectedUntil {
		t.Errorf("until time: expected %d, got %d", expectedUntil, clauses[1].Time)
	}
}

func TestParseRFC3339(t *testing.T) {
	clauses, err := Parse("since:2024-03-01T10:30:00Z")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(clauses) != 1 {
		t.Fatalf("expected 1 clause, got %d", len(clauses))
	}
	expected := time.Date(2024, 3, 1, 10, 30, 0, 0, time.UTC).Unix()
	if clauses[0].Time != expected {
		t.Errorf("expected %d, got %d", expected, clauses[0].Time)
	}
}

func TestParseMalformed(t *testing.T) {
	tests := []string{
		"badtoken",
		"since:notadate",
		"until:2024-13-99",
		"=nokey",
	}
	for _, input := range tests {
		_, err := Parse(input)
		if err == nil {
			t.Errorf("expected error for input %q", input)
		}
	}
}

func TestEvaluateEmptyFilter(t *testing.T) {
	entry := &model.Entry{
		Type:      "task",
		Timestamp: time.Now().Unix(),
		Data:      map[string]any{"status": "open"},
	}
	if !Evaluate(entry, nil) {
		t.Error("empty filter should match everything")
	}
}

func TestEvaluateKeyValue(t *testing.T) {
	entry := &model.Entry{
		Type:      "task",
		Timestamp: time.Now().Unix(),
		Data:      map[string]any{"status": "open", "priority": "high"},
	}

	// Match
	clauses, _ := Parse("status=open")
	if !Evaluate(entry, clauses) {
		t.Error("should match status=open")
	}

	// No match
	clauses, _ = Parse("status=closed")
	if Evaluate(entry, clauses) {
		t.Error("should not match status=closed")
	}

	// Missing key
	clauses, _ = Parse("assignee=alice")
	if Evaluate(entry, clauses) {
		t.Error("should not match missing key")
	}

	// AND: both must match
	clauses, _ = Parse("status=open priority=high")
	if !Evaluate(entry, clauses) {
		t.Error("should match both clauses")
	}

	// AND: one fails
	clauses, _ = Parse("status=open priority=low")
	if Evaluate(entry, clauses) {
		t.Error("should not match when one clause fails")
	}
}

func TestEvaluateTimestamp(t *testing.T) {
	ts := time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC).Unix()
	entry := &model.Entry{
		Type:      "event",
		Timestamp: ts,
		Data:      map[string]any{},
	}

	// since: before entry
	clauses, _ := Parse("since:2024-01-01")
	if !Evaluate(entry, clauses) {
		t.Error("entry should be after since date")
	}

	// since: after entry
	clauses, _ = Parse("since:2024-12-01")
	if Evaluate(entry, clauses) {
		t.Error("entry should be before since date")
	}

	// until: after entry
	clauses, _ = Parse("until:2024-12-31")
	if !Evaluate(entry, clauses) {
		t.Error("entry should be before until date")
	}

	// until: before entry
	clauses, _ = Parse("until:2024-01-01")
	if Evaluate(entry, clauses) {
		t.Error("entry should be after until date")
	}

	// Combined range
	clauses, _ = Parse("since:2024-06-01 until:2024-06-30")
	if !Evaluate(entry, clauses) {
		t.Error("entry should be within range")
	}
}

func TestEvaluateNumericValue(t *testing.T) {
	// Data values may be non-string types from CBOR decoding
	entry := &model.Entry{
		Type:      "metric",
		Timestamp: time.Now().Unix(),
		Data:      map[string]any{"count": 42},
	}

	clauses, _ := Parse("count=42")
	if !Evaluate(entry, clauses) {
		t.Error("should match numeric value via fmt.Sprintf")
	}
}
