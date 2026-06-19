package chat

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	stdlog "log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Basekick-Labs/msgpack/v6"
	"github.com/DeRuina/timberjack"
	"github.com/google/uuid"
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
	if os.Getenv("DEBUG") == "true" {
		log.SetReportCaller(true)
	}
	log.SetLevel(log.DebugLevel) // TODO: put behind the envar when ready.  run in warn otherwise?

	logfile = &timberjack.Logger{
		Filename:           configDirectory + "/bounce-encrypted-log.txt",
		MaxAge:             3,
		Compression:        "none",
		LocalTime:          true,
		RotateAt:           []string{"00:00", "12:00"},
		BackupTimeFormat:   "2006-01-02-15-04-05",
		AppendTimeAfterExt: true,
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
	}
	b.ensureOnlyOneInstance()
	log.RegisterExitHandler(b.fatalShutdown)
	go b.handleInterrupts()

	b.openEncryptedDatabase()
	b.network.Load(b.configDirectory)
	b.openReferenceDatabase()

	go b.recheckEncryptedBlobStorageSize()

	if !b.encryptedManagerProvisioned() {
		secretBytes := make([]byte, 16)
		rand.Read(secretBytes)
		setupKey = fmt.Sprintf("%x", secretBytes)
		fmt.Println("Welcome to Bounce!  This encrypted device is currently unmanaged.  Use the following setup key on an existing device to manage this encrypted device:")
		fmt.Printf("%s:%s\n\n", b.network.Address(), setupKey)
	}

	ticker := time.NewTicker(5 * time.Minute)
	go func() {
		for {
			<-ticker.C
			err := b.database.Where("delete_at != 0 AND delete_at <= ?", time.Now().Unix()).Delete(&encryptedFrame{}).Error
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Error("error pruning encrypted frames")
			}
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
		&recipient{},
		&deviceRecipient{},
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
	}

	var edmr encryptedDeviceManagementRequest
	err := msgpack.Unmarshal(payload, &edmr)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling encrypted device management request")
		return nil, false
	}

	if edmr.Secret == setupKey {
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

type recipient struct {
	ID               uuid.UUID `gorm:"type:uuid;primary_key;" msgpack:"-"`
	EncryptedFrameID uuid.UUID `msgpack:"-"`
	PublicKey        []byte
	EncrypterKey     []byte
	EncryptedDEK     []byte
}

func (r *recipient) BeforeCreate(tx *gorm.DB) error {
	r.ID = uuid.New()
	return nil
}

type deviceRecipient struct {
	ID               uuid.UUID `gorm:"type:uuid;primary_key;" msgpack:"-"`
	EncryptedFrameID uuid.UUID `msgpack:"-"`
	RecipientAddress string
	Counterparty     string
	EncryptedDEK     []byte
}

func (dr *deviceRecipient) BeforeCreate(tx *gorm.DB) error {
	dr.ID = uuid.New()
	return nil
}

type encryptedFrame struct {
	cachedEncoding
	ID               uuid.UUID `gorm:"type:uuid;primary_key;"`
	Type             uint16
	Timestamp        int64
	SavedAt          int64 `msgpack:"-"`
	Payload          []byte
	DeleteAt         int64
	BatchDeleteKey   uuid.UUID
	Recipients       []recipient
	DeviceRecipients []deviceRecipient
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
	return ef.Timestamp
}

func (ef encryptedFrame) getSavedAt() int64 {
	return ef.SavedAt
}

func (ef encryptedFrame) getPayload() []byte {
	if len(ef.payload) == 0 {
		bytes, err := msgpack.Marshal(&ef)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("cannot msgpack marshal encrypted frame")
		}
		ef.payload = bytes
	}
	return ef.payload
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
		}).Error("error unmarshalling encrypted send")
		return nil, false
	}

	var existingEF encryptedFrame
	err = b.database.Take(&existingEF, "id = ?", ef.ID).Error
	if err == nil {
		b.markFrameDelivered(ef.ID, ef.Type, peer)
		go b.sendAck(peer, ef.Type, ef.ID)
		return nil, false
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error looking up encrypted frame")
	}

	authorized := false
	peerUserKeyMutex.Lock()
	pubkey, ok := peerUserKeys[peer]
	peerUserKeyMutex.Unlock()
	if ok {
		var au authorizedUser
		err := b.database.Take(&au, "public_key = ?", pubkey).Error
		if err == nil {
			authorized = true
		}
	}

	if !authorized {
		for _, r := range ef.Recipients {
			var au authorizedUser
			err := b.database.Take(&au, "public_key = ?", r.PublicKey).Error
			if err == nil {
				authorized = true
				break
			}
		}
	}

	if !authorized {
		log.WithFields(log.Fields{
			"type":       ef.Type,
			"id":         ef.ID,
			"peer":       peer,
			"recipients": len(ef.Recipients),
		}).Warn("ignoring encrypted frame that did not include an authorized user as a recipient")
		return nil, false
	}

	if ef.DeleteAt != 0 && ef.DeleteAt <= time.Now().Unix() {
		log.WithFields(log.Fields{
			"id":         ef.ID,
			"peer":       peer,
			"recipients": len(ef.Recipients),
		}).Warn("ignoring encrypted frame that should already be deleted")
		b.markFrameDelivered(ef.ID, ef.Type, peer)
		go b.sendAck(peer, ef.Type, ef.ID)
		return nil, false
	}

	err = b.database.Create(&ef).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error saving encrypted frame")
	}
	b.markFrameDelivered(ef.ID, ef.Type, peer)
	go b.sendAck(peer, ef.Type, ef.ID)

	go b.encryptedBroadcast(ef)

	return nil, false
}

