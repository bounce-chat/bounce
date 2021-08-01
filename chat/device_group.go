package chat

type deviceGroup struct {
	signatures []mutualDeviceSignature
}

func (dg deviceGroup) valid() bool {
	return true
}
