package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/synapse-tool/synapse/internal/ledger"
	"github.com/synapse-tool/synapse/internal/model"
)

var insertCmd = &cobra.Command{
	Use:   "insert",
	Short: "Insert a new entry into the ledger",
	Long:  `Creates a new entry with the specified type and data, appends it to the event log, and prints the entry ID.`,
	RunE:  runInsert,
}

var (
	insertType     string
	insertData     string
	insertMetadata string
	insertID       string
	insertAgentID  string
)

func init() {
	insertCmd.Flags().StringVar(&insertType, "type", "", "entry type (required)")
	insertCmd.Flags().StringVar(&insertData, "data", "", "entry data as JSON string (required)")
	insertCmd.Flags().StringVar(&insertMetadata, "metadata", "", "agent metadata as JSON string")
	insertCmd.Flags().StringVar(&insertID, "id", "", "custom entry ID (auto-generates UUID v4 if omitted)")
	insertCmd.Flags().StringVar(&insertAgentID, "agent-id", "", "agent identifier")
	_ = insertCmd.MarkFlagRequired("type")
	_ = insertCmd.MarkFlagRequired("data")
	rootCmd.AddCommand(insertCmd)
}

func runInsert(cmd *cobra.Command, args []string) error {
	l, err := ledger.Open(dirFlag)
	if err != nil {
		exitOnError(err)
		return err
	}

	// Parse --data JSON.
	var data map[string]any
	if err := json.Unmarshal([]byte(insertData), &data); err != nil {
		fmt.Fprintf(os.Stderr, "invalid --data JSON: %v\n", err)
		os.Exit(1)
	}

	// Parse --metadata JSON if provided.
	var metadata map[string]any
	if insertMetadata != "" {
		if err := json.Unmarshal([]byte(insertMetadata), &metadata); err != nil {
			fmt.Fprintf(os.Stderr, "invalid --metadata JSON: %v\n", err)
			os.Exit(1)
		}
	}

	// Spec: if --id is provided, it must be a non-empty string.
	if cmd.Flags().Changed("id") && insertID == "" {
		fmt.Fprintln(os.Stderr, "error: --id must be a non-empty string")
		os.Exit(1)
	}

	entry := &model.Entry{
		ID:            insertID,
		Type:          insertType,
		AgentID:       insertAgentID,
		Data:          data,
		AgentMetadata: metadata,
	}

	if err := l.Insert(entry); err != nil {
		exitOnError(err)
		return err
	}

	fmt.Fprintln(os.Stdout, entry.ID)
	return nil
}
