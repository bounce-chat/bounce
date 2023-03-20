package chat

import (
	"sort"
	"sync"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"github.com/vmihailenco/msgpack/v5"
)

var catchUpMutex sync.Mutex

type frame struct {
	ID      uuid.UUID
	Type    uint16
	Payload []byte
}

type catchUp struct {
	Frames         []frame
	broadcastables sortableBroadcastables
	payload        []byte
	payloadMutex   sync.Mutex
}

func (cu *catchUp) getType() uint16 {
	return typeCatchUp
}

func (cu *catchUp) getPayload() []byte {
	cu.payloadMutex.Lock()
	defer cu.payloadMutex.Unlock()

	if len(cu.payload) == 0 {
		sort.Sort(cu.broadcastables)
		for _, br := range cu.broadcastables {
			cu.Frames = append(cu.Frames, frame{ID: br.getID(), Type: br.getType(), Payload: br.getPayload()})
		}

		bytes, err := msgpack.Marshal(cu)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("cannot msgpack marshal catch up")
		}
		cu.payload = bytes
	}
	return cu.payload
}

func (cu *catchUp) hasContent() bool {
	return len(cu.broadcastables) > 0
}

func (b *bounce) handleCatchUp(peer string, payload []byte) {
	catchUpMutex.Lock()
	defer catchUpMutex.Unlock()

	var cu catchUp
	err := msgpack.Unmarshal(payload, &cu)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling catch up")
		return
	}

	_, deviceAlreadyExists := b.getDeviceFromAddress(peer)

	b.loadCatchUp(peer, referencesFromFrames(cu.Frames))

	handlers := b.getHandlers()
	for _, fr := range cu.Frames {
		if fr.Type == typeCatchUp {
			log.Warn("refusing to processes recursive catch up")
			return
		}

		handler, ok := handlers[fr.Type]
		if !ok {
			log.WithFields(log.Fields{
				"peer": peer,
				"type": fr.Type,
			}).Warn("peer sent a catch up frame type that doesn't have a handler")
			continue
		}
		handler(peer, fr.Payload)
	}

	if !deviceAlreadyExists {
		if _, deviceNowExists := b.getDeviceFromAddress(peer); deviceNowExists {
			// An unknown device sent a catch up that included frames that prove we should add the device,
			// since we didn't initially offer references to this device we should now do so now that we
			// have context on what this device is
			go b.sendReferences(peer)
		} else {
			log.WithFields(log.Fields{
				"peer":        peer,
				"frame_count": len(cu.Frames),
			}).Warn("catch up from unknown device did not result in learning device identity")
		}
	}

	// TODO: make sure that any groups we learned about contain us
	// SELECT group_id FROM user_groups WHERE user_id != me
	// SELECT FROM groups JOIN group_users WHERE my_id NOT IN (SELECT ID from USERS where group_users)
	//var orphanedGroups []string
	//err := b.database.Table("group_users").
	//	Select("group_id").
	//	Where("user_id != ?", b.currentUserID).
	//	Find(&orphanedGroups).
	//	Error
	//if err != nil {
	//
	//}

}

func referencesFromFrames(frames []frame) []frameReference {
	references := []frameReference{}

	for _, frame := range frames {
		references = append(references, frameReference{FrameID: frame.ID, Type: frame.Type})
	}

	return references
}
