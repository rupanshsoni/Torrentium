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

	// "github.com/libp2p/go-libp2p/p2p/protocol/circuitv2/client"
	"github.com/multiformats/go-multiaddr"
	"github.com/multiformats/go-multihash"
	"github.com/pion/webrtc/v3"
	"github.com/schollz/progressbar/v3"
)

const (
	DefaultPieceSize     = 1 << 20 // 1 MiB pieces
	MaxProviders         = 10
	MaxChunk             = 64 * 1024 // 64KiB chunks
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
	Command   string `json:"command"`
	CID       string `json:"cid,omitempty"`
	PieceSize int64  `json:"piece_size,omitempty"`
	TotalSize int64  `json:"total_size,omitempty"`
	HashHex   string `json:"hash_hex,omitempty"`
	NumPieces int64  `json:"num_pieces,omitempty"`
	PieceHash string `json:"piece_hash,omitempty"`
	Index     int64  `json:"index,omitempty"`
	Filename  string `json:"filename,omitempty"`
	// Target    string `json:"filename,omitempty"`
	// From      string `json:"filename,omitempty"`
}

type DownloadState struct {
	File           *os.File
	Manifest       controlMessage
	TotalPieces    int
	Pieces         []db.Piece
	Completed      chan bool
	Progress       *progressbar.ProgressBar
	PieceStatus    []bool // true if piece is downloaded
	PieceAssignees map[int]peer.ID
	mu             sync.Mutex
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
		log.Println("Shutting down gracefully...")
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

	if err := godotenv.Load(); err != nil {
		log.Printf("Warning: Could not load .env file: %v", err)
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
				c.checkConnectionHealth()
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
				fmt.Println("Example: connect /ip4/127.0.0.1/tcp/54437/p2p/12D3KooWBLZFWsGZxoCFC8NsFgKvD6WJ6xV9UmYdR8t2C1kqYTcd")
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
		case "nettest":
			c.performNetworkDiagnostics()
		case "localtest":
			c.performLocalWebRTCTest()
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
	fmt.Println("\n=== Decentralized P2P File Sharing ===")
	fmt.Println("Commands:")
	fmt.Println(" add <path>           - Share a file on the network")
	fmt.Println(" list                 - List your shared files")
	fmt.Println(" search <cid|text>    - Search by CID or filename text")
	fmt.Println(" download <cid>       - Download a file by CID")
	fmt.Println(" peers                - Show connected peers")
	fmt.Println(" connect <multiaddr>  - Manually connect to a peer")
	fmt.Println(" announce <cid>       - Re-announce a file to DHT")
	fmt.Println(" health               - Check connection health")
	fmt.Println(" nettest              - Perform comprehensive network diagnostics")
	fmt.Println(" localtest            - Test local WebRTC functionality")
	fmt.Println(" debug                - Show detailed network debug info")
	fmt.Println(" help                 - Show this help")
	fmt.Println(" exit                 - Exit the application")
	fmt.Printf("\nYour Peer ID: %s\n", c.host.ID())
	fmt.Printf("Listening on: %v\n\n", c.host.Addrs())
}

func (c *Client) debugNetworkStatus() {
	fmt.Println("\n=== Network Debug Info ===")
	fmt.Printf("Our Peer ID: %s\n", c.host.ID())
	fmt.Printf("Our Addresses:\n")
	for _, addr := range c.host.Addrs() {
		fmt.Printf(" %s/p2p/%s\n", addr, c.host.ID())
	}

	peers := c.host.Network().Peers()
	fmt.Printf("\nConnected Peers (%d):\n", len(peers))
	for i, peerID := range peers {
		conn := c.host.Network().ConnsToPeer(peerID)
		if len(conn) > 0 {
			fmt.Printf(" %d. %s\n", i+1, peerID)
			fmt.Printf("    Address: %s\n", conn[0].RemoteMultiaddr())
		}
	}

	routingTableSize := c.dht.RoutingTable().Size()
	fmt.Printf("\nDHT Routing Table Size: %d\n", routingTableSize)

	fmt.Printf("\nShared Files (%d):\n", len(c.sharingFiles))
	for cid, fileInfo := range c.sharingFiles {
		fmt.Printf(" CID: %s\n", cid)
		fmt.Printf(" File: %s\n", fileInfo.Name)
		fmt.Printf(" ---\n")
	}
}

func (c *Client) announceFile(cidStr string) error {
	fileCID, err := cid.Decode(cidStr)
	if err != nil {
		return fmt.Errorf("invalid CID: %w", err)
	}
	fmt.Printf("Re-announcing CID %s to DHT...\n", cidStr)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := c.dht.Provide(ctx, fileCID, true); err != nil {
		return fmt.Errorf("failed to announce: %w", err)
	}
	fmt.Println(" - Successfully announced to DHT")
	return nil
}

func (c *Client) startDHTMaintenance() {
	go func() {
		ticker := time.NewTicker(10 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			log.Println("Performing DHT maintenance...")
			c.dht.RefreshRoutingTable()
			peers := c.host.Network().Peers()
			log.Printf("Connected to %d peers", len(peers))
			if len(peers) < 5 {
				log.Println("Low peer count; re-bootstrapping...")
				ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
				_ = p2p.Bootstrap(ctx, c.host, c.dht)
				cancel()
			}
		}
	}()
}

func (c *Client) checkConnectionHealth() {
	peers := c.host.Network().Peers()
	fmt.Printf("\n=== Connection Health ===\n")
	fmt.Printf("Connected peers: %d\n", len(peers))
	if len(peers) < 3 {
		fmt.Println(" - Warning: Low peer count. Consider restarting or checking network connectivity.")
	} else {
		fmt.Println(" - Good peer connectivity")
	}

	routingTableSize := c.dht.RoutingTable().Size()
	fmt.Printf("DHT routing table size: %d\n", routingTableSize)
	if routingTableSize < 10 {
		fmt.Println(" - Warning: Small DHT routing table. File discovery may be limited.")
	} else {
		fmt.Println(" - Good DHT connectivity")
	}
}

func (c *Client) performNetworkDiagnostics() {
	fmt.Println("\n=== Network Diagnostics ===")

	// Check our addresses and detect local network
	fmt.Printf("Our listening addresses:\n")
	hasLocalAddr := false
	for _, addr := range c.host.Addrs() {
		addrStr := addr.String()
		fmt.Printf(" - %s/p2p/%s\n", addr, c.host.ID())
		if strings.Contains(addrStr, "192.168.") || strings.Contains(addrStr, "10.") || strings.Contains(addrStr, "172.") {
			hasLocalAddr = true
		}
	}

	if hasLocalAddr {
		fmt.Println("✅ Local network addresses detected - optimized for same-network connections")
	} else {
		fmt.Println("ℹ️  No local network addresses detected")
	}

	// Test ICE connectivity
	fmt.Println("\nTesting ICE connectivity...")
	if err := webRTC.TestICEConnectivity(); err != nil {
		fmt.Printf("❌ ICE connectivity test failed: %v\n", err)
		fmt.Println("This indicates potential WebRTC connection issues")
	} else {
		fmt.Println("✅ ICE connectivity test passed")
	}

	// Check libp2p connectivity
	peers := c.host.Network().Peers()
	fmt.Printf("\nlibp2p peer connections: %d\n", len(peers))
	if len(peers) > 0 {
		fmt.Println("Connected peers:")
		for i, peerID := range peers {
			if i >= 5 { // Limit output
				fmt.Printf(" ... and %d more\n", len(peers)-5)
				break
			}
			conn := c.host.Network().ConnsToPeer(peerID)
			if len(conn) > 0 {
				remoteAddr := conn[0].RemoteMultiaddr().String()
				fmt.Printf(" - %s (%s)\n", peerID, remoteAddr)
				if strings.Contains(remoteAddr, "192.168.") || strings.Contains(remoteAddr, "10.") || strings.Contains(remoteAddr, "172.") {
					fmt.Printf("   ✅ Local network peer\n")
				}
			}
		}
	}

	// Check DHT health
	routingTableSize := c.dht.RoutingTable().Size()
	fmt.Printf("\nDHT routing table size: %d\n", routingTableSize)

	fmt.Println("\n💡 For same-network connections:")
	fmt.Println("   1. Make sure both peers are connected to libp2p first")
	fmt.Println("   2. WebRTC should work directly without TURN servers")
	fmt.Println("   3. Use 'peers' command to see connected peers")
	fmt.Println("   4. Use 'connect <multiaddr>' to manually connect")

	fmt.Println("\nDiagnostics complete.")
}

func (c *Client) performLocalWebRTCTest() {
	fmt.Println("\n=== Local Network WebRTC Test ===")

	// Create a simple WebRTC peer for testing
	testPeer, err := webRTC.NewSimpleWebRTCPeer(func(msg webrtc.DataChannelMessage, peer *webRTC.SimpleWebRTCPeer) {
		log.Printf("Test received message: %s", string(msg.Data))
	})
	if err != nil {
		fmt.Printf("❌ Failed to create test WebRTC peer: %v\n", err)
		return
	}
	defer testPeer.Close()

	// Try to create an offer to test the process
	offer, err := testPeer.CreateOffer()
	if err != nil {
		fmt.Printf("❌ Failed to create WebRTC offer: %v\n", err)
		return
	}

	fmt.Printf("✅ WebRTC offer created successfully (length: %d chars)\n", len(offer))

	// Try to wait for ICE gathering
	fmt.Println("Waiting 5 seconds for ICE candidate gathering...")
	time.Sleep(5 * time.Second)

	fmt.Println("✅ Local WebRTC test completed - basic functionality working")
	fmt.Println("If downloads still fail, the issue is likely in the peer-to-peer signaling")
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

	log.Printf("Announcing file %s with CID %s to DHT...", info.Name(), fileCID.String())
	provideCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if err := c.dht.Provide(provideCtx, fileCID, true); err != nil {
		log.Printf(" - Warning: Failed to announce to DHT: %v", err)
	} else {
		log.Println(" - Successfully announced file to DHT")
	}

	fmt.Printf("✓ File '%s' is now being shared\n", info.Name())
	fmt.Printf(" CID: %s\n", fileCID.String())
	fmt.Printf(" Hash: %s\n", fileHashStr)
	fmt.Printf(" Size: %s\n", humanize.Bytes(uint64(info.Size())))
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
		fmt.Println(" - No files being shared.")
		return
	}
	fmt.Println("\n=== Your Shared Files ===")
	for _, file := range files {
		fmt.Printf("Name: %s\n", file.Filename)
		fmt.Printf(" CID: %s\n", file.CID)
		fmt.Printf(" Size: %s\n", humanize.Bytes(uint64(file.FileSize)))
		fmt.Printf(" Path: %s\n", file.FilePath)
		fmt.Println(" ---")
	}
}

