package network

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"io/ioutil"
	"net"
	"os"
	"time"

	"github.com/hkparker/bounce/chat"

	"github.com/cretz/bine/tor"
	"github.com/ipsn/go-libtor"
	log "github.com/sirupsen/logrus"
)

type TorNetwork struct {
	directory  string
	onion      *tor.OnionService // access hidden service address with onion.ID
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
		log.Fatal()
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

func (bounceTor *TorNetwork) RegisterCallbacks(chat.NetworkCallbacks) {}

func (bounceTor *TorNetwork) Start() error {
	log.WithFields(log.Fields{
		"at": "network.TorNetwork.Start",
	}).Info("connecting to the TOR network")

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
	ctx, _ := context.WithTimeout(context.Background(), 3*time.Minute) // TODO: assign cancel variable and let UI close Tor early

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

func (bounceTor *TorNetwork) Address() (string, error) {
	if bounceTor.onion == nil {
		return "", errors.New("network is not online, cannot determine device address")
	}
	return bounceTor.onion.ID + ".onion", nil // TODO: do I need to add .onion for dialing?
}

func (bounceTor *TorNetwork) Accept() net.Conn {
	connection, err := bounceTor.onion.Accept()
	if err != nil {
		// TODO: the network needs to be restarted, either the machine
		// is offline or the Tor node is having problems and should be
		// restarted.  Determine the best choice for how to bring the
		// network back online.
		// TODO: use a waitgroup and recursion to make this always return
		// a good connection, or pass the error up and let the chat engine
		// wait until the network online callback informs it that it's ok
		// to try again?
		// TODO: another thing to consider is that when we are trying
		// to shut down the network, we expect this to return an error
		// so we need to account for that and handle this gracefully
	}
	return connection
}

func (bounceTor *TorNetwork) Dial(address chat.BounceAddress) (*net.Conn, error) {
	return nil, nil
}

func (bounceTor *TorNetwork) VerifySignature(address chat.BounceAddress, data []byte) error {
	return nil
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
