package chat

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Basekick-Labs/msgpack/v6"
	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const encryptedBlobStorageLimit = 1024 * 1024 * 1024 * 10 // 10GiB
var encryptedBlobStorageSize = int64(0)
var encryptedBlobStorageLimitMutex sync.Mutex

var encryptedChunkOfferMutex sync.Mutex

type encryptedChunkOffer struct {
	SignedFrame
	ID              ID     `gorm:"primary_key;"`
	Hash            string `gorm:"index;uniqueIndex:idx_hash_location"`
	Location        string `gorm:"index;uniqueIndex:idx_hash_location"`
	Timestamp       int64
	Author          ID    `msgpack:"-"`
	FileID          ID    `msgpack:"-"`
	SavedAt         int64 `msgpack:"-"`
	Scope           int   `msgpack:"-"`
	Destination     ID    `msgpack:"-"`
	LastRequestTime int64 `msgpack:"-"`
}

func (eco *encryptedChunkOffer) BeforeCreate(tx *gorm.DB) error {
	eco.SavedAt = time.Now().Unix()
	return nil
}

func (eco *encryptedChunkOffer) getID() ID {
	return eco.ID
}

func (eco *encryptedChunkOffer) getScope(myID ID) int {
	return eco.Scope
}

func (eco *encryptedChunkOffer) getDestination(myID ID) ID {
	if eco.Scope == scopeSync {
		return myID
	}
	if eco.Scope == scopeUser {
		return xor(eco.Destination, myID)
	}
	return eco.Destination
}

func (eco *encryptedChunkOffer) getType() uint16 {
	return typeEncryptedChunkOffer
}

func (eco *encryptedChunkOffer) getPayload() []byte {
	bytes, err := msgpack.Marshal(signedContainer{
		Payload:   eco.OriginalPayload,
		Signature: eco.Signature,
		Signer:    eco.Signer,
	})
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error marshalling encrypted chunk offer's signed container")
	}

	return bytes
}

func (eco *encryptedChunkOffer) getAuthor() ID {
	return eco.Author
}

func (eco *encryptedChunkOffer) getTimestamp() int64 {
	return eco.Timestamp
}

func (eco *encryptedChunkOffer) getSavedAt() int64 {
	return eco.SavedAt
}

func (b *Bounce) handleEncryptedChunkOffer(peer string, payload []byte, _ bool) (broadcastable, bool) {
	encryptedChunkOfferMutex.Lock()
	defer encryptedChunkOfferMutex.Unlock()

	sc, err := b.unpackSignedContainer(typeEncryptedChunkOffer, payload)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unpacking signed container for encrypted chunk offer")
		return nil, false
	}
	var eco encryptedChunkOffer
	err = msgpack.Unmarshal(sc.Payload, &eco)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling encrypted chunk offer")
		return nil, false
	}
	eco.OriginalPayload = sc.Payload
	eco.Signature = sc.Signature
	eco.Signer = sc.Signer

	// An offer that is returned is acked by whoever called this handler, who also records that the peer holds it, so
	// it is not acked here as well
	var existingECO encryptedChunkOffer
	err = b.database.Take(&existingECO, "hash = ? AND location = ?", eco.Hash, eco.Location).Error
	if err == nil {
		if existingECO.ID == eco.ID {
			return &existingECO, false
		}

		// A second offer of a chunk at a location we already know of tells us nothing new.  It is not kept, and it is
		// acked by its own ID so that the peer stops sending it.
		go b.sendAck(peer, typeEncryptedChunkOffer, eco.ID)
		return nil, false
	}

	var c chunk
	err = b.database.Take(&c, "encrypted_hash = ?", eco.Hash).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// An encrypted device offers its chunks as soon as we connect, ahead of the catch up that brings the
			// file they belong to, so this is the usual order and not a fault.  The offer names no file, so there
			// is nothing to keep it under yet.  It is not acked either: an ack is what stops it being offered, and
			// it has to be offered again once the file is here.
			log.WithFields(log.Fields{
				"hash": eco.Hash,
				"peer": peer,
			}).Debug("received encrypted chunk offer before the file its chunk belongs to")
			return nil, false
		} else {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error looking up chunk")
		}
	}

	// Who this offer may be passed on to is decided by the file the chunk belongs to.  It is not taken from a plain
	// offer for the same chunk: those come from devices that hold the file, and an encrypted device is there for
	// when they are all offline.
	var f file
	err = b.database.Select("id", "author", "scope", "destination").Take(&f, "id = ?", c.FileID).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"hash": eco.Hash,
				"peer": peer,
			}).Error("received encrypted chunk offer for chunk with no file")
			go b.sendAck(peer, typeEncryptedChunkOffer, eco.ID)
			return nil, false
		} else {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error looking up file for encrypted chunk offer")
		}
	}

	eco.Author = f.Author
	eco.FileID = f.ID
	eco.Scope = f.Scope
	eco.Destination = f.Destination

	err = b.database.Clauses(clause.OnConflict{DoNothing: true}).Create(&eco).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error saving encrypted chunk offer")
	}

	b.addEncryptedChunkOfferToChunkEngine(&eco)
	return &eco, true
}

