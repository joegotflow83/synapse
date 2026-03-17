package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/synapse-tool/synapse/internal/ledger"
	"github.com/synapse-tool/synapse/internal/model"
)

var exportCmd = &cobra.Command{
	Use:   "export",
	Short: "Export entries to a JSON file",
	Long:  `Reads entries from the event log, optionally filtering by type, and writes them as a JSON array to the specified output file.`,
	RunE:  runExport,
}

var (
	exportType   string
	exportOutput string
)

func init() {
	exportCmd.Flags().StringVar(&exportType, "type", "", "filter by entry type")
	exportCmd.Flags().StringVar(&exportOutput, "output", "", "output file path (required)")
	_ = exportCmd.MarkFlagRequired("output")
	rootCmd.AddCommand(exportCmd)
}

func runExport(cmd *cobra.Command, args []string) error {
	l, err := ledger.Open(dirFlag)
	if err != nil {
		exitOnError(err)
		return err
	}

	results, err := l.Query(ledger.QueryOpts{
		Type: exportType,
	})
	if err != nil {
		exitOnError(err)
		return err
	}

	if results == nil {
		results = make([]*model.Entry, 0)
	}

	f, err := os.Create(exportOutput)
	if err != nil {
		return fmt.Errorf("create output file: %w", err)
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(results); err != nil {
		return fmt.Errorf("encode results: %w", err)
	}

	fmt.Fprintf(os.Stdout, "exported %d entries to %s\n", len(results), exportOutput)
	return nil
}