func (c *Client) searchByText(q string) error {
	ctx := context.Background()
	matches, err := c.db.SearchByFilename(ctx, q)
	if err != nil {
		return err
	}
	if len(matches) == 0 {
		fmt.Printf("Searching for files containing '%s'...\n", q)
		fmt.Println("Note: Direct filename search requires content indexing.")
		fmt.Println("Try using the CID if you have it, or check with known peers.")
		return nil
	}
	fmt.Printf("Local index matches for '%s':\n", q)
	for _, m := range matches {
		fmt.Printf("- %s  CID:%s\n", m.Filename, m.CID)
	}
	return nil
}

func (c *Client) enhancedSearchByCID(cidStr string) error {
	fileCID, err := cid.Decode(cidStr)
	if err != nil {
		return fmt.Errorf("invalid CID: %w", err)
	}
	fmt.Printf("Searching for CID: %s\n", fileCID.String())
	providers, err := c.findProvidersWithTimeout(fileCID, 60*time.Second, MaxProviders)
	if err != nil {
		return fmt.Errorf("provider search failed: %w", err)
	}

	if len(providers) == 0 {
		fmt.Println("No providers found for this CID")
		fmt.Println("This could mean:")
		fmt.Println(" - The file is not being shared")
		fmt.Println(" - The provider is offline")
		fmt.Println(" - Network connectivity issues")
		fmt.Println(" - DHT routing problems")
		return nil
	}

	fmt.Printf("Found %d provider(s):\n", len(providers))
	for i, provider := range providers {
		fmt.Printf(" %d. %s\n", i+1, provider.ID)
		if c.host.Network().Connectedness(provider.ID) == network.Connected {
			fmt.Printf(" - Already connected\n")
		} else {
			fmt.Printf(" - Not connected\n")
		}
	}
	return nil
}

