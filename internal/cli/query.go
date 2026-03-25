package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/synapse-tool/synapse/internal/daemon"
	"github.com/synapse-tool/synapse/internal/ledger"
	"github.com/synapse-tool/synapse/internal/model"
)

var queryCmd = &cobra.Command{
	Use:   "query",
	Short: "Query entries from the ledger",
	Long:  `Reads entries from the event log, applying optional type, filter, and limit constraints. Outputs results as JSON array or JSONL. Use --queries for batch multi-query mode.`,
	RunE:  runQuery,
}

var (
	queryTypes       []string
	queryFilters     []string
	queryLimit       int
	queryFormat      string
	queryQueriesFile string
)

func init() {
	queryCmd.Flags().StringArrayVar(&queryTypes, "type", nil, "filter by entry type (repeatable: each --type pairs with the corresponding --filter for multi-query mode)")
	queryCmd.Flags().StringArrayVar(&queryFilters, "filter", nil, "filter expression (repeatable, paired positionally with --type)")
	queryCmd.Flags().IntVar(&queryLimit, "limit", 0, "max entries to return (0 = unlimited)")
	queryCmd.Flags().StringVar(&queryFormat, "format", "json", "output format: json or jsonl")
	queryCmd.Flags().StringVar(&queryQueriesFile, "queries", "", `JSON file with batch query specs: [{"type":"...","filter":"...","limit":N}]`)
	rootCmd.AddCommand(queryCmd)
}

// isMultiQuery returns true when the caller supplied more than one --type or
// --filter flag, triggering multi-query (batch) mode from the command line.
func isMultiQuery() bool {
	return len(queryTypes) > 1 || len(queryFilters) > 1
}

// singleType returns the first (or only) --type value, or "" if none was given.
func singleType() string {
	if len(queryTypes) > 0 {
		return queryTypes[0]
	}
	return ""
}

// singleFilter returns the first (or only) --filter value, or "" if none.
func singleFilter() string {
	if len(queryFilters) > 0 {
		return queryFilters[0]
	}
	return ""
}

// buildBatchSpecsFromFlags converts repeated --type/--filter flags into
// BatchQuerySpec values. Entries are paired positionally; the shorter list is
// padded with empty strings. The global --limit is applied to every spec.
func buildBatchSpecsFromFlags() []ledger.BatchQuerySpec {
	n := len(queryTypes)
	if len(queryFilters) > n {
		n = len(queryFilters)
	}
	specs := make([]ledger.BatchQuerySpec, n)
	for i := range specs {
		if i < len(queryTypes) {
			specs[i].Type = queryTypes[i]
		}
		if i < len(queryFilters) {
			specs[i].Filter = queryFilters[i]
		}
		specs[i].Limit = queryLimit
	}
	return specs
}

