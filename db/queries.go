package db

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repository struct that holds all database operations
type Repository struct {
	DB *sql.DB
}

// NewRepository creates a new repository instance
func NewRepository(db *sql.DB) *Repository {
	return &Repository{DB: db}
}

// =======================
// PEER OPERATIONS
// =======================

// UpsertPeer inserts a new peer or updates an existing one on connection
func (r *Repository) UpsertPeer(peerID, name, ip string) (uuid.UUID, error) {
	now := time.Now()
	log.Printf("UpsertPeer called with peerID=%s, name=%s, ip=%s", peerID, name, ip)

	// Try updating an existing peer
	result, err := r.DB.ExecContext(context.Background(),
		`UPDATE peers SET is_online=true, last_seen=$1, ip_address=$2 WHERE peer_id=$3`,
		now, ip, peerID)
	if err != nil {
		log.Printf("UPDATE error: %v", err)
		return uuid.Nil, err
	}

	rows, err := result.RowsAffected()
	if err != nil {
		log.Printf("[Repository] RowsAffected error: %v", err)
		return uuid.Nil, err
	}

	if rows == 0 {
		log.Printf("[Repository] No existing peer, inserting new peer with peerID=%s", peerID)
		tx, err := r.DB.BeginTx(context.Background(), nil)
		if err != nil {
			log.Printf("[Repository] Transaction begin error: %v", err)
			return uuid.Nil, err
		}

		// Insert into peers table and get the generated UUID
		var newPeerUUID uuid.UUID
		err = tx.QueryRowContext(context.Background(),
			`INSERT INTO peers (peer_id, name, ip_address, is_online, last_seen, created_at)
			 VALUES ($1, $2, $3, true, $4, $4)
			 RETURNING id`,
			peerID, name, ip, now).Scan(&newPeerUUID)
		if err != nil {
			log.Printf("[Repository] INSERT into peers error: %v", err)
			tx.Rollback()
			return uuid.Nil, err
		}

		// Insert into trust_scores table using the peer's UUID
		_, err = tx.ExecContext(context.Background(),
			`INSERT INTO trust_scores (id, peer_id, score, successful_transfers, failed_transfers, updated_at) -- Changed 50 to 0.50
     VALUES (uuid_generate_v4(), $1, 0.50, 0, 0, $2)`,
			newPeerUUID, now)
		if err != nil {
			log.Printf("[Repository] INSERT into trust_scores error: %v", err)
			tx.Rollback()
			return uuid.Nil, err
		}

		if err = tx.Commit(); err != nil {
			log.Printf("[Repository] Commit error: %v", err)
			return uuid.Nil, err
		}

		log.Printf("[Repository] Inserted new peer and trust score for peerID=%s with UUID=%s", peerID, newPeerUUID)
		return newPeerUUID, nil
	} else {
		log.Printf("[Repository] Updated existing peer with peerID=%s", peerID)
	}

	// Get the UUID of the updated peer
	var peerUUID uuid.UUID
	err = r.DB.QueryRowContext(context.Background(), `SELECT id FROM peers WHERE peer_id = $1`, peerID).Scan(&peerUUID)
	if err != nil {
		log.Printf("failed to fetch UUID for peer_id %s: %v\n", peerID, err)
		return uuid.Nil, err
	}

	return peerUUID, nil
}

// SetPeerOffline sets a peer's is_online to false and updates last_seen
func (r *Repository) SetPeerOffline(peerID string) error {
	now := time.Now()
	log.Printf("SetPeerOffline called for peerID=%s", peerID)
	_, err := r.DB.ExecContext(context.Background(),
		`UPDATE peers SET is_online=false, last_seen=$1 WHERE peer_id=$2`,
		now, peerID)
	if err != nil {
		log.Printf("[Repository] SetPeerOffline UPDATE error: %v", err)
	}
	return err
}

// GetPeerUUIDByID gets peer UUID by peer_id string
func (r *Repository) GetPeerUUIDByID(peerID string) (uuid.UUID, error) {
	var peerUUID uuid.UUID
	err := r.DB.QueryRowContext(context.Background(), `SELECT id FROM peers WHERE peer_id = $1`, peerID).Scan(&peerUUID)
	if err != nil {
		log.Printf("Failed to fetch UUID for peer_id %s: %v\n", peerID, err)
		return uuid.Nil, err
	}
	return peerUUID, nil
}

