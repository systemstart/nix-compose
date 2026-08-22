package cri

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/platforms"
)

// CRINamespace is the containerd namespace the CRI plugin reads images from.
// An image imported into any other namespace (containerd's default is
// "default") is invisible to crictl and to nix-compose.
const CRINamespace = "k8s.io"

// LocalImageDomain is the synthetic registry host under which Nix-built images
// are registered in containerd. It is never resolved against a real registry —
// EnsureImage imports these images directly and never calls PullImage for them.
const LocalImageDomain = "nix-compose.local"

// nixStoreDir is the prefix identifying a Nix store path. Store paths are
// content-addressed, which lets ImportLocalImage derive a stable, unique tag
// and skip re-importing an artifact the runtime already has.
const nixStoreDir = "/nix/store/"

// criImageSettleTimeout bounds how long ImportLocalImage waits for the CRI
// plugin's image store to observe the image it just wrote through containerd's
// native API. The two see the same data, but CRI learns about it from an event.
const criImageSettleTimeout = 10 * time.Second

// maxTagLength is the longest tag the OCI reference grammar permits.
const maxTagLength = 128

// LocalImage describes an image reference that points at an OCI artifact on the
// local filesystem — an OCI layout directory or an oci-archive tarball, as
// produced by nix-oci — rather than at a registry.
type LocalImage struct {
	// Path is the absolute path to the layout directory or archive.
	Path string
	// IsDir reports whether Path is an OCI layout directory rather than a tar.
	IsDir bool
	// Ref is the reference the image is registered under in containerd, and
	// the reference callers must use in pod and container configs.
	Ref string
	// ContentAddressed reports whether Ref is derived from a content hash. When
	// it is, an image already present under Ref is necessarily this exact
	// artifact, so the import can be skipped.
	ContentAddressed bool
}

// IsLocalImageRef reports whether ref names a filesystem path rather than a
// registry reference. Registry references are never absolute paths, so the
// leading slash is an unambiguous discriminator.
func IsLocalImageRef(ref string) bool {
	return strings.HasPrefix(ref, "/")
}

// ParseLocalImage inspects the OCI artifact at path and derives the containerd
// reference it will be registered under.
//
// For a Nix store path the reference is content-addressed — the store hash
// becomes the tag — so a rebuilt image always lands under a new reference and
// an unchanged one is a cheap no-op on re-up.
func ParseLocalImage(path string) (*LocalImage, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("cri: local image %s: %w", path, err)
	}

	li := &LocalImage{Path: path, IsDir: info.IsDir()}
	if li.IsDir {
		if _, err := os.Stat(filepath.Join(path, "oci-layout")); err != nil {
			return nil, fmt.Errorf("cri: local image %s: not an OCI layout directory (no oci-layout marker)", path)
		}
	}

	li.Ref = ResolvedImageRef(path)
	li.ContentAddressed = isContentAddressed(path)
	return li, nil
}

// ResolvedImageRef returns the reference the runtime will know ref by, without
// touching the filesystem or the runtime. For a registry reference that is ref
// itself; for a local artifact it is the reference ImportLocalImage registers,
// which is what ImageStatus must be asked about.
func ResolvedImageRef(ref string) string {
	if !IsLocalImageRef(ref) {
		return ref
	}
	name, tag := deriveLocalRef(ref)
	return LocalImageDomain + "/" + name + ":" + tag
}

// deriveLocalRef splits a path into the name and tag of its containerd
// reference. A Nix store path contributes its store hash as the tag; any other
// path gets the "latest" tag, and is re-imported on every up because nothing
// about the reference proves the bytes are unchanged.
func deriveLocalRef(path string) (name, tag string) {
	base := strings.TrimSuffix(filepath.Base(path), ".tar")

	if strings.HasPrefix(path, nixStoreDir) {
		// Store base names are "<32-char hash>-<name>".
		if hash, rest, ok := strings.Cut(base, "-"); ok && rest != "" {
			return sanitizeImageName(rest), sanitizeImageTag(hash)
		}
	}
	return sanitizeImageName(base), "latest"
}

// isRefNameChar reports whether r is allowed in an OCI repository name.
func isRefNameChar(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-'
}

// isRefTagChar reports whether r is allowed in an OCI tag, which unlike a
// repository name may be mixed case.
func isRefTagChar(r rune) bool {
	return isRefNameChar(r) || r >= 'A' && r <= 'Z'
}

