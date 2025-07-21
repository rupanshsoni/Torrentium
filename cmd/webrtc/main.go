// cmd/webrtc/main.go
package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/joho/godotenv"
	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/pion/webrtc/v3"

	"torrentium/db"
	"torrentium/p2p"
	"torrentium/torrentfile"
	torrentiumWebRTC "torrentium/webRTC"
)

type Client struct {
	host          host.Host
	trackerStream network.Stream
	encoder       *json.Encoder
	decoder       *json.Decoder
	peerName      string
	webRTCPeers   map[peer.ID]*torrentiumWebRTC.WebRTCPeer
	peersMux      sync.RWMutex
}

func main() {
	if err := godotenv.Load(); err != nil {
		log.Fatal("Unable to access .env file:", err)
	}

	h, err := libp2p.New()
	if err != nil {
		log.Fatal("Failed to create libp2p host:", err)
	}
	log.Printf("✅ Peer libp2p Host ID: %s", h.ID())

	setupGracefulShutdown(h)

	trackerMultiAddrStr := os.Getenv("TRACKER_ADDR")
	if trackerMultiAddrStr == "" {
		log.Fatal("TRACKER_ADDR environment variable is not set or .env file not found.")
	}

	trackerAddrInfo, err := peer.AddrInfoFromString(trackerMultiAddrStr)
	if err != nil {
		log.Fatal("Invalid TRACKER_ADDR in .env file:", err)
	}

	client := NewClient(h)
	p2p.RegisterSignalingProtocol(h, client.handleWebRTCOffer)

	if err := client.connectToTracker(*trackerAddrInfo); err != nil {
		log.Fatalf("Failed to connect to tracker: %v", err)
	}
	defer client.trackerStream.Close()

	client.commandLoop()
}

func NewClient(h host.Host) *Client {
	return &Client{
		host:        h,
		webRTCPeers: make(map[peer.ID]*torrentiumWebRTC.WebRTCPeer),
	}
}

func (c *Client) connectToTracker(trackerAddr peer.AddrInfo) error {
	fmt.Print("Enter your peer name: ")
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return errors.New("failed to read peer name")
	}
	c.peerName = scanner.Text()
	if c.peerName == "" {
		return errors.New("peer name cannot be empty")
	}

	// THE KEY FIX: Explicitly connect to the peer to add its address to the peerstore.
	// This solves the "no addresses" error.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := c.host.Connect(ctx, trackerAddr); err != nil {
		return fmt.Errorf("failed to connect to tracker's AddrInfo: %w", err)
	}
	log.Println("Successfully connected to tracker peer.")


	// Now open the stream
	s, err := c.host.NewStream(context.Background(), trackerAddr.ID, p2p.TrackerProtocolID)
	if err != nil {
		return fmt.Errorf("failed to open stream to tracker: %w", err)
	}
	c.trackerStream = s
	c.encoder = json.NewEncoder(s)
	c.decoder = json.NewDecoder(s)

	namePayload, _ := json.Marshal(c.peerName)
	msg := p2p.Message{Command: "NAME", Payload: namePayload}

	if err := c.encoder.Encode(msg); err != nil {
		return fmt.Errorf("failed to send name to tracker: %w", err)
	}

	var welcomeMsg p2p.Message
	if err := c.decoder.Decode(&welcomeMsg); err != nil {
		return fmt.Errorf("failed to read welcome message from tracker: %w", err)
	}
	log.Printf("✅ Tracker handshake complete. Welcome message: %s", welcomeMsg.Command)
	return nil
}

// ... All other functions (commandLoop, addFile, listFiles, get, etc.) remain the same as the last full version I provided ...
func (c *Client) commandLoop() {
	scanner := bufio.NewScanner(os.Stdin)
	printClientInstructions()
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
			printClientInstructions()
		case "add":
			if len(args) != 1 {
				err = errors.New("usage: add <filepath>")
			} else {
				err = c.addFile(args[0])
			}
		case "list":
			err = c.listFiles()

		case "listpeers":
			err = c.listPeers()

		case "get":
			if len(args) != 1 {
				err = errors.New("usage: get <file_id>")
			} else {
				err = c.get(args[0])
			}
		case "exit":
			return
		default:
			err = errors.New("unknown command")
		}
		if err != nil {
			log.Printf("Error: %v", err)
		}
	}
}

