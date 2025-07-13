package webrtc

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/pion/webrtc/v3"
)

// WebRTCPeer represents a WebRTC peer connection for file sharing
type WebRTCPeer struct {
	pc          *webrtc.PeerConnection // WebRTC peer connection
	dataChannel *webrtc.DataChannel   // Data channel for sending/receiving files
	isConnected bool                   // Connection status
	
	// File receiving state
	receivingFile     bool
	receivingFileName string
	receivingFileData []byte
}

// NewWebRTCPeer creates a new WebRTC peer with STUN servers for NAT traversal
func NewWebRTCPeer() (*WebRTCPeer, error) {
	// Configuration with multiple STUN servers for better connectivity
	config := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{URLs: []string{"stun:stun.l.google.com:19302"}},
			{URLs: []string{"stun:stun1.l.google.com:19302"}},
			{URLs: []string{"stun:stun2.l.google.com:19302"}},
		},
	}

	// Create the peer connection
	pc, err := webrtc.NewPeerConnection(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create peer connection: %v", err)
	}

	peer := &WebRTCPeer{
		pc:          pc,
		isConnected: false,
	}

	// Set up comprehensive connection monitoring
	pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		fmt.Printf("🔗 Connection state: %v\n", state)
		peer.isConnected = (state == webrtc.PeerConnectionStateConnected)
		
		if state == webrtc.PeerConnectionStateFailed {
			fmt.Println("❌ Connection failed - try creating new offer/answer")
		}
	})
	
	// Monitor ICE connection state
	pc.OnICEConnectionStateChange(func(state webrtc.ICEConnectionState) {
		fmt.Printf("🧊 ICE connection state: %v\n", state)
	})
		// Monitor ICE gathering state
	pc.OnICEGatheringStateChange(func(state webrtc.ICEGathererState) {
		fmt.Printf("📡 ICE gathering state: %v\n", state)
	})

	return peer, nil
}

// CreateOffer creates an offer to start a WebRTC connection (call this first)
func (p *WebRTCPeer) CreateOffer() (string, error) {
	fmt.Println("🚀 Creating WebRTC offer...")
	
	// Create a data channel for file transfer
	dataChannel, err := p.pc.CreateDataChannel("fileTransfer", &webrtc.DataChannelInit{
		Ordered: &[]bool{true}[0], // Ensure ordered delivery
	})
	if err != nil {
		return "", fmt.Errorf("failed to create data channel: %v", err)
	}
	
	p.dataChannel = dataChannel
	p.setupDataChannelHandlers()

	// Create the offer
	offer, err := p.pc.CreateOffer(nil)
	if err != nil {
		return "", fmt.Errorf("failed to create offer: %v", err)
	}

	// Set our local description
	if err := p.pc.SetLocalDescription(offer); err != nil {
		return "", fmt.Errorf("failed to set local description: %v", err)
	}

	fmt.Println("⏳ Gathering ICE candidates...")
	
	// Wait for ICE candidates to be gathered with timeout
	gatherComplete := webrtc.GatheringCompletePromise(p.pc)
	
	select {
	case <-gatherComplete:
		fmt.Println("✅ ICE gathering complete")
	case <-time.After(10 * time.Second):
		fmt.Println("⚠️ ICE gathering timeout, proceeding anyway")
	}

	// Convert to JSON string for sharing
	localDesc := p.pc.LocalDescription()
	if localDesc == nil {
		return "", fmt.Errorf("local description is nil")
	}
	
	offerJSON, err := json.Marshal(localDesc)
	if err != nil {
		return "", fmt.Errorf("failed to marshal offer: %v", err)
	}

	fmt.Println("✅ Offer created successfully!")
	return string(offerJSON), nil
}

