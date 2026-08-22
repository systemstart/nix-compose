package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/systemstart/nix-compose/pkg/orchestrate/client"
)

var (
	flagFile            string
	flagProjectDir      string
	flagProjectName     string
	flagImpure          bool
	flagFlakeAttr       string
	flagProfiles        []string
	flagCRISocket       string
	flagRemoteSocket    string
	flagRemoteVsockCID  uint32
	flagRemoteVsockPort uint32
)

var rootCmd = &cobra.Command{
	Use:           "nix-compose",
	Short:         "Nix-powered container orchestration",
	Long:          "nix-compose evaluates Nix service definitions and orchestrates containers over CRI (containerd or CRI-O).",
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	rootCmd.PersistentFlags().StringVar(&flagFile, "file", "", "path to compose.nix or flake directory")
	rootCmd.PersistentFlags().StringVar(&flagProjectDir, "project-dir", "", "project directory (default: current directory)")
	rootCmd.PersistentFlags().StringVar(&flagProjectName, "project-name", "", "project name for compose")
	rootCmd.PersistentFlags().BoolVar(&flagImpure, "impure", true, "allow impure nix evaluation")
	rootCmd.PersistentFlags().StringVar(&flagFlakeAttr, "flake-attr", "", "flake attribute to evaluate (default: composition)")
	rootCmd.PersistentFlags().StringSliceVar(&flagProfiles, "profile", nil, "activate only services matching these profiles")
	rootCmd.PersistentFlags().StringVar(&flagCRISocket, "cri-socket", "", "CRI runtime socket path; auto-detected if omitted")
	rootCmd.PersistentFlags().StringVar(&flagRemoteSocket, "remote-socket", "",
		"orchestrate gRPC server unix socket (delegates commands to remote engine)")
	rootCmd.PersistentFlags().Uint32Var(&flagRemoteVsockCID, "remote-vsock-cid", 0,
		"vsock CID of the remote orchestrate server (enables vsock transport when > 0)")
	rootCmd.PersistentFlags().Uint32Var(&flagRemoteVsockPort, "remote-vsock-port", 1024,
		"vsock port of the remote orchestrate server")

	rootCmd.AddCommand(upCmd)
	rootCmd.AddCommand(downCmd)
	rootCmd.AddCommand(startCmd)
	rootCmd.AddCommand(stopCmd)
	rootCmd.AddCommand(restartCmd)
	rootCmd.AddCommand(psCmd)
	rootCmd.AddCommand(logsCmd)
	rootCmd.AddCommand(execCmd)
	rootCmd.AddCommand(pullCmd)
	rootCmd.AddCommand(createCmd)
	rootCmd.AddCommand(killCmd)
	rootCmd.AddCommand(rmCmd)
	rootCmd.AddCommand(topCmd)
	rootCmd.AddCommand(imagesCmd)
	rootCmd.AddCommand(renderCmd)
	rootCmd.AddCommand(planCmd)
	rootCmd.AddCommand(stateCmd)
	rootCmd.AddCommand(docsCmd)
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(serveCmd)
	rootCmd.AddCommand(driftCmd)
	rootCmd.AddCommand(rollbackCmd)
	rootCmd.AddCommand(graphCmd)
	rootCmd.AddCommand(importCmd)
	rootCmd.AddCommand(suggestCmd)
	rootCmd.AddCommand(doctorCmd)
}

// Execute runs the root command.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		// A command that has already reported everything the user needs
		// returns an empty message purely to set the exit status.
		if msg := err.Error(); msg != "" {
			fmt.Fprintln(os.Stderr, msg)
		}
		os.Exit(1)
	}
}

// dialRemote connects to the orchestrate gRPC server via unix socket or vsock.
// Returns nil if neither --remote-socket nor --remote-vsock-cid is set.
func dialRemote(ctx context.Context) *client.Client {
	if flagRemoteSocket != "" {
		c, err := client.Dial(ctx, flagRemoteSocket)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: cannot connect to remote socket: %v\n", err)
			return nil
		}
		return c
	}
	if flagRemoteVsockCID > 0 {
		c, err := client.DialVsock(ctx, flagRemoteVsockCID, flagRemoteVsockPort)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: cannot connect via vsock cid=%d port=%d: %v\n",
				flagRemoteVsockCID, flagRemoteVsockPort, err)
			return nil
		}
		return c
	}
	return nil
}

func projectDir() string {
	if flagProjectDir != "" {
		return flagProjectDir
	}
	dir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: cannot determine working directory: %v\n", err)
		os.Exit(1)
	}
	return dir
}
