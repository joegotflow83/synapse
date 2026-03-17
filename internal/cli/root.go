package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var dirFlag string

var rootCmd = &cobra.Command{
	Use:   "synapse",
	Short: "A lightweight, file-based, append-only ledger for multi-agent collaboration",
	Long: `Synapse is a persistence layer for multi-agent LLM collaboration.
It enables autonomous agents to share state via a shared filesystem volume
with a CLI-first interface for easy integration.`,
	SilenceErrors: true,
	SilenceUsage:  true,
}

func init() {
	defaultDir := os.Getenv("SYNAPSE_DIR")
	if defaultDir == "" {
		defaultDir = "./synapse"
	}
	rootCmd.PersistentFlags().StringVar(&dirFlag, "dir", defaultDir, "synapse data directory (env: SYNAPSE_DIR)")
}

// Execute runs the root command.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
