package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/synapse-tool/synapse/internal/daemon"
	"github.com/synapse-tool/synapse/internal/ledger"
	"github.com/synapse-tool/synapse/internal/model"
)

var insertCmd = &cobra.Command{
	Use:   "insert",
	Short: "Insert one or more entries into the ledger",
	Long: `Creates new entries and appends them to the event log.

Single entry:
  synapse insert --type task --data '{"title":"Buy milk"}'

Batch via repeated --data (all entries share --type):
  synapse insert --type task --data '{"title":"A"}' --data '{"title":"B"}'

Batch via stdin JSONL (one JSON object per line with "type" and "data" fields):
  echo '{"type":"task","data":{"title":"A"}}' | synapse insert --stdin

Prints one entry ID per line.`,
	RunE: runInsert,
}

var (
	insertType        string
	insertDataList    []string
	insertMetadata    string
	insertID          string
	insertAgentID     string
	insertStdin       bool
	insertMaxDataSize int
)

func init() {
	insertCmd.Flags().StringVar(&insertType, "type", "", "entry type (required when using --data)")
	insertCmd.Flags().StringArrayVar(&insertDataList, "data", nil, "entry data as JSON string (repeatable for batch insert)")
	insertCmd.Flags().StringVar(&insertMetadata, "metadata", "", "agent metadata as JSON string")
	insertCmd.Flags().StringVar(&insertID, "id", "", "custom entry ID (auto-generates UUID v4 if omitted; only valid for single-entry inserts)")
	insertCmd.Flags().StringVar(&insertAgentID, "agent-id", "", "agent identifier")
	insertCmd.Flags().BoolVar(&insertStdin, "stdin", false, "read JSONL entries from stdin (each line: {\"type\":\"...\",\"data\":{...}})")
	insertCmd.Flags().IntVar(&insertMaxDataSize, "max-data-size", 0, "reject entries whose serialized data exceeds this many bytes (0 = no hard limit; recommended max: 256)")
	rootCmd.AddCommand(insertCmd)
}

// stdinEntry is the JSON shape expected for each JSONL line when --stdin is used.
type stdinEntry struct {
	Type     string         `json:"type"`
	Data     map[string]any `json:"data"`
	ID       string         `json:"id"`
	AgentID  string         `json:"agent_id"`
	Metadata map[string]any `json:"metadata"`
}

