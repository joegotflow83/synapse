#!/usr/bin/env bash
# Validation script for running Go tests with different strategies
# (equivalent UX to your pytest wrapper, but adapted to Go's test runner)
set -euo pipefail

# ──────────────────────────────────────────────────────────────────────────────
# Configuration – adjust these paths to your project
# ──────────────────────────────────────────────────────────────────────────────
TEST_DIR="./..."                  # or e.g. "./pkg/..." or "./api/..."
COVERPROFILE="coverage.out"       # optional – comment out if you don't want coverage
LOG_FILE="gotest.log"

# Colors for output
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# ──────────────────────────────────────────────────────────────────────────────
usage() {
    cat << EOF
Usage: $0 {full|targeted|lf|ff} [extra go test flags]

Commands:
  full                Run full test suite
  targeted <pattern>  Run specific test(s) using -run regexp
  lf                  Run last-failed tests (not natively supported in Go)
  ff                  Run failed-first (not natively supported in Go)

Examples:
  $0 full
  $0 full -short -race
  $0 targeted TestPostBuild
  $0 targeted 'TestAPI_.*Invalid'
  $0 targeted ./api/tests -run '^TestGalacticHangar$$' -v
  $0 lf               (shows help message – Go has no built-in --lf)
  $0 ff -cover        (shows help message)

Extra flags are passed directly to go test.
EOF
    exit 1
}

if [ $# -eq 0 ]; then
    usage
fi

COMMAND="$1"
shift

# ──────────────────────────────────────────────────────────────────────────────
echo "Logging to → $LOG_FILE"
echo ""

case "$COMMAND" in
    full)
        echo -e "${GREEN}Running full test suite...${NC}"
        set -x
        go test "$TEST_DIR" \
            -v \
            -coverprofile="$COVERPROFILE" \
            "$@" 2>&1 | tee "$LOG_FILE"
        set +x
        ;;

    targeted)
        if [ $# -eq 0 ]; then
            echo -e "${RED}Error: targeted requires a -run pattern or package path${NC}"
            usage
        fi
        echo -e "${GREEN}Running targeted tests: go test ... -run ... $@${NC}"
        set -x
        go test \
            -v \
            -coverprofile="$COVERPROFILE" \
            -run "$1" \
            "${@:2}" \
            2>&1 | tee "$LOG_FILE"
        set +x
        ;;

    lf|ff)
        echo -e "${YELLOW}⚠️  Go's built-in test runner has no --lf / --last-failed or --ff / --failed-first${NC}"
        echo ""
        echo "Options you have instead:"
        echo "  • Manually repeat failing patterns with -run"
        echo "    $0 targeted TestNameYouKnowFailed"
        echo ""
        echo "  • Use -v and grep the log:"
        echo "    grep -C 3 -i fail $LOG_FILE"
        echo ""
        echo "  • For real caching of failed tests consider third-party tools:"
        echo "    • gotestsum -- -run @last-failed   (needs setup)"
        echo "    • go-test-report + custom script"
        echo "    • Save failing test names via go test -json | jq"
        echo ""
        exit 2
        ;;

    *)
        echo -e "${RED}Unknown command: $COMMAND${NC}"
        usage
        ;;
esac

EXIT_CODE=${PIPESTATUS[0]}

echo ""
if [ $EXIT_CODE -eq 0 ]; then
    echo -e "${GREEN}✓ Tests passed${NC}"
    if [ -f "$COVERPROFILE" ]; then
        echo "  Coverage profile saved → $COVERPROFILE"
        go tool cover -func="$COVERPROFILE" | tail -n1
    fi
else
    echo -e "${RED}✗ Tests failed (see $LOG_FILE for details)${NC}"
fi

exit $EXIT_CODE