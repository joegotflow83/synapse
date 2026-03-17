package cli

import (
	"fmt"
	"os"
	"strings"
)

// exitOnError checks the error for known patterns and exits with the
// appropriate exit code per the CLI spec. If the error doesn't match
// a known pattern, it returns false so the caller can fall through.
func exitOnError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()

	// Exit code 2: not initialized / not found.
	if strings.Contains(msg, "not initialized") {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
		return true
	}

	// Exit code 3: lock acquisition failure.
	if strings.Contains(msg, "lock") {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(3)
		return true
	}

	// Exit code 4: data integrity / CBOR corruption.
	if isIntegrityError(msg) {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(4)
		return true
	}

	return false
}

func isIntegrityError(msg string) bool {
	return strings.Contains(msg, "corrupted") ||
		strings.Contains(msg, "file too small") ||
		strings.Contains(msg, "decode entry")
}
