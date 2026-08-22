// Package portfwd implements userspace TCP port forwarding over vsock
// for microVM mode. Published container ports are proxied between host
// TCP listeners and the VM's loopback interface via a vsock connection.
package portfwd

import (
	"fmt"
	"io"
	"net"
	"sync"
)

// Proxy copies data bidirectionally between a and b until either side
// closes or encounters an error. Both connections are closed on return.
func Proxy(a, b net.Conn) error {
	var wg sync.WaitGroup
	wg.Add(2)

	var errA, errB error

	go func() {
		defer wg.Done()
		_, errA = io.Copy(a, b)
		// Signal the other direction that no more data is coming.
		closeWrite(a)
	}()

	go func() {
		defer wg.Done()
		_, errB = io.Copy(b, a)
		closeWrite(b)
	}()

	wg.Wait()
	_ = a.Close()
	_ = b.Close()

	if errA != nil {
		return fmt.Errorf("proxy copy: %w", errA)
	}
	if errB != nil {
		return fmt.Errorf("proxy copy: %w", errB)
	}
	return nil
}

// closeWrite performs a half-close when the connection supports it.
func closeWrite(c net.Conn) {
	type halfCloser interface {
		CloseWrite() error
	}
	if hc, ok := c.(halfCloser); ok {
		_ = hc.CloseWrite()
	}
}
