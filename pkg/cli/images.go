package cli

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/systemstart/nix-compose/pkg/cri"
	"github.com/systemstart/nix-compose/pkg/eval"
	runtimev1 "k8s.io/cri-api/pkg/apis/runtime/v1"
)

var imagesCmd = &cobra.Command{
	Use:   "images [service...]",
	Short: "List images used by services",
	RunE:  runImages,
}

func runImages(_ *cobra.Command, args []string) error {
	ctx := context.Background()
	criClient, err := requireCRI(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = criClient.Close() }()
	return doImagesCRI(ctx, criClient, args)
}

// doImagesCRI lists images via CRI, filtered to those used by the composition.
func doImagesCRI(ctx context.Context, client *cri.Client, args []string) error {
	dir := projectDir()
	runner := &eval.ExecRunner{Dir: dir}
	evaluator := &eval.Evaluator{
		Runner:     runner,
		ProjectDir: dir,
		FlakeAttr:  flagFlakeAttr,
		Impure:     flagImpure,
	}

	comp, _, err := evaluator.Eval(ctx)
	if err != nil {
		return fmt.Errorf("evaluation failed: %w", err)
	}

	wanted := serviceImages(comp, args)
	wantedImages := make(map[string]bool, len(wanted))
	for _, img := range wanted {
		// Match on the reference the runtime knows: a Nix-built image is
		// declared as a store path but tagged nix-compose.local/… once imported.
		wantedImages[cri.ResolvedImageRef(img)] = true
	}

	allImages, err := client.ListImages(ctx)
	if err != nil {
		return fmt.Errorf("list images: %w", err)
	}

	printImageTable(matchingImageRows(allImages, wantedImages))
	return nil
}

// imageRow is one line of the image table: a single repository/tag pointing at
// an image.
type imageRow struct {
	repo string
	tag  string
	id   string
	size uint64
}

// matchingImageRows expands the runtime's images into one row per matching
// reference. An image can carry several references — two services built from
// identical closures produce byte-identical images, and containerd stores those
// as one record under both tags — so rows are per reference, not per image.
func matchingImageRows(images []*runtimev1.Image, wanted map[string]bool) []imageRow {
	var rows []imageRow
	for _, img := range images {
		for _, ref := range img.RepoTags {
			if !wanted[ref] {
				continue
			}
			repo, tag := splitRepoTag(ref)
			rows = append(rows, imageRow{repo: repo, tag: tag, id: shortID(img.Id), size: img.Size})
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].repo != rows[j].repo {
			return rows[i].repo < rows[j].repo
		}
		return rows[i].tag < rows[j].tag
	})
	return rows
}

func printImageTable(rows []imageRow) {
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "REPOSITORY\tTAG\tIMAGE ID\tSIZE")
	for _, r := range rows {
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", r.repo, r.tag, r.id, formatSize(r.size))
	}
	_ = w.Flush()
}

// splitRepoTag splits an image reference into its repository and tag.
func splitRepoTag(ref string) (repo, tag string) {
	if idx := strings.LastIndex(ref, ":"); idx != -1 {
		return ref[:idx], ref[idx+1:]
	}
	return ref, "latest"
}

func shortID(id string) string {
	// Strip "sha256:" prefix and truncate to 12 chars.
	id = strings.TrimPrefix(id, "sha256:")
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

func formatSize(bytes uint64) string {
	const (
		kb = 1024
		mb = kb * 1024
		gb = mb * 1024
	)
	switch {
	case bytes >= gb:
		return fmt.Sprintf("%.1fGB", float64(bytes)/float64(gb))
	case bytes >= mb:
		return fmt.Sprintf("%.1fMB", float64(bytes)/float64(mb))
	case bytes >= kb:
		return fmt.Sprintf("%.1fKB", float64(bytes)/float64(kb))
	default:
		return fmt.Sprintf("%dB", bytes)
	}
}
