package ledger

import (
	"fmt"
	"io"
	"os"

	"github.com/fxamacker/cbor/v2"

	"github.com/synapse-tool/synapse/internal/model"
)

// CBOR indefinite-length array markers per RFC 8949.
const (
	cborArrayStart byte = 0x9F // indefinite-length array
	cborBreak      byte = 0xFF // break code / array terminator
)

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

// AppendEntry appends a CBOR-encoded entry to an indefinite-length array file.
// It seeks to the last byte, verifies it is the break code (0xFF), overwrites
// it with the encoded entry, writes a new 0xFF, and fsyncs.
func AppendEntry(path string, entry *model.Entry) error {
	encoded, err := cbor.Marshal(entry)
	if err != nil {
		return fmt.Errorf("cbor marshal: %w", err)
	}

	f, err := os.OpenFile(path, os.O_RDWR, 0644)
	if err != nil {
		return fmt.Errorf("open file: %w", err)
	}
	defer f.Close()

	// Get file size.
	info, err := f.Stat()
	if err != nil {
		return fmt.Errorf("stat file: %w", err)
	}
	size := info.Size()
	if size < 2 {
		return fmt.Errorf("file too small (%d bytes); expected at least 2 (0x9F 0xFF)", size)
	}

	// Seek to the last byte and verify it's the break code.
	if _, err := f.Seek(size-1, io.SeekStart); err != nil {
		return fmt.Errorf("seek: %w", err)
	}
	var lastByte [1]byte
	if _, err := io.ReadFull(f, lastByte[:]); err != nil {
		return fmt.Errorf("read last byte: %w", err)
	}
	if lastByte[0] != cborBreak {
		return fmt.Errorf("corrupted file: last byte is 0x%02X, expected 0xFF", lastByte[0])
	}

	// Overwrite the break byte with the encoded entry + new break byte.
	if _, err := f.Seek(size-1, io.SeekStart); err != nil {
		return fmt.Errorf("seek for write: %w", err)
	}
	buf := make([]byte, len(encoded)+1)
	copy(buf, encoded)
	buf[len(encoded)] = cborBreak

	if _, err := f.Write(buf); err != nil {
		return fmt.Errorf("write entry: %w", err)
	}
	return f.Sync()
}

// ReadAllEntries reads all entries from a CBOR indefinite-length array file
// using a streaming CBOR decoder (per storage engine spec: "use a CBOR
// streaming decoder to iterate the indefinite-length array").
func ReadAllEntries(path string) ([]*model.Entry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat file: %w", err)
	}
	if info.Size() < 2 {
		return nil, fmt.Errorf("file too small (%d bytes); expected at least 2", info.Size())
	}

	// Read and verify the first byte is the indefinite-length array marker.
	var header [1]byte
	if _, err := io.ReadFull(f, header[:]); err != nil {
		return nil, fmt.Errorf("corrupted file: cannot read header: %w", err)
	}
	if header[0] != cborArrayStart {
		return nil, fmt.Errorf("corrupted file: first byte is 0x%02X, expected 0x9F", header[0])
	}

	// For a 2-byte file, validate the second byte is the break code.
	if info.Size() == 2 {
		var trailer [1]byte
		if _, err := io.ReadFull(f, trailer[:]); err != nil {
			return nil, fmt.Errorf("corrupted file: cannot read trailer: %w", err)
		}
		if trailer[0] != cborBreak {
			return nil, fmt.Errorf("corrupted file: last byte is 0x%02X, expected 0xFF", trailer[0])
		}
		return nil, nil
	}

	// Seek back to the beginning so the streaming decoder can parse the
	// complete indefinite-length array (0x9F ... entries ... 0xFF).
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("seek: %w", err)
	}

	dec := cbor.NewDecoder(f)
	var entries []*model.Entry
	if err := dec.Decode(&entries); err != nil {
		return nil, fmt.Errorf("decode entry: %w", err)
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