func (c *Client) findProvidersWithTimeout(id cid.Cid, timeout time.Duration, maxProviders int) ([]peer.AddrInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	providersChan := c.dht.FindProvidersAsync(ctx, id, maxProviders)
	var providers []peer.AddrInfo
	var totalFound int

	done := make(chan struct{})
	go func() {
		defer close(done)
		for provider := range providersChan {
			totalFound++
			if provider.ID != c.host.ID() {
				providers = append(providers, provider)
				fmt.Printf(" - Found provider %d: %s\n", len(providers), provider.ID)
				if len(providers) >= maxProviders {
					break
				}
			}
		}
	}()

	select {
	case <-done:
		fmt.Printf("Provider search completed. Found %d total providers, %d unique external providers\n",
			totalFound, len(providers))
	case <-time.After(timeout):
		fmt.Printf("Provider search timed out. Found %d providers so far\n", len(providers))
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

	fmt.Printf("Attempting to connect to peer %s...\n", peerInfo.ID)

	// For local network, use shorter timeout but multiple attempts
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := c.host.Connect(ctx, *peerInfo); err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}

	fmt.Printf(" - Successfully connected to peer %s\n", peerInfo.ID)

	// Store the peer addresses for future connections
	c.host.Peerstore().AddAddrs(peerInfo.ID, peerInfo.Addrs, time.Hour)

	return nil
}

