package network

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"io"
	"io/ioutil"
	"net"
	"os"
	"time"

	"github.com/hkparker/bounce/chat"

	"github.com/cretz/bine/tor"
	"github.com/cretz/bine/torutil"
	"github.com/ipsn/go-libtor"
	log "github.com/sirupsen/logrus"
)

var handshakeChallengeSize = 32
var signatureSize = 64

type TorNetwork struct {
	directory  string
	onion      *tor.OnionService
	publicKey  ed25519.PublicKey
	privateKey ed25519.PrivateKey
}

func (bounceTor *TorNetwork) LoadConfig(configDirectory string) {
	bounceTor.directory = configDirectory + "/tor"

	// Create the config directory if needed
	err := os.MkdirAll(bounceTor.directory, 0700)
	if err != nil {
		log.WithFields(log.Fields{
			"at":    "network.TorNetwork.Start",
			"error": err.Error(),
		}).Fatal("error creating tor config directory")
	}

	// Load or create the keypair for the hidden service
	pubkey, privkey := bounceTor.hiddenServiceKey()
	bounceTor.publicKey = pubkey
	bounceTor.privateKey = privkey
}

func (bounceTor *TorNetwork) hiddenServiceKey() (ed25519.PublicKey, ed25519.PrivateKey) {
	// Create the config directory if needed
	hiddenServiceKeyDirectory := bounceTor.directory + "/hidden_service_keys"
	err := os.MkdirAll(hiddenServiceKeyDirectory, 0700)
	if err != nil {
		log.WithFields(log.Fields{
			"at":    "network.TorNetwork.hiddenServiceKey",
			"error": err.Error(),
		}).Fatal("error creating hidden service key directory")
	}

	// Check for keys on disk, return them if they exist
	privateKeyFile := hiddenServiceKeyDirectory + "/private_key"
	publicKeyFile := hiddenServiceKeyDirectory + "/public_key"
	privateKeyBytes, err := ioutil.ReadFile(privateKeyFile)
	if err != nil {
		log.WithFields(log.Fields{
			"path": privateKeyFile,
		}).Info("no hidden service private key found, generating new key pair")
	} else {
		publicKeyBytes, err := ioutil.ReadFile(privateKeyFile)
		if err != nil {
			log.WithFields(log.Fields{
				"at":    "network.TorNetwork.hiddenServiceKey",
				"error": err.Error(),
				"path":  publicKeyFile,
			}).Fatal("private key found but no public key found, something is wrong")
		} else {
			return privateKeyBytes, publicKeyBytes
		}
	}

	// The keys do not exist.  Generate, save, and return them.
	pubkey, privkey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error generating new private key for tor")
	}
	err = ioutil.WriteFile(publicKeyFile, pubkey, 0600)
	if err != nil {
		log.WithFields(log.Fields{
			"at":    "network.TorNetwork.hiddenServiceKey",
			"error": err.Error(),
		}).Fatal("error writing public key")
	}
	err = ioutil.WriteFile(privateKeyFile, privkey, 0600)
	if err != nil {
		log.WithFields(log.Fields{
			"at":    "network.TorNetwork.hiddenServiceKey",
			"error": err.Error(),
		}).Fatal("error writing private key")
	}

	return pubkey, privkey
}

func (bounceTor *TorNetwork) RegisterCallbacks(chat.NetworkCallbacks) {
	// TODO: in theory use this to signal when the network is online / offline.  We'll see if it's needed.
}

