package portfwd

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"testing"
	"time"
)

// startPortFwdVM simulates the VM-side port-forward listener on a TCP
// port. It reads 2-byte port header, dials localhost:<port>, and
// proxies. Returns the listener address and a cancel function.
func startPortFwdVM(t *testing.T) (string, func()) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	vml := NewVMListener(lis)
	go func() { _ = vml.Serve(ctx) }()
	cleanup := func() {
		cancel()
		vml.Stop()
	}
	return lis.Addr().String(), cleanup
}

func TestForwarder_SinglePort(t *testing.T) {
	// Start an echo server.
	echoPort, echoStop := startEchoServer(t)
	defer echoStop()

	// Start a simulated VM-side listener.
	vmAddr, vmStop := startPortFwdVM(t)
	defer vmStop()

	// Create a TCP dialer that connects to the VM listener via TCP.
	dialer := func(_, _ uint32) (net.Conn, error) {
		return net.Dial("tcp", vmAddr)
	}

	mappings := []PortMapping{{
		HostPort: 0, // will be overridden below
		VMPort:   echoPort,
		Protocol: "tcp",
		Service:  fmt.Sprintf("echo (%d)", echoPort),
	}}

	// Use a random port for the host side.
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	hostPort := uint16(lis.Addr().(*net.TCPAddr).Port) //nolint:gosec // test port
	_ = lis.Close()                                    // free the port for the forwarder

	mappings[0].HostPort = hostPort
	mappings[0].VMPort = echoPort

	fwd := NewForwarder(3, 1025, mappings, dialer)
	if err := fwd.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer fwd.Stop()

	// Connect to the host-side forwarded port.
	conn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", hostPort))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	msg := []byte("end-to-end test")
	if _, err := conn.Write(msg); err != nil {
		t.Fatalf("write: %v", err)
	}
	if tc, ok := conn.(*net.TCPConn); ok {
		_ = tc.CloseWrite()
	}

	got, err := io.ReadAll(conn)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != string(msg) {
		t.Errorf("got %q, want %q", got, msg)
	}
}

func TestForwarder_MultiplePorts(t *testing.T) {
	echo1Port, echo1Stop := startEchoServer(t)
	defer echo1Stop()
	echo2Port, echo2Stop := startEchoServer(t)
	defer echo2Stop()

	vmAddr, vmStop := startPortFwdVM(t)
	defer vmStop()

	dialer := func(_, _ uint32) (net.Conn, error) {
		return net.Dial("tcp", vmAddr)
	}

	// Allocate two random host ports.
	lis1, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	hp1 := uint16(lis1.Addr().(*net.TCPAddr).Port) //nolint:gosec // test port
	_ = lis1.Close()

	lis2, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	hp2 := uint16(lis2.Addr().(*net.TCPAddr).Port) //nolint:gosec // test port
	_ = lis2.Close()

	mappings := []PortMapping{
		{HostPort: hp1, VMPort: echo1Port, Protocol: "tcp", Service: "echo1"},
		{HostPort: hp2, VMPort: echo2Port, Protocol: "tcp", Service: "echo2"},
	}

	fwd := NewForwarder(3, 1025, mappings, dialer)
	if err := fwd.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer fwd.Stop()

	// Test both ports.
	for _, hp := range []uint16{hp1, hp2} {
		conn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", hp))
		if err != nil {
			t.Fatalf("dial port %d: %v", hp, err)
		}
		msg := []byte(fmt.Sprintf("test-%d", hp))
		_, _ = conn.Write(msg)
		if tc, ok := conn.(*net.TCPConn); ok {
			_ = tc.CloseWrite()
		}
		got, _ := io.ReadAll(conn)
		if string(got) != string(msg) {
			t.Errorf("port %d: got %q, want %q", hp, got, msg)
		}
	}
}

func TestForwarder_PortConflict(t *testing.T) {
	// Occupy a port.
	blocker, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = blocker.Close() }()
	blockedPort := uint16(blocker.Addr().(*net.TCPAddr).Port) //nolint:gosec // test port

	dialer := func(_, _ uint32) (net.Conn, error) {
		return nil, fmt.Errorf("should not be called")
	}

	mappings := []PortMapping{
		{HostPort: blockedPort, VMPort: 80, Protocol: "tcp", Service: "conflict"},
	}

	fwd := NewForwarder(3, 1025, mappings, dialer)
	err = fwd.Start(context.Background())
	if err == nil {
		fwd.Stop()
		t.Fatal("expected error for port conflict")
	}
}

