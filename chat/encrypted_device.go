package chat

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"fmt"
	stdlog "log"
	"os"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Basekick-Labs/msgpack/v6"
	"github.com/google/uuid"
	"github.com/mdp/qrterminal/v3"
	log "github.com/sirupsen/logrus"
	"github.com/zeebo/blake3"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

var wantToManage = map[string]bool{}
var wantToManageMutex sync.Mutex
var setupKey string
var encryptedDeviceManagementMutex sync.Mutex

type authorizedUser struct {
	ID         uuid.UUID `gorm:"type:uuid;primary_key;"`
	PublicKey  []byte
	SigningKey []byte
	OldKeys    []oldKey
}

type oldKey struct {
	ID               uuid.UUID `gorm:"type:uuid;primary_key;"`
	AuthorizedUserID uuid.UUID
	PublicKey        []byte
	SigningKey       []byte
}

func StartEncryptedDevice(network Network, configDirectory string) {
	if runtime.GOOS == "android" {
		log.SetLevel(log.DebugLevel)
		if os.Getenv("DEBUG") == "true" {
			log.SetReportCaller(true)
		}
	} else {
		if os.Getenv("DEBUG") == "true" {
			log.SetLevel(log.DebugLevel)
			log.SetReportCaller(true)
		} else {
			log.SetLevel(log.InfoLevel)
		}
	}
	log.AddHook(&filehook{})

	b := &Bounce{
		encrypted:       true,
		configDirectory: configDirectory,
		network:         network,
		devicePool: &devicePool{
			deviceMutex:        sync.Mutex{},
			devices:            make(map[string]*remoteDevice),
			userOnlineStatus:   make(map[uuid.UUID]bool),
			deviceOnlineStatus: make(map[uuid.UUID]bool),
			lastDial:           make(map[string]time.Time),
			lastFailedDial:     make(map[string]time.Time),
			revokedDevices:     make(map[string]bool),
		},
		done:         make(chan struct{}),
		handlerSlots: make(chan struct{}, maximumRunningHandlersGlobal),
	}
	b.ensureOnlyOneInstance()
	log.RegisterExitHandler(b.fatalShutdown)
	go b.handleInterrupts()

	b.openEncryptedDatabase()
	b.network.Load(b.configDirectory)
	b.openReferenceDatabase()

	go b.recheckEncryptedBlobStorageSize()
	b.recheckUntrustedEncryptedFrameStorage()

	if !b.encryptedManagerProvisioned() {
		secretBytes := make([]byte, 16)
		rand.Read(secretBytes)
		setupKey = fmt.Sprintf("%x", secretBytes)
		fmt.Println("Welcome to Bounce!  This encrypted device is currently unmanaged.  Use the following setup key on an existing device to manage this encrypted device:")
		fmt.Printf("\n%s:%s\n\n", b.network.Address(), setupKey)
		config := qrterminal.Config{
			Level:     qrterminal.L,
			Writer:    os.Stdout,
			BlackChar: qrterminal.BLACK,
			WhiteChar: qrterminal.WHITE,
			QuietZone: 1,
		}
		qrterminal.GenerateWithConfig(b.network.Address()+":"+setupKey, config)
		fmt.Print("\n\n")
	}

	ticker := time.NewTicker(5 * time.Minute)
	go func() {
		for {
			<-ticker.C
			err := b.database.Clauses(clause.Returning{}).Where("delete_at != 0 AND delete_at <= ?", time.Now().Unix()).Delete(&encryptedFrame{}).Error
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Error("error pruning encrypted frames")
			}
			err = b.database.Clauses(clause.Returning{}).Where("delete_at != 0 AND delete_at <= ?", time.Now().Unix()).Delete(&encryptedChunkStorageRequest{}).Error
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Error("error pruning encrypted frames")
			}

			b.pruneOrphanMailboxEntries()
			b.pruneOwnerMailboxes()

			// Pruning and other deletes do not adjust the in-memory untrusted storage size, so reload it
			b.recheckUntrustedEncryptedFrameStorage()
		}
	}()

	b.network.Start(
		NetworkCallbacks{
			NetworkOnline:  b.networkOnline,
			NetworkOffline: b.networkOffline,
		},
	)
}

func (b *Bounce) openEncryptedDatabase() {
	databaseFile := b.configDirectory + "/bounce.enc.db"

	// Define a logger for gorm that uses logrus
	gormLogger := logger.New(
		stdlog.New(os.Stdout, "\r\n", stdlog.LstdFlags), // TODO: https://gist.github.com/bnadland/2e4287b801a47dcfcc94
		logger.Config{
			LogLevel:                  logger.Error,
			IgnoreRecordNotFoundError: true,
		},
	)

	// Open the database
	var err error
	b.database, err = gorm.Open(sqlite.Open(databaseFile), &gorm.Config{
		TranslateError: true,
		Logger:         gormLogger,
	})
	if err != nil {
		log.WithFields(log.Fields{
			"file":  databaseFile,
			"error": err.Error(),
		}).Fatal("error opening database")
	}

	// Set max connections to 1 to avoid locks
	sqliteDB, err := b.database.DB()
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error getting underlying database interface from gorm while opening database")
	}
	sqliteDB.SetMaxOpenConns(1)

	// Migrate
	err = b.database.AutoMigrate(
		&deliveryRecord{},
		&authorizedUser{},
		&oldKey{},
		&encryptedFrame{},
		&mailboxEntry{},
		&ownerMailbox{},
		&encryptedChunkOffer{},
		&encryptedChunkStorageRequest{},
		&encryptedChunkRecipient{},
	)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error migrating the database")
	}

	// Find any encrypted storage requests that were downloaded and have had their data deleted and remove them
	var srs []encryptedChunkStorageRequest
	err = b.database.Where("downloaded = ?", true).Find(&srs).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error finding downloaded encrypted chunk storage requests")
	}
	for _, sr := range srs {
		path := b.configDirectory + "/blobs/" + sr.Hash
		_, err := os.Stat(path)
		if err != nil {
			log.WithFields(log.Fields{
				"path": path,
			}).Warn("removing downlaoded storage request without file")
			b.database.Delete(&sr)
		}
	}
}