func (bounceTor *TorNetwork) Start() error {
	log.WithFields(log.Fields{
		"at": "network.TorNetwork.Start",
	}).Info("connecting to the Tor network")

	t, err := tor.Start(
		nil,
		&tor.StartConf{
			DataDir:        bounceTor.directory,
			ProcessCreator: libtor.Creator,
			DebugWriter:    os.Stderr, // TODO: logrus
		},
	)
	if err != nil {
		log.WithFields(log.Fields{
			"at":    "network.TorNetwork.Start",
			"error": err.Error(),
		}).Fatal("failed to start TOR")
		// TODO: detect the type of error and decide if it's fatal or
		// if we can try again
	}

	// Wait at most a few minutes to publish the service
	ctx, _ := context.WithTimeout(context.Background(), 3*time.Minute) // TODO: assign cancel variable and let UI close Tor early, or defer it

	// Create an onion service to listen on any port but show as 80
	onion, err := t.Listen(
		ctx,
		&tor.ListenConf{
			Version3:    true,
			Key:         bounceTor.privateKey,
			RemotePorts: []int{80},
		},
	)
	if err != nil {
		log.WithFields(log.Fields{
			"at":    "network.TorNetwork.Start",
			"error": err.Error(),
		}).Fatal("failed to create TOR hidden service")
	}
	bounceTor.onion = onion

	log.WithFields(log.Fields{
		"at":      "network.TorNetwork.Start",
		"address": onion.ID + ".onion",
	}).Info("registered hidden service")
	return nil
}

func (bounceTor *TorNetwork) Address() (string, error) { // TODO: never return error, fatal if can't get address?  probably not.
	if bounceTor.onion == nil {
		return "", errors.New("network is not online, cannot determine device address")
	}
	return bounceTor.onion.ID, nil
}

func (bounceTor *TorNetwork) Accept() (net.Conn, error) {
	connection, err := bounceTor.onion.Accept()
	if err != nil {
		return nil, err
	}

	// Handshake with the connection to learn the remote address
	challenge := make([]byte, handshakeChallengeSize)
	n, err := rand.Read(challenge)
	if n != handshakeChallengeSize {
		return nil, errors.New("failed to generate random challenge for handshake")
	}
	if err != nil {
		return nil, err
	}
	err = write(connection, challenge)
	if err != nil {
		return nil, err
	}

	// All onion IDs will be the same size, read the number of bytes that correspond to our ID
	peerAddress, err := read(connection, len(bounceTor.onion.ID))
	if err != nil {
		return nil, err
	}

	// Read their signature of the challenge
	response, err := read(connection, signatureSize)
	if err != nil {
		return nil, err
	}

	ok := bounceTor.VerifySignature(string(peerAddress), challenge, response)
	if !ok {
		return nil, errors.New("signature validation failed during handshake")
	}

	localAddress, err := bounceTor.Address()
	if err != nil {
		return nil, err
	}
	torConn := &torNetworkConnection{
		underlying: connection,
		localAddress: &torAddress{
			address: localAddress,
		},
		remoteAddress: &torAddress{
			address: string(peerAddress),
		},
	}
	return torConn, nil
}

func (bounceTor *TorNetwork) Dial(address string) (net.Conn, error) {
	dialer, err := bounceTor.onion.Tor.Dialer(context.TODO(), &tor.DialConf{}) // TODO: store this so it doesn't need to be recreated all the time?
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error creating dialer")
	}

	localAddress, err := bounceTor.Address()
	if err != nil {
		return nil, err
	}

	conn, err := dialer.Dial("tcp", address+".onion:80")
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
			address: localAddress,
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
	return ed25519.Verify(ed25519.PublicKey(publicKey), data, signature)
}

func (bounceTor *TorNetwork) Shutdown() {
	// Stop the hidden service
	if bounceTor.onion == nil {
		// Network never fully started and we're already closing the app
		log.WithFields(log.Fields{
			"at": "network.TorNetwork.Shutdown",
		}).Warn("stopping tor before tor has fully started")
	} else {
		err := bounceTor.onion.Close()
		if err != nil {
			log.WithFields(log.Fields{
				"at":    "network.TorNetwork.Shutdown",
				"error": err.Error(),
			}).Error("error stopping hidden service")
		}
		// Stop Tor
		err = bounceTor.onion.Tor.Close()
		if err != nil {
			log.WithFields(log.Fields{
				"at":    "network.TorNetwork.Shutdown",
				"error": err.Error(),
			}).Error("error stopping tor")
		}
	}
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
// Writing primatives for the handshake
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
