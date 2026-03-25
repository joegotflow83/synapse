package ledger

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/fxamacker/cbor/v2"
)

const (
	idIndexFile       = "id_index.bin"
	idIndexMagic      = uint32(0x53594E58) // "SYNX"
	idIndexIDLen      = 64                 // bytes; max ID length = 63 + null terminator
	idIndexRecordSize = idIndexIDLen + 8   // 72 bytes per record
	idIndexHeaderSize = 8                  // magic (4) + count (4)
	MaxIDLength       = idIndexIDLen - 1   // 63 bytes
)

// IndexEntry records the position of an entry in events.cbor, enabling
// O(1) seeks by ID and O(matches) seeks for type-filtered queries.
type IndexEntry struct {
	ID     string `cbor:"id"`
	Type   string `cbor:"type"`
	Offset int64  `cbor:"offset"`
}

// InitIndexFile creates a new empty index.cbor file containing an empty
// indefinite-length CBOR array (0x9F 0xFF).
func InitIndexFile(path string) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("init index file: %w", err)
	}
	defer f.Close()
	if _, err := f.Write([]byte{cborArrayStart, cborBreak}); err != nil {
		return fmt.Errorf("init index file write: %w", err)
	}
	return f.Sync()
}

// AppendIndexEntry appends an IndexEntry to index.cbor using the shared
// appendRaw helper (same pattern as AppendEntry for events.cbor).
func AppendIndexEntry(path string, ie *IndexEntry) error {
	encoded, err := cbor.Marshal(ie)
	if err != nil {
		return fmt.Errorf("cbor marshal index entry: %w", err)
	}
	_, err = appendRaw(path, encoded)
	return err
}

// AppendIndexEntries appends multiple IndexEntry records to index.cbor in a
// single open/write/sync cycle. This is significantly faster than calling
// AppendIndexEntry in a loop.
func AppendIndexEntries(path string, entries []*IndexEntry) error {
	if len(entries) == 0 {
		return nil
	}

	encodedList := make([][]byte, len(entries))
	totalSize := 0
	for i, ie := range entries {
		encoded, err := cbor.Marshal(ie)
		if err != nil {
			return fmt.Errorf("cbor marshal index entry %d: %w", i, err)
		}
		encodedList[i] = encoded
		totalSize += len(encoded)
	}

	f, err := os.OpenFile(path, os.O_RDWR, 0644)
	if err != nil {
		return fmt.Errorf("open index file: %w", err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return fmt.Errorf("stat index file: %w", err)
	}
	size := info.Size()
	if size < 2 {
		return fmt.Errorf("index file too small (%d bytes); expected at least 2", size)
	}

	if _, err := f.Seek(size-1, io.SeekStart); err != nil {
		return fmt.Errorf("seek: %w", err)
	}
	var lastByte [1]byte
	if _, err := io.ReadFull(f, lastByte[:]); err != nil {
		return fmt.Errorf("read last byte: %w", err)
	}
	if lastByte[0] != cborBreak {
		return fmt.Errorf("corrupted index: last byte is 0x%02X, expected 0xFF", lastByte[0])
	}

	buf := make([]byte, 0, totalSize+1)
	for _, encoded := range encodedList {
		buf = append(buf, encoded...)
	}
	buf = append(buf, cborBreak)

	if _, err := f.Seek(size-1, io.SeekStart); err != nil {
		return fmt.Errorf("seek for write: %w", err)
	}
	if _, err := f.Write(buf); err != nil {
		return fmt.Errorf("write index entries: %w", err)
	}
	return f.Sync()
}

// WriteIndex writes a complete index.cbor file from the given IndexEntry slice.
// Used after compaction to replace the index from scratch.
func WriteIndex(path string, entries []*IndexEntry) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create index file: %w", err)
	}
	defer f.Close()

	if _, err := f.Write([]byte{cborArrayStart}); err != nil {
		return fmt.Errorf("write index array start: %w", err)
	}
	for i, ie := range entries {
		encoded, err := cbor.Marshal(ie)
		if err != nil {
			return fmt.Errorf("marshal index entry %d: %w", i, err)
		}
		if _, err := f.Write(encoded); err != nil {
			return fmt.Errorf("write index entry %d: %w", i, err)
		}
	}
	if _, err := f.Write([]byte{cborBreak}); err != nil {
		return fmt.Errorf("write index break: %w", err)
	}
	return f.Sync()
}