// =======================
// FILE OPERATIONS
// =======================

// InsertFile inserts a new file or returns existing file ID
func (r *Repository) InsertFile(fileHash, filename string, fileSize int64, contentType string) (uuid.UUID, error) {
	var existingID uuid.UUID
	err := r.DB.QueryRowContext(context.Background(),
		`SELECT id FROM files WHERE file_hash = $1`, fileHash).Scan(&existingID)

	if err == nil {
		return existingID, nil
	}

	if err != sql.ErrNoRows {
		return uuid.Nil, err
	}

	// Insert new file
	newID := uuid.New()
	now := time.Now()

	query := `INSERT INTO files (id, file_hash, filename, file_size, content_type, created_at)
			  VALUES ($1, $2, $3, $4, $5, $6)`
	_, err = r.DB.ExecContext(context.Background(), query,
		newID, fileHash, filename, fileSize, contentType, now)
	if err != nil {
		return uuid.Nil, err
	}

	return newID, nil
}

// FindFileByID finds a file by its UUID
func (r *Repository) FindFileByID(id uuid.UUID) (*File, error) {
	query := `SELECT id, file_hash, filename, file_size, created_at FROM files WHERE id = $1`
	row := r.DB.QueryRowContext(context.Background(), query, id)
	var file File
	if err := row.Scan(&file.ID, &file.FileHash, &file.Filename, &file.FileSize, &file.CreatedAt); err != nil {
		return nil, err
	}
	return &file, nil
}