func (c *Client) listConnectedPeers() {
	peers := c.host.Network().Peers()
	fmt.Printf("\n=== Connected Peers (%d) ===\n", len(peers))
	for _, peerID := range peers {
		conn := c.host.Network().ConnsToPeer(peerID)
		if len(conn) > 0 {
			fmt.Printf("Peer: %s\n", peerID)
			fmt.Printf(" Address: %s\n", conn[0].RemoteMultiaddr())
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

	fmt.Printf("Looking for providers of CID: %s\n", fileCID.String())
	providers, err := c.findProvidersWithTimeout(fileCID, 60*time.Second, MaxProviders)
	if err != nil {
		return fmt.Errorf("provider search failed: %w", err)
	}

	if len(providers) == 0 {
		return fmt.Errorf("no providers found")
	}

	fmt.Printf("Found %d providers. Getting file manifest...\n", len(providers))
	relayAddrStr := "/dns4/relay-torrentium-9ztp.onrender.com/tcp/443/wss/p2p/12D3KooWJeENaS7RuZLju4dGEZmK7ZJee4RMfBxo6aPfXBrWsuhw"

	var manifest controlMessage
	var firstPeer *webRTC.SimpleWebRTCPeer

	for _, p := range providers {
		// Try direct connection first
		peerConn, err := c.initiateWebRTCConnectionWithRetry(p.ID, 1)
		if err != nil {
			// Fallback: relay connection
			log.Println("🔁 Direct connection failed, trying relay...")

			// Build circuit address
			circuitStr := fmt.Sprintf("%s/p2p-circuit/p2p/%s", relayAddrStr, p.ID.String())
			circuitMaddr, err := multiaddr.NewMultiaddr(circuitStr)
			if err != nil {
				log.Printf("Invalid circuit multiaddr: %v", err)
				continue
			}
			targetInfo := peer.AddrInfo{ID: p.ID, Addrs: []multiaddr.Multiaddr{circuitMaddr}}

			if err := c.host.Connect(ctx, targetInfo); err != nil {
				log.Printf("❌ Relay dial failed: %v", err)
				continue
			}

			log.Printf("✅ Relay dial to %s successful", p.ID)

			// Now perform WebRTC handshake
			peerConn, err = c.initiateWebRTCConnectionWithRetry(p.ID, 1)
			if err != nil {
				log.Printf("⚠️ WebRTC connection via relay failed: %v", err)
				continue
			}
		}

		// If WebRTC connected, fetch manifest
		if peerConn != nil {
			manifest, err = c.requestManifest(peerConn, cidStr)
			if err == nil {
				firstPeer = peerConn
				break
			}
			peerConn.Close()
		}
	}

	if firstPeer == nil {
		return fmt.Errorf("failed to connect to any provider to get manifest")
	}

	// Prepare for file download
	downloadPath := fmt.Sprintf("%s.download", manifest.Filename)
	finalPath := manifest.Filename
	localFile, err := os.Create(downloadPath)
	if err != nil {
		firstPeer.Close()
		return fmt.Errorf("failed to create file: %w", err)
	}

	pieces, _ := c.db.GetPieces(ctx, cidStr)
	state := &DownloadState{
		File:           localFile,
		Manifest:       manifest,
		TotalPieces:    int(manifest.NumPieces),
		Pieces:         pieces,
		Completed:      make(chan bool, 1),
		Progress:       progressbar.DefaultBytes(manifest.TotalSize, "downloading..."),
		PieceStatus:    make([]bool, int(manifest.NumPieces)),
		PieceAssignees: make(map[int]peer.ID),
	}
	c.downloadsMux.Lock()
	c.activeDownloads[cidStr] = state
	c.downloadsMux.Unlock()

	// Parallel downloads
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
			if peerInfo.ID.String() == firstPeer.GetSignalingStream().Conn().RemotePeer().String() {
				peerConn = firstPeer
			} else {
				var connErr error
				peerConn, connErr = c.initiateWebRTCConnectionWithRetry(peerInfo.ID, 2)
				if connErr != nil {
					log.Printf("Chunk peer connect failed: %v", connErr)
					return
				}
				defer peerConn.Close()
			}
			c.downloadChunksFromPeer(peerConn, state, startPiece, endPiece)
		}(p, start, end)
	}

	wg.Wait()
	localFile.Close()

	if err := os.Rename(downloadPath, finalPath); err != nil {
		return fmt.Errorf("failed to rename file: %w", err)
	}

	fmt.Printf("\n✅ Download complete. File saved as %s\n", finalPath)
	return nil
}

