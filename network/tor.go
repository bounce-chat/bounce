package network

import (
	"context"
	"crypto/rand"
	"errors"
	"io"
	"io/ioutil"
	"net"
	"os"
	"sync"
	"time"

	"github.com/hkparker/bounce/chat"

	"github.com/cretz/bine/tor"
	"github.com/cretz/bine/torutil"
	"github.com/cretz/bine/torutil/ed25519"
	"github.com/hkparker/go-libtor"
	log "github.com/sirupsen/logrus"
)

var torLock sync.Mutex
var liveness sync.WaitGroup
var dials sync.WaitGroup

var handshakeChallengeSize = 32
var signatureSize = 64

type TorNetwork struct {
	routerDirectory string
	keyDirectory    string
	onion           *tor.OnionService
	tor             *tor.Tor
	dialer          *tor.Dialer
	callbacks       chat.NetworkCallbacks
	publicKey       ed25519.PublicKey
	privateKey      ed25519.PrivateKey
	online          bool
	shutdown        bool
	shutdownMutex   sync.Mutex
}

func (bounceTor *TorNetwork) loadConfig(configDirectory string) {
	bounceTor.routerDirectory = configDirectory + "/tor/router"
	bounceTor.keyDirectory = configDirectory + "/tor/keys"

	// Create the router directory if needed
	err := os.MkdirAll(bounceTor.routerDirectory, 0700)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error creating tor router directory")
	}

	// Create the key directory if needed
	err = os.MkdirAll(bounceTor.keyDirectory, 0700)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error creating tor key directory")
	}

	// Create an empty torrc file.  If we don't create and specify a file, we leak torrc files with bine
	if _, err := os.Stat(bounceTor.routerDirectory + "/torrc"); os.IsNotExist(err) {
		torrc, err := os.OpenFile(bounceTor.routerDirectory+"/torrc", os.O_RDONLY|os.O_CREATE, 0600)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("error creating torrc file")
		}
		err = torrc.Close()
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("error closing torrc file")
		}
	}

	// Load or create the keypair for the hidden service
	pubkey, privkey := bounceTor.hiddenServiceKey()
	bounceTor.publicKey = pubkey // TODO: needed?  move to the get function?
	bounceTor.privateKey = privkey
}

func (bounceTor *TorNetwork) hiddenServiceKey() (ed25519.PublicKey, ed25519.PrivateKey) { // TODO: reverse the order?
	// Create the config directory if needed
	err := os.MkdirAll(bounceTor.keyDirectory, 0700)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
			"path":  bounceTor.keyDirectory,
		}).Fatal("error creating hidden service key directory")
	}

	// Check for keys on disk, return them if they exist
	privateKeyFile := bounceTor.keyDirectory + "/private_key"
	publicKeyFile := bounceTor.keyDirectory + "/public_key"
	privateKeyBytes, err := ioutil.ReadFile(privateKeyFile)
	if err != nil {
		log.WithFields(log.Fields{
			"path": privateKeyFile,
		}).Info("no hidden service private key found, generating new key pair")
	} else {
		publicKeyBytes, err := ioutil.ReadFile(publicKeyFile)
		if err != nil {
			// We have the private key but the public key is missing.  This is weird, but we can regenerate it.
			pubkey := ed25519.PrivateKey(privateKeyBytes).PublicKey()
			err = ioutil.WriteFile(publicKeyFile, pubkey, 0600)
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Fatal("error writing regenerated public key")
			}
			return pubkey, privateKeyBytes
		} else {
			return publicKeyBytes, privateKeyBytes
		}
	}

	// The keys do not exist.  Generate, save, and return them.
	keypair, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error generating new private key for tor")
	}
	err = ioutil.WriteFile(publicKeyFile, keypair.PublicKey(), 0600)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error writing public key")
	}
	err = ioutil.WriteFile(privateKeyFile, keypair.PrivateKey(), 0600)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error writing private key")
	}

	return keypair.PublicKey(), keypair.PrivateKey()
}

