package cli

import (
	"fmt"

	"github.com/systemstart/nix-compose/pkg/eval"
)

// announceEval says what is about to happen, which is not the same thing in
// every mode. A YAML project without any `package:` never runs nix at all, and
// claiming otherwise makes the tool look slower and more Nix-bound than it is
// — which is the whole impression YAML mode exists to correct.
func announceEval(dir string) {
	mode, err := eval.DetectMode(dir)
	if err != nil {
		// Let the evaluator report the real problem.
		fmt.Println("Evaluating configuration...")
		return
	}

	if mode != eval.ModeYAML {
		fmt.Println("Evaluating Nix configuration...")
		return
	}

	path := eval.FindYAMLFile(dir)
	if _, usesNix, err := eval.LoadYAML(path); err == nil && usesNix {
		fmt.Println("Reading nix-compose.yaml, building packages with Nix...")
		return
	}
	fmt.Println("Reading nix-compose.yaml...")
}
