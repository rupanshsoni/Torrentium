package p2p

import (
	"context"
	"crypto/rand"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/libp2p/go-libp2p"
	dht "github.com/libp2p/go-libp2p-kad-dht"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/multiformats/go-multiaddr"
	ma "github.com/multiformats/go-multiaddr"
)

const privKeyFile = "private_key"

// NewHost creates a new libp2p host with a Kademlia DHT.
func NewHost(ctx context.Context, listenAddr string) (host.Host, *dht.IpfsDHT, error) {
	priv, err := loadOrGeneratePrivateKey()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to load/generate private key: %w", err)
	}

	maddr, err := ma.NewMultiaddr(listenAddr)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse listen address '%s': %w", listenAddr, err)
	}

	var idht *dht.IpfsDHT
	fmt.Println("debugging 11")

	// Configure the static relay
	relayAddr, err := multiaddr.NewMultiaddr("/dns4/relay-torrentium-9ztp.onrender.com/tcp/443/wss/p2p/12D3KooWCrg8BnhFDXwM6GNPVmQHZjfkadQ5D7xG7nYBdUUJtcNS")
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse relay address: %w", err)
	}
	relayInfo, err := peer.AddrInfoFromP2pAddr(relayAddr)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get AddrInfo from relay address: %w", err)
	}

	h, err := libp2p.New(
		libp2p.Identity(priv),
		libp2p.ListenAddrs(maddr),
		// Enable local network optimizations
		libp2p.EnableRelay(),
		// Use the static relay as backup only
		libp2p.EnableAutoRelayWithStaticRelays([]peer.AddrInfo{*relayInfo}),
		// Enable hole punching for better connectivity
		libp2p.EnableHolePunching(),
	)
	if err != nil {
		panic(err)
	}

	// Create DHT with reference to host `h`
	idht, err = dht.New(ctx, h)
	if err != nil {
		panic(err)
	}
	fmt.Println("debugging 14")

	if err != nil {
		return nil, nil, err
	}

	// Bootstrap the DHT
	if err := idht.Bootstrap(ctx); err != nil {
		return nil, nil, err
	}

	log.Printf("Host created with ID: %s", h.ID())
	log.Printf("Host listening on: %s/p2p/%s", h.Addrs()[0], h.ID())
	return h, idht, nil
}

// Add these improvements to your main function and Client struct

