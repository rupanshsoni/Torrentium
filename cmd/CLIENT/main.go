package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	webRTC "torrentium/internal/client"
	db "torrentium/internal/db"
	p2p "torrentium/internal/p2p"

	"github.com/dustin/go-humanize"
	"github.com/ipfs/go-cid"
	"github.com/joho/godotenv"
	dht "github.com/libp2p/go-libp2p-kad-dht"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
	"github.com/multiformats/go-multihash"
	"github.com/pion/webrtc/v3"
	"github.com/schollz/progressbar/v3"
)

const (
	DefaultPieceSize     = 1 << 20 // 1 MiB pieces
	MaxProviders         = 10
	MaxChunk             = 16 * 1024 // 16KiB chunks
	MaxParallelDownloads = 3
)

type Client struct {
	host            host.Host
	dht             *dht.IpfsDHT
	webRTCPeers     map[peer.ID]*webRTC.SimpleWebRTCPeer
	peersMux        sync.RWMutex
	sharingFiles    map[string]*FileInfo
	activeDownloads map[string]*DownloadState
	downloadsMux    sync.RWMutex
	db              *db.Repository
}

type FileInfo struct {
	FilePath string
	Hash     string
	Size     int64
	Name     string
	PieceSz  int64
}

type controlMessage struct {
	Command     string      `json:"command"`
	CID         string      `json:"cid,omitempty"`
	PieceSize   int64       `json:"piece_size,omitempty"`
	TotalSize   int64       `json:"total_size,omitempty"`
	HashHex     string      `json:"hash_hex,omitempty"`
	NumPieces   int64       `json:"num_pieces,omitempty"`
	Pieces      []db.Piece  `json:"pieces,omitempty"`
	PieceHash   string      `json:"piece_hash,omitempty"`
	Index       int64       `json:"index,omitempty"`
	Filename    string      `json:"filename,omitempty"`
	ChunkIndex  int         `json:"chunk_index,omitempty"`
	TotalChunks int         `json:"total_chunks,omitempty"`
	Payload     string      `json:"payload,omitempty"`
}

type DownloadState struct {
	File            *os.File
	Manifest        controlMessage
	TotalPieces     int
	Pieces          []db.Piece
	Completed       chan bool
	Progress        *progressbar.ProgressBar
	PieceStatus     []bool // true if piece is downloaded
	PieceAssignees  map[int]peer.ID
	pieceBuffers    map[int][][]byte // Buffer to reassemble chunks into pieces
	mu              sync.Mutex
	completedPieces int
}

var (
	manifestWaiters = make(map[string]chan controlMessage)
	manifestChMu    sync.Mutex
)

func setupGracefulShutdown(h host.Host) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-ch
		fmt.Println("\nShutting down gracefully...")
		_ = h.Close()
		os.Exit(0)
	}()
}

