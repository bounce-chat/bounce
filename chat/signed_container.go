package chat

type signedContainerFields struct {
	Signer    string `msgpack:"-"`
	Payload   []byte `msgpack:"-"`
	Signature []byte `msgpack:"-"`
}

type signedContainer struct {
	Signer    string
	Payload   []byte
	Signature []byte
}