func (b *Bounce) encryptedManagerProvisioned() bool {
	if !b.encrypted {
		return false
	}

	var au authorizedUser
	err := b.database.First(&au).Error
	return err == nil
}

type encryptedDeviceManagementRequest struct {
	Secret     string
	SigningKey []byte
	Pubkey     []byte
}

func (edmr *encryptedDeviceManagementRequest) getType() uint16 {
	return typeEncryptedDeviceManagementRequest
}

func (edmr *encryptedDeviceManagementRequest) getPayload() []byte {
	payload, err := msgpack.Marshal(edmr)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("cannot msgpack marshal encrypted device management request")
	}
	return payload
}

func (b *Bounce) handleEncryptedDeviceManagementRequest(peer string, payload []byte, catchUp bool) (broadcastable, bool) {
	encryptedDeviceManagementMutex.Lock()
	defer encryptedDeviceManagementMutex.Unlock()

	if b.encryptedManagerProvisioned() {
		go b.sendDirect(peer, &encryptedDeviceManagementResponse{Accepted: false})
		return nil, false
	}

	var edmr encryptedDeviceManagementRequest
	err := msgpack.Unmarshal(payload, &edmr)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling encrypted device management request")
		return nil, false
	}

	if len(edmr.SigningKey) != ed25519.PublicKeySize {
		log.WithFields(log.Fields{
			"peer":     peer,
			"length":   len(edmr.SigningKey),
			"expected": ed25519.PublicKeySize,
		}).Error("invalid signing key lengh in encrypted device management request")
		return nil, false
	}

	if setupKey != "" && subtle.ConstantTimeCompare([]byte(edmr.Secret), []byte(setupKey)) == 1 {
		au := authorizedUser{
			ID:         uuid.New(),
			PublicKey:  edmr.Pubkey,
			SigningKey: edmr.SigningKey,
		}
		err = b.database.Create(&au).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error creating authorized user")
		}

		go b.sendDirect(peer, &encryptedDeviceManagementResponse{Accepted: true})
	} else {
		go b.sendDirect(peer, &encryptedDeviceManagementResponse{Accepted: false})
	}

	return nil, false
}

func (b *Bounce) RequestToManageEncryptedDevice(data string) error {
	currentUser, ok := b.currentUser()
	if !ok {
		log.Error("cannot manage encrypted device before profile creation")
	}

	parts := strings.Split(data, ":")
	if len(parts) != 2 {
		return errors.New("invalid data")
	}
	address := parts[0]
	secret := parts[1]

	conn, err := b.network.Dial(address)
	if err != nil {
		return errors.New("could not connect to device")
	}
	b.insertConnectionIntoDevicePool(conn)

	edmr := encryptedDeviceManagementRequest{
		Pubkey:     currentUser.PublicECDHKey,
		SigningKey: currentUser.PublicECDSAKey,
		Secret:     secret,
	}

	wantToManageMutex.Lock()
	wantToManage[address] = true
	wantToManageMutex.Unlock()
	go func() {
		time.Sleep(5 * time.Minute)
		wantToManageMutex.Lock()
		delete(wantToManage, address)
		wantToManageMutex.Unlock()
	}()

	go b.sendDirect(address, &edmr)

	return nil
}

type encryptedDeviceManagementResponse struct {
	Accepted bool
}

func (edmr *encryptedDeviceManagementResponse) getType() uint16 {
	return typeEncryptedDeviceManagementResponse
}

func (edmr *encryptedDeviceManagementResponse) getPayload() []byte {
	payload, err := msgpack.Marshal(edmr)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("cannot msgpack marshal encrypted device management request")
	}
	return payload
}

func (b *Bounce) handleEncryptedDeviceManagementResponse(peer string, payload []byte, catchUp bool) (broadcastable, bool) {
	var edmr encryptedDeviceManagementResponse
	err := msgpack.Unmarshal(payload, &edmr)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling encrypted device management response")
		return nil, false
	}

	wantToManageMutex.Lock()
	_, ok := wantToManage[peer]
	wantToManageMutex.Unlock()
	if !ok {
		log.Warn("received encrypted device management response from peer that we did not request to manage recently")
		return nil, false
	}

	if edmr.Accepted {
		err := b.addEncryptedDevice(peer)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("error adding new encrypted device")
			return nil, false
		}

		var newESD encryptedSyncDevice
		err = b.database.First(&newESD, "address = ?", peer).Error
		if err != nil {
			log.WithFields(log.Fields{
				"address": peer,
				"error":   err.Error(),
			}).Error("cannot find local sync device that was just created")
		} else {
			b.ui.DeviceAdded(Device{
				ID:        newESD.ID,
				Address:   peer,
				LastSeen:  time.Now().Unix(),
				CreatedAt: newESD.CreatedAt,
				Local:     false,
				Encrypted: true,
				Online:    true,
			})
			b.ui.EncryptedDeviceAdded()
			b.updateUserOnlineStatus(peer)
		}
	} else {
		b.ui.EncryptedDeviceRejected()
	}

	return nil, false
}

