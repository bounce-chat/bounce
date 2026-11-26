package platform

import "os"

// localFiles is the file store of a platform where every location is a path.  A file is opened where it sits, and
// when the engine is finished with it there is nothing to give up: the file is the user's and stays.
type localFiles struct{}

func (localFiles) Open(location string) (*os.File, error) {
	return os.Open(location)
}

func (localFiles) Release(location string) error {
	return nil
}
