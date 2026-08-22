package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// AgentDocs holds the embedded SKILL.md content, set by the main package.
var AgentDocs string

var docsCmd = &cobra.Command{
	Use:   "docs",
	Short: "Print the embedded guide (SKILL.md)",
	Long:  "Prints the built-in documentation so users and AI coding agents can learn how to use nix-compose without needing access to the source tree.",
	RunE: func(_ *cobra.Command, _ []string) error {
		if AgentDocs == "" {
			return fmt.Errorf("agent docs not embedded in this build")
		}
		fmt.Print(AgentDocs)
		return nil
	},
}
