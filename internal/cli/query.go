package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/synapse-tool/synapse/internal/ledger"
	"github.com/synapse-tool/synapse/internal/model"
)

var queryCmd = &cobra.Command{
	Use:   "query",
	Short: "Query entries from the ledger",
	Long:  `Reads entries from the event log, applying optional type, filter, and limit constraints. Outputs results as JSON array or JSONL.`,
	RunE:  runQuery,
}

var (
	queryType   string
	queryFilter string
	queryLimit  int
	queryFormat string
)

func init() {
	queryCmd.Flags().StringVar(&queryType, "type", "", "filter by entry type")
	queryCmd.Flags().StringVar(&queryFilter, "filter", "", "filter expression (space-separated key=value, since:DATE, until:DATE)")
	queryCmd.Flags().IntVar(&queryLimit, "limit", 0, "max entries to return (0 = unlimited)")
	queryCmd.Flags().StringVar(&queryFormat, "format", "json", "output format: json or jsonl")
	rootCmd.AddCommand(queryCmd)
}

func runQuery(cmd *cobra.Command, args []string) error {
	if queryFormat != "json" && queryFormat != "jsonl" {
		fmt.Fprintf(os.Stderr, "invalid --format %q: must be json or jsonl\n", queryFormat)
		os.Exit(1)
	}

	l, err := ledger.Open(dirFlag)
	if err != nil {
		exitOnError(err)
		return err
	}

	results, err := l.Query(ledger.QueryOpts{
		Type:   queryType,
		Filter: queryFilter,
		Limit:  queryLimit,
	})
	if err != nil {
		if strings.Contains(err.Error(), "parse filter") {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		exitOnError(err)
		return err
	}

	if queryFormat == "jsonl" {
		enc := json.NewEncoder(os.Stdout)
		for _, entry := range results {
			if err := enc.Encode(entry); err != nil {
				return fmt.Errorf("encode entry: %w", err)
			}
		}
	} else {
		// JSON array output. Use empty array for no results.
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
