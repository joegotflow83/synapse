package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/synapse-tool/synapse/internal/ledger"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize a new Synapse data directory",
	Long:  `Creates the data directory and initializes events.cbor and types.cbor files.`,
	RunE:  runInit,
}

var forceFlag bool

func init() {
	initCmd.Flags().BoolVar(&forceFlag, "force", false, "reinitialize even if directory already exists")
	rootCmd.AddCommand(initCmd)
}

func runInit(cmd *cobra.Command, args []string) error {
	dir := dirFlag

	err := ledger.Init(dir, forceFlag)
	if err != nil {
		// "Already initialized" is a general validation error (exit code 1).
		// Exit code 2 is reserved for "data/file not found" (e.g., uninitialized directory).
		if strings.Contains(err.Error(), "already initialized") {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		// Handle lock (exit 3) and integrity (exit 4) errors via shared helper.
		exitOnError(err)
		return err
	}

	fmt.Fprintf(os.Stdout, "Initialized synapse directory: %s\n", dir)
	return nil
}
