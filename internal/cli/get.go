package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/synapse-tool/synapse/internal/ledger"
	"github.com/synapse-tool/synapse/internal/model"
)

var getCmd = &cobra.Command{
	Use:   "get",
	Short: "Get an entry by ID",
	Long:  `Retrieves an entry by its ID. With --history, returns all versions ordered by timestamp ascending.`,
	RunE:  runGet,
}

var (
	getID      string
	getHistory bool
	getFormat  string
)

func init() {
	getCmd.Flags().StringVar(&getID, "id", "", "entry ID to retrieve (required)")
	getCmd.MarkFlagRequired("id")
	getCmd.Flags().BoolVar(&getHistory, "history", false, "return all versions, not just latest")
	getCmd.Flags().StringVar(&getFormat, "format", "json", "output format: json or jsonl")
	rootCmd.AddCommand(getCmd)
}

func runGet(cmd *cobra.Command, args []string) error {
	if getFormat != "json" && getFormat != "jsonl" {
		fmt.Fprintf(os.Stderr, "invalid --format %q: must be json or jsonl\n", getFormat)
		os.Exit(1)
	}

	l, err := ledger.Open(dirFlag)
	if err != nil {
		exitOnError(err)
		return err
	}

	results, err := l.Get(getID, getHistory)
	if err != nil {
		exitOnError(err)
		return err
	}

	if results == nil {
		fmt.Fprintf(os.Stderr, "entry not found: %s\n", getID)
		os.Exit(2)
	}

	if getFormat == "jsonl" {
		enc := json.NewEncoder(os.Stdout)
		for _, entry := range results {
			if err := enc.Encode(entry); err != nil {
				return fmt.Errorf("encode entry: %w", err)
			}
		}
	} else {
		if results == nil {
			results = make([]*model.Entry, 0)
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(results); err != nil {
			return fmt.Errorf("encode results: %w", err)
		}
	}

	return nil
}
