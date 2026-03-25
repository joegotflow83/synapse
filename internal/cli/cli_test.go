package cli_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// buildBinary compiles the synapse binary to a temp directory and returns its path.
func buildBinary(t *testing.T) string {
	t.Helper()
	binPath := filepath.Join(t.TempDir(), "synapse")
	cmd := exec.Command("go", "build", "-o", binPath, "./cmd/synapse/")
	cmd.Dir = findModRoot(t)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("failed to build binary: %v\n%s", err, out)
	}
	return binPath
}

// findModRoot walks up from the test file to find the go.mod directory.
func findModRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find go.mod")
		}
		dir = parent
	}
}

// runSynapse runs the synapse binary with the given args and returns stdout, stderr, and exit code.
func runSynapse(t *testing.T, bin string, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	var outBuf, errBuf strings.Builder
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err := cmd.Run()
	exitCode = 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			t.Fatalf("failed to run synapse: %v", err)
		}
	}
	return outBuf.String(), errBuf.String(), exitCode
}

var testBin string

func TestMain(m *testing.M) {
	// Build binary once for all tests.
	binPath, err := filepath.Abs("../../cmd/synapse/")
	if err != nil {
		panic(err)
	}
	tmpDir, err := os.MkdirTemp("", "synapse-test-bin")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(tmpDir)

	outPath := filepath.Join(tmpDir, "synapse")
	// Find module root
	dir, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			break
		}
		dir = filepath.Dir(dir)
	}
	_ = binPath
	cmd := exec.Command("go", "build", "-o", outPath, "./cmd/synapse/")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		panic("build failed: " + string(out))
	}
	testBin = outPath
	os.Exit(m.Run())
}

// run is a convenience wrapper using the pre-built binary.
func run(t *testing.T, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	return runSynapse(t, testBin, args...)
}

// --- Integration Tests ---

func TestFullWorkflow(t *testing.T) {
	dir := t.TempDir()

	// 1. Init
	stdout, _, code := run(t, "--dir", dir, "init")
	if code != 0 {
		t.Fatalf("init failed with code %d", code)
	}
	if !strings.Contains(stdout, "Initialized") {
		t.Fatalf("unexpected init output: %s", stdout)
	}

	// 2. Init again without --force should fail with exit code 1 (general error).
	// Exit code 2 is reserved for "data/file not found" (e.g., uninitialized directory).
	_, stderr, code := run(t, "--dir", dir, "init")
	if code != 1 {
		t.Fatalf("expected exit code 1 for duplicate init, got %d: %s", code, stderr)
	}

	// 3. Init with --force should succeed
	stdout, _, code = run(t, "--dir", dir, "init", "--force")
	if code != 0 {
		t.Fatalf("init --force failed with code %d", code)
	}

	// 4. Insert entries
	stdout, _, code = run(t, "--dir", dir, "insert",
		"--type", "task",
		"--data", `{"title":"Write tests","priority":"high"}`,
		"--id", "task-001",
		"--agent-id", "agent-A")
	if code != 0 {
		t.Fatalf("insert failed with code %d", code)
	}
	id := strings.TrimSpace(stdout)
	if id != "task-001" {
		t.Fatalf("expected id task-001, got %q", id)
	}

	// Insert second entry of same type
	stdout, _, code = run(t, "--dir", dir, "insert",
		"--type", "task",
		"--data", `{"title":"Review code","priority":"low"}`,
		"--id", "task-002")
	if code != 0 {
		t.Fatalf("insert 2 failed with code %d", code)
	}

	// Insert entry with different type
	stdout, _, code = run(t, "--dir", dir, "insert",
		"--type", "note",
		"--data", `{"body":"Some note"}`)
	if code != 0 {
		t.Fatalf("insert note failed with code %d", code)
	}
	noteID := strings.TrimSpace(stdout)
	if noteID == "" {
		t.Fatal("expected auto-generated ID for note")
	}

	// 5. Query all entries
	stdout, _, code = run(t, "--dir", dir, "query")
	if code != 0 {
		t.Fatalf("query failed with code %d", code)
	}
	var entries []map[string]any
	if err := json.Unmarshal([]byte(stdout), &entries); err != nil {
		t.Fatalf("failed to parse query output: %v\n%s", err, stdout)
	}
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}

	// 6. Query with --type filter
	stdout, _, code = run(t, "--dir", dir, "query", "--type", "task")
	if code != 0 {
		t.Fatalf("query --type failed with code %d", code)
	}
	if err := json.Unmarshal([]byte(stdout), &entries); err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 task entries, got %d", len(entries))
	}

	// 7. Query with --limit
	stdout, _, code = run(t, "--dir", dir, "query", "--type", "task", "--limit", "1")
	if code != 0 {
		t.Fatalf("query --limit failed with code %d", code)
	}
	if err := json.Unmarshal([]byte(stdout), &entries); err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry with limit, got %d", len(entries))
	}

	// 8. Query with --filter
	stdout, _, code = run(t, "--dir", dir, "query", "--type", "task", "--filter", "priority=high")
	if code != 0 {
		t.Fatalf("query --filter failed with code %d", code)
	}
	if err := json.Unmarshal([]byte(stdout), &entries); err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 filtered entry, got %d", len(entries))
	}

	// 9. Query with JSONL format
	stdout, _, code = run(t, "--dir", dir, "query", "--type", "task", "--format", "jsonl")
	if code != 0 {
		t.Fatalf("query --format jsonl failed with code %d", code)
	}
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 JSONL lines, got %d", len(lines))
	}

	// 10. Get by ID
	stdout, _, code = run(t, "--dir", dir, "get", "--id", "task-001")
	if code != 0 {
		t.Fatalf("get failed with code %d", code)
	}
	if err := json.Unmarshal([]byte(stdout), &entries); err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry from get, got %d", len(entries))
	}
	if entries[0]["id"] != "task-001" {
		t.Fatalf("expected id task-001, got %v", entries[0]["id"])
	}

	// 11. Get not found
	_, _, code = run(t, "--dir", dir, "get", "--id", "nonexistent")
	if code != 2 {
		t.Fatalf("expected exit code 2 for not found, got %d", code)
	}

	// 12. Export
	exportPath := filepath.Join(t.TempDir(), "export.json")
	stdout, _, code = run(t, "--dir", dir, "export", "--output", exportPath)
	if code != 0 {
		t.Fatalf("export failed with code %d", code)
	}
	if !strings.Contains(stdout, "exported 3 entries") {
		t.Fatalf("unexpected export output: %s", stdout)
	}
	exportData, err := os.ReadFile(exportPath)
	if err != nil {
		t.Fatalf("read export file: %v", err)
	}
	if err := json.Unmarshal(exportData, &entries); err != nil {
		t.Fatalf("parse export file: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("expected 3 exported entries, got %d", len(entries))
	}

	// 13. Export with type filter
	exportPath2 := filepath.Join(t.TempDir(), "export_tasks.json")
	stdout, _, code = run(t, "--dir", dir, "export", "--output", exportPath2, "--type", "note")
	if code != 0 {
		t.Fatalf("export --type failed with code %d", code)
	}
	if !strings.Contains(stdout, "exported 1 entries") {
		t.Fatalf("unexpected export output: %s", stdout)
	}
}