var encryptedChunkRequestMutex sync.Mutex

// wakeEncryptedChunkRequests has keepEncryptedChunksRequested look for chunks to ask for now, not at its next tick.
// It never blocks: one wake is remembered, which is enough for any number that arrive while a pass is running.
func (b *Bounce) wakeEncryptedChunkRequests() {
	select {
	case b.encryptedChunkRequestWake <- struct{}{}:
	default:
	}
}

// keepEncryptedChunksRequested is the one goroutine on an encrypted device that asks peers for the chunks they want
// stored.  It is woken when a storage request or a chunk arrives, and ticks so that a request that went unanswered,
// or that waited behind another to the same peer, is made without anything else having to arrive first.
func (b *Bounce) keepEncryptedChunksRequested() {
	ticker := time.NewTicker((expectedChunkDeliverySeconds + 1) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-b.done:
			return
		case <-b.encryptedChunkRequestWake:
		case <-ticker.C:
		}
		b.makeNextEncryptedChunkRequests()
	}
}

// makeNextEncryptedChunkRequests asks each connected peer for one of the chunks it wants stored, unless a request to
// that peer is still within the time a chunk is expected to take
func (b *Bounce) makeNextEncryptedChunkRequests() {
	encryptedChunkRequestMutex.Lock()
	defer encryptedChunkRequestMutex.Unlock()

	var srs []encryptedChunkStorageRequest
	err := b.database.Where("downloaded = ?", false).Find(&srs).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error looking up encrypted chunk storage requests")
	}

	if len(srs) == 0 {
		return
	}

	neededChunks := map[string][]string{}
	alreadyHitPeer := map[string]bool{}
	for _, sr := range srs {
		if sr.LastAttemptedAt > time.Now().Unix()-expectedChunkDeliverySeconds {
			alreadyHitPeer[sr.Source] = true
		} else {
			neededChunks[sr.Hash] = append(neededChunks[sr.Hash], sr.Source)
		}
	}

	for hash, sources := range neededChunks {
		for _, s := range sources {
			if _, alreadyHit := alreadyHitPeer[s]; alreadyHit {
				continue
			}

			rd := b.getRemoteDevice(s)
			if rd.connectedSockets() > 0 {
				go b.sendDirect(s, &chunkRequest{Hash: hash})
				alreadyHitPeer[s] = true
				err = b.database.Table("encrypted_chunk_storage_requests").Where("source = ? AND hash = ?", s, hash).Updates(map[string]interface{}{
					"last_attempted_at": time.Now().Unix(),
				}).Error
				if err != nil {
					log.Error("error updating last attempt field for encrypted chunk storage request")
				}
				break
			}
		}
	}
}

