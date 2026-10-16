package chat

import (
	"encoding/binary"
	"errors"

	"github.com/Basekick-Labs/msgpack/v6"
	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"github.com/zeebo/blake3"
)

var errSignedContainerHasInvalidSignature = errors.New("signed container has invalid signature")

type signedContainer struct {
	Signer    string
	Payload   []byte
	Signature []byte
}

const signedContainerDomain = "bounce-signed-container:"

func signedContainerHash(frameType uint16, payload []byte) []byte {
	hasher := blake3.New()
	hasher.Write([]byte(signedContainerDomain))
	hasher.Write(binary.BigEndian.AppendUint16(nil, frameType))
	hasher.Write(payload)
	return hasher.Sum(nil)
}

func (b *Bounce) createSignedContainer(frameType uint16, payload []byte) *signedContainer {
	return &signedContainer{
		Signer:    b.network.Address(),
		Payload:   payload,
		Signature: b.network.Sign(signedContainerHash(frameType, payload)),
	}
}

func (b *Bounce) validSignedContainer(frameType uint16, sc signedContainer) bool {
	return b.network.VerifySignature(sc.Signer, signedContainerHash(frameType, sc.Payload), sc.Signature)
}

func (b *Bounce) signedByUser(sc *signedContainer, userID uuid.UUID) bool {
	signerDevice, exists := b.getDeviceFromAddress(sc.Signer)
	if !exists {
		log.WithFields(log.Fields{
			"signer": sc.Signer,
		}).Warn("cannot validate signed container signed by an unknown device")
		return false
	}
	if signerDevice.UserID != userID {
		log.WithFields(log.Fields{
			"expected": userID,
			"actual":   signerDevice.UserID,
		}).Warn("signed container was signed by unexpected user")
		return false
	}

	return true
}

func (b *Bounce) unpackSignedContainer(frameType uint16, payload []byte) (*signedContainer, error) {
	var sc signedContainer
	err := msgpack.Unmarshal(payload, &sc)
	if err != nil {
		return nil, err
	}

	if !b.validSignedContainer(frameType, sc) {
		return nil, errSignedContainerHasInvalidSignature
	}

	return &sc, nil
}