func (c *Client) downloadChunksFromPeer(peer *webRTC.SimpleWebRTCPeer, state *DownloadState, startPiece, endPiece int) {
	for i := startPiece; i < endPiece; i++ {
		state.mu.Lock()
		if state.PieceStatus[i] {
			state.mu.Unlock()
			continue // Another peer finished it
		}
		state.PieceAssignees[i] = peer.GetSignalingStream().Conn().RemotePeer()
		state.mu.Unlock()

		req := controlMessage{
			Command: "REQUEST_PIECE",
			CID:     state.Manifest.CID,
			Index:   int64(i),
		}

		if err := peer.SendJSON(req); err != nil {
			log.Printf("Failed to request piece %d from %s: %v", i, peer.GetSignalingStream().Conn().RemotePeer(), err)
			return
		}
	}
}

// requestManifest is a helper function to get file metadata from a peer.
func (c *Client) requestManifest(peer *webRTC.SimpleWebRTCPeer, cidStr string) (controlMessage, error) {
	req := controlMessage{Command: "REQUEST_MANIFEST", CID: cidStr}
	if err := peer.SendJSON(req); err != nil {
		return controlMessage{}, err
	}

	// Wait for the manifest response
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
	// First, test ICE connectivity
	log.Printf("Testing ICE connectivity before attempting WebRTC connection...")
	if err := webRTC.TestICEConnectivity(); err != nil {
		log.Printf("ICE connectivity test failed: %v", err)
		log.Printf("Warning: WebRTC connections may fail due to network restrictions")
	} else {
		log.Printf("ICE connectivity test passed")
	}
	log.Printf("debug 1")

	var lastErr error
	for attempt := 1; attempt <= maxRetries; attempt++ {
		if attempt > 1 {
			log.Printf("debug 1.1")
			backoff := time.Duration(1<<uint(attempt-1)) * time.Second
			fmt.Printf("Retrying in %v (attempt %d/%d)...\n", backoff, attempt, maxRetries)
			time.Sleep(backoff)
			log.Printf("debug 2")
		}

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)

		var info peer.AddrInfo
		info.ID = targetPeerID
		if pinfo, err := c.dht.FindPeer(ctx, targetPeerID); err == nil && len(pinfo.Addrs) > 0 {
			info = pinfo
			fmt.Printf("Found %d addresses via DHT\n", len(info.Addrs))
		} else {
			log.Printf("debug 3")
			return nil, err
		}
		cancel()
		log.Printf("debug 4")

		if len(info.Addrs) == 0 {
			log.Printf("debug 5")
			lastErr = fmt.Errorf("peer %s has no known multiaddrs", targetPeerID)
			continue
		}

		c.host.Peerstore().AddAddrs(info.ID, info.Addrs, time.Hour)
		fmt.Println("relay ddebugging 1")
		fmt.Println(c.host.Peerstore())
		fmt.Println("relays debugging 2")

		if c.host.Network().Connectedness(info.ID) != network.Connected {
			fmt.Printf("Connecting to peer %s...\n", info.ID)
			// Use shorter timeout for local network connections
			connectCtx, connectCancel := context.WithTimeout(context.Background(), 20*time.Second)
			err := c.host.Connect(connectCtx, info)
			connectCancel()
			if err != nil {
				log.Printf("failed to connect to peer %s: %w", info.ID, err)
				fmt.Printf("DHT lookup failed: %v. This could be a network issue now trying connection using relays.\n", err)
				return nil, err
			}

			fmt.Printf("Successfully connected to peer %s\n", info.ID)
			// Shorter stabilization time for local network
			time.Sleep(1 * time.Second)
		}

		webrtcPeer, err := webRTC.NewSimpleWebRTCPeer(c.onDataChannelMessage)
		if err != nil {
			lastErr = err
			return nil, err
			// continue
		}

		offer, err := webrtcPeer.CreateOffer()
		if err != nil {
			webrtcPeer.Close()
			lastErr = err
			return nil, err
		}

		streamCtx, streamCancel := context.WithTimeout(context.Background(), 30*time.Second)
		s, err := c.host.NewStream(streamCtx, targetPeerID, p2p.SignalingProtocolID)
		streamCancel()
		if err != nil {
			webrtcPeer.Close()
			lastErr = err
			return nil, err
			
		}

		webrtcPeer.SetSignalingStream(s)

		encoder := json.NewEncoder(s)
		decoder := json.NewDecoder(s)

		// Send offer using new signaling message format
		offerMsg := map[string]string{
			"type": "offer",
			"data": offer,
		}

		log.Printf("Sending WebRTC offer to %s", targetPeerID)
		if err := encoder.Encode(offerMsg); err != nil {
			webrtcPeer.Close()
			lastErr = err
			continue
		}

		// Receive answer
		var answerMsg map[string]string
		if err := decoder.Decode(&answerMsg); err != nil {
			webrtcPeer.Close()
			lastErr = fmt.Errorf("failed to decode answer: %w", err)
			continue
		}

		if answerMsg["type"] == "error" {
			webrtcPeer.Close()
			lastErr = fmt.Errorf("peer returned error: %s", answerMsg["data"])
			continue
		}

		if answerMsg["type"] != "answer" {
			webrtcPeer.Close()
			lastErr = fmt.Errorf("expected answer, got: %s", answerMsg["type"])
			continue
		}

		log.Printf("Received WebRTC answer from %s", targetPeerID)
		if err := webrtcPeer.HandleAnswer(answerMsg["data"]); err != nil {
			webrtcPeer.Close()
			lastErr = err
			continue
		}

		log.Printf("Starting WebRTC connection establishment with %s (attempt %d/%d)", targetPeerID, attempt, maxRetries)

		// Add a short delay before starting WebRTC to let libp2p connection stabilize
		time.Sleep(1 * time.Second)

		// Use longer timeout for large files
		connectionTimeout := 90 * time.Second // Increased from 60s
		if err := webrtcPeer.WaitForConnection(connectionTimeout); err != nil {
			webrtcPeer.Close()
			lastErr = fmt.Errorf("WebRTC connection failed: %w", err)
			log.Printf("WebRTC connection failed with %s (attempt %d/%d): %v", targetPeerID, attempt, maxRetries, err)

			// If this was an ICE failure, provide specific guidance
			if strings.Contains(err.Error(), "ICE connection failed") {
				log.Printf("ICE connection failed - this indicates NAT traversal issues")
				log.Printf("Possible causes: firewall blocking, symmetric NAT, TURN server issues")
			}

			// For the last attempt, try a different approach
			if attempt == maxRetries {
				log.Printf("All WebRTC attempts failed. This could be due to:")
				log.Printf("1. Both peers behind symmetric NATs")
				log.Printf("2. Firewall blocking WebRTC traffic")
				log.Printf("3. TURN servers overloaded or unavailable")
				log.Printf("4. Network connectivity issues")
				log.Printf("5. Large file requiring more time for connection establishment")
			}
			continue
		}

		fmt.Printf("WebRTC connection established with %s\n", targetPeerID)
		c.peersMux.Lock()
		c.webRTCPeers[targetPeerID] = webrtcPeer
		c.peersMux.Unlock()

		// Send close message to signaling stream as connection is established
		go func() {
			time.Sleep(2 * time.Second) // Give connection time to stabilize
			if s := webrtcPeer.GetSignalingStream(); s != nil {
				encoder := json.NewEncoder(s)
				closeMsg := map[string]string{
					"type": "close",
					"data": "",
				}
				_ = encoder.Encode(closeMsg)
				s.Close()
			}
		}()

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
	if msg.IsString {
		var ctrl map[string]interface{}
		if err := json.Unmarshal(msg.Data, &ctrl); err == nil {
			switch ctrl["command"] {
			case "PIECE_DATA":
				cidStr := ctrl["cid"].(string)
				index := int(ctrl["index"].(float64))
				payload, _ := hex.DecodeString(ctrl["payload"].(string))

				c.downloadsMux.RLock()
				state, ok := c.activeDownloads[cidStr]
				c.downloadsMux.RUnlock()

				if ok {
					state.mu.Lock()
					defer state.mu.Unlock()

					if !state.PieceStatus[index] {
						piece := state.Pieces[index]
						state.File.WriteAt(payload, piece.Offset)
						state.PieceStatus[index] = true
						state.Progress.Add(len(payload))

						// Check for completion
						completedPieces := 0
						for _, s := range state.PieceStatus {
							if s {
								completedPieces++
							}
						}
						if completedPieces == state.TotalPieces {
							state.Completed <- true
						}
					}
				}
			case "DOWNLOAD_COMPLETE":
				log.Println("Received download completion signal")
				if w := peer.GetFileWriter(); w != nil {
					w.Close()
				}
				peer.Close()
			default:
				// Handle other string-based messages by attempting to unmarshal into controlMessage
				var controlMsg controlMessage
				if err := json.Unmarshal(msg.Data, &controlMsg); err == nil {
					c.handleControlMessage(controlMsg, peer)
				}
			}
		}
	}
}