func (b *Bounce) encryptedBroadcast(ef encryptedFrame) {
	for _, r := range ef.Recipients {
		addr, ok := addressForKey(r.PublicKey)
		if !ok {
			return
		}
		var dr deliveryRecord
		err := b.database.Where("destination = ? AND frame_id = ? AND frame_type = ?", addr, ef.ID, ef.Type).First(&dr).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			rd := b.getRemoteDevice(addr)
			if rd.connectedSockets.Load() > 0 {
				b.sendDirect(addr, encryptedReceive{
					ID:           ef.ID,
					Type:         ef.Type,
					Payload:      ef.Payload,
					EncryptedDEK: r.EncryptedDEK,
					EncrypterKey: r.EncrypterKey,
					UseAddress:   false,
					savedAt:      ef.SavedAt,
				})
			}
		}
	}
}

func addressForKey(key []byte) (string, bool) {
	peerUserKeyMutex.Lock()
	defer peerUserKeyMutex.Unlock()

	for addr, peerKey := range peerUserKeys {
		if bytes.Equal(peerKey, key) {
			return addr, true
		}
	}
	return "", false
}

type encryptedClearBefore struct {
	ID        uuid.UUID `gorm:"type:uuid;primary_key;"`
	BatchKey  uuid.UUID
	Timestamp int64
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

	peerUserKeyMutex.Lock()
	requesterKey, ok := peerUserKeys[peer]
	peerUserKeyMutex.Unlock()

	if !ok {
		log.WithFields(log.Fields{
			"peer": peer,
		}).Error("cannot handle encrypted clear before from peer with unknown user key")
		return nil, false
	}

	err = b.database.Where(
		"id IN (?)",
		b.database.
			Model(&encryptedFrame{}).
			Distinct("encrypted_frames.id").
			Joins("LEFT JOIN recipients ON recipients.encrypted_frame_id == encrypted_frames.id AND recipients.public_key = ?", requesterKey).
			Where(
				"batch_delete_key = ? AND timestamp <= ? AND (encrypted_frames.type = ? OR encrypted_frames.type = ? OR encrypted_frames.type = ? OR encrypted_frames.type = ?) AND recipients.id IS NOT NULL",
				ecb.BatchKey,
				ecb.Timestamp,
				typeDirectMessage,
				typeGroupMessage,
				typeFile,
				typeReadReceipt,
			),
	).Delete(&encryptedFrame{}).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error":     err.Error(),
			"batch_key": ecb.BatchKey,
		}).Error("error deleting encrypted frames by batch key")
	}

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
		if b.getRemoteDevice(addr).connectedSockets.Load() > 0 {
			go b.sendEncryptedClearBefores(addr)
		}
	}
	encryptedDeviceCacheMutex.Unlock()
}

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

	if ownerID == b.currentUserID() {
		var all []encryptedClearBefore
		err := b.database.
			Select("encrypted_clear_befores.*").
			Distinct().
			Joins("LEFT JOIN delivery_records ON delivery_records.frame_id == encrypted_clear_befores.id AND delivery_records.destination == ?", address).
			Where("delivery_records.id IS NULL").
			Find(&all).Error
		if err == nil {
			for _, ecb := range all {
				b.sendDirect(address, ecb)
			}
		} else {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error looking up encrypted clear befores")
		}
	} else {
		var clearForTheUser encryptedClearBefore
		err := b.database.
			Select("encrypted_clear_befores.*").
			Joins("LEFT JOIN delivery_records ON delivery_records.frame_id == encrypted_clear_befores.id AND delivery_records.destination == ?", address).
			Where("delivery_records.id IS NULL AND batch_key = ?", xor(b.currentUserID(), ownerID)).
			Take(&clearForTheUser).Error
		if err == nil {
			b.sendDirect(address, clearForTheUser)
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error looking up encrypted clear before")
		}

		groupIDs := []uuid.UUID{}
		b.consensusStore.Lock()
		for groupID, stack := range b.consensusStore.groups {
			top, err := stack.top()
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Error("error getting group state while sending encrypted clear befores")
				continue
			}
			if top.isMember(ownerID) {
				groupIDs = append(groupIDs, groupID)
			}
		}
		b.consensusStore.Unlock()

		var clearsForTheGroups []encryptedClearBefore
		err = b.database.
			Select("encrypted_clear_befores.*").
			Distinct().
			Joins("LEFT JOIN delivery_records ON delivery_records.frame_id == encrypted_clear_befores.id AND delivery_records.destination == ?", address).
			Where("delivery_records.id IS NULL AND batch_key IN (?)", groupIDs).
			Find(&clearsForTheGroups).Error
		if err == nil {
			for _, ecb := range clearsForTheGroups {
				b.sendDirect(address, ecb)
			}
		} else {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error looking up encrypted clear befores")
		}
	}

	b.pruneEncryptedDrafts()
}

