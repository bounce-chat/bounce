package chat

import (
	"errors"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	// Look up a referenced frame definition by frame type
	referencedFramesByType = map[uint16]*referencedFrame{}

	// All types that can be synced via the reference flow
	referencedTypes []uint16

	// Database table names for each referenced frame
	typeTable = map[uint16]string{}

	// Order of frames inside of a catchup, when the timestamps are identical
	catchUpOrder = map[uint16]int{}

	// Frames that are allowed inside of a catch up
	allowedCatchUpFrames = map[uint16]bool{}
)

func init() {
	for i := range referencedFrames {
		rf := referencedFrames[i]

		if _, duplicate := referencedFramesByType[rf.frameType]; duplicate {
			log.WithFields(log.Fields{
				"type": rf.frameType,
			}).Fatal("duplicate frame type in the frame registry")
		}

		referencedFramesByType[rf.frameType] = rf
		typeTable[rf.frameType] = rf.table
		catchUpOrder[rf.frameType] = i
		allowedCatchUpFrames[rf.frameType] = true
		referencedTypes = append(referencedTypes, rf.frameType)
	}
}

type referencedFrame struct {
	frameType  uint16
	table      string
	dialWorthy bool
	load       func(b *Bounce, ids []uuid.UUID) []broadcastable
	offer      func(b *Bounce, address string, userID uuid.UUID) []frameReference
}

// All frames that are part of the reference flow have additional information defined here.  The order
// of the frames here is used to tie break identical timestamps when sorting these frames in a catchup.
var referencedFrames = []*referencedFrame{
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

// framePtr constrains PT to be *T where *T is a broadcastable. This is what
// lets loadFrames allocate a T, hand back a pointer to it, and still satisfy
// the interface - a plain [T broadcastable] would require the value type to
// implement it, which none of these do.
type framePtr[T any] interface {
	*T
	broadcastable
}

// loadFrames builds the per-type loader stored in referencedFrame.load.
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
					log.WithFields(log.Fields{
						"id": id,
					}).Debug("reference request asks for a frame we no longer have")
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
