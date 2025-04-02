package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync/atomic"
	"time"

	"github.com/DillonEnge/matchmaker/message"
	"github.com/DillonEnge/matchmaker/safety"
)

type Client struct {
	addr           string
	tcpConn        *net.TCPConn
	udpConn        *net.UDPConn
	udpHandlers    *safety.SafeMap[message.PayloadType, udpHandlerFunc]
	sequenceNumber atomic.Int32
	logger         *slog.Logger
	debug          bool
	sessionID      string
	peerAddrsCh    chan []*net.UDPAddr
	ackChannels    *safety.SafeMap[int32, chan message.AckPayload]
}

func NewClient(serverAddr string, opts ...func(*Client)) *Client {
	uhm := map[message.PayloadType]udpHandlerFunc{
		message.PAYLOAD_TYPE_ACK:               handleAckPayload,
		message.PAYLOAD_TYPE_PEER_UDP_ENDPOINT: handlePeerUDPEndpointPayload,
		message.PAYLOAD_TYPE_KEEP_ALIVE:        func(*Client, message.MessageWithDecodedPayload) error { return nil },
	}
	uhsm := safety.NewSafeMap(uhm)
	c := &Client{
		addr:        serverAddr,
		ackChannels: safety.NewSafeMap[int32, chan message.AckPayload](),
		udpHandlers: uhsm,
		peerAddrsCh: make(chan []*net.UDPAddr),
	}

	for _, opt := range opts {
		opt(c)
	}

	if c.logger == nil {
		c.logger = slog.Default()
	}

	return c
}

func WithLogger(logger *slog.Logger) func(*Client) {
	return func(c *Client) {
		c.logger = logger
	}
}

func WithDebug(debug bool) func(*Client) {
	return func(c *Client) {
		c.debug = true
	}
}

func (c *Client) seqNumber() int32 {
	c.sequenceNumber.Add(1)

	return c.sequenceNumber.Load()
}

func (c *Client) Wait(ctx context.Context) (l *net.UDPConn, peers []*net.UDPAddr, err error) {
	select {
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	case addrs := <-c.peerAddrsCh:
		laddr := addrs[0]
		l, err := net.ListenUDP("udp", laddr)
		if err != nil {
			return nil, nil, err
		}
		return l, addrs[1:], nil
	}
}

func (c *Client) Close() (tcpError, udpError error) {
	tcpErr := c.tcpConn.Close()
	udpErr := c.udpConn.Close()

	return tcpErr, udpErr
}

func (c *Client) CreateLobby() (lobbyCode string, err error) {
	if c.sessionID == "" {
		return "", fmt.Errorf("no session id found. you must call NewClient before attempting to join a lobby")
	}

	req := message.Request{
		Type: message.REQUEST_CREATE_LOBBY,
	}
	req.Body, err = json.Marshal(message.CreateLobbyBody{
		SessionID: c.sessionID,
	})
	if err != nil {
		c.logger.Error("failed to encode join lobby body to request", "err", err)
		return
	}

	err = json.NewEncoder(c.tcpConn).Encode(req)
	if err != nil {
		c.logger.Error("failed to encode create lobby request to conn", "err", err)
		return
	}

	var resp message.CreateLobbyResponse
	err = json.NewDecoder(c.tcpConn).Decode(&resp)
	if err != nil {
		c.logger.Error("failed to decode join lobby response to conn", "err", err)
		return
	}

	return resp.LobbyCode, nil
}

func (c *Client) JoinLobby(lobbyCode string) error {
	if c.sessionID == "" {
		return fmt.Errorf("no session id found. you must call NewClient before attempting to join a lobby")
	}

	var err error

	req := message.Request{
		Type: message.REQUEST_JOIN_LOBBY,
	}
	req.Body, err = json.Marshal(message.JoinLobbyBody{
		LobbyCode: lobbyCode,
		SessionID: c.sessionID,
	})
	if err != nil {
		err = fmt.Errorf("failed to encode join lobby body to request: %w", err)
		return err
	}

	err = json.NewEncoder(c.tcpConn).Encode(req)
	if err != nil {
		err = fmt.Errorf("failed to encode join lobby request to conn: %w", err)
		return err
	}

	var resp message.JoinLobbyResponse
	err = json.NewDecoder(c.tcpConn).Decode(&resp)
	if err != nil {
		err = fmt.Errorf("failed to decode join lobby response to conn: %w", err)
		return err
	}

	return nil
}