// Improved bootstrapping function
func Bootstrap(ctx context.Context, h host.Host, d *dht.IpfsDHT) error {
	// Updated bootstrap nodes with more reliable addresses
	bootstrapNodes := []string{
		// Official IPFS bootstrap nodes (mix of DNS and direct IP)
		"/dnsaddr/bootstrap.libp2p.io/p2p/QmNnooDu7bfjPFoTZYxMNLWUQJyrVwtbZg5gBMjTezGAJN",
		"/dnsaddr/bootstrap.libp2p.io/p2p/QmQCU2EcMqAqQPR2i9bChDtGNJchTbq5TbXJJ16u19uLTa",
		"/dnsaddr/bootstrap.libp2p.io/p2p/QmbLHAnMoJPWSCR5Zp7VCk8JpNUQLoUPF3HfrDAQGS52a8",
		"/dnsaddr/bootstrap.libp2p.io/p2p/QmcZf59bWwK5XFi76CZX89HWoNT4gEoNA7MzZqaGzyCu5w",

		// Direct IP addresses as fallback (more reliable)
		"/ip4/104.131.131.82/tcp/4001/p2p/QmaCpDMGvV2BGHeYERUEnRQAwe3N8SzbUtfsmvsqQLuvuJ",
		"/ip4/104.236.179.241/tcp/4001/p2p/QmSoLPppuBtQSGwKDZT2M73ULpjvfd3aZ6ha4oFGL1KrGM",
		"/ip4/128.199.219.111/tcp/4001/p2p/QmSoLSafTMBsPKadTEgaXctDQVcqN88CNLHXMkTNwMKPnu",
		"/ip4/104.236.76.40/tcp/4001/p2p/QmSoLV4Bbm51jM9C4gDYZQ9Cy3U6aXMJDAbzgu2fzaDs64",

		// Alternative public nodes
		"/ip4/147.75.77.187/tcp/4001/p2p/QmQCU2EcMqAqQPR2i9bChDtGNJchTbq5TbXJJ16u19uLTa",
		"/ip6/2604:1380:1000:6000::1/tcp/4001/p2p/QmQCU2EcMqAqQPR2i9bChDtGNJchTbq5TbXJJ16u19uLTa",
	}

	fmt.Println("Connecting to bootstrap nodes...")
	connected := 0
	required := 5
	for i, addrStr := range bootstrapNodes {
		// Stop early if we have enough connections
		if connected >= required {
			fmt.Printf("Already connected to %d nodes, stopping early\n", connected)
			break
		}

		addr, err := multiaddr.NewMultiaddr(addrStr)
		if err != nil {
			log.Printf("Invalid bootstrap address %s: %v", addrStr, err)
			continue
		}

		pi, err := peer.AddrInfoFromP2pAddr(addr)
		if err != nil {
			log.Printf("Failed to parse bootstrap peer info %s: %v", addrStr, err)
			continue
		}

		// Use shorter timeout for individual connections
		connectCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		if err := h.Connect(connectCtx, *pi); err != nil {
			log.Printf("Failed to connect to bootstrap node %s: %v", pi.ID, err)
		} else {
			fmt.Printf("Connected to bootstrap node: %s\n", pi.ID)
			connected++
		}
		cancel()

		// Add small delay between connections to avoid overwhelming
		if i < len(bootstrapNodes)-1 {
			time.Sleep(500 * time.Millisecond)
		}
	}

	if connected < required {
		return fmt.Errorf("insufficient bootstrap connections: got %d, need at least %d", connected, required)
	}

	fmt.Printf("Successfully connected to %d bootstrap nodes (minimum %d required)\n", connected, required)

	// Bootstrap the DHT
	fmt.Println("Bootstrapping DHT...")
	if err := d.Bootstrap(ctx); err != nil {
		return fmt.Errorf("failed to bootstrap DHT: %w", err)
	}

	// Wait for DHT to become ready with better feedback
	fmt.Println("Waiting for DHT to become ready...")
	readyTimeout := time.After(45 * time.Second)
	checkTicker := time.NewTicker(5 * time.Second)
	defer checkTicker.Stop()

	for {
		select {
		case <-readyTimeout:
			routingTableSize := d.RoutingTable().Size()
			if routingTableSize > 0 {
				fmt.Printf("DHT partially ready (routing table size: %d), continuing...\n", routingTableSize)
			} else {
				fmt.Println("DHT bootstrap timeout, but continuing anyway...")
			}
			return nil

		case <-checkTicker.C:
			routingTableSize := d.RoutingTable().Size()
			fmt.Printf("DHT routing table size: %d\n", routingTableSize)
			if routingTableSize >= 10 {
				fmt.Println("DHT is ready with good routing table!")
				return nil
			}

		case <-d.RefreshRoutingTable():
			routingTableSize := d.RoutingTable().Size()
			fmt.Printf("DHT routing table refreshed (size: %d)\n", routingTableSize)
			if routingTableSize >= 5 {
				fmt.Println("DHT is ready!")
				return nil
			}
		}
	}
}

func loadOrGeneratePrivateKey() (crypto.PrivKey, error) {
	privBytes, err := os.ReadFile(privKeyFile)
	if os.IsNotExist(err) {
		priv, _, err := crypto.GenerateEd25519Key(rand.Reader)
		if err != nil {
			return nil, err
		}

		privBytes, err := crypto.MarshalPrivateKey(priv)
		if err != nil {
			return nil, err
		}

		if err := os.WriteFile(privKeyFile, privBytes, 0600); err != nil {
			return nil, fmt.Errorf("failed to write private key to file: %w", err)
		}

		log.Println("Generated new libp2p private key.")
		return priv, nil
	} else if err != nil {
		return nil, err
	}

	log.Println("Loaded existing libp2p private key.")
	return crypto.UnmarshalPrivateKey(privBytes)
}
