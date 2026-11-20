package fyneui

import (
	"github.com/bounce-chat/bounce/ui/assets"
	log "github.com/sirupsen/logrus"
)

// Implement Fyne's fyne.Resource, loading from embedded files
type embeddedResource struct {
	path  string
	bytes []byte
}

// newEmbeddedResource loads a file from ui/assets by its path relative to that directory
func newEmbeddedResource(path string) *embeddedResource {
	bytes, err := assets.FS.ReadFile(path)
	if err != nil {
		log.WithFields(log.Fields{
			"path": path,
		}).Fatal("unable to locate embedded resource")
	}
	return &embeddedResource{
		path:  path,
		bytes: bytes,
	}
}

func (resource *embeddedResource) Name() string {
	return resource.path
}

func (resource *embeddedResource) Content() []byte {
	return resource.bytes
}
