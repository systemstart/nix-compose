package cri

import (
	"archive/tar"
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/platforms"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	runtimev1 "k8s.io/cri-api/pkg/apis/runtime/v1"
)

func TestIsLocalImageRef(t *testing.T) {
	tests := []struct {
		ref  string
		want bool
	}{
		{"/nix/store/wi0pgka6hq3nasng6kddpcadd6nvqh14-hello-oci", true},
		{"/tmp/hello-oci.tar", true},
		{"nginx:latest", false},
		{"docker.io/library/nginx:latest", false},
		{"registry.example.com:5000/team/app:1.0", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := IsLocalImageRef(tt.ref); got != tt.want {
			t.Errorf("IsLocalImageRef(%q) = %v, want %v", tt.ref, got, tt.want)
		}
	}
}

func TestResolvedImageRef(t *testing.T) {
	tests := []struct {
		name string
		ref  string
		want string
	}{
		{
			name: "registry ref is unchanged",
			ref:  "docker.io/library/nginx:latest",
			want: "docker.io/library/nginx:latest",
		},
		{
			name: "store path is tagged with its store hash",
			ref:  "/nix/store/wi0pgka6hq3nasng6kddpcadd6nvqh14-hello-oci",
			want: "nix-compose.local/hello-oci:wi0pgka6hq3nasng6kddpcadd6nvqh14",
		},
		{
			name: "archive suffix is dropped",
			ref:  "/nix/store/wdqpcr3m43m0rp58qn4727dngl0by37b-hello-oci.tar",
			want: "nix-compose.local/hello-oci:wdqpcr3m43m0rp58qn4727dngl0by37b",
		},
		{
			name: "non-store path gets the latest tag",
			ref:  "/tmp/build/myapp.tar",
			want: "nix-compose.local/myapp:latest",
		},
		{
			name: "uppercase is folded into the allowed charset",
			ref:  "/nix/store/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-MyApp",
			want: "nix-compose.local/myapp:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ResolvedImageRef(tt.ref); got != tt.want {
				t.Errorf("ResolvedImageRef(%q) = %q, want %q", tt.ref, got, tt.want)
			}
		})
	}
}

// writeLayout creates a minimal OCI layout directory for tests.
func writeLayout(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "blobs", "sha256"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	files := map[string]string{
		"oci-layout":            `{"imageLayoutVersion":"1.0.0"}`,
		"index.json":            `{"schemaVersion":2,"manifests":[]}`,
		"blobs/sha256/deadbeef": "layer-bytes",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
}

func TestParseLocalImage(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "hello-oci")
	writeLayout(t, dir)

	li, err := ParseLocalImage(dir)
	if err != nil {
		t.Fatalf("ParseLocalImage: %v", err)
	}
	if !li.IsDir {
		t.Error("expected IsDir for a layout directory")
	}
	if li.ContentAddressed {
		t.Error("a path outside the Nix store is not content-addressed")
	}
	if li.Ref != "nix-compose.local/hello-oci:latest" {
		t.Errorf("Ref = %q", li.Ref)
	}
}

func TestParseLocalImage_Archive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hello-oci.tar")
	if err := os.WriteFile(path, []byte("not really a tar"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	li, err := ParseLocalImage(path)
	if err != nil {
		t.Fatalf("ParseLocalImage: %v", err)
	}
	if li.IsDir {
		t.Error("expected IsDir false for an archive")
	}
}

func TestParseLocalImage_Errors(t *testing.T) {
	t.Run("missing path", func(t *testing.T) {
		if _, err := ParseLocalImage(filepath.Join(t.TempDir(), "absent")); err == nil {
			t.Fatal("expected an error for a path that does not exist")
		}
	})

	t.Run("directory without layout marker", func(t *testing.T) {
		dir := t.TempDir()
		_, err := ParseLocalImage(dir)
		if err == nil {
			t.Fatal("expected an error for a directory with no oci-layout marker")
		}
	})
}

func TestTarDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "layout")
	writeLayout(t, dir)

	var buf bytes.Buffer
	if err := tarDir(&buf, dir); err != nil {
		t.Fatalf("tarDir: %v", err)
	}

	found := map[string]string{}
	tr := tar.NewReader(&buf)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar read: %v", err)
		}
		if hdr.Typeflag == tar.TypeDir {
			continue
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			t.Fatalf("read %s: %v", hdr.Name, err)
		}
		found[hdr.Name] = string(body)
	}

	if got := found["oci-layout"]; got != `{"imageLayoutVersion":"1.0.0"}` {
		t.Errorf("oci-layout = %q", got)
	}
	// Paths must be relative to the layout root, not absolute host paths.
	if got, ok := found["blobs/sha256/deadbeef"]; !ok || got != "layer-bytes" {
		t.Errorf("blob entry = %q (present=%v)", got, ok)
	}
}

func TestTarDir_RejectsIrregularFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "layout")
	writeLayout(t, dir)
	if err := os.Symlink("/etc/passwd", filepath.Join(dir, "link")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	if err := tarDir(io.Discard, dir); err == nil {
		t.Fatal("expected an error for a symlink inside the layout")
	}
}

