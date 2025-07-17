package main

import (
	"bufio"
	"context"

	//"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	// "math"
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

// Define the libp2p protocol ID for WebRTC signaling
const WebRTCSignalingProtocolID = "/webrtc/sdp/1.0.0"

// Global variables
var peerConnection *webRTC.WebRTCPeer
var libp2pHost host.Host

// var name string

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

	libp2pHost, err = libp2p.New(
		libp2p.ListenAddrs(
			multiaddr.StringCast("/ip4/0.0.0.0/tcp/0"),
			multiaddr.StringCast("/ip4/0.0.0.0/udp/0/quic-v1"),
			multiaddr.StringCast("/ip6/::/tcp/0"),
			multiaddr.StringCast("/ip6/::/udp/0/quic-v1"),
		),
		libp2p.Identity(nil),
	)
	if err != nil {
		fmt.Printf("❌ Error creating libp2p host: %v\n", err)
		return
	}

	defer func() {
		if libp2pHost != nil {
			fmt.Println("Marking peer offline in tracker...")
			markErr := db.MarkPeerOffline(db.DB, libp2pHost.ID().String(), time.Now())
			if markErr != nil {
				log.Printf("Error marking self offline in tracker: %v\n", markErr)
			} else {
				fmt.Println("✅ Peer marked offline in tracker.")
			}
		}

		fmt.Println("Closing libp2p host...")
		if err := libp2pHost.Close(); err != nil {
			log.Printf("Error closing libp2p host: %v\n", err) 
		}
	}()

	fmt.Printf("✅ LibP2P Host created. Your Peer ID: %s\n", libp2pHost.ID().String())
	fmt.Println("Your Multiaddress (for others to connect directly if not using a tracker):")

	var allMultiaddrs []string
	var printableAddr string

	for i, addr := range libp2pHost.Addrs() {
		fullAddr := fmt.Sprintf("%s/p2p/%s", addr.String(), libp2pHost.ID().String())
		allMultiaddrs = append(allMultiaddrs, fullAddr)

		if i == 0 {
			printableAddr = fullAddr
		}
	}

	fmt.Println(" ", printableAddr)
	fmt.Println("📢 Connect to your tracker using this Peer ID to discover other peers.")

	placeholderIP := "0.0.0.0"

	id, err := db.InsertPeer(db.DB, libp2pHost.ID().String(), name, placeholderIP)
	if err != nil {
		fmt.Printf("Peer intestion to DB failed : %v\n", err)
		fmt.Printf("Your id is %v", id)
	}

	err = db.RegisterPeerAddresses(db.DB, libp2pHost.ID().String(), name, allMultiaddrs)
	if err != nil {
		fmt.Printf("Failed to register peer addresses : %v\n", err)
		fmt.Println("(Tracker cannot lookup the DB for this peer)")
	} else {
		fmt.Println("Your multiAddresses are stored for the Tracker")
	}

	go func() {
		ticker := time.NewTicker(10 * time.Minute)
		defer ticker.Stop()

		for range ticker.C {
			err := db.RegisterPeerAddresses(db.DB, libp2pHost.ID().String(), name, allMultiaddrs)
			if err != nil {
				fmt.Printf("Error updating peer addresses : %v", err)
			}

			err = db.CleanOldPeerAddresses(db.DB, 1*time.Hour)
			if err != nil {
				fmt.Printf("Error cleaning old peer addresses : %v", err)
			}
		}
	}()

	libp2pHost.SetStreamHandler(WebRTCSignalingProtocolID, handleLibp2pSignalingStream)

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

	scanner := bufio.NewScanner(os.Stdin)

	for {
		fmt.Println("\n📋 Available Commands:")
		fmt.Println("  offer <target_libp2p_peer_id> - Create connection offer to a peer")
		fmt.Println("  download <file>    - Download file from peer")
		fmt.Println("  addfile <filename> - Add a file to your shared list")
		fmt.Println("  listfiles          - List all available files on the network")
		fmt.Println("  listpeers          -  Displays a list of all peers, their peerID, name and status")
		fmt.Println("  status             - Show connection status")
		fmt.Println("  help               - Show instructions again")
		fmt.Println("  exit               - Quit application")
		fmt.Print("\n> ")

		if !scanner.Scan() {
			break
		}

		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}

		parts := strings.Fields(input)
		cmd := parts[0]

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
			AddFileCommand(filename)

		// case "connect":
		// 	if len(parts) < 2 {
		// 		fmt.Println("❌ Usage: connect <full_multiaddress>")
		// 		fmt.Println("💡 Example: connect /ip4/192.168.1.100/tcp/4001/p2p/Qm...ABCD")
		// 		continue
		// 	}
		// 	maddrStr := parts[1]
		// 	maddr, err := multiaddr.NewMultiaddr(maddrStr)
		// 	if err != nil {
		// 		fmt.Printf("❌ Invalid multiaddress: %v\n", err)
		// 		continue
		// 	}
		// 	pi, err := peer.AddrInfoFromP2pAddr(maddr)
		// 	if err != nil {
		// 		fmt.Printf("❌ Could not parse peer info from multiaddress: %v\n", err)
		// 		continue
		// 	}
		// 	libp2pHost.Peerstore().AddAddrs(pi.ID, pi.Addrs, time.Duration(math.MaxInt64))
		// 	fmt.Printf("✅ Added peer %s with address %s to peerstore.\n", pi.ID.String(), maddrStr)
		// 	fmt.Println("💡 You can now try 'offer' command with this peer's ID.")

		case "offer":
			if len(parts) < 2 {
				fmt.Println("❌ Usage: offer <target_libp2p_peer_id>")
				continue
			}
			targetPeerIDStr := parts[1]
			targetID, err := peer.Decode(targetPeerIDStr)
			if err != nil {
				fmt.Printf("❌ Invalid libp2p Peer ID: %v\n", err)
				continue
			}

			targetAddrs, err := db.LookupPeerAddress(db.DB, targetPeerIDStr)
			if err != nil {
				fmt.Printf("Could not find addresses for peer %s : %v\n", targetPeerIDStr, err)
				fmt.Println("Ensure the peer in connected to the network and in running.")
			}

			if len(targetAddrs) == 0 {
				fmt.Printf("No online addresses found for peer %s in the tracker.\n", targetPeerIDStr)
				fmt.Println("The peer might be offline or its addresses haven't propagated yet.")
				continue
			}

			for _, addrStr := range targetAddrs {
				mAddr, err := multiaddr.NewMultiaddr(addrStr)
				if err != nil {
					fmt.Printf("Invalid multiaddress from tracker '%s': %v\n", addrStr, err)
					continue
				}

				pi, err := peer.AddrInfoFromP2pAddr(mAddr)
				if err != nil {
					fmt.Printf("Could not parse peerID from multi Address %s : %v\n", addrStr, err)
					continue
				}

				if pi.ID.String() == targetID.String() {
					libp2pHost.Peerstore().AddAddrs(pi.ID, pi.Addrs, 5*time.Minute)
					fmt.Printf("Added address %s for peer %s from tracker to peerstore.\n", addrStr, pi.ID.String())
				} else {
					fmt.Printf("Skipping address '%s' as its Peer ID '%s' does not match target '%s'.\n", addrStr, pi.ID.String(), targetID.String())
				}

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
		

		case "listpeers": 
			peers, err := db.ListPeers(db.DB) 
			if err != nil {
				fmt.Printf("❌ Error listing peers: %v\n", err)
				continue
			}

			fmt.Println("\n--- Known Peers ---")
			if len(peers) == 0 {
				fmt.Println("No peers found in the tracker.")
			} else {
				for _, p := range peers {
					status := "Offline"
					if p.Online {
						status = "Online"
					}
					fmt.Printf("- Peer ID: %s\n  Name: %s\n  Status: %s\n\n", p.PeerID, p.Name, status)
				}
			}
			fmt.Println("-------------------")


		default:
			fmt.Printf("❌ Unknown command: %s\n", cmd)
			fmt.Println("💡 Type 'help' to see available commands")
		}
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

// processes incoming WebRTC signaling messages (offers/answers) over a libp2p stream.
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
			return
		}

		str = strings.TrimSpace(str)
		if str == "" {
			continue
		}

		parts := strings.SplitN(str, ":", 2)
		if len(parts) != 2 {
			fmt.Printf("Malformed signaling message received from %s: %s\n", s.Conn().RemotePeer().String(), str)
			continue
		}
		msgType := parts[0]
		data := parts[1] // This is the SDP string (Base64 encoded)

		switch msgType {
		case "OFFER":
			fmt.Printf("Received WebRTC offer from %s. Creating answer...\n", s.Conn().RemotePeer().String())

			decodedSDP, err := base64.StdEncoding.DecodeString(data)
			if err != nil {
				fmt.Printf("❌ Error decoding Base64 SDP offer from (%s): %v\n", s.Conn().RemotePeer().String(), err)
				continue
			}
			sdpString := string(decodedSDP)
			// fmt.Printf("DEBUG: Received (decoded) SDP data (length %d):\n%s\n", len(sdpString), sdpString)

			answer, err := peerConnection.CreateAnswer(sdpString)
			if err != nil {
				fmt.Printf("❌ Error creating answer for offer from %s: %v\n", s.Conn().RemotePeer().String(), err)
				_, writeErr := rw.WriteString(fmt.Sprintf("ERROR:%v\n", err))
				if writeErr != nil {
					fmt.Printf("Error sending error message: %v\n", writeErr)
				}
				rw.Flush()
				return
			}

			encodedAnswer := base64.StdEncoding.EncodeToString([]byte(answer))
			_, err = rw.WriteString(fmt.Sprintf("ANSWER:%s\n", encodedAnswer)) // Send encoded answer
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

			go func(remotePeerID peer.ID) {
				if err := peerConnection.WaitForConnection(30 * time.Second); err != nil {
					fmt.Printf("❌ WebRTC Connection timeout with peer %s: %v\n", remotePeerID.String(), err)
				} else {
					fmt.Printf("🎉 WebRTC Connection established with peer %s!\n", remotePeerID.String())
					fmt.Println("✅ You can now transfer files using the 'download' command")
				}
			}(s.Conn().RemotePeer())

		case "ANSWER":
			fmt.Printf("Received WebRTC answer from %s. Completing connection...\n", s.Conn().RemotePeer().String())
			// DECODE Base64 SDP
			decodedSDP, err := base64.StdEncoding.DecodeString(data)
			if err != nil {
				fmt.Printf("❌ Error decoding Base64 SDP answer from %s: %v\n", s.Conn().RemotePeer().String(), err)
				continue
			}
			sdpString := string(decodedSDP)
			// fmt.Printf("DEBUG: Received (decoded) SDP data (length %d):\n%s\n", len(sdpString), sdpString)

			err = peerConnection.SetAnswer(sdpString)
			if err != nil {
				fmt.Printf("❌ Error applying answer from %s: %v\n", s.Conn().RemotePeer().String(), err)
				return
			}
			fmt.Println("✅ Answer applied. WebRTC connection should be establishing.")

		case "ERROR":
			fmt.Printf("Received ERROR from %s during signaling: %s\n", s.Conn().RemotePeer().String(), data)

		default:
			fmt.Printf("Unknown signaling message type: %s from peer %s\n", msgType, s.Conn().RemotePeer().String())
		}
	}
}

