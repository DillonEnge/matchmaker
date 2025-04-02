package message

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
)

type PayloadType int

const (
	PAYLOAD_TYPE_ACK PayloadType = iota
	PAYLOAD_TYPE_BROADCAST_UDP_ENDPOINT
	PAYLOAD_TYPE_KEEP_ALIVE
	PAYLOAD_TYPE_PEER_UDP_ENDPOINT
)

func (p PayloadType) String() string {
	switch int(p) {
	case 0:
		return "ack"
	case 1:
		return "broadcast_udp_endpoint"
	case 2:
		return "keep_alive"
	case 3:
		return "peer_udp_endpoint"
	default:
		return ""
	}
}

type Message struct {
	MessageHeader
	Payload []byte
}

type MessageWithDecodedPayload struct {
	MessageHeader
	Payload Payload
}

type MessageHeader struct {
	SequenceNumber int32
	PayloadLen     int16
	ChannelID      byte
}

type Payload struct {
	Type int
	Data []byte
}

type BroadcastUDPEndpointPayload struct {
	SessionID string
}

type AckPayload struct {
	SequenceNumber int
	Timestamp      int64
}

type PeerUDPEndpointPayload struct {
	PeerUDPEndpoints []string
}

func (m *Message) BinaryEncode(maxMessageSize int) ([]byte, error) {
	m.PayloadLen = int16(len(m.Payload))

	var buf bytes.Buffer
	err := binary.Write(&buf, binary.LittleEndian, m.MessageHeader)
	if err != nil {
		return nil, err
	}

	_, err = buf.Write(m.Payload)
	if err != nil {
		return nil, err
	}

	b := buf.Bytes()
	if len(b) > maxMessageSize {
		return nil, fmt.Errorf("message exceeded maxMessageSize, len(b): %d", len(b))
	}

	return b, nil
}

func (m *Message) BinaryDecode(r io.Reader) error {
	var buf bytes.Buffer
	_, err := buf.ReadFrom(r)
	if err != nil {
		return err
	}

	err = binary.Read(&buf, binary.LittleEndian, &m.MessageHeader)
	if err != nil {
		return err
	}

	m.Payload = make([]byte, m.PayloadLen)
	n, err := buf.Read(m.Payload)
	if err != nil {
		return err
	}

	if m.PayloadLen != int16(n) {
		return fmt.Errorf("read bytes did not match payload length")
	}

	return nil
}

func (m *Message) SetPayload(p *Payload) error {
	var err error
	m.Payload, err = json.Marshal(p)

	return err
}
