package chat

type signedContainer struct {
	Signer    string
	Payload   []byte
	Signature []byte
}
