package ledger

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/fxamacker/cbor/v2"

	"github.com/synapse-tool/synapse/internal/model"
)

// CBOR indefinite-length array markers per RFC 8949.
const (
	cborArrayStart byte = 0x9F // indefinite-length array
	cborBreak      byte = 0xFF // break code / array terminator
)

// cborDecMode is a custom decode mode that raises the element limit from the
// default 131,072 to 10,000,000. The default cap would brick any ledger that
// accumulates more than ~131K entries (roughly 13 hours at 10K inserts/hour).
// 10M still provides a safety net against runaway corruption while supporting
// years of multi-agent operation on a local filesystem.
var cborDecMode cbor.DecMode

func init() {
	dm, err := cbor.DecOptions{
		MaxArrayElements: 10_000_000,
		MaxMapPairs:      10_000_000,
	}.DecMode()
	if err != nil {
		panic(fmt.Sprintf("cbor: failed to create DecMode: %v", err))
	}
	cborDecMode = dm
}

// InitFile creates a new events.cbor file containing an empty
// indefinite-length CBOR array (0x9F 0xFF).
func InitFile(path string) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("init file: %w", err)
	}
	defer f.Close()

	if _, err := f.Write([]byte{cborArrayStart, cborBreak}); err != nil {
		return fmt.Errorf("init file write: %w", err)
	}
	return f.Sync()
}

// appendRaw opens an indefinite-length CBOR array file, seeks to the trailing
// break byte (0xFF), overwrites it with data followed by a new break byte, and
// fsyncs. Returns the byte offset where data was written (the position of the
// old break byte). This is the shared low-level helper used by AppendEntry,
// AppendIndexEntry, and AppendEntryAndIndexEntry.
func appendRaw(path string, data []byte) (int64, error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0644)
	if err != nil {
		return 0, fmt.Errorf("open file: %w", err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return 0, fmt.Errorf("stat file: %w", err)
	}
	size := info.Size()
	if size < 2 {
		return 0, fmt.Errorf("file too small (%d bytes); expected at least 2 (0x9F 0xFF)", size)
	}

	if _, err := f.Seek(size-1, io.SeekStart); err != nil {
		return 0, fmt.Errorf("seek: %w", err)
	}
	var lastByte [1]byte
	if _, err := io.ReadFull(f, lastByte[:]); err != nil {
		return 0, fmt.Errorf("read last byte: %w", err)
	}
	if lastByte[0] != cborBreak {
		return 0, fmt.Errorf("corrupted file: last byte is 0x%02X, expected 0xFF", lastByte[0])
	}

	offset := size - 1
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return 0, fmt.Errorf("seek for write: %w", err)
	}
	buf := make([]byte, len(data)+1)
	copy(buf, data)
	buf[len(data)] = cborBreak
	if _, err := f.Write(buf); err != nil {
		return 0, fmt.Errorf("write: %w", err)
	}
	return offset, f.Sync()
}

// AppendEntry appends a CBOR-encoded entry to an indefinite-length array file.
// Returns the byte offset within the file where the entry was written.
func AppendEntry(path string, entry *model.Entry) (int64, error) {
	encoded, err := cbor.Marshal(entry)
	if err != nil {
		return 0, fmt.Errorf("cbor marshal: %w", err)
	}
	return appendRaw(path, encoded)
}

// AppendEntryAndIndexEntry encodes an entry and its corresponding index record,
// then appends each to their respective files (events.cbor and index.cbor) in a
// single logical operation. This is faster than calling AppendEntry +
// AppendIndexEntry separately because the entry is encoded once and the function
// eliminates one layer of intermediate abstraction.
func AppendEntryAndIndexEntry(eventsPath, indexPath string, entry *model.Entry) (*IndexEntry, error) {
	encoded, err := cbor.Marshal(entry)
	if err != nil {
		return nil, fmt.Errorf("cbor marshal entry: %w", err)
	}

	offset, err := appendRaw(eventsPath, encoded)
	if err != nil {
		return nil, fmt.Errorf("append entry: %w", err)
	}

	ie := &IndexEntry{ID: entry.ID, Type: entry.Type, Offset: offset}
	encodedIE, err := cbor.Marshal(ie)
	if err != nil {
		return nil, fmt.Errorf("cbor marshal index entry: %w", err)
	}

	if _, err := appendRaw(indexPath, encodedIE); err != nil {
		return nil, fmt.Errorf("append index entry: %w", err)
	}
	return ie, nil
}

