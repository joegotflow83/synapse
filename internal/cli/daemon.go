package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/synapse-tool/synapse/internal/daemon"
)

var daemonCmd = &cobra.Command{
	Use:   "daemon",
	Short: "Manage the Synapse background daemon",
	Long: `The Synapse daemon is a persistent background process that serves requests
over a Unix socket. It eliminates the ~5.3ms Go-runtime startup cost on every
CLI invocation and keeps ledger file descriptors warm between requests.

Commands:
  synapse daemon start   -- start the daemon in the background
  synapse daemon stop    -- stop the running daemon
  synapse daemon status  -- show whether the daemon is running`,
}

var daemonSocketFlag string

var daemonStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the Synapse daemon in the background",
	RunE:  runDaemonStart,
}

var daemonStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the running Synapse daemon",
	RunE:  runDaemonStop,
}

var daemonStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show daemon status",
	RunE:  runDaemonStatus,
}

// daemonRunCmd is the internal command invoked by "daemon start" to run the
// server in the background process. It is hidden from user-visible help.
var daemonRunCmd = &cobra.Command{
	Use:    "_run",
	Hidden: true,
	RunE:   runDaemonRun,
}

func init() {
	daemonCmd.PersistentFlags().StringVar(&daemonSocketFlag, "socket", "",
		"Unix socket path (default: <dir>/.synapse.sock or $SYNAPSE_SOCKET)")
	daemonCmd.AddCommand(daemonStartCmd)
	daemonCmd.AddCommand(daemonStopCmd)
	daemonCmd.AddCommand(daemonStatusCmd)
	daemonCmd.AddCommand(daemonRunCmd)
	rootCmd.AddCommand(daemonCmd)
}

// resolveSocketPath returns the socket path to use for daemon commands.
// Precedence: --socket flag > $SYNAPSE_SOCKET > <dir>/.synapse.sock.
func resolveSocketPath(dir string) string {
	if daemonSocketFlag != "" {
		return daemonSocketFlag
	}
	return daemonSocketPath(dir)
}

func runDaemonStart(cmd *cobra.Command, args []string) error {
	socketPath := resolveSocketPath(dirFlag)

	// Check if a healthy daemon is already running.
	c := daemon.NewClient(socketPath)
	if c.Ping() == nil {
		fmt.Println("daemon is already running")
		return nil
	}

	// Resolve the binary path so the child process can be launched.
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("find executable: %w", err)
	}

	// Resolve absolute dir so the child uses a stable path.
	absD := absDir(dirFlag)

	childArgs := []string{"--dir", absD, "daemon", "_run", "--socket", socketPath}
	child := exec.Command(self, childArgs...)
	// Detach from the parent's session so the daemon outlives the parent.
	child.SysProcAttr = daemonSysProcAttr()
	child.Stdout = nil
	child.Stderr = nil
	child.Stdin = nil

	if err := child.Start(); err != nil {
		return fmt.Errorf("start daemon: %w", err)
	}

	// Write PID file alongside the socket for use by "daemon stop".
	pidPath := socketPath + ".pid"
	os.WriteFile(pidPath, []byte(strconv.Itoa(child.Process.Pid)), 0644) //nolint:errcheck

	// Wait up to 3 seconds for the socket to appear and the daemon to respond.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(socketPath); err == nil {
			if c.Ping() == nil {
				fmt.Printf("daemon started (pid %d, socket %s)\n", child.Process.Pid, socketPath)
				return nil
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("daemon did not start within 3 seconds (socket: %s)", socketPath)
}

func runDaemonStop(cmd *cobra.Command, args []string) error {
	socketPath := resolveSocketPath(dirFlag)
	c := daemon.NewClient(socketPath)
	if err := c.Ping(); err != nil {
		fmt.Println("daemon is not running")
		return nil
	}
	if err := c.Shutdown(); err != nil && !isDaemonGone(err) {
		return fmt.Errorf("shutdown: %w", err)
	}
	// Clean up the PID file if present.
	os.Remove(socketPath + ".pid") //nolint:errcheck
	fmt.Println("daemon stopped")
	return nil
}

func runDaemonStatus(cmd *cobra.Command, args []string) error {
	socketPath := resolveSocketPath(dirFlag)
	c := daemon.NewClient(socketPath)
	if err := c.Ping(); err != nil {
		fmt.Printf("daemon not running (socket: %s)\n", socketPath)
		return nil
	}

	pid := "unknown"
	if b, err := os.ReadFile(socketPath + ".pid"); err == nil {
		pid = strings.TrimSpace(string(b))
	}
	fmt.Printf("daemon running (pid %s, socket %s)\n", pid, socketPath)
	return nil
}

// runDaemonRun is the internal server process entry-point.
// It is invoked by "daemon start" in a detached background process.
func runDaemonRun(cmd *cobra.Command, args []string) error {
	socketPath := resolveSocketPath(dirFlag)
	if socketPath == "" {
		return fmt.Errorf("--socket is required")
	}
	// Ensure the parent directory of the socket exists.
	if err := os.MkdirAll(filepath.Dir(socketPath), 0755); err != nil {
		return fmt.Errorf("create socket directory: %w", err)
	}
	srv := daemon.NewServer(socketPath)
	return srv.Start()
}

// isDaemonGone returns true for errors that indicate the daemon connection
// was closed during shutdown (expected when the daemon exits quickly).
func isDaemonGone(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "connection reset") ||
		strings.Contains(s, "broken pipe") ||
		strings.Contains(s, "EOF") ||
		strings.Contains(s, "use of closed")
}
