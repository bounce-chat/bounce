package chat

import (
	"github.com/zeebo/blake3"
)

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
