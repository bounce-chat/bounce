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

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"github.com/vmihailenco/msgpack/v5"
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
	)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error migrating the database")
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
	Payload          []byte
	DeleteAt         int64
	Recipients       []recipient
	DeviceRecipients []deviceRecipient
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

func (ef encryptedFrame) getPayload() []byte {
	ef.payloadMutex.Lock()
	defer ef.payloadMutex.Unlock()

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

func (b *Bounce) handleEncryptedFrame(peer string, payload []byte, catchUp bool) (broadcastable, bool) {
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

	foundAuthorizedUser := false
	for _, r := range ef.Recipients {
		var au authorizedUser
		err := b.database.Take(&au, "public_key = ?", r.PublicKey).Error
		if err == nil {
			foundAuthorizedUser = true
			break
		}
	}

	if !foundAuthorizedUser {
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

	return nil, false
}

type appendRecipient struct {
	ID           uuid.UUID `gorm:"type:uuid;primary_key;"`
	FrameID      uuid.UUID
	PublicKey    []byte
	EncrypterKey []byte
	EncryptedDEK []byte
}

func (ar appendRecipient) getID() uuid.UUID {
	return ar.ID
}

func (ar appendRecipient) getType() uint16 {
	return typeAppendRecipient
}

func (ar appendRecipient) getTimestamp() int64 {
	return 0
}

func (ar appendRecipient) getPayload() []byte {
	bytes, err := msgpack.Marshal(&ar)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("cannot msgpack marshal append recipient")
	}
	return bytes
}

func (b *Bounce) handleAppendRecipient(peer string, payload []byte, catchUp bool) (broadcastable, bool) {
	var ar appendRecipient
	err := msgpack.Unmarshal(payload, &ar)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling append recipient")
		return nil, false
	}

	err = b.database.Clauses(clause.OnConflict{DoNothing: true}).Create(&recipient{
		EncryptedFrameID: ar.FrameID,
		PublicKey:        ar.PublicKey,
		EncrypterKey:     ar.EncrypterKey,
		EncryptedDEK:     ar.EncryptedDEK,
	}).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error saving appended recipient")
	}

	go b.sendAck(peer, typeAppendRecipient, ar.ID)

	return nil, false
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
	ciphertext := dekGCM.Seal(nil, []byte{}, eroc.Challenge, nil)

	go b.sendDirect(peer, &encryptedReferenceOfferResponse{
		PublicKey: currentUser.PublicECDHKey,
		Response:  ciphertext,
	})

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

func (b *Bounce) challengeUnencryptedPeerForReferenceOffer(peer string) {
	challenge := make([]byte, 32)
	rand.Read(challenge)

	referenceOfferChallengeMutex.Lock()
	referenceOfferChallengeMap[peer] = challenge
	referenceOfferChallengeTime[peer] = time.Now().Unix()
	referenceOfferChallengeMutex.Unlock()

	_, pubkey := eroChallengeKey()

	go b.sendDirect(peer, &encryptedReferenceOfferChallenge{
		Key:       pubkey,
		Challenge: challenge,
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
		}).Fatal("cannot msgpack marshal encrypted reference offer challenge")
	}
	return bytes
}

func (b *Bounce) handleEncryptedReferenceOfferResponse(peer string, payload []byte, catchUp bool) (broadcastable, bool) {
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
		referenceOfferChallengeMutex.Lock()
		peerUserKeys[peer] = eroc.PublicKey
		referenceOfferChallengeMutex.Unlock()

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

		go b.sendDirect(peer, ero)
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
		referenceOfferChallengeMutex.Lock()
		for addr, key := range peerUserKeys {
			if bytes.Equal(key, au.PublicKey) {
				peerUserKeys[addr] = ks.PublicECDHKey
			}
		}
		referenceOfferChallengeMutex.Unlock()

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
