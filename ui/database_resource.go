package ui

/*
import (
	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
)

type databaseResource struct {
	cached []byte
	fileID uuid.UUID
	getter func(uuid.UUID) ([]byte, error)
}

func newDatabaseResource(fileID uuid.UUID, getter func(uuid.UUID) ([]byte, error)) *databaseResource {
	return &databaseResource{
		cached: []byte{},
		fileID: fileID,
		getter: getter,
	}
}

func (dr *databaseResource) Name() string {
	return dr.fileID.String()
}

func (dr *databaseResource) Content() []byte {
	if len(dr.cached) > 0 {
		return dr.cached
	}
	if dr.getter == nil {
		log.Error("database resource does not have file getter defined")
		return []byte{}
	}
	bytes, err := dr.getter(dr.fileID)
	if err != nil {
		return []byte{}
	}
	dr.cached = bytes
	return bytes
}
*/
