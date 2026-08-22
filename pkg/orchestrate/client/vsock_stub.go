//go:build !linux

package client

import (
	"context"
	"fmt"
	"net"
	"runtime"
)

// DialVsockRaw is not supported on non-Linux platforms.
func DialVsockRaw(_, _ uint32) (net.Conn, error) {
	return nil, fmt.Errorf("vsock transport is only supported on Linux (current OS: %s)", runtime.GOOS)
}

// DialVsock is not supported on non-Linux platforms.
func DialVsock(_ context.Context, _, _ uint32) (*Client, error) {
	return nil, fmt.Errorf("vsock transport is only supported on Linux (current OS: %s)", runtime.GOOS)
}
