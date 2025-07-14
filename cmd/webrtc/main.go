package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
	"github.com/pion/webrtc/v3" 

	"torrentium/db"        
	"torrentium/webRTC"    
	
)


const WebRTCSignalingProtocolID = "/webrtc/sdp/1.0.0"

// Global variables
var peerConnection *webRTC.WebRTCPeer 
var libp2pHost host.Host

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel() 

	db.InitDB() 
	fmt.Println("🔥 Torrentium - P2P File Sharing")
	fmt.Println("==================================")
	fmt.Println("Direct peer-to-peer file sharing that works through firewalls!")
	fmt.Println()

	fmt.Print("Enter your name: ")
	reader := bufio.NewReader(os.Stdin)
	name, err := reader.ReadString('\n')
	if err != nil {
		fmt.Printf("Error reading name: %v\n", err)
		return
	}
	name = strings.TrimSpace(name)

	webRTC.PrintInstructions()

	// LibP2P Host Initialization 
	// Listen on a random available port (0) for TCP
	libp2pHost, err = libp2p.New(
		libp2p.ListenAddrs(
            multiaddr.StringCast("/ip4/0.0.0.0/tcp/0"),
            multiaddr.StringCast("/ip4/0.0.0.0/udp/0/quic-v1"), // Add QUIC for better NAT traversal
            multiaddr.StringCast("/ip6/::/tcp/0"),
            multiaddr.StringCast("/ip6/::/udp/0/quic-v1"),
        ),
		libp2p.Identity(nil), // Generates a new private key if none provided
	)
	if err != nil {
		fmt.Printf("❌ Error creating libp2p host: %v\n", err)
		return
	}
	defer func() {
		fmt.Println("Closing libp2p host...")
		if err := libp2pHost.Close(); err != nil {
			fmt.Printf("Error closing libp2p host: %v\n", err)
		}
	}()

	fmt.Printf("✅ LibP2P Host created. Your Peer ID: %s\n", libp2pHost.ID().String())
	fmt.Println("Your Multiaddrs (for others to connect directly if not using a tracker):")
	for _, addr := range libp2pHost.Addrs() {
		fmt.Printf("  - %s/p2p/%s\n", addr.String(), libp2pHost.ID().String())
	}
	fmt.Println("📢 Connect to your tracker using this Peer ID to discover other peers.")

	libp2pHost.SetStreamHandler(WebRTCSignalingProtocolID, handleLibp2pSignalingStream)

	//  WebRTC Peer Initialization
	peerConnection, err = webRTC.NewWebRTCPeer(handleIncomingDataChannelMessage)
	if err != nil {
		fmt.Printf("❌ Error creating WebRTC peer: %v\n", err)
		return
	}
	defer func() {
		fmt.Println("Closing WebRTC peer connection...")
		if err := peerConnection.Close(); err != nil {
			fmt.Printf("Error closing WebRTC peer: %v\n", err)
		}
	}()

	// --- Command Line Interface ---
	scanner := bufio.NewScanner(os.Stdin)

	for {
		fmt.Println("\n📋 Available Commands:")
		fmt.Println(" offer <target_libp2p_peer_id> - Create connection offer to a peer")
		fmt.Println(" download <file> - Download file from peer")
		fmt.Println(" addfile <filename> - Add a file to your shared list")
		fmt.Println(" listfiles - List all available files on the network")
		fmt.Println(" status - Show connection status")
		fmt.Println(" help - Show instructions again")
		fmt.Println(" exit - Quit application")
		fmt.Print("\n> ")

		if !scanner.Scan() {
			break // EOF or error
		}

		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}

		// Parse command
		parts := strings.Fields(input)
		cmd := parts[0]

		// Handle commands
		switch cmd {
		case "exit", "quit", "q":
			fmt.Println("👋 Goodbye!")
			return

		case "help", "instructions":
			webRTC.PrintInstructions()

		case "status":
			if peerConnection.IsConnected() {
				fmt.Println("✅ Status: Connected and ready to transfer files")
			} else {
				fmt.Println("⏳ Status: Not connected yet")
			}

		case "addfile":
			if len(parts) < 2 {
				fmt.Println("❌ Usage: addfile <filename>")
				continue
			}
			filename := parts[1]
			addFileCommand(filename)

		case "offer":
			if len(parts) < 2 {
				fmt.Println("❌ Usage: offer <target_libp2p_peer_id>")
				continue
			}
			targetPeerIDStr := parts[1]
			// Decode the target Peer ID string to a libp2p peer.ID type
			targetID, err := peer.Decode(targetPeerIDStr)
			if err != nil {
				fmt.Printf("❌ Invalid libp2p Peer ID: %v\n", err)
				continue
			}
			sendLibp2pOffer(ctx, libp2pHost, targetID)

		case "download":
			if len(parts) != 2 {
				fmt.Println("❌ Usage: download <filename>")
				fmt.Println("💡 Example: download hello.txt")
				continue
			}
			filename := parts[1]
			handleDownloadCommand(filename)

		case "listfiles":
			db.ListAvailableFiles(db.DB)

		default:
			fmt.Printf("❌ Unknown command: %s\n", cmd)
			fmt.Println("💡 Type 'help' to see available commands")
		}
	}
}