func TestCreateTypeAndListTypes(t *testing.T) {
	dir := t.TempDir()

	// Init
	_, _, code := run(t, "--dir", dir, "init")
	if code != 0 {
		t.Fatalf("init failed: %d", code)
	}

	// Create a type
	stdout, _, code := run(t, "--dir", dir, "create-type", "task",
		"--description", "A work item to be completed",
		"--example", `{"title":"Do something","priority":"high"}`)
	if code != 0 {
		t.Fatalf("create-type failed: %d", code)
	}
	if strings.TrimSpace(stdout) != "task" {
		t.Fatalf("expected 'task', got %q", stdout)
	}

	// Create another type
	stdout, _, code = run(t, "--dir", dir, "create-type", "note",
		"--description", "A freeform note")
	if code != 0 {
		t.Fatalf("create-type note failed: %d", code)
	}

	// Insert an entry with an unregistered type
	_, _, code = run(t, "--dir", dir, "insert",
		"--type", "event",
		"--data", `{"name":"deploy"}`)
	if code != 0 {
		t.Fatalf("insert event failed: %d", code)
	}

	// List types — should show registered types + discovered types
	stdout, _, code = run(t, "--dir", dir, "list-types")
	if code != 0 {
		t.Fatalf("list-types failed: %d", code)
	}
	var types []map[string]any
	if err := json.Unmarshal([]byte(stdout), &types); err != nil {
		t.Fatalf("parse list-types: %v\n%s", err, stdout)
	}
	// Should have at least task, note (registered), and event (discovered)
	names := make(map[string]bool)
	for _, ti := range types {
		name, _ := ti["name"].(string)
		names[name] = true
	}
	for _, expected := range []string{"task", "note", "event"} {
		if !names[expected] {
			t.Fatalf("expected type %q in list-types output, got: %v", expected, names)
		}
	}

	// Check that registered types have registered=true
	for _, ti := range types {
		name, _ := ti["name"].(string)
		registered, _ := ti["registered"].(bool)
		if name == "task" || name == "note" {
			if !registered {
				t.Fatalf("expected type %q to be registered", name)
			}
		}
		if name == "event" {
			if registered {
				t.Fatalf("expected type %q to NOT be registered", name)
			}
		}
	}
}

func TestCompactReducesEntries(t *testing.T) {
	dir := t.TempDir()

	// Init
	_, _, code := run(t, "--dir", dir, "init")
	if code != 0 {
		t.Fatalf("init failed: %d", code)
	}

	// Insert same ID twice (two versions)
	_, _, code = run(t, "--dir", dir, "insert",
		"--type", "task", "--data", `{"version":1}`, "--id", "dup-001")
	if code != 0 {
		t.Fatalf("insert v1 failed: %d", code)
	}
	_, _, code = run(t, "--dir", dir, "insert",
		"--type", "task", "--data", `{"version":2}`, "--id", "dup-001")
	if code != 0 {
		t.Fatalf("insert v2 failed: %d", code)
	}

	// Insert another unique entry
	_, _, code = run(t, "--dir", dir, "insert",
		"--type", "note", "--data", `{"body":"unique"}`, "--id", "note-001")
	if code != 0 {
		t.Fatalf("insert note failed: %d", code)
	}

	// Verify 3 entries before compact
	stdout, _, code := run(t, "--dir", dir, "query")
	if code != 0 {
		t.Fatalf("query failed: %d", code)
	}
	var entries []map[string]any
	if err := json.Unmarshal([]byte(stdout), &entries); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries before compact, got %d", len(entries))
	}

	// Verify history shows both versions
	stdout, _, code = run(t, "--dir", dir, "get", "--id", "dup-001", "--history")
	if code != 0 {
		t.Fatalf("get --history failed: %d", code)
	}
	if err := json.Unmarshal([]byte(stdout), &entries); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 history entries, got %d", len(entries))
	}

	// Compact
	stdout, _, code = run(t, "--dir", dir, "compact")
	if code != 0 {
		t.Fatalf("compact failed: %d", code)
	}
	if !strings.Contains(stdout, "3 entries -> 2 entries") {
		// could also be "3 entries" or similar
		if !strings.Contains(stdout, "Compaction complete") {
			t.Fatalf("unexpected compact output: %s", stdout)
		}
	}

	// Verify 2 entries after compact
	stdout, _, code = run(t, "--dir", dir, "query")
	if code != 0 {
		t.Fatalf("query after compact failed: %d", code)
	}
	if err := json.Unmarshal([]byte(stdout), &entries); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries after compact, got %d", len(entries))
	}

	// The remaining dup-001 should have version 2
	stdout, _, code = run(t, "--dir", dir, "get", "--id", "dup-001")
	if code != 0 {
		t.Fatalf("get after compact failed: %d", code)
	}
	if err := json.Unmarshal([]byte(stdout), &entries); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	data, _ := entries[0]["data"].(map[string]any)
	if v, _ := data["version"].(float64); v != 2 {
		t.Fatalf("expected version 2 after compact, got %v", v)
	}
}

