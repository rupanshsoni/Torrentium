package p2p

import (
	"bufio"
	"fmt"
	"log"
	"strings"

	"github.com/1amKhush/Torrentium/tracker"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
)

func ShortenPeerID(id string) string {
	if len(id) >= 8 {
		return id[len(id)-8:]
	}
	return id
}

const TrackerProtocol = "/tracker/1.0.0"

// yeh protocol 1.PeerID assign karta hai
//
//	    	    2.peer name(input) read karta hai
//	    	    3.Peer ko DB mein store karta hai
//			    4.is_online state ko track karta hai
func RegisterProtocol(h host.Host, t *tracker.Tracker) {
	h.SetStreamHandler(TrackerProtocol, func(s network.Stream) {
		// defer s.Close()

		peerID := s.Conn().RemotePeer().String()
		r := bufio.NewReader(s)

		line, err := r.ReadString('\n')
		if err != nil {
			log.Println("Error in Reading :", err)
		}

		name := strings.TrimSpace(line)

		remoteAddr := s.Conn().RemoteMultiaddr().String()

		shortPeerID := ShortenPeerID(peerID)

		go func() {
			for {
				_, err := r.ReadString('\n')
				if err != nil {
					log.Printf("%s (%v) has disconnected", name, shortPeerID)
					t.RemovePeer(peerID)
					return
				}

				// msg := strings.TrimSpace(line)
				// if msg == "ping" {
				// 	s.Write([]byte(fmt.Sprintf("Received from %s: %s", shortPeerID, msg)))
				// 	continue
				// }
			}
		}()

		var ip string
		parts := strings.Split(remoteAddr, "/")
		for i := 0; i < len(parts)-1; i++ {
			if parts[i] == "ip4" || parts[i] == "ip6" {
				ip = parts[i+1]
				break
			}
		}

		err = t.AddPeer(peerID, name, ip)
		if err != nil {
			log.Println("Error in Adding Peer to DB: ", err)
			return
		}

		log.Printf("New peer: %s (%s)", name, peerID)

		s.Write([]byte(fmt.Sprintf("Welcome %s!\n", name)))
		s.Write([]byte(fmt.Sprintf("Connected peers: %v\n", t.ListPeers())))
	})
}
