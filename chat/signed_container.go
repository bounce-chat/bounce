package chat

import (
	"errors"

	"github.com/vmihailenco/msgpack/v5"
	"github.com/zeebo/blake3"
)

var ERR_SIGNED_CONTAINER_HAS_INVALID_SIGNATURE = errors.New("signed container has invalid signature")

type signedContainer struct {
	Signer    string
	Payload   []byte
	Signature []byte
}

func (b *bounce) createSignedContainer(payload []byte) *signedContainer {
	hash := blake3.Sum256(payload)
	return &signedContainer{
		Signer:    b.network.Address(),
		Payload:   payload,
		Signature: b.network.Sign(hash[:]),
	}
}

func (b *bounce) validSignedContainer(sc signedContainer) bool {
	hash := blake3.Sum256(sc.Payload)
	return b.network.VerifySignature(sc.Signer, hash[:], sc.Signature)
}

func (b *bounce) unpackSignedContainer(payload []byte) (*signedContainer, error) {
	var sc signedContainer
	err := msgpack.Unmarshal(payload, &sc)
	if err != nil {
		return nil, err
	}

	if !b.validSignedContainer(sc) {
		return nil, ERR_SIGNED_CONTAINER_HAS_INVALID_SIGNATURE
	}

	return &sc, nil
}