// A frame as an encrypted device stores it (see mailbox.go).  The ID is a random value the sender chose for this
// copy, the payload is sealed, and the mailboxes are the only thing the host can index by.  The type, the real
// frame ID, the author, the conversation and the time are all inside the ciphertext.
type encryptedFrame struct {
	ID        uuid.UUID `gorm:"type:uuid;primary_key;"`
	SavedAt   int64     `msgpack:"-" gorm:"index:idx_encrypted_frames_untrusted_age,priority:2"`
	FromOwner bool      `msgpack:"-" gorm:"index:idx_encrypted_frames_untrusted_age,priority:1"` // Stored by a peer that proved it is the owner, so not counted against the untrusted storage budget
	Payload   []byte
	DeleteAt  int64
	Slot      []byte `gorm:"index"` // The slot this copy occupies; see "Slots" in mailbox.go.  Random for all but drafts.
	Version   int64  // Which version of the slot's contents this is; a newer one replaces this copy
	Mailboxes []mailboxEntry
}

// One mailbox a stored frame is in, with the frame's DEK wrapped for that mailbox
type mailboxEntry struct {
	ID               uuid.UUID `gorm:"type:uuid;primary_key;" msgpack:"-"`
	EncryptedFrameID uuid.UUID `gorm:"index" msgpack:"-"`
	Label            []byte    `gorm:"index"`
	Tag              []byte    `gorm:"index"`
	WrappedDEK       []byte
}

func (entry *mailboxEntry) BeforeCreate(tx *gorm.DB) error {
	if entry.ID == uuid.Nil {
		entry.ID = uuid.New()
	}
	return nil
}

func (ef *encryptedFrame) BeforeCreate(tx *gorm.DB) error {
	ef.SavedAt = time.Now().Unix()
	return nil
}

func (ef encryptedFrame) getID() uuid.UUID {
	return ef.ID
}

func (ef encryptedFrame) getType() uint16 {
	return typeEncryptedFrame
}

func (ef encryptedFrame) getTimestamp() int64 {
	return ef.SavedAt
}

func (ef encryptedFrame) getSavedAt() int64 {
	return ef.SavedAt
}

func (ef encryptedFrame) getPayload() []byte {
	bytes, err := msgpack.Marshal(&ef)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("cannot msgpack marshal encrypted frame")
	}

	return bytes
}

// deleteEncryptedFrame removes one stored copy along with its mailbox entries and the records of who it was
// delivered to
func (b *Bounce) deleteEncryptedFrame(id uuid.UUID) {
	for _, step := range []error{
		b.database.Where("encrypted_frame_id = ?", id).Delete(&mailboxEntry{}).Error,
		b.database.Where("frame_id = ? AND frame_type = ?", id, typeEncryptedFrame).Delete(&deliveryRecord{}).Error,
		b.database.Where("id = ?", id).Delete(&encryptedFrame{}).Error,
	} {
		if step != nil {
			log.WithFields(log.Fields{
				"id":    id,
				"error": step.Error(),
			}).Error("error deleting encrypted frame")
		}
	}
}

// pruneOrphanMailboxEntries removes mailbox entries whose frame is gone.  Bulk deletes of frames do not run per row
// hooks, so every bulk delete calls this afterwards.
func (b *Bounce) pruneOrphanMailboxEntries() {
	err := b.database.Exec("DELETE FROM mailbox_entries WHERE encrypted_frame_id NOT IN (SELECT id FROM encrypted_frames)").Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error pruning orphan mailbox entries")
	}
}

var encryptedFrameMutex sync.Mutex

