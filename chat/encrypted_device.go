package chat

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	stdlog "log"
	"os"
	"slices"
	"sync"
	"time"

	"github.com/Basekick-Labs/msgpack/v6"
	"github.com/mdp/qrterminal/v3"
	log "github.com/sirupsen/logrus"
	"github.com/zeebo/blake3"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// The encrypted devices we have asked to manage, and when.  An answer is only taken from a device asked within
// wantToManageWindow; entries are compared against the clock rather than removed by a timer, so asking again
// restarts the window instead of being cut short by the first request's expiry.
var wantToManage = map[string]time.Time{}
var wantToManageMutex sync.Mutex

const wantToManageWindow = 5 * time.Minute

var setupKey string
var encryptedDeviceManagementMutex sync.Mutex

type authorizedUser struct {
	ID         ID `gorm:"primary_key;"`
	PublicKey  []byte
	SigningKey []byte
	OldKeys    []oldKey
}

type oldKey struct {
	ID               ID `gorm:"primary_key;"`
	AuthorizedUserID ID
	PublicKey        []byte
	SigningKey       []byte
}

func StartEncryptedDevice(network Network, platform Platform) {
	if os.Getenv("DEBUG") == "true" {
		log.SetLevel(log.DebugLevel)
		log.SetReportCaller(true)
	} else {
		log.SetLevel(log.InfoLevel)
	}
	installLogHooks(platform.ConfigDirectory)
	runningEngines.Add(1)

	b := &Bounce{
		encrypted:      true,
		platform:       platform,
		network:        network,
		referenceState: newReferenceState(),
		devicePool: &devicePool{
			deviceMutex:        sync.Mutex{},
			devices:            make(map[string]*remoteDevice),
			userOnlineStatus:   make(map[ID]bool),
			deviceOnlineStatus: make(map[ID]bool),
			lastDial:           make(map[string]time.Time),
			lastFailedDial:     make(map[string]time.Time),
			revokedDevices:     make(map[string]bool),
		},
		done:                      make(chan struct{}),
		handlerSlots:              make(chan struct{}, maximumRunningHandlersGlobal),
		encryptedChunkRequestWake: make(chan struct{}, 1),
	}
	b.ensureOnlyOneInstance()

	// An encrypted device has nobody to ask for a password or a recovery code, so its key file is wrapped by the
	// keystore or by nothing, and an install that cannot be unlocked cannot be started at all.
	//
	// It is also never moved onto the keystore after its first run, as Open does for a user's install.  These run
	// on machines with nobody at them, and one that was started a single time from a desktop session would from
	// then on need that session's keyring to start at all.
	kf, keys, err := unlock(platform, b.platform.ConfigDirectory+"/bounce.enc.db", "")
	if err != nil {
		b.releaseInstance()
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("this encrypted device could not be unlocked")
	}
	if !kf.wrapped() {
		log.Warn("the data in this install is not protected on this device: it has no keystore")
	}

	log.RegisterExitHandler(b.fatalShutdown)
	go b.handleInterrupts()

	b.openEncryptedDatabase(keys.databaseKey())
	b.network.Load(b.platform.ConfigDirectory, keys.networkKey())
	b.openReferenceDatabase()

	go b.recheckEncryptedBlobStorageSize()
	b.recheckUntrustedEncryptedFrameStorage()

	if !b.encryptedManagerProvisioned() {
		secretBytes := make([]byte, 16)
		rand.Read(secretBytes)
		setupKey = fmt.Sprintf("%x", secretBytes)
		fmt.Println("Welcome to Bounce!  This encrypted device is currently unmanaged.  Use the following setup key on an existing device to manage this encrypted device:")
		fmt.Printf("\n%s\n\n", b.buildSyncString(setupKey))
		config := qrterminal.Config{
			Level:     qrterminal.L,
			Writer:    os.Stdout,
			BlackChar: qrterminal.BLACK,
			WhiteChar: qrterminal.WHITE,
			QuietZone: 1,
		}
		qrterminal.GenerateWithConfig(b.buildSyncString(setupKey), config)
		fmt.Print("\n\n")
	}

	b.background(b.keepEncryptedDevicePruned)
	b.background(b.keepEncryptedChunksRequested)

	b.network.Start(
		NetworkCallbacks{
			NetworkOnline:  b.networkOnline,
			NetworkOffline: b.networkOffline,
		},
	)
}