// AppendEntries appends multiple CBOR-encoded entries to an indefinite-length
// array file in a single open/write/sync cycle. Returns the byte offset of
// each entry within the file. This is significantly faster than calling
// AppendEntry in a loop because the file is opened, stat'd, and fsync'd only
// once.
func AppendEntries(path string, entries []*model.Entry) ([]int64, error) {
	if len(entries) == 0 {
		return nil, nil
	}

	// Pre-encode all entries.
	encodedList := make([][]byte, len(entries))
	totalSize := 0
	for i, entry := range entries {
		encoded, err := cbor.Marshal(entry)
		if err != nil {
			return nil, fmt.Errorf("cbor marshal entry %d: %w", i, err)
		}
		encodedList[i] = encoded
		totalSize += len(encoded)
	}

	f, err := os.OpenFile(path, os.O_RDWR, 0644)
	if err != nil {
		return nil, fmt.Errorf("open file: %w", err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat file: %w", err)
	}
	size := info.Size()
	if size < 2 {
		return nil, fmt.Errorf("file too small (%d bytes); expected at least 2 (0x9F 0xFF)", size)
	}

	// Verify the break byte.
	if _, err := f.Seek(size-1, io.SeekStart); err != nil {
		return nil, fmt.Errorf("seek: %w", err)
	}
	var lastByte [1]byte
	if _, err := io.ReadFull(f, lastByte[:]); err != nil {
		return nil, fmt.Errorf("read last byte: %w", err)
	}
	if lastByte[0] != cborBreak {
		return nil, fmt.Errorf("corrupted file: last byte is 0x%02X, expected 0xFF", lastByte[0])
	}

	// Build a single buffer: all encoded entries + trailing break byte.
	buf := make([]byte, 0, totalSize+1)
	offsets := make([]int64, len(entries))
	currentOffset := size - 1 // where the old break byte was
	for i, encoded := range encodedList {
		offsets[i] = currentOffset
		buf = append(buf, encoded...)
		currentOffset += int64(len(encoded))
	}
	buf = append(buf, cborBreak)

	// Write everything in one call.
	if _, err := f.Seek(size-1, io.SeekStart); err != nil {
		return nil, fmt.Errorf("seek for write: %w", err)
	}
	if _, err := f.Write(buf); err != nil {
		return nil, fmt.Errorf("write entries: %w", err)
	}
	return offsets, f.Sync()
}

// EntryIter is a streaming CBOR iterator over a ledger file. It decodes one
// entry at a time, enabling early exit without loading the full file into
// memory. The caller must call Close() when done, even after io.EOF.
type EntryIter struct {
	f   *os.File
	dec *cbor.Decoder
}

// NewEntryIter opens path and positions the iterator at the first entry.
// Returns an error if the file is missing, too small, or has a bad header byte.
func NewEntryIter(path string) (*EntryIter, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open file: %w", err)
	}

	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("stat file: %w", err)
	}
	if info.Size() < 2 {
		f.Close()
		return nil, fmt.Errorf("file too small (%d bytes); expected at least 2", info.Size())
	}

	// Read and validate the indefinite-length array start marker directly from
	// the file. We use io.ReadFull so the cbor.Decoder starts exactly at the
	// first entry byte, with no intermediate bufio layer that could desync the
	// decoder's internal read-ahead buffer from our own position tracking.
	var header [1]byte
	if _, err := io.ReadFull(f, header[:]); err != nil {
		f.Close()
		return nil, fmt.Errorf("corrupted file: cannot read header: %w", err)
	}
	if header[0] != cborArrayStart {
		f.Close()
		return nil, fmt.Errorf("corrupted file: first byte is 0x%02X, expected 0x9F", header[0])
	}

	return &EntryIter{
		f:   f,
		dec: cborDecMode.NewDecoder(f),
	}, nil
}

