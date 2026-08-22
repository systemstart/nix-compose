package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/systemstart/nix-compose/pkg/cri"
	runtimev1 "k8s.io/cri-api/pkg/apis/runtime/v1"
)

var (
	psAll      bool
	psQuiet    bool
	psFormat   string
	psServices bool
)

var psCmd = &cobra.Command{
	Use:   "ps [service...]",
	Short: "List running services",
	RunE: func(_ *cobra.Command, args []string) error {
		ctx := context.Background()
		criClient, err := requireCRI(ctx)
		if err != nil {
			return err
		}
		defer func() { _ = criClient.Close() }()
		return runPsCRI(ctx, criClient, resolveProject(), args)
	},
}

func init() {
	psCmd.Flags().BoolVarP(&psAll, "all", "a", false, "show all containers (including stopped)")
	psCmd.Flags().BoolVarP(&psQuiet, "quiet", "q", false, "only display container IDs")
	psCmd.Flags().StringVar(&psFormat, "format", "", "output format (table, json)")
	psCmd.Flags().BoolVar(&psServices, "services", false, "display services")
}

// runPsCRI lists containers via CRI with various output modes.
func runPsCRI(ctx context.Context, client *cri.Client, project string, services []string) error {
	containers, err := resolveContainers(ctx, client, project, services)
	if err != nil {
		return err
	}

	if !psAll {
		running := filterRunning(containers)
		// A service that crashed is otherwise indistinguishable from a project
		// that was never started: both print a bare header and exit 0. Keep the
		// default filter (scripts parse this table) but never let the crash
		// itself be silent.
		reportHidden(containers, running)
		containers = running
	}

	return formatPsOutput(containers)
}

// reportHidden warns on stderr about containers the default filter removed.
// Table output only — `-q`, `--services` and `--format json` are consumed by
// other programs, and stderr noise there would be a surprise.
func reportHidden(all, shown []containerInfo) {
	if psQuiet || psServices || psFormat != "" {
		return
	}
	visible := make(map[string]bool, len(shown))
	for _, c := range shown {
		visible[c.ContainerID] = true
	}
	var hidden []containerInfo
	for _, c := range all {
		if !visible[c.ContainerID] {
			hidden = append(hidden, c)
		}
	}
	if len(hidden) == 0 {
		return
	}
	for _, c := range hidden {
		fmt.Fprintf(os.Stderr, "Warning: service %q is %s\n", c.Service, containerStateDetail(c))
	}
	fmt.Fprintf(os.Stderr, "  → `nix-compose ps -a` lists them; `nix-compose logs %s` shows why\n", hidden[0].Service)
}

func filterRunning(containers []containerInfo) []containerInfo {
	var filtered []containerInfo
	for _, c := range containers {
		if c.State == runtimev1.ContainerState_CONTAINER_RUNNING {
			filtered = append(filtered, c)
		}
	}
	return filtered
}

func formatPsOutput(containers []containerInfo) error {
	if psServices {
		seen := make(map[string]bool)
		for _, c := range containers {
			if !seen[c.Service] {
				seen[c.Service] = true
				fmt.Println(c.Service)
			}
		}
		return nil
	}

	if psQuiet {
		for _, c := range containers {
			fmt.Println(c.ContainerID)
		}
		return nil
	}

	if psFormat == "json" {
		return printPsJSON(containers)
	}

	return printPsTable(containers)
}

type psJSONEntry struct {
	Service     string `json:"service"`
	State       string `json:"state"`
	Image       string `json:"image"`
	ContainerID string `json:"container_id"`
	// ExitCode is only meaningful for an exited container, so it is omitted
	// rather than reported as a misleading 0 for a running one.
	ExitCode *int32 `json:"exit_code,omitempty"`
}

func printPsJSON(containers []containerInfo) error {
	entries := make([]psJSONEntry, len(containers))
	for i, c := range containers {
		entries[i] = psJSONEntry{
			Service:     c.Service,
			State:       containerStateName(c.State),
			Image:       c.Image,
			ContainerID: c.ContainerID,
		}
		if c.State == runtimev1.ContainerState_CONTAINER_EXITED {
			code := c.ExitCode
			entries[i].ExitCode = &code
		}
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(entries); err != nil {
		return fmt.Errorf("encoding JSON: %w", err)
	}
	return nil
}

func printPsTable(containers []containerInfo) error {
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "SERVICE\tSTATE\tIMAGE\tCONTAINER ID")
	for _, c := range containers {
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
			c.Service,
			containerStateDetail(c),
			c.Image,
			shortContainerID(c.ContainerID),
		)
	}
	if err := w.Flush(); err != nil {
		return fmt.Errorf("writing table: %w", err)
	}
	return nil
}

// containerStateDetail is containerStateName plus the exit code, which is the
// single most useful fact about a container that is no longer running and was
// collected but never shown before.
func containerStateDetail(c containerInfo) string {
	name := containerStateName(c.State)
	if c.State == runtimev1.ContainerState_CONTAINER_EXITED {
		return fmt.Sprintf("%s (%d)", name, c.ExitCode)
	}
	return name
}

func containerStateName(state runtimev1.ContainerState) string {
	switch state {
	case runtimev1.ContainerState_CONTAINER_CREATED:
		return "created"
	case runtimev1.ContainerState_CONTAINER_RUNNING:
		return "running"
	case runtimev1.ContainerState_CONTAINER_EXITED:
		return "exited"
	case runtimev1.ContainerState_CONTAINER_UNKNOWN:
		return "unknown"
	default:
		return strings.ToLower(state.String())
	}
}

func shortContainerID(id string) string {
	id = strings.TrimPrefix(id, "sha256:")
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
