package chat

import (
	"errors"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type frameSpec struct {
	frameType  uint16
	table      string // Database table name
	dialWorthy bool   // TODO: dialUserFor
	load       func(b *Bounce, ids []uuid.UUID) []broadcastable
	offer      func(b *Bounce, address string, userID uuid.UUID) []frameReference // TODO: need to pass the user iD?
}

// frameSpecs is ordered, and the order is load-bearing in exactly one way: the
// position of a syncable entry becomes its catchUpOrder rank, which is the
// order frames are replayed out of a catch up. groupCreation must precede
// updateGroup, addUser and device must precede anything that references them,
// and so on. Reordering these lines reorders replay.
//
// Nothing else depends on the order. The offer, request and ack paths all
// group by type before doing anything, and a catch up is explicitly sorted by
// sortableCatchUpAbles.Less before it is marshalled.
var frameSpecs = []frameSpec{
	{
		frameType:  typeAddUser,
		table:      "add_users",
		dialWorthy: true,
		load:       loadFrames[addUser](false),
		offer:      (*Bounce).getAddUsersToOffer,
	},
	{
		frameType:  typeDevice,
		table:      "devices",
		dialWorthy: true,
		load:       loadFrames[device](true),
		offer:      (*Bounce).getDevicesToOffer,
	},
	{
		frameType:  typeUpdateDevice,
		table:      "update_devices",
		dialWorthy: true,
		load:       loadFrames[updateDevice](false),
		offer:      (*Bounce).getUpdateDevicesToOffer,
	},
	{
		frameType: typeUpdateUser,
		table:     "update_users",
		load:      loadFrames[updateUser](false),
		offer:     (*Bounce).getUpdateUsersToOffer,
	},
	{
		frameType: typeUpdateDM,
		table:     "update_dms",
		load:      loadFrames[updateDM](false),
		offer:     (*Bounce).getUpdateDMsToOffer,
	},
	{
		frameType:  typeDirectMessage,
		table:      "direct_messages",
		dialWorthy: true,
		load:       loadFrames[directMessage](true),
		offer:      (*Bounce).getDirectMessagesToOffer,
	},
	{
		frameType:  typeGroupCreation,
		table:      "group_creations",
		dialWorthy: true,
		load:       loadFrames[groupCreation](false),
		offer:      (*Bounce).getGroupCreationsToOffer,
	},
	{
		frameType:  typeUpdateGroup,
		table:      "update_groups",
		dialWorthy: true,
		load:       loadFrames[updateGroup](false),
		offer:      (*Bounce).getUpdateGroupsToOffer,
	},
	{
		frameType:  typeGroupMessage,
		table:      "group_messages",
		dialWorthy: true,
		load:       loadFrames[groupMessage](true),
		offer:      (*Bounce).getGroupMessagesToOffer,
	},
	{
		frameType:  typeConfirmation,
		table:      "confirmations",
		dialWorthy: true,
		load:       loadFrames[confirmation](false),
		offer:      (*Bounce).getConfirmationsToOffer,
	},
	{
		frameType:  typeReadReceipt,
		table:      "read_receipts",
		dialWorthy: true,
		load:       loadFrames[readReceipt](false),
		offer:      (*Bounce).getReadReceiptsToOffer,
	},
	{
		frameType:  typeUpdateSettings,
		table:      "update_settings",
		dialWorthy: true,
		load:       loadFrames[updateSettings](false),
		offer:      (*Bounce).getUpdateSettingsToOffer,
	},
	{
		frameType:  typeFile,
		table:      "files",
		dialWorthy: true,
		load:       loadFrames[file](false),
		offer:      (*Bounce).getFilesToOffer,
	},
	{
		frameType:  typeChunkOffer,
		table:      "chunk_offers",
		dialWorthy: true,
		load:       loadFrames[chunkOffer](false),
		offer:      (*Bounce).getChunkOffersToOffer,
	},
	{
		frameType:  typeEncryptedChunkOffer,
		table:      "encrypted_chunk_offers",
		dialWorthy: true,
		load:       loadFrames[encryptedChunkOffer](false),
		offer:      (*Bounce).getEncryptedChunkOffersToOffer,
	},
	{
		frameType:  typeDraft,
		table:      "drafts",
		dialWorthy: true,
		load:       loadFrames[draft](false),
		offer:      (*Bounce).getDraftsToOffer,
	},
}

var (
	// specsByType indexes frameSpecs. Prefer syncableSpecs when iterating, so
	// that catch up order is respected and the traversal is deterministic.
	specsByType = map[uint16]*frameSpec{}

	// syncableSpecs is frameSpecs filtered to syncable entries, order preserved.
	syncableSpecs []*frameSpec

	// syncableTypes is the same set as bare type IDs, in the same order.
	syncableTypes []uint16

	// typeTable maps a frame type to its gorm table.
	typeTable = map[uint16]string{}

	// catchUpOrder ranks types for replay out of a catch up.
	catchUpOrder = map[uint16]int{}

	// allowedCatchUpFrames gates which types a peer may put in a catch up.
	allowedCatchUpFrames = map[uint16]bool{}
)

func init() {
	for i := range frameSpecs {
		spec := &frameSpecs[i]

		if _, duplicate := specsByType[spec.frameType]; duplicate {
			log.WithFields(log.Fields{
				"type": spec.frameType,
			}).Fatal("duplicate frame type in the frame registry")
		}

		specsByType[spec.frameType] = spec
		typeTable[spec.frameType] = spec.table

		catchUpOrder[spec.frameType] = i
		allowedCatchUpFrames[spec.frameType] = true
		syncableSpecs = append(syncableSpecs, spec)
		syncableTypes = append(syncableTypes, spec.frameType)
	}
}

// framePtr constrains PT to be *T where *T is a broadcastable. This is what
// lets loadFrames allocate a T, hand back a pointer to it, and still satisfy
// the interface - a plain [T broadcastable] would require the value type to
// implement it, which none of these do.
type framePtr[T any] interface {
	*T
	broadcastable
}

// loadFrames builds the per-type loader stored in frameSpec.load.
//
// This replaced sixteen near-identical functions in reference_request.go that
// differed only in the model type, whether they preloaded associations, and the
// noun in their log lines.
//
// preload selects Preload(clause.Associations), which the four types with
// association rows need in order to marshal completely. what names the type in
// log lines and nothing else.
func loadFrames[T any, PT framePtr[T]](preload bool) func(*Bounce, []uuid.UUID) []broadcastable {
	return func(b *Bounce, ids []uuid.UUID) []broadcastable {
		loaded := []broadcastable{}

		for _, id := range ids {
			var row T

			query := b.database
			if preload {
				query = query.Preload(clause.Associations)
			}

			if err := query.First(&row, "id = ?", id).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					// Not necessarily misbehaviour: a frame can be deleted
					// between the offer and the request that follows it.
					log.WithFields(log.Fields{
						"id": id,
					}).Warn("reference request asks for a frame we no longer have")
				} else {
					log.WithFields(log.Fields{
						"id":    id,
						"error": err.Error(),
					}).Fatal("database error querying for requested frame")
				}
				continue
			}

			loaded = append(loaded, PT(&row))
		}

		return loaded
	}
}
