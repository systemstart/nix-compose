// Package builder automates building the NixOS microVM image via nix build.
package builder

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/systemstart/nix-compose/pkg/eval"
)

// ImagePaths holds the resolved paths to the kernel and rootfs produced
// by a successful nix build of the microVM image.
type ImagePaths struct {
	Kernel string
	RootFS string
}

// Builder invokes nix build to produce the microVM image closure.
type Builder struct {
	// Runner executes commands (reuses the existing eval.CommandRunner interface).
	Runner eval.CommandRunner

	// FlakeRef is the flake reference to build from. When empty, the
	// builder uses "." (current directory).
	FlakeRef string

	// FlakeAttr is the flake output attribute to build. Defaults to
	// "microvm-image" if empty.
	FlakeAttr string
}

// Build runs nix build and returns the resolved kernel and rootfs paths.
func (b *Builder) Build(ctx context.Context) (*ImagePaths, error) {
	ref := b.FlakeRef
	if ref == "" {
		ref = "."
	}
	attr := b.FlakeAttr
	if attr == "" {
		attr = "microvm-image"
	}

	installable := fmt.Sprintf("%s#%s", ref, attr)
	args := []string{"build", installable, "--no-link", "--print-out-paths"}

	stdout, stderr, err := b.Runner.Run(ctx, "nix", args...)
	if err != nil {
		msg := strings.TrimSpace(string(stderr))
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("nix build failed: %s", msg)
	}

	outPath := strings.TrimSpace(string(bytes.Split(stdout, []byte("\n"))[0]))
	if outPath == "" {
		return nil, fmt.Errorf("nix build produced no output path")
	}

	kernelPath := filepath.Join(outPath, "kernel")
	rootfsPath := filepath.Join(outPath, "rootfs")

	// Resolve symlinks to the actual store paths.
	kernelResolved, err := filepath.EvalSymlinks(kernelPath)
	if err != nil {
		return nil, fmt.Errorf("resolving kernel path: %w", err)
	}
	rootfsResolved, err := filepath.EvalSymlinks(rootfsPath)
	if err != nil {
		return nil, fmt.Errorf("resolving rootfs path: %w", err)
	}

	// Validate both paths exist.
	if _, err := os.Stat(kernelResolved); err != nil {
		return nil, fmt.Errorf("kernel not found at %s: %w", kernelResolved, err)
	}
	if _, err := os.Stat(rootfsResolved); err != nil {
		return nil, fmt.Errorf("rootfs not found at %s: %w", rootfsResolved, err)
	}

	return &ImagePaths{
		Kernel: kernelResolved,
		RootFS: rootfsResolved,
	}, nil
}