func (b *Bounce) handleEncryptedFrame(peer string, payload []byte, catchUp bool) (broadcastable, bool) {
	encryptedFrameMutex.Lock()
	defer encryptedFrameMutex.Unlock()

	var ef encryptedFrame
	err := msgpack.Unmarshal(payload, &ef)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling encrypted frame")
		return nil, false
	}
	if ef.ID == uuid.Nil || len(ef.Mailboxes) == 0 || len(ef.Payload) == 0 || len(ef.Slot) != slotSize {
		log.WithFields(log.Fields{
			"peer": peer,
		}).Warn("ignoring encrypted frame without an id, payload, slot, or mailboxes")
		return nil, false
	}
	labels := [][]byte{}
	tags := [][]byte{}
	for _, entry := range ef.Mailboxes {
		if len(entry.Label) != mailboxLabelSize || len(entry.Tag) != mailboxTagSize || len(entry.WrappedDEK) == 0 {
			log.WithFields(log.Fields{
				"peer": peer,
			}).Warn("ignoring encrypted frame with a malformed mailbox entry")
			return nil, false
		}
		labels = append(labels, entry.Label)
		tags = append(tags, entry.Tag)
	}

	var existingEF encryptedFrame
	err = b.database.Take(&existingEF, "id = ?", ef.ID).Error
	if err == nil {
		b.markFrameDelivered(ef.ID, typeEncryptedFrame, peer)
		go b.sendAck(peer, typeEncryptedFrame, ef.ID)
		return nil, false
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error looking up encrypted frame")
	}

	// Another device may have relayed this same frame already under a different copy ID; the mailbox tags say so.
	// Keep the copy we have, remember that this peer holds it, and ack the sender's copy so its own tracking works.
	var existingEntry mailboxEntry
	err = b.database.Take(&existingEntry, "tag IN ?", tags).Error
	if err == nil {
		b.markFrameDelivered(existingEntry.EncryptedFrameID, typeEncryptedFrame, peer)
		go b.sendAck(peer, typeEncryptedFrame, ef.ID)
		return nil, false
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error looking up mailbox entry")
	}

	// A peer that passed the reference offer challenge for the authorized user is the owner's own device.  Anyone
	// else may only leave frames in the owner's mailboxes, which the owner registers on every connection; that is
	// how the devices of contacts this device has never met deliver to the owner, without the host learning who
	// they are.  Their frames are stored in the untrusted tier.
	fromOwner := b.peerIsOwner(peer)
	if !fromOwner && !b.ownerCanReadAnyLabel(labels) {
		log.WithFields(log.Fields{
			"id":        ef.ID,
			"peer":      peer,
			"mailboxes": len(ef.Mailboxes),
		}).Warn("ignoring encrypted frame that is not addressed to a mailbox of this device's owner")
		return nil, false
	}

	if ef.DeleteAt != 0 && ef.DeleteAt <= time.Now().Unix() {
		log.WithFields(log.Fields{
			"id":   ef.ID,
			"peer": peer,
		}).Warn("ignoring encrypted frame that should already be deleted")
		b.markFrameDelivered(ef.ID, typeEncryptedFrame, peer)
		go b.sendAck(peer, typeEncryptedFrame, ef.ID)
		return nil, false
	}

	// An owner frame occupies its slot alone: a newer version replaces what is there, an older one is dropped.  Only
	// owner frames are compared, so nobody else can hold a slot against the owner.  Slots are random for everything
	// but drafts, so this fires only for them (see "Slots" in mailbox.go).
	if fromOwner {
		var occupant encryptedFrame
		err = b.database.Take(&occupant, "slot = ? AND from_owner = ?", ef.Slot, true).Error
		if err == nil {
			if occupant.Version >= ef.Version {
				b.markFrameDelivered(occupant.ID, typeEncryptedFrame, peer)
				go b.sendAck(peer, typeEncryptedFrame, ef.ID)
				return nil, false
			}
			b.deleteEncryptedFrame(occupant.ID)
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error looking up slot occupant")
		}
	}

	// Frames that did not come from the owner share a storage budget
	ef.FromOwner = fromOwner
	if !fromOwner && !b.makeRoomForUntrustedEncryptedFrame(&ef, peer) {
		return nil, false
	}

	err = b.database.Create(&ef).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error saving encrypted frame")
	} else if !fromOwner {
		b.adjustUntrustedEncryptedFrameStorage(int64(len(ef.Payload)), 1)
	}
	b.markFrameDelivered(ef.ID, typeEncryptedFrame, peer)
	go b.sendAck(peer, typeEncryptedFrame, ef.ID)

	go b.encryptedBroadcast(ef)

	return nil, false
}

// encryptedBroadcast pushes a newly stored frame to every connected peer that has asked for one of its mailboxes
func (b *Bounce) encryptedBroadcast(ef encryptedFrame) {
	peerMailboxLabelsMutex.Lock()
	readers := map[string]mailboxEntry{}
	for peer, labels := range peerMailboxLabels {
		for _, entry := range ef.Mailboxes {
			if labels[string(entry.Label)] {
				readers[peer] = entry
				break
			}
		}
	}
	peerMailboxLabelsMutex.Unlock()

	for peer, entry := range readers {
		var dr deliveryRecord
		err := b.database.Where("destination = ? AND frame_id = ? AND frame_type = ?", peer, ef.ID, typeEncryptedFrame).First(&dr).Error
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			continue
		}
		rd := b.getRemoteDevice(peer)
		if rd.connectedSockets() > 0 {
			b.sendDirect(peer, encryptedReceive{
				ID:         ef.ID,
				Payload:    ef.Payload,
				Label:      entry.Label,
				WrappedDEK: entry.WrappedDEK,
				savedAt:    ef.SavedAt,
			})
		}
	}
}

// A clear of a conversation's history on encrypted devices.  Locally it records which thread was cleared and up to
// when; on the wire it carries only the mailbox labels of the completed epochs before that time, so the host deletes
// mailboxes it cannot interpret rather than frames of a conversation it can name.
type encryptedClearBefore struct {
	ID        uuid.UUID `gorm:"type:uuid;primary_key;"`
	BatchKey  uuid.UUID `msgpack:"-"`
	Timestamp int64     `msgpack:"-"`
	Labels    [][]byte  `gorm:"-"`
}

func (ecb encryptedClearBefore) getType() uint16 {
	return typeEncryptedClearBefore
}

func (ecb encryptedClearBefore) getPayload() []byte {
	bytes, err := msgpack.Marshal(ecb)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("cannot msgpack marshal encrypted clear before")
	}
	return bytes
}

