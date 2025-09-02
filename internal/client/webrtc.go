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

const (
	maxICEGatheringTimeout = 15 * time.Second
	connectionTimeout      = 30 * time.Second
	keepAliveInterval      = 15 * time.Second
)

var webrtcConfig = webrtc.Configuration{
	ICEServers: []webrtc.ICEServer{
		{
			URLs: []string{
				"stun:stun.l.google.com:19302",
				"stun:stun1.l.google.com:19302",
				"stun:stun.cloudflare.com:3478",
			},
		},
	},
}

type ConnectionState int

const (
	ConnectionStateNew ConnectionState = iota
	ConnectionStateConnecting
	ConnectionStateConnected
	ConnectionStateDisconnected
	ConnectionStateFailed
	ConnectionStateClosed
)

// SimpleWebRTCPeer is a minimal, working WebRTC implementation
type SimpleWebRTCPeer struct {
	pc              *webrtc.PeerConnection
	dc              *webrtc.DataChannel
	onMessage       func(msg webrtc.DataChannelMessage, peer *SimpleWebRTCPeer)
	fileWriter      io.WriteCloser
	writerMutex     sync.Mutex
	signalingStream network.Stream
	state           ConnectionState
	stateMux        sync.RWMutex
	closeOnce       sync.Once
	closeCh         chan struct{}
	keepAliveTick   *time.Ticker
}

func NewSimpleWebRTCPeer(onMessage func(msg webrtc.DataChannelMessage, peer *SimpleWebRTCPeer)) (*SimpleWebRTCPeer, error) {
	pc, err := webrtc.NewPeerConnection(webrtcConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create peer connection: %w", err)
	}

	peer := &SimpleWebRTCPeer{
		pc:        pc,
		onMessage: onMessage,
		closeCh:   make(chan struct{}),
		state:     ConnectionStateNew,
	}

	peer.setupConnectionHandlers()
	return peer, nil
}

func (p *SimpleWebRTCPeer) setupConnectionHandlers() {
	p.pc.OnICEConnectionStateChange(func(state webrtc.ICEConnectionState) {
		log.Printf("ICE Connection State changed: %s", state.String())
		switch state {
		case webrtc.ICEConnectionStateConnected:
			p.setConnectionState(ConnectionStateConnected)
		case webrtc.ICEConnectionStateDisconnected:
			p.setConnectionState(ConnectionStateDisconnected)
		case webrtc.ICEConnectionStateFailed:
			p.setConnectionState(ConnectionStateFailed)
		case webrtc.ICEConnectionStateClosed:
			p.setConnectionState(ConnectionStateClosed)
		}
	})

	p.pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		log.Printf("Connection State changed: %s", state.String())
		if state == webrtc.PeerConnectionStateFailed {
			p.Close()
		}
	})

	p.pc.OnDataChannel(func(dc *webrtc.DataChannel) {
		log.Printf("New DataChannel %s", dc.Label())
		p.dc = dc
		p.setupDataChannel()
	})
}

func (p *SimpleWebRTCPeer) setupDataChannel() {
	p.dc.OnOpen(func() {
		log.Printf("Data channel '%s' opened", p.dc.Label())
		p.setConnectionState(ConnectionStateConnected)
	})

	p.dc.OnClose(func() {
		log.Printf("Data channel '%s' closed", p.dc.Label())
		p.setConnectionState(ConnectionStateClosed)
	})

	p.dc.OnMessage(func(msg webrtc.DataChannelMessage) {
		p.onMessage(msg, p)
	})
}

func (p *SimpleWebRTCPeer) CreateOffer() (string, error) {
	dc, err := p.pc.CreateDataChannel("data", nil)
	if err != nil {
		return "", fmt.Errorf("failed to create data channel: %w", err)
	}
	p.dc = dc
	p.setupDataChannel()

	offer, err := p.pc.CreateOffer(nil)
	if err != nil {
		return "", err
	}

	gatherComplete := webrtc.GatheringCompletePromise(p.pc)

	if err := p.pc.SetLocalDescription(offer); err != nil {
		return "", err
	}

	select {
	case <-gatherComplete:
	case <-time.After(maxICEGatheringTimeout):
		log.Println("ICE gathering timed out")
	}

	offerJSON, err := json.Marshal(p.pc.LocalDescription())
	if err != nil {
		return "", err
	}
	return string(offerJSON), nil
}