func TestErrorCases(t *testing.T) {
	t.Run("uninitialized directory", func(t *testing.T) {
		dir := t.TempDir()
		uninitDir := filepath.Join(dir, "nope")

		// Query on uninitialized dir
		_, _, code := run(t, "--dir", uninitDir, "query")
		if code != 2 {
			t.Fatalf("expected exit 2 for uninitialized dir, got %d", code)
		}

		// Insert on uninitialized dir
		_, _, code = run(t, "--dir", uninitDir, "insert",
			"--type", "t", "--data", `{"k":"v"}`)
		if code != 2 {
			t.Fatalf("expected exit 2, got %d", code)
		}

		// Get on uninitialized dir
		_, _, code = run(t, "--dir", uninitDir, "get", "--id", "x")
		if code != 2 {
			t.Fatalf("expected exit 2, got %d", code)
		}

		// Export on uninitialized dir
		_, _, code = run(t, "--dir", uninitDir, "export", "--output", "/dev/null")
		if code != 2 {
			t.Fatalf("expected exit 2, got %d", code)
		}

		// list-types on uninitialized dir
		_, _, code = run(t, "--dir", uninitDir, "list-types")
		if code != 2 {
			t.Fatalf("expected exit 2, got %d", code)
		}

		// create-type on uninitialized dir
		_, _, code = run(t, "--dir", uninitDir, "create-type", "foo")
		if code != 2 {
			t.Fatalf("expected exit 2, got %d", code)
		}

		// Compact on uninitialized dir
		_, _, code = run(t, "--dir", uninitDir, "compact")
		if code != 2 {
			t.Fatalf("expected exit 2 for compact on uninitialized dir, got %d", code)
		}

		// Reindex on uninitialized dir
		_, _, code = run(t, "--dir", uninitDir, "reindex")
		if code != 2 {
			t.Fatalf("expected exit 2 for reindex on uninitialized dir, got %d", code)
		}
	})

	t.Run("init lock failure exit code 3", func(t *testing.T) {
		dir := t.TempDir()

		// Create the data directory and make .synapse.lock a directory
		// so that flock acquisition fails instantly (can't open dir for writing).
		dataDir := filepath.Join(dir, "lockfail")
		if err := os.MkdirAll(filepath.Join(dataDir, ".synapse.lock"), 0755); err != nil {
			t.Fatal(err)
		}

		// Init should fail with exit code 3 (lock failure)
		_, stderr, code := run(t, "--dir", dataDir, "init", "--force")
		if code != 3 {
			t.Fatalf("expected exit 3 for lock failure on init, got %d: %s", code, stderr)
		}
	})

	t.Run("corrupted CBOR file exit code 4", func(t *testing.T) {
		dir := t.TempDir()

		// Init normally
		_, _, code := run(t, "--dir", dir, "init")
		if code != 0 {
			t.Fatalf("init failed: %d", code)
		}

		// Corrupt the events.cbor file (write invalid bytes)
		eventsPath := filepath.Join(dir, "events.cbor")
		if err := os.WriteFile(eventsPath, []byte{0x00, 0x00}, 0644); err != nil {
			t.Fatalf("write corrupt file: %v", err)
		}

		// Query on corrupted file should exit 4
		_, stderr, code := run(t, "--dir", dir, "query")
		if code != 4 {
			t.Fatalf("expected exit 4 for corrupted CBOR on query, got %d: %s", code, stderr)
		}

		// Get on corrupted file should exit 4
		_, stderr, code = run(t, "--dir", dir, "get", "--id", "x")
		if code != 4 {
			t.Fatalf("expected exit 4 for corrupted CBOR on get, got %d: %s", code, stderr)
		}

		// Compact on corrupted file should exit 4
		_, stderr, code = run(t, "--dir", dir, "compact")
		if code != 4 {
			t.Fatalf("expected exit 4 for corrupted CBOR on compact, got %d: %s", code, stderr)
		}

		// list-types on corrupted file should exit 4
		_, stderr, code = run(t, "--dir", dir, "list-types")
		if code != 4 {
			t.Fatalf("expected exit 4 for corrupted CBOR on list-types, got %d: %s", code, stderr)
		}

		// Export on corrupted file should exit 4
		_, stderr, code = run(t, "--dir", dir, "export", "--output", filepath.Join(t.TempDir(), "out.json"))
		if code != 4 {
			t.Fatalf("expected exit 4 for corrupted CBOR on export, got %d: %s", code, stderr)
		}
	})

	t.Run("missing required flags", func(t *testing.T) {
		dir := t.TempDir()
		run(t, "--dir", dir, "init")

		// Insert without --type
		_, _, code := run(t, "--dir", dir, "insert", "--data", `{"k":"v"}`)
		if code == 0 {
			t.Fatal("expected non-zero exit for missing --type")
		}

		// Insert without --data
		_, _, code = run(t, "--dir", dir, "insert", "--type", "t")
		if code == 0 {
			t.Fatal("expected non-zero exit for missing --data")
		}

		// Get without --id
		_, _, code = run(t, "--dir", dir, "get")
		if code == 0 {
			t.Fatal("expected non-zero exit for missing --id")
		}

		// Export without --output
		_, _, code = run(t, "--dir", dir, "export")
		if code == 0 {
			t.Fatal("expected non-zero exit for missing --output")
		}

		// create-type without name arg
		_, _, code = run(t, "--dir", dir, "create-type")
		if code == 0 {
			t.Fatal("expected non-zero exit for missing type name")
		}
	})

	t.Run("invalid JSON", func(t *testing.T) {
		dir := t.TempDir()
		run(t, "--dir", dir, "init")

		// Invalid --data JSON
		_, stderr, code := run(t, "--dir", dir, "insert",
			"--type", "t", "--data", "not-json")
		if code != 1 {
			t.Fatalf("expected exit 1 for invalid JSON, got %d: %s", code, stderr)
		}

		// Invalid --metadata JSON
		_, stderr, code = run(t, "--dir", dir, "insert",
			"--type", "t", "--data", `{"k":"v"}`, "--metadata", "bad")
		if code != 1 {
			t.Fatalf("expected exit 1 for invalid metadata JSON, got %d: %s", code, stderr)
		}
	})

	t.Run("invalid format flag", func(t *testing.T) {
		dir := t.TempDir()
		run(t, "--dir", dir, "init")

		_, _, code := run(t, "--dir", dir, "query", "--format", "xml")
		if code != 1 {
			t.Fatalf("expected exit 1 for invalid format, got %d", code)
		}

		_, _, code = run(t, "--dir", dir, "get", "--id", "x", "--format", "csv")
		if code != 1 {
			t.Fatalf("expected exit 1 for invalid format, got %d", code)
		}
	})

	t.Run("insert empty id rejected", func(t *testing.T) {
		dir := t.TempDir()
		run(t, "--dir", dir, "init")

		// Spec: --id must be a non-empty string when explicitly provided.
		_, stderr, code := run(t, "--dir", dir, "insert",
			"--type", "t", "--data", `{"k":"v"}`, "--id", "")
		if code != 1 {
			t.Fatalf("expected exit 1 for empty --id, got %d: %s", code, stderr)
		}
		if !strings.Contains(stderr, "non-empty") {
			t.Fatalf("expected non-empty error message, got: %s", stderr)
		}
	})

	t.Run("already initialized exit code 1", func(t *testing.T) {
		dir := t.TempDir()

		// First init succeeds
		_, _, code := run(t, "--dir", dir, "init")
		if code != 0 {
			t.Fatalf("expected exit 0 for first init, got %d", code)
		}

		// Second init without --force should exit 1 (general error),
		// NOT exit 2 which is reserved for "data/file not found".
		_, stderr, code := run(t, "--dir", dir, "init")
		if code != 1 {
			t.Fatalf("expected exit 1 for already initialized, got %d: %s", code, stderr)
		}
	})
}

