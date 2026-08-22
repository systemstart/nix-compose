package cri

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"

	"github.com/moby/spdystream"
	"golang.org/x/term"
	runtimev1 "k8s.io/cri-api/pkg/apis/runtime/v1"
)

// Exec calls the CRI Exec RPC and returns the streaming URL for an
// interactive session. The caller is expected to connect to this URL
// via SPDY to stream stdin/stdout/stderr.
func (c *Client) Exec(ctx context.Context, containerID string, cmd []string, tty, stdin bool) (string, error) {
	resp, err := c.runtime.Exec(ctx, &runtimev1.ExecRequest{
		ContainerId: containerID,
		Cmd:         cmd,
		Tty:         tty,
		Stdin:       stdin,
		Stdout:      true,
		Stderr:      !tty, // when TTY is on, stderr is multiplexed into stdout
	})
	if err != nil {
		return "", fmt.Errorf("cri: exec in %s: %w", containerID, err)
	}
	return resp.GetUrl(), nil
}

// StreamOptions configures the I/O for an interactive exec stream.
type StreamOptions struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	Tty    bool
}

// ExecStream connects to the CRI streaming URL via SPDY, creates
// stdin/stdout/stderr streams, and copies data between the local
// terminal and the remote container. It blocks until the session ends.
func ExecStream(ctx context.Context, rawURL string, opts StreamOptions) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("parsing exec URL: %w", err)
	}

	conn, err := dialSPDY(u)
	if err != nil {
		return fmt.Errorf("SPDY dial: %w", err)
	}
	defer func() { _ = conn.Close() }()

	var wg sync.WaitGroup

	if err := attachStreams(ctx, conn, opts, &wg); err != nil {
		return err
	}

	wg.Wait()
	return nil
}

// attachStreams creates SPDY streams for stdin/stdout/stderr/resize and
// starts goroutines to copy data.
func attachStreams(ctx context.Context, conn *spdystream.Connection, opts StreamOptions, wg *sync.WaitGroup) error {
	if opts.Stdin != nil {
		s, err := createSPDYStream(conn, "stdin")
		if err != nil {
			return err
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = io.Copy(s, opts.Stdin)
			_ = s.Close()
		}()
	}

	if opts.Stdout != nil {
		s, err := createSPDYStream(conn, "stdout")
		if err != nil {
			return err
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = io.Copy(opts.Stdout, s)
		}()
	}

	if opts.Stderr != nil && !opts.Tty {
		s, err := createSPDYStream(conn, "stderr")
		if err != nil {
			return err
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = io.Copy(opts.Stderr, s)
		}()
	}

	if opts.Tty {
		s, err := createSPDYStream(conn, "resize")
		if err != nil {
			return err
		}
		sendInitialResize(s)
		watchResize(ctx, s)
	}

	return nil
}

func createSPDYStream(conn *spdystream.Connection, streamType string) (*spdystream.Stream, error) {
	s, err := conn.CreateStream(http.Header{
		"streamType": []string{streamType},
	}, nil, false)
	if err != nil {
		return nil, fmt.Errorf("creating %s stream: %w", streamType, err)
	}
	return s, nil
}

// dialSPDY establishes a SPDY connection to the given URL.
func dialSPDY(u *url.URL) (*spdystream.Connection, error) {
	host := u.Host
	if !strings.Contains(host, ":") {
		if u.Scheme == "https" {
			host += ":443"
		} else {
			host += ":80"
		}
	}

	var rawConn net.Conn
	var err error
	if u.Scheme == "https" {
		rawConn, err = tls.Dial("tcp", host, &tls.Config{InsecureSkipVerify: true}) //nolint:gosec // CRI streaming is local
	} else {
		rawConn, err = net.Dial("tcp", host)
	}
	if err != nil {
		return nil, fmt.Errorf("dialing %s: %w", host, err)
	}

	spdyConn, err := spdystream.NewConnection(rawConn, false)
	if err != nil {
		_ = rawConn.Close()
		return nil, fmt.Errorf("SPDY handshake: %w", err)
	}
	go spdyConn.Serve(spdystream.NoOpStreamHandler)

	return spdyConn, nil
}

// sendInitialResize sends the current terminal size on the resize stream.
func sendInitialResize(stream *spdystream.Stream) {
	w, h, err := term.GetSize(int(os.Stdin.Fd())) //nolint:gosec // fd fits in int
	if err != nil {
		return
	}
	msg := fmt.Sprintf(`{"Width":%d,"Height":%d}`, w, h)
	_, _ = stream.Write([]byte(msg))
}

// watchResize listens for SIGWINCH and sends updated terminal dimensions
// on the resize stream.
func watchResize(ctx context.Context, stream *spdystream.Stream) {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGWINCH)
	go func() {
		defer signal.Stop(sigCh)
		for {
			select {
			case <-ctx.Done():
				return
			case <-sigCh:
				w, h, err := term.GetSize(int(os.Stdin.Fd())) //nolint:gosec // fd fits in int
				if err != nil {
					continue
				}
				msg := fmt.Sprintf(`{"Width":%d,"Height":%d}`, w, h)
				_, _ = stream.Write([]byte(msg))
			}
		}
	}()
}

// SetRawTerminal puts the terminal into raw mode and returns a restore
// function that must be called to reset the terminal state.
func SetRawTerminal(fd int) (restore func(), err error) {
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return nil, fmt.Errorf("setting raw mode: %w", err)
	}
	return func() { _ = term.Restore(fd, oldState) }, nil
}

// IsTerminal returns true if the given file descriptor is a terminal.
func IsTerminal(fd int) bool {
	return term.IsTerminal(fd)
}