// Stores our intention to add this user's key as a recipient to all a group's update groups up until this timestamp
type appendRecipient struct {
	ID        uuid.UUID `gorm:"type:uuid;primary_key;"`
	GroupID   uuid.UUID
	UserID    uuid.UUID
	Timestamp int64
	Address   string
}

var appendRecipientMutex sync.Mutex

func (b *Bounce) addRecipientsIfNeeded() {
	appendRecipientMutex.Lock()
	defer appendRecipientMutex.Unlock()

	var ars []appendRecipient
	err := b.database.Find(&ars).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error looking up all append recipients")
	}

	for _, ar := range ars {
		rd := b.getRemoteDevice(ar.Address)
		if rd.connectedSockets.Load() < 1 {
			continue
		}

		var u user
		err = b.database.Take(&u, "id = ?", ar.UserID).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				log.WithFields(log.Fields{
					"user_id": ar.UserID,
				}).Error("user not found in append recipeint")
				continue
			} else {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Fatal("database error looking up user")
			}
		}

		desiredIDs := []uuid.UUID{ar.GroupID}
		var ugs []updateGroup
		err = b.database.Select("id").Where("target = ?", ar.GroupID).Find(&ugs).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error looking up update groups")
		}
		for _, ug := range ugs {
			desiredIDs = append(desiredIDs, ug.ID)
		}

		go b.sendDirect(ar.Address, &appendRecipientRequest{
			ID:     ar.ID,
			Frames: desiredIDs,
			Pubkey: u.PublicECDHKey,
		})
	}
}

type appendRecipientRequest struct {
	ID     uuid.UUID
	Frames []uuid.UUID
	Pubkey []byte
}

func (arr *appendRecipientRequest) getType() uint16 {
	return typeAppendRecipientRequest
}

func (arr *appendRecipientRequest) getPayload() []byte {
	bytes, err := msgpack.Marshal(arr)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("cannot msgpack marshal append recipient request")
	}
	return bytes
}

