// p2p/signaling.go
package p2p

import (
	"encoding/json"
	"fmt"
	"log"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	
)

const SignalingProtocolID = "/torrentium/webrtc-signaling/1.0"

// RegisterSignalingProtocol sets up the handler for incoming WebRTC offers.
func RegisterSignalingProtocol(h host.Host, onOffer func(offer, remotePeerID string) (string, error)) {
	h.SetStreamHandler(SignalingProtocolID, func(s network.Stream) {
		log.Printf("Received incoming signaling connection from %s", s.Conn().RemotePeer())
		defer s.Close()

		decoder := json.NewDecoder(s)
		encoder := json.NewEncoder(s)

		var offer string
		if err := decoder.Decode(&offer); err != nil {
			log.Printf("Error decoding offer: %v", err)
			return
		}

		answer, err := onOffer(offer, s.Conn().RemotePeer().String())
		if err != nil {
			log.Printf("Error handling offer: %v", err)
			encoder.Encode(fmt.Sprintf("ERROR:%s", err.Error()))
			return
		}

		if err := encoder.Encode(answer); err != nil {
			log.Printf("Error encoding answer: %v", err)
		}
	})
}