// CreateAnswer creates an answer to respond to an offer
func (p *WebRTCPeer) CreateAnswer(offerJSON string) (string, error) {
	fmt.Println("🚀 Creating WebRTC answer...")
	fmt.Printf("📝 Received offer length: %d characters\n", len(offerJSON))
	
	// Parse the received offer
	var offer webrtc.SessionDescription
	if err := json.Unmarshal([]byte(offerJSON), &offer); err != nil {
		return "", fmt.Errorf("failed to parse offer JSON: %v", err)
	}
	
	fmt.Printf("📋 Offer type: %s\n", offer.Type.String())

	// Set up handler for incoming data channel
	p.pc.OnDataChannel(func(d *webrtc.DataChannel) {
		fmt.Println("📡 Data channel received from peer")
		p.dataChannel = d
		p.setupDataChannelHandlers()
	})

	// Set the remote description (the offer)
	if err := p.pc.SetRemoteDescription(offer); err != nil {
		return "", fmt.Errorf("failed to set remote description: %v", err)
	}

	// Create our answer
	answer, err := p.pc.CreateAnswer(nil)
	if err != nil {
		return "", fmt.Errorf("failed to create answer: %v", err)
	}

	// Set our local description
	if err := p.pc.SetLocalDescription(answer); err != nil {
		return "", fmt.Errorf("failed to set local description: %v", err)
	}

	fmt.Println("⏳ Gathering ICE candidates...")
	
	// Wait for ICE candidates with timeout
	gatherComplete := webrtc.GatheringCompletePromise(p.pc)
	
	select {
	case <-gatherComplete:
		fmt.Println("✅ ICE gathering complete")
	case <-time.After(10 * time.Second):
		fmt.Println("⚠️ ICE gathering timeout, proceeding anyway")
	}

	// Convert to JSON for sharing
	localDesc := p.pc.LocalDescription()
	if localDesc == nil {
		return "", fmt.Errorf("local description is nil")
	}
	
	answerJSON, err := json.Marshal(localDesc)
	if err != nil {
		return "", fmt.Errorf("failed to marshal answer: %v", err)
	}

	fmt.Println("✅ Answer created successfully!")
	return string(answerJSON), nil
}

// SetAnswer completes the connection by setting the answer (offerer calls this)
func (p *WebRTCPeer) SetAnswer(answerJSON string) error {
	fmt.Println("🔗 Setting answer to complete connection...")
	fmt.Printf("📝 Received answer length: %d characters\n", len(answerJSON))
	
	// Parse the answer
	var answer webrtc.SessionDescription
	if err := json.Unmarshal([]byte(answerJSON), &answer); err != nil {
		return fmt.Errorf("failed to parse answer JSON: %v", err)
	}
	
	fmt.Printf("📋 Answer type: %s\n", answer.Type.String())

	// Set the remote description (the answer)
	if err := p.pc.SetRemoteDescription(answer); err != nil {
		return fmt.Errorf("failed to set remote description: %v", err)
	}

	fmt.Println("✅ Connection setup complete!")
	fmt.Println("⏳ Waiting for connection to establish...")
	return nil
}

// setupDataChannelHandlers sets up event handlers for the data channel
func (p *WebRTCPeer) setupDataChannelHandlers() {
	if p.dataChannel == nil {
		fmt.Println("⚠️ Data channel is nil")
		return
	}
	
	// When data channel opens
	p.dataChannel.OnOpen(func() {
		fmt.Println("🎉 WebRTC data channel opened!")
		fmt.Println("✅ Connection established! Ready to transfer files.")
		p.isConnected = true
	})

	// When data channel closes
	p.dataChannel.OnClose(func() {
		fmt.Println("🔌 WebRTC data channel closed")
		p.isConnected = false
	})
	
	// Monitor data channel state
	p.dataChannel.OnError(func(err error) {
		fmt.Printf("❌ Data channel error: %v\n", err)
	})

	// When we receive data
	p.dataChannel.OnMessage(func(msg webrtc.DataChannelMessage) {
		if msg.IsString {
			// Handle text commands (file requests, etc.)
			p.handleCommand(string(msg.Data))
		} else {
			// Handle binary data (file content)
			p.handleFileData(msg.Data)
		}
	})
	
	fmt.Printf("📡 Data channel handlers set up (ready state: %v)\n", p.dataChannel.ReadyState())
}

// handleCommand processes text commands received from the peer
func (p *WebRTCPeer) handleCommand(command string) {
	fmt.Printf("📨 Received command: %s\n", command)
	
	// Parse command
	cmd, filename, _ := parseCommand(command)
	
	switch cmd {
	case "REQUEST_FILE":
		// Peer is requesting a file from us
		fmt.Printf("📤 Peer requested file: %s\n", filename)
		p.sendFile(filename)
		
	case "FILE_START":
		// Peer is starting to send us a file
		fmt.Printf("📥 Starting to receive file: %s\n", filename)
		p.receivingFile = true
		p.receivingFileName = filename
		p.receivingFileData = []byte{}
		
	case "FILE_END":
		// Peer finished sending file
		if p.receivingFile {
			p.saveReceivedFile()
		}
		
	default:
		fmt.Printf("❓ Unknown command: %s\n", command)
	}
}

