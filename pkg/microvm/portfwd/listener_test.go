package portfwd

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"
)

// startEchoServer starts a TCP echo server on a random port and returns
// the port number and a cancel function.
func startEchoServer(t *testing.T) (uint16, func()) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("echo listen: %v", err)
	}
	port := uint16(lis.Addr().(*net.TCPAddr).Port) //nolint:gosec // test port
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		for {
			conn, err := lis.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = conn.Close() }()
				_, _ = io.Copy(conn, conn)
			}()
		}
	}()
	go func() {
		<-ctx.Done()
		_ = lis.Close()
	}()
	return port, cancel
}

func TestVMListener_BasicForward(t *testing.T) {
	echoPort, echoStop := startEchoServer(t)
	defer echoStop()

	// Create a TCP listener to act as the "vsock" side.
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	lisAddr := lis.Addr().String()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	vml := NewVMListener(lis)
	serveDone := make(chan error, 1)
	go func() { serveDone <- vml.Serve(ctx) }()

	// Dial the VM listener and send the port header.
	conn, err := net.Dial("tcp", lisAddr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	var portBuf [2]byte
	binary.BigEndian.PutUint16(portBuf[:], echoPort)
	if _, err := conn.Write(portBuf[:]); err != nil {
		t.Fatalf("write header: %v", err)
	}

	// Send data and verify echo.
	msg := []byte("hello portfwd")
	if _, err := conn.Write(msg); err != nil {
		t.Fatalf("write data: %v", err)
	}

	// Half-close write side so the echo server returns.
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

	cancel()
	<-serveDone
}

func TestVMListener_InvalidPort(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	lisAddr := lis.Addr().String()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	vml := NewVMListener(lis)
	serveDone := make(chan error, 1)
	go func() { serveDone <- vml.Serve(ctx) }()

	// Dial and send port 0 — should be rejected.
	conn, err := net.Dial("tcp", lisAddr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	var portBuf [2]byte
	binary.BigEndian.PutUint16(portBuf[:], 0)
	_, _ = conn.Write(portBuf[:])

	// The connection should be closed by the VM listener.
	buf := make([]byte, 1)
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, readErr := conn.Read(buf)
	if readErr == nil {
		t.Error("expected connection to be closed for port 0")
	}
	_ = conn.Close()

	cancel()
	<-serveDone
}

func TestVMListener_HeaderTimeout(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	lisAddr := lis.Addr().String()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	vml := NewVMListener(lis)
	serveDone := make(chan error, 1)
	go func() { serveDone <- vml.Serve(ctx) }()

	// Dial but don't send anything — should timeout and close.
	conn, err := net.Dial("tcp", lisAddr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	// Wait for the connection to be closed by timeout.
	buf := make([]byte, 1)
	_ = conn.SetReadDeadline(time.Now().Add(headerTimeout + 2*time.Second))
	_, readErr := conn.Read(buf)
	if readErr == nil {
		t.Error("expected connection to be closed after header timeout")
	}
	_ = conn.Close()

	cancel()
	<-serveDone
}

func TestVMListener_ContextCancel(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	vml := NewVMListener(lis)
	serveDone := make(chan error, 1)
	go func() { serveDone <- vml.Serve(ctx) }()

	// Cancel immediately.
	cancel()

	select {
	case err := <-serveDone:
		if err != nil {
			t.Errorf("Serve returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after cancel")
	}
}
