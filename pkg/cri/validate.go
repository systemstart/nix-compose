package cri

import (
	"fmt"
	"sort"

	"github.com/systemstart/nix-compose/pkg/eval"
)

// ValidateImages checks that every service names an image the CRI backend can
// actually obtain, and fails with a message that says what to write instead.
//
// CRI has no build API (ADR-006), so a `build:` directive cannot be honoured.
// Since ADR-015 the answer is no longer "go build it elsewhere and push it":
// name the package and nix-compose builds the image from its closure.
func ValidateImages(comp *eval.Composition) error {
	if comp == nil {
		return nil
	}

	names := make([]string, 0, len(comp.Services))
	for name := range comp.Services {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		if err := validateServiceImage(name, comp.Services[name]); err != nil {
			return err
		}
	}
	return nil
}

func validateServiceImage(name string, svc eval.Service) error {
	if svc.Image != "" {
		return nil
	}
	if svc.Build != nil {
		return fmt.Errorf("service %q uses `build:`, which the CRI backend cannot honour — "+
			"CRI has no build API. Build the image from a Nix closure instead:\n\n"+
			"    services.%s.package = pkgs.<package>;\n\n"+
			"or pass a prebuilt one with `services.%s.ociImage`. Both need the composition "+
			"to go through nix-compose's `mkComposition`. To keep a Dockerfile build, run "+
			"it externally and reference the resulting image by tag", name, name, name)
	}
	return fmt.Errorf("service %q has no image — set `image`, `package`, or `ociImage`", name)
}