func TestInsertWithMetadata(t *testing.T) {
	dir := t.TempDir()
	run(t, "--dir", dir, "init")

	// Insert with metadata
	stdout, _, code := run(t, "--dir", dir, "insert",
		"--type", "task",
		"--data", `{"title":"test"}`,
		"--metadata", `{"model":"gpt-4","confidence":0.95}`,
		"--agent-id", "agent-X",
		"--id", "meta-001")
	if code != 0 {
		t.Fatalf("insert with metadata failed: %d", code)
	}
	if strings.TrimSpace(stdout) != "meta-001" {
		t.Fatalf("unexpected id: %s", stdout)
	}

	// Get and verify metadata is present
	stdout, _, code = run(t, "--dir", dir, "get", "--id", "meta-001")
	if code != 0 {
		t.Fatalf("get failed: %d", code)
	}
	var entries []map[string]any
	if err := json.Unmarshal([]byte(stdout), &entries); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	entry := entries[0]
	if entry["agent_id"] != "agent-X" {
		t.Fatalf("expected agent_id 'agent-X', got %v", entry["agent_id"])
	}
	meta, ok := entry["agent_metadata"].(map[string]any)
	if !ok {
		t.Fatalf("expected agent_metadata map, got %T", entry["agent_metadata"])
	}
	if meta["model"] != "gpt-4" {
		t.Fatalf("expected model 'gpt-4', got %v", meta["model"])
	}
}

func TestAutoGeneratedID(t *testing.T) {
	dir := t.TempDir()
	run(t, "--dir", dir, "init")

	// Insert without --id should auto-generate UUID
	stdout, _, code := run(t, "--dir", dir, "insert",
		"--type", "note",
		"--data", `{"body":"auto id"}`)
	if code != 0 {
		t.Fatalf("insert failed: %d", code)
	}
	id := strings.TrimSpace(stdout)
	if id == "" {
		t.Fatal("expected auto-generated ID")
	}
	// UUID v4 format: 8-4-4-4-12 hex chars
	if len(id) != 36 {
		t.Fatalf("expected UUID v4 (36 chars), got %q (%d chars)", id, len(id))
	}

	// Verify we can get it back
	_, _, code = run(t, "--dir", dir, "get", "--id", id)
	if code != 0 {
		t.Fatalf("get auto-id failed: %d", code)
	}
}