// keepEncryptedDevicePruned is the encrypted device's housekeeping: what has expired is deleted, and what is only
// kept in memory about a peer goes once the peer has
func (b *Bounce) keepEncryptedDevicePruned() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-b.done:
			return
		case <-ticker.C:
		}
		b.pruneEncryptedDevice()
	}
}

func (b *Bounce) pruneEncryptedDevice() {
	err := deleteEach[encryptedFrame](b.database, "delete_at != 0 AND delete_at <= ?", time.Now().Unix())
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error pruning encrypted frames")
	}
	err = deleteEach[encryptedChunkStorageRequest](b.database, "delete_at != 0 AND delete_at <= ?", time.Now().Unix())
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error pruning encrypted chunk storage requests")
	}

	b.pruneOwnerMailboxes()
	b.forgetDepartedPeers()

	// Pruning and other deletes do not adjust the in-memory untrusted storage size, so reload it
	b.recheckUntrustedEncryptedFrameStorage()
}

// forgetDepartedPeers drops what an encrypted device holds in memory about peers that are no longer connected: the
// key each proved, the mailboxes each asked for, and any challenge still open.  A peer that returns is challenged
// again and says again what it can read, so nothing here needs to outlive the connection.
func (b *Bounce) forgetDepartedPeers() {
	connected := func(peer string) bool {
		b.devicePool.deviceMutex.Lock()
		rd, inPool := b.devicePool.devices[peer]
		b.devicePool.deviceMutex.Unlock()
		return inPool && rd.connectedSockets() > 0
	}

	peerUserKeyMutex.Lock()
	for peer := range peerUserKeys {
		if !connected(peer) {
			delete(peerUserKeys, peer)
		}
	}
	peerUserKeyMutex.Unlock()

	peerMailboxLabelsMutex.Lock()
	for peer := range peerMailboxLabels {
		if !connected(peer) {
			delete(peerMailboxLabels, peer)
		}
	}
	peerMailboxLabelsMutex.Unlock()

	lastERORTimeMutex.Lock()
	for peer := range lastERORTime {
		if !connected(peer) {
			delete(lastERORTime, peer)
		}
	}
	lastERORTimeMutex.Unlock()

	referenceOfferChallengeMutex.Lock()
	for peer := range referenceOfferChallengeTime {
		if !connected(peer) {
			delete(referenceOfferChallengeTime, peer)
			delete(referenceOfferChallengeMap, peer)
			delete(referenceOfferKeyMap, peer)
		}
	}
	referenceOfferChallengeMutex.Unlock()
}

