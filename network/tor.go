package network

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"io/ioutil"
	"net"
	"os"
	"time"

	"github.com/cretz/bine/tor"
	"github.com/ipsn/go-libtor"
	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc"
)

type TorNetwork struct {
	directory  string
	onion      *tor.OnionService // access hidden service address with onion.ID
	publicKey  ed25519.PublicKey
	privateKey ed25519.PrivateKey
}

func NewTorNetwork(configDirectory string) *TorNetwork {
	bounceTor := &TorNetwork{
		directory: configDirectory + "/tor",
	}

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

	return bounceTor
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

func (bounceTor *TorNetwork) Start() error {
	log.WithFields(log.Fields{
		"at": "network.TorNetwork.Start",
	}).Info("connecting to the TOR network")

	t, err := tor.Start(
		nil,
		&tor.StartConf{
			DataDir:        bounceTor.directory,
			ProcessCreator: libtor.Creator,
			DebugWriter:    os.Stderr,
		},
	)
	if err != nil {
		log.WithFields(log.Fields{
			"at":    "network.TorNetwork.Start",
			"error": err.Error(),
		}).Fatal("failed to start TOR")
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

func (bounceTor *TorNetwork) ServeGRPC(grpcServer *grpc.Server) error {
	if bounceTor.onion.LocalListener == nil {
		log.WithFields(log.Fields{
			"at": "network.TorNetwork.ServeGRPC",
		}).Fatal("onion listener is nil, cannot setup gRPC")
	}
	err := grpcServer.Serve(bounceTor.onion)
	//err := grpcServer.Serve(bounceTor.onion.LocalListener) // TODO: should I be passing the lower level listner here instead?
	if err != nil {
		log.WithFields(log.Fields{
			"at":    "network.TorNetwork.ServeGRPC",
			"error": err.Error(),
		}).Error("error serving gRPC server on Tor")
		return err
	}
	return nil
}

func (bounceTor *TorNetwork) Connect(address BounceAddress) (*net.Conn, error) {
	return nil, nil
}

func (bounceTor *TorNetwork) VerifySignature(address BounceAddress, data []byte) error {
	return nil
}

func (bounceTor *TorNetwork) Shutdown() {
	// Stop the hidden service
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
