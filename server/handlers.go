package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net"
	"time"

	"github.com/DillonEnge/matchmaker/lobby"
	"github.com/DillonEnge/matchmaker/message"
	"github.com/DillonEnge/matchmaker/peer"
	"github.com/google/uuid"
)

type messageHandlerFunc func(*Server, *net.UDPConn, messageWithAddr, message.Payload) error
type requestHandlerFunc func(*Server, net.Conn, message.Request) error

func handleKeepAlive(s *Server, conn *net.UDPConn, m messageWithAddr, p message.Payload) error {
	s.logger.Info("keep-alive received")
	return nil
}

func handleBroadcastUDPEndpoint(s *Server, conn *net.UDPConn, m messageWithAddr, p message.Payload) error {
	var d message.BroadcastUDPEndpointPayload
	err := json.Unmarshal(p.Data, &d)
	if err != nil {
		s.logger.Error("error unmarshalling payload data", "err", err)
		return err
	}

	peer, ok := s.peers.Value(d.SessionID)
	if !ok {
		s.logger.Error("no peer found", "sessionID", d.SessionID)
		return err
	}

	if peer.UDPAddr == nil {
		peer.UDPAddr = m.SenderAddr
		peer.UDPConn = conn
	}

	slog.Info("storing udp conn", "rAddr", peer.UDPAddr)

	if err = s.sendAck(conn, m.SenderAddr, m.Message.SequenceNumber); err != nil {
		s.logger.Error("failed to send ack", "sessionID", d.SessionID, "sequenceNumber", m.Message.SequenceNumber)
		return err
	}

	return nil
}

func handleAckPayload(s *Server, conn *net.UDPConn, m messageWithAddr, p message.Payload) error {
	var d message.AckPayload
	err := json.Unmarshal(p.Data, &d)
	if err != nil {
		s.logger.Error("error unmarshalling payload data", "err", err)
		return err
	}

	ackCh, ok := s.ackChannels.Value(int32(d.SequenceNumber))
	if !ok {
		s.logger.Error("no ack channel found for received ack")
		return fmt.Errorf("no ack channel found for received ack")
	}

	ackCh <- d

	return nil
}

func handleNewClientRequest(s *Server, conn net.Conn, req message.Request) error {
	id, err := uuid.NewRandom()
	if err != nil {
		s.logger.Error("failed to create new uuid", "err", err)
		return err
	}

	err = json.NewEncoder(conn).Encode(message.NewClientResponse{
		SessionID: id.String(),
	})
	if err != nil {
		s.logger.Error("failed to send new client response", "err", err)
		return err
	}

	s.peers.Set(id.String(), &peer.Peer{
		SessionID: id.String(),
		TCPConn:   conn,
	})

	return nil
}

func handleCreateLobbyRequest(s *Server, conn net.Conn, req message.Request) error {
	var body message.CreateLobbyBody
	err := json.NewDecoder(bytes.NewReader(req.Body)).Decode(&body)
	if err != nil {
		s.logger.Error("failed to decode create lobby request body", "err", err)
		return err
	}

	peer, ok := s.peers.Value(body.SessionID)
	if !ok {
		s.logger.Error("peer not found to create lobby with", "err", err, "sessionID", body.SessionID)
		return err
	}

	lobbyCode := getNCharString(4)

	l := lobby.NewLobby(peer)

	s.lobbies.Set(lobbyCode, l)

	err = json.NewEncoder(conn).Encode(message.CreateLobbyResponse{
		LobbyCode: lobbyCode,
	})
	if err != nil {
		s.logger.Error("failed to encode create lobby resp to conn", "err", err)
		return err
	}

	return nil
}

func handleJoinLobbyRequest(s *Server, conn net.Conn, req message.Request) error {
	var body message.JoinLobbyBody
	err := json.NewDecoder(bytes.NewReader(req.Body)).Decode(&body)
	if err != nil {
		s.logger.Error("failed to decode join lobby request body", "err", err)
		return err
	}

	p, ok := s.peers.Value(body.SessionID)
	if !ok {
		s.logger.Error("peer not found", "sessionID", body.SessionID)
		return err
	}

	l, ok := s.lobbies.Value(body.LobbyCode)
	if !ok {
		s.logger.Error("lobby not found", "err", err, "lobbyCode", body.LobbyCode)
		return err
	}

	l.Join(p)

	err = json.NewEncoder(conn).Encode(message.JoinLobbyResponse{})

	return nil
}

