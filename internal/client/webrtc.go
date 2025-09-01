package client

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
	"github.com/pion/webrtc/v3"
)

type WebRTCPeer struct {
	peerConnection    *webrtc.PeerConnection
	dataChannel       *webrtc.DataChannel
	closeChan         chan struct{}
	connectedChan     chan struct{}
	dataChannelReady  chan struct{}
	onMessage         func(msg webrtc.DataChannelMessage, peer *WebRTCPeer)
	fileWriter        io.WriteCloser
	writerMutex       sync.Mutex
	signalingStream   network.Stream
	iceCandidates     []webrtc.ICECandidate
	candidatesMux     sync.Mutex
	isDataChannelOpen bool
	dcMutex           sync.RWMutex
}

func NewWebRTCPeer(onMessage func(msg webrtc.DataChannelMessage, peer *WebRTCPeer)) (*WebRTCPeer, error) {
	// Simplified configuration for local network connections
	config := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			// For same-network, we primarily need STUN for host candidates
			{URLs: []string{"stun:stun.l.google.com:19302"}},
		},
		// Relaxed transport policy to allow all connection types
		ICETransportPolicy: webrtc.ICETransportPolicyAll,
	}

	pc, err := webrtc.NewPeerConnection(config)
	if err != nil {
		return nil, err
	}

	peer := &WebRTCPeer{
		peerConnection:   pc,
		closeChan:        make(chan struct{}),
		connectedChan:    make(chan struct{}),
		dataChannelReady: make(chan struct{}),
		onMessage:        onMessage,
		iceCandidates:    make([]webrtc.ICECandidate, 0),
	}

	// Enhanced connection state monitoring
	pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		log.Printf("Peer Connection State changed: %s", state.String())
		switch state {
		case webrtc.PeerConnectionStateConnected:
			select {
			case peer.connectedChan <- struct{}{}:
			default:
			}
		case webrtc.PeerConnectionStateFailed, webrtc.PeerConnectionStateClosed, webrtc.PeerConnectionStateDisconnected:
			if peer.fileWriter != nil {
				peer.fileWriter.Close()
			}
			select {
			case <-peer.closeChan:
			default:
				close(peer.closeChan)
			}
		}
	})

	// ICE connection state monitoring
	pc.OnICEConnectionStateChange(func(state webrtc.ICEConnectionState) {
		log.Printf("ICE Connection State changed: %s", state.String())
		switch state {
		case webrtc.ICEConnectionStateFailed:
			log.Printf("ICE connection failed - this usually indicates NAT traversal issues")
		case webrtc.ICEConnectionStateDisconnected:
			log.Printf("ICE connection disconnected")
		case webrtc.ICEConnectionStateConnected:
			log.Printf("ICE connection established successfully")
		}
	})

	// ICE gathering state monitoring
	pc.OnICEGatheringStateChange(func(state webrtc.ICEGathererState) {
		log.Printf("ICE Gathering State changed: %s", state.String())
	})

	// ICE candidate handling - critical for NAT traversal
	pc.OnICECandidate(func(candidate *webrtc.ICECandidate) {
		if candidate != nil {
			log.Printf("Generated ICE candidate: %s (type: %s, protocol: %s)",
				candidate.String(), candidate.Typ, candidate.Protocol)
			peer.candidatesMux.Lock()
			peer.iceCandidates = append(peer.iceCandidates, *candidate)
			peer.candidatesMux.Unlock()
		} else {
			log.Printf("ICE candidate gathering completed - total candidates: %d", len(peer.iceCandidates))
		}
	})

	pc.OnDataChannel(func(dc *webrtc.DataChannel) {
		log.Printf("New DataChannel \"%s\" (ID: %d) received!", dc.Label(), *dc.ID())
		peer.dataChannel = dc
		peer.setupDataChannel()
	})

	return peer, nil
}

