package chat

import (
	"encoding/binary"
	"errors"
	"io"
	"net"

	log "github.com/sirupsen/logrus"
)

//
// Reader/Writer for a simple Type,Length,Value protocol used by Bounce
//

var headerSize = 6
var typeSize = 2
var maxPayloadSize = intPow(2, (headerSize-typeSize)*8)

//
// Read a Bounce frame from the socket.  This will return the type of frame, the frame bytes, and any error
//
func readFrame(conn net.Conn) (uint16, []byte, error) {
	header := make([]byte, 0)
	headerRead := 0
	for headerRead < headerSize {
		buf := make([]byte, headerSize)
		n, err := conn.Read(buf)
		headerRead += n
		if err == io.EOF {
			if headerRead != headerSize {
				return 0, []byte{}, err
			}
		} else if err != nil {
			return 0, []byte{}, err

		}
		header = append(header, buf[:n]...)
	} // TODO: maybe break header reading out so the engine can drop connections with unknown frame types before reading the whole thing?

	typeBytes := header[:typeSize]
	sizeBytes := header[typeSize:]

	typeInt := binary.BigEndian.Uint16(typeBytes)
	payloadSize := binary.BigEndian.Uint32(sizeBytes)

	payload := make([]byte, 0)
	payloadRead := uint32(0)
	for payloadRead < payloadSize {
		buf := make([]byte, payloadSize-payloadRead)
		n, err := conn.Read(buf)
		payloadRead += uint32(n)
		if err == io.EOF {
			if payloadRead != payloadSize {
				return 0, []byte{}, err
			}
		} else if err != nil {
			return 0, []byte{}, err
		}
		payload = append(payload, buf[:n]...)
	}

	return typeInt, payload, nil
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
		n, err := conn.Write(frame[bytesWritten:])
		if err != nil {
			return err
		}
		bytesWritten += n
	}
	return nil
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