func (b *Bounce) handleAppendRecipientRequest(peer string, payload []byte, _ bool) (broadcastable, bool) {
	var arr appendRecipientRequest
	err := msgpack.Unmarshal(payload, &arr)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling append recipient request")
		return nil, false
	}

	response := appendRecipientResponse{
		ID: arr.ID,
	}

	peerUserKeyMutex.Lock()
	peerKey, ok := peerUserKeys[peer]
	peerUserKeyMutex.Unlock()
	if !ok {
		log.WithFields(log.Fields{
			"peer": peer,
		}).Warn("append recipient request from peer without key")
		b.sendDirect(peer, &response)
		return nil, false
	}

	for _, frameID := range arr.Frames {
		var ef encryptedFrame
		err = b.database.Select("id").Take(&ef, "id = ?", frameID).Error
		if err != nil {
			continue
		}

		var r recipient
		err = b.database.Select("id").Where("encrypted_frame_id = ? AND public_key = ?", frameID, arr.Pubkey).First(&r).Error
		if err == nil {
			continue
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error looking up recipient")
		}

		var peerRecipient recipient
		err = b.database.Where("encrypted_frame_id = ? AND public_key = ?", frameID, peerKey).First(&peerRecipient).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			continue
		} else if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error looking up recipient")
		}

		response.DEKs = append(response.DEKs, discloseDEK{
			FrameID:      frameID,
			EncrypterKey: peerRecipient.EncrypterKey,
			EncryptedDEK: peerRecipient.EncryptedDEK,
		})
	}

	b.sendDirect(peer, &response)
	return nil, false
}

type discloseDEK struct {
	FrameID      uuid.UUID
	EncrypterKey []byte
	EncryptedDEK []byte
}

type appendRecipientResponse struct {
	ID   uuid.UUID
	DEKs []discloseDEK
}

func (arr *appendRecipientResponse) getType() uint16 {
	return typeAppendRecipientResponse
}

func (arr *appendRecipientResponse) getPayload() []byte {
	bytes, err := msgpack.Marshal(arr)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("cannot msgpack marshal append recipient response")
	}
	return bytes
}

func (b *Bounce) handleAppendRecipientResponse(peer string, payload []byte, _ bool) (broadcastable, bool) {
	var arr appendRecipientResponse
	err := msgpack.Unmarshal(payload, &arr)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling append recipient response")
		return nil, false
	}

	// Find the original intention and user
	var ar appendRecipient
	err = b.database.Take(&ar, "id = ?", arr.ID).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"id": arr.ID,
			}).Warn("append recipient not found from append recipient response")
			return nil, false
		} else if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error looking up append recipient")
		}
	}

	var u user
	err = b.database.Take(&u, "id = ?", ar.UserID).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"id": ar.UserID,
			}).Warn("user not found from append recipient response")
			return nil, false
		} else if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error looking up user")
		}
	}

	currentUser, ok := b.currentUser()
	if !ok {
		log.Error("cannot handle append recipient response before profile is created")
		return nil, false
	}

	// Delete if there is nothing to do
	if len(arr.DEKs) == 0 {
		b.database.Delete(&ar)
		return nil, false
	}

	// Create the new recipients
	recipients := map[uuid.UUID]recipient{}
	for _, dd := range arr.DEKs {
		// Decrypt the DEK
		oldKek, err := b.generateKEK(dd.EncrypterKey)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("error generating key encryption key from discloseDEK")
			continue
		}

		oldKekBlock, err := aes.NewCipher(oldKek)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("error creating block")
			continue
		}
		oldKekGCM, err := cipher.NewGCMWithRandomNonce(oldKekBlock)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("error creating gcm")
			continue
		}

		dek, err := oldKekGCM.Open(nil, []byte{}, dd.EncryptedDEK, nil)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("error decrypting key in disclose DEK")
			continue
		}

		// Encrypt this DEK with the new recipient's key and generate a new recipient
		newKek, err := b.generateKEK(u.PublicECDHKey)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("error generating kek")
			continue
		}

		block, err := aes.NewCipher(newKek)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("error encrypting frame")
			continue
		}
		gcm, err := cipher.NewGCMWithRandomNonce(block)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("error encrypting frame")
			continue
		}

		recipients[dd.FrameID] = recipient{
			EncrypterKey: currentUser.PublicECDHKey,
			PublicKey:    u.PublicECDHKey,
			EncryptedDEK: gcm.Seal(nil, []byte{}, dek, nil),
		}
	}

	arp := appendRecipientPayloads{
		ID:         arr.ID,
		Recipients: recipients,
	}
	for range 5 {
		b.sendDirect(peer, &arp)
		time.Sleep(5 * time.Second)

		var originalAR appendRecipient
		err = b.database.Take(&originalAR, "id = ?", arr.ID).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				break
			} else {
				log.WithFields(log.Fields{
					"id":    arr.ID,
					"error": err.Error(),
				}).Fatal("database error looking up append recipient")
			}
		}
	}
	var originalAR appendRecipient
	err = b.database.Take(&originalAR, "id = ?", arr.ID).Error
	if err == nil {
		log.WithFields(log.Fields{
			"id": arr.ID,
		}).Error("append recipient still exists in database after attempt to send new recipients")
	}

	return nil, false
}