type encryptedStorageReferenceOffer struct {
	ECOs []ID
}

func (esro encryptedStorageReferenceOffer) getType() uint16 {
	return typeEncryptedStorageReferenceOffer
}

func (esro encryptedStorageReferenceOffer) getPayload() []byte {
	bytes, err := msgpack.Marshal(esro)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error marshalling encrypted storage reference offer")
	}
	return bytes
}

func (b *Bounce) sendEncryptedStorageReferenceOffer(address string) encryptedStorageReferenceOffer {
	esro := encryptedStorageReferenceOffer{}

	labels := b.peerLabels(address)
	if len(labels) == 0 {
		return esro
	}
	labelList := make([][]byte, 0, len(labels))
	for label := range labels {
		labelList = append(labelList, []byte(label))
	}

	var ecos []encryptedChunkOffer
	err := b.database.Distinct("encrypted_chunk_offers.id").
		Joins("LEFT JOIN delivery_records ON delivery_records.frame_id == encrypted_chunk_offers.id AND delivery_records.destination == ? AND delivery_records.frame_type == ?", address, typeEncryptedChunkOffer).
		Joins("LEFT JOIN encrypted_chunk_recipients ON encrypted_chunk_recipients.hash == encrypted_chunk_offers.hash AND encrypted_chunk_recipients.label IN ?", labelList).
		Where("delivery_records.id IS NULL AND encrypted_chunk_recipients.id IS NOT NULL").
		Find(&ecos).
		Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("database error looking up encrypted chunk offers to reference")
		return esro
	}

	for _, eco := range ecos {
		esro.ECOs = append(esro.ECOs, eco.ID)
	}

	b.sendDirect(address, esro)

	return esro
}

func (b *Bounce) handleEncryptedStorageReferenceOffer(peer string, payload []byte, catchUp bool) (broadcastable, bool) {
	var esro encryptedStorageReferenceOffer
	err := msgpack.Unmarshal(payload, &esro)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling encrypted storage reference offer")
		return nil, false
	}

	// Request any ECOs this ED has for us that we do not yet have
	esrr := encryptedStorageReferenceRequest{}
	for _, ecoID := range esro.ECOs {
		var existingECO encryptedChunkOffer
		err = b.database.Take(&existingECO, "id = ?", ecoID).Error
		if err == nil {
			go b.sendAck(peer, typeEncryptedChunkOffer, ecoID)
		} else if errors.Is(err, gorm.ErrRecordNotFound) {
			esrr.ECORequests = append(esrr.ECORequests, ecoID)
		} else {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error looking up encrypted chunk offer")
		}
	}

	// Find any chunks that the owner of the ED should have, and ask the ED to request them from us if it doesn't have them
	var ownerUser user
	err = b.database.Where("encrypted_devices LIKE ?", "%"+peer+"%").Take(&ownerUser).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"address": peer,
			}).Warn("could not determine owner of encrypted device that sent encrypted storage reference offer")
			go b.sendDirect(peer, esrr)
			return nil, false
		} else {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error looking up user")
		}
	}

	// Find all files that this user can have access to
	filesThisDeviceCanHave := b.getFilesUserCanHaveViaEncryptedDevice(ownerUser.ID)
	for _, fileID := range filesThisDeviceCanHave {
		// For each encrypted hash in this file, check if this encrypted device has already offered it
		var f file
		err = b.database.Take(&f, "id = ?", fileID).Error
		if err != nil {
			log.WithFields(log.Fields{
				"file_id": fileID,
			}).Warn("file not found that was returned from get files to offer")
			continue
		}

		if len(f.EncryptedHashList) > 0 {
			secrets, err := b.threadSecretsFor(&f)
			if err != nil {
				log.WithFields(log.Fields{
					"file_id": f.ID,
					"error":   err.Error(),
				}).Warn("cannot derive mailboxes for file, not requesting encrypted storage")
				continue
			}
			labels := mailboxLabels(b.secretsForOwner(secrets, ownerUser.ID))
			if len(labels) == 0 {
				continue
			}

			var downloadedHashes []string
			err = b.database.Model(&chunk{}).
				Where("file_id = ? AND downloaded = ? AND length(encrypted_hash) > 0", f.ID, true).
				Pluck("encrypted_hash", &downloadedHashes).Error
			if err != nil {
				log.WithFields(log.Fields{
					"file_id": f.ID,
					"error":   err.Error(),
				}).Fatal("database error looking up downloaded chunks for encrypted storage")
			}
			downloaded := map[string]bool{}
			for _, h := range downloadedHashes {
				downloaded[h] = true
			}

			deleteAt, canBatchDelete := b.getDeleteAtAndCanBatchDelete(fileID)
			for _, hash := range strings.Split(f.EncryptedHashList, ",") {
				if !downloaded[hash] {
					continue
				}
				// Request that this encrypted device store the chunks it does not have
				var existingECO encryptedChunkOffer
				err = b.database.Where("location = ? AND hash = ?", peer, hash).Take(&existingECO).Error
				if errors.Is(err, gorm.ErrRecordNotFound) {
					esrr.StorageRequests = append(
						esrr.StorageRequests,
						encryptedChunkStorageRequest{
							ID:             NewID(),
							Hash:           hash,
							Labels:         labels,
							DeleteAt:       deleteAt,
							CanBatchDelete: canBatchDelete,
						},
					)
				}
			}
		} else {
			log.WithFields(log.Fields{
				"file_id": f.ID,
			}).Warn("file does not have an encrypted hash list")
		}
	}

	go b.sendDirect(peer, esrr)

	return nil, false
}

