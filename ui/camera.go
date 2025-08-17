package ui

import "errors"

/*
import (
	"errors"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/sensor"
	"github.com/makiuchi-d/gozxing"

	zxingqr "github.com/makiuchi-d/gozxing/qrcode"

	log "github.com/sirupsen/logrus"
)

func (ui *ui) scanQR() (string, error) {
	cameraDevice, ok := fyne.CurrentDevice().(sensor.CameraDevice)
	if !ok {
		log.Error("camera is unsupported on this device")
		return "", nil
	}

	img, captured, err := cameraDevice.CapturePhoto()
	if !captured {
		return "", nil
	}
	if err != nil {
		return "", err
	}

	bmp, err := gozxing.NewBinaryBitmapFromImage(img)
	if err != nil {
		ui.showDialog(dialog.NewError(errors.New("error creating binary bitmap: "+err.Error()), ui.window), nil)
		return "", err
	}

	qrReader := zxingqr.NewQRCodeReader()
	result, err := qrReader.Decode(bmp, nil)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error scanning for QR code in image")
		ui.showDialog(dialog.NewError(errors.New("Unable to find QR code in image"), ui.window), nil)
		return "", err
	}

	return result.GetText(), nil
}
*/

func (ui *ui) scanQR() (string, error) {
	return "", errors.New("QR scanning not yet merged")
}
