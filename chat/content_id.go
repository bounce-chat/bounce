package chat

import (
	"crypto/rand"
	"database/sql/driver"
	"encoding/binary"
	"fmt"

	log "github.com/sirupsen/logrus"
	"github.com/zeebo/blake3"
)

// In order to prevent a frame from being broadcast with the same ID but different content, frames that do not
// have their content change have their IDs derived from the content of the frame.  This is done by packing the
// relevant data in a consistent way, then hashing it and using that hash as the ID.
//
// Every such frame also carries a nonce, which is part of the content.  It makes two frames with otherwise identical
// content distinct, so sending the same message twice is two messages, and it keeps IDs unguessable, so nobody can
// compute the ID of a frame before its author makes it.  The packer requires the nonce up front and idMatchesContent
// refuses a frame without one, so neither side can leave it out.
type contentAddressable interface {
	getID() ID
	getNonce() nonce
	generateID() ID
}

const contentIDDomain = "bounce-content-id:"

// A nonce is 16 random bytes that make a content addressed frame unique
type nonce [16]byte

var noNonce = nonce{}

func newNonce() nonce {
	var n nonce
	if _, err := rand.Read(n[:]); err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error generating nonce")
	}
	return n
}

// A nonce is 16 bytes of bin on the wire and a 16-byte blob in the database, like an ID
func (n nonce) MarshalBinary() ([]byte, error) {
	return n[:], nil
}

func (n *nonce) UnmarshalBinary(data []byte) error {
	if len(data) != len(n) {
		return fmt.Errorf("invalid nonce: %d bytes", len(data))
	}
	copy(n[:], data)
	return nil
}

func (n nonce) Value() (driver.Value, error) {
	return n[:], nil
}

func (n *nonce) Scan(src interface{}) error {
	switch value := src.(type) {
	case nil:
		*n = noNonce
		return nil
	case []byte:
		return n.UnmarshalBinary(value)
	default:
		return fmt.Errorf("invalid nonce: cannot scan %T", src)
	}
}

func (nonce) GormDataType() string {
	return "blob"
}

// An contentHasher accumulates the preimage of a content ID: the domain, the frame type and the nonce, then each field in
// a fixed order with a length in front of anything variable, so no arrangement of fields can produce another's bytes.
type contentHasher struct {
	hasher *blake3.Hasher
}

func newContentHasher(frameType uint16, n nonce) *contentHasher {
	ch := &contentHasher{hasher: blake3.New()}
	ch.hasher.Write([]byte(contentIDDomain))
	ch.hasher.Write(binary.BigEndian.AppendUint16(nil, frameType))
	ch.hasher.Write(n[:])
	return ch
}

func (ch *contentHasher) bytes(data []byte) *contentHasher {
	ch.hasher.Write(binary.BigEndian.AppendUint32(nil, uint32(len(data))))
	ch.hasher.Write(data)
	return ch
}

func (ch *contentHasher) id(id ID) *contentHasher {
	ch.hasher.Write(id[:])
	return ch
}

func (ch *contentHasher) uint16(value uint16) *contentHasher {
	ch.hasher.Write(binary.BigEndian.AppendUint16(nil, value))
	return ch
}

func (ch *contentHasher) int64(value int64) *contentHasher {
	ch.hasher.Write(binary.BigEndian.AppendUint64(nil, uint64(value)))
	return ch
}

func (ch *contentHasher) generate() ID {
	var id ID
	ch.hasher.Sum(id[:0])
	return id
}

// idMatchesContent reports whether a frame carries a nonce and the ID its content generates
func idMatchesContent(f contentAddressable) bool {
	return f.getNonce() != noNonce && f.getID() != NilID && f.getID() == f.generateID()
}