func NewClient(h host.Host, d *dht.IpfsDHT, repo *db.Repository) *Client {
	return &Client{
		host:            h,
		dht:             d,
		webRTCPeers:     make(map[peer.ID]*webRTC.SimpleWebRTCPeer),
		sharingFiles:    make(map[string]*FileInfo),
		activeDownloads: make(map[string]*DownloadState),
		db:              repo,
	}
}

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Use a custom logger that doesn't print timestamps
	log.SetFlags(0)

	if err := godotenv.Load(); err != nil {
		// This is not a critical error, so we don't need to log it
	}

	DB := db.InitDB()
	if DB == nil {
		log.Fatal("Database initialization failed")
	}

	h, d, err := p2p.NewHost(ctx, "/ip4/0.0.0.0/tcp/0")
	if err != nil {
		log.Fatal("Failed to create libp2p host:", err)
	}
	defer h.Close()

	fmt.Println("Connecting to the network...")
	go func() {
		if err := p2p.Bootstrap(ctx, h, d); err != nil {
			log.Printf("Error bootstrapping DHT: %v", err)
		}
	}()

	setupGracefulShutdown(h)

	repo := db.NewRepository(DB)
	client := NewClient(h, d, repo)
	client.startDHTMaintenance()
	p2p.RegisterSignalingProtocol(h, client.handleWebRTCOffer)

	client.commandLoop()
}
func (c *Client) commandLoop() {
	scanner := bufio.NewScanner(os.Stdin)
	c.printInstructions()
	for {
		fmt.Print("> ")
		if !scanner.Scan() {
			break
		}
		parts := strings.Fields(scanner.Text())
		if len(parts) == 0 {
			continue
		}
		cmd, args := parts[0], parts[1:]
		var err error
		switch cmd {
		case "help":
			c.printInstructions()
		case "add":
			if len(args) != 1 {
				fmt.Println("Usage: add <path>")
			} else {
				err = c.addFile(args[0])
			}
		case "list":
			c.listLocalFiles()
		case "search":
			if len(args) != 1 {
				fmt.Println("Usage: search <cid|text>")
			} else {
				if strings.HasPrefix(args[0], "bafy") || strings.HasPrefix(args[0], "Qm") {
					err = c.enhancedSearchByCID(args[0])
				} else {
					err = c.searchByText(args[0])
				}
			}
		case "download":
			if len(args) != 1 {
				fmt.Println("Usage: download <cid>")
			} else {
				err = c.downloadFile(args[0])
			}
		case "peers":
			c.listConnectedPeers()
		case "connect":
			if len(args) != 1 {
				fmt.Println("Usage: connect <multiaddr>")
			} else {
				err = c.connectToPeer(args[0])
			}
		case "announce":
			if len(args) != 1 {
				fmt.Println("Usage: announce <cid>")
			} else {
				err = c.announceFile(args[0])
			}
		case "health":
			c.checkConnectionHealth()
		case "debug":
			c.debugNetworkStatus()
		case "exit":
			return
		default:
			fmt.Println("Unknown command. Type 'help' for available commands.")
		}
		if err != nil {
			log.Printf("Error: %v", err)
		}
	}
}
func (c *Client) printInstructions() {
	fmt.Println("\n=== Torrentium P2P File Sharing ===")
	fmt.Println("Commands:")
	fmt.Println("  add <path>           - Share a file")
	fmt.Println("  list                 - List shared files")
	fmt.Println("  search <cid|text>    - Search for a file")
	fmt.Println("  download <cid>       - Download a file")
	fmt.Println("  peers                - Show connected peers")
	fmt.Println("  connect <multiaddr>  - Connect to a peer")
	fmt.Println("  announce <cid>       - Re-announce a file")
	fmt.Println("  health               - Check network health")
	fmt.Println("  debug                - Show debug info")
	fmt.Println("  help                 - Show this help menu")
	fmt.Println("  exit                 - Exit")
	fmt.Printf("\nYour Peer ID: %s\n", c.host.ID())
}
func (c *Client) debugNetworkStatus() {
	fmt.Println("\n=== Network Debug Info ===")
	fmt.Printf("Peer ID: %s\n", c.host.ID())
	fmt.Println("Addresses:")
	for _, addr := range c.host.Addrs() {
		fmt.Printf("  %s/p2p/%s\n", addr, c.host.ID())
	}

	peers := c.host.Network().Peers()
	fmt.Printf("\nConnected Peers: %d\n", len(peers))
	for i, peerID := range peers {
		conn := c.host.Network().ConnsToPeer(peerID)
		if len(conn) > 0 {
			fmt.Printf("  %d. %s (%s)\n", i+1, peerID, conn[0].RemoteMultiaddr())
		}
	}

	routingTableSize := c.dht.RoutingTable().Size()
	fmt.Printf("\nDHT Routing Table Size: %d\n", routingTableSize)

	fmt.Printf("\nShared Files: %d\n", len(c.sharingFiles))
	for cid, fileInfo := range c.sharingFiles {
		fmt.Printf("  - %s (%s)\n", fileInfo.Name, cid)
	}
}

