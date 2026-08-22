package cli

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/systemstart/nix-compose/pkg/cni"
	"github.com/systemstart/nix-compose/pkg/cri"
	"github.com/systemstart/nix-compose/pkg/logs"
	"github.com/systemstart/nix-compose/pkg/microvm/portfwd"
	"github.com/systemstart/nix-compose/pkg/orchestrate/server"
	"github.com/systemstart/nix-compose/pkg/volumes"
)

var (
	serveSocket      string
	serveVsockPort   uint32
	servePortFwdPort uint32
	serveLogBase     string
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Run orchestrate gRPC server (for microVM use)",
	Long:  "Start a gRPC server that exposes the orchestrate API over a unix socket or vsock.",
	RunE:  runServe,
}

func init() {
	serveCmd.Flags().StringVar(&serveSocket, "socket", "", "unix socket path")
	serveCmd.Flags().Uint32Var(&serveVsockPort, "vsock-port", 1024, "vsock port to listen on (used when --socket is not set)")
	serveCmd.Flags().Uint32Var(&servePortFwdPort, "portfwd-port", 1025, "vsock port for port-forwarding listener")
	serveCmd.Flags().StringVar(&serveLogBase, "log-base", logs.DefaultLogBase, "CRI log directory")
}

func runServe(_ *cobra.Command, _ []string) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	criClient, err := detectCRI(ctx)
	if err != nil {
		return fmt.Errorf("detecting CRI runtime: %w", err)
	}
	defer func() { _ = criClient.Close() }()

	cniStore := cni.NewStore()
	volStore, err := volumes.NewStore()
	if err != nil {
		return fmt.Errorf("volume store: %w", err)
	}

	srv := server.New(server.Config{
		CRIClient: criClient,
		CNIStore:  cniStore,
		VolStore:  volStore,
		LogBase:   serveLogBase,
	})

	var lis net.Listener
	if serveSocket != "" {
		// Unix socket mode.
		_ = os.Remove(serveSocket)
		lis, err = net.Listen("unix", serveSocket)
		if err != nil {
			return fmt.Errorf("listening on %s: %w", serveSocket, err)
		}
	} else {
		// Vsock mode.
		lis, err = listenVsock(serveVsockPort)
		if err != nil {
			return fmt.Errorf("listening on vsock port %d: %w", serveVsockPort, err)
		}

		// Start port-forward listener on a separate vsock port.
		pfLis, pfErr := listenVsockPortFwd(servePortFwdPort)
		if pfErr != nil {
			fmt.Printf("Warning: port-forward listener: %v\n", pfErr)
		} else {
			vml := portfwd.NewVMListener(pfLis)
			go func() { _ = vml.Serve(ctx) }()
			defer vml.Stop()
			fmt.Printf("Port-forward listener on vsock port %d\n", servePortFwdPort)
		}
	}

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(lis) }()

	select {
	case <-ctx.Done():
		fmt.Println("\nShutting down orchestrate server...")
		srv.GracefulStop()
		return nil
	case err := <-errCh:
		return err
	}
}

// detectCRI connects to a CRI runtime, failing if none is found.
func detectCRI(ctx context.Context) (*cri.Client, error) {
	if flagCRISocket != "" {
		c, err := cri.Dial(ctx, flagCRISocket)
		if err != nil {
			return nil, fmt.Errorf("dialing CRI socket: %w", err)
		}
		return c, nil
	}
	c, err := cri.Detect(ctx)
	if err != nil {
		return nil, fmt.Errorf("detecting CRI runtime: %w", err)
	}
	return c, nil
}