func (c *Client) Connect() error {
	if err := c.dial(); err != nil {
		return err
	}

	go c.listenUDP()

	err := c.newClient()
	if err != nil {
		return err
	}

	return nil
}

func (c *Client) dial() error {
	tcpAddr, err := net.ResolveTCPAddr("tcp", c.addr)
	if err != nil {
		return err
	}

	tcpConn, err := net.DialTCP("tcp", nil, tcpAddr)
	if err != nil {
		return err
	}

	c.tcpConn = tcpConn

	udpAddr, err := net.ResolveUDPAddr("udp", c.addr)
	if err != nil {
		return err
	}

	udpConn, err := net.DialUDP("udp", nil, udpAddr)
	if err != nil {
		return err
	}

	c.udpConn = udpConn

	go keepAlive(c, c.udpConn)

	return nil
}

func (c *Client) listenUDP() error {
	for {
		b := make([]byte, 1024)
		_, _, err := c.udpConn.ReadFrom(b)
		if err != nil {
			if c.debug {
				c.logger.Error("error reading from packet conn", "err", err)
			}

			if errors.Is(err, net.ErrClosed) {
				return err
			}
			continue
		}
		var m message.Message
		err = m.BinaryDecode(bytes.NewReader(b))
		if err != nil {
			if c.debug {
				c.logger.Error("error decoding binary message", "err", err)
			}
			continue
		}

		var p message.Payload
		err = json.Unmarshal(m.Payload, &p)
		if err != nil {
			if c.debug {
				c.logger.Error("failed to unmarshal message payload", "err", err)
			}
			continue
		}

		if c.debug {
			c.logger.Info("received binary payload", "raw", m.Payload, "type", message.PayloadType(p.Type).String())
		}

		mp := message.MessageWithDecodedPayload{
			MessageHeader: m.MessageHeader,
			Payload:       p,
		}

		handle, ok := c.udpHandlers.Value(message.PayloadType(p.Type))
		if !ok {
			if c.debug {
				c.logger.Error("no handler found")
			}
			continue
		}

		if err = handle(c, mp); err != nil {
			if c.debug {
				c.logger.Error("error during udp handler func", "err", err)
			}
			continue
		}
	}

}

func (c *Client) newClient() error {
	req := message.Request{
		Type: message.REQUEST_NEW_CLIENT,
	}
	err := json.NewEncoder(c.tcpConn).Encode(req)
	if err != nil {
		return err
	}

	var resp message.NewClientResponse
	err = json.NewDecoder(c.tcpConn).Decode(&resp)
	if err != nil {
		return err
	}

	c.sessionID = resp.SessionID

	m := &message.Message{
		MessageHeader: message.MessageHeader{
			SequenceNumber: c.seqNumber(),
		},
	}
	p := message.Payload{
		Type: int(message.PAYLOAD_TYPE_BROADCAST_UDP_ENDPOINT),
	}
	p.Data, err = json.Marshal(message.BroadcastUDPEndpointPayload{
		SessionID: resp.SessionID,
	})
	if err != nil {
		return err
	}
	err = m.SetPayload(&p)
	if err != nil {
		return err
	}

	go func() {
		err := transmitMessage(c, c.udpConn, m)
		if err != nil {
			if c.debug {
				c.logger.Error("failed to transmit message", "err", err)
			}
		}
	}()

	return nil
}

func (c *Client) BroadcastUDPEndpoint() error {
	var err error

	m := &message.Message{
		MessageHeader: message.MessageHeader{
			SequenceNumber: c.seqNumber(),
		},
	}
	p := message.Payload{
		Type: int(message.PAYLOAD_TYPE_BROADCAST_UDP_ENDPOINT),
	}
	p.Data, err = json.Marshal(message.BroadcastUDPEndpointPayload{
		SessionID: c.sessionID,
	})
	if err != nil {
		return err
	}

	err = m.SetPayload(&p)
	if err != nil {
		return err
	}

	go func() {
		err := transmitMessage(c, c.udpConn, m)
		if err != nil {
			if c.debug {
				c.logger.Error("failed to transmit message", "err", err)
			}
		}
	}()

	return nil
}

