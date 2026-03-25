package model

import (
	"fmt"
	"time"

	"github.com/fxamacker/cbor/v2"
	"github.com/google/uuid"
)

// Entry represents a single ledger entry in the Synapse event log.
type Entry struct {
	ID            string         `cbor:"id" json:"id"`
	Type          string         `cbor:"type" json:"type"`
	Timestamp     int64          `cbor:"timestamp" json:"timestamp"`
	AgentID       string         `cbor:"agent_id,omitempty" json:"agent_id,omitempty"`
	Data          map[string]any `cbor:"data,omitempty" json:"data,omitempty"`
	AgentMetadata map[string]any `cbor:"agent_metadata,omitempty" json:"agent_metadata,omitempty"`
}

// rawEntry is used for custom CBOR unmarshaling to handle flexible timestamp
// formats (uint64 Unix seconds or ISO 8601 string) per the storage spec.
type rawEntry struct {
	ID            string         `cbor:"id"`
	Type          string         `cbor:"type"`
	Timestamp     any            `cbor:"timestamp"`
	AgentID       string         `cbor:"agent_id,omitempty"`
	Data          map[string]any `cbor:"data,omitempty"`
	AgentMetadata map[string]any `cbor:"agent_metadata,omitempty"`
}

// UnmarshalCBOR implements cbor.Unmarshaler to support both uint64 (Unix
// seconds) and ISO 8601 string timestamps in the CBOR storage format.
func (e *Entry) UnmarshalCBOR(data []byte) error {
	var raw rawEntry
	if err := cbor.Unmarshal(data, &raw); err != nil {
		return err
	}

	e.ID = raw.ID
	e.Type = raw.Type
	e.AgentID = raw.AgentID
	e.Data = raw.Data
	e.AgentMetadata = raw.AgentMetadata

	switch v := raw.Timestamp.(type) {
	case uint64:
		e.Timestamp = int64(v)
	case int64:
		e.Timestamp = v
	case float64:
		e.Timestamp = int64(v)
	case string:
		ts, err := parseISOTimestamp(v)
		if err != nil {
			return fmt.Errorf("invalid ISO timestamp %q: %w", v, err)
		}
		e.Timestamp = ts
	case nil:
		e.Timestamp = 0
	default:
		return fmt.Errorf("unsupported timestamp type %T", raw.Timestamp)
	}

	return nil
}

// parseISOTimestamp parses an ISO 8601 timestamp string and returns Unix seconds.
func parseISOTimestamp(s string) (int64, error) {
	// Try RFC3339 first (e.g. "2024-01-15T10:30:00Z")
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.Unix(), nil
	}
	// Try date-only YYYY-MM-DD (start of day UTC)
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t.Unix(), nil
	}
	return 0, fmt.Errorf("cannot parse %q as RFC3339 or YYYY-MM-DD", s)
}

// Validate checks required fields, auto-generates ID if empty, and sets
// Timestamp to now (Unix seconds) if zero. Returns an error if Type is empty.
// MaxIDLength is the maximum allowed length for entry IDs, constrained by
// the binary id_index.bin format (64-byte field, zero-padded).
const MaxIDLength = 63

func (e *Entry) Validate() error {
	if e.Type == "" {
		return fmt.Errorf("entry type is required")
	}
	if e.ID == "" {
		e.ID = uuid.New().String()
	}
	if len(e.ID) > MaxIDLength {
		return fmt.Errorf("id too long: %d bytes (max %d)", len(e.ID), MaxIDLength)
	}
	if e.Timestamp == 0 {
		e.Timestamp = time.Now().Unix()
	}
	return nil
}