func TestDateFilters(t *testing.T) {
	dir := t.TempDir()
	run(t, "--dir", dir, "init")

	// Insert entries — timestamps are auto-set to now (Unix seconds).
	_, _, code := run(t, "--dir", dir, "insert",
		"--type", "event", "--data", `{"name":"deploy-1"}`, "--id", "ev-001")
	if code != 0 {
		t.Fatalf("insert ev-001 failed: %d", code)
	}
	_, _, code = run(t, "--dir", dir, "insert",
		"--type", "event", "--data", `{"name":"deploy-2"}`, "--id", "ev-002")
	if code != 0 {
		t.Fatalf("insert ev-002 failed: %d", code)
	}

	// Query with since: a date far in the past — should match all
	stdout, _, code := run(t, "--dir", dir, "query", "--type", "event",
		"--filter", "since:2020-01-01")
	if code != 0 {
		t.Fatalf("query since past failed: %d", code)
	}
	var entries []map[string]any
	if err := json.Unmarshal([]byte(stdout), &entries); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries with since:2020-01-01, got %d", len(entries))
	}

	// Query with since: a date far in the future — should match none
	stdout, _, code = run(t, "--dir", dir, "query", "--type", "event",
		"--filter", "since:2099-01-01")
	if code != 0 {
		t.Fatalf("query since future failed: %d", code)
	}
	if err := json.Unmarshal([]byte(stdout), &entries); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected 0 entries with since:2099-01-01, got %d", len(entries))
	}

	// Query with until: a date far in the past — should match none
	stdout, _, code = run(t, "--dir", dir, "query", "--type", "event",
		"--filter", "until:2020-01-01")
	if code != 0 {
		t.Fatalf("query until past failed: %d", code)
	}
	if err := json.Unmarshal([]byte(stdout), &entries); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected 0 entries with until:2020-01-01, got %d", len(entries))
	}

	// Query with until: a date far in the future — should match all
	stdout, _, code = run(t, "--dir", dir, "query", "--type", "event",
		"--filter", "until:2099-12-31")
	if code != 0 {
		t.Fatalf("query until future failed: %d", code)
	}
	if err := json.Unmarshal([]byte(stdout), &entries); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries with until:2099-12-31, got %d", len(entries))
	}

	// Combined since + until — bracket around now
	stdout, _, code = run(t, "--dir", dir, "query", "--type", "event",
		"--filter", "since:2020-01-01 until:2099-12-31")
	if code != 0 {
		t.Fatalf("query since+until failed: %d", code)
	}
	if err := json.Unmarshal([]byte(stdout), &entries); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries with since+until bracket, got %d", len(entries))
	}

	// Combined since + until with key=value filter
	stdout, _, code = run(t, "--dir", dir, "query",
		"--filter", "since:2020-01-01 name=deploy-1")
	if code != 0 {
		t.Fatalf("query since+key=value failed: %d", code)
	}
	if err := json.Unmarshal([]byte(stdout), &entries); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry with since+name=deploy-1, got %d", len(entries))
	}

	// Invalid filter should exit 1
	_, _, code = run(t, "--dir", dir, "query", "--filter", "badtoken")
	if code != 1 {
		t.Fatalf("expected exit 1 for invalid filter, got %d", code)
	}

	// Invalid since date should exit 1
	_, _, code = run(t, "--dir", dir, "query", "--filter", "since:notadate")
	if code != 1 {
		t.Fatalf("expected exit 1 for invalid since date, got %d", code)
	}
}

func TestInsertCorruptedFileExitCode4(t *testing.T) {
	dir := t.TempDir()

	// Init normally
	_, _, code := run(t, "--dir", dir, "init")
	if code != 0 {
		t.Fatalf("init failed: %d", code)
	}

	// Corrupt the events.cbor file
	eventsPath := filepath.Join(dir, "events.cbor")
	if err := os.WriteFile(eventsPath, []byte{0x00, 0x00}, 0644); err != nil {
		t.Fatalf("write corrupt file: %v", err)
	}

	// Insert on corrupted file should exit 4
	_, stderr, code := run(t, "--dir", dir, "insert",
		"--type", "task", "--data", `{"title":"test"}`)
	if code != 4 {
		t.Fatalf("expected exit 4 for corrupted CBOR on insert, got %d: %s", code, stderr)
	}
}

func TestGetJSONLFormat(t *testing.T) {
	dir := t.TempDir()
	run(t, "--dir", dir, "init")

	// Insert two versions of the same ID
	_, _, code := run(t, "--dir", dir, "insert",
		"--type", "task", "--data", `{"v":1}`, "--id", "fmt-001")
	if code != 0 {
		t.Fatalf("insert v1 failed: %d", code)
	}
	_, _, code = run(t, "--dir", dir, "insert",
		"--type", "task", "--data", `{"v":2}`, "--id", "fmt-001")
	if code != 0 {
		t.Fatalf("insert v2 failed: %d", code)
	}

	// get --format jsonl (latest only = 1 line)
	stdout, _, code := run(t, "--dir", dir, "get", "--id", "fmt-001", "--format", "jsonl")
	if code != 0 {
		t.Fatalf("get jsonl failed: %d", code)
	}
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected 1 JSONL line for latest, got %d", len(lines))
	}
	var entry map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &entry); err != nil {
		t.Fatalf("parse JSONL line: %v", err)
	}
	if entry["id"] != "fmt-001" {
		t.Fatalf("expected id fmt-001, got %v", entry["id"])
	}

	// get --history --format jsonl (2 lines)
	stdout, _, code = run(t, "--dir", dir, "get", "--id", "fmt-001", "--history", "--format", "jsonl")
	if code != 0 {
		t.Fatalf("get history jsonl failed: %d", code)
	}
	lines = strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 JSONL lines for history, got %d", len(lines))
	}
	// Verify ascending timestamp order
	var entries []map[string]any
	for _, line := range lines {
		var e map[string]any
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("parse JSONL line: %v", err)
		}
		entries = append(entries, e)
	}
	ts0, _ := entries[0]["timestamp"].(float64)
	ts1, _ := entries[1]["timestamp"].(float64)
	if ts0 > ts1 {
		t.Fatalf("expected ascending timestamp order in history, got %v > %v", ts0, ts1)
	}
}

