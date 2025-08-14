package client

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/libp2p/go-libp2p-core/host"
	"github.com/libp2p/go-libp2p-core/peer"
	"github.com/pion/webrtc/v3"

	"torrentium/internal/p2p"
)

// StartWebRTC initiates the WebRTC connection process
func StartWebRTC(h host.Host, remotePeerID peer.ID) {
	// Create a new WebRTC peer connection
	peerConnection, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		log.Fatalf("Failed to create peer connection: %v", err)
	}

	// Create a data channel
	dataChannel, err := peerConnection.CreateDataChannel("data", nil)
	if err != nil {
		log.Fatalf("Failed to create data channel: %v", err)
	}

	dataChannel.OnOpen(func() {
		log.Println("Data channel opened")
		go readData(dataChannel)
		writeData(dataChannel)
	})

	dataChannel.OnMessage(func(msg webrtc.DataChannelMessage) {
		fmt.Printf("Message from remote peer: %s\n", string(msg.Data))
	})

	peerConnection.OnICEConnectionStateChange(func(state webrtc.ICEConnectionState) {
		log.Printf("ICE connection state has changed: %s\n", state.String())
	})

	// Create an offer
	offer, err := peerConnection.CreateOffer(nil)
	if err != nil {
		log.Fatalf("Failed to create offer: %v", err)
	}

	// Set the local description
	err = peerConnection.SetLocalDescription(offer)
	if err != nil {
		log.Fatalf("Failed to set local description: %v", err)
	}

	// Send the offer to the remote peer
	offerJSON, err := json.Marshal(offer)
	if err != nil {
		log.Fatalf("Failed to marshal offer: %v", err)
	}

	stream, err := h.NewStream(context.Background(), remotePeerID, p2p.ProtocolID)
	if err != nil {
		log.Fatalf("Failed to create stream: %v", err)
	}

	_, err = stream.Write(offerJSON)
	if err != nil {
		log.Fatalf("Failed to send offer: %v", err)
	}

	// Read the answer from the remote peer
	buf := make([]byte, 4096)
	n, err := stream.Read(buf)
	if err != nil {
		log.Fatalf("Failed to read answer: %v", err)
	}

	var answer webrtc.SessionDescription
	err = json.Unmarshal(buf[:n], &answer)
	if err != nil {
		log.Fatalf("Failed to unmarshal answer: %v", err)
	}

	// Set the remote description
	err = peerConnection.SetRemoteDescription(answer)
	if err != nil {
		log.Fatalf("Failed to set remote description: %v", err)
	}

	log.Println("WebRTC connection established")
}

func readData(dc *webrtc.DataChannel) {
	for {
		// Read data from the data channel
	}
}

func writeData(dc *webrtc.DataChannel) {
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		text := scanner.Text()
		if err := dc.SendText(text); err != nil {
			log.Printf("Failed to send text: %v", err)
		}
	}
}

// Encode encodes the input into a string
func Encode(obj interface{}) string {
	b, err := json.Marshal(obj)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// Decode decodes the input from a string
func Decode(in string, obj interface{}) {
	err := json.Unmarshal([]byte(in), obj)
	if err != nil {
		panic(err)
	}
}

// Readln reads a line from stdin
func Readln() string {
	reader := bufio.NewReader(os.Stdin)
	text, _ := reader.ReadString('\n')
	return strings.TrimSpace(text)
}
