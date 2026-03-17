package types

import (
	"fmt"
	"os"
	"time"

	"github.com/fxamacker/cbor/v2"
)

// TypeMeta holds metadata for a registered type.
type TypeMeta struct {
	Description string `cbor:"description"`
	Example     string `cbor:"example,omitempty"`
	CreatedAt   int64  `cbor:"created_at"`
}

// InitFile creates a new types.cbor file containing an empty CBOR map.
func InitFile(path string) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("init types file: %w", err)
	}
	defer f.Close()

	// Encode an empty map.
	data, err := cbor.Marshal(map[string]TypeMeta{})
	if err != nil {
		return fmt.Errorf("marshal empty map: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("write types file: %w", err)
	}
	return f.Sync()
}

// ReadTypes reads the types.cbor file and returns the type metadata map.
func ReadTypes(path string) (map[string]TypeMeta, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read types file: %w", err)
	}

	var types map[string]TypeMeta
	if err := cbor.Unmarshal(data, &types); err != nil {
		return nil, fmt.Errorf("decode types file: %w", err)
	}
	if types == nil {
		types = make(map[string]TypeMeta)
	}
	return types, nil
}

// WriteTypes writes the full type metadata map to the types.cbor file.
func WriteTypes(path string, types map[string]TypeMeta) error {
	data, err := cbor.Marshal(types)
	if err != nil {
		return fmt.Errorf("marshal types: %w", err)
	}

	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create types file: %w", err)
	}
	defer f.Close()

	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("write types file: %w", err)
	}
	return f.Sync()
}

// CreateType adds or updates a type in the types.cbor file.
func CreateType(path, name, description, example string) error {
	types, err := ReadTypes(path)
	if err != nil {
		return fmt.Errorf("create type: %w", err)
	}

	types[name] = TypeMeta{
		Description: description,
		Example:     example,
		CreatedAt:   time.Now().Unix(),
	}

	if err := WriteTypes(path, types); err != nil {
		return fmt.Errorf("create type: %w", err)
	}
	return nil
}

// ListTypes returns the type metadata map from the types.cbor file.
func ListTypes(path string) (map[string]TypeMeta, error) {
	return ReadTypes(path)
}
