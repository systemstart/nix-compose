package eval

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// recordingRunner captures the commands it is asked to run.
type recordingRunner struct {
	calls [][]string
	err   error
}

func (r *recordingRunner) Run(_ context.Context, name string, args ...string) ([]byte, []byte, error) {
	r.calls = append(r.calls, append([]string{name}, args...))
	if r.err != nil {
		return nil, []byte("nix-store: build failed"), r.err
	}
	return nil, nil, nil
}

func svcWithDrv(image, drv string) Service {
	return Service{
		Image:       image,
		XNixCompose: &NixComposeExtended{ImageDrv: drv},
	}
}

func TestRealiseImages_BuildsMissingImages(t *testing.T) {
	comp := &Composition{Services: map[string]Service{
		"web": svcWithDrv("/nix/store/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-web-oci", "/nix/store/w.drv"),
	}}

	runner := &recordingRunner{}
	if err := RealiseImages(context.Background(), runner, comp); err != nil {
		t.Fatalf("RealiseImages: %v", err)
	}

	if len(runner.calls) != 1 {
		t.Fatalf("expected 1 nix invocation, got %d: %v", len(runner.calls), runner.calls)
	}
	want := []string{"nix-store", "--realise", "/nix/store/w.drv"}
	got := runner.calls[0]
	if len(got) != len(want) {
		t.Fatalf("call = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("call = %v, want %v", got, want)
		}
	}
}

// TestRealiseImages_SkipsPresentImages keeps a re-up from rebuilding an image
// whose store path is already there.
func TestRealiseImages_SkipsPresentImages(t *testing.T) {
	// A path that exists but is not really in the store still exercises the
	// stat check, which is all this branch depends on.
	present := filepath.Join(t.TempDir(), "image")
	if err := os.WriteFile(present, []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	comp := &Composition{Services: map[string]Service{
		"web":   svcWithDrv(present, "/nix/store/w.drv"),
		"store": svcWithDrv(NixStorePrefix+"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb-db-oci", "/nix/store/db.drv"),
	}}

	runner := &recordingRunner{}
	if err := RealiseImages(context.Background(), runner, comp); err != nil {
		t.Fatalf("RealiseImages: %v", err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("expected 1 invocation, got %v", runner.calls)
	}
	// Only the absent store path is built; "web" points at an existing file,
	// and it is not a store path either.
	last := runner.calls[0]
	if last[len(last)-1] != "/nix/store/db.drv" {
		t.Errorf("built %v, want only /nix/store/db.drv", last)
	}
}

func TestRealiseImages_NoWorkIsNoInvocation(t *testing.T) {
	comp := &Composition{Services: map[string]Service{
		"web": {Image: "nginx:latest"},
		"db":  {Image: "postgres:16", XNixCompose: &NixComposeExtended{}},
	}}

	runner := &recordingRunner{}
	if err := RealiseImages(context.Background(), runner, comp); err != nil {
		t.Fatalf("RealiseImages: %v", err)
	}
	if len(runner.calls) != 0 {
		t.Errorf("expected no nix invocation for registry images, got %v", runner.calls)
	}
}

// TestRealiseImages_DedupesAndSorts covers services sharing one image: the
// derivation must be passed once, and the argument order must be stable.
func TestRealiseImages_DedupesAndSorts(t *testing.T) {
	comp := &Composition{Services: map[string]Service{
		"a": svcWithDrv(NixStorePrefix+"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-a-oci", "/nix/store/zzz.drv"),
		"b": svcWithDrv(NixStorePrefix+"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb-b-oci", "/nix/store/aaa.drv"),
		"c": svcWithDrv(NixStorePrefix+"cccccccccccccccccccccccccccccccc-c-oci", "/nix/store/zzz.drv"),
	}}

	for range 5 {
		runner := &recordingRunner{}
		if err := RealiseImages(context.Background(), runner, comp); err != nil {
			t.Fatalf("RealiseImages: %v", err)
		}
		got := runner.calls[0][1:]
		if len(got) != 3 {
			t.Fatalf("args = %v, want --realise plus 2 unique drvs", got)
		}
		if got[1] != "/nix/store/aaa.drv" || got[2] != "/nix/store/zzz.drv" {
			t.Fatalf("args = %v, want sorted unique drvs", got)
		}
	}
}

func TestRealiseImages_ReportsBuildFailure(t *testing.T) {
	comp := &Composition{Services: map[string]Service{
		"web": svcWithDrv(NixStorePrefix+"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-web-oci", "/nix/store/w.drv"),
	}}

	runner := &recordingRunner{err: os.ErrPermission}
	err := RealiseImages(context.Background(), runner, comp)
	if err == nil {
		t.Fatal("expected an error when the build fails")
	}
	if !strings.Contains(err.Error(), "nix-store: build failed") {
		t.Errorf("error should carry the build output, got: %v", err)
	}
}