func (b *Bounce) handleEncryptedClearBefore(peer string, payload []byte, _ bool) (broadcastable, bool) {
	var ecb encryptedClearBefore
	err := msgpack.Unmarshal(payload, &ecb)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling encrypted clear before")
		return nil, false
	}

	labels := [][]byte{}
	for _, label := range ecb.Labels {
		if len(label) == mailboxLabelSize {
			labels = append(labels, label)
		}
	}
	if len(labels) == 0 {
		b.sendAck(peer, typeEncryptedClearBefore, ecb.ID)
		return nil, false
	}

	// Presenting a label is the proof of being able to read that mailbox, which is the same standing as being
	// allowed to clear it.  A frame belongs to one conversation, and its other mailboxes are only the sender's sync
	// copy, so a frame in a cleared mailbox goes entirely rather than lingering under a label the clearer cannot
	// name.
	inClearedMailboxes := b.database.Model(&mailboxEntry{}).Select("encrypted_frame_id").Where("label IN ?", labels)
	err = b.database.Where("id IN (?)", inClearedMailboxes).Delete(&encryptedFrame{}).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error deleting encrypted frames for clear before")
	}
	b.pruneOrphanMailboxEntries()

	// Likewise for stored chunks, which are deleted one at a time so that their files go with them
	var clearedSRs []encryptedChunkStorageRequest
	inClearedChunkMailboxes := b.database.Model(&encryptedChunkRecipient{}).Select("encrypted_chunk_storage_request_id").Where("label IN ?", labels)
	err = b.database.Where("can_batch_delete = ? AND id IN (?)", true, inClearedChunkMailboxes).Find(&clearedSRs).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error finding encrypted chunk storage requests to delete")
	}
	for _, sr := range clearedSRs {
		err := b.database.Clauses(clause.Returning{}).Delete(&sr).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
				"id":    sr.ID,
			}).Error("error deleting encrypted chunk storage request")
		}
	}

	b.recheckUntrustedEncryptedFrameStorage()
	b.sendAck(peer, typeEncryptedClearBefore, ecb.ID)

	return nil, false
}

func (b *Bounce) updateEncryptedClearBefore(thread uuid.UUID, timestamp int64) {
	err := b.database.Where("batch_key = ?", thread).Delete(&encryptedClearBefore{}).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error":  err.Error(),
			"thread": thread,
		}).Error("error removing old encrypted clear before records")
	}

	err = b.database.Create(&encryptedClearBefore{
		ID:        uuid.New(),
		BatchKey:  thread,
		Timestamp: timestamp,
	}).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error":  err.Error(),
			"thread": thread,
		}).Error("error saving new encrypted clear before record")
		return
	}

	encryptedDeviceCacheMutex.Lock()
	for addr, _ := range encryptedDeviceCache {
		if b.getRemoteDevice(addr).connectedSockets() > 0 {
			go b.sendEncryptedClearBefores(addr)
		}
	}
	encryptedDeviceCacheMutex.Unlock()
}

// sendEncryptedClearBefores sends an encrypted device every clear it has not yet received, as mailbox labels.  Our
// own encrypted devices get every clear; another user's encrypted device gets the clears for the conversations we
// share with that user.
func (b *Bounce) sendEncryptedClearBefores(address string) {
	encryptedDeviceCacheMutex.Lock()
	ownerID, ok := encryptedDeviceCache[address]
	encryptedDeviceCacheMutex.Unlock()
	if !ok {
		log.WithFields(log.Fields{
			"address": address,
		}).Error("cannot send encrypted clear befores to address with unknown owner")
		return
	}

	var clears []encryptedClearBefore
	if ownerID == b.currentUserID() {
		err := b.database.
			Select("encrypted_clear_befores.*").
			Distinct().
			Joins("LEFT JOIN delivery_records ON delivery_records.frame_id == encrypted_clear_befores.id AND delivery_records.destination == ?", address).
			Where("delivery_records.id IS NULL").
			Find(&clears).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error looking up encrypted clear befores")
		}
	} else {
		groupIDs := []uuid.UUID{}
		b.consensusStore.Lock()
		for groupID, stack := range b.consensusStore.groups {
			top, err := stack.top()
			if err != nil {
				continue
			}
			if top.isMember(ownerID) {
				groupIDs = append(groupIDs, groupID)
			}
		}
		b.consensusStore.Unlock()
		threads := append(groupIDs, xor(b.currentUserID(), ownerID))

		err := b.database.
			Select("encrypted_clear_befores.*").
			Distinct().
			Joins("LEFT JOIN delivery_records ON delivery_records.frame_id == encrypted_clear_befores.id AND delivery_records.destination == ?", address).
			Where("delivery_records.id IS NULL AND batch_key IN (?)", threads).
			Find(&clears).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error looking up encrypted clear befores")
		}
	}

	for _, ecb := range clears {
		ecb.Labels = b.clearLabelsForThread(ecb.BatchKey, ecb.Timestamp)
		if len(ecb.Labels) == 0 {
			continue
		}
		b.sendDirect(address, ecb)
	}
}

type encryptedReferenceOfferChallenge struct {
	Key       []byte
	Challenge []byte
}

func (eroc *encryptedReferenceOfferChallenge) getType() uint16 {
	return typeEncryptedReferenceOfferChallenge
}

func (eroc *encryptedReferenceOfferChallenge) getPayload() []byte {
	bytes, err := msgpack.Marshal(&eroc)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("cannot msgpack marshal encrypted reference offer challenge")
	}
	return bytes
}

