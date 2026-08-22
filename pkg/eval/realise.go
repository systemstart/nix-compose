package eval

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
)

// NixStorePrefix is the store directory a locally-built image path lives under.
const NixStorePrefix = "/nix/store/"

// RealiseImages builds the images of services that name a package rather than
// a registry tag.
//
// Evaluating a composition yields each image's store *path* without building
// anything — `nix eval` never builds — so a fresh checkout produces a path that
// does not exist yet. The derivation behind it is written to the store during
// that same evaluation, which is what makes it realisable here: an output path
// on its own cannot be built ("no substituter that can build it"), but its
// derivation can.
//
// Services whose image is already present, or that name a registry tag, cost
// nothing.
func RealiseImages(ctx context.Context, runner CommandRunner, comp *Composition) error {
	drvs := pendingImageDrvs(comp)
	if len(drvs) == 0 {
		return nil
	}

	args := append([]string{"--realise"}, drvs...)
	if _, stderr, err := runner.Run(ctx, "nix-store", args...); err != nil {
		return fmt.Errorf("building service images: %s: %w", strings.TrimSpace(string(stderr)), err)
	}
	return nil
}

// pendingImageDrvs returns the derivations for images that are not in the store
// yet, sorted and deduplicated so one Nix invocation covers them all.
func pendingImageDrvs(comp *Composition) []string {
	if comp == nil {
		return nil
	}

	seen := make(map[string]struct{})
	for _, svc := range comp.Services {
		if svc.XNixCompose == nil || svc.XNixCompose.ImageDrv == "" {
			continue
		}
		if !strings.HasPrefix(svc.Image, NixStorePrefix) {
			continue
		}
		if _, err := os.Stat(svc.Image); err == nil {
			continue
		}
		seen[svc.XNixCompose.ImageDrv] = struct{}{}
	}

	drvs := make([]string, 0, len(seen))
	for drv := range seen {
		drvs = append(drvs, drv)
	}
	sort.Strings(drvs)
	return drvs
}
