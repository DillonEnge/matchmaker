package peer

import "net"

type Peer struct {
	SessionID string
	TCPConn   net.Conn
	UDPConn   *net.UDPConn
	UDPAddr   net.Addr
}