func (b *Bounce) handleEncryptedReferenceOfferChallenge(peer string, payload []byte, catchUp bool) (broadcastable, bool) {
	currentUser, ok := b.currentUser()
	if !ok {
		log.Error("cannot handle encrypted reference offer challenge when no profile exists")
		return nil, false
	}

	var eroc encryptedReferenceOfferChallenge
	err := msgpack.Unmarshal(payload, &eroc)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling encrypted reference offer challenge")
		return nil, false
	}

	dek, err := b.generateKEK(eroc.Key)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error generating reference offer challenge key")
		return nil, false
	}

	dekBlock, err := aes.NewCipher(dek)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error encrypting frame")
		return nil, false
	}
	dekGCM, err := cipher.NewGCMWithRandomNonce(dekBlock)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error encrypting frame")
		return nil, false
	}
	session := slices.Concat(
		[]byte(peer),
		[]byte(b.network.Address()),
	)
	ciphertext := dekGCM.Seal(nil, []byte{}, eroc.Challenge, session)

	b.sendDirect(peer, &encryptedReferenceOfferResponse{
		PublicKey: currentUser.PublicECDHKey,
		Response:  ciphertext,
	})

	// The challenge is the encrypted device asking what we can read; answer that too
	go b.sendMailboxRequest(peer)

	return nil, false
}

func eroChallengeKey() ([]byte, []byte) {
	curve := ecdh.X25519()
	privateKey, err := curve.GenerateKey(rand.Reader)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error generating x25519 private key")
	}
	publicKey := privateKey.PublicKey()

	return privateKey.Bytes(), publicKey.Bytes()
}

var referenceOfferChallengeMutex sync.Mutex
var referenceOfferChallengeMap = map[string][]byte{}
var referenceOfferKeyMap = map[string][]byte{}
var referenceOfferChallengeTime = map[string]int64{}
var peerUserKeys = map[string][]byte{}
var peerUserKeyMutex sync.Mutex

var lastERORTime = map[string]int64{}
var lastERORTimeMutex sync.Mutex

func (b *Bounce) challengeUnencryptedPeerForReferenceOffer(peer string) {
	privkey, pubkey := eroChallengeKey()

	challenge := make([]byte, 32)
	rand.Read(challenge)

	referenceOfferChallengeMutex.Lock()
	referenceOfferKeyMap[peer] = privkey
	referenceOfferChallengeMap[peer] = challenge
	referenceOfferChallengeTime[peer] = time.Now().Unix()
	referenceOfferChallengeMutex.Unlock()

	start := time.Now().Unix()
	go func() {
		success := false
		for range 5 {
			go b.sendDirect(peer, &encryptedReferenceOfferChallenge{
				Key:       pubkey,
				Challenge: challenge,
			})

			time.Sleep(5 * time.Second)

			lastERORTimeMutex.Lock()
			t, ok := lastERORTime[peer]
			lastERORTimeMutex.Unlock()
			if ok && t >= start {
				success = true
				break
			}
		}
		if !success {
			log.WithFields(log.Fields{
				"peer": peer,
			}).Warn("gave up delivering encrypted reference offer challenge to peer")
		}
	}()
}

type encryptedReferenceOfferResponse struct {
	PublicKey []byte
	Response  []byte
}

func (eroc *encryptedReferenceOfferResponse) getType() uint16 {
	return typeEncryptedReferenceOfferResponse
}

func (eroc *encryptedReferenceOfferResponse) getPayload() []byte {
	bytes, err := msgpack.Marshal(&eroc)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("cannot msgpack marshal encrypted reference offer challenge")
	}
	return bytes
}

func (b *Bounce) handleEncryptedReferenceOfferResponse(peer string, payload []byte, catchUp bool) (broadcastable, bool) {
	lastERORTimeMutex.Lock()
	lastERORTime[peer] = time.Now().Unix()
	lastERORTimeMutex.Unlock()

	var eroc encryptedReferenceOfferResponse
	err := msgpack.Unmarshal(payload, &eroc)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling encrypted reference offer response")
		return nil, false
	}

	// Get the current user
	var au authorizedUser
	err = b.database.First(&au).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error getting authorized user while handling encrypted reference offer response")
	}

	// Make sure that we offered a challenge to this device recently, and store it if we did
	referenceOfferChallengeMutex.Lock()
	for peer, ts := range referenceOfferChallengeTime {
		if ts < time.Now().Add(-5*time.Minute).Unix() {
			delete(referenceOfferChallengeTime, peer)
			delete(referenceOfferChallengeMap, peer)
		}
	}
	challenge, ok := referenceOfferChallengeMap[peer]
	referenceOfferChallengeMutex.Unlock()
	if !ok {
		log.WithFields(log.Fields{
			"peer": peer,
		}).Error("received encrypted reference offer response for unknown challenge")
		return nil, false
	}

	// Find our challenge private key, their public key, and do an ECDH exchange to get a shared key
	curve := ecdh.X25519()
	referenceOfferChallengeMutex.Lock()
	challengePrivateKeyBytes, ok := referenceOfferKeyMap[peer]
	referenceOfferChallengeMutex.Unlock()
	if !ok {
		log.WithFields(log.Fields{
			"peer": peer,
		}).Error("no reference offer challenge key for peer")
		return nil, false
	}

	challengePrivateKey, err := curve.NewPrivateKey(challengePrivateKeyBytes)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error parsing private key")
		return nil, false
	}
	counterpartyPublicKey, err := curve.NewPublicKey(eroc.PublicKey)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error parsing public key")
		return nil, false
	}
	dek, err := challengePrivateKey.ECDH(counterpartyPublicKey)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error doing ECDH exchange during reference offer challenge")
		return nil, false
	}

	// Prepare to decrypt with this key
	dekBlock, err := aes.NewCipher(dek)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error creating block")
		return nil, false
	}
	dekGCM, err := cipher.NewGCMWithRandomNonce(dekBlock)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error creating gcm")
		return nil, false
	}

	// Try to decypt the challenge and make sure the data is unchanged
	session := slices.Concat(
		[]byte(b.network.Address()),
		[]byte(peer),
	)
	decryptedChallenge, err := dekGCM.Open(nil, []byte{}, eroc.Response, session)
	if err == nil && bytes.Equal(challenge, decryptedChallenge) {
		peerUserKeyMutex.Lock()
		peerUserKeys[peer] = eroc.PublicKey
		peerUserKeyMutex.Unlock()

		// The peer proved who it is; what it can read is decided by the mailboxes it asks for.  If it already
		// asked before proving itself, register those as the owner's and answer now.
		b.registerOwnerMailboxes(peer)
		if len(b.peerLabels(peer)) > 0 {
			go b.sendDirect(peer, b.hostOfferFor(peer))
		}

		b.makeNextEncryptedChunkRequests()
	} else {
		log.WithFields(log.Fields{
			"peer": peer,
		}).Error("received encrypted reference offer response with invalid signature")
	}

	return nil, false
}

