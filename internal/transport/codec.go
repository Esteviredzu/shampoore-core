// FILE: internal/transport/codec.go
package transport

import (
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

const (
	envelopeVersion = 1

	kindData = 1
	kindACK  = 2

	headerSize = 67
	maxChunks  = 65535
)

var envelopeMagic = [4]byte{'R', 'S', 'B', '2'}

type DecodedPacket struct {
	Kind            string
	SessionID       string
	SenderSessionID string
	Sequence        int
	ChunkIndex      int
	TotalChunks     int
	TransmissionID  string
	Data            []byte
}

type ReliableCodec struct {
	chunkSize int
}

func NewReliableCodec(chunkSize int) (*ReliableCodec, error) {
	if chunkSize <= 0 {
		return nil, errors.New("chunkSize must be positive")
	}
	return &ReliableCodec{chunkSize: chunkSize}, nil
}

func (c *ReliableCodec) ChunkSize() int {
	return c.chunkSize
}

func (c *ReliableCodec) Encode(sessionID string, sequence int, data []byte) ([][]byte, error) {
	sessionUUID, err := parseSessionID(sessionID)
	if err != nil {
		return nil, fmt.Errorf("invalid session_id: %w", err)
	}
	if sequence < 0 || sequence > 0xFFFFFFFF {
		return nil, fmt.Errorf("sequence out of range: %d", sequence)
	}

	var chunks [][]byte
	for off := 0; off < len(data); off += c.chunkSize {
		end := off + c.chunkSize
		if end > len(data) {
			end = len(data)
		}
		chunks = append(chunks, data[off:end])
	}
	if len(chunks) == 0 {
		chunks = [][]byte{{}}
	}
	if len(chunks) > maxChunks {
		return nil, fmt.Errorf("too many chunks: %d", len(chunks))
	}

	tid := uuid.New()

	out := make([][]byte, 0, len(chunks))
	for i, chunk := range chunks {
		buf := make([]byte, headerSize+len(chunk))
		copy(buf[0:4], envelopeMagic[:])
		buf[4] = envelopeVersion
		buf[5] = kindData
		buf[6] = 0
		copy(buf[7:23], sessionUUID[:])
		binary.BigEndian.PutUint32(buf[39:43], uint32(sequence))
		binary.BigEndian.PutUint16(buf[43:45], uint16(i))
		binary.BigEndian.PutUint16(buf[45:47], uint16(len(chunks)))
		copy(buf[47:63], tid[:])
		binary.BigEndian.PutUint32(buf[63:67], uint32(len(chunk)))
		copy(buf[67:], chunk)
		out = append(out, buf)
	}
	return out, nil
}

func (c *ReliableCodec) EncodeACK(targetSessionID, senderSessionID string, sequence int) ([]byte, error) {
	target, err := parseSessionID(targetSessionID)
	if err != nil {
		return nil, fmt.Errorf("invalid target session_id: %w", err)
	}
	sender, err := parseSessionID(senderSessionID)
	if err != nil {
		return nil, fmt.Errorf("invalid sender session_id: %w", err)
	}
	if sequence < 0 || sequence > 0xFFFFFFFF {
		return nil, fmt.Errorf("sequence out of range: %d", sequence)
	}

	tid := uuid.New()
	buf := make([]byte, headerSize)
	copy(buf[0:4], envelopeMagic[:])
	buf[4] = envelopeVersion
	buf[5] = kindACK
	buf[6] = 0
	copy(buf[7:23], target[:])
	copy(buf[23:39], sender[:])
	binary.BigEndian.PutUint32(buf[39:43], uint32(sequence))
	copy(buf[47:63], tid[:])
	return buf, nil
}

func (c *ReliableCodec) Decode(buf []byte) *DecodedPacket {
	if len(buf) < headerSize {
		return nil
	}
	if buf[0] != envelopeMagic[0] ||
		buf[1] != envelopeMagic[1] ||
		buf[2] != envelopeMagic[2] ||
		buf[3] != envelopeMagic[3] {
		return nil
	}
	if buf[4] != envelopeVersion {
		return nil
	}

	kind := buf[5]

	var sessionUUID uuid.UUID
	copy(sessionUUID[:], buf[7:23])

	sequence := int(binary.BigEndian.Uint32(buf[39:43]))
	chunkIndex := int(binary.BigEndian.Uint16(buf[43:45]))
	totalChunks := int(binary.BigEndian.Uint16(buf[45:47]))

	var tid uuid.UUID
	copy(tid[:], buf[47:63])

	payloadLen := int(binary.BigEndian.Uint32(buf[63:67]))

	// payloadLen может быть МЕНЬШЕ len(buf)-headerSize, если вышележащий
	// слой (например, SquaresPipe) дополнил сообщение padding'ом.
	// Больше — не может, это битый пакет.
	if payloadLen < 0 || payloadLen > len(buf)-headerSize {
		return nil
	}

	if kind == kindACK {
		var senderUUID uuid.UUID
		copy(senderUUID[:], buf[23:39])
		return &DecodedPacket{
			Kind:            "ack",
			SessionID:       sessionUUID.String(),
			SenderSessionID: senderUUID.String(),
			Sequence:        sequence,
			TransmissionID:  tid.String(),
		}
	}

	if kind != kindData {
		return nil
	}

	if totalChunks <= 0 || totalChunks > maxChunks {
		return nil
	}
	if chunkIndex < 0 || chunkIndex >= totalChunks {
		return nil
	}
	if payloadLen > c.chunkSize {
		return nil
	}

	payload := make([]byte, payloadLen)
	copy(payload, buf[headerSize:headerSize+payloadLen])

	return &DecodedPacket{
		Kind:           "data",
		SessionID:      sessionUUID.String(),
		Sequence:       sequence,
		ChunkIndex:     chunkIndex,
		TotalChunks:    totalChunks,
		TransmissionID: tid.String(),
		Data:           payload,
	}
}

func parseSessionID(s string) (uuid.UUID, error) {
	return uuid.Parse(s)
}

func DeriveChunkSize(maxMessageSize int) (int, error) {
	const envelopeOverhead = 400
	usable := maxMessageSize - envelopeOverhead
	if usable <= 0 {
		return 0, fmt.Errorf(
			"pipe.MaxMessageSize=%d too small for envelope",
			maxMessageSize,
		)
	}
	chunkSize := (usable * 3) / 4
	if chunkSize < 512 {
		chunkSize = 512
	}
	return chunkSize, nil
}