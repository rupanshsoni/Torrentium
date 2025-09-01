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

// SimpleWebRTCPeer is a minimal, working WebRTC implementation
type SimpleWebRTCPeer struct {
	pc              *webrtc.PeerConnection
	dc              *webrtc.DataChannel
	isOfferer       bool
	connected       chan struct{}
	closed          chan struct{}
	dataReady       chan struct{}
	iceComplete     chan struct{}
	onMessage       func(msg webrtc.DataChannelMessage, peer *SimpleWebRTCPeer)
	fileWriter      io.WriteCloser
	writerMutex     sync.Mutex
	signalingStream network.Stream
}

func NewSimpleWebRTCPeer(onMessage func(msg webrtc.DataChannelMessage, peer *SimpleWebRTCPeer)) (*SimpleWebRTCPeer, error) {
	// Ultra-simple config for localhost/same-network
	config := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{URLs: []string{"stun:stun.l.google.com:19302"}},
		},
	}

	pc, err := webrtc.NewPeerConnection(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create peer connection: %w", err)
	}

	peer := &SimpleWebRTCPeer{
		pc:          pc,
		connected:   make(chan struct{}),
		closed:      make(chan struct{}),
		dataReady:   make(chan struct{}),
		iceComplete: make(chan struct{}),
		onMessage:   onMessage,
	}

	// ICE gathering state
	pc.OnICEGatheringStateChange(func(state webrtc.ICEGathererState) {
		log.Printf("ICE gathering state: %s", state.String())
		if state == webrtc.ICEGathererStateComplete {
			select {
			case peer.iceComplete <- struct{}{}:
			default:
			}
		}
	})

	// ICE connection state
	pc.OnICEConnectionStateChange(func(state webrtc.ICEConnectionState) {
		log.Printf("ICE connection state: %s", state.String())
	})

	// Simple state tracking
	pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		log.Printf("Simple WebRTC state: %s", state.String())
		if state == webrtc.PeerConnectionStateConnected {
			select {
			case peer.connected <- struct{}{}:
			default:
			}
		}
		if state == webrtc.PeerConnectionStateFailed || state == webrtc.PeerConnectionStateClosed {
			select {
			case <-peer.closed:
			default:
				close(peer.closed)
			}
		}
	})

	// Handle incoming data channels
	pc.OnDataChannel(func(dc *webrtc.DataChannel) {
		log.Printf("Received data channel: %s", dc.Label())
		peer.dc = dc
		peer.setupDataChannel()
	})

	return peer, nil
}

func (p *SimpleWebRTCPeer) setupDataChannel() {
	if p.dc == nil {
		return
	}

	p.dc.OnOpen(func() {
		log.Printf("Data channel opened!")
		select {
		case p.dataReady <- struct{}{}:
		default:
		}
	})

	p.dc.OnClose(func() {
		log.Printf("Data channel closed")
	})

	p.dc.OnMessage(func(msg webrtc.DataChannelMessage) {
		if p.onMessage != nil {
			p.onMessage(msg, p)
		}
	})
}

func (p *SimpleWebRTCPeer) CreateOffer() (string, error) {
	p.isOfferer = true

	// Create data channel
	dc, err := p.pc.CreateDataChannel("data", nil)
	if err != nil {
		return "", fmt.Errorf("failed to create data channel: %w", err)
	}
	p.dc = dc
	p.setupDataChannel()

	// Create offer
	offer, err := p.pc.CreateOffer(nil)
	if err != nil {
		return "", fmt.Errorf("failed to create offer: %w", err)
	}

	// Set local description
	err = p.pc.SetLocalDescription(offer)
	if err != nil {
		return "", fmt.Errorf("failed to set local description: %w", err)
	}

	// Wait for ICE gathering to complete
	log.Printf("Waiting for ICE gathering to complete...")
	select {
	case <-p.iceComplete:
		log.Printf("ICE gathering completed")
	case <-time.After(10 * time.Second):
		log.Printf("ICE gathering timeout, proceeding anyway...")
	}

	// Get the complete offer with ICE candidates
	completeOffer := p.pc.LocalDescription()

	// Convert to JSON
	offerJSON, err := json.Marshal(completeOffer)
	if err != nil {
		return "", fmt.Errorf("failed to marshal offer: %w", err)
	}

	log.Printf("Created complete offer successfully")
	return string(offerJSON), nil
}