func (b *Bounce) openEncryptedDatabase(databaseKey []byte) {
	databasePath := b.platform.ConfigDirectory + "/bounce.enc.db"
	databaseFile := databasePath + "?_key=" + sqlcipherKey(databaseKey)

	// Define a logger for gorm that uses logrus
	gormLogger := logger.New(
		stdlog.New(os.Stdout, "\r\n", stdlog.LstdFlags),
		logger.Config{
			LogLevel:                  logger.Error,
			IgnoreRecordNotFoundError: true,
		},
	)

	// Open the database
	var err error
	b.database, err = gorm.Open(sqlite.Open(databaseFile), &gorm.Config{
		TranslateError:                           true,
		Logger:                                   gormLogger,
		DisableForeignKeyConstraintWhenMigrating: true, // Use GORM hooks, not schema constraints
	})
	if err != nil {
		log.WithFields(log.Fields{
			"file":  databasePath,
			"error": err.Error(),
		}).Fatal("error opening database")
	}
	restrictDatabaseFiles(databaseFile)
	b.requireEncryptedDatabase()

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
		path := b.platform.ConfigDirectory + "/blobs/" + sr.Hash
		_, err := os.Stat(path)
		if err != nil {
			log.WithFields(log.Fields{
				"path": path,
			}).Warn("removing downloaded storage request without file")
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

// validManagementKeys reports whether an X25519 public key and an ed25519 public key are usable as the keys of the
// user who manages an encrypted device
func validManagementKeys(publicECDHKey []byte, signingKey []byte) bool {
	if len(signingKey) != ed25519.PublicKeySize {
		return false
	}
	_, err := ecdh.X25519().NewPublicKey(publicECDHKey)
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

	// These are the keys every later proof of ownership and management action is checked against, so a key that could
	// never check anything is refused here rather than stored
	if !validManagementKeys(edmr.Pubkey, edmr.SigningKey) {
		log.WithFields(log.Fields{
			"peer": peer,
		}).Warn("invalid keys in encrypted device management request")
		go b.sendDirect(peer, &encryptedDeviceManagementResponse{Accepted: false})
		return nil, false
	}

	if setupKey != "" && subtle.ConstantTimeCompare([]byte(edmr.Secret), []byte(setupKey)) == 1 {
		au := authorizedUser{
			ID:         NewID(),
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
		return errUserNotFound
	}

	address, secret, err := b.parseSyncString(data)
	if err != nil {
		return err
	}

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
	for asked, at := range wantToManage {
		if time.Since(at) > wantToManageWindow {
			delete(wantToManage, asked)
		}
	}
	wantToManage[address] = time.Now()
	wantToManageMutex.Unlock()

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
		}).Fatal("cannot msgpack marshal encrypted device management response")
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
	asked, ok := wantToManage[peer]
	wantToManageMutex.Unlock()
	if !ok || time.Since(asked) > wantToManageWindow {
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

			// It was a stranger when we connected to it, so it was told nothing then.  Now that it is ours, tell it
			// which mailboxes we can read and offer it what we hold, rather than leaving both to the next connection.
			go b.sendMailboxRequest(peer)
			go b.sendReferences(peer)
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
	ID        ID    `gorm:"primary_key;"`
	SavedAt   int64 `msgpack:"-" gorm:"index:idx_encrypted_frames_untrusted_age,priority:2"`
	FromOwner bool  `msgpack:"-" gorm:"index:idx_encrypted_frames_untrusted_age,priority:1"` // Stored by a peer that proved it is the owner, so not counted against the untrusted storage budget
	Payload   []byte
	DeleteAt  int64
	Slot      []byte `gorm:"index"` // The slot this copy occupies; see "Slots" in mailbox.go.  Random for all but drafts.
	Version   int64  // Which version of the slot's contents this is; a newer one replaces this copy
	Mailboxes []mailboxEntry
}

// One mailbox a stored frame is in, with the frame's DEK wrapped for that mailbox
type mailboxEntry struct {
	ID               ID     `gorm:"primary_key;" msgpack:"-"`
	EncryptedFrameID ID     `gorm:"index" msgpack:"-"`
	Label            []byte `gorm:"index"`
	Tag              []byte `gorm:"index"`
	WrappedDEK       []byte
}

func (entry *mailboxEntry) BeforeCreate(tx *gorm.DB) error {
	if entry.ID == NilID {
		entry.ID = NewID()
	}
	return nil
}

// A wrapped DEK is a 32 byte key sealed with AES-GCM, sixty bytes; anything much larger is not one
const maximumWrappedDEKBytes = 128

var errInvalidEncryptedFrame = errors.New("invalid encrypted frame")

// validEncryptedFrame checks the shape of a copy a peer wants stored.  A host cannot read a copy, but everything it
// stores about one is of a known size, so anything else is refused before it is stored.
func validEncryptedFrame(ef *encryptedFrame) error {
	if ef.ID == NilID || len(ef.Payload) == 0 || len(ef.Slot) != slotSize || ef.DeleteAt < 0 {
		return errInvalidEncryptedFrame
	}
	if len(ef.Mailboxes) == 0 || len(ef.Mailboxes) > maximumMailboxesPerCopy {
		return errInvalidEncryptedFrame
	}
	for _, entry := range ef.Mailboxes {
		if len(entry.Label) != mailboxLabelSize || len(entry.Tag) != mailboxTagSize {
			return errInvalidEncryptedFrame
		}
		if len(entry.WrappedDEK) == 0 || len(entry.WrappedDEK) > maximumWrappedDEKBytes {
			return errInvalidEncryptedFrame
		}
	}
	return nil
}

func (ef *encryptedFrame) BeforeCreate(tx *gorm.DB) error {
	if err := validEncryptedFrame(ef); err != nil {
		return err
	}
	ef.SavedAt = time.Now().Unix()
	return nil
}

// A stored copy's mailbox entries and the records of who it was delivered to go with it.  The host database is single
// connection, so this must use the transaction it is given and never b.database.
func (ef *encryptedFrame) AfterDelete(tx *gorm.DB) error {
	if ef.ID == NilID {
		return nil
	}
	err := tx.Where("encrypted_frame_id = ?", ef.ID).Delete(&mailboxEntry{}).Error
	if err != nil {
		return err
	}
	return tx.Where("frame_id = ? AND frame_type = ?", ef.ID, typeEncryptedFrame).Delete(&deliveryRecord{}).Error
}

func (ef encryptedFrame) getID() ID {
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
	if err := validEncryptedFrame(&ef); err != nil {
		log.WithFields(log.Fields{
			"peer":      peer,
			"mailboxes": len(ef.Mailboxes),
		}).Warn("ignoring encrypted frame without an id, payload or slot, or with missing, malformed or too many mailbox entries")
		return nil, false
	}
	labels := [][]byte{}
	tags := [][]byte{}
	for _, entry := range ef.Mailboxes {
		labels = append(labels, entry.Label)
		tags = append(tags, entry.Tag)
	}

	// A peer that passed the reference offer challenge for the authorized user is the owner's own device.  Anyone
	// else may only leave frames in the owner's mailboxes, which the owner registers on every connection; that is
	// how the devices of contacts this device has never met deliver to the owner, without the host learning who
	// they are.  Their frames are stored in the untrusted tier.
	// Checked before duplicate lookups so a stranger cannot probe whether a copy ID or tag is stored here.
	fromOwner := b.peerIsOwner(peer)
	if !fromOwner && !b.ownerCanReadAnyLabel(labels) {
		log.WithFields(log.Fields{
			"id":        ef.ID,
			"peer":      peer,
			"mailboxes": len(ef.Mailboxes),
		}).Warn("ignoring encrypted frame that is not addressed to a mailbox of this device's owner")
		return nil, false
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
	// Keep the copy we have unless the owner is providing a trusted or more complete copy to replace an untrusted
	// or partial one (so a non-owner cannot poison or narrow an owner copy by racing its tag).
	var existingEntries []mailboxEntry
	err = b.database.Where("tag IN ?", tags).Find(&existingEntries).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error looking up mailbox entries")
	}
	if len(existingEntries) > 0 {
		if !fromOwner {
			b.markFrameDelivered(existingEntries[0].EncryptedFrameID, typeEncryptedFrame, peer)
			go b.sendAck(peer, typeEncryptedFrame, ef.ID)
			return nil, false
		}

		matchedIDs := []ID{}
		seenID := map[ID]bool{}
		for _, entry := range existingEntries {
			if !seenID[entry.EncryptedFrameID] {
				seenID[entry.EncryptedFrameID] = true
				matchedIDs = append(matchedIDs, entry.EncryptedFrameID)
			}
		}
		var matchedFrames []encryptedFrame
		err = b.database.Preload("Mailboxes").Where("id IN ?", matchedIDs).Find(&matchedFrames).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error loading matched encrypted frames")
		}

		alreadyCovered := len(matchedFrames) == 1 && matchedFrames[0].FromOwner
		if alreadyCovered {
			existingLabels := map[string]bool{}
			for _, entry := range matchedFrames[0].Mailboxes {
				existingLabels[string(entry.Label)] = true
			}
			for _, entry := range ef.Mailboxes {
				if !existingLabels[string(entry.Label)] {
					alreadyCovered = false
					break
				}
			}
		}
		if alreadyCovered {
			b.markFrameDelivered(matchedFrames[0].ID, typeEncryptedFrame, peer)
			go b.sendAck(peer, typeEncryptedFrame, ef.ID)
			return nil, false
		}

		for i := range matchedFrames {
			if err := b.database.Delete(&matchedFrames[i]).Error; err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Error("error replacing existing encrypted frame with owner copy")
				return nil, false
			}
			if !matchedFrames[i].FromOwner {
				b.adjustUntrustedEncryptedFrameStorage(-int64(len(matchedFrames[i].Payload)), -1)
			}
		}
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
			err = b.database.Delete(&occupant).Error
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Error("error replacing the occupant of a slot")
				return nil, false
			}
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
		// Not acked and not recorded as delivered, so the sender keeps offering it and a later attempt can store it
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error saving encrypted frame")
		return nil, false
	}
	if !fromOwner {
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
			go b.sendDirect(peer, encryptedReceive{
				ID:         ef.ID,
				Payload:    ef.Payload,
				Label:      entry.Label,
				WrappedDEK: entry.WrappedDEK,
			})
		}
	}
}