func (p *SimpleWebRTCPeer) HandleOffer(offerStr string) (string, error) {
	var offer webrtc.SessionDescription
	if err := json.Unmarshal([]byte(offerStr), &offer); err != nil {
		return "", err
	}

	if err := p.pc.SetRemoteDescription(offer); err != nil {
		return "", err
	}

	answer, err := p.pc.CreateAnswer(nil)
	if err != nil {
		return "", err
	}

	gatherComplete := webrtc.GatheringCompletePromise(p.pc)

	if err := p.pc.SetLocalDescription(answer); err != nil {
		return "", err
	}

	select {
	case <-gatherComplete:
	case <-time.After(maxICEGatheringTimeout):
		log.Println("ICE gathering timed out")
	}

	answerJSON, err := json.Marshal(p.pc.LocalDescription())
	if err != nil {
		return "", err
	}
	return string(answerJSON), nil
}

func (p *SimpleWebRTCPeer) HandleAnswer(answerStr string) error {
	var answer webrtc.SessionDescription
	if err := json.Unmarshal([]byte(answerStr), &answer); err != nil {
		return err
	}

	return p.pc.SetRemoteDescription(answer)
}

func (p *SimpleWebRTCPeer) SendJSON(v interface{}) error {
	if p.dc == nil || p.dc.ReadyState() != webrtc.DataChannelStateOpen {
		return fmt.Errorf("data channel is not open")
	}
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return p.dc.SendText(string(data))
}

func (p *SimpleWebRTCPeer) SendRaw(data []byte) error {
	if p.dc == nil || p.dc.ReadyState() != webrtc.DataChannelStateOpen {
		return fmt.Errorf("data channel is not open")
	}
	return p.dc.Send(data)
}

func (p *SimpleWebRTCPeer) Close() {
	p.closeOnce.Do(func() {
		p.stopKeepAlive()
		if p.pc != nil {
			p.pc.Close()
		}
		close(p.closeCh)
	})
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

func (p *SimpleWebRTCPeer) WaitForConnection(timeout time.Duration) error {
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-timer.C:
			return fmt.Errorf("connection timeout")
		case <-ticker.C:
			if p.getConnectionState() == ConnectionStateConnected {
				return nil
			}
		case <-p.closeCh:
			return fmt.Errorf("connection closed")
		}
	}
}

func (p *SimpleWebRTCPeer) setConnectionState(state ConnectionState) {
	p.stateMux.Lock()
	defer p.stateMux.Unlock()

	if p.state == state {
		return
	}

	p.state = state
	log.Printf("Connection state changed to: %d", state)

	if state == ConnectionStateConnected {
		p.startKeepAlive()
	} else {
		p.stopKeepAlive()
	}
}

func (p *SimpleWebRTCPeer) getConnectionState() ConnectionState {
	p.stateMux.RLock()
	defer p.stateMux.RUnlock()
	return p.state
}

func (p *SimpleWebRTCPeer) startKeepAlive() {
	if p.keepAliveTick != nil {
		return
	}
	p.keepAliveTick = time.NewTicker(keepAliveInterval)
	go func() {
		for {
			select {
			case <-p.keepAliveTick.C:
				if err := p.SendJSON(map[string]string{"type": "ping"}); err != nil {
					log.Printf("Failed to send keepalive: %v", err)
				}
			case <-p.closeCh:
				return
			}
		}
	}()
}

func (p *SimpleWebRTCPeer) stopKeepAlive() {
	if p.keepAliveTick != nil {
		p.keepAliveTick.Stop()
		p.keepAliveTick = nil
	}
}

func (p *SimpleWebRTCPeer) WaitForCloseChannel() <-chan struct{} {
	return p.closeCh
}
func (p *SimpleWebRTCPeer) SignalDownloadComplete() {
	log.Printf("Download completed, closing connection")
	p.Close()
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
