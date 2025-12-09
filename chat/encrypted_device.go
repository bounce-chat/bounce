package chat

import (
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"github.com/vmihailenco/msgpack/v5"
)

var wantToManage = map[string]bool{}
var wantToManageMutex sync.Mutex
var setupKey string
var encryptedDeviceManagementMutex sync.Mutex

type authorizedUser struct {
	ID      uuid.UUID
	Pubkey  []byte
	Manager bool
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
			ID:      uuid.New(),
			Pubkey:  edmr.Pubkey,
			Manager: true,
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

type encryptedSend struct {
	Frame        []byte
	Client       []byte
	Signature    []byte
	payload      []byte
	payloadMutex sync.Mutex
}

func (es encryptedSend) getType() uint16 {
	return typeEncryptedSend
}

func (es encryptedSend) getPayload() []byte {
	es.payloadMutex.Lock()
	defer es.payloadMutex.Unlock()

	if len(es.payload) == 0 {
		bytes, err := msgpack.Marshal(&es)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("cannot msgpack marshal encrypted send")
		}
		es.payload = bytes
	}
	return es.payload
	return []byte{}
}

func (b *Bounce) handleEncryptedSend(peer string, payload []byte, catchUp bool) (broadcastable, bool) {
	return nil, false
}
