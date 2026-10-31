//go:build linux || windows

package ui

import (
	"bytes"
	"context"
	"image"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/svanichkin/gocam"
)

func cameraSupported() bool {
	return true
}

var cameraCancelFunc context.CancelFunc

func capturePhoto() (image.Image, error) {
	frame, err := gocam.CaptureSingleFrame(context.Background(), time.Duration(5*time.Second))
	if err != nil {
		return nil, err
	}

	img, _, err := image.Decode(bytes.NewReader(frame.Data))
	if err != nil {
		return nil, err
	}

	return img, nil
}

// startCameraPreview opens the camera and returns its preview frames.  The channel is closed when the camera
// stops, by stopCameraPreview or because the stream could not be opened, so a frame processor ranging over it exits
// on its own, and a processor left over from an earlier preview can never receive frames from a later one.
func startCameraPreview() <-chan image.Image {
	preview := make(chan image.Image)
	ctx, cancel := context.WithCancel(context.Background())
	cameraCancelFunc = cancel

	frames, err := gocam.StartStream(ctx)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error starting camera stream")
		cancel()
		close(preview)
		return preview
	}

	go func() {
		defer close(preview)
		for frame := range frames {
			img, err := decodeInterleaved444(frame.Data, frame.Width, frame.Height)
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Error("error decoding camera frame")
				continue
			}
			// A frame nobody is waiting for is dropped so the preview never falls behind the camera
			select {
			case preview <- img:
			default:
			}
		}
	}()
	return preview
}

func stopCameraPreview() {
	if cameraCancelFunc != nil {
		cameraCancelFunc()
		cameraCancelFunc = nil
	}
}

func decodeInterleaved444(rawBytes []byte, width, height int) (image.Image, error) {
	rect := image.Rect(0, 0, width, height)
	img := image.NewYCbCr(rect, image.YCbCrSubsampleRatio444)

	for i := 0; i < width*height; i++ {
		img.Y[i] = rawBytes[i*3]
		img.Cb[i] = rawBytes[i*3+1]
		img.Cr[i] = rawBytes[i*3+2]
	}
	return img, nil
}