// addFileCommand calculates file hash and size, then adds it to the database.
func addFileCommand(filename string) {
	fileHash, filesize, err := calculateFileHash(filename)
	if err != nil {
		fmt.Printf("Error calculating hash for %s: %v\n", filename, err)
		return
	}
	// Use libp2pHost.ID().String() as the peer ID for the database
	err = db.AddFile(db.DB, fileHash, filename, filesize, libp2pHost.ID().String())
	if err != nil {
		fmt.Printf("Failed to add file %s to database: %v\n", filename, err)
	} else {
		fmt.Printf("✅ File '%s' added successfully and announced locally.\n", filename)
	}
}

// handleDownloadCommand requests a file from the connected WebRTC peer.
func handleDownloadCommand(filename string) {
	if !peerConnection.IsConnected() {
		fmt.Println("❌ Not connected to any peer")
		fmt.Println("💡 Complete the connection setup first using 'offer' command.")
		return
	}

	fmt.Printf("📥 Requesting file: %s\n", filename)
	err := peerConnection.RequestFile(filename)
	if err != nil {
		fmt.Printf("❌ Error requesting file: %v\n", err)
		return
	}

	fmt.Println("⏳ File request sent. Waiting for peer to send the file...")
	fmt.Println("💡 The file will be saved with 'downloaded_' prefix when received.")
}

// handleLibp2pSignalingStream processes incoming WebRTC signaling messages (offers/answers) over a libp2p stream.
func handleLibp2pSignalingStream(s network.Stream) {
	defer func() {
		fmt.Printf("Closing signaling stream from %s\n", s.Conn().RemotePeer().String())
		s.Close()
	}()

	fmt.Printf("\n📢 Received signaling stream from peer %s\n", s.Conn().RemotePeer().String())
	rw := bufio.NewReadWriter(bufio.NewReader(s), bufio.NewWriter(s))

	for {
		str, err := rw.ReadString('\n')
		if err != nil {
			if err != io.EOF {
				fmt.Printf("Error reading from libp2p signaling stream (%s): %v\n", s.Conn().RemotePeer().String(), err)
			}
			return // Stream closed or error
		}

		str = strings.TrimSpace(str)
		if str == "" {
			continue
		}

		// Expected format: "TYPE:DATA"
		parts := strings.SplitN(str, ":", 2)
		if len(parts) != 2 {
			fmt.Printf("Malformed signaling message received from %s: %s\n", s.Conn().RemotePeer().String(), str)
			continue
		}
		msgType := parts[0]
		data := parts[1]

		switch msgType {
		case "OFFER":
			fmt.Printf("Received WebRTC offer from %s. Creating answer...\n", s.Conn().RemotePeer().String())
			answer, err := peerConnection.CreateAnswer(data) // Use global WebRTCPeer `peerConnection`
			if err != nil {
				fmt.Printf("❌ Error creating answer for offer from %s: %v\n", s.Conn().RemotePeer().String(), err)
				// Attempt to send an error back, if possible
				_, writeErr := rw.WriteString(fmt.Sprintf("ERROR:%v\n", err))
				if writeErr != nil {
					fmt.Printf("Error sending error message: %v\n", writeErr)
				}
				rw.Flush()
				return
			}
			// Send answer back over the same libp2p stream
			_, err = rw.WriteString(fmt.Sprintf("ANSWER:%s\n", answer))
			if err != nil {
				fmt.Printf("❌ Error sending answer to %s: %v\n", s.Conn().RemotePeer().String(), err)
				return
			}
			err = rw.Flush()
			if err != nil {
				fmt.Printf("❌ Error flushing answer to %s: %v\n", s.Conn().RemotePeer().String(), err)
				return
			}
			fmt.Printf("✅ Answer sent to peer %s. Waiting for WebRTC connection...\n", s.Conn().RemotePeer().String())

			// Wait for WebRTC connection to establish in a goroutine
			go func(remotePeerID peer.ID) {
				if err := peerConnection.WaitForConnection(30 * time.Second); err != nil {
					fmt.Printf("❌ WebRTC Connection timeout with peer %s: %v\n", remotePeerID.String(), err)
				} else {
					fmt.Printf("🎉 WebRTC Connection established with peer %s!\n", remotePeerID.String())
					fmt.Println("✅ You can now transfer files using the 'download' command")
				}
			}(s.Conn().RemotePeer()) // Pass the remote peer ID

		case "ANSWER":
			fmt.Printf("Received WebRTC answer from %s. Completing connection...\n", s.Conn().RemotePeer().String())
			err := peerConnection.SetAnswer(data) // Use global WebRTCPeer `peerConnection`
			if err != nil {
				fmt.Printf("❌ Error applying answer from %s: %v\n", s.Conn().RemotePeer().String(), err)
				return
			}
			fmt.Println("✅ Answer applied. WebRTC connection should be establishing.")
			// The `sendLibp2pOffer` function (the offerer) is already waiting for this connection.
			// No additional WaitForConnection needed here.

		case "ERROR":
			fmt.Printf("Received ERROR from %s during signaling: %s\n", s.Conn().RemotePeer().String(), data)

		// ICE candidates can also be exchanged here if not handled by WebRTC itself
		// case "ICE_CANDIDATE":
		//     ... add to peerConnection ...

		default:
			fmt.Printf("Unknown signaling message type: %s from peer %s\n", msgType, s.Conn().RemotePeer().String())
		}
	}
}

