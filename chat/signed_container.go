package chat

type signedContainerFields struct {
}

type signedContainer struct {
	Signer    string
	Payload   []byte
	Signature []byte
}