// handleFileData processes binary file data
func (p *WebRTCPeer) handleFileData(data []byte) {
	if p.receivingFile {
		// Append data to our receiving buffer
		p.receivingFileData = append(p.receivingFileData, data...)
				// Show progress with human-readable sizes
		fmt.Printf("📊 Received %s for %s\n", formatFileSize(int64(len(p.receivingFileData))), p.receivingFileName)
	}
}

// saveReceivedFile saves the received file to disk
func (p *WebRTCPeer) saveReceivedFile() {
	filename := "downloaded_" + p.receivingFileName
	
	err := os.WriteFile(filename, p.receivingFileData, 0644)
	if err != nil {
		fmt.Printf("❌ Error saving file: %v\n", err)
		return
	}
	
	fmt.Printf("✅ File saved successfully: %s (%s)\n", filename, formatFileSize(int64(len(p.receivingFileData))))
	
	// Reset receiving state
	p.receivingFile = false
	p.receivingFileName = ""
	p.receivingFileData = nil
}

// RequestFile requests a file from the connected peer
func (p *WebRTCPeer) RequestFile(filename string) error {
	if !p.isConnected || p.dataChannel == nil {
		return fmt.Errorf("not connected to peer")
	}
	
	// Send file request command
	command := fmt.Sprintf("REQUEST_FILE:%s", filename)
	err := p.dataChannel.SendText(command)
	if err != nil {
		return fmt.Errorf("failed to send file request: %v", err)
	}
	
	fmt.Printf("📤 Requested file: %s\n", filename)
	return nil
}

// sendFile sends a file to the connected peer
func (p *WebRTCPeer) sendFile(filename string) {
	// Check if file exists
	file, err := os.Open(filename)
	if err != nil {
		fmt.Printf("❌ Cannot open file %s: %v\n", filename, err)
		return
	}
	defer file.Close()

	// Get file size
	fileInfo, err := file.Stat()
	if err != nil {
		fmt.Printf("❌ Cannot get file info: %v\n", err)
		return
	}
	fileSize := fileInfo.Size()

	fmt.Printf("📤 Sending file: %s (%s)\n", filename, formatFileSize(fileSize))

	// Send start command
	startCommand := fmt.Sprintf("FILE_START:%s:%d", filename, fileSize)
	if err := p.dataChannel.SendText(startCommand); err != nil {
		fmt.Printf("❌ Failed to send start command: %v\n", err)
		return
	}

	// Send file in chunks
	buffer := make([]byte, 16384) // 16KB chunks
	totalSent := 0
	
	for {
		n, err := file.Read(buffer)
		if err == io.EOF {
			break
		}
		if err != nil {
			fmt.Printf("❌ Error reading file: %v\n", err)
			return
		}

		// Send the chunk
		if err := p.dataChannel.Send(buffer[:n]); err != nil {
			fmt.Printf("❌ Error sending chunk: %v\n", err)
			return
		}
		
		totalSent += n
		fmt.Printf("📊 Sent %s/%s (%.1f%%)\n", formatFileSize(int64(totalSent)), formatFileSize(fileSize), float64(totalSent)/float64(fileSize)*100)
	}

	// Send end command
	if err := p.dataChannel.SendText("FILE_END"); err != nil {
		fmt.Printf("❌ Failed to send end command: %v\n", err)
		return
	}

	fmt.Printf("✅ File sent successfully: %s\n", filename)
}

// WaitForConnection waits until the WebRTC connection is established
func (p *WebRTCPeer) WaitForConnection(timeout time.Duration) error {
	start := time.Now()
	
	for {
		if p.isConnected {
			return nil
		}
		
		if time.Since(start) > timeout {
			return fmt.Errorf("connection timeout")
		}
		
		time.Sleep(100 * time.Millisecond)
	}
}

// Close closes the WebRTC connection
func (p *WebRTCPeer) Close() error {
	if p.pc != nil {
		return p.pc.Close()
	}
	return nil
}

// IsConnected returns true if the peer is connected
func (p *WebRTCPeer) IsConnected() bool {
	return p.isConnected
}