// replaceDisallowed maps every rune outside allow onto a hyphen.
func replaceDisallowed(s string, allow func(rune) bool) string {
	var b strings.Builder
	for _, r := range s {
		if allow(r) {
			b.WriteRune(r)
			continue
		}
		b.WriteRune('-')
	}
	return b.String()
}

// sanitizeImageName maps a Nix derivation name onto the character set OCI
// repository names allow: lowercase alphanumerics with . _ - separators.
func sanitizeImageName(s string) string {
	out := strings.Trim(replaceDisallowed(strings.ToLower(s), isRefNameChar), "._-")
	if out == "" {
		return "image"
	}
	return out
}

// sanitizeImageTag maps a string onto the character set OCI tags allow.
func sanitizeImageTag(s string) string {
	out := replaceDisallowed(s, isRefTagChar)
	if out == "" || strings.HasPrefix(out, ".") || strings.HasPrefix(out, "-") {
		return "latest"
	}
	if len(out) > maxTagLength {
		out = out[:maxTagLength]
	}
	return out
}

// EnsureImage makes ref available to the CRI runtime and returns the reference
// the runtime knows it by — which is not ref itself when ref is a local path.
//
// A local OCI artifact is imported straight into containerd's content store, so
// a Nix-built image reaches the runtime with no registry and no daemon build.
// A registry reference is pulled as before, but only when ImageStatus says it
// is not already present.
func (c *Client) EnsureImage(ctx context.Context, ref string) (string, error) {
	if ref == "" {
		return "", errors.New("cri: empty image reference")
	}

	if !IsLocalImageRef(ref) {
		if c.imagePresent(ctx, ref) {
			return ref, nil
		}
		if err := c.PullImage(ctx, ref); err != nil {
			return "", err
		}
		return ref, nil
	}

	// A content-addressed reference already in the runtime is necessarily this
	// exact artifact, so this answers without touching the filesystem — which
	// also means a re-up still works when the store path has been collected.
	if resolved := ResolvedImageRef(ref); isContentAddressed(ref) && c.imagePresent(ctx, resolved) {
		return resolved, nil
	}

	local, err := ParseLocalImage(ref)
	if err != nil {
		return "", err
	}
	if err := c.ImportLocalImage(ctx, local); err != nil {
		return "", err
	}
	return local.Ref, nil
}

// isContentAddressed reports whether a local reference's derived tag pins the
// artifact's content, which is true exactly for Nix store paths.
func isContentAddressed(ref string) bool {
	return strings.HasPrefix(ref, nixStoreDir)
}

// imagePresent reports whether the runtime already has ref. A failed status
// probe is reported as "not present" rather than as an error: the probe only
// ever saves work, and both paths it guards are safe to run redundantly.
func (c *Client) imagePresent(ctx context.Context, ref string) bool {
	img, err := c.ImageStatus(ctx, ref)
	return err == nil && img != nil
}

// ImportLocalImage loads an OCI layout directory or oci-archive into containerd
// and registers it under local.Ref, bypassing the registry entirely.
//
// This is the import ADR-015 specifies. It speaks containerd's native API,
// which the CRI ImageService does not expose, so it opens a second connection
// to the same socket; runtimes without that API (CRI-O) are unsupported.
func (c *Client) ImportLocalImage(ctx context.Context, local *LocalImage) error {
	cc, err := containerd.New(c.socket, containerd.WithDefaultNamespace(CRINamespace))
	if err != nil {
		return fmt.Errorf("cri: containerd client on %s: %w", c.socket, err)
	}
	defer func() { _ = cc.Close() }()

	rc, err := openOCIArtifact(local)
	if err != nil {
		return err
	}
	defer func() { _ = rc.Close() }()

	// The archive names its manifests through the standard ref.name annotation;
	// translating every one of them to local.Ref is how ctr assigns a name on
	// import. Platform selection happens below, not here — the translator only
	// ever sees the annotation.
	imported, err := cc.Import(ctx, rc,
		containerd.WithImageRefTranslator(func(string) string { return local.Ref }),
		containerd.WithAllPlatforms(false),
	)
	if err != nil {
		return fmt.Errorf("cri: import %s: %w", local.Path, err)
	}

	img, err := selectPlatformImage(imported, local.Ref)
	if err != nil {
		return fmt.Errorf("cri: import %s: %w", local.Path, err)
	}

	// Import writes one record per manifest in the index, all under the same
	// name, so on a multi-platform artifact the last one written wins and may
	// be the wrong platform. Re-point the record at the manifest we selected.
	if _, err := cc.ImageService().Update(ctx, *img, "target"); err != nil {
		return fmt.Errorf("cri: register %s: %w", local.Ref, err)
	}

	// Unpack into the runtime's snapshotter; "" resolves to the daemon default.
	if err := containerd.NewImage(cc, *img).Unpack(ctx, ""); err != nil {
		return fmt.Errorf("cri: unpack %s: %w", local.Ref, err)
	}

	return c.awaitImage(ctx, local.Ref)
}

