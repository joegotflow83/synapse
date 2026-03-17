package cli

import (
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

	bytesSaved := stats.BytesBefore - stats.BytesAfter
	fmt.Fprintf(cmd.OutOrStdout(), "Compaction complete: %d entries -> %d entries, %d bytes saved\n",
		stats.EntriesBefore, stats.EntriesAfter, bytesSaved)
	return nil
}