func (p *SimpleWebRTCPeer) HandleOffer(offerStr string) (string, error) {
	p.isOfferer = false

	// Parse offer
	var offer webrtc.SessionDescription
	err := json.Unmarshal([]byte(offerStr), &offer)
	if err != nil {
		return "", fmt.Errorf("failed to unmarshal offer: %w", err)
	}

	// Set remote description
	err = p.pc.SetRemoteDescription(offer)
	if err != nil {
		return "", fmt.Errorf("failed to set remote description: %w", err)
	}

	// Create answer
	answer, err := p.pc.CreateAnswer(nil)
	if err != nil {
		return "", fmt.Errorf("failed to create answer: %w", err)
	}

	// Set local description
	err = p.pc.SetLocalDescription(answer)
	if err != nil {
		return "", fmt.Errorf("failed to set local description: %w", err)
	}

	// Wait for ICE gathering to complete
	log.Printf("Waiting for ICE gathering to complete on answerer...")
	select {
	case <-p.iceComplete:
		log.Printf("ICE gathering completed on answerer")
	case <-time.After(10 * time.Second):
		log.Printf("ICE gathering timeout on answerer, proceeding anyway...")
	}

	// Get the complete answer with ICE candidates
	completeAnswer := p.pc.LocalDescription()

	// Convert to JSON
	answerJSON, err := json.Marshal(completeAnswer)
	if err != nil {
		return "", fmt.Errorf("failed to marshal answer: %w", err)
	}

	log.Printf("Created complete answer successfully")
	return string(answerJSON), nil
}

func (p *SimpleWebRTCPeer) HandleAnswer(answerStr string) error {
	if !p.isOfferer {
		return fmt.Errorf("not the offerer, shouldn't handle answer")
	}

	// Parse answer
	var answer webrtc.SessionDescription
	err := json.Unmarshal([]byte(answerStr), &answer)
	if err != nil {
		return fmt.Errorf("failed to unmarshal answer: %w", err)
	}

	// Set remote description
	err = p.pc.SetRemoteDescription(answer)
	if err != nil {
		return fmt.Errorf("failed to set remote description: %w", err)
	}

	log.Printf("Set remote answer successfully")
	return nil
}

func (p *SimpleWebRTCPeer) WaitForConnection(timeout time.Duration) error {
	log.Printf("Waiting for WebRTC connection...")

	// For non-offerer (answerer), the connection might already be established
	if !p.isOfferer && p.pc.ConnectionState() == webrtc.PeerConnectionStateConnected {
		log.Printf("Connection already established")
		// Still need to wait for data channel
		if p.dc != nil && p.dc.ReadyState() == webrtc.DataChannelStateOpen {
			log.Printf("Data channel already ready!")
			return nil
		}
	}

	select {
	case <-p.connected:
		log.Printf("Peer connection established")
	case <-p.closed:
		return fmt.Errorf("connection closed")
	case <-time.After(timeout):
		return fmt.Errorf("peer connection timeout")
	}

	// Now wait for data channel
	log.Printf("Waiting for data channel...")

	// Check if data channel is already open
	if p.dc != nil && p.dc.ReadyState() == webrtc.DataChannelStateOpen {
		log.Printf("Data channel already ready!")
		return nil
	}

	select {
	case <-p.dataReady:
		log.Printf("Data channel ready!")
		return nil
	case <-p.closed:
		return fmt.Errorf("connection closed while waiting for data channel")
	case <-time.After(5 * time.Second):
		// Check state one more time
		if p.dc != nil && p.dc.ReadyState() == webrtc.DataChannelStateOpen {
			log.Printf("Data channel ready (detected on timeout check)!")
			return nil
		}
		return fmt.Errorf("data channel timeout")
	}
}

func (p *SimpleWebRTCPeer) SendJSON(v interface{}) error {
	if p.dc == nil {
		return fmt.Errorf("data channel not ready")
	}

	data, err := json.Marshal(v)
	if err != nil {
		return err
	}

	return p.dc.SendText(string(data))
}

func (p *SimpleWebRTCPeer) SendRaw(data []byte) error {
	if p.dc == nil {
		return fmt.Errorf("data channel not ready")
	}

	return p.dc.Send(data)
}

func (p *SimpleWebRTCPeer) Close() {
	if p.pc != nil {
		p.pc.Close()
	}
}

func (p *SimpleWebRTCPeer) SetFileWriter(w io.WriteCloser) {
	p.writerMutex.Lock()
	defer p.writerMutex.Unlock()
	p.fileWriter = w
}

func (p *SimpleWebRTCPeer) GetFileWriter() io.WriteCloser {
	p.writerMutex.Lock()
	defer p.writerMutex.Unlock()
	return p.fileWriter
}

func (p *SimpleWebRTCPeer) SetSignalingStream(s network.Stream) {
	p.signalingStream = s
}

func (p *SimpleWebRTCPeer) GetSignalingStream() network.Stream {
	return p.signalingStream
}

func (p *SimpleWebRTCPeer) WaitForClose() {
	<-p.closed
}

func (p *SimpleWebRTCPeer) WaitForCloseChannel() <-chan struct{} {
	return p.closed
}

// SignalDownloadComplete signals that the download is complete
func (p *SimpleWebRTCPeer) SignalDownloadComplete() {
	log.Printf("Download completed, closing connection")
	p.Close()
}
