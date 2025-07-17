package db

import (
	"context"
	"time"

	"fmt"
	"log"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func InsertPeer(db *pgxpool.Pool, peerID, name, IP string) (uuid.UUID, error) {
	var id uuid.UUID
	ctx := context.Background()

	query := `
		INSERT INTO peers(peer_id, name, ip_address, is_online)
		VALUES ($1, $2, $3, true)
		RETURNING id;
	`
	err := db.QueryRow(ctx, query, peerID, name, IP).Scan(&id)
	return id, err
}


func MarkPeerOffline(db *pgxpool.Pool, peerID string, lastSeen time.Time) error {
	ctx := context.Background()
	query := `
		UPDATE peers
		SET is_online = false,
		    last_seen = $2
		WHERE peer_id = $1;
	`
	_, err := db.Exec(ctx, query, peerID, lastSeen)
	return err
}


func RegisterPeerAddresses(db *pgxpool.Pool, peerID string, name string, multiaddrs []string) error {
	ctx := context.Background()

	query := `
		INSERT INTO peers(peer_id, name, multiaddrs, is_online, last_seen)
		VALUES ($1, $2, $3, TRUE, NOW())

		ON CONFLICT (peer_id) DO UPDATE SET
			multiaddrs = $3,
			is_online = TRUE,
			last_seen = NOW();
	`

	_, err := db.Exec(ctx, query, peerID, name, multiaddrs)
	if err != nil {
		return fmt.Errorf("failed to register peer Address : %w", err)
	}

	return nil
}


func LookupPeerAddress(db *pgxpool.Pool, peerID string) ([]string, error) {
	ctx := context.Background()
	var multiaddrs []string
	
	query := `
		SELECT multiaddrs
		FROM peers
		WHERE peer_id = $1 and is_online = TRUE
	`

	err := db.QueryRow(ctx, query, peerID).Scan(&multiaddrs)
	if err != nil {
		return nil, fmt.Errorf("failed to lookup peer address for %s: %w", peerID, err)
	}

	return multiaddrs, nil
}


func CleanOldPeerAddresses(db *pgxpool.Pool, olderThan time.Duration) (error) {
	ctx := context.Background()
	threshold := time.Now().Add(-olderThan)

	query := `
		UPDATE peers
		SET is_online = FALSE,
			last_seen = $1
		WHERE is_online = TRUE;
	`

	result,  err := db.Exec(ctx, query, threshold)
	if err != nil {
		return fmt.Errorf("failed to clean old peer addresses : %w", err)
	}

	log.Printf("Cleaned %d old peer entries.", result.RowsAffected())
	return nil
}


func AddFile(db *pgxpool.Pool, fileHash, filename string, fileSize int64, peerID string) error {
	ctx := context.Background()
	_, err := db.Exec(ctx, `
		INSERT INTO files (file_hash, filename, file_size)
		VALUES ($1, $2, $3)
		ON CONFLICT (file_hash) DO NOTHING
	`, fileHash, filename, fileSize)

	if err != nil {
		log.Printf("Error inserting file: %v\n", err)
		return err
	}

	fmt.Printf("File %s added to database\n", filename)

	fileUUID, err := GetFileUUIDByHash(db, fileHash)
	if err != nil {
		log.Printf("Couldn't get file UUID: %v", err)
		return err
	}

	err = AddPeerFile(db, fmt.Sprintf("%v", peerID), fileUUID)
	if err != nil {
		log.Printf("Failed to link peer to file: %v", err)
	}
	return nil
}


func AddPeerFile(db *pgxpool.Pool, peerID, fileUUID string) error {
	ctx := context.Background()
	query := `
		INSERT INTO peer_files(peer_id, file_id)
		VALUES ($1, $2)
		ON CONFLICT (peer_id, file_id) DO NOTHING;
	`
	_, err := db.Exec(ctx, query, peerID, fileUUID)
	if err != nil {
		log.Printf("Error updating peer_id for file_id %s: %v\n", fileUUID, err)
		return err
	}
	fmt.Printf("Updated peer_id to %s for file %s\n", peerID, fileUUID)
	return nil
}


func ListAvailableFiles(db *pgxpool.Pool) {
	ctx := context.Background()
	rows, err := db.Query(ctx, `
		SELECT filename, file_hash, file_size FROM files
	`)
	if err != nil {
		log.Printf("Failed to fetch available files: %v", err)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var filename, fileHash string
		var fileSize int64
		err := rows.Scan(&filename, &fileHash, &fileSize)
		if err != nil {
			log.Printf("Error scanning row: %v", err)
			continue
		}
		fmt.Printf("%s (%d bytes)\nHash:  %s\n\n", filename, fileSize, fileHash)
	}
}


func GetPeerUUIDByID(db *pgxpool.Pool, peerID string) (string, error) {
	ctx := context.Background()
	var uuid string
	// idStr := fmt.Sprintf(peerID)
	err := db.QueryRow(ctx, `SELECT id FROM peers WHERE peer_id = $1`, peerID).Scan(&uuid)
	if err != nil {
		log.Printf("Failed to fetch UUID for peer_id %s: %v\n", peerID, err)
		return "", err
	}
	return uuid, nil
}


func GetFileUUIDByHash(db *pgxpool.Pool, fileHash string) (string, error) {
	ctx := context.Background()
	var fileUUID string
	err := db.QueryRow(ctx, `SELECT id FROM files WHERE file_hash = $1`, fileHash).Scan(&fileUUID)
	if err != nil {
		log.Printf("Failed to fetch file UUID for hash %s: %v\n", fileHash, err)
		return "", err
	}
	return fileUUID, nil
}


type PeerInfo struct {
	PeerID string
	Name   string
	Online bool
}

func ListPeers(db *pgxpool.Pool) ([]PeerInfo, error) {
	ctx := context.Background()
	var peers []PeerInfo
	query := `
        SELECT peer_id, name, is_online
        FROM peers
        ORDER BY last_seen DESC;
    `
	rows, err := db.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query peers: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var p PeerInfo
		if err := rows.Scan(&p.PeerID, &p.Name, &p.Online); err != nil {
			return nil, fmt.Errorf("failed to scan peer row: %w", err)
		}
		peers = append(peers, p)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error after scanning peer rows: %w", err)
	}

	return peers, nil
}