func (c *Client) announceFile(cidStr string) error {
	fileCID, err := cid.Decode(cidStr)
	if err != nil {
		return fmt.Errorf("invalid CID: %w", err)
	}
	fmt.Printf("Re-announcing %s...\n", cidStr)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := c.dht.Provide(ctx, fileCID, true); err != nil {
		return fmt.Errorf("failed to announce: %w", err)
	}
	fmt.Println("Successfully announced.")
	return nil
}

func (c *Client) startDHTMaintenance() {
	go func() {
		ticker := time.NewTicker(10 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			c.dht.RefreshRoutingTable()
		}
	}()
}

func (c *Client) checkConnectionHealth() {
	peers := c.host.Network().Peers()
	fmt.Println("\n=== Connection Health ===")
	fmt.Printf("Connected peers: %d\n", len(peers))
	if len(peers) < 3 {
		fmt.Println("Warning: Low peer count. File discovery may be slow.")
	} else {
		fmt.Println("Peer connectivity is good.")
	}

	routingTableSize := c.dht.RoutingTable().Size()
	fmt.Printf("DHT routing table size: %d\n", routingTableSize)
	if routingTableSize < 10 {
		fmt.Println("Warning: DHT routing table is small. File discovery may be limited.")
	} else {
		fmt.Println("DHT connectivity is good.")
	}
}

func (c *Client) performNetworkDiagnostics() {
	// This functionality is now integrated into the `health` and `debug` commands
	fmt.Println("Network diagnostics are now part of the 'health' and 'debug' commands.")
}

func (c *Client) performLocalWebRTCTest() {
	// This functionality is for debugging and has been removed from the user-facing commands
	fmt.Println("Local WebRTC testing is an internal function.")
}

func (c *Client) addFile(filePath string) error {
	ctx := context.Background()
	f, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("failed to open file: %w", err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return fmt.Errorf("failed to get file info: %w", err)
	}

	hasher := sha256.New()
	if _, err := io.Copy(hasher, f); err != nil {
		return fmt.Errorf("failed to calculate hash: %w", err)
	}

	fileHashBytes := hasher.Sum(nil)
	fileHashStr := hex.EncodeToString(fileHashBytes)
	mhash, err := multihash.Encode(fileHashBytes, multihash.SHA2_256)
	if err != nil {
		return fmt.Errorf("failed to create multihash: %w", err)
	}

	fileCID := cid.NewCidV1(cid.Raw, mhash)

	// Create pieces manifest
	pieceSz := int64(DefaultPieceSize)
	numPieces := (info.Size() + pieceSz - 1) / pieceSz
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}

	for idx := int64(0); idx < numPieces; idx++ {
		offset := idx * pieceSz
		size := min64(pieceSz, info.Size()-offset)
		h := sha256.New()
		if _, err := io.CopyN(h, f, size); err != nil {
			return err
		}
		ph := hex.EncodeToString(h.Sum(nil))
		if err := c.db.UpsertPiece(ctx, fileCID.String(), idx, offset, size, ph, true); err != nil {
			return err
		}
	}

	if err := c.db.AddLocalFile(ctx, fileCID.String(), info.Name(), info.Size(), filePath, fileHashStr); err != nil {
		return fmt.Errorf("failed to store file metadata: %w", err)
	}

	c.sharingFiles[fileCID.String()] = &FileInfo{
		FilePath: filePath,
		Hash:     fileHashStr,
		Size:     info.Size(),
		Name:     info.Name(),
		PieceSz:  pieceSz,
	}

	fmt.Printf("Sharing '%s' (%s)\n", info.Name(), humanize.Bytes(uint64(info.Size())))
	fmt.Printf("CID: %s\n", fileCID.String())

	go func() {
		provideCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		if err := c.dht.Provide(provideCtx, fileCID, true); err != nil {
			log.Printf("Failed to announce file to DHT: %v", err)
		}
	}()

	return nil
}

