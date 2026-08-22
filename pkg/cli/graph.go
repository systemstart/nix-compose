package cli

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/systemstart/nix-compose/pkg/orchestrate"
)

var graphFormat string

var graphCmd = &cobra.Command{
	Use:   "graph",
	Short: "Inspect the dependency graph",
}

var graphShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Visualise the full resource DAG",
	RunE:  runGraphShow,
}

var graphDepsCmd = &cobra.Command{
	Use:   "deps <resource-id>",
	Short: "Show transitive dependencies of a resource",
	Args:  cobra.ExactArgs(1),
	RunE:  runGraphDeps,
}

var graphImpactCmd = &cobra.Command{
	Use:   "impact <resource-id>",
	Short: "Show transitive dependents of a resource",
	Args:  cobra.ExactArgs(1),
	RunE:  runGraphImpact,
}

func init() {
	graphShowCmd.Flags().StringVar(&graphFormat, "format", "text", "output format: text or dot")
	graphCmd.AddCommand(graphShowCmd)
	graphCmd.AddCommand(graphDepsCmd)
	graphCmd.AddCommand(graphImpactCmd)
}

func runGraphShow(_ *cobra.Command, _ []string) error {
	engine, err := openReadOnlyEngine()
	if err != nil {
		return fmt.Errorf("opening engine: %w", err)
	}
	defer func() { _ = engine.Close() }()

	nodes, edges, err := engine.Graph()
	if err != nil {
		return fmt.Errorf("loading graph: %w", err)
	}

	if len(nodes) == 0 {
		fmt.Println("No resources found.")
		return nil
	}

	switch graphFormat {
	case "dot":
		printDOT(nodes, edges)
	default:
		printTextTree(nodes, edges)
	}
	return nil
}

func runGraphDeps(_ *cobra.Command, args []string) error {
	resourceID := args[0]

	engine, err := openReadOnlyEngine()
	if err != nil {
		return fmt.Errorf("opening engine: %w", err)
	}
	defer func() { _ = engine.Close() }()

	deps, err := engine.TransitiveDeps(resourceID)
	if err != nil {
		return fmt.Errorf("computing dependencies: %w", err)
	}

	if len(deps) == 0 {
		fmt.Println("No dependencies found.")
		return nil
	}

	printNodeTable(deps)
	return nil
}

func runGraphImpact(_ *cobra.Command, args []string) error {
	resourceID := args[0]

	engine, err := openReadOnlyEngine()
	if err != nil {
		return fmt.Errorf("opening engine: %w", err)
	}
	defer func() { _ = engine.Close() }()

	dependents, err := engine.TransitiveDependents(resourceID)
	if err != nil {
		return fmt.Errorf("computing dependents: %w", err)
	}

	if len(dependents) == 0 {
		fmt.Println("No dependents found.")
		return nil
	}

	fmt.Printf("The following resources depend (transitively) on %s:\n", resourceID)
	printNodeTable(dependents)
	return nil
}

// printNodeTable prints a tabular list of GraphNodes.
func printNodeTable(nodes []orchestrate.GraphNode) {
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "ID\tKIND\tSTATUS")
	for _, n := range nodes {
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\n", n.ID, n.Kind, n.Status)
	}
	_ = w.Flush()
}

// printTextTree prints the graph as a text tree grouped by kind,
// showing dependency arrows for each resource.
func printTextTree(nodes []orchestrate.GraphNode, edges []orchestrate.GraphEdge) {
	// Build edge map: source → list of targets.
	edgeMap := make(map[string][]string)
	for _, e := range edges {
		edgeMap[e.SourceID] = append(edgeMap[e.SourceID], e.TargetID)
	}

	// Group nodes by kind.
	groups := make(map[string][]orchestrate.GraphNode)
	var kinds []string
	for _, n := range nodes {
		if _, exists := groups[n.Kind]; !exists {
			kinds = append(kinds, n.Kind)
		}
		groups[n.Kind] = append(groups[n.Kind], n)
	}
	sort.Strings(kinds)

	for i, kind := range kinds {
		if i > 0 {
			fmt.Println()
		}
		fmt.Println(kind)
		nodesInKind := groups[kind]
		sort.Slice(nodesInKind, func(a, b int) bool {
			return nodesInKind[a].ID < nodesInKind[b].ID
		})
		for _, n := range nodesInKind {
			targets := edgeMap[n.ID]
			if len(targets) > 0 {
				sort.Strings(targets)
				fmt.Printf("  %s -> %s\n", n.ID, strings.Join(targets, ", "))
			} else {
				fmt.Printf("  %s\n", n.ID)
			}
		}
	}
}

// printDOT emits a DOT digraph for graphviz piping.
func printDOT(nodes []orchestrate.GraphNode, edges []orchestrate.GraphEdge) {
	fmt.Println("digraph G {")
	fmt.Println("  rankdir=LR;")

	// Sort nodes for deterministic output.
	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].ID < nodes[j].ID
	})

	for _, n := range nodes {
		label := shortLabel(n.ID)
		fmt.Printf("  %q [label=%q];\n", n.ID, label+"\n("+n.Kind+")")
	}

	// Sort edges for deterministic output.
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].SourceID == edges[j].SourceID {
			return edges[i].TargetID < edges[j].TargetID
		}
		return edges[i].SourceID < edges[j].SourceID
	})

	for _, e := range edges {
		fmt.Printf("  %q -> %q;\n", e.SourceID, e.TargetID)
	}

	fmt.Println("}")
}

// shortLabel strips the project prefix from a resource ID for display.
func shortLabel(id string) string {
	if idx := strings.Index(id, "/"); idx >= 0 {
		return id[idx+1:]
	}
	return id
}
