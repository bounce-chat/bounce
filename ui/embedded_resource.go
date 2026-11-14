package ui

import (
	"embed"

	log "github.com/sirupsen/logrus"
)

//go:embed assets
var assets embed.FS

// Implement Fyne's fyne.Resource, loading from embedded files
type embeddedResource struct {
	path  string
	bytes []byte
}

func newEmbeddedResource(path string) *embeddedResource {
	bytes, err := assets.ReadFile(path)
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