type appendRecipientPayloads struct {
	ID         uuid.UUID
	Recipients map[uuid.UUID]recipient
}

func (arp *appendRecipientPayloads) getType() uint16 {
	return typeAppendRecipientPayloads
}

func (arp *appendRecipientPayloads) getPayload() []byte {
	bytes, err := msgpack.Marshal(arp)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("cannot msgpack marshal append recipient payloads")
	}
	return bytes
}

func (b *Bounce) handleAppendRecipientPayloads(peer string, payload []byte, _ bool) (broadcastable, bool) {
	var arp appendRecipientPayloads
	err := msgpack.Unmarshal(payload, &arp)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling append recipient payloads")
		return nil, false
	}

	go b.sendAck(peer, typeAppendRecipientPayloads, arp.ID)

	for id, r := range arp.Recipients {
		r.ID = uuid.New()
		r.EncryptedFrameID = id
		err = b.database.Clauses(clause.OnConflict{DoNothing: true}).Create(&r).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("error saving new recipient")
		}
	}

	return nil, false
}

func (b *Bounce) encryptedDevicesInGroup(groupID uuid.UUID) []string {
	gs, err := b.currentGroupState(groupID)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error getting group state for all encrypted devices in a group")
		return []string{}
	}

	addresses := []string{}
	for _, userID := range gs.users {
		var u user
		err = b.database.Select("encrypted_devices").Where("id = ?", userID).Take(&u).Error
		if err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Fatal("database error looking up user encrypted devices")
			} else {
				continue
			}
		}
		if len(u.EncryptedDevices) > 0 {
			for _, addr := range strings.Split(u.EncryptedDevices, ",") {
				addresses = append(addresses, addr)
			}
		}
	}
	return addresses
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

var lastROTime = map[string]int64{}
var lastROTimeMutex sync.Mutex

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
	ciphertext := dekGCM.Seal(nil, []byte{}, eroc.Challenge, nil)

	start := time.Now().Unix()
	success := false
	for range 5 {
		go b.sendDirect(peer, &encryptedReferenceOfferResponse{
			PublicKey: currentUser.PublicECDHKey,
			Response:  ciphertext,
		})

		time.Sleep(5 * time.Second)

		lastROTimeMutex.Lock()
		t, ok := lastROTime[peer]
		lastROTimeMutex.Unlock()
		if ok && t >= start {
			success = true
			break
		}
	}
	if !success {
		log.WithFields(log.Fields{
			"peer": peer,
		}).Warn("gave up delivering encrypted reference offer response to peer")
	}

	return nil, false
}

var eroChallengePrivateKey = []byte{}
var eroChallengePublicKey = []byte{}

func eroChallengeKey() ([]byte, []byte) {
	if len(eroChallengePrivateKey) == 0 {

		curve := ecdh.X25519()
		privateKey, err := curve.GenerateKey(rand.Reader)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("error generating x25519 private key")
		}
		publicKey := privateKey.PublicKey()

		eroChallengePrivateKey = privateKey.Bytes()
		eroChallengePublicKey = publicKey.Bytes()
	}

	return eroChallengePrivateKey, eroChallengePublicKey
}

var referenceOfferChallengeMutex sync.Mutex
var referenceOfferChallengeMap = map[string][]byte{}
var referenceOfferChallengeTime = map[string]int64{}
var peerUserKeys = map[string][]byte{}
var peerUserKeyMutex sync.Mutex

var lastERORTime = map[string]int64{}
var lastERORTimeMutex sync.Mutex