func (c *Client) listLocalFiles() {
	ctx := context.Background()
	files, err := c.db.GetLocalFiles(ctx)
	if err != nil {
		log.Printf("Error retrieving files: %v", err)
		return
	}
	if len(files) == 0 {
		fmt.Println("You are not sharing any files.")
		return
	}
	fmt.Println("\n=== Shared Files ===")
	for _, file := range files {
		fmt.Printf("- %s (%s)\n", file.Filename, humanize.Bytes(uint64(file.FileSize)))
		fmt.Printf("  CID: %s\n", file.CID)
	}
}

func (c *Client) searchByText(q string) error {
	ctx := context.Background()
	matches, err := c.db.SearchByFilename(ctx, q)
	if err != nil {
		return err
	}
	if len(matches) == 0 {
		fmt.Printf("No local files found matching '%s'.\n", q)
		return nil
	}
	fmt.Printf("Found %d local match(es) for '%s':\n", len(matches), q)
	for _, m := range matches {
		fmt.Printf("- %s (CID: %s)\n", m.Filename, m.CID)
	}
	return nil
}

func (c *Client) enhancedSearchByCID(cidStr string) error {
	fileCID, err := cid.Decode(cidStr)
	if err != nil {
		return fmt.Errorf("invalid CID: %w", err)
	}
	fmt.Printf("Searching for providers of %s...\n", fileCID.String())
	providers, err := c.findProvidersWithTimeout(fileCID, 30*time.Second, MaxProviders)
	if err != nil {
		return fmt.Errorf("provider search failed: %w", err)
	}

	if len(providers) == 0 {
		fmt.Println("No providers found.")
		return nil
	}

	fmt.Printf("Found %d provider(s):\n", len(providers))
	for i, provider := range providers {
		status := "Not connected"
		if c.host.Network().Connectedness(provider.ID) == network.Connected {
			status = "Connected"
		}
		fmt.Printf("  %d. %s (%s)\n", i+1, provider.ID, status)
	}
	return nil
}

func (c *Client) findProvidersWithTimeout(id cid.Cid, timeout time.Duration, maxProviders int) ([]peer.AddrInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	providersChan := c.dht.FindProvidersAsync(ctx, id, maxProviders)
	var providers []peer.AddrInfo

	for provider := range providersChan {
		if provider.ID != c.host.ID() {
			providers = append(providers, provider)
			if len(providers) >= maxProviders {
				break
			}
		}
	}

	return providers, nil
}

func (c *Client) connectToPeer(multiaddrStr string) error {
	addr, err := multiaddr.NewMultiaddr(multiaddrStr)
	if err != nil {
		return fmt.Errorf("invalid multiaddr: %w", err)
	}

	peerInfo, err := peer.AddrInfoFromP2pAddr(addr)
	if err != nil {
		return fmt.Errorf("failed to parse peer info: %w", err)
	}

	fmt.Printf("Connecting to %s...\n", peerInfo.ID)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := c.host.Connect(ctx, *peerInfo); err != nil {
		return fmt.Errorf("connection failed: %w", err)
	}

	fmt.Printf("Successfully connected to %s.\n", peerInfo.ID)
	c.host.Peerstore().AddAddrs(peerInfo.ID, peerInfo.Addrs, time.Hour)
	return nil
}

func (c *Client) listConnectedPeers() {
	peers := c.host.Network().Peers()
	fmt.Printf("\n=== Connected Peers (%d) ===\n", len(peers))
	for _, peerID := range peers {
		conns := c.host.Network().ConnsToPeer(peerID)
		if len(conns) > 0 {
			fmt.Printf("- %s (%s)\n", peerID, conns[0].RemoteMultiaddr())
		}
	}
}

