package chat

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Basekick-Labs/msgpack/v6"
	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const encryptedBlobStorageLimit = 1024 * 1024 * 1024 * 10 // 10GiB
var encryptedBlobStorageSize = int64(0)
var encryptedBlockStorageLimitMutex sync.Mutex

var encryptedChunkOfferMutex sync.Mutex

type encryptedChunkOffer struct {
	SignedFrame
	cachedEncoding
	ID              uuid.UUID `gorm:"type:uuid;primary_key;"`
	Hash            string
	Location        string
	Timestamp       int64
	Author          uuid.UUID `msgpack:"-"`
	FileID          uuid.UUID `msgpack:"-"`
	SavedAt         int64     `msgpack:"-"`
	Scope           int       `msgpack:"-"`
	Destination     uuid.UUID `msgpack:"-"`
	LastRequestTime int64     `msgpack:"-"`
}

func (eco *encryptedChunkOffer) BeforeCreate(tx *gorm.DB) error {
	eco.SavedAt = time.Now().Unix()
	return nil
}

func (eco *encryptedChunkOffer) getID() uuid.UUID {
	return eco.ID
}

func (eco *encryptedChunkOffer) getScope(myID uuid.UUID) int {
	return eco.Scope
}

func (eco *encryptedChunkOffer) getDestination(myID uuid.UUID) uuid.UUID {
	if eco.Scope == scopeUser {
		return xor(eco.Destination, myID)
	}
	return eco.Destination
}

func (eco *encryptedChunkOffer) getType() uint16 {
	return typeEncryptedChunkOffer
}

func (eco *encryptedChunkOffer) getPayload() []byte {
	if len(eco.payload) == 0 {
		bytes, err := msgpack.Marshal(signedContainer{
			Payload:   eco.OriginalPayload,
			Signature: eco.Signature,
			Signer:    eco.Signer,
		})
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("error marshalling file's signed container")
		}
		eco.payload = bytes
	}

	return eco.payload
}

func (eco *encryptedChunkOffer) getAuthor() uuid.UUID {
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

	sc, err := b.unpackSignedContainer(payload)
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

	var existingECO encryptedChunkOffer
	err = b.database.Take(&existingECO, "id = ?", eco.ID).Error
	if err == nil {
		return &existingECO, false
	}

	var c chunk
	err = b.database.Take(&c, "encrypted_hash = ?", eco.Hash).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"hash": eco.Hash,
				"peer": peer,
			}).Error("received encrypted chunk offer for unknown chunk")
			return nil, false
		} else {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error looking up chunk")
		}
	}

	var unencryptedCO chunkOffer
	err = b.database.Take(&unencryptedCO, "hash = ?", c.Hash).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"hash": eco.Hash,
				"peer": peer,
			}).Warn("received encrypted chunk offer for chunk with no unencrypted offers")
			return nil, false
		} else {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error looking up chunk offer")
		}
	}

	eco.Author = unencryptedCO.Author
	eco.FileID = unencryptedCO.FileID
	eco.Scope = unencryptedCO.Scope
	eco.Destination = unencryptedCO.Destination

	err = b.database.Create(&eco).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error saving encrypted chunk offer")
	}

	b.makeNextChunkRequests()

	return nil, false
}

var encryptedChunkRequestMutex sync.Mutex

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
	for _, sr := range srs {
		neededChunks[sr.Hash] = append(neededChunks[sr.Hash], sr.Source)
	}

	recheck := false
	for hash, sources := range neededChunks {
		for i, s := range sources {
			rd := b.getRemoteDevice(s)
			if rd.connectedSockets.Load() > 0 {
				go b.sendDirect(s, &chunkRequest{Hash: hash})

				if len(sources) > i+1 {
					recheck = true
				}
				break
			}
		}
	}

	if recheck {
		go func() {
			time.Sleep((expectedChunkDeliverySeconds + 1) * time.Second)
			b.makeNextEncryptedChunkRequests()
		}()
	}
}