func (c *Client) addFile(filePath string) error {
	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return err
	}

	payload, _ := json.Marshal(p2p.AnnounceFilePayload{
		FileHash: fmt.Sprintf("%x", hasher.Sum(nil)),
		Filename: filepath.Base(filePath),
		FileSize: info.Size(),
	})
	if err := c.encoder.Encode(p2p.Message{Command: "ANNOUNCE_FILE", Payload: payload}); err != nil {
		return err
	}

	var resp p2p.Message
	if err := c.decoder.Decode(&resp); err != nil {
		return err
	}
	if resp.Command != "ACK" {
		return fmt.Errorf("tracker responded with error: %s", resp.Payload)
	}

	if err := torrentfile.CreateTorrentFile(filePath); err != nil {
		log.Printf("Warning: failed to create .torrent file: %v", err)
	}

	fmt.Printf("File '%s' announced successfully.\n", filepath.Base(filePath))
	return nil
}

func (c *Client) listFiles() error {
	if err := c.encoder.Encode(p2p.Message{Command: "LIST_FILES"}); err != nil {
		return err
	}
	var resp p2p.Message
	if err := c.decoder.Decode(&resp); err != nil {
		return err
	}
	if resp.Command != "FILE_LIST" {
		return fmt.Errorf("tracker responded with error: %s", resp.Payload)
	}

	var files []db.File
	if err := json.Unmarshal(resp.Payload, &files); err != nil {
		return err
	}
	if len(files) == 0 {
		fmt.Println("No files available on the tracker.")
		return nil
	}

	fmt.Println("\nAvailable Files:")
	for _, file := range files {
		fmt.Println("--------------------")
		fmt.Printf("  ID: %s\n  Name: %s\n  Size: %s\n", file.ID, file.Filename, torrentiumWebRTC.FormatFileSize(file.FileSize))
	}
	fmt.Println("--------------------")
	return nil
}


func (c *Client) listPeers() error {
	if err := c.encoder.Encode(p2p.Message{Command: "LIST_PEERS"}); err != nil {
		return err
	}
	var resp p2p.Message
	if err := c.decoder.Decode(&resp); err != nil {
		return err
	}
	if resp.Command != "PEER_LIST_ALL" {
		return fmt.Errorf("tracker responded with an error: %s", resp.Payload)
	}

	var peers []db.Peer
	if err := json.Unmarshal(resp.Payload, &peers); err != nil {
		return err
	}

	fmt.Println("\nOnline Peers:")
	fmt.Println("----------------------------------------")
	if len(peers) <= 1 {
		fmt.Println("You are the only peer currently online.")
	} else {
		for _, peer := range peers {
			// Don't list yourself
			if peer.PeerID == c.host.ID().String() {
				continue
			}
			fmt.Printf("  Name: %s\n  ID:   %s\n", peer.Name, peer.PeerID)
			fmt.Println("----------------------------------------")
		}
	}
	return nil
}


func (c *Client) get(fileIDStr string) error {
	fileID, err := uuid.Parse(fileIDStr)
	if err != nil {
		return fmt.Errorf("invalid file ID format: %w", err)
	}

	payload, _ := json.Marshal(p2p.GetPeersPayload{FileID: fileID})
	if err := c.encoder.Encode(p2p.Message{Command: "GET_PEERS_FOR_FILE", Payload: payload}); err != nil {
		return err
	}
	var resp p2p.Message
	if err := c.decoder.Decode(&resp); err != nil {
		return err
	}
	if resp.Command != "PEER_LIST" {
		return fmt.Errorf("tracker responded with error: %s", resp.Payload)
	}

	var peers []db.PeerFile
	if err := json.Unmarshal(resp.Payload, &peers); err != nil {
		return err
	}
	if len(peers) == 0 {
		return errors.New("no online peers found for this file")
	}

	fmt.Println("Found online peers:")
	for i, p := range peers {
		fmt.Printf("  [%d] Peer DB ID: %s (Score: %.2f)\n", i, p.PeerID, p.Score)
	}
	fmt.Print("Select a peer to download from (enter number): ")
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Scan()
	choice, err := strconv.Atoi(scanner.Text())
	if err != nil || choice < 0 || choice >= len(peers) {
		return errors.New("invalid selection")
	}
	selectedPeer := peers[choice]

	peerInfoPayload, _ := json.Marshal(p2p.GetPeerInfoPayload{PeerDBID: selectedPeer.PeerID})
	if err := c.encoder.Encode(p2p.Message{Command: "GET_PEER_INFO", Payload: peerInfoPayload}); err != nil {
		return err
	}
	if err := c.decoder.Decode(&resp); err != nil || resp.Command != "PEER_INFO" {
		return errors.New("could not retrieve peer's libp2p info")
	}
	var peerInfo db.Peer
	if err := json.Unmarshal(resp.Payload, &peerInfo); err != nil {
		return err
	}

	targetPeerID, err := peer.Decode(peerInfo.PeerID)
	if err != nil {
		return err
	}

	log.Println("Initiating WebRTC connection...")
	webRTCPeer, err := c.initiateWebRTCConnection(targetPeerID)
	if err != nil {
		return fmt.Errorf("WebRTC connection failed: %w", err)
	}
	c.addWebRTCPeer(targetPeerID, webRTCPeer)
	log.Println("✅ WebRTC connection established!")

	log.Printf("Requesting file %s from peer...", fileID)
	requestPayload := map[string]string{"command": "REQUEST_FILE", "file_id": fileID.String()}
	if err := webRTCPeer.Send(requestPayload); err != nil {
		return fmt.Errorf("failed to send file request: %w", err)
	}

	fmt.Println("File request sent. Waiting for transfer to complete...")
	return nil
}

