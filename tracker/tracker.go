package tracker

import (
	"log"
	"torrentium/db"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/stdlib" // For converting pgxpool to sql.DB
)

type Tracker struct {
	peers map[string]bool
	repo  *db.Repository
}

func NewTracker() *Tracker {
	// Convert the global pgxpool.Pool to sql.DB
	sqlDB := stdlib.OpenDB(*db.DB.Config().ConnConfig)

	return &Tracker{
		peers: make(map[string]bool),
		repo:  db.NewRepository(sqlDB),
	}
}

func (t *Tracker) AddPeer(peerID, name, ip string) error {
	t.peers[peerID] = true

	_, err := t.repo.UpsertPeer(peerID, name, ip)
	if err != nil {
		log.Printf("Failed to upsert peer %s: %v", peerID, err)
		return err
	}

	log.Printf("Successfully added/updated peer %s", peerID)
	return nil
}

func (t *Tracker) RemovePeer(peerID string) {
	delete(t.peers, peerID)

	err := t.repo.SetPeerOffline(peerID)
	if err != nil {
		log.Printf("Failed to set peer %s offline: %v", peerID, err)
	}
}

func (t *Tracker) ListPeers() []string {
	var list []string
	for peer := range t.peers {
		list = append(list, peer)
	}
	return list
}

// New methods using the consolidated repository

func (t *Tracker) AddFile(fileHash, filename string, fileSize int64, contentType string) (uuid.UUID, error) {
	return t.repo.InsertFile(fileHash, filename, fileSize, contentType)
}

func (t *Tracker) LinkPeerToFile(peerID string, fileID uuid.UUID) (uuid.UUID, error) {
	return t.repo.InsertPeerFile(peerID, fileID)
}

func (t *Tracker) GetOnlinePeersForFile(fileID uuid.UUID) ([]*db.PeerFile, error) {
	return t.repo.FindOnlineFilePeersByID(fileID)
}

func (t *Tracker) GetFileByID(fileID uuid.UUID) (*db.File, error) {
	return t.repo.FindFileByID(fileID)
}

func (t *Tracker) GetAllFiles() ([]db.File, error) {
	return t.repo.FindAllFiles()
}

func (t *Tracker) GetFileByHash(fileHash string) (uuid.UUID, error) {
	return t.repo.GetFileUUIDByHash(fileHash)
}

func (t *Tracker) GetPeerUUID(peerID string) (uuid.UUID, error) {
	return t.repo.GetPeerUUIDByID(peerID)
}

// Active connection methods

func (t *Tracker) CreateSignalingSession(requesterID, providerID, fileID uuid.UUID) (*db.ActiveConnection, error) {
	return t.repo.CreateSignalingSession(requesterID, providerID, fileID)
}

func (t *Tracker) GetSignalingSession(fileID uuid.UUID) (*db.ActiveConnection, error) {
	return t.repo.GetSignalingSession(fileID)
}

func (t *Tracker) UpdateSignalingStatus(fileID uuid.UUID, status string) error {
	return t.repo.UpdateSignalingStatus(fileID, status)
}

func (t *Tracker) CompleteSignalingSession(fileID uuid.UUID) error {
	return t.repo.CompleteSignalingSession(fileID)
}

func (t *Tracker) GetSignalingSessionByRequester(requesterID, fileID uuid.UUID) (*db.ActiveConnection, error) {
	return t.repo.GetSignalingSessionByRequester(requesterID, fileID)
}

func (t *Tracker) GetSignalingSessionByProvider(providerID, fileID uuid.UUID) (*db.ActiveConnection, error) {
	return t.repo.GetSignalingSessionByProvider(providerID, fileID)
}

// Utility methods for backward compatibility

func (t *Tracker) AddFileWithPeer(fileHash, filename string, fileSize int64, peerID string) error {
	// Add file first
	fileID, err := t.repo.InsertFile(fileHash, filename, fileSize, "")
	if err != nil {
		log.Printf("Failed to insert file: %v", err)
		return err
	}

	// Link peer to file
	_, err = t.repo.InsertPeerFile(peerID, fileID)
	if err != nil {
		log.Printf("Failed to link peer to file: %v", err)
		return err
	}

	log.Printf("Successfully added file %s and linked to peer %s", filename, peerID)
	return nil
}