// sendLibp2pOffer initiates the WebRTC offer process by sending an SDP offer over a libp2p stream.
func sendLibp2pOffer(ctx context.Context, h host.Host, targetPeerID peer.ID) {
	fmt.Println("🔄 Creating WebRTC offer...")
	offer, err := peerConnection.CreateOffer() // Use global WebRTCPeer `peerConnection`
	if err != nil {
		fmt.Printf("❌ Error creating offer: %v\n", err)
		return
	}

	// Open a new stream to the target peer using our signaling protocol
	s, err := h.NewStream(ctx, targetPeerID, WebRTCSignalingProtocolID)
	if err != nil {
		fmt.Printf("❌ Failed to open libp2p stream to %s: %v\n", targetPeerID.String(), err)
		return
	}
	defer func() {
		fmt.Printf("Closing signaling stream to %s\n", targetPeerID.String())
		s.Close()
	}()

	rw := bufio.NewReadWriter(bufio.NewReader(s), bufio.NewWriter(s))

	// Send the offer
	_, err = rw.WriteString(fmt.Sprintf("OFFER:%s\n", offer))
	if err != nil {
		fmt.Printf("❌ Failed to send offer to %s: %v\n", targetPeerID.String(), err)
		return
	}
	err = rw.Flush()
	if err != nil {
		fmt.Printf("❌ Failed to flush offer to %s: %v\n", targetPeerID.String(), err)
		return
	}
	fmt.Printf("✅ Offer sent to peer %s. Waiting for their answer...\n", targetPeerID.String())

	// Wait for the answer on the same stream
	answerStr, err := rw.ReadString('\n')
	if err != nil {
		fmt.Printf("❌ Error reading answer from %s: %v\n", targetPeerID.String(), err)
		return
	}
	answerStr = strings.TrimSpace(answerStr)

	answerParts := strings.SplitN(answerStr, ":", 2)
	if len(answerParts) != 2 || answerParts[0] != "ANSWER" {
		fmt.Printf("Malformed answer received from %s: %s\n", targetPeerID.String(), answerStr)
		return
	}
	answer := answerParts[1]

	fmt.Printf("Received WebRTC answer from %s. Completing connection...\n", targetPeerID.String())
	err = peerConnection.SetAnswer(answer)
	if err != nil {
		fmt.Printf("❌ Error applying answer from %s: %v\n", targetPeerID.String(), err)
		return
	}

	fmt.Println("⏳ Establishing WebRTC connection...")
	// Wait for WebRTC connection to establish in a goroutine
	go func(remotePeerID peer.ID) {
		if err := peerConnection.WaitForConnection(30 * time.Second); err != nil {
			fmt.Printf("❌ WebRTC Connection timeout with peer %s: %v\n", remotePeerID.String(), err)
		} else {
			fmt.Printf("🎉 WebRTC Connection established with peer %s!\n", remotePeerID.String())
			fmt.Println("✅ You can now transfer files using the 'download' command")
		}
	}(targetPeerID) // Pass the target peer ID
}