// A clear of a conversation's history on encrypted devices.  Locally it records which thread was cleared and up to
// when; on the wire it carries only the mailbox labels of the completed epochs before that time, so the host deletes
// mailboxes it cannot interpret rather than frames of a conversation it can name.
type encryptedClearBefore struct {
	ID        ID       `gorm:"primary_key;"`
	BatchKey  ID       `msgpack:"-"`
	Timestamp int64    `msgpack:"-"`
	Labels    [][]byte `gorm:"-"`
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
		go b.sendAck(peer, typeEncryptedClearBefore, ecb.ID)
		return nil, false
	}

	// Presenting a label is the proof of being able to read that mailbox, which is the same standing as being
	// allowed to clear it.  Only the cleared mailbox entries are removed; a copy is deleted once none of its
	// mailboxes remain, so clearing one conversation does not wipe a multi-mailbox copy (such as a profile update on
	// the author's own host) from every other conversation it belongs to.  The labels are taken a batch at a time, so
	// that however many a peer sends, no one query binds more than the database allows.
	for start := 0; start < len(labels); start += mailboxLabelQueryBatch {
		batch := labels[start:min(start+mailboxLabelQueryBatch, len(labels))]

		var affectedFrameIDs []ID
		err = b.database.Model(&mailboxEntry{}).Distinct("encrypted_frame_id").Where("label IN ?", batch).Pluck("encrypted_frame_id", &affectedFrameIDs).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("error finding encrypted frames for clear before")
		} else if len(affectedFrameIDs) > 0 {
			err = b.database.Where("label IN ?", batch).Delete(&mailboxEntry{}).Error
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Error("error deleting mailbox entries for clear before")
			} else {
				for fStart := 0; fStart < len(affectedFrameIDs); fStart += mailboxLabelQueryBatch {
					frameBatch := affectedFrameIDs[fStart:min(fStart+mailboxLabelQueryBatch, len(affectedFrameIDs))]
					stillHasEntries := b.database.Model(&mailboxEntry{}).Select("encrypted_frame_id").Where("encrypted_frame_id IN ?", frameBatch)
					err = deleteEach[encryptedFrame](b.database, "id IN ? AND id NOT IN (?)", frameBatch, stillHasEntries)
					if err != nil {
						log.WithFields(log.Fields{
							"error": err.Error(),
						}).Error("error deleting encrypted frames for clear before")
					}
				}
			}
		}

		// Likewise for batch-deletable stored chunks, whose files go once their last storage request is gone
		batchDeletableRequests := b.database.Model(&encryptedChunkStorageRequest{}).Select("id").Where("can_batch_delete = ?", true)
		var affectedRequestIDs []ID
		err = b.database.Model(&encryptedChunkRecipient{}).
			Distinct("encrypted_chunk_storage_request_id").
			Where("label IN ? AND encrypted_chunk_storage_request_id IN (?)", batch, batchDeletableRequests).
			Pluck("encrypted_chunk_storage_request_id", &affectedRequestIDs).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("error finding encrypted chunk storage requests for clear before")
		} else if len(affectedRequestIDs) > 0 {
			for rStart := 0; rStart < len(affectedRequestIDs); rStart += mailboxLabelQueryBatch {
				reqBatch := affectedRequestIDs[rStart:min(rStart+mailboxLabelQueryBatch, len(affectedRequestIDs))]
				err = b.database.Where("label IN ? AND encrypted_chunk_storage_request_id IN ?", batch, reqBatch).Delete(&encryptedChunkRecipient{}).Error
				if err != nil {
					log.WithFields(log.Fields{
						"error": err.Error(),
					}).Error("error deleting encrypted chunk recipients for clear before")
					continue
				}
				stillHasRecipients := b.database.Model(&encryptedChunkRecipient{}).Select("encrypted_chunk_storage_request_id").Where("encrypted_chunk_storage_request_id IN ?", reqBatch)
				err = deleteEach[encryptedChunkStorageRequest](b.database, "id IN ? AND id NOT IN (?)", reqBatch, stillHasRecipients)
				if err != nil {
					log.WithFields(log.Fields{
						"error": err.Error(),
					}).Error("error deleting encrypted chunk storage requests for clear before")
				}
			}
		}
	}

	b.recheckUntrustedEncryptedFrameStorage()
	go b.sendAck(peer, typeEncryptedClearBefore, ecb.ID)

	return nil, false
}