func (c *Client) downloadFile(cidStr string) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fileCID, err := cid.Decode(cidStr)
	if err != nil {
		return fmt.Errorf("invalid CID: %w", err)
	}

	fmt.Printf("Searching for providers of %s...\n", fileCID.String())
	providers, err := c.findProvidersWithTimeout(fileCID, 60*time.Second, MaxProviders)
	if err != nil {
		return fmt.Errorf("provider search failed: %w", err)
	}

	if len(providers) == 0 {
		return fmt.Errorf("no providers found")
	}

	fmt.Printf("Found %d provider(s). Requesting file info...\n", len(providers))

	var manifest controlMessage
	var firstPeer *webRTC.SimpleWebRTCPeer
	for _, p := range providers {
		peerConn, err := c.initiateWebRTCConnectionWithRetry(p.ID, 1)
		if err == nil {
			firstPeer = peerConn
			manifest, err = c.requestManifest(firstPeer, cidStr)
			if err == nil {
				break
			}
			firstPeer.Close()
		}
	}

	if firstPeer == nil {
		return fmt.Errorf("failed to get file info from any provider")
	}

	// Store pieces in the database
	for _, piece := range manifest.Pieces {
		if err := c.db.UpsertPiece(ctx, cidStr, piece.Index, piece.Offset, piece.Size, piece.Hash, false); err != nil {
			log.Printf("Failed to store piece info: %v", err)
		}
	}

	downloadPath := fmt.Sprintf("%s.download", manifest.Filename)
	finalPath := manifest.Filename
	localFile, err := os.Create(downloadPath)
	if err != nil {
		firstPeer.Close()
		return fmt.Errorf("failed to create file: %w", err)
	}

	pieces, _ := c.db.GetPieces(ctx, cidStr)
	if len(pieces) == 0 {
		return fmt.Errorf("failed to retrieve piece info after manifest")
	}

	state := &DownloadState{
		File:            localFile,
		Manifest:        manifest,
		TotalPieces:     int(manifest.NumPieces),
		Pieces:          pieces,
		Completed:       make(chan bool, 1),
		Progress:        progressbar.DefaultBytes(manifest.TotalSize, "Downloading"),
		PieceStatus:     make([]bool, int(manifest.NumPieces)),
		PieceAssignees:  make(map[int]peer.ID),
		pieceBuffers:    make(map[int][][]byte),
		completedPieces: 0,
	}
	c.downloadsMux.Lock()
	c.activeDownloads[cidStr] = state
	c.downloadsMux.Unlock()

	var wg sync.WaitGroup
	peersToUse := providers
	if len(peersToUse) > MaxParallelDownloads {
		peersToUse = peersToUse[:MaxParallelDownloads]
	}

	chunksPerPeer := state.TotalPieces / len(peersToUse)

	for i, p := range peersToUse {
		start := i * chunksPerPeer
		end := start + chunksPerPeer
		if i == len(peersToUse)-1 {
			end = state.TotalPieces
		}

		wg.Add(1)
		go func(peerInfo peer.AddrInfo, startPiece, endPiece int) {
			defer wg.Done()
			var peerConn *webRTC.SimpleWebRTCPeer
			if peerInfo.ID == firstPeer.GetSignalingStream().Conn().RemotePeer() {
				peerConn = firstPeer
			} else {
				var connErr error
				peerConn, connErr = c.initiateWebRTCConnectionWithRetry(peerInfo.ID, 2)
				if connErr != nil {
					log.Printf("Failed to connect to %s: %v", peerInfo.ID, connErr)
					return
				}
				defer peerConn.Close()
			}
			c.downloadChunksFromPeer(peerConn, state, startPiece, endPiece)
		}(p, start, end)
	}

	<-state.Completed
	localFile.Close()

	if err := os.Rename(downloadPath, finalPath); err != nil {
		return fmt.Errorf("failed to rename downloaded file: %w", err)
	}

	fmt.Printf("\nDownload complete: %s\n", finalPath)
	return nil
}

