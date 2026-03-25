package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/synapse-tool/synapse/internal/ledger"
)

var listTypesCmd = &cobra.Command{
	Use:   "list-types",
	Short: "List all known entry types",
	Long:  `Lists all types from types.cbor metadata plus types discovered from entries in events.cbor.`,
	RunE:  runListTypes,
}

var listTypesFormat string

func init() {
	listTypesCmd.Flags().StringVar(&listTypesFormat, "format", "json", "output format: json or jsonl")
	rootCmd.AddCommand(listTypesCmd)
}

// typeInfoJSON is the JSON representation of a TypeInfo.
type typeInfoJSON struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Example     string `json:"example,omitempty"`
	CreatedAt   int64  `json:"created_at,omitempty"`
	Registered  bool   `json:"registered"`
}

func runListTypes(cmd *cobra.Command, args []string) error {
	if listTypesFormat != "json" && listTypesFormat != "jsonl" {
		fmt.Fprintf(os.Stderr, "invalid --format %q: must be json or jsonl\n", listTypesFormat)
		os.Exit(1)
	}

	// Try daemon first; fall back to direct file I/O if it is not running.
	if c := newDaemonClient(dirFlag); c != nil {
		raw, err := c.ListTypes(absDir(dirFlag))
		if err != nil {
			exitOnError(err)
			return err
		}
		// The daemon serializes []ledger.TypeInfo which has the same JSON
		// field names as typeInfoJSON, so we can unmarshal directly.
		var items []typeInfoJSON
		if err := json.Unmarshal(raw, &items); err != nil {
			return fmt.Errorf("decode daemon response: %w", err)
		}
		return writeListTypesOutput(items)
	}

	l, err := ledger.Open(dirFlag)
	if err != nil {
		exitOnError(err)
		return err
	}

	typeInfos, err := l.ListTypes()
	if err != nil {
		exitOnError(err)
		return err
	}

	items := make([]typeInfoJSON, len(typeInfos))
	for i, ti := range typeInfos {
		items[i] = typeInfoJSON{
			Name:        ti.Name,
			Description: ti.Description,
			Example:     ti.Example,
			CreatedAt:   ti.CreatedAt,
			Registered:  ti.Registered,
		}
	}

	return writeListTypesOutput(items)
}

func writeListTypesOutput(items []typeInfoJSON) error {
	if listTypesFormat == "jsonl" {
		enc := json.NewEncoder(os.Stdout)
		for _, item := range items {
			if err := enc.Encode(item); err != nil {
				return fmt.Errorf("encode type: %w", err)
			}
		}
	} else {
		if items == nil {
			items = make([]typeInfoJSON, 0)
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(items); err != nil {
			return fmt.Errorf("encode types: %w", err)
		}
	}
	return nil
}
