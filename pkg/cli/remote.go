package cli

import (
	"fmt"
	"os"

	orchestratev1 "github.com/systemstart/nix-compose/api/orchestrate/v1"
)

// printRemoteActions prints plan/apply actions from the remote server.
func printRemoteActions(actions []*orchestratev1.Action) {
	for _, a := range actions {
		symbol := " "
		switch a.Type {
		case "create":
			symbol = "+"
		case "update":
			symbol = "~"
		case "destroy":
			symbol = "-"
		case "noop":
			continue
		}
		_, _ = fmt.Fprintf(os.Stdout, "  %s %-8s %-20s (%s)\n", symbol, a.Kind, a.ResourceId, a.Reason)
	}
}

// printRemoteSummary prints a plan/apply summary from remote response counts.
func printRemoteSummary(creates, updates, destroys, noops int32) {
	fmt.Println()
	fmt.Printf("Plan: %d to create, %d to update, %d to destroy, %d unchanged\n",
		creates, updates, destroys, noops)
}
