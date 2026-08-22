package cri

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"testing"

	"github.com/moby/spdystream"
)

func TestExec_ReturnsURL(t *testing.T) {
	env := setupContainerLifecycle(t)
	ctx := context.Background()
	if err := env.client.StartContainer(ctx, env.ctrID); err != nil {
		t.Fatalf("StartContainer: %v", err)
	}

	url, err := env.client.Exec(ctx, env.ctrID, []string{"bash"}, true, true)
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if url == "" {
		t.Error("expected non-empty URL")
	}
}

func TestExec_NoTTY(t *testing.T) {
	env := setupContainerLifecycle(t)
	ctx := context.Background()
	if err := env.client.StartContainer(ctx, env.ctrID); err != nil {
		t.Fatalf("StartContainer: %v", err)
	}

	url, err := env.client.Exec(ctx, env.ctrID, []string{"ls", "-la"}, false, false)
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if url == "" {
		t.Error("expected non-empty URL")
	}
}

func TestIsTerminal(t *testing.T) {
	// In test environment, stdin is not a terminal.
	if IsTerminal(0) {
		t.Skip("stdin is a terminal in this environment")
	}
}

func TestIsTerminal_Invalid(t *testing.T) {
	// An invalid fd should not be a terminal.
	if IsTerminal(999) {
		t.Error("fd 999 should not be a terminal")
	}
}

// startMockSPDYServer starts a TCP listener that accepts one connection and
// handles SPDY streams (stdout writes data, stdin is drained, stderr is closed).
// Returns the listener address and a cleanup function.
func startMockSPDYServer(t *testing.T, stdoutData string) net.Listener {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() {
		conn, err := lis.Accept()
		if err != nil {
			return
		}
		spdyConn, err := spdystream.NewConnection(conn, true)
		if err != nil {
			_ = conn.Close()
			return
		}
		spdyConn.Serve(func(stream *spdystream.Stream) {
			headers := stream.Headers()
			streamType := headers.Get("streamType")
			_ = stream.SendReply(http.Header{}, false)
			switch streamType {
			case "stdout":
				_, _ = stream.Write([]byte(stdoutData))
				_ = stream.Close()
			case "stdin":
				_, _ = io.Copy(io.Discard, stream)
			case "stderr":
				_ = stream.Close()
			case "resize":
				_, _ = io.Copy(io.Discard, stream)
			}
		})
	}()
	t.Cleanup(func() { _ = lis.Close() })
	return lis
}

func TestExecStream_FullIntegration(t *testing.T) {
	lis := startMockSPDYServer(t, "container output")
	addr := lis.Addr().String()
	url := fmt.Sprintf("http://%s/exec/test", addr)

	var stdout bytes.Buffer
	stdinReader := bytes.NewReader([]byte("input\n"))

	err := ExecStream(context.Background(), url, StreamOptions{
		Stdin:  stdinReader,
		Stdout: &stdout,
		Stderr: io.Discard,
		Tty:    false,
	})
	if err != nil {
		t.Fatalf("ExecStream: %v", err)
	}
	if stdout.String() != "container output" {
		t.Errorf("stdout = %q, want %q", stdout.String(), "container output")
	}
}

func TestExecStream_StdoutOnly(t *testing.T) {
	lis := startMockSPDYServer(t, "just stdout")
	addr := lis.Addr().String()
	url := fmt.Sprintf("http://%s/exec/test", addr)

	var stdout bytes.Buffer
	err := ExecStream(context.Background(), url, StreamOptions{
		Stdout: &stdout,
		Tty:    false,
	})
	if err != nil {
		t.Fatalf("ExecStream: %v", err)
	}
	if stdout.String() != "just stdout" {
		t.Errorf("stdout = %q, want %q", stdout.String(), "just stdout")
	}
}

func TestExecStream_WithTTY(t *testing.T) {
	lis := startMockSPDYServer(t, "tty output")
	addr := lis.Addr().String()
	url := fmt.Sprintf("http://%s/exec/test", addr)

	var stdout bytes.Buffer
	stdinReader := bytes.NewReader([]byte("cmd\n"))

	err := ExecStream(context.Background(), url, StreamOptions{
		Stdin:  stdinReader,
		Stdout: &stdout,
		Stderr: io.Discard,
		Tty:    true, // stderr should NOT be created when TTY is on
	})
	if err != nil {
		t.Fatalf("ExecStream: %v", err)
	}
	if stdout.String() != "tty output" {
		t.Errorf("stdout = %q, want %q", stdout.String(), "tty output")
	}
}

func TestExecStream_InvalidURL(t *testing.T) {
	err := ExecStream(context.Background(), "://invalid", StreamOptions{})
	if err == nil {
		t.Error("expected error for invalid URL")
	}
}

func TestExecStream_ConnectionRefused(t *testing.T) {
	// Use a port that's not listening.
	err := ExecStream(context.Background(), "http://127.0.0.1:1/exec/test", StreamOptions{
		Stdout: io.Discard,
	})
	if err == nil {
		t.Error("expected error for connection refused")
	}
}

func TestExecStream_WithMockSPDY(t *testing.T) {
	// Set up a mock SPDY server that echoes stdout.
	serverConn, clientConn := net.Pipe()

	// Server side: accept streams, write data to stdout stream.
	serverSPDY, err := spdystream.NewConnection(serverConn, true)
	if err != nil {
		t.Fatalf("server SPDY: %v", err)
	}

	var serverWg sync.WaitGroup
	serverWg.Add(1)
	go serverSPDY.Serve(func(stream *spdystream.Stream) {
		headers := stream.Headers()
		streamType := headers.Get("streamType")
		_ = stream.SendReply(http.Header{}, false)
		switch streamType {
		case "stdout":
			defer serverWg.Done()
			_, _ = stream.Write([]byte("hello from container"))
			_ = stream.Close()
		case "stdin":
			// Drain stdin.
			_, _ = io.Copy(io.Discard, stream)
		case "stderr":
			_ = stream.Close()
		}
	})

	// Client side.
	clientSPDY, err := spdystream.NewConnection(clientConn, false)
	if err != nil {
		t.Fatalf("client SPDY: %v", err)
	}
	go clientSPDY.Serve(spdystream.NoOpStreamHandler)

	var stdout bytes.Buffer
	var wg sync.WaitGroup

	// Create stdout stream.
	stdoutStream, err := clientSPDY.CreateStream(http.Header{
		"streamType": []string{"stdout"},
	}, nil, false)
	if err != nil {
		t.Fatalf("create stdout stream: %v", err)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(&stdout, stdoutStream)
	}()

	// Create stdin stream (just close it).
	stdinStream, err := clientSPDY.CreateStream(http.Header{
		"streamType": []string{"stdin"},
	}, nil, false)
	if err != nil {
		t.Fatalf("create stdin stream: %v", err)
	}
	_ = stdinStream.Close()

	serverWg.Wait()
	wg.Wait()

	if stdout.String() != "hello from container" {
		t.Errorf("stdout = %q, want %q", stdout.String(), "hello from container")
	}

	_ = clientSPDY.Close()
	_ = serverSPDY.Close()
}

func TestSetRawTerminal_NotATerminal(t *testing.T) {
	// Should fail because fd 999 is not a valid terminal.
	_, err := SetRawTerminal(999)
	if err == nil {
		t.Error("expected error for invalid fd")
	}
}