// Next returns the next entry. It returns nil, io.EOF when all entries have
// been read. Any other error indicates file corruption or an I/O failure.
//
// The cbor.Decoder reads directly from the file through its own internal buffer.
// When it encounters the 0xFF break code that terminates the indefinite-length
// array, fxamacker/cbor v2 returns a *cbor.SyntaxError with "break" in the
// message. We convert that specific error to io.EOF so callers see a clean
// end-of-stream signal.
func (it *EntryIter) Next() (*model.Entry, error) {
	var entry model.Entry
	if err := it.dec.Decode(&entry); err != nil {
		if err == io.EOF {
			return nil, io.EOF
		}
		// The break code (0xFF) terminates the indefinite-length array.
		// fxamacker/cbor returns &SyntaxError{"cbor: unexpected \"break\" code"}
		// when 0xFF is encountered outside of an indefinite-length context.
		var syntaxErr *cbor.SyntaxError
		if errors.As(err, &syntaxErr) && strings.Contains(syntaxErr.Error(), "break") {
			return nil, io.EOF
		}
		return nil, fmt.Errorf("decode entry: %w", err)
	}
	return &entry, nil
}

// Close releases the file descriptor held by the iterator.
func (it *EntryIter) Close() error {
	return it.f.Close()
}

// ReadAllEntries reads all entries from a CBOR indefinite-length array file.
// It is a convenience wrapper over NewEntryIter for callers that need the full
// slice rather than streaming one entry at a time.
func ReadAllEntries(path string) ([]*model.Entry, error) {
	it, err := NewEntryIter(path)
	if err != nil {
		return nil, err
	}
	defer it.Close()

	var entries []*model.Entry
	for {
		entry, err := it.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

// ReadEntryAt opens path, seeks to offset, and decodes exactly one entry.
// Used by Get to perform an O(1) lookup when the index is available.
func ReadEntryAt(path string, offset int64) (*model.Entry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open file: %w", err)
	}
	defer f.Close()

	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, fmt.Errorf("seek to offset %d: %w", offset, err)
	}

	var entry model.Entry
	if err := cborDecMode.NewDecoder(f).Decode(&entry); err != nil {
		return nil, fmt.Errorf("decode entry at offset %d: %w", offset, err)
	}
	return &entry, nil
}

// streamWriteSurvivorEntries is the streaming write phase of Compact (pass 2).
// It re-reads srcPath entry-by-entry and writes to dstPath only the entries
// whose stream position matches lastMaxPos[id] — the last occurrence of the
// maximum timestamp for that ID. This keeps peak memory at O(unique IDs × ~40
// bytes) rather than O(total entries × entry_size).
// Returns index entries (ID, Type, offset) for each written entry so Compact
// can rebuild index.cbor without a third pass.
func streamWriteSurvivorEntries(srcPath, dstPath string, lastMaxPos map[string]int) ([]*IndexEntry, error) {
	iter, err := NewEntryIter(srcPath)
	if err != nil {
		return nil, fmt.Errorf("open entry iter: %w", err)
	}
	defer iter.Close()

	f, err := os.Create(dstPath)
	if err != nil {
		return nil, fmt.Errorf("create temp file: %w", err)
	}
	defer f.Close()

	if _, err := f.Write([]byte{cborArrayStart}); err != nil {
		return nil, fmt.Errorf("write array start: %w", err)
	}

	filePos := int64(1) // position after the 0x9F header byte
	indexEntries := make([]*IndexEntry, 0, len(lastMaxPos))
	pos := 0

	for {
		e, err := iter.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read entry: %w", err)
		}
		if pos == lastMaxPos[e.ID] {
			encoded, err := cbor.Marshal(e)
			if err != nil {
				return nil, fmt.Errorf("marshal entry: %w", err)
			}
			indexEntries = append(indexEntries, &IndexEntry{
				ID:     e.ID,
				Type:   e.Type,
				Offset: filePos,
			})
			if _, err := f.Write(encoded); err != nil {
				return nil, fmt.Errorf("write entry: %w", err)
			}
			filePos += int64(len(encoded))
		}
		pos++
	}

	if _, err := f.Write([]byte{cborBreak}); err != nil {
		return nil, fmt.Errorf("write break: %w", err)
	}
	return indexEntries, f.Sync()
}

