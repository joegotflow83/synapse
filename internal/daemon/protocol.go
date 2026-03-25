// Package daemon provides the Unix-socket server and client for synapse daemon
// mode. Running a persistent daemon eliminates the ~5.3ms Go-runtime startup
// cost on every CLI invocation and keeps ledger file descriptors warm between
// requests.
package daemon

import "encoding/json"

// Command names for the JSON-over-Unix-socket protocol.
const (
	CmdHealth      = "health"
	CmdInsert      = "insert"
	CmdInsertBatch = "insert-batch"
	CmdQuery       = "query"
	CmdQueryBatch  = "query-batch"
	CmdGet         = "get"
	CmdCompact     = "compact"
	CmdListTypes   = "list-types"
	CmdCreateType  = "create-type"
	CmdReindex     = "reindex"
	CmdShutdown    = "shutdown"
)

// Request is sent by the client over the Unix socket.
// Each request is a single JSON object terminated by a newline.
type Request struct {
	Command string          `json:"command"`
	Dir     string          `json:"dir,omitempty"`
	Args    json.RawMessage `json:"args,omitempty"`
}

// ResponseStats contains operation metrics included in daemon responses.
// Fields mirror the protocol spec: scanned (entries examined), matched
// (entries returned), and duration_ms (server-side wall time).
type ResponseStats struct {
	Scanned    int     `json:"scanned"`
	Matched    int     `json:"matched"`
	DurationMs float64 `json:"duration_ms"`
}

// Response is sent by the server.
// Status is "ok" on success, "not_found" when a Get misses, "error" on failure.
type Response struct {
	Status string          `json:"status"`
	Data   json.RawMessage `json:"data,omitempty"`
	Error  string          `json:"error,omitempty"`
	Stats  *ResponseStats  `json:"stats,omitempty"`
}

// OKResponse builds a successful response with data marshaled as JSON.
func OKResponse(data any) (*Response, error) {
	b, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	return &Response{Status: "ok", Data: b}, nil
}

// OKResponseWithStats builds a successful response with stats included.
func OKResponseWithStats(data any, stats *ResponseStats) (*Response, error) {
	b, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	return &Response{Status: "ok", Data: b, Stats: stats}, nil
}

// ErrResponse builds an error response.
func ErrResponse(msg string) *Response {
	return &Response{Status: "error", Error: msg}
}

// InsertArgs is the args payload for CmdInsert.
type InsertArgs struct {
	Type     string         `json:"type"`
	Data     map[string]any `json:"data"`
	Metadata map[string]any `json:"metadata,omitempty"`
	ID       string         `json:"id,omitempty"`
	AgentID  string         `json:"agent_id,omitempty"`
}

// InsertBatchArgs is the args payload for CmdInsertBatch.
type InsertBatchArgs struct {
	Entries []InsertArgs `json:"entries"`
}

// QueryArgs is the args payload for CmdQuery.
type QueryArgs struct {
	Type   string `json:"type,omitempty"`
	Filter string `json:"filter,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

// GetArgs is the args payload for CmdGet.
type GetArgs struct {
	ID      string `json:"id"`
	History bool   `json:"history,omitempty"`
}

// CreateTypeArgs is the args payload for CmdCreateType.
type CreateTypeArgs struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Example     string `json:"example,omitempty"`
}