func TestListTypesJSONLFormat(t *testing.T) {
	dir := t.TempDir()
	run(t, "--dir", dir, "init")

	// Create two types
	_, _, code := run(t, "--dir", dir, "create-type", "task", "--description", "A task")
	if code != 0 {
		t.Fatalf("create-type task failed: %d", code)
	}
	_, _, code = run(t, "--dir", dir, "create-type", "note", "--description", "A note")
	if code != 0 {
		t.Fatalf("create-type note failed: %d", code)
	}

	// list-types --format jsonl
	stdout, _, code := run(t, "--dir", dir, "list-types", "--format", "jsonl")
	if code != 0 {
		t.Fatalf("list-types jsonl failed: %d", code)
	}
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 JSONL lines, got %d: %s", len(lines), stdout)
	}
	// Each line should be valid JSON
	for _, line := range lines {
		var ti map[string]any
		if err := json.Unmarshal([]byte(line), &ti); err != nil {
			t.Fatalf("parse JSONL type line: %v\nline: %s", err, line)
		}
		name, _ := ti["name"].(string)
		if name != "note" && name != "task" {
			t.Fatalf("unexpected type name: %s", name)
		}
	}

	// invalid --format should exit 1
	_, _, code = run(t, "--dir", dir, "list-types", "--format", "yaml")
	if code != 1 {
		t.Fatalf("expected exit 1 for invalid list-types format, got %d", code)
	}
}

func TestInsertMaxDataSize(t *testing.T) {
	dir := t.TempDir()
	run(t, "--dir", dir, "init")

	// Small data (under 256 bytes) should succeed with --max-data-size 256.
	smallData := `{"key":"value"}`
	stdout, stderr, code := run(t, "--dir", dir, "insert", "--type", "task", "--data", smallData, "--max-data-size", "256")
	if code != 0 {
		t.Fatalf("expected success for small data with --max-data-size 256, got code %d\nstderr: %s", code, stderr)
	}
	if strings.TrimSpace(stdout) == "" {
		t.Fatal("expected ID printed on success")
	}

	// Large data (over 256 bytes) should be rejected with exit code 1.
	largeValue := strings.Repeat("x", 300)
	largeData := `{"blob":"` + largeValue + `"}`
	_, stderr, code = run(t, "--dir", dir, "insert", "--type", "task", "--data", largeData, "--max-data-size", "256")
	if code != 1 {
		t.Fatalf("expected exit 1 for oversized data with --max-data-size 256, got code %d", code)
	}
	if !strings.Contains(stderr, "exceeds --max-data-size") {
		t.Fatalf("expected 'exceeds --max-data-size' in stderr, got: %s", stderr)
	}

	// Without --max-data-size the oversized insert should succeed.
	_, _, code = run(t, "--dir", dir, "insert", "--type", "task", "--data", largeData)
	if code != 0 {
		t.Fatalf("expected success without --max-data-size flag, got code %d", code)
	}
}

func TestEnvVarDir(t *testing.T) {
	dir := t.TempDir()

	// Use env var instead of --dir flag
	cmd := exec.Command(testBin, "init")
	cmd.Env = append(os.Environ(), "SYNAPSE_DIR="+dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("init via env var failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "Initialized") {
		t.Fatalf("unexpected output: %s", out)
	}

	// Verify files were created in the env var dir
	if _, err := os.Stat(filepath.Join(dir, "events.cbor")); err != nil {
		t.Fatalf("events.cbor not created: %v", err)
	}
}

// TestQueryMultiTypeCLI verifies that repeated --type/--filter flags trigger
// multi-query (batch) mode, returning grouped results indexed by position.
func TestQueryMultiTypeCLI(t *testing.T) {
	dir := t.TempDir()
	run(t, "--dir", dir, "init")

	// Insert entries of two different types.
	run(t, "--dir", dir, "insert", "--type", "bug", "--data", `{"severity":"critical","title":"crash"}`)
	run(t, "--dir", dir, "insert", "--type", "bug", "--data", `{"severity":"low","title":"typo"}`)
	run(t, "--dir", dir, "insert", "--type", "note", "--data", `{"content":"reminder"}`)

	// Query with two --type flags — should return batch results.
	stdout, stderr, code := run(t, "--dir", dir, "query",
		"--type", "bug",
		"--type", "note",
	)
	if code != 0 {
		t.Fatalf("multi-type query failed with code %d: %s", code, stderr)
	}

	// Output should be a JSON array of indexed results.
	var results []struct {
		Index   int              `json:"index"`
		Entries []map[string]any `json:"entries"`
	}
	if err := json.Unmarshal([]byte(stdout), &results); err != nil {
		t.Fatalf("failed to parse multi-query output: %v\n%s", err, stdout)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 result groups, got %d", len(results))
	}
	if results[0].Index != 0 || len(results[0].Entries) != 2 {
		t.Fatalf("expected group 0 to have 2 bug entries, got %d", len(results[0].Entries))
	}
	if results[1].Index != 1 || len(results[1].Entries) != 1 {
		t.Fatalf("expected group 1 to have 1 note entry, got %d", len(results[1].Entries))
	}
}

// TestQueryMultiTypeWithFilter verifies that --type/--filter pairs are applied
// correctly in multi-query mode.
func TestQueryMultiTypeWithFilter(t *testing.T) {
	dir := t.TempDir()
	run(t, "--dir", dir, "init")

	run(t, "--dir", dir, "insert", "--type", "bug", "--data", `{"severity":"critical"}`)
	run(t, "--dir", dir, "insert", "--type", "bug", "--data", `{"severity":"low"}`)
	run(t, "--dir", dir, "insert", "--type", "task", "--data", `{"status":"open"}`)
	run(t, "--dir", dir, "insert", "--type", "task", "--data", `{"status":"done"}`)

	stdout, stderr, code := run(t, "--dir", dir, "query",
		"--type", "bug", "--filter", "severity=critical",
		"--type", "task", "--filter", "status=open",
	)
	if code != 0 {
		t.Fatalf("multi-type+filter query failed with code %d: %s", code, stderr)
	}

	var results []struct {
		Index   int              `json:"index"`
		Entries []map[string]any `json:"entries"`
	}
	if err := json.Unmarshal([]byte(stdout), &results); err != nil {
		t.Fatalf("failed to parse output: %v\n%s", err, stdout)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(results))
	}
	if len(results[0].Entries) != 1 {
		t.Fatalf("expected 1 critical bug, got %d", len(results[0].Entries))
	}
	if len(results[1].Entries) != 1 {
		t.Fatalf("expected 1 open task, got %d", len(results[1].Entries))
	}
}

