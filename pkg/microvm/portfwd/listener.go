package portfwd

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"sync"
	"time"
)

// headerTimeout is how long the VM listener waits for the 2-byte port
// header before closing the connection.
const headerTimeout = 5 * time.Second

// VMListener accepts connections on a vsock port and, for each
// connection, reads a 2-byte big-endian target port, dials
// 127.0.0.1:<port>, and proxies the two streams.
type VMListener struct {
	lis net.Listener

	mu   sync.Mutex
	wg   sync.WaitGroup
	done bool
}

// NewVMListener wraps an existing net.Listener (typically a vsock
// listener) into a VMListener.
func NewVMListener(lis net.Listener) *VMListener {
	return &VMListener{lis: lis}
}

// Serve runs the accept loop until the context is cancelled or the
// listener is closed. It returns nil when the context is done.
func (v *VMListener) Serve(ctx context.Context) error {
	go func() {
		<-ctx.Done()
		_ = v.lis.Close()
	}()

	for {
		conn, err := v.lis.Accept()
		if err != nil {
			v.mu.Lock()
			d := v.done
			v.mu.Unlock()
			if d || ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("portfwd: accept: %w", err)
		}
		v.wg.Add(1)
		go func() {
			defer v.wg.Done()
			v.handleConn(conn)
		}()
	}
}

// Stop closes the listener and waits for in-flight connections to
// drain.
func (v *VMListener) Stop() {
	v.mu.Lock()
	v.done = true
	v.mu.Unlock()
	_ = v.lis.Close()
	v.wg.Wait()
}

func (v *VMListener) handleConn(conn net.Conn) {
	defer func() { _ = conn.Close() }()

	// Read 2-byte big-endian target port.
	_ = conn.SetReadDeadline(time.Now().Add(headerTimeout))
	var portBuf [2]byte
	if _, err := conn.Read(portBuf[:]); err != nil {
		return
	}
	_ = conn.SetReadDeadline(time.Time{}) // clear deadline

	port := binary.BigEndian.Uint16(portBuf[:])
	if port == 0 {
		return
	}

	target, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 5*time.Second)
	if err != nil {
		return
	}

	_ = Proxy(conn, target)
}
