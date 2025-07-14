package webRTC

import (
	"encoding/base64"
	"fmt"

	"os"

	"sync"
	"time"

	"github.com/pion/webrtc/v3"
)

// WebRTCPeer represents a WebRTC peer connection.
type WebRTCPeer struct {
	connection      *webrtc.PeerConnection
	dataChannel     *webrtc.DataChannel // The primary data channel for file transfer
	fileWriter      *os.File            // Current file being written to (for downloads)
	connected       bool                // Connection status
	connectedMu     sync.RWMutex        // Mutex for connected status
	connectionReady chan struct{}       // Channel to signal when connection is established
}

// NewWebRTCPeer creates a new WebRTCPeer instance.
func NewWebRTCPeer(onDataChannelMessage func(webrtc.DataChannelMessage, *WebRTCPeer)) (*WebRTCPeer, error) {
	// WebRTC configuration (STUN server for NAT traversal)
	config := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{URLs: []string{"stun:stun.l.google.com:19302"}},
		},
	}

	// Create a new PeerConnection
	conn, err := webrtc.NewPeerConnection(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create PeerConnection: %w", err)
	}

	peer := &WebRTCPeer{
		connection:      conn,
		connectionReady: make(chan struct{}),
	}

	// Set up handlers for ICE connection state changes
	conn.OnICEConnectionStateChange(func(connectionState webrtc.ICEConnectionState) {
		fmt.Printf("ICE Connection State has changed to: %s\n", connectionState.String())
		peer.connectedMu.Lock()
		defer peer.connectedMu.Unlock()
		if connectionState == webrtc.ICEConnectionStateConnected || connectionState == webrtc.ICEConnectionStateCompleted {
			if !peer.connected {
				peer.connected = true
				// Close the channel to signal that connection is ready
				// Only close if it hasn't been closed already
				select {
				case <-peer.connectionReady:
					// Channel already closed, do nothing
				default:
					close(peer.connectionReady)
				}
			}
		} else if connectionState == webrtc.ICEConnectionStateFailed || connectionState == webrtc.ICEConnectionStateDisconnected {
			peer.connected = false
			// If connection drops, prepare for potential re-establishment
			// Re-initialize connectionReady channel
			peer.connectionReady = make(chan struct{})
		}
	})

	// Set up handler for signaling state changes
	conn.OnSignalingStateChange(func(state webrtc.SignalingState) {
		fmt.Printf("Signaling State has changed to: %s\n", state.String())
	})

	// Set up handler for incoming data channels (for the answering peer)
	conn.OnDataChannel(func(dc *webrtc.DataChannel) {
		fmt.Printf("New DataChannel %s (ID: %d) received!\n", dc.Label(), dc.ID())
		peer.dataChannel = dc // Assign the incoming data channel

		dc.OnOpen(func() {
			fmt.Printf("✅ Data channel '%s' opened\n", dc.Label())
			peer.connectedMu.Lock()
			peer.connected = true
			if peer.connectionReady != nil {
				select {
				case <-peer.connectionReady:
					// Already closed, fine.
				default:
					close(peer.connectionReady) // Signal that connection is ready
				}
			}
			peer.connectedMu.Unlock()
		})

		dc.OnMessage(func(msg webrtc.DataChannelMessage) {
			onDataChannelMessage(msg, peer) // Call the provided handler
		})

		dc.OnClose(func() {
			fmt.Printf("❌ Data channel '%s' closed\n", dc.Label())
			peer.connectedMu.Lock()
			peer.connected = false
			peer.connectedMu.Unlock()
			if peer.fileWriter != nil {
				peer.fileWriter.Close()
				peer.fileWriter = nil
			}
		})
	})

	// Create a reliable data channel for the offerer.
	// This channel will be created by the peer initiating the connection.
	// The `OnDataChannel` callback above handles the remote side when they open one.
	ordered := true // Explicitly define boolean for pointer
	dc, err := conn.CreateDataChannel("file-transfer", &webrtc.DataChannelInit{
		Ordered: &ordered, // Use the address of a boolean variable
		// `Reliable` field does not exist. Reliability is implied by `Ordered: true`
		// along with default SCTP settings. If you need to tune reliability/unreliability,
		// use MaxRetransmits or MaxPacketLifeTime.
		// For example, for unreliable: MaxRetransmits: webrtc.Uint16(0)
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create data channel: %w", err)
	}
	peer.dataChannel = dc // Assign this created data channel as the primary

	dc.OnOpen(func() {
		fmt.Printf("✅ Data channel '%s' (created by us) opened\n", dc.Label())
		peer.connectedMu.Lock()
		peer.connected = true
		if peer.connectionReady != nil {
			select {
			case <-peer.connectionReady:
				// Already closed, fine.
			default:
				close(peer.connectionReady) // Signal that connection is ready
			}
		}
		peer.connectedMu.Unlock()
	})

	dc.OnMessage(func(msg webrtc.DataChannelMessage) {
		onDataChannelMessage(msg, peer) // Call the provided handler
	})

	dc.OnClose(func() {
		fmt.Printf("❌ Data channel '%s' (created by us) closed\n", dc.Label())
		peer.connectedMu.Lock()
		peer.connected = false
		peer.connectedMu.Unlock()
		if peer.fileWriter != nil {
			peer.fileWriter.Close()
			peer.fileWriter = nil
		}
	})

	return peer, nil
}

