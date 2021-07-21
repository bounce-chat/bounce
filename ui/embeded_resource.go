package ui

import (
	"embed"

	log "github.com/sirupsen/logrus"
)

//go:embed assets
var assets embed.FS

// Implement Fyne's fyne.Resource, loading from embedded files
type embededResource struct {
	path  string
	bytes []byte
}

func newEmbeddedResource(path string) *embededResource {
	bytes, err := assets.ReadFile(path)
	if err != nil {
		log.WithFields(log.Fields{
			"path": path,
		}).Fatal("unable to locate embedded resource")
	}
	return &embededResource{
		path:  path,
		bytes: bytes,
	}
}

func (resource *embededResource) Name() string {
	return resource.path
}

func (resource *embededResource) Content() []byte {
	return resource.bytes
}
