package cli

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/synapse-tool/synapse/internal/ledger"
)

// compact uses exitOnError from errors.go for exit codes 2, 3, 4.

var compactCmd = &cobra.Command{
	Use:   "compact",
	Short: "Compact the event log by deduplicating entries",
	Long:  `Removes older versions of entries with the same ID, keeping only the latest. Creates a backup before compaction.`,
	RunE:  runCompact,
}

func init() {
	rootCmd.AddCommand(compactCmd)
}

func runCompact(cmd *cobra.Command, args []string) error {
	// Try daemon first; fall back to direct file I/O if it is not running.
	if c := newDaemonClient(dirFlag); c != nil {
		raw, err := c.Compact(absDir(dirFlag))
		if err != nil {
			exitOnError(err)
			return err
		}
		var stats ledger.CompactStats
		if err := json.Unmarshal(raw, &stats); err != nil {
			return fmt.Errorf("decode daemon response: %w", err)
		}
		return printCompactStats(cmd, stats)
	}

	l, err := ledger.Open(dirFlag)
	if err != nil {
		exitOnError(err)
		return err
	}

	stats, err := l.Compact()
	if err != nil {
		exitOnError(err)
		return err
	}

	return printCompactStats(cmd, stats)
}

func printCompactStats(cmd *cobra.Command, stats ledger.CompactStats) error {
	if stats.NoOp {
		fmt.Fprintf(cmd.OutOrStdout(), "Compaction complete: already compact, nothing to do (%d entries)\n",
			stats.EntriesBefore)
		return nil
	}
	bytesSaved := stats.BytesBefore - stats.BytesAfter
	fmt.Fprintf(cmd.OutOrStdout(), "Compaction complete: %d entries -> %d entries, %d bytes saved\n",
		stats.EntriesBefore, stats.EntriesAfter, bytesSaved)
	return nil
}