func TestSelectPlatformImage(t *testing.T) {
	host := platforms.DefaultSpec()
	other := ocispec.Platform{OS: "plan9", Architecture: "mips"}

	t.Run("picks the host platform", func(t *testing.T) {
		imported := []images.Image{
			{Name: "ref", Target: ocispec.Descriptor{Digest: "sha256:aaa", Platform: &other}},
			{Name: "ref", Target: ocispec.Descriptor{Digest: "sha256:bbb", Platform: &host}},
			{Name: "other-ref", Target: ocispec.Descriptor{Digest: "sha256:ccc", Platform: &host}},
		}
		img, err := selectPlatformImage(imported, "ref")
		if err != nil {
			t.Fatalf("selectPlatformImage: %v", err)
		}
		if img.Target.Digest != "sha256:bbb" {
			t.Errorf("selected %s, want sha256:bbb", img.Target.Digest)
		}
	})

	t.Run("wrong platform last still loses", func(t *testing.T) {
		// Import writes records in index order and the last write wins, so the
		// host manifest must be selected even when it is not last.
		imported := []images.Image{
			{Name: "ref", Target: ocispec.Descriptor{Digest: "sha256:bbb", Platform: &host}},
			{Name: "ref", Target: ocispec.Descriptor{Digest: "sha256:aaa", Platform: &other}},
		}
		img, err := selectPlatformImage(imported, "ref")
		if err != nil {
			t.Fatalf("selectPlatformImage: %v", err)
		}
		if img.Target.Digest != "sha256:bbb" {
			t.Errorf("selected %s, want sha256:bbb", img.Target.Digest)
		}
	})

	t.Run("falls back to an unlabelled descriptor", func(t *testing.T) {
		imported := []images.Image{
			{Name: "ref", Target: ocispec.Descriptor{Digest: "sha256:aaa"}},
		}
		img, err := selectPlatformImage(imported, "ref")
		if err != nil {
			t.Fatalf("selectPlatformImage: %v", err)
		}
		if img.Target.Digest != "sha256:aaa" {
			t.Errorf("selected %s", img.Target.Digest)
		}
	})

	t.Run("no match is an error", func(t *testing.T) {
		imported := []images.Image{
			{Name: "ref", Target: ocispec.Descriptor{Digest: "sha256:aaa", Platform: &other}},
		}
		if _, err := selectPlatformImage(imported, "ref"); err == nil {
			t.Fatal("expected an error when no manifest matches the host platform")
		}
	})
}

func TestEnsureImage_PullsWhenAbsent(t *testing.T) {
	sock, mock := startFullMockCRI(t)
	ctx := context.Background()
	c, err := Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = c.Close() }()

	ref, err := c.EnsureImage(ctx, "nginx:latest")
	if err != nil {
		t.Fatalf("EnsureImage: %v", err)
	}
	if ref != "nginx:latest" {
		t.Errorf("ref = %q, want nginx:latest", ref)
	}
	if _, ok := mock.images["nginx:latest"]; !ok {
		t.Error("expected the image to have been pulled")
	}
}

// TestEnsureImage_SkipsPullWhenPresent covers the bug that made a Nix-built
// image unusable even after it was imported: every up went to the registry.
func TestEnsureImage_SkipsPullWhenPresent(t *testing.T) {
	sock, mock := startFullMockCRI(t)
	ctx := context.Background()
	c, err := Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = c.Close() }()

	// Seed the image with a marker the mock's PullImage would overwrite.
	mock.images["nginx:latest"] = &runtimev1.Image{
		Id:       "sha256:preexisting",
		RepoTags: []string{"nginx:latest"},
		Spec:     &runtimev1.ImageSpec{Image: "nginx:latest"},
	}

	if _, err := c.EnsureImage(ctx, "nginx:latest"); err != nil {
		t.Fatalf("EnsureImage: %v", err)
	}
	if got := mock.images["nginx:latest"].Id; got != "sha256:preexisting" {
		t.Errorf("image was re-pulled: Id = %q", got)
	}
}

func TestEnsureImage_EmptyRef(t *testing.T) {
	sock, _ := startFullMockCRI(t)
	ctx := context.Background()
	c, err := Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = c.Close() }()

	if _, err := c.EnsureImage(ctx, ""); err == nil {
		t.Fatal("expected an error for an empty image reference")
	}
}

// TestEnsureImage_LocalSkipsRegistry asserts that a store path never reaches
// PullImage — the mock has no containerd API, so the import fails, but it must
// fail as an import and not as a registry pull.
func TestEnsureImage_LocalSkipsRegistry(t *testing.T) {
	sock, mock := startFullMockCRI(t)
	ctx := context.Background()
	c, err := Dial(ctx, sock)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = c.Close() }()

	dir := filepath.Join(t.TempDir(), "hello-oci")
	writeLayout(t, dir)

	if _, err := c.EnsureImage(ctx, dir); err == nil {
		t.Fatal("expected the import to fail against a mock with no containerd API")
	}
	if len(mock.images) != 0 {
		t.Errorf("a local artifact must not be pulled, got images %v", mock.images)
	}
}
