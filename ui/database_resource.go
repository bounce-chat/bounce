package ui

/*
import (
	log "github.com/sirupsen/logrus"
)

type databaseResource struct {
	cached []byte
	fileID chat.ID
	getter func(chat.ID) ([]byte, error)
}

func newDatabaseResource(fileID chat.ID, getter func(chat.ID) ([]byte, error)) *databaseResource {
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
