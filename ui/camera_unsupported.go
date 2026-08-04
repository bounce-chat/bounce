//go:build !linux && !windows

package ui

import (
	"image"

	log "github.com/sirupsen/logrus"
)

func cameraSupported() bool {
	return false
}

func capturePhoto() (image.Image, error) {
	log.Fatal("unsupported camera device")
	return nil, nil
}

func startCameraPreview() {
	log.Fatal("unsupported camera device")
}

func stopCameraPreview() {
	log.Fatal("unsupported camera device")
}

func cameraPreview() chan image.Image {
	log.Fatal("unsupported camera device")
	return nil
}