// sendLibp2pOffer initiates the WebRTC offer process by sending an SDP offer over a libp2p stream.
func sendLibp2pOffer(ctx context.Context, h host.Host, targetPeerID peer.ID) {
	fmt.Println("🔄 Creating WebRTC offer...")
	offer, err := peerConnection.CreateOffer()
	if err != nil {
		fmt.Printf("❌ Error creating offer: %v\n", err)
		return
	}
	fmt.Printf("DEBUG: Generated Offer SDP (length %d):\n%s\n", len(offer), offer)

	encodedOffer := base64.StdEncoding.EncodeToString([]byte(offer))

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

	_, err = rw.WriteString(fmt.Sprintf("OFFER:%s\n", encodedOffer)) // Send encoded offer
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
	data := answerParts[1]

	decodedSDP, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		fmt.Printf("❌ Error decoding Base64 SDP answer from %s: %v\n", targetPeerID.String(), err)
		return
	}
	sdpString := string(decodedSDP)
	// fmt.Printf("DEBUG: Received (decoded) SDP data (length %d):\n%s\n", len(sdpString), sdpString)

	fmt.Printf("Received WebRTC answer from %s. Completing connection...\n", targetPeerID.String())
	err = peerConnection.SetAnswer(sdpString) // Use decoded SDP
	if err != nil {
		fmt.Printf("❌ Error applying answer from %s: %v\n", targetPeerID.String(), err)
		return
	}

	fmt.Println("⏳ Establishing WebRTC connection...")
	go func(remotePeerID peer.ID) {
		if err := peerConnection.WaitForConnection(30 * time.Second); err != nil {
			fmt.Printf("❌ WebRTC Connection timeout with peer %s: %v\n", remotePeerID.String(), err)
		} else {
			fmt.Printf("🎉 WebRTC Connection established with peer %s!\n", remotePeerID.String())
			fmt.Println("✅ You can now transfer files using the 'download' command")
		}
	}(targetPeerID)
}