const actionTypeChangeManagementKey = 0

type encryptedDeviceManagementAction struct {
	ActionType int
	Data       []byte
}

type manageEncryptedDevice struct {
	ID        uuid.UUID
	Action    []byte
	Signature []byte
}

func (med *manageEncryptedDevice) getID() uuid.UUID {
	return med.ID
}

func (med *manageEncryptedDevice) getType() uint16 {
	return typeManageEncryptedDevice
}

func (med *manageEncryptedDevice) getPayload() []byte {
	bytes, err := msgpack.Marshal(med)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("cannot msgpack marshal manage encrypted device")
	}
	return bytes
}

func (b *Bounce) handleManageEncryptedDevice(peer string, payload []byte, catchUp bool) (broadcastable, bool) {
	var med manageEncryptedDevice
	err := msgpack.Unmarshal(payload, &med)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling manage encrypted device")
		return nil, false
	}

	response := encryptedDeviceManagementActionResponse{
		ID: med.ID,
	}

	var au authorizedUser
	err = b.database.First(&au).Error
	if err != nil {
		go b.sendDirect(peer, &response)
		return nil, false
	}

	if len(au.SigningKey) != ed25519.PublicKeySize {
		log.WithFields(log.Fields{
			"peer":     peer,
			"length":   len(au.SigningKey),
			"expected": ed25519.PublicKeySize,
		}).Error("invalid signing key lengh in manage encrypted device frame")
		return nil, false
	}

	validSignature := ed25519.Verify(au.SigningKey, med.Action, med.Signature)
	if !validSignature {
		log.Error("manage encrypted device has invalid signature with authorized user key")
		go b.sendDirect(peer, &response)
		return nil, false
	} else {
		response.Authorized = true
	}

	var edma encryptedDeviceManagementAction
	msgpack.Unmarshal(med.Action, &edma)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("failed to unmarshal manage encrypted device payload")
		go b.sendDirect(peer, &response)
		return nil, false
	}
	response.Type = edma.ActionType

	switch edma.ActionType {
	case actionTypeChangeManagementKey:
		var ks keySet
		err = msgpack.Unmarshal(edma.Data, &ks)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("invlaid key set in manage encrypted device")
			return nil, false
		}

		// Create a copy of the old key
		err = b.database.Create(&oldKey{
			ID:               uuid.New(),
			AuthorizedUserID: au.ID,
			PublicKey:        au.PublicKey,
			SigningKey:       au.SigningKey,
		}).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("error creating old key for authorized user")
		}

		// Update the peer address to key map
		peerUserKeyMutex.Lock()
		for addr, key := range peerUserKeys {
			if bytes.Equal(key, au.PublicKey) {
				peerUserKeys[addr] = ks.PublicECDHKey
			}
		}
		peerUserKeyMutex.Unlock()

		// Update the key in the database
		err = b.database.Table("authorized_users").Where("id = ?", au.ID).Updates(map[string]interface{}{"public_key": ks.PublicECDHKey, "signing_key": ks.PublicECDSAKey}).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("error updating management key")
			go b.sendDirect(peer, &response)
		} else {
			response.Applied = true
			go b.sendDirect(peer, &response)
		}
	default:
		log.WithFields(log.Fields{
			"action_type": edma.ActionType,
		}).Warn("unknown management action received from managing user")
		go b.sendDirect(peer, &response)
	}

	return nil, false
}

type encryptedDeviceManagementActionResponse struct {
	ID         uuid.UUID
	Type       int
	Authorized bool
	Applied    bool
}

func (edmar *encryptedDeviceManagementActionResponse) getID() uuid.UUID {
	return edmar.ID
}

func (edmar *encryptedDeviceManagementActionResponse) getType() uint16 {
	return typeEncryptedDeviceManagementActionResponse
}

func (edmar *encryptedDeviceManagementActionResponse) getPayload() []byte {
	bytes, err := msgpack.Marshal(edmar)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("cannot msgpack marshal encrypted device management action response")
	}
	return bytes
}

func (b *Bounce) handleEncryptedDeviceManagementActionResponse(peer string, payload []byte, _ bool) (broadcastable, bool) {
	var edmr encryptedDeviceManagementActionResponse
	err := msgpack.Unmarshal(payload, &edmr)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling encrypted device management response")
		return nil, false
	}

	if !edmr.Authorized {
		b.updateManagableWarning(peer, false)
		return nil, false
	} else {
		b.updateManagableWarning(peer, true)
	}

	return nil, false
}

type getManagementKeyHash struct{}

func (gmkh *getManagementKeyHash) getPayload() []byte {
	return []byte{}
}

