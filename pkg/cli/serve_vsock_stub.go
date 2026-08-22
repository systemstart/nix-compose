//go:build !linux

package cli

import (
	"fmt"
	"net"
	"runtime"
)

func listenVsock(_ uint32) (net.Listener, error) {
	return nil, fmt.Errorf("vsock listener is only supported on Linux (current OS: %s)", runtime.GOOS)
}

func listenVsockPortFwd(_ uint32) (net.Listener, error) {
	return nil, fmt.Errorf("vsock port-forward listener is only supported on Linux (current OS: %s)", runtime.GOOS)
}