// TestQueryMultiTypeJSONL verifies --format jsonl output in multi-query mode.
func TestQueryMultiTypeJSONL(t *testing.T) {
	dir := t.TempDir()
	run(t, "--dir", dir, "init")

	run(t, "--dir", dir, "insert", "--type", "bug", "--data", `{"title":"b1"}`)
	run(t, "--dir", dir, "insert", "--type", "note", "--data", `{"title":"n1"}`)

	stdout, stderr, code := run(t, "--dir", dir, "query",
		"--type", "bug", "--type", "note",
		"--format", "jsonl",
	)
	if code != 0 {
		t.Fatalf("multi-type jsonl query failed with code %d: %s", code, stderr)
	}

	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 JSONL lines, got %d\n%s", len(lines), stdout)
	}
	for i, line := range lines {
		var row struct {
			Index   int              `json:"index"`
			Entries []map[string]any `json:"entries"`
		}
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatalf("failed to parse line %d: %v", i, err)
		}
		if row.Index != i {
			t.Fatalf("expected index %d, got %d", i, row.Index)
		}
		if len(row.Entries) != 1 {
			t.Fatalf("expected 1 entry in group %d, got %d", i, len(row.Entries))
		}
	}
}

// TestInsertStdin_Basic verifies that --stdin reads JSONL from stdin, inserts all
// entries, and prints one ID per line on stdout.
func TestInsertStdin_Basic(t *testing.T) {
	dir := t.TempDir()
	run(t, "--dir", dir, "init")

	jsonl := `{"type":"task","data":{"title":"first"}}
{"type":"note","data":{"body":"second"}}
{"type":"task","data":{"title":"third"}}
`
	cmd := exec.Command(testBin, "--dir", dir, "insert", "--stdin")
	cmd.Stdin = strings.NewReader(jsonl)
	var outBuf, errBuf strings.Builder
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		t.Fatalf("insert --stdin failed: %v\nstderr: %s", err, errBuf.String())
	}

	lines := strings.Split(strings.TrimSpace(outBuf.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 IDs, got %d: %q", len(lines), outBuf.String())
	}
	// Each line should be a non-empty ID.
	for i, id := range lines {
		if strings.TrimSpace(id) == "" {
			t.Fatalf("line %d: empty ID", i)
		}
	}

	// Verify all three entries are queryable.
	stdout, _, code := run(t, "--dir", dir, "query")
	if code != 0 {
		t.Fatalf("query failed: %d", code)
	}
	var entries []map[string]any
	if err := json.Unmarshal([]byte(stdout), &entries); err != nil {
		t.Fatalf("parse query result: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}
}

// TestInsertStdin_OptionalFields verifies that --stdin entries can include
// optional id, agent_id, and metadata fields.
func TestInsertStdin_OptionalFields(t *testing.T) {
	dir := t.TempDir()
	run(t, "--dir", dir, "init")

	jsonl := `{"type":"task","data":{"title":"opt"},"id":"custom-001","agent_id":"agent-Z","metadata":{"confidence":0.9}}
`
	cmd := exec.Command(testBin, "--dir", dir, "insert", "--stdin")
	cmd.Stdin = strings.NewReader(jsonl)
	var outBuf, errBuf strings.Builder
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		t.Fatalf("insert --stdin with optional fields failed: %v\nstderr: %s", err, errBuf.String())
	}
	if strings.TrimSpace(outBuf.String()) != "custom-001" {
		t.Fatalf("expected id 'custom-001', got %q", outBuf.String())
	}

	// Verify agent_id and metadata are stored.
	stdout, _, code := run(t, "--dir", dir, "get", "--id", "custom-001")
	if code != 0 {
		t.Fatalf("get failed: %d", code)
	}
	var result []map[string]any
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if result[0]["agent_id"] != "agent-Z" {
		t.Fatalf("expected agent_id 'agent-Z', got %v", result[0]["agent_id"])
	}
	meta, ok := result[0]["agent_metadata"].(map[string]any)
	if !ok {
		t.Fatalf("expected agent_metadata map, got %T", result[0]["agent_metadata"])
	}
	if meta["confidence"] == nil {
		t.Fatal("expected confidence field in agent_metadata")
	}
}

// TestInsertStdin_EmptyLines verifies that blank lines in JSONL input are skipped.
func TestInsertStdin_EmptyLines(t *testing.T) {
	dir := t.TempDir()
	run(t, "--dir", dir, "init")

	jsonl := `
{"type":"task","data":{"n":1}}

{"type":"task","data":{"n":2}}

`
	cmd := exec.Command(testBin, "--dir", dir, "insert", "--stdin")
	cmd.Stdin = strings.NewReader(jsonl)
	var outBuf, errBuf strings.Builder
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		t.Fatalf("insert --stdin with empty lines failed: %v\nstderr: %s", err, errBuf.String())
	}
	lines := strings.Split(strings.TrimSpace(outBuf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 IDs (empty lines skipped), got %d: %q", len(lines), outBuf.String())
	}
}

// TestInsertStdin_EmptyInput verifies that --stdin with no entries exits with code 1.
func TestInsertStdin_EmptyInput(t *testing.T) {
	dir := t.TempDir()
	run(t, "--dir", dir, "init")

	cmd := exec.Command(testBin, "--dir", dir, "insert", "--stdin")
	cmd.Stdin = strings.NewReader("")
	var errBuf strings.Builder
	cmd.Stderr = &errBuf
	err := cmd.Run()
	if err == nil {
		t.Fatal("expected exit non-zero for empty stdin")
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok || exitErr.ExitCode() != 1 {
		t.Fatalf("expected exit 1 for empty stdin, got: %v", err)
	}
	if !strings.Contains(errBuf.String(), "no entries read from stdin") {
		t.Fatalf("expected 'no entries read from stdin' in stderr, got: %s", errBuf.String())
	}
}

// TestInsertStdin_InvalidJSON verifies that malformed JSONL exits with code 1.
func TestInsertStdin_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	run(t, "--dir", dir, "init")

	cmd := exec.Command(testBin, "--dir", dir, "insert", "--stdin")
	cmd.Stdin = strings.NewReader("not-valid-json\n")
	var errBuf strings.Builder
	cmd.Stderr = &errBuf
	err := cmd.Run()
	if err == nil {
		t.Fatal("expected exit non-zero for invalid JSON")
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok || exitErr.ExitCode() != 1 {
		t.Fatalf("expected exit 1 for invalid JSON, got: %v", err)
	}
	if !strings.Contains(errBuf.String(), "invalid JSON on stdin") {
		t.Fatalf("expected 'invalid JSON on stdin' in stderr, got: %s", errBuf.String())
	}
}

// TestInsertStdin_MissingType verifies that a JSONL line without "type" exits with code 1.
func TestInsertStdin_MissingType(t *testing.T) {
	dir := t.TempDir()
	run(t, "--dir", dir, "init")

	cmd := exec.Command(testBin, "--dir", dir, "insert", "--stdin")
	cmd.Stdin = strings.NewReader(`{"data":{"key":"val"}}` + "\n")
	var errBuf strings.Builder
	cmd.Stderr = &errBuf
	err := cmd.Run()
	if err == nil {
		t.Fatal("expected exit non-zero for missing type")
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok || exitErr.ExitCode() != 1 {
		t.Fatalf("expected exit 1 for missing type, got: %v", err)
	}
	if !strings.Contains(errBuf.String(), `missing "type"`) {
		t.Fatalf("expected 'missing \"type\"' in stderr, got: %s", errBuf.String())
	}
}

// TestInsertStdin_ConflictsWithData verifies that --stdin and --data are mutually exclusive.
func TestInsertStdin_ConflictsWithData(t *testing.T) {
	dir := t.TempDir()
	run(t, "--dir", dir, "init")

	_, stderr, code := run(t, "--dir", dir, "insert",
		"--stdin",
		"--type", "task",
		"--data", `{"key":"val"}`,
	)
	if code != 1 {
		t.Fatalf("expected exit 1 for --stdin + --data, got %d", code)
	}
	if !strings.Contains(stderr, "mutually exclusive") {
		t.Fatalf("expected 'mutually exclusive' in stderr, got: %s", stderr)
	}
}

// TestReindexCLI verifies the reindex command rebuilds index.cbor from events.cbor.
// This is an important recovery path: agents may need to reconstruct a lost or
// stale index without any data loss, and the CLI must surface the entry count so
// operators can confirm the rebuild covered the full ledger.
func TestReindexCLI(t *testing.T) {
	dir := t.TempDir()
	run(t, "--dir", dir, "init")

	// Insert a few entries so the index is non-trivial.
	for i := 0; i < 3; i++ {
		_, _, code := run(t, "--dir", dir, "insert", "--type", "task", "--data", `{"n":1}`)
		if code != 0 {
			t.Fatalf("insert %d failed", i)
		}
	}

	// Remove index.cbor to simulate a missing/stale index.
	indexPath := filepath.Join(dir, "index.cbor")
	if err := os.Remove(indexPath); err != nil && !os.IsNotExist(err) {
		t.Fatalf("remove index: %v", err)
	}

	// Reindex should succeed (exit 0) and report the entry count.
	stdout, stderr, code := run(t, "--dir", dir, "reindex")
	if code != 0 {
		t.Fatalf("reindex failed with code %d\nstderr: %s", code, stderr)
	}
	if !strings.Contains(stdout, "3") {
		t.Fatalf("expected '3' in reindex output, got: %s", stdout)
	}
	if !strings.Contains(stdout, "Reindex complete") {
		t.Fatalf("expected 'Reindex complete' in output, got: %s", stdout)
	}

	// After reindex, get should still resolve entries correctly.
	stdout, _, code = run(t, "--dir", dir, "query", "--type", "task")
	if code != 0 {
		t.Fatalf("query after reindex failed: %d", code)
	}
	var results []map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &results); err != nil {
		t.Fatalf("parse query result: %v\nstdout: %s", err, stdout)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 entries after reindex, got %d", len(results))
	}
}

// TestQueryMultiTypeConflictsWithQueriesFlag verifies that combining
// --type (multi) with --queries is rejected.
func TestQueryMultiTypeConflictsWithQueriesFlag(t *testing.T) {
	dir := t.TempDir()
	run(t, "--dir", dir, "init")

	queriesFile := filepath.Join(dir, "q.json")
	if err := os.WriteFile(queriesFile, []byte(`[{"type":"bug"}]`), 0o644); err != nil {
		t.Fatal(err)
	}

	_, stderr, code := run(t, "--dir", dir, "query",
		"--queries", queriesFile,
		"--type", "bug",
	)
	if code != 1 {
		t.Fatalf("expected exit 1 when combining --queries with --type, got %d", code)
	}
	if !strings.Contains(stderr, "--queries cannot be combined") {
		t.Fatalf("expected conflict error, got: %s", stderr)
	}
}
