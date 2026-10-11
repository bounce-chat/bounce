package chat

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"time"

	log "github.com/sirupsen/logrus"
)

var headerSize = 6

// payloadPreallocation is how much of a frame's payload buffer is reserved up front.  Most frames are far smaller
// than this, so they cost a single allocation; anything larger grows as its bytes actually arrive, so a peer that
// declares a large frame and then sends little or nothing cannot make us allocate for what it never sent.
const payloadPreallocation = 64 * 1024

var typeSize = 2
var maxPayloadSize = intPow(2, (headerSize-typeSize)*8)

var maximumPayloadFromKnownDevice = maxPayloadSize
var maximumPayloadFromUnknownDevice = 1024 * 1024

var errUnknownDeviceFrameTooLarge = errors.New("refusing to read frame from unknown device that is too large")
var errKnownDeviceFrameTooLarge = errors.New("refusing to read frame from device that is too large")
var errChunkFrameTooLarge = errors.New("chunk payload declared size larger than maximum allowed chunk size")

// Read a Bounce frame from the socket.  This will return the type of frame, the frame bytes, and any error
func readFrame(conn net.Conn, deviceIsKnown bool) (uint16, []byte, error) {
	header := make([]byte, headerSize)
	_, err := io.ReadFull(conn, header)
	if err != nil {
		return 0, []byte{}, err
	}

	typeBytes := header[:typeSize]
	sizeBytes := header[typeSize:]

	typeInt := binary.BigEndian.Uint16(typeBytes)
	payloadSize := binary.BigEndian.Uint32(sizeBytes)

	if typeInt == typeChunk && payloadSize > fileChunkSize {
		log.WithFields(log.Fields{
			"peer": conn.RemoteAddr().String(),
			"size": payloadSize,
		}).Warn("ignoring chunk that is larger than chunk size")
		return 0, []byte{}, errChunkFrameTooLarge
	}

	if deviceIsKnown {
		if int(payloadSize) > maximumPayloadFromKnownDevice {
			log.WithFields(log.Fields{
				"peer": conn.RemoteAddr().String(),
				"size": payloadSize,
			}).Warn(errKnownDeviceFrameTooLarge.Error())
			conn.Close()
			return 0, []byte{}, errKnownDeviceFrameTooLarge
		}
	} else {
		if int(payloadSize) > maximumPayloadFromUnknownDevice {
			log.WithFields(log.Fields{
				"peer": conn.RemoteAddr().String(),
				"size": payloadSize,
			}).Warn(errUnknownDeviceFrameTooLarge.Error())
			conn.Close()
			return 0, []byte{}, errUnknownDeviceFrameTooLarge
		}
	}

	// Read exactly payloadSize bytes.  The buffer grows with the bytes received rather than being sized to the
	// declared length, and io.CopyN reports io.EOF if the stream ends before the whole payload arrived.
	var payload bytes.Buffer
	payload.Grow(int(min(payloadSize, uint32(payloadPreallocation))))
	_, err = io.CopyN(&payload, conn, int64(payloadSize))
	if err != nil {
		return 0, []byte{}, err
	}

	return typeInt, payload.Bytes(), nil
}

func writeFrame(conn net.Conn, frameType uint16, payload []byte) error {
	frame := make([]byte, 0)

	typeBytes := make([]byte, typeSize)
	binary.BigEndian.PutUint16(typeBytes, frameType)
	frame = append(frame, typeBytes...)

	lengthSpace := headerSize - typeSize
	length := len(payload)
	if length > maxPayloadSize {
		log.WithFields(log.Fields{
			"frame_type":   frameType,
			"payload_size": length,
			"maximum_size": maxPayloadSize,
		}).Error("attempted to send message that does not fit in frame")
		return errors.New("payload cannot fit in frame")
	}
	lengthBytes := make([]byte, lengthSpace)
	binary.BigEndian.PutUint32(lengthBytes, uint32(length))
	frame = append(frame, lengthBytes...)

	frame = append(frame, payload...)

	bytesToWrite := headerSize + length
	bytesWritten := 0
	for bytesWritten < bytesToWrite {
		conn.SetWriteDeadline(writeDeadline(bytesToWrite))
		n, err := conn.Write(frame[bytesWritten:])
		if err != nil {
			return err
		}
		bytesWritten += n
	}
	return nil
}

func writeDeadline(size int) time.Time {
	// Writes should finish in 60s, + allowing 4KiB/s for chunks and larger frames
	return time.Now().Add(time.Duration(60*time.Second) + time.Duration(size/4096)*time.Second)
}

// https://stackoverflow.com/a/66429580
func intPow(n, m int) int {
	if m == 0 {
		return 1
	}
	result := n
	for i := 2; i <= m; i++ {
		result *= n
	}
	return result
}
