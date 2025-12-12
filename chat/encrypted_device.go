package chat

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
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
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var wantToManage = map[string]bool{}
var wantToManageMutex sync.Mutex
var setupKey string
var encryptedDeviceManagementMutex sync.Mutex

type authorizedUser struct {
	ID         uuid.UUID
	PublicKey  []byte
	SigningKey []byte
	Manager    bool
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
		&encryptedFrame{},
		&recipient{},
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
	err := b.database.Where("manager = ?", true).First(&au).Error
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
			Manager:    true,
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
	EncryptedDEK     []byte
}

func (r *recipient) BeforeCreate(tx *gorm.DB) error {
	r.ID = uuid.New()
	return nil
}

type encryptedFrame struct {
	ID           uuid.UUID `gorm:"type:uuid;primary_key;"`
	Type         uint16
	Timestamp    int64
	Payload      []byte
	DeleteAt     int64
	Recipients   []recipient
	payload      []byte
	payloadMutex sync.Mutex
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

	if foundAuthorizedUser {
		err = b.database.Create(&ef).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("error saving encrypted frame")
		}
		b.markFrameDelivered(ef.ID, ef.Type, peer)
		go b.sendAck(peer, ef.Type, ef.ID)
	} else {
		log.WithFields(log.Fields{
			"id":         ef.ID,
			"peer":       peer,
			"recipients": len(ef.Recipients),
		}).Warn("ignoring encrypted frame that did not include an authorized user as a recipient")
	}

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
		go b.sendDirect(peer, b.getEncryptedReferenceOfferFor(peer, eroc.PublicKey))
	} else {
		log.WithFields(log.Fields{
			"peer": peer,
		}).Error("received encrypted reference offer response with invalid signature")
	}

	return nil, false
}