func (bounceTor *TorNetwork) Start(configDirectory string, callbacks chat.NetworkCallbacks) {
	defer func() {
		if r := recover(); r != nil {
			// https://github.com/cretz/bine/issues/57
			log.Fatal("recovered a panic while starting Tor, this happens due to a nil-pointer derefernce in bine when Tor is shut down while publishing a hidden service")
		}
	}()
	bounceTor.loadConfig(configDirectory)
	bounceTor.callbacks = callbacks
	log.Info("connecting to the Tor network")

	var err error
	bounceTor.tor, err = tor.Start(
		nil,
		&tor.StartConf{
			DataDir:        bounceTor.routerDirectory,
			TorrcFile:      bounceTor.routerDirectory + "/torrc",
			ProcessCreator: libtor.Creator,
			DebugWriter:    &torLogger{},
		},
	)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("failed to start TOR")
	}

	// Create an onion service to listen on any port but show as 80
	bounceTor.onion, err = bounceTor.tor.Listen(
		context.Background(),
		&tor.ListenConf{
			Version3:    true,
			Key:         bounceTor.privateKey,
			RemotePorts: []int{80},
		},
	)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("failed to create TOR hidden service")
	}

	// Create a dialer
	bounceTor.dialer, err = bounceTor.tor.Dialer(context.TODO(), &tor.DialConf{})
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error creating dialer")
	}

	log.WithFields(log.Fields{
		"id": bounceTor.onion.ID,
	}).Info("published hidden service")

	bounceTor.updateOnlineStatus()
	ticker := time.NewTicker(10 * time.Second)
	for _ = range ticker.C { // TODO: Close this on shutdown?
		bounceTor.updateOnlineStatus()
	}
}

func (bounceTor *TorNetwork) updateOnlineStatus() {
	torLock.Lock()
	dials.Wait()
	liveness.Add(1)
	torLock.Unlock()
	defer liveness.Done()

	if bounceTor.tor == nil {
		if bounceTor.online {
			// This shoudn't be possible, but let's handle it anyway
			bounceTor.online = false
			bounceTor.callbacks.NetworkOffline()
		}
		return
	}

	// This works, but it's slow to detect network failures.  It might be faster (though more involved) to
	// look at the status of cirtuits with "circuit-status" or "status/circuit-established"
	response, err := bounceTor.tor.Control.GetInfo("network-liveness")
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error calling network-liveness on tor control port")
	} else {
		for _, kv := range response {
			if kv.Key == "network-liveness" {
				if kv.Val == "up" {
					if !bounceTor.online {
						bounceTor.online = true
						bounceTor.callbacks.NetworkOnline()
					}
				} else if kv.Val == "down" {
					if bounceTor.online {
						bounceTor.online = false
						bounceTor.callbacks.NetworkOffline()
					}
				} else {
					log.WithFields(log.Fields{
						"value": kv.Val,
					}).Error("unknown tor network-liveness value")
				}
			}
			log.WithFields(log.Fields{
				"key":   kv.Key,
				"value": kv.Val,
				"empty": kv.ValSetAndEmpty,
			}).Trace("result from network-liveness check on Tor control port")
		}
	}

}

func (bounceTor *TorNetwork) Address() string {
	// If the network is online, get the ID from the service
	if bounceTor.onion != nil {
		return bounceTor.onion.ID
	}

	// TODO: get it off the bounceTor object?

	// If the network is offline, parse the address from the public key on disk
	pubkey, _ := bounceTor.hiddenServiceKey()
	return torutil.OnionServiceIDFromV3PublicKey(pubkey)
}

func (bounceTor *TorNetwork) Accept() (net.Conn, error, bool) {
	connection, err := bounceTor.onion.Accept()
	if err != nil {
		return nil, err, true
	}

	// Handshake with the connection to learn the remote address
	challenge := make([]byte, handshakeChallengeSize)
	n, err := rand.Read(challenge)
	if n != handshakeChallengeSize {
		return nil, errors.New("failed to generate random challenge for handshake"), false
	}
	if err != nil {
		return nil, err, false
	}
	err = write(connection, challenge)
	if err != nil {
		return nil, err, false
	}

	// All onion IDs will be the same size, read the number of bytes that correspond to our ID
	peerAddress, err := read(connection, len(bounceTor.onion.ID)) // TODO: use Address()
	if err != nil {
		return nil, err, false
	}

	// Read their signature of the challenge
	response, err := read(connection, signatureSize)
	if err != nil {
		return nil, err, false
	}

	ok := bounceTor.VerifySignature(string(peerAddress), challenge, response)
	if !ok {
		return nil, errors.New("signature validation failed during handshake"), false
	}

	torConn := &torNetworkConnection{
		underlying: connection,
		localAddress: &torAddress{
			address: bounceTor.onion.ID,
		},
		remoteAddress: &torAddress{
			address: string(peerAddress),
		},
	}
	return torConn, nil, false
}

