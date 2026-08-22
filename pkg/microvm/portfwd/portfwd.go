package portfwd

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"sync"
)

// PortMapping describes a single port to forward from the host into the VM.
type PortMapping struct {
	HostIP   string // bind address; "" means "0.0.0.0"
	HostPort uint16
	VMPort   uint16 // same as HostPort — CNI portmap inside the VM listens on HostPort
	Protocol string // "tcp" only
	Service  string // display label, e.g. "web (80)"
}

// Dialer opens a connection to the VM. In production this is vsock.Dial;
// in tests it can be a plain TCP dialer.
type Dialer func(cid, port uint32) (net.Conn, error)

// Forwarder opens TCP listeners on the host for each PortMapping and
// proxies connections through the VM's port-forward vsock listener.
type Forwarder struct {
	cid         uint32
	portFwdPort uint32
	mappings    []PortMapping
	dialer      Dialer

	mu        sync.Mutex
	listeners []net.Listener
	wg        sync.WaitGroup
	closed    bool
}

// NewForwarder creates a new host-side port forwarder.
//
//   - cid is the VM's vsock context ID.
//   - portFwdPort is the vsock port where the VM's port-forward listener runs.
//   - mappings lists the ports to forward.
//   - dialer opens vsock connections to the VM.
func NewForwarder(cid, portFwdPort uint32, mappings []PortMapping, dialer Dialer) *Forwarder {
	return &Forwarder{
		cid:         cid,
		portFwdPort: portFwdPort,
		mappings:    mappings,
		dialer:      dialer,
	}
}

// Start opens a TCP listener for each mapping and begins accepting
// connections. It returns immediately after all listeners are open.
func (f *Forwarder) Start(ctx context.Context) error {
	for i := range f.mappings {
		m := &f.mappings[i]
		hostIP := m.HostIP
		if hostIP == "" {
			hostIP = "0.0.0.0"
		}
		addr := fmt.Sprintf("%s:%d", hostIP, m.HostPort)

		lis, err := net.Listen("tcp", addr)
		if err != nil {
			f.Stop()
			return fmt.Errorf("portfwd: listen %s: %w", addr, err)
		}

		f.mu.Lock()
		f.listeners = append(f.listeners, lis)
		f.mu.Unlock()

		f.wg.Add(1)
		go f.acceptLoop(ctx, lis, m)
	}
	return nil
}

// Stop closes all listeners and waits for in-flight connections to
// drain.
func (f *Forwarder) Stop() {
	f.mu.Lock()
	f.closed = true
	for _, lis := range f.listeners {
		_ = lis.Close()
	}
	f.mu.Unlock()
	f.wg.Wait()
}

// ForwardedAddrs returns human-readable descriptions of each forwarded
// port, suitable for printing to the user.
func (f *Forwarder) ForwardedAddrs() []string {
	addrs := make([]string, 0, len(f.mappings))
	for _, m := range f.mappings {
		hostIP := m.HostIP
		if hostIP == "" {
			hostIP = "0.0.0.0"
		}
		addrs = append(addrs, fmt.Sprintf("%s:%d -> %s", hostIP, m.HostPort, m.Service))
	}
	return addrs
}

func (f *Forwarder) acceptLoop(ctx context.Context, lis net.Listener, m *PortMapping) {
	defer f.wg.Done()
	for {
		conn, err := lis.Accept()
		if err != nil {
			f.mu.Lock()
			closed := f.closed
			f.mu.Unlock()
			if closed || ctx.Err() != nil {
				return
			}
			continue
		}
		f.wg.Add(1)
		go func() {
			defer f.wg.Done()
			f.handleConn(conn, m)
		}()
	}
}

func (f *Forwarder) handleConn(conn net.Conn, m *PortMapping) {
	defer func() { _ = conn.Close() }()

	// Dial the VM's port-forward vsock listener.
	vmConn, err := f.dialer(f.cid, f.portFwdPort)
	if err != nil {
		return
	}

	// Send the 2-byte big-endian target port.
	var portBuf [2]byte
	binary.BigEndian.PutUint16(portBuf[:], m.VMPort)
	if _, err := vmConn.Write(portBuf[:]); err != nil {
		_ = vmConn.Close()
		return
	}

	_ = Proxy(conn, vmConn)
}
