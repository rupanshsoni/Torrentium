// p2p/protocol.go
package p2p

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"strings"

	"torrentium/tracker"

	"github.com/google/uuid"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
)

const TrackerProtocolID = "/torrentium/tracker/1.0"

// Message represents a command sent between client and tracker
type Message struct {
	Command string          `json:"command"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// Payloads for specific commands
type AnnounceFilePayload struct {
	FileHash string `json:"file_hash"`
	Filename string `json:"filename"`
	FileSize int64  `json:"file_size"`
}

type GetPeersPayload struct {
	FileID uuid.UUID `json:"file_id"`
}

type GetPeerInfoPayload struct {
	PeerDBID uuid.UUID `json:"peer_db_id"`
}

func RegisterTrackerProtocol(h host.Host, t *tracker.Tracker) {
	h.SetStreamHandler(TrackerProtocolID, func(s network.Stream) {
		log.Printf("New peer connected: %s", s.Conn().RemotePeer())
		ctx := context.Background() // Use a more specific context if available
		defer s.Close()
		defer t.RemovePeer(ctx, s.Conn().RemotePeer().String()) // Mark peer offline on disconnect

		if err := handleStream(ctx, s, t); err != nil {
			if err != io.EOF {
				log.Printf("Error handling stream for peer %s: %v", s.Conn().RemotePeer(), err)
			}
		}
	})
}

func handleStream(ctx context.Context, s network.Stream, t *tracker.Tracker) error {
	decoder := json.NewDecoder(s)
	encoder := json.NewEncoder(s)
	remotePeerID := s.Conn().RemotePeer().String()

	// Initial Handshake
	var nameMsg Message
	if err := decoder.Decode(&nameMsg); err != nil {
		return err
	}
	var peerName string
	if err := json.Unmarshal(nameMsg.Payload, &peerName); err != nil || peerName == "" {
		return encoder.Encode(Message{Command: "ERROR", Payload: json.RawMessage(`"Invalid name"`)})
	}

	ip := strings.Split(s.Conn().RemoteMultiaddr().String(), "/")[2]
	if err := t.AddPeer(ctx, remotePeerID, peerName, ip); err != nil {
		return encoder.Encode(Message{Command: "ERROR", Payload: json.RawMessage(`"Failed to register with tracker"`)})
	}

	welcomePayload, _ := json.Marshal(t.ListPeers())
	encoder.Encode(Message{Command: "WELCOME", Payload: welcomePayload})

	// Command Loop
	for {
		var msg Message
		if err := decoder.Decode(&msg); err != nil {
			return err
		}

		log.Printf("Received command '%s' from peer %s", msg.Command, remotePeerID)

		var response Message
		switch msg.Command {
		case "ANNOUNCE_FILE":
			var p AnnounceFilePayload
			if err := json.Unmarshal(msg.Payload, &p); err != nil {
				response.Command = "ERROR"
				continue
			}
			err := t.AddFileWithPeer(ctx, p.FileHash, p.Filename, p.FileSize, remotePeerID)
			if err != nil {
				response.Command = "ERROR"
			} else {
				response.Command = "ACK"
			}

		case "LIST_FILES":
			files, err := t.GetAllFiles(ctx)
			if err != nil {
				response.Command = "ERROR"
			} else {
				response.Command = "FILE_LIST"
				response.Payload, _ = json.Marshal(files)
			}

		case "GET_PEERS_FOR_FILE":
			var p GetPeersPayload
			if err := json.Unmarshal(msg.Payload, &p); err != nil {
				response.Command = "ERROR"
				continue
			}
			peers, err := t.GetOnlinePeersForFile(ctx, p.FileID)
			if err != nil {
				response.Command = "ERROR"
			} else {
				response.Command = "PEER_LIST"
				response.Payload, _ = json.Marshal(peers)
			}

		case "GET_PEER_INFO":
			var p GetPeerInfoPayload
			if err := json.Unmarshal(msg.Payload, &p); err != nil {
				response.Command = "ERROR"
				continue
			}
			peerInfo, err := t.GetPeerInfoByDBID(ctx, p.PeerDBID)
			if err != nil {
				response.Command = "ERROR"
			} else {
				response.Command = "PEER_INFO"
				response.Payload, _ = json.Marshal(peerInfo)
			}

		case "LIST_PEERS":
			peers, err := t.GetOnlinePeers(ctx)
			if err != nil {
				response.Command = "ERROR"
			} else {
				response.Command = "PEER_LIST_ALL"
				response.Payload, _ = json.Marshal(peers)
			}
		default:
			response.Command = "ERROR"
			response.Payload, _ = json.Marshal("Unknown command")
		}

		if err := encoder.Encode(response); err != nil {
			return err
		}
	}
}
