package portfwd

import (
	"bytes"
	"io"
	"net"
	"testing"
)

// tcpConnPair creates a pair of connected TCP connections that support
// half-close (CloseWrite), unlike net.Pipe.
func tcpConnPair(t *testing.T) (net.Conn, net.Conn) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()

	var server net.Conn
	var acceptErr error
	ch := make(chan struct{})
	go func() {
		server, acceptErr = ln.Accept()
		close(ch)
	}()

	client, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	<-ch
	if acceptErr != nil {
		_ = client.Close()
		t.Fatal(acceptErr)
	}
	return client, server
}

func TestProxy_Bidirectional(t *testing.T) {
	// Use TCP connections (not net.Pipe) so that CloseWrite works.
	a1, a2 := tcpConnPair(t)
	b1, b2 := tcpConnPair(t)

	done := make(chan error, 1)
	go func() { done <- Proxy(a2, b1) }()

	// Write from a-side, read from b-side.
	want := []byte("hello from a")
	go func() {
		_, _ = a1.Write(want)
		closeWrite(a1)
	}()

	got := make([]byte, len(want))
	if _, err := io.ReadFull(b2, got); err != nil {
		t.Fatalf("read from b: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}

	// Write from b-side, read from a-side.
	want2 := []byte("hello from b")
	go func() {
		_, _ = b2.Write(want2)
		_ = b2.Close()
	}()

	got2, err := io.ReadAll(a1)
	if err != nil {
		t.Fatalf("read from a: %v", err)
	}
	if !bytes.Equal(got2, want2) {
		t.Errorf("got %q, want %q", got2, want2)
	}

	_ = a1.Close()
	<-done
}

func TestProxy_OneDirectionClose(t *testing.T) {
	a1, a2 := tcpConnPair(t)
	b1, b2 := tcpConnPair(t)

	done := make(chan error, 1)
	go func() { done <- Proxy(a2, b1) }()

	// Close a-side immediately — b-side should see EOF.
	_ = a1.Close()

	buf := make([]byte, 1)
	_, err := b2.Read(buf)
	if err != io.EOF && err != io.ErrClosedPipe {
		t.Fatalf("expected EOF or ErrClosedPipe, got %v", err)
	}

	_ = b2.Close()
	<-done
}

func TestProxy_LargeTransfer(t *testing.T) {
	a1, a2 := tcpConnPair(t)
	b1, b2 := tcpConnPair(t)

	done := make(chan error, 1)
	go func() { done <- Proxy(a2, b1) }()

	// 1 MB transfer.
	size := 1 << 20
	data := make([]byte, size)
	for i := range data {
		data[i] = byte(i % 251)
	}

	go func() {
		_, _ = a1.Write(data)
		_ = a1.Close()
	}()

	got, err := io.ReadAll(b2)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != size {
		t.Fatalf("got %d bytes, want %d", len(got), size)
	}
	if !bytes.Equal(got, data) {
		t.Error("data mismatch")
	}

	_ = b2.Close()
	<-done
}