// FindAllFiles returns all files in the database
func (r *Repository) FindAllFiles() ([]File, error) {
	query := `SELECT id, file_hash, filename, file_size, created_at FROM files`
	rows, err := r.DB.QueryContext(context.Background(), query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var files []File
	for rows.Next() {
		var file File
		if err := rows.Scan(&file.ID, &file.FileHash, &file.Filename, &file.FileSize, &file.CreatedAt); err != nil {
			return nil, fmt.Errorf("results decode error %s", err.Error())
		}
		files = append(files, file)
	}
	return files, nil
}

// UpdateFileByID updates a file by its ID
func (r *Repository) UpdateFileByID(id uuid.UUID, fileHash, filename string, fileSize int64) (int64, error) {
	query := `UPDATE files SET file_hash=$1, filename=$2, file_size=$3 WHERE id=$4`
	result, err := r.DB.ExecContext(context.Background(), query, fileHash, filename, fileSize, id)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// DeleteFileByID deletes a file by its ID
func (r *Repository) DeleteFileByID(id uuid.UUID) (int64, error) {
	query := `DELETE FROM files WHERE id=$1`
	result, err := r.DB.ExecContext(context.Background(), query, id)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// DeleteAllFiles deletes all files
func (r *Repository) DeleteAllFiles() (int64, error) {
	query := `DELETE FROM files`
	result, err := r.DB.ExecContext(context.Background(), query)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// GetFileUUIDByHash gets file UUID by file hash
func (r *Repository) GetFileUUIDByHash(fileHash string) (uuid.UUID, error) {
	var fileUUID uuid.UUID
	err := r.DB.QueryRowContext(context.Background(), `SELECT id FROM files WHERE file_hash = $1`, fileHash).Scan(&fileUUID)
	if err != nil {
		log.Printf("Failed to fetch file UUID for hash %s: %v\n", fileHash, err)
		return uuid.Nil, err
	}
	return fileUUID, nil
}

// =======================
// PEER-FILE OPERATIONS
// =======================

// InsertPeerFile links a peer to a file
func (r *Repository) InsertPeerFile(peerID string, fileID uuid.UUID) (uuid.UUID, error) {
	var peerUUID uuid.UUID
	err := r.DB.QueryRowContext(context.Background(),
		`SELECT id FROM peers WHERE peer_id = $1`, peerID).Scan(&peerUUID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("failed to find peer with peer_id=%s: %w", peerID, err)
	}

	// Generate new UUID for peer_file entry
	newID := uuid.New()
	createdAt := time.Now()

	// Insert into peer_files with conflict handling
	query := `
		INSERT INTO peer_files (id, peer_id, file_id, announced_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (peer_id, file_id) DO NOTHING
	`
	_, err = r.DB.ExecContext(context.Background(), query,
		newID, peerUUID, fileID, createdAt)
	if err != nil {
		return uuid.Nil, err
	}

	return newID, nil
}

// FindOnlineFilePeersByID finds all online peers that have a specific file
func (r *Repository) FindOnlineFilePeersByID(fileID uuid.UUID) ([]*PeerFile, error) {
	log.Printf("looking for peers with file_id: %s", fileID)

	query := `
		SELECT pf.id, pf.file_id, pf.peer_id, pf.announced_at, 
		       COALESCE(ts.score, 50.0) as score
		FROM peer_files pf
		JOIN peers p ON pf.peer_id = p.id
		LEFT JOIN trust_scores ts ON p.id = ts.peer_id
		WHERE pf.file_id = $1 AND p.is_online = true
	`

	rows, err := r.DB.QueryContext(context.Background(), query, fileID)
	if err != nil {
		log.Printf("Main query error: %v", err)
		return nil, err
	}
	defer rows.Close()

	var peers []*PeerFile
	for rows.Next() {
		var pfile PeerFile
		if err := rows.Scan(&pfile.ID, &pfile.FileID, &pfile.PeerID, &pfile.AnnouncedAt, &pfile.Score); err != nil {
			log.Printf("Row scan error: %v", err)
			return nil, err
		}
		log.Printf("Found peer: ID=%s, FileID=%s, PeerID=%s, Score=%f",
			pfile.ID, pfile.FileID, pfile.PeerID, pfile.Score)
		peers = append(peers, &pfile)
	}

	if err := rows.Err(); err != nil {
		log.Printf("Rows error: %v", err)
		return nil, err
	}

	log.Printf("Total peers found: %d", len(peers))
	return peers, nil
}

// =======================
// ACTIVE CONNECTION OPERATIONS
// =======================

// CreateSignalingSession creates a new signaling session in active_connections
func (r *Repository) CreateSignalingSession(requesterID, providerID, fileID uuid.UUID) (*ActiveConnection, error) {
	newID := uuid.New()
	now := time.Now()

	conn := &ActiveConnection{
		ID:          newID,
		RequesterID: requesterID,
		ProviderID:  providerID,
		FileID:      fileID,
		Status:      "pending",
		StartedAt:   now,
	}

	query := `
		INSERT INTO active_connections (id, requester_id, provider_id, file_id, status, started_at)
		VALUES ($1, $2, $3, $4, $5, $6)
	`

	_, err := r.DB.ExecContext(context.Background(), query,
		conn.ID, conn.RequesterID, conn.ProviderID, conn.FileID, conn.Status, conn.StartedAt)
	if err != nil {
		return nil, err
	}

	return conn, nil
}

// GetSignalingSession retrieves a signaling session by file_id
func (r *Repository) GetSignalingSession(fileID uuid.UUID) (*ActiveConnection, error) {
	query := `
		SELECT id, requester_id, provider_id, file_id, status, started_at, completed_at
		FROM active_connections
		WHERE file_id = $1 AND status IN ('pending', 'offer_sent', 'answer_received')
		ORDER BY started_at DESC
		LIMIT 1
	`

	var conn ActiveConnection
	var completedAt sql.NullTime

	err := r.DB.QueryRowContext(context.Background(), query, fileID).Scan(
		&conn.ID, &conn.RequesterID, &conn.ProviderID, &conn.FileID,
		&conn.Status, &conn.StartedAt, &completedAt,
	)
	if err != nil {
		return nil, err
	}

	if completedAt.Valid {
		conn.CompletedAt = &completedAt.Time
	}

	return &conn, nil
}

// UpdateSignalingStatus updates status of a signaling session
func (r *Repository) UpdateSignalingStatus(fileID uuid.UUID, status string) error {
	query := `
		UPDATE active_connections
		SET status = $1
		WHERE file_id = $2 AND status IN ('pending', 'offer_sent', 'answer_received')
	`

	_, err := r.DB.ExecContext(context.Background(), query, status, fileID)
	return err
}

// CompleteSignalingSession marks a signaling session completed
func (r *Repository) CompleteSignalingSession(fileID uuid.UUID) error {
	query := `
		UPDATE active_connections
		SET status = 'completed', completed_at = $1
		WHERE file_id = $2 AND status IN ('pending', 'offer_sent', 'answer_received')
	`

	_, err := r.DB.ExecContext(context.Background(), query, time.Now(), fileID)
	return err
}

// GetSignalingSessionByRequester gets session where peer is the requester
func (r *Repository) GetSignalingSessionByRequester(requesterID uuid.UUID, fileID uuid.UUID) (*ActiveConnection, error) {
	query := `
		SELECT id, requester_id, provider_id, file_id, status, started_at, completed_at
		FROM active_connections
		WHERE requester_id = $1 AND file_id = $2 AND status IN ('pending', 'offer_sent', 'answer_received')
		ORDER BY started_at DESC
		LIMIT 1
	`

	var conn ActiveConnection
	var completedAt sql.NullTime

	err := r.DB.QueryRowContext(context.Background(), query, requesterID, fileID).Scan(
		&conn.ID, &conn.RequesterID, &conn.ProviderID, &conn.FileID,
		&conn.Status, &conn.StartedAt, &completedAt,
	)
	if err != nil {
		return nil, err
	}

	if completedAt.Valid {
		conn.CompletedAt = &completedAt.Time
	}

	return &conn, nil
}

// GetSignalingSessionByProvider gets session where peer is the provider
func (r *Repository) GetSignalingSessionByProvider(providerID uuid.UUID, fileID uuid.UUID) (*ActiveConnection, error) {
	query := `
		SELECT id, requester_id, provider_id, file_id, status, started_at, completed_at
		FROM active_connections
		WHERE provider_id = $1 AND file_id = $2 AND status IN ('pending', 'offer_sent', 'answer_received')
		ORDER BY started_at DESC
		LIMIT 1
	`

	var conn ActiveConnection
	var completedAt sql.NullTime

	err := r.DB.QueryRowContext(context.Background(), query, providerID, fileID).Scan(
		&conn.ID, &conn.RequesterID, &conn.ProviderID, &conn.FileID,
		&conn.Status, &conn.StartedAt, &completedAt,
	)
	if err != nil {
		return nil, err
	}

	if completedAt.Valid {
		conn.CompletedAt = &completedAt.Time
	}

	return &conn, nil
}

// Legacy functions for backward compatibility with existing code
func MarkPeerOffline(db *pgxpool.Pool, peerID string) error {
	ctx := context.Background()
<<<<<<< Updated upstream
	query := `
		UPDATE peers
		SET is_online = false,
		    last_seen = $2
		WHERE peer_id = $1;
	`
	lastSeen := time.Now()
	_, err := db.Exec(ctx, query, peerID, lastSeen)
	return err
=======
	
    query := `
        UPDATE peers
        SET is_online = false,
            last_seen = $2
        WHERE peer_id = $1;
    `
    _, err := db.Exec(ctx, query, peerID, lastSeen) // Use the passed context
    return err
>>>>>>> Stashed changes
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

	err = AddPeerFile(db, peerID, fileUUID.String())
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

func GetPeerUUIDByID(db *pgxpool.Pool, peerID int) (string, error) {
	ctx := context.Background()
	var uuid string
	idStr := fmt.Sprintf("%d", peerID)
	err := db.QueryRow(ctx, `SELECT id FROM peers WHERE peer_id = $1`, idStr).Scan(&uuid)
	if err != nil {
		log.Printf("Failed to fetch UUID for peer_id %d: %v\n", peerID, err)
		return "", err
	}
	return uuid, nil
}

func GetFileUUIDByHash(db *pgxpool.Pool, fileHash string) (uuid.UUID, error) {
	ctx := context.Background()
	var fileUUID uuid.UUID
	err := db.QueryRow(ctx, `SELECT id FROM files WHERE file_hash = $1`, fileHash).Scan(&fileUUID)
	if err != nil {
		log.Printf("Failed to fetch file UUID for hash %s: %v\n", fileHash, err)
		return uuid.Nil, err
	}
	return fileUUID, nil
}
