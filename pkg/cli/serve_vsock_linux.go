//go:build linux

package cli

import (
	"fmt"
	"net"

	"github.com/mdlayher/vsock"
)

func listenVsock(port uint32) (net.Listener, error) {
	lis, err := vsock.Listen(port, nil)
	if err != nil {
		return nil, fmt.Errorf("vsock listen port %d: %w", port, err)
	}
	return lis, nil
}

func listenVsockPortFwd(port uint32) (net.Listener, error) {
	lis, err := vsock.Listen(port, nil)
	if err != nil {
		return nil, fmt.Errorf("vsock listen port-fwd %d: %w", port, err)
	}
	return lis, nil
}
