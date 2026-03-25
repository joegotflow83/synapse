package cli

import (
	"os"
	"path/filepath"

	"github.com/synapse-tool/synapse/internal/daemon"
)

// daemonSocketPath returns the Unix socket path to probe for daemon-mode
// operation. Precedence: $SYNAPSE_SOCKET env var > <dir>/.synapse.sock.
func daemonSocketPath(dir string) string {
	if s := os.Getenv("SYNAPSE_SOCKET"); s != "" {
		return s
	}
	return filepath.Join(dir, ".synapse.sock")
}

// newDaemonClient returns a *daemon.Client if a healthy daemon is reachable at
// the default socket for dir, otherwise nil. CLI commands call this to
// transparently forward requests to the daemon when it is running, falling
// back to direct file I/O when it is not.
func newDaemonClient(dir string) *daemon.Client {
	c := daemon.NewClient(daemonSocketPath(dir))
	if c.Ping() != nil {
		return nil
	}
	return c
}

// absDir resolves dir to an absolute path for passing in daemon requests,
// so that relative paths (e.g. "./synapse") resolve consistently regardless
// of the daemon's working directory.
func absDir(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return dir
	}
	return abs
}