// selectPlatformImage picks the record matching the host platform from the set
// Import created under ref.
func selectPlatformImage(imported []images.Image, ref string) (*images.Image, error) {
	matcher := platforms.Default()
	var fallback *images.Image

	for i := range imported {
		if imported[i].Name != ref {
			continue
		}
		p := imported[i].Target.Platform
		if p == nil {
			// A descriptor with no platform is only a candidate of last resort.
			fallback = &imported[i]
			continue
		}
		if matcher.Match(*p) {
			return &imported[i], nil
		}
	}

	if fallback != nil {
		return fallback, nil
	}
	return nil, fmt.Errorf("no manifest for platform %s", platforms.DefaultString())
}

// awaitImage waits for the CRI plugin's image store to observe an image written
// through containerd's native API. CRI learns of it from an event, so it can
// lag the import by a moment.
func (c *Client) awaitImage(ctx context.Context, ref string) error {
	deadline := time.Now().Add(criImageSettleTimeout)
	for {
		if c.imagePresent(ctx, ref) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("cri: imported %s but the runtime does not report it after %s", ref, criImageSettleTimeout)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("cri: waiting for %s: %w", ref, ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// openOCIArtifact returns a tar stream of the artifact. An oci-archive is
// already one; a layout directory is tarred on the fly.
func openOCIArtifact(local *LocalImage) (io.ReadCloser, error) {
	if !local.IsDir {
		f, err := os.Open(local.Path)
		if err != nil {
			return nil, fmt.Errorf("cri: open %s: %w", local.Path, err)
		}
		return f, nil
	}

	pr, pw := io.Pipe()
	go func() {
		_ = pw.CloseWithError(tarDir(pw, local.Path))
	}()
	return pr, nil
}

// tarDir writes dir to w as a tar stream with paths relative to dir, which is
// the shape containerd's OCI archive importer expects.
func tarDir(w io.Writer, dir string) error {
	tw := tar.NewWriter(w)

	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("walk %s: %w", path, err)
		}
		return writeTarEntry(tw, dir, path, d)
	})
	if err == nil {
		err = tw.Close()
	}
	if err != nil {
		return fmt.Errorf("cri: tar OCI layout %s: %w", dir, err)
	}
	return nil
}

// writeTarEntry appends one directory entry to the stream, named relative to
// root. Anything that is not a directory or a regular file is rejected: an OCI
// layout has neither, and following one would put host bytes in the image.
func writeTarEntry(tw *tar.Writer, root, path string, d fs.DirEntry) error {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return fmt.Errorf("relative path for %s: %w", path, err)
	}
	if rel == "." {
		return nil
	}
	if !d.IsDir() && !d.Type().IsRegular() {
		return fmt.Errorf("unsupported file type in OCI layout: %s", path)
	}

	info, err := d.Info()
	if err != nil {
		return fmt.Errorf("stat %s: %w", path, err)
	}
	hdr, err := tar.FileInfoHeader(info, "")
	if err != nil {
		return fmt.Errorf("tar header for %s: %w", path, err)
	}
	hdr.Name = filepath.ToSlash(rel)
	if d.IsDir() {
		hdr.Name += "/"
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return fmt.Errorf("write tar header %s: %w", hdr.Name, err)
	}
	if d.IsDir() {
		return nil
	}
	return copyTarFile(tw, path)
}

// copyTarFile streams one file's bytes into the tar writer.
func copyTarFile(tw *tar.Writer, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	if _, err := io.Copy(tw, f); err != nil {
		return fmt.Errorf("copy %s: %w", path, err)
	}
	return nil
}