type encryptedStorageReferenceRequest struct {
	ECORequests     []ID
	StorageRequests []encryptedChunkStorageRequest
}

func (esrr encryptedStorageReferenceRequest) getType() uint16 {
	return typeEncryptedStorageReferenceRequest
}

func (esrr encryptedStorageReferenceRequest) getPayload() []byte {
	bytes, err := msgpack.Marshal(esrr)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error marshalling encrypted storage reference request")
	}
	return bytes
}
func (b *Bounce) handleEncryptedStorageReferenceRequest(peer string, payload []byte, catchUp bool) (broadcastable, bool) {
	var esrr encryptedStorageReferenceRequest
	err := msgpack.Unmarshal(payload, &esrr)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling encrypted storage reference request")
		return nil, false
	}

	for _, ecoRequest := range esrr.ECORequests {
		var eco encryptedChunkOffer
		err = b.database.Take(&eco, "id = ?", ecoRequest).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				log.WithFields(log.Fields{
					"encrypted_chunk_offer_id": ecoRequest,
				}).Warn("device requested encrypted chunk offer that could not be found")
				continue
			} else {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Fatal("database error looking up encrypted chunk offer")
			}
		}

		// Only serve chunk offers for chunks addressed to a mailbox the peer has asked for
		var recipients []encryptedChunkRecipient
		err = b.database.Where("hash = ?", eco.Hash).Find(&recipients).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error looking up encrypted chunk recipients")
		}
		allowed := false
		for _, r := range recipients {
			if b.peerCanReadLabel(peer, r.Label) {
				allowed = true
				break
			}
		}
		if !allowed {
			log.WithFields(log.Fields{
				"peer": peer,
				"id":   eco.ID,
			}).Warn("peer requested encrypted chunk offer for a mailbox it did not ask for")
			continue
		}

		go b.sendDirect(peer, &eco)
	}

	stored := false
	for _, sr := range esrr.StorageRequests {
		if b.storeEncryptedChunkStorageRequest(sr, peer) {
			stored = true
		}
	}

	if stored {
		b.wakeEncryptedChunkRequests()
	}

	return nil, false
}