// CreateOffer generates a WebRTC offer SDP.
func (p *WebRTCPeer) CreateOffer() (string, error) {
	offer, err := p.connection.CreateOffer(nil)
	if err != nil {
		return "", fmt.Errorf("failed to create offer: %w", err)
	}

	// Create a new gathering infrastructure for the offer
	offerGatheringComplete := webrtc.GatheringCompletePromise(p.connection)

	// Set the local description and start ICE candidate gathering
	if err = p.connection.SetLocalDescription(offer); err != nil {
		return "", fmt.Errorf("failed to set local description for offer: %w", err)
	}

	// Wait for ICE gathering to complete before returning the SDP
	// Correct way to wait for a channel to close:
	<-offerGatheringComplete

	if p.connection.LocalDescription() == nil {
		return "", fmt.Errorf("local description is nil after gathering")
	}

	return p.connection.LocalDescription().SDP, nil
}

// CreateAnswer sets the remote offer and generates a WebRTC answer SDP.
func (p *WebRTCPeer) CreateAnswer(offerSDP string) (string, error) {
	offer := webrtc.SessionDescription{
		Type: webrtc.SDPTypeOffer,
		SDP:  offerSDP,
	}

	// Set the remote description
	if err := p.connection.SetRemoteDescription(offer); err != nil {
		return "", fmt.Errorf("failed to set remote description for answer: %w", err)
	}

	answer, err := p.connection.CreateAnswer(nil)
	if err != nil {
		return "", fmt.Errorf("failed to create answer: %w", err)
	}

	// Create a new gathering infrastructure for the answer
	answerGatheringComplete := webrtc.GatheringCompletePromise(p.connection)

	// Set the local description
	if err := p.connection.SetLocalDescription(answer); err != nil {
		return "", fmt.Errorf("failed to set local description for answer: %w", err)
	}

	// Wait for ICE gathering to complete before returning the SDP
	// Correct way to wait for a channel to close:
	<-answerGatheringComplete

	if p.connection.LocalDescription() == nil {
		return "", fmt.Errorf("local description is nil after gathering")
	}

	return p.connection.LocalDescription().SDP, nil
}

// SetAnswer sets the remote answer SDP to complete the connection.
func (p *WebRTCPeer) SetAnswer(answerSDP string) error {
	answer := webrtc.SessionDescription{
		Type: webrtc.SDPTypeAnswer,
		SDP:  answerSDP,
	}

	// Set the remote description
	if err := p.connection.SetRemoteDescription(answer); err != nil {
		return fmt.Errorf("failed to set remote description for answer: %w", err)
	}
	return nil
}

// IsConnected checks if the WebRTC peer is connected.
func (p *WebRTCPeer) IsConnected() bool {
	p.connectedMu.RLock()
	defer p.connectedMu.RUnlock()
	return p.connected
}

// WaitForConnection blocks until the WebRTC connection is established or a timeout occurs.
func (p *WebRTCPeer) WaitForConnection(timeout time.Duration) error {
	select {
	case <-p.connectionReady:
		return nil
	case <-time.After(timeout):
		return fmt.Errorf("WebRTC connection timed out after %s", timeout)
	}
}

// RequestFile sends a command to the connected peer to request a file.
func (p *WebRTCPeer) RequestFile(filename string) error {
	if !p.IsConnected() {
		return fmt.Errorf("not connected to a peer")
	}
	encodedFilename := base64.StdEncoding.EncodeToString([]byte(filename))
	// Command format: COMMAND:FILENAME_ENCODED
	return p.SendTextData(fmt.Sprintf("REQUEST_FILE:%s", encodedFilename))
}

// SendTextData sends a string message over the data channel.
func (p *WebRTCPeer) SendTextData(data string) error {
	if p.dataChannel == nil || p.dataChannel.ReadyState() != webrtc.DataChannelStateOpen {
		return fmt.Errorf("data channel not ready or closed")
	}
	return p.dataChannel.SendText(data)
}

// SendBinaryData sends a byte slice (binary data) over the data channel.
func (p *WebRTCPeer) SendBinaryData(data []byte) error {
	if p.dataChannel == nil || p.dataChannel.ReadyState() != webrtc.DataChannelStateOpen {
		return fmt.Errorf("data channel not ready or closed")
	}
	return p.dataChannel.Send(data)
}

// SetFileWriter sets the file writer for incoming file data.
func (p *WebRTCPeer) SetFileWriter(w *os.File) {
	p.fileWriter = w
}

// GetFileWriter returns the current file writer.
func (p *WebRTCPeer) GetFileWriter() *os.File {
	return p.fileWriter
}
