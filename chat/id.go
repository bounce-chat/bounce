package chat

import (
	"crypto/rand"
	"database/sql/driver"
	"encoding/hex"
	"errors"
	"fmt"
)

// An ID identifies a frame, a user, a group or a file.  It is 32 bytes so that it can hold a whole blake3 hash: frames
// whose content never changes once stored take the hash of that content as their ID, which means an ID commits to one
// definition of the thing it names and nobody can hand two peers different content under one ID.  Everything else
// gets a random ID.  Either way an ID is opaque to everyone outside this package, and crosses the UI boundary as a
// lowercase hex string.
type ID [32]byte

const idSize = 32

// NilID is the zero ID, which nothing real ever has
var NilID = ID{}

var errInvalidID = errors.New("invalid ID")

// NewID returns a random ID
func NewID() ID {
	var id ID
	if _, err := rand.Read(id[:]); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return id
}

// IDFromBytes returns the ID held in exactly idSize bytes
func IDFromBytes(data []byte) (ID, error) {
	var id ID
	if len(data) != idSize {
		return NilID, fmt.Errorf("%w: %d bytes", errInvalidID, len(data))
	}
	copy(id[:], data)
	return id, nil
}

// ParseID reads the hex form that String produces
func ParseID(text string) (ID, error) {
	if len(text) != hex.EncodedLen(idSize) {
		return NilID, fmt.Errorf("%w: %q", errInvalidID, text)
	}
	data, err := hex.DecodeString(text)
	if err != nil {
		return NilID, fmt.Errorf("%w: %q", errInvalidID, text)
	}
	return IDFromBytes(data)
}

// MustParseID is ParseID for constants in tests, and panics on anything else
func MustParseID(text string) ID {
	id, err := ParseID(text)
	if err != nil {
		panic(err)
	}
	return id
}

func (id ID) IsNil() bool {
	return id == NilID
}

func (id ID) String() string {
	return hex.EncodeToString(id[:])
}

// MarshalText and UnmarshalText are what encoding/json uses, both for values and for map keys, so an ID crosses the
// Android boundary as a string
func (id ID) MarshalText() ([]byte, error) {
	return []byte(id.String()), nil
}

func (id *ID) UnmarshalText(text []byte) error {
	parsed, err := ParseID(string(text))
	if err != nil {
		return err
	}
	*id = parsed
	return nil
}

// MarshalBinary and UnmarshalBinary are what msgpack uses, so an ID is 32 bytes on the wire rather than an array of
// 32 integers
func (id ID) MarshalBinary() ([]byte, error) {
	return id[:], nil
}

func (id *ID) UnmarshalBinary(data []byte) error {
	parsed, err := IDFromBytes(data)
	if err != nil {
		return err
	}
	*id = parsed
	return nil
}

// Value and Scan store an ID as a 32-byte blob
func (id ID) Value() (driver.Value, error) {
	return id[:], nil
}

func (id *ID) Scan(src interface{}) error {
	switch value := src.(type) {
	case nil:
		*id = NilID
		return nil
	case []byte:
		return id.UnmarshalBinary(value)
	case string:
		return id.UnmarshalText([]byte(value))
	default:
		return fmt.Errorf("%w: cannot scan %T", errInvalidID, src)
	}
}

// GormDataType gives gorm the column type, so no field needs a type tag
func (ID) GormDataType() string {
	return "blob"
}