type encryptedChunkStorageRequest struct {
	ID              ID `gorm:"primary_key;"`
	Hash            string
	DeleteAt        int64
	CanBatchDelete  bool
	Labels          [][]byte                  `gorm:"-"` // The mailboxes (see mailbox.go) whose readers may fetch this chunk
	Timestamp       int64                     `msgpack:"-"`
	RecipientUsers  []encryptedChunkRecipient `msgpack:"-"`
	Source          string                    `msgpack:"-"`
	Downloaded      bool                      `msgpack:"-"`
	DownloadedAt    int64                     `msgpack:"-"`
	LastAttemptedAt int64                     `msgpack:"-"`
	FullPath        string                    `msgpack:"-" gorm:"not null"`
	OwnerRequested  bool                      `msgpack:"-"`
}

func (ecsr *encryptedChunkStorageRequest) BeforeCreate(tx *gorm.DB) error {
	ecsr.Timestamp = time.Now().Unix()
	return nil
}

// A chunk's blob is shared by every storage request for its hash, one per source and message, so it is removed only
// with the last of them, and the storage counter is adjusted here, in the one place every delete path passes.  The
// host database is single connection, so this must use the transaction it is given and never b.database.
func (ecsr *encryptedChunkStorageRequest) AfterDelete(tx *gorm.DB) error {
	if ecsr.ID == NilID {
		return nil
	}

	var remaining int64
	err := tx.Model(&encryptedChunkStorageRequest{}).Where("hash = ? AND id != ?", ecsr.Hash, ecsr.ID).Count(&remaining).Error
	if err != nil {
		return err
	}
	if remaining == 0 {
		if ecsr.FullPath != "" {
			releaseEncryptedBlob(ecsr.FullPath)
		}
		var offerIDs []ID
		if err := tx.Model(&encryptedChunkOffer{}).Where("hash = ?", ecsr.Hash).Pluck("id", &offerIDs).Error; err != nil {
			return err
		}
		if len(offerIDs) > 0 {
			if err := tx.Where("frame_id IN ? AND frame_type = ?", offerIDs, typeEncryptedChunkOffer).Delete(&deliveryRecord{}).Error; err != nil {
				return err
			}
			if err := tx.Where("id IN ?", offerIDs).Delete(&encryptedChunkOffer{}).Error; err != nil {
				return err
			}
		}
	}

	// A storage request has no delivery records to go with it.  It is kept under an ID this device gave it and is
	// never acked: a device that wants a chunk stored asks again through the storage reference flow.
	return tx.Where("encrypted_chunk_storage_request_id = ?", ecsr.ID).Delete(&encryptedChunkRecipient{}).Error
}

func (ecsr encryptedChunkStorageRequest) getType() uint16 {
	return typeEncryptedChunkStorageRequest
}

func (ecsr encryptedChunkStorageRequest) getPayload() []byte {
	bytes, err := msgpack.Marshal(ecsr)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error marshalling encrypted chunk storage request")
	}
	return bytes
}

func (b *Bounce) handleEncryptedChunkStorageRequest(peer string, payload []byte, catchUp bool) (broadcastable, bool) {
	var sr encryptedChunkStorageRequest
	err := msgpack.Unmarshal(payload, &sr)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling encrypted chunk storage request")
		return nil, false
	}

	if b.storeEncryptedChunkStorageRequest(sr, peer) {
		b.wakeEncryptedChunkRequests()
	}

	return nil, false
}

var encryptedChunkHashFormat = regexp.MustCompile("^[a-f0-9]{64}$")