func (c *Client) initiateWebRTCConnection(targetPeerID peer.ID) (*torrentiumWebRTC.WebRTCPeer, error) {
	s, err := c.host.NewStream(context.Background(), targetPeerID, p2p.SignalingProtocolID)
	if err != nil {
		return nil, err
	}
	defer s.Close()

	webRTCPeer, err := torrentiumWebRTC.NewWebRTCPeer(c.onDataChannelMessage)
	if err != nil {
		return nil, err
	}

	offer, err := webRTCPeer.CreateOffer()
	if err != nil {
		return nil, err
	}

	encoder := json.NewEncoder(s)
	if err := encoder.Encode(offer); err != nil {
		return nil, err
	}

	var answer string
	decoder := json.NewDecoder(s)
	if err := decoder.Decode(&answer); err != nil {
		return nil, err
	}

	if err := webRTCPeer.SetAnswer(answer); err != nil {
		return nil, err
	}

	if err := webRTCPeer.WaitForConnection(30 * time.Second); err != nil {
		return nil, err
	}
	return webRTCPeer, nil
}

func (c *Client) handleWebRTCOffer(offer, remotePeerIDStr string) (string, error) {
	remotePeerID, err := peer.Decode(remotePeerIDStr)
	if err != nil {
		return "", err
	}

	log.Printf("Handling incoming WebRTC offer from %s", remotePeerID)
	webRTCPeer, err := torrentiumWebRTC.NewWebRTCPeer(c.onDataChannelMessage)
	if err != nil {
		return "", err
	}

	answer, err := webRTCPeer.CreateAnswer(offer)
	if err != nil {
		return "", err
	}

	c.addWebRTCPeer(remotePeerID, webRTCPeer)
	return answer, nil
}

func (c *Client) onDataChannelMessage(msg webrtc.DataChannelMessage, p *torrentiumWebRTC.WebRTCPeer) {
	if msg.IsString {
		log.Printf("Received message: %s\n", string(msg.Data))
	} else {
		if writer := p.GetFileWriter(); writer != nil {
			if _, err := writer.Write(msg.Data); err != nil {
				log.Printf("Error writing file chunk: %v", err)
			}
		} else {
			log.Println("Received binary data but no file writer is active.")
		}
	}
}

func (c *Client) addWebRTCPeer(id peer.ID, p *torrentiumWebRTC.WebRTCPeer) {
	c.peersMux.Lock()
	defer c.peersMux.Unlock()
	c.webRTCPeers[id] = p
}

func setupGracefulShutdown(h host.Host) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-ch
		log.Println("Shutting down...")
		if err := h.Close(); err != nil {
			log.Printf("Error closing libp2p host: %v", err)
		}
		os.Exit(0)
	}()
}

func printClientInstructions() {
	fmt.Println(`
📖 Torrentium Client Commands:
  help          - Show this help message.
  add <path>    - Announce a local file to the tracker.
  list          - List all files available on the tracker.
  listpeers     - List all currently online peers.
  get <file_id> - Find and download a file from a peer.
  exit          - Shutdown the client.`)
}