func (c *Client) StartLobby(lobbyCode string) error {
	if c.sessionID == "" {
		return fmt.Errorf("no session id found. you must call NewClient before attempting to start a lobby")
	}

	var err error

	req := message.Request{
		Type: message.REQUEST_START_LOBBY,
	}
	req.Body, err = json.Marshal(message.StartLobbyBody{
		LobbyCode: lobbyCode,
		SessionID: c.sessionID,
	})
	if err != nil {
		err = fmt.Errorf("failed to marshal start lobby body to request: %w", err)
		return err
	}

	err = json.NewEncoder(c.tcpConn).Encode(req)
	if err != nil {
		err = fmt.Errorf("failed to encode start lobby request to conn: %w", err)
		return err
	}

	var resp message.StartLobbyResponse
	err = json.NewDecoder(c.tcpConn).Decode(&resp)
	if err != nil {
		err = fmt.Errorf("failed to decode start lobby response from conn: %w", err)
		return err
	}

	return nil
}

func keepAlive(c *Client, conn *net.UDPConn) {
	ticker := time.NewTicker(500 * time.Millisecond)
	for {
		select {
		case <-ticker.C:
			m := &message.Message{}
			m.SetPayload(&message.Payload{
				Type: int(message.PAYLOAD_TYPE_KEEP_ALIVE),
				Data: []byte{},
			})

			b, err := m.BinaryEncode(1024)
			if err != nil {
				if c.debug {
					c.logger.Error("failed to encode message to binary", "err", err)
				}
				continue
			}

			_, err = conn.Write(b)
			if err != nil {
				if errors.Is(err, net.ErrClosed) {
					if c.debug {
						c.logger.Error("failed to write to a closed conn", "err", err)
					}
					return
				}
				if c.debug {
					c.logger.Error("failed to send keep alive", "err", err)
				}
				continue
			}
		}
	}
}

func transmitMessage(c *Client, conn *net.UDPConn, m *message.Message) error {
	ackCh := make(chan message.AckPayload)
	c.ackChannels.Set(int32(m.SequenceNumber), ackCh)

	d, err := m.BinaryEncode(1024)
	if err != nil {
		err = fmt.Errorf("failed to binary encode message: %w", err)
		return err
	}
	t := time.Now()
	_, err = conn.Write(d)
	if err != nil {
		err = fmt.Errorf("failed to transmit message: %w", err)
		return err
	}

	retryCount := 0
	maxRetryCount := 10

	for {
		if retryCount >= maxRetryCount {
			if c.debug {
				c.logger.Warn("retryCount exceeded maxRetryCount. ceasing transmission")
			}
			break
		}
		select {
		case <-time.After(time.Second * 5):
			_, err := conn.Write(d)
			if err != nil {
				err = fmt.Errorf("failed to transmit message: %w", err)
				return err
			}
		case a := <-ackCh:
			rtt := time.Since(t)
			if c.debug {
				c.logger.Info("processing ack", "ack", a, "rtt", rtt)
			}
			if a.SequenceNumber == int(m.SequenceNumber) {
				if c.debug {
					c.logger.Info("packet acknowledged, ceasing retransmission", "sequenceNumber", m.SequenceNumber)
				}
				return nil
			}
		}
	}

	return nil
}

func sendAck(c *Client, conn *net.UDPConn, sequenceNumber int16) error {
	p := &message.Payload{
		Type: int(message.PAYLOAD_TYPE_ACK),
	}

	a := &message.AckPayload{
		SequenceNumber: int(sequenceNumber),
		Timestamp:      time.Now().UnixMicro(),
	}

	if c.debug {
		c.logger.Info("sending ack", "ack", a)
	}

	var err error
	p.Data, err = json.Marshal(a)
	if err != nil {
		return err
	}

	m := &message.Message{}

	err = m.SetPayload(p)
	if err != nil {
		return err
	}

	d, err := m.BinaryEncode(1024)
	if err != nil {
		return err
	}

	_, err = conn.Write(d)
	if err != nil {
		return err
	}

	return nil
}
