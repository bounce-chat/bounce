package network

import (
	"net"
	"os"
	"context"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/cretz/bine/tor"
	"github.com/ipsn/go-libtor"
)

var TorDataDirectory = "/home/hayden/.bounce/tor"

type TorNetwork struct {
}

func NewTorNetwork() *TorNetwork {
	err := os.MkdirAll(TorDataDirectory, 0700)
	if err != nil {
		log.Fatal(err.Error())
	}

	bounceTor := &TorNetwork{}

	return bounceTor
}

func (bounceTor *TorNetwork) Start() error {
	log.Info("Starting and registering onion service, please wait a bit...")
	t, err := tor.Start(
		nil,
		&tor.StartConf{
			DataDir: TorDataDirectory,
			ProcessCreator: libtor.Creator,
			DebugWriter: os.Stderr,
		},
	)
	if err != nil {
		log.Fatal("Failed to start tor: %v", err)
	}
	defer t.Close()

	// Wait at most a few minutes to publish the service
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	// Create an onion service to listen on any port but show as 80
	onion, err := t.Listen(ctx, &tor.ListenConf{RemotePorts: []int{80}})
	if err != nil {
		log.Panicf("Failed to create onion service: %v", err)
	}
	defer onion.Close()

	log.Info("Listening on " + onion.ID + ".onion")
	return nil
}

func (bounceTor *TorNetwork) Connect(address BounceAddress) (*net.Conn, error) {
	return nil, nil
}

func (bounceTor *TorNetwork) VerifySignature(address BounceAddress, data []byte) error {
	return nil
}
