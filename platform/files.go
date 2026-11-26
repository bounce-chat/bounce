package platform

import "os"

// A download that has not finished is written beside its destination under this extension
const downloadExtension = ".bouncedownload"

// LocalFiles is the file store of a platform where every location is a path.  A file is opened where it sits, a
// download is written beside its destination until it is complete, and when the engine is finished with a location
// there is nothing to give up: the file is the user's and stays.
type LocalFiles struct{}

func (LocalFiles) Open(location string) (*os.File, error) {
	return os.Open(location)
}

func (LocalFiles) Create(location string) (*os.File, error) {
	return os.OpenFile(location, os.O_RDWR|os.O_CREATE, 0600)
}

func (LocalFiles) Partial(location string) string {
	return location + downloadExtension
}

func (LocalFiles) Rename(from, to string) error {
	return os.Rename(from, to)
}

func (LocalFiles) Remove(location string) error {
	return os.Remove(location)
}

func (LocalFiles) Release(location string) error {
	return nil
}
