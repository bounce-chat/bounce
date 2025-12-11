package chat

import (
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
	ID        uuid.UUID
	PublicKey []byte
	Manager   bool
}

func StartEncryptedDevice(network Network, configDirectory string) {
	if os.Getenv("DEBUG") == "true" {
		log.SetReportCaller(true)
	}
	log.SetLevel(log.InfoLevel) // TODO: put behind the envar when ready.  run in warn otherwise?

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
	Secret string
	Pubkey []byte
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
			ID:        uuid.New(),
			PublicKey: edmr.Pubkey,
			Manager:   true,
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
		Pubkey: currentUser.PublicECDSAKey,
		Secret: secret,
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
	ID           uuid.UUID `gorm:"type:uuid;primary_key;" msgpack:"-"`
	FrameID      uuid.UUID `msgpack:"-"`
	PublicKey    []byte
	EncryptedDEK []byte
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

func (ef encryptedFrame) getType() uint16 {
	return typeEncryptedFrame
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
	} else {
		log.WithFields(log.Fields{
			"peer": peer,
		}).Warn("ignoring encrypted frame that did not include an authorized user as a recipient")
	}

	b.markFrameDelivered(ef.ID, ef.Type, peer)
	go b.sendAck(peer, ef.Type, ef.ID)

	return nil, false
}
