package tracker

import (
	"time"
	"torrentium/db"
)


type Tracker struct {
	peers map[string]bool
}


func NewTracker() *Tracker {
	return &Tracker{
		peers: make(map[string]bool),
	}
}

func (t *Tracker) AddPeer(peerID, name, ip string) error {
	t.peers[peerID] = true

	_, err := db.InsertPeer(db.DB, peerID, name, ip)
	return err
}


func (t *Tracker) RemovePeer(peerID string) {
	delete(t.peers, peerID)
	_ = db.MarkPeerOffline(db.DB, peerID, time.Now())
}


func (t *Tracker) ListPeers() []string {
	var list []string
	for peer := range t.peers {
		list = append(list, peer)
	}
	return list
}