func runInsert(cmd *cobra.Command, args []string) error {
	if !insertStdin && len(insertDataList) == 0 {
		fmt.Fprintln(os.Stderr, "error: one of --data or --stdin is required")
		os.Exit(1)
	}
	if insertStdin && len(insertDataList) > 0 {
		fmt.Fprintln(os.Stderr, "error: --stdin and --data are mutually exclusive")
		os.Exit(1)
	}
	if len(insertDataList) > 1 && cmd.Flags().Changed("id") {
		fmt.Fprintln(os.Stderr, "error: --id may not be used with multiple --data flags")
		os.Exit(1)
	}
	if cmd.Flags().Changed("id") && insertID == "" {
		fmt.Fprintln(os.Stderr, "error: --id must be a non-empty string")
		os.Exit(1)
	}

	var entries []*model.Entry

	if insertStdin {
		// JSONL mode: each line is {"type":"...","data":{...},...}
		scanner := bufio.NewScanner(os.Stdin)
		lineNum := 0
		for scanner.Scan() {
			lineNum++
			line := scanner.Text()
			if line == "" {
				continue
			}
			var se stdinEntry
			if err := json.Unmarshal([]byte(line), &se); err != nil {
				fmt.Fprintf(os.Stderr, "invalid JSON on stdin line %d: %v\n", lineNum, err)
				os.Exit(1)
			}
			if se.Type == "" {
				fmt.Fprintf(os.Stderr, "stdin line %d: missing \"type\" field\n", lineNum)
				os.Exit(1)
			}
			if se.Data == nil {
				fmt.Fprintf(os.Stderr, "stdin line %d: missing \"data\" field\n", lineNum)
				os.Exit(1)
			}
			entries = append(entries, &model.Entry{
				ID:            se.ID,
				Type:          se.Type,
				AgentID:       se.AgentID,
				Data:          se.Data,
				AgentMetadata: se.Metadata,
			})
		}
		if err := scanner.Err(); err != nil {
			fmt.Fprintf(os.Stderr, "reading stdin: %v\n", err)
			os.Exit(1)
		}
		if len(entries) == 0 {
			fmt.Fprintln(os.Stderr, "error: --stdin provided but no entries read from stdin")
			os.Exit(1)
		}
	} else {
		// --data mode: all entries share --type, --metadata, --agent-id.
		if insertType == "" {
			fmt.Fprintln(os.Stderr, "error: --type is required when using --data")
			os.Exit(1)
		}

		var metadata map[string]any
		if insertMetadata != "" {
			if err := json.Unmarshal([]byte(insertMetadata), &metadata); err != nil {
				fmt.Fprintf(os.Stderr, "invalid --metadata JSON: %v\n", err)
				os.Exit(1)
			}
		}

		for i, raw := range insertDataList {
			var data map[string]any
			if err := json.Unmarshal([]byte(raw), &data); err != nil {
				fmt.Fprintf(os.Stderr, "invalid --data JSON (item %d): %v\n", i+1, err)
				os.Exit(1)
			}
			id := ""
			if len(insertDataList) == 1 {
				id = insertID
			}
			entries = append(entries, &model.Entry{
				ID:            id,
				Type:          insertType,
				AgentID:       insertAgentID,
				Data:          data,
				AgentMetadata: metadata,
			})
		}
	}

	// Enforce hard size limit if --max-data-size was specified.
	if insertMaxDataSize > 0 {
		for i, e := range entries {
			b, _ := json.Marshal(e.Data)
			if len(b) > insertMaxDataSize {
				fmt.Fprintf(os.Stderr, "error: entry %d data exceeds --max-data-size (%d > %d bytes)\n", i+1, len(b), insertMaxDataSize)
				os.Exit(1)
			}
		}
	}

	// Try daemon first; fall back to direct file I/O if it is not running.
	if c := newDaemonClient(dirFlag); c != nil {
		dir := absDir(dirFlag)
		if len(entries) == 1 {
			e := entries[0]
			id, err := c.Insert(dir, daemon.InsertArgs{
				Type:     e.Type,
				Data:     e.Data,
				Metadata: e.AgentMetadata,
				ID:       e.ID,
				AgentID:  e.AgentID,
			})
			if err != nil {
				exitOnError(err)
				return err
			}
			fmt.Fprintln(os.Stdout, id)
			return nil
		}
		batchArgs := daemon.InsertBatchArgs{Entries: make([]daemon.InsertArgs, len(entries))}
		for i, e := range entries {
			batchArgs.Entries[i] = daemon.InsertArgs{
				Type:     e.Type,
				Data:     e.Data,
				Metadata: e.AgentMetadata,
				ID:       e.ID,
				AgentID:  e.AgentID,
			}
		}
		ids, err := c.InsertBatch(dir, batchArgs)
		if err != nil {
			exitOnError(err)
			return err
		}
		for _, id := range ids {
			fmt.Fprintln(os.Stdout, id)
		}
		return nil
	}

	// Direct file I/O fallback.
	l, err := ledger.Open(dirFlag)
	if err != nil {
		exitOnError(err)
		return err
	}

	if len(entries) == 1 {
		if err := l.Insert(entries[0]); err != nil {
			exitOnError(err)
			return err
		}
		fmt.Fprintln(os.Stdout, entries[0].ID)
		return nil
	}

	if err := l.InsertBatch(entries); err != nil {
		exitOnError(err)
		return err
	}
	for _, e := range entries {
		fmt.Fprintln(os.Stdout, e.ID)
	}
	return nil
}