// handleIncomingDataChannelMessage processes messages received on the WebRTC DataChannel.
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
			err := SendFile(p, filename)
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
				p.SetFileWriter(nil)
			}
		default:
			fmt.Printf("Received unknown command on data channel: %s\n", cmd)
		}
	} else {
		if p.GetFileWriter() != nil {
			if _, err := p.GetFileWriter().Write(msg.Data); err != nil {
				fmt.Printf("❌ Error writing to file: %v\n", err)
			}
		}
	}
}

// reads a file from disk and sends it in chunks over the WebRTC data channel.
func SendFile(p *webRTC.WebRTCPeer, filename string) error {
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

	cmdStart := fmt.Sprintf("FILE_START:%s:%d", encodedName, filesize)
	if err := p.SendTextData(cmdStart); err != nil {
		return fmt.Errorf("failed to send FILE_START command: %w", err)
	}

	buffer := make([]byte, 16*1024)
	for {
		n, err := file.Read(buffer)
		if err != nil && err != io.EOF {
			return fmt.Errorf("failed to read file chunk: %w", err)
		}
		if n == 0 {
			break
		}

		if err := p.SendBinaryData(buffer[:n]); err != nil {
			return fmt.Errorf("failed to send file chunk: %w", err)
		}
	}

	cmdEnd := fmt.Sprintf("FILE_END:%s", encodedName)
	if err := p.SendTextData(cmdEnd); err != nil {
		return fmt.Errorf("failed to send FILE_END command: %w", err)
	}

	fmt.Printf("✅ File '%s' sent successfully.\n", filename)
	return nil
}

// addFileCommand calculates file hash and size, then adds it to the database.
func AddFileCommand(filename string) {
	fileHash, filesize, err := webRTC.CalculateFileHash(filename)
	if err != nil {
		fmt.Printf("Error calculating hash for %s: %v\n", filename, err)
		return
	}
	err = db.AddFile(db.DB, fileHash, filename, filesize, libp2pHost.ID().String())
	if err != nil {
		fmt.Printf("Failed to add file %s to database: %v\n", filename, err)
	} else {
		fmt.Printf("✅ File '%s' added successfully and announced locally.\n", filename)
	}
}
