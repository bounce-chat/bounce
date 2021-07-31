package chat

type deviceGroup struct {
	assertions []deviceAssertion
}

func (dg deviceGroup) valid() bool {
	return true
}
