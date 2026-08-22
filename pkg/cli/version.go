package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/systemstart/nix-compose/pkg/cri"
)

// Version is set at build time via -ldflags.
var Version = "dev"

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the version",
	Run: func(_ *cobra.Command, _ []string) {
		fmt.Printf("nix-compose %s\n", Version)

		ctx := context.Background()
		var c *cri.Client
		var err error

		if flagCRISocket != "" {
			c, err = cri.Dial(ctx, flagCRISocket)
		} else {
			c, err = cri.Detect(ctx)
		}

		if err != nil {
			fmt.Println("CRI: not detected")
			return
		}
		defer func() { _ = c.Close() }()

		v, err := c.Version(ctx)
		if err != nil {
			fmt.Println("CRI: not detected")
			return
		}

		fmt.Printf("CRI: %s %s via %s\n", v.RuntimeName, v.RuntimeVersion, c.Socket())
	},
}
