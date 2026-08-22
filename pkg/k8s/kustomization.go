package k8s

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// writeKustomization generates a kustomization.yaml listing the given resource files.
func writeKustomization(dir string, filenames []string) error {
	k := Kustomization{
		APIVersion: "kustomize.config.k8s.io/v1beta1",
		Kind:       "Kustomization",
		Resources:  filenames,
	}

	data, err := yaml.Marshal(k)
	if err != nil {
		return fmt.Errorf("marshaling kustomization: %w", err)
	}

	path := filepath.Join(dir, "kustomization.yaml")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("writing kustomization.yaml: %w", err)
	}
	return nil
}