func (c *Client) handleControlMessage(ctrl controlMessage, peer *webRTC.SimpleWebRTCPeer) {
	ctx := context.Background()
	switch ctrl.Command {
	case "REQUEST_FILE":
		go c.handleFileRequest(ctx, ctrl, peer)
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
	default:
		log.Printf("Unknown control command: %s", ctrl.Command)
	}
}

func (c *Client) handleFileRequest(ctx context.Context, ctrl controlMessage, peer *webRTC.SimpleWebRTCPeer) {
	fileInfo, exists := c.sharingFiles[ctrl.CID]
	if !exists {
		localFile, err := c.db.GetLocalFileByCID(ctx, ctrl.CID)
		if err != nil {
			log.Printf("File not found: %s", ctrl.CID)
			return
		}
		fileInfo = &FileInfo{
			FilePath: localFile.FilePath,
			Hash:     localFile.FileHash,
			Size:     localFile.FileSize,
			Name:     localFile.Filename,
		}
	}

	file, err := os.Open(fileInfo.FilePath)
	if err != nil {
		log.Printf("Error opening file %s: %v", fileInfo.FilePath, err)
		return
	}
	defer file.Close()

	fmt.Printf("Starting file transfer: %s (%s)\n", fileInfo.Name, humanize.Bytes(uint64(fileInfo.Size)))

	bar := progressbar.DefaultBytes(
		fileInfo.Size,
		fmt.Sprintf("uploading %s", fileInfo.Name),
	)

	buffer := make([]byte, MaxChunk)
	for {
		n, err := file.Read(buffer)
		if n > 0 {
			if sendErr := peer.SendRaw(buffer[:n]); sendErr != nil {
				log.Printf("Error sending data: %v", sendErr)
				break
			}
			bar.Add(n)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Printf("Error reading file: %v", err)
			break
		}
	}

	// Signal that the download is complete
	log.Println("\nFile upload finished, sending completion signal.")
	completionSignal := controlMessage{Command: "DOWNLOAD_COMPLETE"}
	if err := peer.SendJSON(completionSignal); err != nil {
		log.Printf("Failed to send download completion signal: %v", err)
	}

	// Give the signal a moment to send before closing
	time.Sleep(2 * time.Second)
	peer.Close()
}

