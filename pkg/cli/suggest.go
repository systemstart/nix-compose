package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/systemstart/nix-compose/pkg/composeimport"
	"github.com/systemstart/nix-compose/pkg/eval"
)

var suggestCmd = &cobra.Command{
	Use:   "suggest",
	Short: "Show which registry images could be built from nixpkgs instead",
	Long: `Show which registry images could be built from nixpkgs instead.

Reads the project's nix-compose.yaml and, for every service naming a registry
image, looks for a nixpkgs package of the same name. Nothing is changed — the
replacement is yours to make, one service at a time.`,
	RunE: runSuggest,
}

func runSuggest(cmd *cobra.Command, args []string) error {
	dir := projectDir()

	path := eval.FindYAMLFile(dir)
	if path == "" {
		return fmt.Errorf("no %s in %s — `suggest` reads a YAML project; "+
			"for a flake, set `package = pkgs.<name>;` directly",
			eval.YAMLFileNames[0], dir)
	}

	doc, _, err := eval.LoadYAML(path)
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}

	nixpkgsRef := eval.NixpkgsRefFor(doc)
	runner := &eval.ExecRunner{Dir: dir}

	suggestions, err := composeimport.Suggest(context.Background(), runner, nixpkgsRef, doc)
	if err != nil {
		return fmt.Errorf("looking up nixpkgs attributes: %w", err)
	}
	if len(suggestions) == 0 {
		fmt.Println("No services name a registry image — nothing to suggest.")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	matched := 0
	for _, s := range suggestions {
		if s.Attr == "" {
			_, _ = fmt.Fprintf(w, "%s\t%s\t→ no nixpkgs match, keeping registry image\n", s.Service, s.Image)
			continue
		}
		matched++
		var notes []string
		if s.MajorMismatch() {
			notes = append(notes, fmt.Sprintf(
				"major version %s → %s", s.TagMajor, s.PkgMajor))
		}
		if !s.MainProg {
			notes = append(notes, "needs an `entrypoint:` — no meta.mainProgram")
		}
		note := ""
		if len(notes) > 0 {
			note = "  ! " + strings.Join(notes, "; ")
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t→ package: %s\t(nixpkgs %s)%s\n",
			s.Service, s.Image, s.Attr, s.Version, note)
	}
	if err := w.Flush(); err != nil {
		return fmt.Errorf("writing suggestions: %w", err)
	}

	if matched == 0 {
		return nil
	}
	fmt.Printf("\n%d of %d services could stop pulling from a registry. "+
		"Replace `image:` with the `package:` line above — the two kinds mix, "+
		"so change one and run `nix-compose up` to check it.\n",
		matched, len(suggestions))

	return nil
}
