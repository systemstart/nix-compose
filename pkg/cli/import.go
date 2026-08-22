package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/systemstart/nix-compose/pkg/composeimport"
	"github.com/systemstart/nix-compose/pkg/eval"
)

var (
	importOutput string
	importForce  bool
)

// composeFileNames are the names docker compose itself looks for, in its own
// precedence order.
var composeFileNames = []string{
	"compose.yaml",
	"compose.yml",
	"docker-compose.yaml",
	"docker-compose.yml",
}

var importCmd = &cobra.Command{
	Use:   "import [compose-file]",
	Short: "Convert a docker-compose file into a nix-compose.yaml",
	Long: `Convert a docker-compose file into a nix-compose.yaml.

With no argument, looks for compose.yaml, compose.yml, docker-compose.yaml or
docker-compose.yml in the project directory.

The conversion is one-way and lossy. Anything that does not survive it is
reported, and written into the generated file as a comment, so nothing is lost
quietly. Services keep their registry images; run 'nix-compose suggest'
afterwards to see which of them could be built from a Nix closure instead.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runImport,
}

func init() {
	importCmd.Flags().StringVarP(&importOutput, "output", "o", "",
		"write to this path instead of <project>/nix-compose.yaml")
	importCmd.Flags().BoolVar(&importForce, "force", false,
		"overwrite the output file if it already exists")
}

func runImport(cmd *cobra.Command, args []string) error {
	dir := projectDir()

	source, err := resolveComposeSource(dir, args)
	if err != nil {
		return err
	}

	output := importOutput
	if output == "" {
		output = filepath.Join(dir, eval.YAMLFileNames[0])
	}
	if !importForce {
		if _, err := os.Stat(output); err == nil {
			return fmt.Errorf("%s already exists — pass --force to overwrite it, "+
				"or --output to write somewhere else", output)
		}
	}

	data, err := os.ReadFile(source)
	if err != nil {
		return fmt.Errorf("reading %s: %w", source, err)
	}

	res, err := composeimport.Convert(data)
	if err != nil {
		return fmt.Errorf("converting %s: %w", source, err)
	}

	rendered, err := composeimport.Render(res, filepath.Base(source))
	if err != nil {
		return fmt.Errorf("rendering %s: %w", output, err)
	}
	if err := os.WriteFile(output, rendered, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", output, err)
	}

	fmt.Printf("wrote %s (%d services)\n", output, len(res.Services))
	if report := composeimport.NotesReport(res); report != "" {
		fmt.Print(report)
	}
	fmt.Println("\nNext: `nix-compose suggest` — which images have a nixpkgs equivalent")

	return nil
}

// resolveComposeSource picks the compose file to read: the one named on the
// command line, or the first of docker compose's own default names.
func resolveComposeSource(dir string, args []string) (string, error) {
	if len(args) == 1 {
		if _, err := os.Stat(args[0]); err != nil {
			return "", fmt.Errorf("reading %s: %w", args[0], err)
		}
		return args[0], nil
	}

	for _, name := range composeFileNames {
		path := filepath.Join(dir, name)
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}

	return "", fmt.Errorf("no compose file found in %s (looked for %v) — "+
		"pass one as an argument", dir, composeFileNames)
}