func (c *Client) downloadChunksFromPeer(peer *webRTC.SimpleWebRTCPeer, state *DownloadState, startPiece, endPiece int) {
	remotePeerID := peer.GetSignalingStream().Conn().RemotePeer()
	for i := startPiece; i < endPiece; i++ {
		state.mu.Lock()
		if state.PieceStatus[i] {
			state.mu.Unlock()
			continue
		}
		state.PieceAssignees[i] = remotePeerID
		state.mu.Unlock()

		req := controlMessage{
			Command: "REQUEST_PIECE",
			CID:     state.Manifest.CID,
			Index:   int64(i),
		}

		if err := peer.SendJSON(req); err != nil {
			log.Printf("Failed to request piece %d from %s: %v", i, remotePeerID, err)
			return
		}
	}
}

func (c *Client) requestManifest(peer *webRTC.SimpleWebRTCPeer, cidStr string) (controlMessage, error) {
	req := controlMessage{Command: "REQUEST_MANIFEST", CID: cidStr}
	if err := peer.SendJSON(req); err != nil {
		return controlMessage{}, err
	}

	manifestCh := make(chan controlMessage, 1)
	manifestChMu.Lock()
	manifestWaiters[cidStr] = manifestCh
	manifestChMu.Unlock()

	defer func() {
		manifestChMu.Lock()
		delete(manifestWaiters, cidStr)
		manifestChMu.Unlock()
	}()

	select {
	case manifest := <-manifestCh:
		return manifest, nil
	case <-time.After(30 * time.Second):
		return controlMessage{}, fmt.Errorf("timed out waiting for manifest")
	}
}

