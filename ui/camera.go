package ui

import "errors"

/*
import (
	"bytes"
	"encoding/base64"
	"errors"
	"image"
	"io"

	"fyne.io/fyne/v2/camera"
	"fyne.io/fyne/v2/dialog"
	"github.com/makiuchi-d/gozxing"
	zxingqr "github.com/makiuchi-d/gozxing/qrcode"
	log "github.com/sirupsen/logrus"
)

func (ui *ui) scanQR() (string, error) {
	reader, err := camera.Open()
	if err != nil {
		ui.showDialog(dialog.NewError(errors.New("error opening camera: "+err.Error()), ui.window), nil)
		return "", err
	}

	encoded, err := io.ReadAll(reader)
	if err != nil {
		ui.showDialog(dialog.NewError(errors.New("error reading camera data: "+err.Error()), ui.window), nil)
		return "", err
	}

	data, err := base64.StdEncoding.DecodeString(string(encoded))
	if err != nil {
		ui.showDialog(dialog.NewError(errors.New("error decoding camera data: "+err.Error()), ui.window), nil)
		return "", err
	}

	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		ui.showDialog(dialog.NewError(errors.New("error decoding image: "+err.Error()), ui.window), nil)
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