// handleIncomingDataChannelMessage processes messages received on the WebRTC DataChannel.
// This function acts as the unified handler for incoming data, whether it's commands or file chunks.
func handleIncomingDataChannelMessage(msg webrtc.DataChannelMessage, p *webRTC.WebRTCPeer) {
	if msg.IsString {
		cmd, encodedFilename, filesizeStr := webRTC.ParseCommand(string(msg.Data))
		filenameBytes, _ := base64.StdEncoding.DecodeString(encodedFilename)
		filename := string(filenameBytes)

		var filesize int64 
		if filesizeStr != "" {
			var err error
			filesize, err = strconv.ParseInt(filesizeStr, 10, 64)
			if err != nil {
				fmt.Printf("❌ Error parsing filesize '%s': %v\n", filesizeStr, err)
				return
			}
		}

		switch cmd {
		case "REQUEST_FILE":
			fmt.Printf("⬆️ Received request for file: %s\n", filename)
			err := sendFile(p, filename) // Call function to send the file
			if err != nil {
				fmt.Printf("❌ Error sending file '%s': %v\n", filename, err)
			}

		case "FILE_START":
			file, err := os.Create("downloaded_" + filename)
			if err != nil {
				fmt.Printf("❌ Failed to create file: %v\n", err)
				return
			}
			p.SetFileWriter(file)
			fmt.Printf("📁 Receiving file: %s (Size: %s)\n", filename, webRTC.FormatFileSize(filesize))

		case "FILE_END":
			if p.GetFileWriter() != nil {
				p.GetFileWriter().Close()
				fmt.Println("✅ File received successfully")
				p.SetFileWriter(nil) // Reset writer
			}
		default:
			fmt.Printf("Received unknown command on data channel: %s\n", cmd)
		}
	} else {
		// This is binary data (file chunks)
		if p.GetFileWriter() != nil {
			if _, err := p.GetFileWriter().Write(msg.Data); err != nil {
				fmt.Printf("❌ Error writing to file: %v\n", err)
			}
		}
	}
}

// sendFile reads a file from disk and sends it in chunks over the WebRTC data channel.
func sendFile(p *webRTC.WebRTCPeer, filename string) error {
	file, err := os.Open(filename)
	if err != nil {
		return fmt.Errorf("could not open file '%s': %w", filename, err)
	}
	defer file.Close()

	fileInfo, err := file.Stat()
	if err != nil {
		return fmt.Errorf("could not get file info for '%s': %w", filename, err)
	}

	filesize := fileInfo.Size()
	encodedName := base64.StdEncoding.EncodeToString([]byte(filename))

	// Send FILE_START command: COMMAND:FILENAME_ENCODED:FILESIZE
	cmdStart := fmt.Sprintf("FILE_START:%s:%d", encodedName, filesize)
	if err := p.SendTextData(cmdStart); err != nil {
		return fmt.Errorf("failed to send FILE_START command: %w", err)
	}

	// Send file chunks
	buffer := make([]byte, 16*1024) // 16KB buffer size, common for data channels
	for {
		n, err := file.Read(buffer)
		if err != nil && err != io.EOF {
			return fmt.Errorf("failed to read file chunk: %w", err)
		}
		if n == 0 {
			break // End of file
		}

		if err := p.SendBinaryData(buffer[:n]); err != nil {
			return fmt.Errorf("failed to send file chunk: %w", err)
		}
	}

	// Send FILE_END command: COMMAND:FILENAME_ENCODED
	cmdEnd := fmt.Sprintf("FILE_END:%s", encodedName)
	if err := p.SendTextData(cmdEnd); err != nil {
		return fmt.Errorf("failed to send FILE_END command: %w", err)
	}

	fmt.Printf("✅ File '%s' sent successfully.\n", filename)
	return nil
}

// calculateFileHash computes the SHA256 hash and size of a given file.
func calculateFileHash(filename string) (string, int64, error) {
	file, err := os.Open(filename)
	if err != nil {
		return "", 0, fmt.Errorf("could not open file: %w", err)
	}
	defer file.Close()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", 0, fmt.Errorf("could not hash file: %w", err)
	}
	fileInfo, err := file.Stat()
	if err != nil {
		return "", 0, fmt.Errorf("could not get file info: %w", err)
	}
	return fmt.Sprintf("%x", hasher.Sum(nil)), fileInfo.Size(), nil
}

// readInput is a utility function to read a line from stdin.
func readInput() string {
	reader := bufio.NewReader(os.Stdin)
	input, _ := reader.ReadString('\n')
	return strings.TrimSpace(input)
}
