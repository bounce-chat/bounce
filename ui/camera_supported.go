//go:build linux || windows

package ui

import (
	"bytes"
	"context"
	"image"
	"log"
	"time"

	"github.com/svanichkin/gocam"
)

func cameraSupported() bool {
	return true
}

var previewFrames = make(chan image.Image)
var cameraContext context.Context
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

func startCameraPreview() {
	cameraContext, cameraCancelFunc = context.WithCancel(context.Background())
	frames, err := gocam.StartStream(cameraContext)
	if err != nil {
		log.Printf("error starting camera string: %s\n", err.Error())
	}
	go func() {
		for frame := range frames {
			img, err := decodeInterleaved444(frame.Data, frame.Width, frame.Height)
			if err == nil {
				select {
				case previewFrames <- img:
				default:
					log.Printf("camera preview frame is backed up")
				}
			} else {
				log.Printf("error decoding frame: %s\n", err.Error())
			}
		}
	}()
}

func stopCameraPreview() {
	if cameraCancelFunc != nil {
		cameraCancelFunc()
	}
	for len(previewFrames) > 0 {
		<-previewFrames
	}
}

func cameraPreview() chan image.Image {
	return previewFrames
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
