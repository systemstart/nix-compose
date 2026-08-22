package cri

import (
	"strings"
	"testing"

	"github.com/systemstart/nix-compose/pkg/eval"
)

func TestValidateImages_AcceptsImages(t *testing.T) {
	comp := &eval.Composition{Services: map[string]eval.Service{
		"web":   {Image: "nginx:latest"},
		"hello": {Image: "/nix/store/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-hello-oci"},
	}}
	if err := ValidateImages(comp); err != nil {
		t.Fatalf("ValidateImages: %v", err)
	}
}

// TestValidateImages_RejectsBuild is the error ADR-006 always promised: CRI has
// no build API, and the message has to say what to write instead.
func TestValidateImages_RejectsBuild(t *testing.T) {
	comp := &eval.Composition{Services: map[string]eval.Service{
		"web": {Build: &eval.BuildConfig{Context: "."}},
	}}

	err := ValidateImages(comp)
	if err == nil {
		t.Fatal("expected an error for a service using build:")
	}
	for _, want := range []string{`service "web"`, "services.web.package", "ociImage", "externally"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q, got: %v", want, err)
		}
	}
}

// TestValidateImages_BuildWithImageIsFine covers a service that has both: the
// image is usable, so the unusable build directive is not worth failing over.
func TestValidateImages_BuildWithImageIsFine(t *testing.T) {
	comp := &eval.Composition{Services: map[string]eval.Service{
		"web": {Image: "nginx:latest", Build: &eval.BuildConfig{Context: "."}},
	}}
	if err := ValidateImages(comp); err != nil {
		t.Fatalf("ValidateImages: %v", err)
	}
}

func TestValidateImages_RejectsMissingImage(t *testing.T) {
	comp := &eval.Composition{Services: map[string]eval.Service{
		"web": {},
	}}

	err := ValidateImages(comp)
	if err == nil {
		t.Fatal("expected an error for a service with no image")
	}
	if !strings.Contains(err.Error(), "no image") {
		t.Errorf("error = %v", err)
	}
}

// TestValidateImages_StableAcrossRuns keeps the reported service deterministic
// when several are broken — map iteration order must not leak into the message.
func TestValidateImages_StableAcrossRuns(t *testing.T) {
	comp := &eval.Composition{Services: map[string]eval.Service{
		"alpha": {},
		"beta":  {},
		"gamma": {},
	}}

	first := ValidateImages(comp)
	if first == nil {
		t.Fatal("expected an error")
	}
	for range 10 {
		if got := ValidateImages(comp); got.Error() != first.Error() {
			t.Fatalf("error varies between runs: %v vs %v", got, first)
		}
	}
	if !strings.Contains(first.Error(), "alpha") {
		t.Errorf("expected the first service by name, got: %v", first)
	}
}

func TestValidateImages_NilComposition(t *testing.T) {
	if err := ValidateImages(nil); err != nil {
		t.Fatalf("ValidateImages(nil): %v", err)
	}
}