// LoadAllOffsets reads all index entries from path and returns every offset in
// insertion order (across all types). Returns nil on missing/corrupt index so
// callers can fall back to a full scan.
func LoadAllOffsets(path string) ([]int64, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, nil
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil || info.Size() < 2 {
		return nil, nil
	}

	var header [1]byte
	if _, err := io.ReadFull(f, header[:]); err != nil || header[0] != cborArrayStart {
		return nil, nil
	}

	dec := cborDecMode.NewDecoder(f)
	var offsets []int64
	for {
		var ie IndexEntry
		if err := dec.Decode(&ie); err != nil {
			if err == io.EOF {
				break
			}
			var syntaxErr *cbor.SyntaxError
			if errors.As(err, &syntaxErr) && strings.Contains(syntaxErr.Error(), "break") {
				break
			}
			return nil, nil
		}
		offsets = append(offsets, ie.Offset)
	}
	return offsets, nil
}

// LoadTypeIndex reads all index entries from path and returns a type→[]offset
// map containing every offset recorded for each type, in insertion order.
// All entries are included (not deduplicated by ID), so results match what a
// sequential full scan would return. Returns an empty map on missing/corrupt
// index so callers can fall back to a full scan without crashing.
func LoadTypeIndex(path string) (map[string][]int64, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return make(map[string][]int64), nil
	}
	if err != nil {
		return make(map[string][]int64), nil
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil || info.Size() < 2 {
		return make(map[string][]int64), nil
	}

	var header [1]byte
	if _, err := io.ReadFull(f, header[:]); err != nil || header[0] != cborArrayStart {
		return make(map[string][]int64), nil
	}

	dec := cborDecMode.NewDecoder(f)
	index := make(map[string][]int64)
	for {
		var ie IndexEntry
		if err := dec.Decode(&ie); err != nil {
			if err == io.EOF {
				break
			}
			var syntaxErr *cbor.SyntaxError
			if errors.As(err, &syntaxErr) && strings.Contains(syntaxErr.Error(), "break") {
				break
			}
			// Corrupt index — return empty map to trigger full-scan fallback.
			return make(map[string][]int64), nil
		}
		index[ie.Type] = append(index[ie.Type], ie.Offset)
	}
	return index, nil
}

// InitIDIndexFile creates an empty id_index.bin file with an 8-byte header
// (magic + count=0).
func InitIDIndexFile(path string) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("init id index file: %w", err)
	}
	defer f.Close()
	var header [idIndexHeaderSize]byte
	binary.BigEndian.PutUint32(header[0:4], idIndexMagic)
	binary.BigEndian.PutUint32(header[4:8], 0)
	if _, err := f.Write(header[:]); err != nil {
		return fmt.Errorf("write id index header: %w", err)
	}
	return f.Sync()
}

// WriteIDIndex writes a binary id_index.bin file from the given IndexEntry
// slice. Entries are sorted by ID lexicographically and written as fixed-size
// 72-byte records (64-byte zero-padded ID + 8-byte big-endian offset).
// Called by Reindex and Compact.
func WriteIDIndex(path string, entries []*IndexEntry) error {
	// Deduplicate by ID (keep the last entry for each ID, which has the
	// latest offset after compaction/reindex).
	idMap := make(map[string]*IndexEntry, len(entries))
	for _, ie := range entries {
		idMap[ie.ID] = ie
	}
	sorted := make([]*IndexEntry, 0, len(idMap))
	for _, ie := range idMap {
		sorted = append(sorted, ie)
	}
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].ID < sorted[j].ID
	})

	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create id index file: %w", err)
	}
	defer f.Close()

	// Write header.
	var header [idIndexHeaderSize]byte
	binary.BigEndian.PutUint32(header[0:4], idIndexMagic)
	binary.BigEndian.PutUint32(header[4:8], uint32(len(sorted)))
	if _, err := f.Write(header[:]); err != nil {
		return fmt.Errorf("write id index header: %w", err)
	}

	// Write records.
	var record [idIndexRecordSize]byte
	for _, ie := range sorted {
		// Zero the record buffer.
		for i := range record {
			record[i] = 0
		}
		copy(record[:idIndexIDLen], ie.ID)
		binary.BigEndian.PutUint64(record[idIndexIDLen:], uint64(ie.Offset))
		if _, err := f.Write(record[:]); err != nil {
			return fmt.Errorf("write id index record: %w", err)
		}
	}
	return f.Sync()
}