func (bounceTor *TorNetwork) Dial(address string) (net.Conn, error) {
	torLock.Lock()
	liveness.Wait()
	dials.Add(1)
	torLock.Unlock()
	defer dials.Done()

	if bounceTor.tor == nil || bounceTor.onion == nil {
		// Technically we don't need to wait for the hidden service to be published before we can dial,
		// but any failures to publish indicate a major problem
		return nil, errors.New("cannot dial while network is not started")
	}

	conn, err := bounceTor.dialer.Dial("tcp", address+".onion:80")
	if err != nil {
		return nil, err
	}

	// Handshake
	challenge, err := read(conn, handshakeChallengeSize)
	if err != nil {
		return nil, err
	}
	response := bounceTor.Sign(challenge)

	err = write(conn, []byte(bounceTor.onion.ID))
	if err != nil {
		return nil, err
	}

	err = write(conn, response)
	if err != nil {
		return nil, err
	}

	torConn := &torNetworkConnection{
		underlying: conn,
		localAddress: &torAddress{
			address: bounceTor.onion.ID,
		},
		remoteAddress: &torAddress{
			address: address,
		},
	}
	return torConn, nil
}

func (bounceTor *TorNetwork) Sign(data []byte) []byte {
	return ed25519.Sign(bounceTor.privateKey, data)
}

func (bounceTor *TorNetwork) VerifySignature(address string, data []byte, signature []byte) bool {
	publicKey, err := torutil.PublicKeyFromV3OnionServiceID(address)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("invalid address passed to VerifySignature")
		return false
	}
	return ed25519.Verify(publicKey, data, signature)
}

func (bounceTor *TorNetwork) Shutdown() {
	bounceTor.shutdownMutex.Lock()
	if bounceTor.shutdown {
		// We're already shutting down, there's no need to enter this
		// function again, and doing so can cause segfaults.
		bounceTor.shutdownMutex.Unlock()
		return
	}
	bounceTor.shutdown = true
	bounceTor.shutdownMutex.Unlock()

	log.Info("shutting down tor")
	if bounceTor.onion == nil {
		// Network never fully started and we're already closing the app
		log.Warn("stopping hidden service before hidden service was published")
	} else {
		// Stop the hidden service
		err := bounceTor.onion.Close()
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("error stopping hidden service")
		}
	}
	if bounceTor.tor == nil {
		// Network never fully started and we're already closing the app
		log.Warn("stopping tor before tor has fully started")
	} else {
		// Stop Tor
		err := bounceTor.tor.Close()
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("error stopping tor")
		}
	}
	log.Info("tor stopped")
}

//
// torNetworkConnection is an implementation of net.Conn that allows us to specify the onion ID as the remote and local address
//

type torNetworkConnection struct {
	underlying    net.Conn
	localAddress  net.Addr
	remoteAddress net.Addr
}

func (conn *torNetworkConnection) Read(b []byte) (int, error) {
	return conn.underlying.Read(b)
}

func (conn *torNetworkConnection) Write(b []byte) (int, error) {
	return conn.underlying.Write(b)
}

func (conn *torNetworkConnection) Close() error {
	return conn.underlying.Close()
}

func (conn *torNetworkConnection) LocalAddr() net.Addr {
	return conn.localAddress
}

func (conn *torNetworkConnection) RemoteAddr() net.Addr {
	return conn.remoteAddress
}

func (conn *torNetworkConnection) SetDeadline(t time.Time) error {
	return conn.underlying.SetDeadline(t)
}

func (conn *torNetworkConnection) SetReadDeadline(t time.Time) error {
	return conn.underlying.SetReadDeadline(t)
}

func (conn *torNetworkConnection) SetWriteDeadline(t time.Time) error {
	return conn.underlying.SetWriteDeadline(t)
}

type torAddress struct {
	address string
}

func (ta *torAddress) Network() string {
	return "tor"
}

func (ta *torAddress) String() string {
	return ta.address
}

//
// Wire primatives for the handshake
//

func read(conn net.Conn, size int) ([]byte, error) {
	payload := make([]byte, 0)
	payloadRead := 0
	for payloadRead < size {
		buf := make([]byte, size-payloadRead)
		n, err := conn.Read(buf)
		payloadRead += n
		if err == io.EOF {
			if payloadRead != size {
				return []byte{}, err
			}
		} else if err != nil {
			return []byte{}, err
		}
		payload = append(payload, buf[:n]...)
	}
	return payload, nil
}

func write(conn net.Conn, payload []byte) error {
	bytesToWrite := len(payload)
	bytesWritten := 0
	for bytesWritten < bytesToWrite {
		n, err := conn.Write(payload[bytesWritten:])
		if err != nil {
			return err
		}
		bytesWritten += n
	}
	return nil
}

//
// A custom logger that will send Tor logs to logrus for debug logging
//

type torLogger struct{}

func (_ *torLogger) Write(line []byte) (int, error) {
	log.WithFields(log.Fields{
		"source": "tor",
	}).Debug(string(line))

	return len(line), nil
}
