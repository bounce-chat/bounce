package chat

import (
	"errors"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// The frame registry is the single description of every frame type that has a
// database table, and of the subset of those that travel through the reference
// flow.
//
// Before it existed the same sixteen types were enumerated in eight separate
// places - catchUpOrder, typeTable, allowedCatchUpFrames, typesToRespondWith,
// shouldDialUser, the seed map in referencedIDs, the offer queries and the
// request queries - and nothing connected them. Omitting a type from one of the
// later ones produced a frame that worked perfectly between two online devices
// and silently never reached an offline one, which is close to the worst
// failure shape this codebase has: it does not error, it does not log, and it
// only shows up as missing history days later on a device nobody is watching.
//
// Every one of those tables is now derived from this list in init(), and
// frame_registry_test.go fails the build if an entry is incomplete or if a
// syncable type has no handler. Adding a persisted frame type is one entry here
// plus the broadcastable/catchUpAble methods on the struct itself.
type frameSpec struct {
	typ uint16

	// Gorm table name. Every persisted frame type has one, including the ones
	// that never enter the reference flow, because getFramesToRequestAndAck
	// counts rows by table name to decide whether a frame needs requesting.
	table string

	// syncable marks the types that move through offer -> request -> catch up.
	// A false here is not an oversight: chunks, append recipients and the
	// encrypted-device bookkeeping frames are persisted but are moved by their
	// own protocols rather than by the reference flow.
	syncable bool

	// dialWorthy answers "is a peer offering only frames of this type worth
	// opening a connection for". Consumed by referenceOffer.shouldDialUser.
	dialWorthy bool

	// load fetches the given IDs from the database as broadcastables. nil for
	// non-syncable types. The IDs handed to it have already been intersected
	// with what we offered - see handleReferenceRequest.
	load func(b *Bounce, ids []uuid.UUID) []broadcastable

	// offer produces the references for this type that address/userID has not
	// received yet, including every authorisation check that decides whether
	// they may have them at all. nil for non-syncable types.
	offer func(b *Bounce, address string, userID uuid.UUID) []frameReference
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
		typ: typeAddUser, table: "add_users", syncable: true, dialWorthy: true,
		load:  loadFrames[addUser](false, "add user"),
		offer: (*Bounce).getAddUsersToOffer,
	},
	{
		typ: typeDevice, table: "devices", syncable: true, dialWorthy: true,
		load:  loadFrames[device](true, "device"),
		offer: (*Bounce).getDevicesToOffer,
	},
	{
		typ: typeUpdateDevice, table: "update_devices", syncable: true, dialWorthy: true,
		load:  loadFrames[updateDevice](false, "update device"),
		offer: (*Bounce).getUpdateDevicesToOffer,
	},
	{
		// dialWorthy is false here and on updateDM, matching the behaviour of
		// the shouldDialUser chain this replaced. Both types omitted it there.
		typ: typeUpdateUser, table: "update_users", syncable: true,
		load:  loadFrames[updateUser](false, "update user"),
		offer: (*Bounce).getUpdateUsersToOffer,
	},
	{
		typ: typeUpdateDM, table: "update_dms", syncable: true,
		load:  loadFrames[updateDM](false, "update DM"),
		offer: (*Bounce).getUpdateDMsToOffer,
	},
	{
		typ: typeDirectMessage, table: "direct_messages", syncable: true, dialWorthy: true,
		load:  loadFrames[directMessage](true, "direct message"),
		offer: (*Bounce).getDirectMessagesToOffer,
	},
	{
		typ: typeGroupCreation, table: "group_creations", syncable: true, dialWorthy: true,
		load:  loadFrames[groupCreation](false, "group creation"),
		offer: (*Bounce).getGroupCreationsToOffer,
	},
	{
		typ: typeUpdateGroup, table: "update_groups", syncable: true, dialWorthy: true,
		load:  loadFrames[updateGroup](true, "update group"),
		offer: (*Bounce).getUpdateGroupsToOffer,
	},
	{
		typ: typeGroupMessage, table: "group_messages", syncable: true, dialWorthy: true,
		load:  loadFrames[groupMessage](true, "group message"),
		offer: (*Bounce).getGroupMessagesToOffer,
	},
	{
		typ: typeConfirmation, table: "confirmations", syncable: true, dialWorthy: true,
		load:  loadFrames[confirmation](false, "confirmation"),
		offer: (*Bounce).getConfirmationsToOffer,
	},
	{
		typ: typeReadReceipt, table: "read_receipts", syncable: true, dialWorthy: true,
		load:  loadFrames[readReceipt](false, "read receipt"),
		offer: (*Bounce).getReadReceiptsToOffer,
	},
	{
		typ: typeUpdateSettings, table: "update_settings", syncable: true, dialWorthy: true,
		load:  loadFrames[updateSettings](false, "update settings"),
		offer: (*Bounce).getUpdateSettingsToOffer,
	},
	{
		typ: typeFile, table: "files", syncable: true, dialWorthy: true,
		load:  loadFrames[file](false, "file"),
		offer: (*Bounce).getFilesToOffer,
	},
	{
		typ: typeChunkOffer, table: "chunk_offers", syncable: true, dialWorthy: true,
		load:  loadFrames[chunkOffer](false, "chunk offer"),
		offer: (*Bounce).getChunkOffersToOffer,
	},
	{
		typ: typeEncryptedChunkOffer, table: "encrypted_chunk_offers", syncable: true, dialWorthy: true,
		load:  loadFrames[encryptedChunkOffer](false, "encrypted chunk offer"),
		offer: (*Bounce).getEncryptedChunkOffersToOffer,
	},
	{
		typ: typeDraft, table: "drafts", syncable: true, dialWorthy: true,
		load:  loadFrames[draft](false, "draft"),
		offer: (*Bounce).getDraftsToOffer,
	},

	// Persisted, but not carried by the reference flow.
	{typ: typeChunk, table: "chunks"},
	{typ: typeAppendRecipient, table: "append_recipients"},
	{typ: typeEncryptedChunkStorageRequest, table: "encrypted_chunk_storage_requests"},
	{typ: typeEncryptedClearBefore, table: "encrypted_clear_befores"},
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

		if _, duplicate := specsByType[spec.typ]; duplicate {
			log.WithFields(log.Fields{
				"type": spec.typ,
			}).Fatal("duplicate frame type in the frame registry")
		}

		specsByType[spec.typ] = spec
		typeTable[spec.typ] = spec.table

		if spec.syncable {
			catchUpOrder[spec.typ] = len(syncableSpecs)
			allowedCatchUpFrames[spec.typ] = true
			syncableSpecs = append(syncableSpecs, spec)
			syncableTypes = append(syncableTypes, spec.typ)
		}
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
func loadFrames[T any, PT framePtr[T]](preload bool, what string) func(*Bounce, []uuid.UUID) []broadcastable {
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
						"id":    id,
						"frame": what,
					}).Warn("reference request asks for a frame we no longer have")
				} else {
					log.WithFields(log.Fields{
						"id":    id,
						"frame": what,
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
