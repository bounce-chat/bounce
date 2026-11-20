//go:build !linux && !windows

package fyneui

import (
	"image"

	log "github.com/sirupsen/logrus"
)

func cameraSupported() bool {
	return false
}

func startCameraPreview() <-chan image.Image {
	log.Fatal("unsupported camera device")
	return nil
}

func stopCameraPreview() {
	log.Fatal("unsupported camera device")
}