func (c *Client) initiateWebRTCConnectionWithRetry(targetPeerID peer.ID, maxRetries int) (*webRTC.SimpleWebRTCPeer, error) {
	var lastErr error
	for attempt := 1; attempt <= maxRetries; attempt++ {
		if attempt > 1 {
			time.Sleep(time.Duration(attempt) * time.Second)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		if c.host.Network().Connectedness(targetPeerID) != network.Connected {
			if err := c.host.Connect(ctx, peer.AddrInfo{ID: targetPeerID}); err != nil {
				lastErr = fmt.Errorf("failed to connect to %s: %w", targetPeerID, err)
				continue
			}
		}

		webrtcPeer, err := webRTC.NewSimpleWebRTCPeer(c.onDataChannelMessage)
		if err != nil {
			lastErr = err
			continue
		}

		offer, err := webrtcPeer.CreateOffer()
		if err != nil {
			webrtcPeer.Close()
			lastErr = err
			continue
		}

		streamCtx, streamCancel := context.WithTimeout(context.Background(), 30*time.Second)
		s, err := c.host.NewStream(streamCtx, targetPeerID, p2p.SignalingProtocolID)
		streamCancel()
		if err != nil {
			webrtcPeer.Close()
			lastErr = err
			continue
		}

		webrtcPeer.SetSignalingStream(s)
		encoder := json.NewEncoder(s)
		decoder := json.NewDecoder(s)

		offerMsg := map[string]string{"type": "offer", "data": offer}
		if err := encoder.Encode(offerMsg); err != nil {
			webrtcPeer.Close()
			lastErr = err
			continue
		}

		var answerMsg map[string]string
		if err := decoder.Decode(&answerMsg); err != nil {
			webrtcPeer.Close()
			lastErr = fmt.Errorf("failed to decode answer: %w", err)
			continue
		}

		if answerMsg["type"] == "error" {
			webrtcPeer.Close()
			lastErr = fmt.Errorf("peer returned an error: %s", answerMsg["data"])
			continue
		}
		if answerMsg["type"] != "answer" {
			webrtcPeer.Close()
			lastErr = fmt.Errorf("expected 'answer', got '%s'", answerMsg["type"])
			continue
		}

		if err := webrtcPeer.HandleAnswer(answerMsg["data"]); err != nil {
			webrtcPeer.Close()
			lastErr = err
			continue
		}

		if err := webrtcPeer.WaitForConnection(30 * time.Second); err != nil {
			webrtcPeer.Close()
			lastErr = fmt.Errorf("WebRTC connection failed: %w", err)
			continue
		}

		c.peersMux.Lock()
		c.webRTCPeers[targetPeerID] = webrtcPeer
		c.peersMux.Unlock()

		return webrtcPeer, nil
	}
	return nil, lastErr
}
func (c *Client) handleWebRTCOffer(offer, remotePeerID string, s network.Stream) (string, error) {
	peerID, err := peer.Decode(remotePeerID)
	if err != nil {
		return "", fmt.Errorf("invalid peer ID: %w", err)
	}

	webrtcPeer, err := webRTC.NewSimpleWebRTCPeer(c.onDataChannelMessage)
	if err != nil {
		return "", err
	}

	webrtcPeer.SetSignalingStream(s)
	answer, err := webrtcPeer.HandleOffer(offer)
	if err != nil {
		webrtcPeer.Close()
		return "", err
	}

	c.peersMux.Lock()
	c.webRTCPeers[peerID] = webrtcPeer
	c.peersMux.Unlock()

	return answer, nil
}

func (c *Client) onDataChannelMessage(msg webrtc.DataChannelMessage, peer *webRTC.SimpleWebRTCPeer) {
	if !msg.IsString {
		log.Println("Received unexpected binary message, expecting JSON")
		return
	}

	var ctrl controlMessage
	if err := json.Unmarshal(msg.Data, &ctrl); err != nil {
		var ping map[string]string
		if json.Unmarshal(msg.Data, &ping) == nil && ping["type"] == "ping" {
			return // Ignore keep-alive pings
		}
		log.Printf("Failed to unmarshal control message: %v", err)
		return
	}
	c.handleControlMessage(ctrl, peer)
}

func (c *Client) handleControlMessage(ctrl controlMessage, peer *webRTC.SimpleWebRTCPeer) {
	ctx := context.Background()
	switch ctrl.Command {
	case "REQUEST_MANIFEST":
		c.handleManifestRequest(ctx, ctrl, peer)
	case "MANIFEST":
		manifestChMu.Lock()
		if ch, ok := manifestWaiters[ctrl.CID]; ok {
			ch <- ctrl
		}
		manifestChMu.Unlock()
	case "REQUEST_PIECE":
		go c.handlePieceRequest(ctx, ctrl, peer)
	case "PIECE_CHUNK":
		c.handlePieceChunk(ctrl)
	default:
		log.Printf("Unknown command received: %s", ctrl.Command)
	}
}
func (c *Client) handlePieceChunk(ctrl controlMessage) {
	c.downloadsMux.RLock()
	state, ok := c.activeDownloads[ctrl.CID]
	c.downloadsMux.RUnlock()
	if !ok {
		return // Download is no longer active
	}

	state.mu.Lock()
	defer state.mu.Unlock()

	if state.PieceStatus[ctrl.Index] {
		return // Already have this piece
	}

	if state.pieceBuffers[int(ctrl.Index)] == nil {
		state.pieceBuffers[int(ctrl.Index)] = make([][]byte, ctrl.TotalChunks)
	}

	chunkData, err := hex.DecodeString(ctrl.Payload)
	if err != nil {
		log.Printf("Failed to decode chunk payload: %v", err)
		return
	}

	state.pieceBuffers[int(ctrl.Index)][ctrl.ChunkIndex] = chunkData
	_ = state.Progress.Add(len(chunkData))

	// Check if piece is complete
	isComplete := true
	var pieceSize int
	for _, chunk := range state.pieceBuffers[int(ctrl.Index)] {
		if chunk == nil {
			isComplete = false
			break
		}
		pieceSize += len(chunk)
	}

	if isComplete {
		pieceData := make([]byte, 0, pieceSize)
		for _, chunk := range state.pieceBuffers[int(ctrl.Index)] {
			pieceData = append(pieceData, chunk...)
		}

		h := sha256.New()
		h.Write(pieceData)
		if hex.EncodeToString(h.Sum(nil)) != state.Pieces[ctrl.Index].Hash {
			log.Printf("Piece %d hash mismatch, retrying...", ctrl.Index)
			state.pieceBuffers[int(ctrl.Index)] = nil // Clear buffer to re-download
			return
		}

		if _, err := state.File.WriteAt(pieceData, state.Pieces[ctrl.Index].Offset); err != nil {
			log.Printf("Failed to write piece %d: %v", ctrl.Index, err)
			return
		}

		state.PieceStatus[ctrl.Index] = true
		state.completedPieces++
		delete(state.pieceBuffers, int(ctrl.Index))

		if state.completedPieces == state.TotalPieces {
			state.Completed <- true
		}
	}
}

func (c *Client) handlePieceRequest(ctx context.Context, ctrl controlMessage, peer *webRTC.SimpleWebRTCPeer) {
	pieces, err := c.db.GetPieces(ctx, ctrl.CID)
	if err != nil || int(ctrl.Index) >= len(pieces) {
		log.Printf("Invalid piece request: CID %s, index %d", ctrl.CID, ctrl.Index)
		return
	}

	fileInfo, err := c.db.GetLocalFileByCID(ctx, ctrl.CID)
	if err != nil {
		log.Printf("File not found for piece request: %s", ctrl.CID)
		return
	}

	file, err := os.Open(fileInfo.FilePath)
	if err != nil {
		log.Printf("Failed to open file for piece request: %v", err)
		return
	}
	defer file.Close()

	piece := pieces[ctrl.Index]
	pieceBuffer := make([]byte, piece.Size)
	if _, err := file.ReadAt(pieceBuffer, piece.Offset); err != nil {
		log.Printf("Failed to read piece %d: %v", ctrl.Index, err)
		return
	}

	totalChunks := (len(pieceBuffer) + MaxChunk - 1) / MaxChunk
	for i := 0; i < totalChunks; i++ {
		start := i * MaxChunk
		end := start + MaxChunk
		if end > len(pieceBuffer) {
			end = len(pieceBuffer)
		}
		chunk := pieceBuffer[start:end]

		chunkMsg := controlMessage{
			Command:     "PIECE_CHUNK",
			CID:         ctrl.CID,
			Index:       ctrl.Index,
			ChunkIndex:  i,
			TotalChunks: totalChunks,
			Payload:     hex.EncodeToString(chunk),
		}
		if err := peer.SendJSON(chunkMsg); err != nil {
			log.Printf("Failed to send chunk %d of piece %d: %v", i, ctrl.Index, err)
			return
		}
	}
}
func (c *Client) handleManifestRequest(ctx context.Context, ctrl controlMessage, peer *webRTC.SimpleWebRTCPeer) {
	localFile, err := c.db.GetLocalFileByCID(ctx, ctrl.CID)
	if err != nil {
		log.Printf("Manifest request for unknown file: %s", ctrl.CID)
		return
	}

	pieces, err := c.db.GetPieces(ctx, ctrl.CID)
	if err != nil {
		log.Printf("Failed to get pieces for manifest: %v", err)
		return
	}

	manifest := controlMessage{
		Command:   "MANIFEST",
		CID:       ctrl.CID,
		TotalSize: localFile.FileSize,
		HashHex:   localFile.FileHash,
		NumPieces: int64(len(pieces)),
		Pieces:    pieces,
		Filename:  localFile.Filename,
	}

	if err := peer.SendJSON(manifest); err != nil {
		log.Printf("Failed to send manifest: %v", err)
	}
}

func (c *Client) handleFileRequest(ctx context.Context, ctrl controlMessage, peer *webRTC.SimpleWebRTCPeer) {
	// This function is deprecated and should not be called
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}