// storeEncryptedChunkStorageRequest records a peer's request that we fetch a chunk from it and keep it for the
// readers of some mailboxes, and reports whether there is now something new to fetch.  A request arrives on its own
// or inside a storage reference request; both come through here, so a chunk is taken on the same terms either way:
// the owner may store anything, and anyone else only chunks addressed to one of the owner's mailboxes.
func (b *Bounce) storeEncryptedChunkStorageRequest(sr encryptedChunkStorageRequest, peer string) bool {
	if !encryptedChunkHashFormat.MatchString(sr.Hash) {
		log.WithFields(log.Fields{
			"hash": sr.Hash,
			"peer": peer,
		}).Warn("ignoring encrypted chunk storage request with invalid hash")
		return false
	}
	if sr.DeleteAt < 0 {
		log.WithFields(log.Fields{
			"peer": peer,
		}).Warn("ignoring encrypted chunk storage request with negative delete_at")
		return false
	}
	if len(sr.Labels) == 0 || len(sr.Labels) > maximumMailboxesPerCopy {
		log.WithFields(log.Fields{
			"peer":      peer,
			"mailboxes": len(sr.Labels),
		}).Warn("ignoring encrypted chunk storage request with no mailboxes or too many")
		return false
	}

	sr.ID = NewID()
	seenLabel := map[string]bool{}
	for _, label := range sr.Labels {
		if len(label) != mailboxLabelSize {
			log.WithFields(log.Fields{
				"peer": peer,
			}).Warn("ignoring encrypted chunk storage request with a malformed mailbox label")
			return false
		}
		if seenLabel[string(label)] {
			continue
		}
		seenLabel[string(label)] = true
		sr.RecipientUsers = append(sr.RecipientUsers, encryptedChunkRecipient{
			ID:    NewID(),
			Hash:  sr.Hash,
			Label: label,
		})
	}
	sr.Source = peer
	sr.FullPath = b.platform.ConfigDirectory + "/blobs/" + sr.Hash

	var existingSR encryptedChunkStorageRequest
	err := b.database.Where("hash = ? AND source = ?", sr.Hash, sr.Source).Take(&existingSR).Error
	if err == nil {
		return false
	}

	sr.OwnerRequested = b.peerIsOwner(peer)
	if !sr.OwnerRequested && !b.ownerCanReadAnyLabel(sr.Labels) {
		log.WithFields(log.Fields{
			"peer": peer,
			"hash": sr.Hash,
		}).Warn("ignoring encrypted chunk storage request that is not addressed to a mailbox of this device's owner")
		return false
	}

	var alreadyDownloadedSR encryptedChunkStorageRequest
	err = b.database.Where("hash = ? AND downloaded = ?", sr.Hash, true).Take(&alreadyDownloadedSR).Error
	if err == nil {
		sr.Downloaded = true
		sr.DownloadedAt = time.Now().Unix()
	}

	err = b.database.Create(&sr).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("database error saving encrypted chunk storage request")
		return false
	}
	return !sr.Downloaded
}

// A mailbox whose readers may fetch a stored chunk
type encryptedChunkRecipient struct {
	ID                             ID `gorm:"primary_key;"`
	EncryptedChunkStorageRequestID ID
	Hash                           string
	Label                          []byte `gorm:"index"`
}