func (b *Bounce) updateEncryptedClearBefore(thread ID, timestamp int64) {
	err := b.database.Where("batch_key = ?", thread).Delete(&encryptedClearBefore{}).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error":  err.Error(),
			"thread": thread,
		}).Error("error removing old encrypted clear before records")
	}

	err = b.database.Create(&encryptedClearBefore{
		ID:        NewID(),
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
		groupIDs := []ID{}
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

	shared, err := ecdhSharedSecret(currentUser.PrivateECDHKey, eroc.Key)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error generating reference offer challenge key")
		return nil, false
	}
	dek := referenceOfferChallengeKey(shared)

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
	session := slices.Concat(
		[]byte(peer),
		[]byte(b.network.Address()),
	)
	ciphertext := dekGCM.Seal(nil, []byte{}, eroc.Challenge, session)

	go b.sendDirect(peer, &encryptedReferenceOfferResponse{
		PublicKey: currentUser.PublicECDHKey,
		Response:  ciphertext,
	})

	// The challenge is an encrypted device asking who we are; one we know of is also told what we can read.  Anyone
	// can pose a challenge, and our mailboxes are not for anyone who asks.
	encryptedDeviceCacheMutex.Lock()
	_, encryptedPeer := encryptedDeviceCache[peer]
	encryptedDeviceCacheMutex.Unlock()
	if encryptedPeer {
		go b.sendMailboxRequest(peer)
	}

	return nil, false
}