func TestForwarder_StopDraining(t *testing.T) {
	echoPort, echoStop := startEchoServer(t)
	defer echoStop()

	vmAddr, vmStop := startPortFwdVM(t)
	defer vmStop()

	dialer := func(_, _ uint32) (net.Conn, error) {
		return net.Dial("tcp", vmAddr)
	}

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	hp := uint16(lis.Addr().(*net.TCPAddr).Port) //nolint:gosec // test port
	_ = lis.Close()

	mappings := []PortMapping{
		{HostPort: hp, VMPort: echoPort, Protocol: "tcp", Service: "drain"},
	}

	fwd := NewForwarder(3, 1025, mappings, dialer)
	if err := fwd.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Open a connection.
	conn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", hp))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	// Send the port header manually via the forwarded path — the data
	// should flow through.
	msg := []byte("drain-test")
	_, _ = conn.Write(msg)

	// Stop should wait for the connection to drain.
	stopDone := make(chan struct{})
	go func() {
		fwd.Stop()
		close(stopDone)
	}()

	// Close the client-side connection to allow drain.
	_ = conn.Close()

	select {
	case <-stopDone:
		// OK
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not return after connection closed")
	}
}

func TestForwarder_HostIPBinding(t *testing.T) {
	echoPort, echoStop := startEchoServer(t)
	defer echoStop()

	vmAddr, vmStop := startPortFwdVM(t)
	defer vmStop()

	dialer := func(_, _ uint32) (net.Conn, error) {
		return net.Dial("tcp", vmAddr)
	}

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	hp := uint16(lis.Addr().(*net.TCPAddr).Port) //nolint:gosec // test port
	_ = lis.Close()

	mappings := []PortMapping{
		{HostIP: "127.0.0.1", HostPort: hp, VMPort: echoPort, Protocol: "tcp", Service: "bound"},
	}

	fwd := NewForwarder(3, 1025, mappings, dialer)
	if err := fwd.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer fwd.Stop()

	addrs := fwd.ForwardedAddrs()
	if len(addrs) != 1 {
		t.Fatalf("got %d addrs, want 1", len(addrs))
	}
	if addrs[0] != fmt.Sprintf("127.0.0.1:%d -> bound", hp) {
		t.Errorf("addr = %q", addrs[0])
	}

	// Verify we can connect.
	conn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", hp))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	msg := []byte("bound-test")
	_, _ = conn.Write(msg)
	if tc, ok := conn.(*net.TCPConn); ok {
		_ = tc.CloseWrite()
	}
	got, _ := io.ReadAll(conn)
	if string(got) != string(msg) {
		t.Errorf("got %q, want %q", got, msg)
	}
}

// TestForwarder_ForwardedAddrs verifies the display strings.
func TestForwarder_ForwardedAddrs(t *testing.T) {
	dialer := func(_, _ uint32) (net.Conn, error) {
		return nil, fmt.Errorf("unused")
	}
	mappings := []PortMapping{
		{HostPort: 8080, VMPort: 8080, Protocol: "tcp", Service: "web (80)"},
		{HostIP: "127.0.0.1", HostPort: 3000, VMPort: 3000, Protocol: "tcp", Service: "api (3000)"},
	}
	fwd := NewForwarder(3, 1025, mappings, dialer)
	addrs := fwd.ForwardedAddrs()
	if len(addrs) != 2 {
		t.Fatalf("got %d addrs, want 2", len(addrs))
	}
	if addrs[0] != "0.0.0.0:8080 -> web (80)" {
		t.Errorf("addrs[0] = %q", addrs[0])
	}
	if addrs[1] != "127.0.0.1:3000 -> api (3000)" {
		t.Errorf("addrs[1] = %q", addrs[1])
	}
}

// startHeaderInterceptor listens on a TCP port, reads the 2-byte port header
// from the first accepted connection, sends it to headerCh, and forwards
// the rest to the echo server at echoPort.
func startHeaderInterceptor(t *testing.T, echoPort uint16) (addr string, headerCh <-chan uint16) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	ch := make(chan uint16, 1)
	go func() {
		conn, err := lis.Accept()
		if err != nil {
			return
		}
		var buf [2]byte
		if _, err := io.ReadFull(conn, buf[:]); err != nil {
			_ = conn.Close()
			return
		}
		ch <- binary.BigEndian.Uint16(buf[:])

		target, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", echoPort))
		if err != nil {
			_ = conn.Close()
			return
		}
		_ = Proxy(conn, target)
	}()
	return lis.Addr().String(), ch
}

// Verify the handleConn function reads the 2-byte port header correctly
// by using a standalone VM listener and checking the wire format.
func TestForwarder_WireFormat(t *testing.T) {
	echoPort, echoStop := startEchoServer(t)
	defer echoStop()

	lisAddr, headerCh := startHeaderInterceptor(t, echoPort)

	dialer := func(_, _ uint32) (net.Conn, error) {
		return net.Dial("tcp", lisAddr)
	}

	tmpLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	hp := uint16(tmpLis.Addr().(*net.TCPAddr).Port) //nolint:gosec // test port
	_ = tmpLis.Close()

	mappings := []PortMapping{
		{HostPort: hp, VMPort: 4321, Protocol: "tcp", Service: "test"},
	}

	fwd := NewForwarder(3, 1025, mappings, dialer)
	if err := fwd.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer fwd.Stop()

	conn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", hp))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	_, _ = conn.Write([]byte("x"))
	_ = conn.Close()

	select {
	case port := <-headerCh:
		if port != 4321 {
			t.Errorf("wire header port = %d, want 4321", port)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for header")
	}
}
