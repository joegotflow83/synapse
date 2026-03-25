package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/synapse-tool/synapse/internal/daemon"
	"github.com/synapse-tool/synapse/internal/ledger"
)

var createTypeCmd = &cobra.Command{
	Use:   "create-type <name>",
	Short: "Register a new entry type with optional metadata",
	Long:  `Registers a type name in types.cbor with an optional description and example JSON payload.`,
	Args:  cobra.ExactArgs(1),
	RunE:  runCreateType,
}

var (
	createTypeDescription string
	createTypeExample     string
)

func init() {
	createTypeCmd.Flags().StringVar(&createTypeDescription, "description", "", "human-readable description of the type")
	createTypeCmd.Flags().StringVar(&createTypeExample, "example", "", "example JSON payload for the type")
	rootCmd.AddCommand(createTypeCmd)
}

func runCreateType(cmd *cobra.Command, args []string) error {
	typeName := args[0]

	// Try daemon first; fall back to direct file I/O if it is not running.
	if c := newDaemonClient(dirFlag); c != nil {
		err := c.CreateType(absDir(dirFlag), daemon.CreateTypeArgs{
			Name:        typeName,
			Description: createTypeDescription,
			Example:     createTypeExample,
		})
		if err != nil {
			exitOnError(err)
			return err
		}
		fmt.Println(typeName)
		return nil
	}

	l, err := ledger.Open(dirFlag)
	if err != nil {
		exitOnError(err)
		return err
	}

	if err := l.CreateType(typeName, createTypeDescription, createTypeExample); err != nil {
		exitOnError(err)
		return err
	}

	fmt.Println(typeName)
	return nil
}