func (c *Client) handlePieceRequest(ctx context.Context, ctrl controlMessage, peer *webRTC.SimpleWebRTCPeer) {
	pieces, err := c.db.GetPieces(ctx, ctrl.CID)
	if err != nil || int(ctrl.Index) >= len(pieces) {
		log.Printf("Invalid piece request for CID %s, index %d", ctrl.CID, ctrl.Index)
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
	buffer := make([]byte, piece.Size)
	_, err = file.ReadAt(buffer, piece.Offset)
	if err != nil {
		log.Printf("Failed to read piece %d: %v", ctrl.Index, err)
		return
	}

	// Serialize piece data and send
	dataMsg := map[string]interface{}{
		"command": "PIECE_DATA",
		"cid":     ctrl.CID,
		"index":   ctrl.Index,
		"payload": hex.EncodeToString(buffer),
	}

	if err := peer.SendJSON(dataMsg); err != nil {
		log.Printf("Failed to send piece %d: %v", ctrl.Index, err)
	}
}

func (c *Client) handleManifestRequest(ctx context.Context, ctrl controlMessage, peer *webRTC.SimpleWebRTCPeer) {
	localFile, err := c.db.GetLocalFileByCID(ctx, ctrl.CID)
	if err != nil {
		log.Printf("File not found for manifest: %s", ctrl.CID)
		return
	}

	pieces, err := c.db.GetPieces(ctx, ctrl.CID)
	if err != nil {
		log.Printf("Error getting pieces: %v", err)
		return
	}

	manifest := controlMessage{
		Command:   "MANIFEST",
		CID:       ctrl.CID,
		TotalSize: localFile.FileSize,
		HashHex:   localFile.FileHash,
		NumPieces: int64(len(pieces)),
		Filename:  localFile.Filename,
		// From:      string(c.host.ID()),
		// Target:    ctrl.From,
	}

	if err := peer.SendJSON(manifest); err != nil {
		log.Printf("Error sending manifest: %v", err)
	}
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