// LookupIDIndex performs a binary search in id_index.bin for the given ID.
// Returns the byte offset in events.cbor and found=true on a hit. Returns
// (0, false, nil) when the ID is not found, the file is missing, or the
// file is corrupt. Errors are returned only for unexpected I/O failures.
func LookupIDIndex(path, id string) (int64, bool, error) {
	if len(id) > MaxIDLength {
		return 0, false, nil
	}

	f, err := os.Open(path)
	if err != nil {
		return 0, false, nil // missing file → fallback
	}
	defer f.Close()

	// Read header.
	var header [idIndexHeaderSize]byte
	if _, err := io.ReadFull(f, header[:]); err != nil {
		return 0, false, nil
	}
	magic := binary.BigEndian.Uint32(header[0:4])
	if magic != idIndexMagic {
		return 0, false, nil
	}
	count := int(binary.BigEndian.Uint32(header[4:8]))
	if count == 0 {
		return 0, false, nil
	}

	// Prepare the search key: zero-padded to idIndexIDLen bytes.
	var searchKey [idIndexIDLen]byte
	copy(searchKey[:], id)

	// Binary search.
	var record [idIndexRecordSize]byte
	lo, hi := 0, count-1
	for lo <= hi {
		mid := lo + (hi-lo)/2
		seekPos := int64(idIndexHeaderSize) + int64(mid)*int64(idIndexRecordSize)
		if _, err := f.Seek(seekPos, io.SeekStart); err != nil {
			return 0, false, fmt.Errorf("seek id index: %w", err)
		}
		if _, err := io.ReadFull(f, record[:]); err != nil {
			return 0, false, nil // truncated file → fallback
		}
		cmp := compareBytes(record[:idIndexIDLen], searchKey[:])
		if cmp == 0 {
			offset := int64(binary.BigEndian.Uint64(record[idIndexIDLen:]))
			return offset, true, nil
		}
		if cmp < 0 {
			lo = mid + 1
		} else {
			hi = mid - 1
		}
	}
	return 0, false, nil
}

// compareBytes compares two byte slices lexicographically.
// Returns -1, 0, or +1.
func compareBytes(a, b []byte) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] < b[i] {
			return -1
		}
		if a[i] > b[i] {
			return 1
		}
	}
	if len(a) < len(b) {
		return -1
	}
	if len(a) > len(b) {
		return 1
	}
	return 0
}

// LoadIndex reads all index entries from path and returns an ID→IndexEntry map.
// If the file is missing or corrupt, it returns an empty map (no error) so
// callers can fall back to a full scan without crashing.
func LoadIndex(path string) (map[string]*IndexEntry, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return make(map[string]*IndexEntry), nil
	}
	if err != nil {
		return make(map[string]*IndexEntry), nil
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil || info.Size() < 2 {
		return make(map[string]*IndexEntry), nil
	}

	var header [1]byte
	if _, err := io.ReadFull(f, header[:]); err != nil || header[0] != cborArrayStart {
		return make(map[string]*IndexEntry), nil
	}

	dec := cborDecMode.NewDecoder(f)
	index := make(map[string]*IndexEntry)
	for {
		var ie IndexEntry
		if err := dec.Decode(&ie); err != nil {
			if err == io.EOF {
				break
			}
			var syntaxErr *cbor.SyntaxError
			if errors.As(err, &syntaxErr) && strings.Contains(syntaxErr.Error(), "break") {
				break
			}
			// Corrupt index — return empty map to trigger full-scan fallback.
			return make(map[string]*IndexEntry), nil
		}
		index[ie.ID] = &ie
	}
	return index, nil
}