func (p *WebRTCPeer) CreateOffer() (string, error) {
	// Configure data channel for same-network optimization
	dcOptions := &webrtc.DataChannelInit{
		Ordered:        &[]bool{true}[0], // Ensure ordered delivery for file transfer
		MaxRetransmits: &[]uint16{3}[0],  // Limited retries for faster failure detection
	}

	dc, err := p.peerConnection.CreateDataChannel("data", dcOptions)
	if err != nil {
		return "", err
	}
	p.dataChannel = dc
	p.setupDataChannel()

	// Wait a bit for data channel to be fully set up
	time.Sleep(100 * time.Millisecond)

	offer, err := p.peerConnection.CreateOffer(nil)
	if err != nil {
		return "", err
	}
	if err := p.peerConnection.SetLocalDescription(offer); err != nil {
		return "", err
	}

	log.Printf("Created WebRTC offer with data channel")
	return p.encode(offer)
}

func (p *WebRTCPeer) CreateAnswer(offerStr string) (string, error) {
	var offer webrtc.SessionDescription
	if err := p.decode(offerStr, &offer); err != nil {
		return "", err
	}
	if err := p.peerConnection.SetRemoteDescription(offer); err != nil {
		return "", err
	}
	answer, err := p.peerConnection.CreateAnswer(nil)
	if err != nil {
		return "", err
	}
	if err := p.peerConnection.SetLocalDescription(answer); err != nil {
		return "", err
	}
	return p.encode(answer)
}

func (p *WebRTCPeer) SetAnswer(answerStr string) error {
	var answer webrtc.SessionDescription
	if err := p.decode(answerStr, &answer); err != nil {
		return err
	}
	return p.peerConnection.SetRemoteDescription(answer)
}

func (p *WebRTCPeer) setupDataChannel() {
	if p.dataChannel == nil {
		log.Printf("Warning: setupDataChannel called with nil dataChannel")
		return
	}

	p.dataChannel.OnOpen(func() {
		log.Printf("Data channel '%s' (ID: %d) opened successfully!", p.dataChannel.Label(), *p.dataChannel.ID())
		p.dcMutex.Lock()
		p.isDataChannelOpen = true
		p.dcMutex.Unlock()

		// Signal that data channel is ready
		select {
		case p.dataChannelReady <- struct{}{}:
		default:
		}
	})

	p.dataChannel.OnClose(func() {
		log.Printf("Data channel '%s' closed", p.dataChannel.Label())
		p.dcMutex.Lock()
		p.isDataChannelOpen = false
		p.dcMutex.Unlock()
	})

	p.dataChannel.OnError(func(err error) {
		log.Printf("Data channel error: %v", err)
	})

	p.dataChannel.OnMessage(func(msg webrtc.DataChannelMessage) {
		p.onMessage(msg, p)
	})
}

func (p *WebRTCPeer) IsDataChannelOpen() bool {
	p.dcMutex.RLock()
	defer p.dcMutex.RUnlock()
	return p.isDataChannelOpen
}