const referenceOfferChallengeInfo = "bounce-reference-offer-challenge"

// referenceOfferChallengeKey derives the key a reference offer challenge is answered under from the agreement
// between the encrypted device's ephemeral key and the user's key.  Whoever poses a challenge chooses both the
// public key the user agrees with and the bytes the user seals, so the answer is never sealed under the agreement
// itself or under anything else derived from it: with a label of its own, an answer says nothing about the keys
// that wrap a conversation between the same two public keys.
func referenceOfferChallengeKey(shared []byte) []byte {
	key, err := hkdf.Key(sha256.New, shared, nil, referenceOfferChallengeInfo, 32)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error deriving reference offer challenge key")
	}
	return key
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
	b.background(func() {
		success := false
		for range 5 {
			go b.sendDirect(peer, &encryptedReferenceOfferChallenge{
				Key:       pubkey,
				Challenge: challenge,
			})

			select {
			case <-b.done:
				return
			case <-time.After(5 * time.Second):
			}

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
	})
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
		}).Fatal("cannot msgpack marshal encrypted reference offer response")
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

	// Make sure that we offered a challenge to this device recently, and store it if we did
	referenceOfferChallengeMutex.Lock()
	for peer, ts := range referenceOfferChallengeTime {
		if ts < time.Now().Add(-5*time.Minute).Unix() {
			delete(referenceOfferChallengeTime, peer)
			delete(referenceOfferChallengeMap, peer)
			delete(referenceOfferKeyMap, peer)
		}
	}
	challenge, ok := referenceOfferChallengeMap[peer]
	referenceOfferChallengeMutex.Unlock()
	if !ok {
		// A slow peer answering a challenge that has since expired does this routinely
		log.WithFields(log.Fields{
			"peer": peer,
		}).Warn("received encrypted reference offer response for unknown challenge")
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
	shared, err := challengePrivateKey.ECDH(counterpartyPublicKey)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error doing ECDH exchange during reference offer challenge")
		return nil, false
	}
	dek := referenceOfferChallengeKey(shared)

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

	// Try to decrypt the challenge and make sure the data is unchanged
	session := slices.Concat(
		[]byte(b.network.Address()),
		[]byte(peer),
	)
	decryptedChallenge, err := dekGCM.Open(nil, []byte{}, eroc.Response, session)
	if err == nil && bytes.Equal(challenge, decryptedChallenge) {
		// Discard the single-use challenge and its ephemeral private key immediately
		referenceOfferChallengeMutex.Lock()
		delete(referenceOfferChallengeTime, peer)
		delete(referenceOfferChallengeMap, peer)
		delete(referenceOfferKeyMap, peer)
		referenceOfferChallengeMutex.Unlock()

		peerUserKeyMutex.Lock()
		peerUserKeys[peer] = eroc.PublicKey
		peerUserKeyMutex.Unlock()

		// The peer proved who it is; what it can read is decided by the mailboxes it asks for.  If it already
		// asked before proving itself, register those as the owner's and answer now.
		b.registerOwnerMailboxes(peer)
		if len(b.peerLabels(peer)) > 0 {
			go b.sendDirect(peer, b.hostOfferFor(peer))
		}

		b.wakeEncryptedChunkRequests()
	} else {
		log.WithFields(log.Fields{
			"peer": peer,
		}).Error("received encrypted reference offer response that could not be decrypted")
	}

	return nil, false
}

