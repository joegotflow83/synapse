package query

import (
	"fmt"
	"strings"
	"time"

	"github.com/synapse-tool/synapse/internal/model"
)

// ClauseType represents the kind of filter clause.
type ClauseType int

const (
	ClauseKeyValue ClauseType = iota
	ClauseSince
	ClauseUntil
)

// Clause represents a single filter condition.
type Clause struct {
	Type  ClauseType
	Key   string // for key=value filters
	Value string // for key=value filters
	Time  int64  // Unix seconds for since/until
}

// Parse parses a filter string into a slice of Clauses.
// Tokens are space-separated and AND-ed together.
// Supported formats:
//   - key=value  — exact equality on entry.Data[key]
//   - since:DATE — timestamp >= parsed date (YYYY-MM-DD or RFC3339)
//   - until:DATE — timestamp <= parsed date (YYYY-MM-DD or RFC3339)
func Parse(filter string) ([]Clause, error) {
	filter = strings.TrimSpace(filter)
	if filter == "" {
		return nil, nil
	}

	tokens := strings.Fields(filter)
	clauses := make([]Clause, 0, len(tokens))

	for _, tok := range tokens {
		c, err := parseToken(tok)
		if err != nil {
			return nil, err
		}
		clauses = append(clauses, c)
	}

	return clauses, nil
}

func parseToken(tok string) (Clause, error) {
	if strings.HasPrefix(tok, "since:") {
		dateStr := tok[6:]
		t, err := parseDate(dateStr)
		if err != nil {
			return Clause{}, fmt.Errorf("invalid since date %q: %w", dateStr, err)
		}
		return Clause{Type: ClauseSince, Time: t.Unix()}, nil
	}

	if strings.HasPrefix(tok, "until:") {
		dateStr := tok[6:]
		t, err := parseDate(dateStr)
		if err != nil {
			return Clause{}, fmt.Errorf("invalid until date %q: %w", dateStr, err)
		}
		// For date-only (YYYY-MM-DD), set to end of day
		if len(dateStr) == 10 {
			t = t.Add(24*time.Hour - time.Second)
		}
		return Clause{Type: ClauseUntil, Time: t.Unix()}, nil
	}

	// key=value
	eqIdx := strings.Index(tok, "=")
	if eqIdx <= 0 {
		return Clause{}, fmt.Errorf("invalid filter token %q: expected key=value, since:DATE, or until:DATE", tok)
	}

	key := tok[:eqIdx]
	value := tok[eqIdx+1:]
	return Clause{Type: ClauseKeyValue, Key: key, Value: value}, nil
}

func parseDate(s string) (time.Time, error) {
	// Try RFC3339 first (ISO8601 with timezone)
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}

	// Try date-only YYYY-MM-DD (interpret as start of day UTC)
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t, nil
	}

	return time.Time{}, fmt.Errorf("cannot parse %q as RFC3339 or YYYY-MM-DD", s)
}

// Evaluate returns true if the entry matches all the given clauses (AND logic).
func Evaluate(entry *model.Entry, clauses []Clause) bool {
	for _, c := range clauses {
		switch c.Type {
		case ClauseKeyValue:
			val, ok := entry.Data[c.Key]
			if !ok {
				return false
			}
			if fmt.Sprintf("%v", val) != c.Value {
				return false
			}
		case ClauseSince:
			if entry.Timestamp < c.Time {
				return false
			}
		case ClauseUntil:
			if entry.Timestamp > c.Time {
				return false
			}
		}
	}
	return true
}