// readEntriesFrom reads CBOR entries appended to path after snapshotSize bytes.
// At the time the snapshot was taken, the byte at snapshotSize-1 was the break
// code (0xFF). Subsequent inserts overwrote that byte and appended new entries
// followed by a new 0xFF. This function seeks to snapshotSize-1 and decodes
// the raw CBOR items from that point, using the same termination logic as
// EntryIter (0xFF break code → io.EOF). Returns nil, nil when the file has not
// grown beyond snapshotSize (no concurrent inserts occurred).
func readEntriesFrom(path string, snapshotSize int64) ([]*model.Entry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open file: %w", err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat file: %w", err)
	}
	if info.Size() <= snapshotSize {
		return nil, nil
	}

	// Seek to the byte position where the old 0xFF was (snapshotSize-1). The
	// new entries are raw CBOR items starting here, terminated by a new 0xFF.
	// No 0x9F array header is present — we decode items directly.
	if _, err := f.Seek(snapshotSize-1, io.SeekStart); err != nil {
		return nil, fmt.Errorf("seek: %w", err)
	}

	dec := cborDecMode.NewDecoder(f)
	var entries []*model.Entry
	for {
		var e model.Entry
		if err := dec.Decode(&e); err != nil {
			if err == io.EOF {
				break
			}
			var syntaxErr *cbor.SyntaxError
			if errors.As(err, &syntaxErr) && strings.Contains(syntaxErr.Error(), "break") {
				break
			}
			return nil, fmt.Errorf("decode entry: %w", err)
		}
		entries = append(entries, &e)
	}
	return entries, nil
}

// buildIndexFromScan performs a full sequential scan of ep (events.cbor) and
// returns an IndexEntry for every entry, recording each entry's byte offset
// within the file. This is the underlying implementation of Reindex.
func buildIndexFromScan(ep string) ([]*IndexEntry, error) {
	f, err := os.Open(ep)
	if err != nil {
		return nil, fmt.Errorf("open file: %w", err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat: %w", err)
	}
	if info.Size() < 2 {
		return nil, fmt.Errorf("file too small (%d bytes)", info.Size())
	}

	var header [1]byte
	if _, err := io.ReadFull(f, header[:]); err != nil {
		return nil, fmt.Errorf("read header: %w", err)
	}
	if header[0] != cborArrayStart {
		return nil, fmt.Errorf("corrupted file: header byte 0x%02X, expected 0x9F", header[0])
	}

	// Use dec.NumBytesRead() to track the byte offset of each entry within
	// the data section. This correctly accounts for the decoder's internal
	// buffering, unlike a countingReader which reflects bytes buffered rather
	// than bytes consumed.
	dec := cborDecMode.NewDecoder(f)

	var entries []*IndexEntry
	for {
		// Offset of this entry in events.cbor = 1 (header byte) + bytes consumed so far.
		offset := int64(1) + int64(dec.NumBytesRead())
		var e model.Entry
		if err := dec.Decode(&e); err != nil {
			if err == io.EOF {
				break
			}
			var syntaxErr *cbor.SyntaxError
			if errors.As(err, &syntaxErr) && strings.Contains(syntaxErr.Error(), "break") {
				break
			}
			return nil, fmt.Errorf("decode entry at offset %d: %w", offset, err)
		}
		entries = append(entries, &IndexEntry{ID: e.ID, Type: e.Type, Offset: offset})
	}
	return entries, nil
}

// WriteEntries writes a complete CBOR indefinite-length array file from the
// given entries. Used for compaction — replaces the entire file content.
func WriteEntries(path string, entries []*model.Entry) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}
	defer f.Close()

	// Write array start.
	if _, err := f.Write([]byte{cborArrayStart}); err != nil {
		return fmt.Errorf("write array start: %w", err)
	}

	// Write each entry.
	for i, entry := range entries {
		encoded, err := cbor.Marshal(entry)
		if err != nil {
			return fmt.Errorf("marshal entry %d: %w", i, err)
		}
		if _, err := f.Write(encoded); err != nil {
			return fmt.Errorf("write entry %d: %w", i, err)
		}
	}

	// Write break byte.
	if _, err := f.Write([]byte{cborBreak}); err != nil {
		return fmt.Errorf("write break: %w", err)
	}
	return f.Sync()
}
