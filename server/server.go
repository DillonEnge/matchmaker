package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/DillonEnge/matchmaker/lobby"
	"github.com/DillonEnge/matchmaker/message"
	"github.com/DillonEnge/matchmaker/peer"
	"github.com/DillonEnge/matchmaker/safety"
)

type messageWithAddr struct {
	Message    *message.Message
	SenderAddr net.Addr
}

type Server struct {
	addr        string
	ackChannels *safety.SafeMap[int32, chan message.AckPayload]
	logger      *slog.Logger
	seqNumber   atomic.Int32
	peers       *safety.SafeMap[string, *peer.Peer]
	lobbies     *safety.SafeMap[string, *lobby.Lobby]
	mu          sync.RWMutex
	udpHandlers *safety.SafeMap[message.PayloadType, messageHandlerFunc]
	tcpHandlers *safety.SafeMap[message.RequestType, requestHandlerFunc]
}

func NewServer(addr string, opts ...func(*Server)) *Server {
	udpHandlers := map[message.PayloadType]messageHandlerFunc{
		message.PAYLOAD_TYPE_KEEP_ALIVE:             handleKeepAlive,
		message.PAYLOAD_TYPE_BROADCAST_UDP_ENDPOINT: handleBroadcastUDPEndpoint,
		message.PAYLOAD_TYPE_ACK:                    handleAckPayload,
	}
	tcpHandlers := map[message.RequestType]requestHandlerFunc{
		message.REQUEST_NEW_CLIENT:   handleNewClientRequest,
		message.REQUEST_CREATE_LOBBY: handleCreateLobbyRequest,
		message.REQUEST_JOIN_LOBBY:   handleJoinLobbyRequest,
		message.REQUEST_START_LOBBY:  handleStartLobbyRequest,
	}

	uSM := safety.NewSafeMap(udpHandlers)
	tSM := safety.NewSafeMap(tcpHandlers)

	s := &Server{
		addr:        addr,
		ackChannels: safety.NewSafeMap[int32, chan message.AckPayload](),
		logger:      slog.Default(),
		peers:       safety.NewSafeMap[string, *peer.Peer](),
		lobbies:     safety.NewSafeMap[string, *lobby.Lobby](),
		udpHandlers: uSM,
		tcpHandlers: tSM,
	}

	for _, v := range opts {
		v(s)
	}

	return s
}

func WithLogger(logger *slog.Logger) func(*Server) {
	return func(s *Server) {
		s.logger = logger
	}
}

func (s *Server) ListenAndServe(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	udpAddr, err := net.ResolveUDPAddr("udp", s.addr)
	if err != nil {
		return err
	}
	udpConn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		return err
	}
	go s.handleUDPConn(ctx, udpConn)

	go keepAlive(s, udpConn)

	tcpAddr, err := net.ResolveTCPAddr("tcp", s.addr)
	if err != nil {
		return err
	}

	l, err := net.ListenTCP("tcp", tcpAddr)
	if err != nil {
		return err
	}

	connCh := make(chan net.Conn)
	for {
		go func() {
			conn, err := l.Accept()
			if err != nil {
				s.logger.Error("error accepting conn from listener", "err", err)
			}

			connCh <- conn
		}()

		select {
		case conn := <-connCh:
			go s.handleConn(ctx, conn)
		case <-ctx.Done():
			return ctx.Err()
		}
	}

}

func (s *Server) handleConn(_ context.Context, conn net.Conn) {
	defer conn.Close()
	for {
		var req message.Request
		err := json.NewDecoder(conn).Decode(&req)
		if err != nil {
			if err == io.EOF {
				break
			}
			s.logger.Error("error decoding request from conn", "err", err)
			continue
		}

		s.logger.Info("received request", "req", req, "type", req.Type.String())

		handle, ok := s.tcpHandlers.Value(req.Type)
		if !ok {
			s.logger.Error("handler not found")
			continue
		}

		err = handle(s, conn, req)
		if err != nil {
			s.logger.Error("error handing tcp request", "err", err)
		}
	}
}

func (s *Server) handleUDPConn(ctx context.Context, conn *net.UDPConn) {
	msgCh := make(chan messageWithAddr)
	quitCh := make(chan struct{})
	for {
		go func() {
			p := make([]byte, 1024)
			n, addr, err := conn.ReadFrom(p)
			if err != nil {
				if errors.Is(err, net.ErrClosed) {
					quitCh <- struct{}{}
				}
				s.logger.Error("failed to read from packet conn", "err", err)
				msgCh <- messageWithAddr{}
				return
			}

			m := &message.Message{}
			err = m.BinaryDecode(bytes.NewReader(p[:n]))
			if err != nil {
				s.logger.Error("failed to decode binary message", "err", err)
				msgCh <- messageWithAddr{}
				return
			}

			msgCh <- messageWithAddr{
				Message:    m,
				SenderAddr: addr,
			}
		}()

		select {
		case <-quitCh:
			s.logger.Info("udp conn closed")
			return
		case m := <-msgCh:
			if m.Message == nil {
				continue
			}

			s.logger.Info("received message", "message", m)

			var p message.Payload
			err := json.Unmarshal(m.Message.Payload, &p)
			if err != nil {
				s.logger.Error("failed to unmarshal message payload", "err", err)
				continue
			}

			handle, ok := s.udpHandlers.Value(message.PayloadType(p.Type))
			if !ok {
				s.logger.Error("udp handler not found")
				continue
			}

			s.logger.Info("received udp message", "type", message.PayloadType(p.Type).String())

			err = handle(s, conn, m, p)
			if err != nil {
				s.logger.Error("error handling message", "err", err)
				continue
			}
		case <-ctx.Done():
			return
		}
	}
}

func (s *Server) sendAck(conn *net.UDPConn, addr net.Addr, sequenceNumber int32) error {
	p := &message.Payload{
		Type: int(message.PAYLOAD_TYPE_ACK),
	}

	a := &message.AckPayload{
		SequenceNumber: int(sequenceNumber),
		Timestamp:      time.Now().UnixMicro(),
	}

	s.logger.Info("sending ack", "ack", a)

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

	_, err = conn.WriteTo(d, addr)
	if err != nil {
		return err
	}

	return nil
}