const actionTypeChangeManagementKey = 0

type encryptedDeviceManagementAction struct {
	ActionType int
	Data       []byte
}

type manageEncryptedDevice struct {
	ID        ID
	Action    []byte
	Signature []byte
}

func (med *manageEncryptedDevice) getID() ID {
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
		}).Error("invalid signing key length in manage encrypted device frame")
		go b.sendDirect(peer, &response)
		return nil, false
	}

	validSignature := ed25519.Verify(au.SigningKey, med.Action, med.Signature)
	if !validSignature {
		log.Warn("manage encrypted device has invalid signature with authorized user key")
		go b.sendDirect(peer, &response)
		return nil, false
	} else {
		response.Authorized = true
	}

	var edma encryptedDeviceManagementAction
	err = msgpack.Unmarshal(med.Action, &edma)
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
		// The new keys replace the ones every later action is checked against, so they are checked first: a key
		// that could never verify anything would leave the device unmanageable for good
		var ks keySet
		err = msgpack.Unmarshal(edma.Data, &ks)
		if err != nil || !validManagementKeys(ks.PublicECDHKey, ks.PublicECDSAKey) {
			log.WithFields(log.Fields{
				"peer": peer,
			}).Error("invalid key set in manage encrypted device")
			go b.sendDirect(peer, &response)
			return nil, false
		}

		// Create a copy of the old key
		err = b.database.Create(&oldKey{
			ID:               NewID(),
			AuthorizedUserID: au.ID,
			PublicKey:        au.PublicKey,
			SigningKey:       au.SigningKey,
		}).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("error creating old key for authorized user")
		}

		// Update the key in the database before revoking active owner sessions for the old key
		err = b.database.Table("authorized_users").Where("id = ?", au.ID).Updates(map[string]interface{}{"public_key": ks.PublicECDHKey, "signing_key": ks.PublicECDSAKey}).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("error updating management key")
			go b.sendDirect(peer, &response)
		} else {
			// Drop every cached owner proof under the replaced key and re-challenge connected peers so that
			// revoked devices cannot keep owner privileges on an existing connection while legitimate devices
			// with the new key re-prove ownership.
			rechallenge := []string{}
			peerUserKeyMutex.Lock()
			for addr, key := range peerUserKeys {
				if bytes.Equal(key, au.PublicKey) {
					delete(peerUserKeys, addr)
					rechallenge = append(rechallenge, addr)
				}
			}
			peerUserKeyMutex.Unlock()
			for _, addr := range rechallenge {
				if b.getRemoteDevice(addr).connectedSockets() > 0 {
					b.challengeUnencryptedPeerForReferenceOffer(addr)
				}
			}

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
	ID         ID
	Type       int
	Authorized bool
	Applied    bool
}