type encryptedStorageReferenceOffer struct {
	ECOs []uuid.UUID
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

	peerUserKeyMutex.Lock()
	peerKey, ok := peerUserKeys[address]
	peerUserKeyMutex.Unlock()
	if !ok {
		log.WithFields(log.Fields{
			"peer": address,
		}).Error("unable to send encrypted storage reference to peer without known key")
		return esro
	}

	var ecos []encryptedChunkOffer
	err := b.database.Select("encrypted_chunk_offers.id").
		Joins("LEFT JOIN delivery_records ON delivery_records.frame_id == encrypted_chunk_offers.id AND delivery_records.destination == ? AND delivery_records.frame_type == ?", address, typeEncryptedChunkOffer).
		Joins("LEFT JOIN encrypted_chunk_recipients ON encrypted_chunk_recipients.hash == encrypted_chunk_offers.hash AND encrypted_chunk_recipients.public_key == ?", peerKey).
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
			}).Warn("could not determine owner of encrypted device that send encrypted storage reference offer")
			b.sendDirect(peer, esrr)
			return nil, false
		} else {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error looking up user")
		}
	}

	// Find all files that this user can have access to
	filesThisDeviceCanHave := b.getFilesUserCanHave(ownerUser.ID)
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
			for _, hash := range strings.Split(f.EncryptedHashList, ",") {
				// Request that this encrypted devices store the chunks it does not have
				var existingECO encryptedChunkOffer
				err = b.database.Where("location = ? AND hash = ?", peer, hash).Take(&existingECO).Error
				if errors.Is(err, gorm.ErrRecordNotFound) {
					usersToInclude := b.getUsersInScope(&f)
					userKeys := [][]byte{}
					for i, _ := range usersToInclude {
						userKeys = append(userKeys, usersToInclude[i].PublicECDHKey)
					}
					esrr.StorageRequests = append(
						esrr.StorageRequests,
						encryptedChunkStorageRequest{
							ID:         uuid.New(),
							Hash:       hash,
							Recipients: userKeys,
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

	b.sendDirect(peer, esrr)

	return nil, false
}

type encryptedStorageReferenceRequest struct {
	ECORequests     []uuid.UUID
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
				}).Error("device requested encrypted chunk offer that could not be found")
			} else {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Fatal("database error looking up encrypted chunk offer")
			}
		}

		b.sendDirect(peer, &eco)
	}

	for _, sr := range esrr.StorageRequests {
		for _, key := range sr.Recipients {
			sr.RecipientUsers = append(sr.RecipientUsers, encryptedChunkRecipient{
				ID:        uuid.New(),
				Hash:      sr.Hash,
				PublicKey: key,
			})
		}
		sr.Source = peer

		var existingSR encryptedChunkStorageRequest
		err = b.database.Where("hash = ? AND source = ?", sr.Hash, sr.Source).Take(&existingSR).Error
		if err == nil {
			continue
		}

		err = b.database.Create(&sr).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("database error saving encrypted chunk storage request")
		}
	}

	if len(esrr.StorageRequests) > 0 {
		b.makeNextEncryptedChunkRequests()
	}

	return nil, false
}

type encryptedChunkStorageRequest struct {
	ID             uuid.UUID `gorm:"type:uuid;primary_key;"`
	Hash           string
	Recipients     [][]byte                  `gorm:"-"`
	RecipientUsers []encryptedChunkRecipient `msgpack:"-"`
	Source         string                    `msgpack:"-"`
	Downloaded     bool                      `msgpack:"-"`
	DownloadedAt   int64                     `msgpack:"-"`
}

func (ecsr *encryptedChunkStorageRequest) AfterDelete(tx *gorm.DB) error {
	err := tx.Clauses(clause.Returning{}).Where("encrypted_chunk_storage_request_id = ?", ecsr.ID).Delete(&encryptedChunkRecipient{}).Error
	if err != nil {
		return err
	}
	return tx.Clauses(clause.Returning{}).Where("frame_id = ? AND frame_type = ?", ecsr.ID, typeEncryptedChunkStorageRequest).Delete(&deliveryRecord{}).Error
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

	for _, key := range sr.Recipients {
		sr.RecipientUsers = append(sr.RecipientUsers, encryptedChunkRecipient{
			ID:        uuid.New(),
			Hash:      sr.Hash,
			PublicKey: key,
		})
	}
	sr.Source = peer

	var existingSR encryptedChunkStorageRequest
	err = b.database.Where("hash = ? AND source = ?", sr.Hash, sr.Source).Take(&existingSR).Error
	if err == nil {
		return nil, false
	}

	err = b.database.Create(&sr).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("database error saving encrypted chunk storage request")
	}
	b.makeNextEncryptedChunkRequests()

	return nil, false
}

type encryptedChunkRecipient struct {
	ID                             uuid.UUID `gorm:"type:uuid;primary_key;"`
	EncryptedChunkStorageRequestID uuid.UUID
	Hash                           string
	PublicKey                      []byte
}

func (b *Bounce) getFilesUserCanHave(userID uuid.UUID) []uuid.UUID {
	var allowedFiles []file

	if userID == b.currentUserID() {
		err := b.database.Select("files.*").Find(&allowedFiles).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error selecting files")
		}
	} else {
		// Get all files where
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
					)`,
				scopeSync,
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

	ids := []uuid.UUID{}
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
	b.sendEncryptedStorageReferenceOffer(peer)
	return nil, false
}

func (b *Bounce) getEncryptedBlobStorageSize() int64 {
	encryptedBlockStorageLimitMutex.Lock()
	size := encryptedBlobStorageSize
	encryptedBlockStorageLimitMutex.Unlock()
	return size
}

func (b *Bounce) incrementEncryptedBlobStorageSize(size int64) {
	encryptedBlockStorageLimitMutex.Lock()
	defer encryptedBlockStorageLimitMutex.Unlock()

	encryptedBlobStorageSize += size
}

func (b *Bounce) decrementEncryptedBlobStorageSize(size int64) {
	encryptedBlockStorageLimitMutex.Lock()
	defer encryptedBlockStorageLimitMutex.Unlock()

	encryptedBlobStorageSize -= size
	if encryptedBlobStorageSize < 0 {
		encryptedBlobStorageSize = 0
	}
}

func (b *Bounce) recheckEncryptedBlobStorageSize() {
	encryptedBlockStorageLimitMutex.Lock()
	defer encryptedBlockStorageLimitMutex.Unlock()

	var size int64
	err := filepath.Walk(b.configDirectory+"/blobs/", func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			size += info.Size()
		}
		return err
	})

	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
			"path":  b.configDirectory + "/blobs/",
		}).Error("error getting blob storage usage")
	}
	encryptedBlobStorageSize = size
}
