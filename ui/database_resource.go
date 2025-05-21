package ui

import (
	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
)

type databaseResource struct {
	fileID uuid.UUID
	getter func(uuid.UUID) ([]byte, error)
}

func newDatabaseResource(fileID uuid.UUID, getter func(uuid.UUID) ([]byte, error)) *databaseResource {
	return &databaseResource{
		fileID: fileID,
		getter: getter,
	}
}

func (resource *databaseResource) Name() string {
	return resource.fileID.String()
}

func (resource *databaseResource) Content() []byte {
	if resource.getter == nil {
		log.Error("database resource does not have file getter defined")
		return []byte{}
	}
	bytes, err := resource.getter(resource.fileID)
	if err != nil {
		return []byte{}
	}
	return bytes
}
