package cli

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/synapse-tool/synapse/internal/ledger"
)

var reindexCmd = &cobra.Command{
	Use:   "reindex",
	Short: "Rebuild the index from the event log",
	Long: `Performs a full scan of events.cbor and rewrites index.cbor from scratch.

Use this command to recover from index drift (e.g. after a crash that left the
index stale) or to create an index for a ledger that pre-dates the index feature.`,
	RunE: runReindex,
}

func init() {
	rootCmd.AddCommand(reindexCmd)
}

func runReindex(cmd *cobra.Command, args []string) error {
	// Try daemon first; fall back to direct file I/O if it is not running.
	if c := newDaemonClient(dirFlag); c != nil {
		raw, err := c.Reindex(absDir(dirFlag))
		if err != nil {
			exitOnError(err)
			return err
		}
		var result map[string]int
		if err := json.Unmarshal(raw, &result); err != nil {
			return fmt.Errorf("decode daemon response: %w", err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Reindex complete: %d entries indexed\n", result["entries_indexed"])
		return nil
	}

	l, err := ledger.Open(dirFlag)
	if err != nil {
		exitOnError(err)
		return err
	}

	n, err := l.Reindex()
	if err != nil {
		exitOnError(err)
		return err
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Reindex complete: %d entries indexed\n", n)
	return nil
}