func (edmar *encryptedDeviceManagementActionResponse) getID() ID {
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
		}).Error("error unmarshalling encrypted device management action response")
		return nil, false
	}

	// A key change the device authorized but could not apply leaves it on a key we no longer use, which is as
	// unmanageable as one that would not authorize us at all
	manageable := edmr.Authorized
	if edmr.Type == actionTypeChangeManagementKey && !edmr.Applied {
		manageable = false
	}
	b.updateManageableWarning(peer, manageable)

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

	go b.sendDirect(peer, &managementKeyHashResponse{Hash: hashString(blake3.Sum256(au.SigningKey))})

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
		b.updateManageableWarning(peer, true)
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
			ID:        NewID(),
			Action:    encoded,
			Signature: signature,
		}

		go b.sendDirect(peer, &med)

		b.updateManageableWarning(peer, true)
	} else {
		log.WithFields(log.Fields{
			"peer": peer,
		}).Warn("encrypted device cannot be managed with any known key")
		b.updateManageableWarning(peer, false)
	}

	return nil, false
}

func (b *Bounce) updateManageableWarning(peer string, manageable bool) {
	var esd encryptedSyncDevice
	err := b.database.First(&esd, "address = ?", peer).Error
	if err == nil {
		if manageable {
			b.ui.EncryptedDeviceManageable(esd.ID)
		} else {
			b.ui.EncryptedDeviceUnmanageable(esd.ID)
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

// Create a map from public key hash to private key for all encrypted device management keys we've ever used
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
			}).Error("invalid key set in update user")
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
			}).Error("cannot get encryption key history before user exists")
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
			}).Error("invalid key set in update user")
			continue
		}

		results[hashString(blake3.Sum256(ks.PublicECDHKey))] = ks.PrivateECDHKey
	}

	return results
}