func (p *WebRTCPeer) SendJSON(v interface{}) error {
	if !p.IsDataChannelOpen() {
		return fmt.Errorf("data channel is not open")
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return p.dataChannel.SendText(string(b))
}

func (p *WebRTCPeer) SendRaw(b []byte) error {
	if !p.IsDataChannelOpen() {
		return fmt.Errorf("data channel is not open")
	}
	return p.dataChannel.Send(b)
}

func (p *WebRTCPeer) WaitForConnection(timeout time.Duration) error {
	log.Printf("Waiting for WebRTC connection (timeout: %v)", timeout)

	// For same-network connections, use shorter check intervals
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	deadline := time.Now().Add(timeout)
	peerConnected := false

	for {
		select {
		case <-p.dataChannelReady:
			log.Printf("Data channel is ready!")
			if p.IsDataChannelOpen() {
				log.Printf("WebRTC connection fully established - data channel is open")
				return nil
			}
		case <-p.connectedChan:
			log.Printf("Peer connection established!")
			peerConnected = true
			// Don't return yet, wait for data channel
		case <-p.closeChan:
			return fmt.Errorf("connection closed while waiting")
		case <-ticker.C:
			connState := p.peerConnection.ConnectionState()
			iceState := p.peerConnection.ICEConnectionState()
			dcOpen := p.IsDataChannelOpen()
			log.Printf("Connection status - Peer: %s, ICE: %s, DataChannel: %v",
				connState.String(), iceState.String(), dcOpen)

			// Check if everything is ready
			if connState == webrtc.PeerConnectionStateConnected && dcOpen {
				log.Printf("Full WebRTC connection established!")
				return nil
			}

			// Check for failure states
			if connState == webrtc.PeerConnectionStateFailed {
				return fmt.Errorf("peer connection failed")
			}
			if iceState == webrtc.ICEConnectionStateFailed {
				return fmt.Errorf("ICE connection failed - NAT traversal unsuccessful")
			}

			// For same-network, give more time for data channel to open
			if peerConnected && iceState == webrtc.ICEConnectionStateConnected {
				log.Printf("ICE connected, waiting for data channel to open...")
			}

			if time.Now().After(deadline) {
				return fmt.Errorf("connection timeout - Peer state: %s, ICE state: %s, DataChannel open: %v",
					connState.String(), iceState.String(), dcOpen)
			}
		}
	}
}

func (p *WebRTCPeer) Close() {
	if p.peerConnection != nil {
		p.peerConnection.Close()
	}
	if p.signalingStream != nil {
		// Send close message before closing stream
		encoder := json.NewEncoder(p.signalingStream)
		closeMsg := map[string]string{
			"type": "close",
			"data": "",
		}
		_ = encoder.Encode(closeMsg)
		p.signalingStream.Close()
	}
}

func (p *WebRTCPeer) SetSignalingStream(s network.Stream) {
	p.signalingStream = s
}

func (p *WebRTCPeer) SetFileWriter(w io.WriteCloser) {
	p.writerMutex.Lock()
	defer p.writerMutex.Unlock()
	p.fileWriter = w
}

func (p *WebRTCPeer) GetFileWriter() io.WriteCloser {
	p.writerMutex.Lock()
	defer p.writerMutex.Unlock()
	return p.fileWriter
}

func (p *WebRTCPeer) GetSignalingStream() network.Stream {
	return p.signalingStream
}

func (p *WebRTCPeer) encode(obj interface{}) (string, error) {
	b, err := json.Marshal(obj)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (p *WebRTCPeer) decode(s string, obj interface{}) error {
	return json.Unmarshal([]byte(s), obj)
}

func (p *WebRTCPeer) WaitForClose() {
	<-p.closeChan
}

// TestICEConnectivity performs a basic ICE connectivity test
func TestICEConnectivity() error {
	config := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{URLs: []string{"stun:stun.cloudflare.com:3478"}},
			{URLs: []string{"stun:stun.l.google.com:19302"}},
		},
	}

	pc, err := webrtc.NewPeerConnection(config)
	if err != nil {
		return fmt.Errorf("failed to create test peer connection: %w", err)
	}
	defer pc.Close()

	candidateCount := 0
	done := make(chan bool, 1)

	pc.OnICECandidate(func(candidate *webrtc.ICECandidate) {
		if candidate != nil {
			candidateCount++
			log.Printf("Test ICE candidate: %s", candidate.String())
		} else {
			done <- true
		}
	})

	pc.OnICEGatheringStateChange(func(state webrtc.ICEGathererState) {
		log.Printf("Test ICE gathering state: %s", state.String())
	})

	// Create a dummy offer to trigger ICE gathering
	dc, err := pc.CreateDataChannel("test", nil)
	if err != nil {
		return fmt.Errorf("failed to create test data channel: %w", err)
	}
	defer dc.Close()

	offer, err := pc.CreateOffer(nil)
	if err != nil {
		return fmt.Errorf("failed to create test offer: %w", err)
	}

	if err := pc.SetLocalDescription(offer); err != nil {
		return fmt.Errorf("failed to set test local description: %w", err)
	}

	// Wait for ICE gathering to complete or timeout
	select {
	case <-done:
		log.Printf("ICE connectivity test completed with %d candidates", candidateCount)
		if candidateCount == 0 {
			return fmt.Errorf("no ICE candidates generated - network connectivity issues")
		}
		return nil
	case <-time.After(10 * time.Second):
		log.Printf("ICE connectivity test timeout with %d candidates", candidateCount)
		if candidateCount == 0 {
			return fmt.Errorf("ICE connectivity test failed - no candidates generated")
		}
		return nil
	}
}
