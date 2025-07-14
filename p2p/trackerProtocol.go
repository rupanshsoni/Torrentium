package p2p

import (
	"bufio"
	"fmt"
	"log"
	"strings"

	"torrentium/tracker"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
)

// yeh protocol 1.PeerID assign karta hai
//	    	    2.peer name(input) read karta hai
//	    	    3.Peer ko DB mein store karta hai
//			    4.is_online state ko track karta hai

func ShortenPeerID(id string) string {
	if len(id) >= 8 {
		return id[len(id)-8:]
	}
	return id
}

const TrackerProtocol = "/tracker/1.0.0"


func RegisterProtocol(h host.Host, t *tracker.Tracker) {
	h.SetStreamHandler(TrackerProtocol, func(s network.Stream) {
		defer s.Close()

		peerID := s.Conn().RemotePeer().String()
		r := bufio.NewReader(s)

		line, err := r.ReadString('\n')
		if err != nil {
			log.Println(" Error reading name:", err)
			return
		}

		name := strings.TrimSpace(line)
		if name == "" {
			s.Write([]byte(" Error: Name cannot be empty\n"))
			return
		}

		remoteAddr := s.Conn().RemoteMultiaddr().String()
		shortPeerID := ShortenPeerID(peerID)

		
		var ip string
		parts := strings.Split(remoteAddr, "/")
		for i := 0; i < len(parts)-1; i++ {
			if parts[i] == "ip4" || parts[i] == "ip6" {
				ip = parts[i+1]
				break
			}
		}

		
		if err := t.AddPeer(peerID, name, ip); err != nil {
			log.Println(" Error adding peer to DB:", err)
			return
		}

		log.Printf("New peer connected: %s (%s)", name, shortPeerID)

		
		s.Write([]byte(fmt.Sprintf("->  Welcome %s (%s)!\n", name, shortPeerID)))
		s.Write([]byte(fmt.Sprintf("-> Connected peers: %v\n", t.ListPeers())))

		
		go func() {
			for {
				_, err := r.ReadString('\n')
				if err != nil {
					log.Printf("-> Peer disconnected: %s (%s)", name, shortPeerID)
					t.RemovePeer(peerID)
					return
				}
				// pinging tracker for alive peer yha daal dunga
			}
		}()
	})
}
