package daemon

import (
	"encoding/json"
	"fmt"
	"net"
	"time"
)

// Client connects to a running daemon over a Unix socket.
// Each method opens a fresh connection, sends one request, reads one response,
// and closes the connection. This keeps the client stateless and safe for
// concurrent use from multiple goroutines.
type Client struct {
	socketPath string
}

// NewClient creates a Client for the given socket path.
// It does NOT verify that the daemon is running — call Ping for that.
func NewClient(socketPath string) *Client {
	return &Client{socketPath: socketPath}
}

func (c *Client) send(req *Request) (*Response, error) {
	conn, err := net.DialTimeout("unix", c.socketPath, 2*time.Second)
	if err != nil {
		return nil, fmt.Errorf("connect to daemon: %w", err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(30 * time.Second)) //nolint:errcheck

	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	var resp Response
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	return &resp, nil
}

// Ping checks whether the daemon is reachable and healthy.
func (c *Client) Ping() error {
	resp, err := c.send(&Request{Command: CmdHealth})
	if err != nil {
		return err
	}
	if resp.Status != "ok" {
		return fmt.Errorf("daemon unhealthy: %s", resp.Error)
	}
	return nil
}

// Shutdown sends a graceful-shutdown command to the daemon.
func (c *Client) Shutdown() error {
	_, err := c.send(&Request{Command: CmdShutdown})
	return err
}

// Insert sends an insert request to the daemon and returns the assigned ID.
func (c *Client) Insert(dir string, args InsertArgs) (string, error) {
	argBytes, _ := json.Marshal(args)
	resp, err := c.send(&Request{Command: CmdInsert, Dir: dir, Args: argBytes})
	if err != nil {
		return "", err
	}
	if resp.Status != "ok" {
		return "", fmt.Errorf("%s", resp.Error)
	}
	var result map[string]string
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}
	return result["id"], nil
}

// InsertBatch sends a batch insert request and returns the assigned IDs.
func (c *Client) InsertBatch(dir string, args InsertBatchArgs) ([]string, error) {
	argBytes, _ := json.Marshal(args)
	resp, err := c.send(&Request{Command: CmdInsertBatch, Dir: dir, Args: argBytes})
	if err != nil {
		return nil, err
	}
	if resp.Status != "ok" {
		return nil, fmt.Errorf("%s", resp.Error)
	}
	var result map[string][]string
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return result["ids"], nil
}

// Query sends a query request and returns the raw JSON response data.
func (c *Client) Query(dir string, args QueryArgs) (json.RawMessage, error) {
	argBytes, _ := json.Marshal(args)
	resp, err := c.send(&Request{Command: CmdQuery, Dir: dir, Args: argBytes})
	if err != nil {
		return nil, err
	}
	if resp.Status != "ok" {
		return nil, fmt.Errorf("%s", resp.Error)
	}
	return resp.Data, nil
}

// QueryBatch sends a batch query and returns the raw JSON response data.
func (c *Client) QueryBatch(dir string, specs json.RawMessage) (json.RawMessage, error) {
	resp, err := c.send(&Request{Command: CmdQueryBatch, Dir: dir, Args: specs})
	if err != nil {
		return nil, err
	}
	if resp.Status != "ok" {
		return nil, fmt.Errorf("%s", resp.Error)
	}
	return resp.Data, nil
}

// Get sends a get-by-ID request. Returns (data, found, error).
// found is false when the daemon returns "not_found" status.
func (c *Client) Get(dir, id string, history bool) (json.RawMessage, bool, error) {
	argBytes, _ := json.Marshal(GetArgs{ID: id, History: history})
	resp, err := c.send(&Request{Command: CmdGet, Dir: dir, Args: argBytes})
	if err != nil {
		return nil, false, err
	}
	if resp.Status == "not_found" {
		return nil, false, nil
	}
	if resp.Status != "ok" {
		return nil, false, fmt.Errorf("%s", resp.Error)
	}
	return resp.Data, true, nil
}

// Compact sends a compact request and returns the raw JSON stats.
func (c *Client) Compact(dir string) (json.RawMessage, error) {
	resp, err := c.send(&Request{Command: CmdCompact, Dir: dir})
	if err != nil {
		return nil, err
	}
	if resp.Status != "ok" {
		return nil, fmt.Errorf("%s", resp.Error)
	}
	return resp.Data, nil
}

// ListTypes sends a list-types request and returns the raw JSON data.
func (c *Client) ListTypes(dir string) (json.RawMessage, error) {
	resp, err := c.send(&Request{Command: CmdListTypes, Dir: dir})
	if err != nil {
		return nil, err
	}
	if resp.Status != "ok" {
		return nil, fmt.Errorf("%s", resp.Error)
	}
	return resp.Data, nil
}

// Reindex sends a reindex request and returns the raw JSON result.
func (c *Client) Reindex(dir string) (json.RawMessage, error) {
	resp, err := c.send(&Request{Command: CmdReindex, Dir: dir})
	if err != nil {
		return nil, err
	}
	if resp.Status != "ok" {
		return nil, fmt.Errorf("%s", resp.Error)
	}
	return resp.Data, nil
}

// CreateType sends a create-type request.
func (c *Client) CreateType(dir string, args CreateTypeArgs) error {
	argBytes, _ := json.Marshal(args)
	resp, err := c.send(&Request{Command: CmdCreateType, Dir: dir, Args: argBytes})
	if err != nil {
		return err
	}
	if resp.Status != "ok" {
		return fmt.Errorf("%s", resp.Error)
	}
	return nil
}
