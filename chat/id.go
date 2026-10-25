package chat

import (
	"crypto/rand"
	"database/sql/driver"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	log "github.com/sirupsen/logrus"
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
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error using rand")
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

// parseIDList reads a comma separated list of IDs, the form the engine stores ID lists in.  Entries that are not
// IDs are left out and reported in the error, which names the first of them, so a caller can log it with its own
// context and go on with the entries that parsed.
func parseIDList(joined string) ([]ID, error) {
	ids := []ID{}
	if joined == "" {
		return ids, nil
	}
	var err error
	for _, idString := range strings.Split(joined, ",") {
		id, parseErr := ParseID(idString)
		if parseErr != nil {
			if err == nil {
				err = fmt.Errorf("list contains %q, which is not an ID", idString)
			}
			continue
		}
		ids = append(ids, id)
	}
	return ids, err
}

// MustParseID is ParseID for constants in tests, and logs a fatal error on anything else
func MustParseID(text string) ID {
	id, err := ParseID(text)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error parsing ID")
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
