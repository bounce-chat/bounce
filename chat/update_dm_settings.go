package chat

import (
	"sync"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"github.com/vmihailenco/msgpack/v5"
	"gorm.io/gorm"
)

type updateDMSettings struct {
	ID           uuid.UUID `gorm:"type:uuid;primary_key;"`
	Xor          uuid.UUID
	Retention    int64
	ClearBefore  int64
	payload      []byte
	payloadMutex sync.Mutex
}

func (uds *updateDMSettings) BeforeCreate(tx *gorm.DB) error {
	if uds.ID == uuid.Nil {
		uds.ID = uuid.New()
	} // TODO: fatal if already set?
	return nil
}

func (uds *updateDMSettings) AfterDelete(tx *gorm.DB) error {
	return tx.Where("frame_id = ? AND frame_type = ?", uds.ID, typeUpdateDMSettings).Delete(&deliveryRecord{}).Error
}

func (uds *updateDMSettings) getID() uuid.UUID {
	return uds.ID
}

func (ulds *updateDMSettings) getScope(_ uuid.UUID) int {
	return scopeUser
}

func (uds *updateDMSettings) getDestination(myID uuid.UUID) uuid.UUID {
	return xor(myID, uds.Xor)
}

func (uds *updateDMSettings) getType() uint16 {
	return typeUpdateDMSettings
}

func (uds *updateDMSettings) getPayload() []byte {
	uds.payloadMutex.Lock()
	defer uds.payloadMutex.Unlock()

	if len(uds.payload) == 0 {
		bytes, err := msgpack.Marshal(uds)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("cannot msgpack marshal update dm settings")
		}
		uds.payload = bytes
	}
	return uds.payload
}

func (b *bounce) setDMRetention(user uuid.UUID, retention int64) {
	// Create a uds using my ID xored with the user as the target
}

func (b *bounce) getDMRetention(user uuid.UUID) (int64, error) {
	return 0, nil
}

func (b *bounce) handleUpdateDMSettings(peer string, payload []byte) {
	// Unmarshall it
	// XOR the target with my ID to get the user in question
	// apply the settings change

}

func xor(uuid1, uuid2 uuid.UUID) uuid.UUID {
	xored := [16]byte{}
	for i, b := range uuid1 {
		xored[i] = b ^ uuid2[i]
	}

	xorUUID, err := uuid.FromBytes(xored[:])
	if err != nil {
		log.WithFields(log.Fields{
			"uuid1": uuid1,
			"uuid2": uuid2,
			"xored": xored,
			"error": err.Error(),
		}).Fatal("unable to create UUID from XORed UUIDs")
	}

	return xorUUID
}