func (gmkh *getManagementKeyHash) getType() uint16 {
	return typeGetManagementKeyHash
}

func (b *Bounce) handleGetManagementKeyHash(peer string, payload []byte, _ bool) (broadcastable, bool) {
	var au authorizedUser
	err := b.database.First(&au).Error
	if err != nil {
		log.Error("cannot return management key hash when no managing user exists")
		return nil, false
	}

	b.sendDirect(peer, &managementKeyHashResponse{Hash: hashString(blake3.Sum256(au.SigningKey))})

	return nil, false
}

func (b *Bounce) getManagementKeyHash(address string) {
	var esd encryptedSyncDevice
	err := b.database.First(&esd, "address = ?", address).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return
		} else {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error looking up encrypted sync device")
		}
	}

	go b.sendDirect(address, &getManagementKeyHash{})
}

type managementKeyHashResponse struct {
	Hash string
}

func (mkhr *managementKeyHashResponse) getPayload() []byte {
	payload, err := msgpack.Marshal(mkhr)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("cannot msgpack marshal management key hash response")
	}
	return payload
}

func (mkhr *managementKeyHashResponse) getType() uint16 {
	return typeManagementKeyHashResponse
}

func (b *Bounce) handleManagementKeyHashResponse(peer string, payload []byte, _ bool) (broadcastable, bool) {
	var mkhr managementKeyHashResponse
	err := msgpack.Unmarshal(payload, &mkhr)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling management key hash response")
		return nil, false
	}

	cu, ok := b.currentUser()
	if !ok {
		log.Error("cannot handle management key hash response before profile creation")
		return nil, false
	}

	if mkhr.Hash == hashString(blake3.Sum256(cu.PublicECDSAKey)) {
		// Keys already match, sync up the authorized user state now
		b.updateManagableWarning(peer, true)
		return nil, false
	}

	oldKeys := b.managementKeyHistory()
	if private, ok := oldKeys[mkhr.Hash]; ok {
		ks := keySet{
			PublicECDSAKey: cu.PublicECDSAKey,
			PublicECDHKey:  cu.PublicECDHKey,
		}
		keySetData, err := msgpack.Marshal(&ks)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("failed to marshal key set")
			return nil, false
		}

		edma := encryptedDeviceManagementAction{
			ActionType: actionTypeChangeManagementKey,
			Data:       keySetData,
		}
		encoded, err := msgpack.Marshal(&edma)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("error encoding encrypted device management action")
			return nil, false
		}
		signature := ed25519.Sign(private, encoded)

		med := manageEncryptedDevice{
			ID:        uuid.New(),
			Action:    encoded,
			Signature: signature,
		}

		b.sendDirect(peer, &med)

		b.updateManagableWarning(peer, true)
	} else {
		log.WithFields(log.Fields{
			"peer": peer,
		}).Warn("encrypted device cannot be managed with any known key")
		b.updateManagableWarning(peer, false)
	}

	return nil, false
}

func (b *Bounce) updateManagableWarning(peer string, managable bool) {
	var esd encryptedSyncDevice
	err := b.database.First(&esd, "address = ?", peer).Error
	if err == nil {
		if managable {
			b.ui.EncryptedDeviceManagable(esd.ID)
		} else {
			b.ui.EncryptedDeviceUnmanagable(esd.ID)
		}
	} else if errors.Is(err, gorm.ErrRecordNotFound) {
		log.WithFields(log.Fields{
			"peer": peer,
		}).Error("key hash response from unknown encrypted sync device")
	} else {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error looking up encrypted sync device")
	}
}

// Create a map from publick key hash to private key for all encrypted device management keys we've ever used
func (b *Bounce) managementKeyHistory() map[string]ed25519.PrivateKey {
	results := make(map[string]ed25519.PrivateKey)

	var u user
	err := b.database.Where("id = ?", b.currentUserID()).First(&u).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("cannot get management key history before user exists")
			return results
		} else {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error looking up current user")
		}
	}

	results[hashString(blake3.Sum256(u.PublicECDSAKey))] = ed25519.PrivateKey(u.PrivateECDSAKey)

	var uus []updateUser
	err = b.database.Where("target = ? AND type = ?", b.currentUserID(), updateUserTypeReplaceKeys).Find(&uus).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error looking up update users")
	}
	for _, uu := range uus {
		var ks keySet
		err = msgpack.Unmarshal(uu.Data, &ks)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("invlaid key set in update user")
			continue
		}

		results[hashString(blake3.Sum256(ks.PublicECDSAKey))] = ed25519.PrivateKey(ks.PrivateECDSAKey)
	}

	return results
}

func (b *Bounce) encryptionKeyHistory() map[string][]byte {
	results := make(map[string][]byte)

	var u user
	err := b.database.Where("id = ?", b.currentUserID()).First(&u).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("cannot get management key history before user exists")
			return results
		} else {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error looking up current user")
		}
	}

	results[hashString(blake3.Sum256(u.PublicECDHKey))] = u.PrivateECDHKey

	var uus []updateUser
	err = b.database.Where("target = ? AND type = ?", b.currentUserID(), updateUserTypeReplaceKeys).Find(&uus).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error looking up update users")
	}
	for _, uu := range uus {
		var ks keySet
		err = msgpack.Unmarshal(uu.Data, &ks)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("invlaid key set in update user")
			continue
		}

		results[hashString(blake3.Sum256(ks.PublicECDHKey))] = ks.PrivateECDHKey
	}

	return results
}
