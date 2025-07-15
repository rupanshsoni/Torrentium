package p2p

import (
	"context"
	"fmt"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/host"
)

func NewHost(ctx context.Context) (host.Host, error) {
	h, err := libp2p.New()
	if err != nil {
		return nil, err
	}

	// fmt.Println("Host ID: ", h.ID())

	addr := h.Addrs()[0].String()
	
	fmt.Printf("Tracker listning on: %s/p2p/%s\n", addr, h.ID())

	return h, nil
}
