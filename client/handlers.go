package client

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net"

	"github.com/DillonEnge/matchmaker/message"
)

type udpHandlerFunc func(*Client, message.MessageWithDecodedPayload) error

func handleAckPayload(c *Client, m message.MessageWithDecodedPayload) error {
	var d message.AckPayload
	err := json.Unmarshal(m.Payload.Data, &d)
	if err != nil {
		err = fmt.Errorf("error unmarshalling payload data: %w", err)
		return err
	}

	ackCh, ok := c.ackChannels.Value(int32(d.SequenceNumber))
	if !ok {
		err = fmt.Errorf("no ack channel found for received ack: %w", err)
		return err
	}

	ackCh <- d

	return nil
}

func handlePeerUDPEndpointPayload(c *Client, m message.MessageWithDecodedPayload) error {
	var d message.PeerUDPEndpointPayload
	err := json.Unmarshal(m.Payload.Data, &d)
	if err != nil {
		err = fmt.Errorf("error unmarshalling payload data: %w", err)
		return err
	}

	sendAck(c, c.udpConn, int16(m.SequenceNumber))

	lAddr, err := net.ResolveUDPAddr("udp", c.udpConn.LocalAddr().String())
	if err != nil {
		err = fmt.Errorf("failed to resolve local UDPAddr: %w", err)
		return err
	}

	peerAddrs := []*net.UDPAddr{
		lAddr,
	}

	for _, addr := range d.PeerUDPEndpoints {
		rAddr, err := net.ResolveUDPAddr("udp", addr)
		if err != nil {
			if c.debug {
				slog.Error("failed to resolve remote UDPAddr from peer udp endpoint", "err", err)
			}
			continue
		}

		peerAddrs = append(peerAddrs, rAddr)
	}

	if err := c.udpConn.Close(); err != nil {
		if c.debug {
			slog.Error("error closing udpConn", "err", err)
		}
	}
	c.peerAddrsCh <- peerAddrs

	return nil
}