func runQuery(cmd *cobra.Command, args []string) error {
	if queryFormat != "json" && queryFormat != "jsonl" {
		fmt.Fprintf(os.Stderr, "invalid --format %q: must be json or jsonl\n", queryFormat)
		os.Exit(1)
	}

	// --queries file cannot be combined with --type, --filter, or --limit.
	if queryQueriesFile != "" {
		if cmd.Flags().Changed("type") || cmd.Flags().Changed("filter") || cmd.Flags().Changed("limit") {
			fmt.Fprintln(os.Stderr, "--queries cannot be combined with --type, --filter, or --limit")
			os.Exit(1)
		}
	}

	// Try daemon first; fall back to direct file I/O if it is not running.
	if c := newDaemonClient(dirFlag); c != nil {
		return runQueryViaDaemon(cmd, c)
	}

	l, err := ledger.Open(dirFlag)
	if err != nil {
		exitOnError(err)
		return err
	}

	// Batch mode: --queries file takes precedence.
	if queryQueriesFile != "" {
		return runBatchQuery(l)
	}

	// Multi-query mode: repeated --type/--filter flags.
	if isMultiQuery() {
		return runMultiQueryFromFlags(l)
	}

	// Single-query mode (existing behavior).
	results, err := l.Query(ledger.QueryOpts{
		Type:   singleType(),
		Filter: singleFilter(),
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

// runMultiQueryFromFlags executes a batch query built from repeated --type/--filter flags.
func runMultiQueryFromFlags(l *ledger.Ledger) error {
	specs := buildBatchSpecsFromFlags()
	results, err := l.QueryBatch(specs)
	if err != nil {
		if strings.Contains(err.Error(), "parse filter") {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		exitOnError(err)
		return err
	}
	return writeBatchQueryResults(results)
}

// runQueryViaDaemon executes the query through the running daemon.
func runQueryViaDaemon(cmd *cobra.Command, c *daemon.Client) error {
	dir := absDir(dirFlag)

	// --queries file batch mode.
	if queryQueriesFile != "" {
		data, err := os.ReadFile(queryQueriesFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "read --queries file: %v\n", err)
			os.Exit(1)
		}
		raw, err := c.QueryBatch(dir, data)
		if err != nil {
			exitOnError(err)
			return err
		}
		return writeQueryBatchOutput(raw)
	}

	// Multi-query mode via repeated --type/--filter flags.
	if isMultiQuery() {
		specs := buildBatchSpecsFromFlags()
		data, err := json.Marshal(specs)
		if err != nil {
			return fmt.Errorf("marshal batch specs: %w", err)
		}
		raw, err := c.QueryBatch(dir, data)
		if err != nil {
			if strings.Contains(err.Error(), "parse filter") {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			exitOnError(err)
			return err
		}
		return writeQueryBatchOutput(raw)
	}

	raw, err := c.Query(dir, daemon.QueryArgs{
		Type:   singleType(),
		Filter: singleFilter(),
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

	// Unmarshal and re-encode with the requested format/indentation.
	var results []*model.Entry
	if err := json.Unmarshal(raw, &results); err != nil {
		return fmt.Errorf("decode daemon response: %w", err)
	}
	if queryFormat == "jsonl" {
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

// writeQueryBatchOutput formats raw batch query JSON from the daemon to stdout.
func writeQueryBatchOutput(raw []byte) error {
	type batchResult struct {
		Entries []*model.Entry `json:"entries"`
	}
	var results []batchResult
	if err := json.Unmarshal(raw, &results); err != nil {
		return fmt.Errorf("decode batch response: %w", err)
	}
	enc := json.NewEncoder(os.Stdout)
	if queryFormat == "jsonl" {
		for i, r := range results {
			row := map[string]interface{}{"index": i, "entries": r.Entries}
			if err := enc.Encode(row); err != nil {
				return fmt.Errorf("encode result %d: %w", i, err)
			}
		}
	} else {
		type indexedResult struct {
			Index   int            `json:"index"`
			Entries []*model.Entry `json:"entries"`
		}
		out := make([]indexedResult, len(results))
		for i, r := range results {
			out[i] = indexedResult{Index: i, Entries: r.Entries}
		}
		enc.SetIndent("", "  ")
		if err := enc.Encode(out); err != nil {
			return fmt.Errorf("encode results: %w", err)
		}
	}
	return nil
}

// runBatchQuery reads a JSON file of query specs, executes them in one lock
// acquisition, and writes the grouped results to stdout.
func runBatchQuery(l *ledger.Ledger) error {
	data, err := os.ReadFile(queryQueriesFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read --queries file: %v\n", err)
		os.Exit(1)
	}

	var specs []ledger.BatchQuerySpec
	if err := json.Unmarshal(data, &specs); err != nil {
		fmt.Fprintf(os.Stderr, "parse --queries file: %v\n", err)
		os.Exit(1)
	}

	results, err := l.QueryBatch(specs)
	if err != nil {
		if strings.Contains(err.Error(), "parse filter") {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		exitOnError(err)
		return err
	}

	return writeBatchQueryResults(results)
}

// writeBatchQueryResults encodes a slice of BatchQueryResult to stdout using
// the current --format setting.
func writeBatchQueryResults(results []ledger.BatchQueryResult) error {
	enc := json.NewEncoder(os.Stdout)
	if queryFormat == "jsonl" {
		// One line per query result: {"index":N,"entries":[...]}
		for i, r := range results {
			row := map[string]interface{}{"index": i, "entries": r.Entries}
			if err := enc.Encode(row); err != nil {
				return fmt.Errorf("encode result %d: %w", i, err)
			}
		}
	} else {
		// JSON array: each element is {"index":N,"entries":[...]}.
		type indexedResult struct {
			Index   int            `json:"index"`
			Entries []*model.Entry `json:"entries"`
		}
		out := make([]indexedResult, len(results))
		for i, r := range results {
			out[i] = indexedResult{Index: i, Entries: r.Entries}
		}
		enc.SetIndent("", "  ")
		if err := enc.Encode(out); err != nil {
			return fmt.Errorf("encode results: %w", err)
		}
	}
	return nil
}