func (b *Bounce) challengeUnencryptedPeerForReferenceOffer(peer string) {
	challenge := make([]byte, 32)
	rand.Read(challenge)

	referenceOfferChallengeMutex.Lock()
	referenceOfferChallengeMap[peer] = challenge
	referenceOfferChallengeTime[peer] = time.Now().Unix()
	referenceOfferChallengeMutex.Unlock()

	_, pubkey := eroChallengeKey()

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
	challengePrivateKeyBytes, _ := eroChallengeKey()
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
	decryptedChallenge, err := dekGCM.Open(nil, []byte{}, eroc.Response, nil)
	if err == nil && bytes.Equal(challenge, decryptedChallenge) {
		peerUserKeyMutex.Lock()
		peerUserKeys[peer] = eroc.PublicKey
		peerUserKeyMutex.Unlock()

		ero := b.getEncryptedReferenceOfferFor(peer, eroc.PublicKey)

		// If this public key belongs to our authorized user, then also include anything we have for their old keys
		if bytes.Equal(eroc.PublicKey, au.PublicKey) {
			var oldKeys []oldKey
			err = b.database.Where("authorized_user_id = ?", au.ID).Find(&oldKeys).Error
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Fatal("database error getting all old keys")
			}

			for _, pastKey := range oldKeys {
				okEro := b.getEncryptedReferenceOfferFor(peer, pastKey.PublicKey)
				if okEro != nil {
					ero.References = append(ero.References, okEro.References...)
				}
			}
		}

		go func() {
			success := false
			for range 5 {
				b.sendDirect(peer, ero)

				time.Sleep(10 * time.Second)

				var dr deliveryRecord
				err := b.referenceDatabase.Where("destination = ? AND frame_id = ? AND frame_type = ?", peer, ero.ID, typeReferenceOffer).First(&dr).Error
				if err == nil {
					success = true
					break
				}
			}
			if !success {
				log.WithFields(log.Fields{
					"peer": peer,
				}).Warn("gave up delivering reference offer to peer")
			}
		}()

		b.makeNextEncryptedChunkRequests()
	} else {
		log.WithFields(log.Fields{
			"peer": peer,
		}).Error("received encrypted reference offer response with invalid signature")
	}

	return nil, false
}

const actionTypeChangeManagementKey = 0
const actionTypePruneDrafts = 1

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

	validSignature := ed25519.Verify(au.SigningKey, med.Action, med.Signature)
	if !validSignature {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("manage encrypted device has invalid signature with authorized user key")
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
	case actionTypePruneDrafts:
		draftPayload := string(edma.Data)
		if len(draftPayload) > 0 {
			drafts := strings.Split(draftPayload, ",")

			err = b.database.Where("type = ? AND id NOT IN (?)", typeDraft, drafts).Delete(&encryptedFrame{}).Error
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Error("database error pruning drafts")
			}
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

func (b *Bounce) pruneEncryptedDrafts() {
	currentUser, ok := b.currentUser()
	if !ok {
		log.Warn("attempt to prune drafts from encrypted device when no profile exists")
		return
	}
	if time.Now().Before(startupTime.Add(5 * time.Minute)) {
		return
	}

	var esds []encryptedSyncDevice
	err := b.database.Find(&esds).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error finding all encrypted sync devices")
	}

	draftIDs := []string{}
	draftMutex.Lock()
	for _, d := range draftCache {
		draftIDs = append(draftIDs, d.ID.String())
	}
	draftMutex.Unlock()
	draftsPayload := strings.Join(draftIDs, ",")

	for _, esd := range esds {
		rd := b.getRemoteDevice(esd.Address)
		if rd.connectedSockets.Load() > 0 {
			edma := encryptedDeviceManagementAction{
				ActionType: actionTypePruneDrafts,
				Data:       []byte(draftsPayload),
			}
			encoded, err := msgpack.Marshal(&edma)
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Error("error encoding encrypted device management action")
				return
			}
			signature := ed25519.Sign(currentUser.PrivateECDSAKey, encoded)

			med := manageEncryptedDevice{
				ID:        uuid.New(),
				Action:    encoded,
				Signature: signature,
			}

			b.sendDirect(esd.Address, &med)
		}
	}
}