func handleStartLobbyRequest(s *Server, conn net.Conn, req message.Request) error {
	var body message.StartLobbyBody
	err := json.NewDecoder(bytes.NewReader(req.Body)).Decode(&body)
	if err != nil {
		s.logger.Error("failed to decode start lobby request body", "err", err)
		return err
	}

	_, ok := s.peers.Value(body.SessionID)
	if !ok {
		s.logger.Error("peer not found", "sessionID", body.SessionID)
		return err
	}

	l, ok := s.lobbies.Value(body.LobbyCode)
	if !ok {
		s.logger.Error("lobby not found", "err", err, "lobbyCode", body.LobbyCode)
		return err
	}

	err = json.NewEncoder(conn).Encode(message.StartLobbyResponse{})
	if err != nil {
		s.logger.Error("failed to encode resp to conn", "err", err)
		return err
	}

	ps := l.Peers()

	peers := []*peer.Peer{}

	for kv := range ps.Range() {
		peers = append(peers, kv.Val)
	}

	for _, v := range peers {
		pp := message.PeerUDPEndpointPayload{
			PeerUDPEndpoints: make([]string, 0),
		}
		for _, v2 := range peers {
			if v.SessionID == v2.SessionID {
				continue
			}

			pp.PeerUDPEndpoints = append(pp.PeerUDPEndpoints, v2.UDPAddr.String())
		}

		s.seqNumber.Add(1)

		m := message.Message{
			MessageHeader: message.MessageHeader{
				SequenceNumber: s.seqNumber.Load(),
			},
		}

		p := message.Payload{
			Type: int(message.PAYLOAD_TYPE_PEER_UDP_ENDPOINT),
		}
		p.Data, err = json.Marshal(pp)
		if err != nil {
			s.logger.Error("failed to marshal peer udp endpoint payload to payload data", "err", err)
			continue
		}

		err = m.SetPayload(&p)
		if err != nil {
			slog.Error("error setting peer udp endpoint payload on message", "err", err)
			continue
		}

		if v.UDPConn == nil {
			slog.Error("UDPConn nil value", "sessionID", v.SessionID)
			continue
		}

		s.transmitMessage(v.UDPConn, v.UDPAddr, &m)

		// err = v.UDPConn.Close()
		// if err != nil {
		// 	s.logger.Error("failed to close peer udpConn", "sessionID", v.SessionID, "err", err)
		// 	continue
		// }

		//TODO iterate through peers to send keep-alive packets so that this line causes the transmission to cease
		s.peers.Delete(v.SessionID)
	}

	return nil
}

func getNCharString(n int) string {
	b := []byte{}

	for range n {
		num := 97 + rand.IntN(26)

		b = append(b, byte(num))
	}

	return string(b)
}

func keepAlive(s *Server, conn *net.UDPConn) {
	ticker := time.NewTicker(500 * time.Millisecond)
	for {
		select {
		case <-ticker.C:
			for kv := range s.peers.Range() {
				m := &message.Message{}
				m.SetPayload(&message.Payload{
					Type: int(message.PAYLOAD_TYPE_KEEP_ALIVE),
					Data: []byte{},
				})

				b, err := m.BinaryEncode(1024)
				if err != nil {
					s.logger.Error("failed to encode message to binary", "err", err)
					continue
				}

				_, err = conn.WriteTo(b, kv.Val.UDPAddr)
				if err != nil {
					if errors.Is(err, net.ErrClosed) {
						s.logger.Error("failed to write to a closed conn", "err", err)
						return
					}
					s.logger.Error("failed to send keep alive", "err", err)
					continue
				}
			}
		}
	}
}

func (s *Server) transmitMessage(conn *net.UDPConn, rAddr net.Addr, m *message.Message) {
	ackCh := make(chan message.AckPayload)
	s.ackChannels.Set(int32(m.SequenceNumber), ackCh)

	d, err := m.BinaryEncode(1024)
	if err != nil {
		s.logger.Error("failed to binary encode message", "err", err)
		return
	}

	slog.Info("transmitting message", "sequenceNum", m.SequenceNumber)
	t := time.Now()
	_, err = conn.WriteTo(d, rAddr)
	if err != nil {
		s.logger.Error("failed to transmit message", "err", err)
		return
	}

	retryCount := 0
	maxRetryCount := 10

	for {
		if retryCount >= maxRetryCount {
			slog.Warn("retryCount exceeded maxRetryCount. ceasing transmission")
			break
		}

		select {
		case <-time.After(time.Millisecond * 200):
			retryCount++
			_, err := conn.WriteTo(d, rAddr)
			if err != nil {
				s.logger.Error("failed to transmit message", "err", err)
				return
			}
		case a := <-ackCh:
			rtt := time.Since(t)
			s.logger.Info("processing ack", "ack", a, "rtt", rtt)
			if a.SequenceNumber == int(m.SequenceNumber) {
				s.logger.Info("packet acknowledged, ceasing retransmission", "sequenceNumber", m.SequenceNumber)
				return
			}
		}
	}
}