func (b *Bounce) getFilesUserCanHaveViaEncryptedDevice(userID ID) []ID {
	var allowedFiles []file

	if userID == b.currentUserID() {
		err := b.database.Where("length(encrypted_hash_list) > 0").Select("files.*").Find(&allowedFiles).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error selecting files")
		}
	} else {
		// Get all files where
		//	the file has encrypted hashes
		// 	scope is not sync AND
		// 	scope is user and the destination is this device's user OR
		// 	scope is group and the destination is a group that this device's user is in OR
		//	scope is group with invites and the destination is a group that this device's user is in OR it is a group that this device's user has been invited to OR
		// 	scope is global and the author is me or this device's user OR
		// 	scope is global and the author is someone who shares a group with this device's user
		err := b.database.
			Distinct("files.id").
			Where(
				`
					(
						length(files.encrypted_hash_list) > 0
					) AND (
						(
							files.scope == ? AND files.destination = ?
						) OR (
							files.scope = ? AND files.destination IN (?)
						) OR (
							files.scope = ? AND (files.destination IN (?) OR files.destination IN (?))
						) OR (
							files.scope == ? AND (
								files.author == ? OR
								files.author == ? OR
								files.author IN (?)
							)
						)
					)`,
				scopeUser,
				xor(b.currentUserID(), userID),
				scopeGroup,
				b.database.
					Model(&group{}).
					Distinct().
					Select("groups.id").
					Joins("JOIN group_users ON group_users.group_id = groups.id").
					Where("user_id = ?", userID),
				scopeGroupWithInvites,
				b.database.
					Model(&group{}).
					Distinct().
					Select("groups.id").
					Joins("JOIN group_users ON group_users.group_id = groups.id").
					Where("user_id = ?", userID),
				b.database.
					Model(&group{}).
					Distinct().
					Select("id").
					Where("invites LIKE ?", "%"+userID.String()+"%"),
				scopeGlobal,
				b.currentUserID(),
				userID,
				b.database.
					Model(&user{}).
					Distinct().
					Select("users.id").
					Joins("JOIN group_users ON group_users.user_id = users.id").
					Where(
						"group_users.group_id IN (?)",
						b.database.
							Model(&group{}).
							Distinct().
							Select("groups.id").
							Joins("JOIN group_users ON group_users.group_id = groups.id").
							Where("user_id = ?", userID),
					),
			).
			Find(&allowedFiles).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error selecting files for reference offer")
		}
	}

	ids := []ID{}
	for _, f := range allowedFiles {
		ids = append(ids, f.ID)
	}

	return ids
}

type requestECRO struct{}

func (recro requestECRO) getType() uint16 {
	return typeRequestECRO
}

func (recro requestECRO) getPayload() []byte {
	return []byte{}
}

func (b *Bounce) handleRequestECRO(peer string, payload []byte, catchUp bool) (broadcastable, bool) {
	go b.sendEncryptedStorageReferenceOffer(peer)
	return nil, false
}

// releaseEncryptedBlob removes a chunk's file and takes its size off the storage counter.  Several storage request
// rows deleted together share one blob; whichever hook runs first removes it and the rest find it already gone.
func releaseEncryptedBlob(path string) {
	info, err := os.Stat(path)
	if err != nil {
		return
	}
	err = os.Remove(path)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
			"path":  path,
		}).Warn("failed to remove encrypted chunk from disk")
		return
	}
	encryptedBlobStorageLimitMutex.Lock()
	encryptedBlobStorageSize -= info.Size()
	if encryptedBlobStorageSize < 0 {
		encryptedBlobStorageSize = 0
	}
	encryptedBlobStorageLimitMutex.Unlock()
}

func (b *Bounce) getEncryptedBlobStorageSize() int64 {
	encryptedBlobStorageLimitMutex.Lock()
	size := encryptedBlobStorageSize
	encryptedBlobStorageLimitMutex.Unlock()
	return size
}

func (b *Bounce) incrementEncryptedBlobStorageSize(size int64) {
	encryptedBlobStorageLimitMutex.Lock()
	defer encryptedBlobStorageLimitMutex.Unlock()

	encryptedBlobStorageSize += size
}

func (b *Bounce) recheckEncryptedBlobStorageSize() {
	encryptedBlobStorageLimitMutex.Lock()
	defer encryptedBlobStorageLimitMutex.Unlock()

	var size int64
	err := filepath.Walk(b.platform.ConfigDirectory+"/blobs/", func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			size += info.Size()

			var srs []encryptedChunkStorageRequest
			b.database.Where("hash = ?", info.Name()).Find(&srs)
			if len(srs) == 0 {
				log.WithFields(log.Fields{
					"name": info.Name(),
				}).Warn("file in blobs directory has no database record associated with it")
			}
		}

		return nil
	})

	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
			"path":  b.platform.ConfigDirectory + "/blobs/",
		}).Error("error getting blob storage usage")
	}
	encryptedBlobStorageSize = size
}
