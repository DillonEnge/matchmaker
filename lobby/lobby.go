package lobby

import (
	"sync"

	"github.com/DillonEnge/matchmaker/peer"
	"github.com/DillonEnge/matchmaker/safety"
)

type Lobby struct {
	peers *safety.SafeMap[string, *peer.Peer]
	mu    sync.RWMutex
}

func NewLobby(peers ...*peer.Peer) *Lobby {
	sm := safety.NewSafeMap[string, *peer.Peer]()

	for _, p := range peers {
		sm.Set(p.SessionID, p)
	}

	return &Lobby{
		peers: sm,
	}
}

func (l *Lobby) Join(peer *peer.Peer) {
	l.peers.Set(peer.SessionID, peer)
}

func (l *Lobby) Peers() *safety.SafeMap[string, *peer.Peer] {
	return l.peers
}
